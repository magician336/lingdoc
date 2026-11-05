package evidence

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type parsedOriginDBReader struct {
	db      *gorm.DB
	file    string
	readErr error
	afterIO func()
}

func (r parsedOriginDBReader) KnowledgeForOrigin(ctx context.Context, id string) (*types.Knowledge, error) {
	var row types.Knowledge
	err := r.db.WithContext(ctx).Where("id=? AND deleted_at IS NULL", id).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &row, err
}
func (r parsedOriginDBReader) UsesBuiltinConverter(context.Context, string) (bool, error) {
	return false, nil
}
func (r parsedOriginDBReader) ParsedOriginForKnowledge(ctx context.Context, row *types.Knowledge) (*ParsedOriginSnapshot, error) {
	return NewParsedOriginStore(r.db).ReadCurrent(ctx, row)
}
func (r parsedOriginDBReader) OriginBytes(ctx context.Context, id string) ([]byte, bool, error) {
	if r.readErr != nil {
		return nil, false, r.readErr
	}
	// Like the real resource reader, resolve the file through the same DB handle.
	if _, err := r.KnowledgeForOrigin(ctx, id); err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(r.file)
	if r.afterIO != nil {
		r.afterIO()
	}
	return data, err == nil, err
}

func newParsedOriginFixture(t *testing.T, kind string, migrate bool) (*gorm.DB, parsedOriginDBReader, ParsedOriginSnapshot) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "origin.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	rawDB, err := db.DB()
	require.NoError(t, err)
	rawDB.SetMaxOpenConns(1)
	rawDB.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = rawDB.Close() })
	require.NoError(t, db.Exec(`CREATE TABLE knowledges(id TEXT PRIMARY KEY,tenant_id INTEGER,file_path TEXT,file_hash TEXT,file_type TEXT,file_size INTEGER,parse_status TEXT,deleted_at DATETIME)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE knowledge_processing_spans(knowledge_id TEXT,attempt INTEGER)`).Error)
	if migrate {
		require.NoError(t, NewParsedOriginStore(db).AutoMigrate(context.Background()))
	}
	data := []byte("original container bytes for " + kind)
	file := filepath.Join(t.TempDir(), "actual."+kind)
	require.NoError(t, os.WriteFile(file, data, 0600))
	row := &types.Knowledge{ID: "parsed-document", TenantID: 7, FilePath: "resource://actual-document", FileHash: "stored-file-identity", FileSize: int64(len(data)), FileType: kind, ParseStatus: types.ParseStatusProcessing}
	require.NoError(t, db.Exec("INSERT INTO knowledges(id,tenant_id,file_path,file_hash,file_type,file_size,parse_status) VALUES(?,?,?,?,?,?,?)", row.ID, row.TenantID, row.FilePath, row.FileHash, row.FileType, row.FileSize, row.ParseStatus).Error)
	require.NoError(t, db.Exec("INSERT INTO knowledge_processing_spans VALUES(?,1)", row.ID).Error)
	snapshot := CaptureParsedOriginInput(row, row.FilePath, kind, 1, data)
	snapshot.SetChunkerText("# 解析标题\n\n这是送入切分器的最终原文。\n另一个段落。", map[string]string{"scanned_page_count": "0", "image_source_type": "pdf_text_layer"})
	return db, parsedOriginDBReader{db: db, file: file}, snapshot
}

func completeParsedOrigin(t *testing.T, db *gorm.DB, snapshot ParsedOriginSnapshot) {
	t.Helper()
	saved, err := NewParsedOriginStore(db).Save(context.Background(), snapshot)
	require.NoError(t, err)
	require.True(t, saved)
	require.NoError(t, db.Exec("UPDATE knowledges SET parse_status='completed' WHERE id=?", snapshot.KnowledgeID).Error)
}

func TestParsedOriginSnapshotPersistsAndUsesSingleTransactionReader(t *testing.T) {
	for _, kind := range []string{"pdf", "docx"} {
		t.Run(kind, func(t *testing.T) {
			db, reader, snapshot := newParsedOriginFixture(t, kind, true)
			saved, err := NewParsedOriginStore(db).Save(context.Background(), snapshot)
			require.NoError(t, err)
			require.True(t, saved)
			_, ok, err := NewOriginReader(reader).OriginText(context.Background(), snapshot.KnowledgeID)
			require.NoError(t, err)
			require.False(t, ok, "a saved parser result is not yet a completed ingestion")
			require.NoError(t, db.Exec("UPDATE knowledges SET parse_status='completed'").Error)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			require.NoError(t, db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				reader.db = tx
				text, ok, err := NewOriginReader(reader).OriginText(ctx, snapshot.KnowledgeID)
				require.NoError(t, err)
				require.True(t, ok)
				require.Equal(t, snapshot.Text, text)
				return nil
			}))
			var persisted ParsedOriginSnapshot
			require.NoError(t, db.Where("knowledge_id=?", snapshot.KnowledgeID).Take(&persisted).Error)
			require.Equal(t, snapshot.OriginalSHA256, persisted.OriginalSHA256)
			require.Equal(t, snapshot.TextSHA256, persisted.TextSHA256)
		})
	}
}

