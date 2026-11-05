package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const parsedOriginTable = "lingdoc_parsed_origin_snapshots"

var ErrParsedOriginSuperseded = errors.New("parsed origin input or attempt superseded")

// ParsedOriginSnapshot is the exact post-sanitization, post-image-resolution
// input given to the chunker. It does not reconstruct or reparse a document.
type ParsedOriginSnapshot struct {
	TenantID         uint64    `gorm:"primaryKey;autoIncrement:false"`
	KnowledgeID      string    `gorm:"primaryKey;size:36"`
	Attempt          int       `gorm:"primaryKey;autoIncrement:false"`
	FilePath         string    `gorm:"not null;type:text"`
	FileHash         string    `gorm:"not null;type:text"`
	FileType         string    `gorm:"not null;size:32"`
	FileSize         int64     `gorm:"not null"`
	OriginalSize     int64     `gorm:"not null"`
	OriginalSHA256   string    `gorm:"not null;size:64"`
	Text             string    `gorm:"not null;type:text"`
	TextSHA256       string    `gorm:"not null;size:64"`
	ScannedPageCount int       `gorm:"not null"`
	ImageSourceType  string    `gorm:"not null;size:128"`
	Verifiable       bool      `gorm:"not null"`
	CreatedAt        time.Time `gorm:"not null"`
}

func (ParsedOriginSnapshot) TableName() string { return parsedOriginTable }

// These optional ports keep repositories without the local snapshot table
// compatible. No read or write path creates or migrates a table implicitly.
type ParsedOriginWriter interface {
	SaveParsedOrigin(context.Context, ParsedOriginSnapshot) (bool, error)
}

type ParsedOriginReader interface {
	ParsedOriginForKnowledge(context.Context, *types.Knowledge) (*ParsedOriginSnapshot, error)
}

func SupportsParsedOrigin(fileType string) bool {
	ext := normalizeExtension(fileType)
	return ext == "pdf" || ext == "docx"
}

func parsedOriginHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// CaptureParsedOriginInput must receive the actual bytes passed to DocReader,
// never a digest copied from parser output or user-controlled metadata.
func CaptureParsedOriginInput(row *types.Knowledge, path, fileType string, attempt int, data []byte) ParsedOriginSnapshot {
	if row == nil || !SupportsParsedOrigin(fileType) {
		return ParsedOriginSnapshot{}
	}
	return ParsedOriginSnapshot{
		TenantID: row.TenantID, KnowledgeID: row.ID, Attempt: attempt,
		FilePath: path, FileHash: row.FileHash, FileType: normalizeExtension(fileType), FileSize: row.FileSize,
		OriginalSize: int64(len(data)), OriginalSHA256: parsedOriginHash(data),
	}
}

func (snapshot *ParsedOriginSnapshot) SetChunkerText(text string, metadata map[string]string) {
	snapshot.ScannedPageCount = 0
	if raw := metadata["scanned_page_count"]; raw != "" {
		count, err := strconv.Atoi(raw)
		if err != nil || count < 0 {
			snapshot.ScannedPageCount = -1
		} else {
			snapshot.ScannedPageCount = count
		}
	}
	snapshot.ImageSourceType = metadata["image_source_type"]
	snapshot.Verifiable = snapshot.OriginalSize > 0 && snapshot.OriginalSize <= maxOriginBytes &&
		len(text) > 0 && len(text) <= maxOriginBytes && utf8.ValidString(text) && !strings.ContainsRune(text, 0) &&
		!strings.Contains(text, "\r") && strings.TrimSpace(text) != "" && snapshot.ScannedPageCount == 0 &&
		!strings.Contains(strings.ToLower(snapshot.ImageSourceType), "scanned") && !strings.Contains(strings.ToLower(snapshot.ImageSourceType), "ocr")
	// The current PDF reader explicitly reports whether its text came from a
	// native text layer. An absent/unknown scanner report is not proof of that.
	if snapshot.FileType == "pdf" && (metadata["scanned_page_count"] == "" || snapshot.ImageSourceType != "pdf_text_layer") {
		snapshot.Verifiable = false
	}
	if len(text) > maxOriginBytes {
		text = ""
	}
	snapshot.Text, snapshot.TextSHA256 = text, parsedOriginHash([]byte(text))
}

type ParsedOriginStore struct{ db *gorm.DB }

func NewParsedOriginStore(db *gorm.DB) *ParsedOriginStore { return &ParsedOriginStore{db: db} }

func (s *ParsedOriginStore) AutoMigrate(ctx context.Context) error {
	return s.db.WithContext(ctx).AutoMigrate(&ParsedOriginSnapshot{})
}

func (s *ParsedOriginStore) available(ctx context.Context) (bool, error) {
	// Probe the catalog instead of querying a possibly absent table. Catching a
	// missing-table error would already have aborted a PostgreSQL transaction.
	var available bool
	var query string
	switch s.db.Dialector.Name() {
	case "sqlite":
		query = "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)"
	case "postgres":
		query = "SELECT to_regclass(?) IS NOT NULL"
	case "mysql":
		query = "SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=?)"
	default:
		// This optional local feature is not installed on unknown dialects.
		return false, nil
	}
	err := s.db.WithContext(ctx).Raw(query, parsedOriginTable).Scan(&available).Error
	return available, err
}

func parsedOriginAttempt(ctx context.Context, db *gorm.DB, knowledgeID string) (int, error) {
	var attempt int
	err := db.WithContext(ctx).Model(&types.KnowledgeProcessingSpan{}).Where("knowledge_id=?", knowledgeID).
		Select("COALESCE(MAX(attempt),0)").Scan(&attempt).Error
	return attempt, err
}

