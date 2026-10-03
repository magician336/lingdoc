package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/Tencent/WeKnora/internal/evidence"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrRewriteInvalidRequest = errors.New("selected_rewrite_invalid_request")
	ErrRewriteNotFound       = errors.New("selected_rewrite_not_found")
	ErrRewriteStale          = errors.New("selected_rewrite_stale")
	ErrRewriteConflict       = errors.New("selected_rewrite_conflict")
	ErrRewriteUnavailable    = errors.New("selected_rewrite_unavailable")
	ErrRewriteSourceDenied   = errors.New("selected_rewrite_source_denied")
	ErrRewriteKeyConflict    = errors.New("selected_rewrite_idempotency_conflict")
)

type RewriteSelection struct {
	StartUTF16   int    `json:"start_utf16"`
	EndUTF16     int    `json:"end_utf16"`
	SelectedText string `json:"selected_text"`
}

type SelectedRewriteRequest struct {
	BaseChapterVersionID        *string          `json:"base_chapter_version_id"`
	ExpectedSpecRevision        int64            `json:"expected_spec_revision"`
	ExpectedWorkingCopyRevision int64            `json:"expected_working_copy_revision"`
	Selection                   RewriteSelection `json:"selection"`
	Instruction                 string           `json:"instruction"`
	SourceIDs                   []string         `json:"source_ids"`
}

type RewriteSourceSnapshot struct {
	SourceID       string    `json:"source_id"`
	AssetID        string    `json:"asset_id"`
	AssetRevision  int       `json:"asset_revision"`
	Locator        string    `json:"locator"`
	QuotedTextHash string    `json:"quoted_text_hash"`
	AuthorizedAt   time.Time `json:"authorized_at"`
}

type SelectedRewriteCandidate struct {
	CandidateID          string                  `json:"candidate_id"`
	ProjectID            string                  `json:"project_id"`
	ChapterID            string                  `json:"chapter_id"`
	Status               string                  `json:"status"`
	RunMode              string                  `json:"run_mode"`
	BaseChapterVersionID *string                 `json:"base_chapter_version_id"`
	SpecRevision         int64                   `json:"spec_revision"`
	WorkingCopyRevision  int64                   `json:"working_copy_revision"`
	Selection            RewriteSelection        `json:"selection"`
	ReplacementMarkdown  string                  `json:"replacement_markdown"`
	SourceIDs            []string                `json:"source_ids"`
	AuthorizedSources    []RewriteSourceSnapshot `json:"authorized_sources"`
	ReviewItems          []core.ReviewItem       `json:"review_items"`
	ErrorCode            string                  `json:"error_code"`
	CreatedAt            time.Time               `json:"created_at"`
}

type SelectedRewritePrompt struct {
	Selection     RewriteSelection
	Instruction   string
	ContextBefore string
	ContextAfter  string
	Sources       []evidence.Source
}

type SelectedRewriteOutput struct {
	ReplacementMarkdown string   `json:"replacement_markdown"`
	SourceIDs           []string `json:"source_ids"`
	ReviewItems         []string `json:"review_items"`
	RunMode             string   `json:"-"`
}

type SelectedRewriteModel interface {
	RunMode() string
	RewriteSelected(context.Context, Actor, SelectedRewritePrompt) (SelectedRewriteOutput, error)
}

type SelectedRewriteApplication interface {
	Create(context.Context, Actor, string, string, string, SelectedRewriteRequest) (SelectedRewriteCandidate, error)
	Get(context.Context, Actor, string, string) (SelectedRewriteCandidate, error)
	Apply(context.Context, Actor, string, string, string, string, ApplySelectedRewriteInput) (core.WorkingCopy, bool, error)
}

type ApplySelectedRewriteInput struct {
	ExpectedSpecRevision        int64   `json:"expected_spec_revision"`
	ExpectedWorkingCopyRevision int64   `json:"expected_working_copy_revision"`
	BaseChapterVersionID        *string `json:"base_chapter_version_id"`
}