func TestParsedOriginRejectsChangedOrCorruptFacts(t *testing.T) {
	for _, scenario := range []string{"bytes", "path", "file-hash", "text", "attempt", "processing", "scanned", "unknown-pdf", "during-io"} {
		t.Run(scenario, func(t *testing.T) {
			db, reader, snapshot := newParsedOriginFixture(t, "pdf", true)
			if scenario == "scanned" {
				snapshot.SetChunkerText(snapshot.Text, map[string]string{"scanned_page_count": "1", "image_source_type": "scanned_pdf"})
			}
			if scenario == "unknown-pdf" {
				snapshot.SetChunkerText(snapshot.Text, nil)
			}
			completeParsedOrigin(t, db, snapshot)
			switch scenario {
			case "bytes":
				require.NoError(t, os.WriteFile(reader.file, []byte(strings.Repeat("x", int(snapshot.OriginalSize))), 0600))
			case "path":
				require.NoError(t, db.Exec("UPDATE knowledges SET file_path='resource://replacement'").Error)
			case "file-hash":
				require.NoError(t, db.Exec("UPDATE knowledges SET file_hash='changed'").Error)
			case "text":
				require.NoError(t, db.Model(&ParsedOriginSnapshot{}).Where("knowledge_id=?", snapshot.KnowledgeID).Update("text", "损坏的快照正文").Error)
			case "attempt":
				require.NoError(t, db.Exec("INSERT INTO knowledge_processing_spans VALUES(?,2)", snapshot.KnowledgeID).Error)
			case "processing":
				require.NoError(t, db.Exec("UPDATE knowledges SET parse_status='processing'").Error)
			case "during-io":
				reader.afterIO = func() {
					require.NoError(t, db.Exec("INSERT INTO knowledge_processing_spans VALUES(?,2)", snapshot.KnowledgeID).Error)
				}
			}
			text, ok, err := NewOriginReader(reader).OriginText(context.Background(), snapshot.KnowledgeID)
			require.NoError(t, err)
			require.False(t, ok)
			require.Empty(t, text)
		})
	}
}

func TestParsedOriginOldWorkerCannotOverwriteNewAttempt(t *testing.T) {
	db, _, old := newParsedOriginFixture(t, "docx", true)
	saved, err := NewParsedOriginStore(db).Save(context.Background(), old)
	require.NoError(t, err)
	require.True(t, saved)
	require.NoError(t, db.Exec("INSERT INTO knowledge_processing_spans VALUES(?,2)", old.KnowledgeID).Error)
	stale := old
	stale.SetChunkerText("旧任务稍后返回的不同正文", nil)
	_, err = NewParsedOriginStore(db).Save(context.Background(), stale)
	require.ErrorIs(t, err, ErrParsedOriginSuperseded)
	var persisted ParsedOriginSnapshot
	require.NoError(t, db.Where("knowledge_id=? AND attempt=1", old.KnowledgeID).Take(&persisted).Error)
	require.Equal(t, old.Text, persisted.Text)
	latest := old
	latest.Attempt = 2
	latest.SetChunkerText("当前解析轮次的最终正文", nil)
	saved, err = NewParsedOriginStore(db).Save(context.Background(), latest)
	require.NoError(t, err)
	require.True(t, saved)
	var count int64
	require.NoError(t, db.Model(&ParsedOriginSnapshot{}).Where("knowledge_id=?", old.KnowledgeID).Count(&count).Error)
	require.EqualValues(t, 2, count, "new attempts keep their own snapshot instead of replacing history")
}

func TestParsedOriginMissingFeatureAndInfrastructureErrors(t *testing.T) {
	t.Run("optional-table-absent", func(t *testing.T) {
		db, reader, snapshot := newParsedOriginFixture(t, "pdf", false)
		saved, err := NewParsedOriginStore(db).Save(context.Background(), snapshot)
		require.NoError(t, err)
		require.False(t, saved)
		require.NoError(t, db.Exec("UPDATE knowledges SET parse_status='completed'").Error)
		_, ok, err := NewOriginReader(reader).OriginText(context.Background(), snapshot.KnowledgeID)
		require.NoError(t, err)
		require.False(t, ok)
		require.False(t, db.Migrator().HasTable(&ParsedOriginSnapshot{}))
	})
	for _, scenario := range []string{"storage", "database"} {
		t.Run(scenario, func(t *testing.T) {
			db, reader, snapshot := newParsedOriginFixture(t, "docx", true)
			completeParsedOrigin(t, db, snapshot)
			if scenario == "storage" {
				reader.readErr = errors.New("resource store unavailable")
			} else {
				require.NoError(t, db.Exec("DROP TABLE knowledge_processing_spans").Error)
			}
			_, ok, err := NewOriginReader(reader).OriginText(context.Background(), snapshot.KnowledgeID)
			require.Error(t, err, "infrastructure failure must not be reported as weak evidence")
			require.False(t, ok)
		})
	}
}
