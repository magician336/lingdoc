package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/infrastructure/chunker"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type parsedOriginFiles struct{ interfaces.FileService }

func (parsedOriginFiles) GetFile(_ context.Context, path string) (io.ReadCloser, error) {
	return os.Open(path)
}

type parsedOriginTenantService struct{ interfaces.TenantService }

func (parsedOriginTenantService) GetWeKnoraCloudCredentials(context.Context) *types.WeKnoraCloudCredentials {
	return nil
}

type parsedOriginDocumentReader struct {
	interfaces.DocumentReader
	seen []byte
}

func (r *parsedOriginDocumentReader) Read(_ context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	r.seen = append([]byte(nil), req.FileContent...)
	return &types.ReadResult{MarkdownContent: "解析\x00正文\r\n第二行", Metadata: map[string]string{
		"scanned_page_count": "0", "image_source_type": "pdf_text_layer",
		"original_sha256": "untrusted-parser-hash", "text_sha256": "untrusted-text-hash", "parse_attempt": "999",
	}}, nil
}

func TestKnowledgeParsedOriginCapturesActualInputAndFinalChunkerText(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "origin.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	rawDB, err := db.DB()
	require.NoError(t, err)
	rawDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = rawDB.Close() })
	require.NoError(t, db.Exec(`CREATE TABLE knowledges(id TEXT PRIMARY KEY,tenant_id INTEGER,file_path TEXT,file_hash TEXT,file_type TEXT,file_size INTEGER,parse_status TEXT,deleted_at DATETIME)`).Error)
	require.NoError(t, db.Exec(`CREATE TABLE knowledge_processing_spans(knowledge_id TEXT,attempt INTEGER)`).Error)
	require.NoError(t, evidence.NewParsedOriginStore(db).AutoMigrate(context.Background()))
	data := []byte("%PDF-1.7 actual bytes sent to the existing converter")
	file := filepath.Join(t.TempDir(), "native.pdf")
	require.NoError(t, os.WriteFile(file, data, 0600))
	knowledge := &types.Knowledge{ID: "origin-document", TenantID: 7, FilePath: file, FileType: "pdf", FileHash: "file-record-hash", FileSize: int64(len(data)), ParseStatus: types.ParseStatusProcessing}
	require.NoError(t, db.Exec("INSERT INTO knowledges VALUES(?,?,?,?,?,?,?,NULL)", knowledge.ID, knowledge.TenantID, knowledge.FilePath, knowledge.FileHash, knowledge.FileType, knowledge.FileSize, knowledge.ParseStatus).Error)
	require.NoError(t, db.Exec("INSERT INTO knowledge_processing_spans VALUES(?,3)", knowledge.ID).Error)
	reader := &parsedOriginDocumentReader{}
	service := &knowledgeService{repo: repository.NewKnowledgeRepository(db), fileSvc: parsedOriginFiles{}, documentReader: reader, tenantService: parsedOriginTenantService{}}
	ctx := withAttempt(context.Background(), 3)
	var captured evidence.ParsedOriginSnapshot
	result, err := service.convert(ctx, types.DocumentProcessPayload{FilePath: file, FileName: "native.pdf", FileType: "pdf"}, &types.KnowledgeBase{ID: "kb"}, knowledge, types.EffectiveProcessConfig{}, true, &captured)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, data, reader.seen)
	digest := sha256.Sum256(data)
	require.Equal(t, hex.EncodeToString(digest[:]), captured.OriginalSHA256)
	require.Equal(t, 3, captured.Attempt)
	// Model the final in-process image rewrite and the actual normalization
	// immediately before chunking; the saved body must be this final string.
	result.MarkdownContent += "\r\n最终图片替换后的文本"
	sanitizeReadResult(result)
	result.MarkdownContent = chunker.NormalizeLineEndings(result.MarkdownContent)
	require.NoError(t, service.saveParsedOrigin(ctx, captured, result))
	var saved evidence.ParsedOriginSnapshot
	require.NoError(t, db.Where("knowledge_id=?", knowledge.ID).Take(&saved).Error)
	require.Equal(t, "解析正文\n第二行\n最终图片替换后的文本", saved.Text)
	require.Equal(t, captured.OriginalSHA256, saved.OriginalSHA256)
	require.Equal(t, 3, saved.Attempt)
	require.NotEqual(t, result.Metadata["text_sha256"], saved.TextSHA256)
	require.True(t, saved.Verifiable)
}

type failingParsedOriginRepository struct {
	interfaces.KnowledgeRepository
	err error
}

func (r failingParsedOriginRepository) SaveParsedOrigin(context.Context, evidence.ParsedOriginSnapshot) (bool, error) {
	return false, r.err
}

func TestKnowledgeParsedOriginHookPreservesOptionalPortAndErrors(t *testing.T) {
	input := evidence.CaptureParsedOriginInput(&types.Knowledge{ID: "k", TenantID: 7, FilePath: "resource://k", FileType: "docx"}, "resource://k", "docx", 1, []byte("actual docx bytes"))
	result := &types.ReadResult{MarkdownContent: "最终解析正文"}
	service := &knowledgeService{}
	require.NoError(t, service.saveParsedOrigin(context.Background(), input, result), "legacy repositories without the capability remain supported")
	want := errors.New("snapshot database unavailable")
	service.repo = failingParsedOriginRepository{err: want}
	require.ErrorIs(t, service.saveParsedOrigin(context.Background(), input, result), want)
	textInput := evidence.CaptureParsedOriginInput(&types.Knowledge{ID: "txt"}, "original.txt", "txt", 1, []byte("text"))
	require.NoError(t, service.saveParsedOrigin(context.Background(), textInput, result), "direct text keeps its existing path")
}