func (snapshot ParsedOriginSnapshot) matches(row *types.Knowledge) bool {
	return row != nil && !row.DeletedAt.Valid && snapshot.KnowledgeID == row.ID && snapshot.TenantID == row.TenantID &&
		snapshot.FilePath == row.FilePath && snapshot.FileHash == row.FileHash && snapshot.FileSize == row.FileSize &&
		snapshot.FileType == normalizeExtension(row.FileType)
}

// Save runs before chunking. A late worker may not publish over a newer parse
// attempt, a replacement file, or an already completed/cancelled ingestion.
func (s *ParsedOriginStore) Save(ctx context.Context, snapshot ParsedOriginSnapshot) (bool, error) {
	available, err := s.available(ctx)
	if err != nil || !available {
		return false, err
	}
	if snapshot.TenantID == 0 || snapshot.KnowledgeID == "" || snapshot.Attempt < 1 || snapshot.FilePath == "" ||
		!SupportsParsedOrigin(snapshot.FileType) || len(snapshot.OriginalSHA256) != 64 ||
		len(snapshot.Text) > maxOriginBytes || parsedOriginHash([]byte(snapshot.Text)) != snapshot.TextSHA256 {
		return false, errors.New("invalid parsed origin snapshot")
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row types.Knowledge
		if err := tx.Where("id=? AND tenant_id=? AND deleted_at IS NULL", snapshot.KnowledgeID, snapshot.TenantID).Take(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrParsedOriginSuperseded
			}
			return err
		}
		attempt, err := parsedOriginAttempt(ctx, tx, row.ID)
		if err != nil {
			return err
		}
		if !snapshot.matches(&row) || row.ParseStatus != types.ParseStatusProcessing || attempt != snapshot.Attempt {
			return ErrParsedOriginSuperseded
		}
		snapshot.CreatedAt = time.Now().UTC()
		return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "knowledge_id"}, {Name: "attempt"}},
			DoUpdates: clause.AssignmentColumns([]string{"file_path", "file_hash", "file_type", "file_size", "original_size", "original_sha256", "text", "text_sha256", "scanned_page_count", "image_source_type", "verifiable", "created_at"})}).Create(&snapshot).Error
	})
	return err == nil, err
}

// ReadCurrent returns no strong original until processing is completed and the
// exact attempt, file identity and stored text digest still match. Real storage
// errors are returned, while an uninstalled optional table simply stays weak.
func (s *ParsedOriginStore) ReadCurrent(ctx context.Context, row *types.Knowledge) (*ParsedOriginSnapshot, error) {
	if row == nil || row.ParseStatus != types.ParseStatusCompleted || row.DeletedAt.Valid || !SupportsParsedOrigin(row.FileType) {
		return nil, nil
	}
	available, err := s.available(ctx)
	if err != nil || !available {
		return nil, err
	}
	var snapshot ParsedOriginSnapshot
	err = s.db.WithContext(ctx).Where("knowledge_id=? AND tenant_id=?", row.ID, row.TenantID).Order("attempt DESC").Take(&snapshot).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !snapshot.matches(row) || !snapshot.Verifiable || snapshot.ScannedPageCount != 0 || snapshot.OriginalSize <= 0 || snapshot.OriginalSize > maxOriginBytes ||
		len(snapshot.Text) == 0 || len(snapshot.Text) > maxOriginBytes || !utf8.ValidString(snapshot.Text) ||
		parsedOriginHash([]byte(snapshot.Text)) != snapshot.TextSHA256 {
		return nil, nil
	}
	if snapshot.FileType == "pdf" && snapshot.ImageSourceType != "pdf_text_layer" {
		return nil, nil
	}
	attempt, err := parsedOriginAttempt(ctx, s.db, row.ID)
	if err != nil {
		return nil, err
	}
	if attempt != snapshot.Attempt {
		return nil, nil
	}
	return &snapshot, nil
}

func (r *fileOriginReader) parsedOrigin(ctx context.Context, row *types.Knowledge) (string, bool, error) {
	reader, supported := r.knowledge.(ParsedOriginReader)
	bytesReader, haveBytes := r.knowledge.(OriginBytesReader)
	if !supported || !haveBytes {
		return "", false, nil
	}
	snapshot, err := reader.ParsedOriginForKnowledge(ctx, row)
	if err != nil || snapshot == nil {
		return "", false, err
	}
	data, available, err := bytesReader.OriginBytes(ctx, row.ID)
	if err != nil || !available {
		return "", false, err
	}
	if len(data) > maxOriginBytes || int64(len(data)) != snapshot.OriginalSize || parsedOriginHash(data) != snapshot.OriginalSHA256 {
		return "", false, nil
	}
	// Recheck after IO: a new attempt or file replacement must not make an old
	// snapshot appear current merely because the original bytes are unchanged.
	latest, err := r.knowledge.KnowledgeForOrigin(ctx, row.ID)
	if err != nil || latest == nil {
		return "", false, err
	}
	current, err := reader.ParsedOriginForKnowledge(ctx, latest)
	if err != nil || current == nil {
		return "", false, err
	}
	if current.Attempt != snapshot.Attempt || current.TextSHA256 != snapshot.TextSHA256 || current.OriginalSHA256 != snapshot.OriginalSHA256 || !current.matches(latest) {
		return "", false, nil
	}
	return snapshot.Text, true, nil
}