type selectedRewriteRow struct {
	ID                    string  `gorm:"primaryKey;size:36"`
	TenantID              uint64  `gorm:"not null;uniqueIndex:idx_lingdoc_rewrite_idempotency,priority:1"`
	ActorID               string  `gorm:"not null;size:64;uniqueIndex:idx_lingdoc_rewrite_idempotency,priority:2"`
	ProjectID             string  `gorm:"not null;size:36;index;uniqueIndex:idx_lingdoc_rewrite_idempotency,priority:3"`
	ChapterID             string  `gorm:"not null;size:36;index;uniqueIndex:idx_lingdoc_rewrite_idempotency,priority:4"`
	IdempotencyKey        string  `gorm:"not null;size:128;uniqueIndex:idx_lingdoc_rewrite_idempotency,priority:5"`
	RequestHash           string  `gorm:"not null;size:64"`
	Status                string  `gorm:"not null;size:16;index"`
	RunMode               string  `gorm:"not null;size:32"`
	BaseChapterVersionID  *string `gorm:"size:36"`
	SpecRevision          int64   `gorm:"not null"`
	WorkingCopyRevision   int64   `gorm:"not null"`
	StartUTF16            int     `gorm:"not null"`
	EndUTF16              int     `gorm:"not null"`
	SelectedText          string  `gorm:"not null;type:text"`
	Instruction           string  `gorm:"not null;size:2000"`
	ReplacementMarkdown   string  `gorm:"not null;type:text"`
	SourceIDsJSON         string  `gorm:"not null;type:text"`
	AuthorizedSourcesJSON string  `gorm:"not null;type:text"`
	ReviewItemsJSON       string  `gorm:"not null;type:text"`
	ErrorCode             string  `gorm:"not null;size:64;default:''"`
	AppliedKey            string  `gorm:"not null;size:128;default:''"`
	AppliedResponseJSON   string  `gorm:"not null;type:text;default:''"`
	ApplyPendingKey       string  `gorm:"not null;size:128;default:''"`
	ApplyPendingInputJSON string  `gorm:"not null;type:text;default:''"`
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

func (selectedRewriteRow) TableName() string { return "lingdoc_selected_rewrites" }

type GORMSelectedRewriteStore struct{ db *gorm.DB }

func NewGORMSelectedRewriteStore(db *gorm.DB) *GORMSelectedRewriteStore {
	return &GORMSelectedRewriteStore{db: db}
}

func (s *GORMSelectedRewriteStore) AutoMigrate(ctx context.Context) error {
	return s.db.WithContext(ctx).AutoMigrate(&selectedRewriteRow{})
}

func (s *GORMSelectedRewriteStore) createOrReplay(ctx context.Context, row selectedRewriteRow) (selectedRewriteRow, bool, error) {
	created := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "tenant_id"}, {Name: "actor_id"},
			{Name: "project_id"}, {Name: "chapter_id"}, {Name: "idempotency_key"}}, DoNothing: true}).Create(&row)
		if result.Error != nil {
			return result.Error
		}
		created = result.RowsAffected == 1
		if created {
			return nil
		}
		var existing selectedRewriteRow
		if err := tx.Where("tenant_id = ? AND actor_id = ? AND project_id = ? AND chapter_id = ? AND idempotency_key = ?",
			row.TenantID, row.ActorID, row.ProjectID, row.ChapterID, row.IdempotencyKey).First(&existing).Error; err != nil {
			return err
		}
		if existing.RequestHash != row.RequestHash {
			return ErrRewriteKeyConflict
		}
		row = existing
		return nil
	})
	return row, created, err
}

func (s *GORMSelectedRewriteStore) byID(ctx context.Context, tenantID uint64, projectID, chapterID, candidateID string) (selectedRewriteRow, error) {
	var row selectedRewriteRow
	query := s.db.WithContext(ctx).Where("tenant_id = ? AND project_id = ? AND id = ?", tenantID, projectID, candidateID)
	if chapterID != "" {
		query = query.Where("chapter_id = ?", chapterID)
	}
	err := query.First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return selectedRewriteRow{}, ErrRewriteNotFound
	}
	return row, err
}

func (s *GORMSelectedRewriteStore) byOperation(ctx context.Context, tenantID uint64, projectID, chapterID, key string) (selectedRewriteRow, error) {
	var ro