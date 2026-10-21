package candidateadoption

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
)

// SQLiteCandidateAdoptionStore owns the T11 candidate and adoption records
// while reusing the T08 workspace tables for projects, chapters, and versions.
type SQLiteCandidateAdoptionStore struct {
	db             *gorm.DB
	focusedSchema  bool // Enabled only by the focused-test AutoMigrate helper.
	evidencePolicy CandidateEvidencePolicy
	authorizer     Authorizer
}

func NewSQLiteCandidateAdoptionStore(db *gorm.DB) *SQLiteCandidateAdoptionStore {
	return &SQLiteCandidateAdoptionStore{db: db}
}

func (s *SQLiteCandidateAdoptionStore) DB() *gorm.DB { return s.db }

type projectRow struct {
	ID                     string `gorm:"primaryKey;size:36"`
	TenantID               uint64 `gorm:"not null;index"`
	Name                   string `gorm:"not null;size:120"`
	Status                 string `gorm:"not null;size:16"`
	ProjectVersion         int64  `gorm:"not null"`
	SpecRevision           int64  `gorm:"not null"`
	SpecJSON               string `gorm:"column:spec_json;not null;type:text"`
	TemplateID             string `gorm:"not null;size:80"`
	TemplateVersion        string `gorm:"not null;size:40"`
	TemplateCopyVersion    int64  `gorm:"column:template_copy_version;not null;default:0"`
	CurrentContextRevision int64  `gorm:"column:current_context_revision;not null;default:0"`
	CreatedAt              time.Time
}

type projectTemplateCopyRow struct {
	ID             string `gorm:"primaryKey;size:36"`
	ProjectID      string `gorm:"not null;size:36"`
	Version        int64  `gorm:"not null"`
	ContentHash    string `gorm:"not null;size:64"`
	RulesetHash    string `gorm:"not null;size:64"`
	Status         string `gorm:"not null;size:16"`
	DefinitionJSON string `gorm:"column:definition_json;not null;type:text"`
}

func (projectTemplateCopyRow) TableName() string { return "lingdoc_project_template_copies" }

type chapterRow struct {
	ID               string  `gorm:"primaryKey;size:36"`
	ProjectID        string  `gorm:"not null;index;size:36"`
	SectionID        string  `gorm:"not null;size:80"`
	Title            string  `gorm:"not null;size:120"`
	CurrentVersionID *string `gorm:"size:36"`
}

type chapterVersionRow struct {
	ID                 string  `gorm:"primaryKey;size:36"`
	ProjectID          string  `gorm:"not null;index;size:36"`
	ChapterID          string  `gorm:"not null;index;size:36"`
	ParentVersionID    *string `gorm:"size:36"`
	CandidateID        string  `gorm:"index;size:36"`
	BodyMarkdown       string  `gorm:"not null;type:text"`
	SourceIDsJSON      string  `gorm:"column:source_ids_json;not null;type:text"`
	CitationUsagesJSON string  `gorm:"column:citation_usages_json;not null;type:text"`
	ReviewItemsJSON    string  `gorm:"column:review_items_json;not null;type:text"`
	SpecRevision       int64   `gorm:"not null;default:0"`
	ConfirmationValid  bool    `gorm:"not null;default:false"`
	CreatedBy          string  `gorm:"not null;default:'';size:128"`
	CreatedAt          time.Time
}

type confirmationRow struct {
	ID               string `gorm:"primaryKey;size:36"`
	ChapterID        string `gorm:"index;not null;size:36"`
	ChapterVersionID string `gorm:"index;not null;size:36"`
	Valid            bool   `gorm:"not null;default:false"`
	DetailsJSON      string `gorm:"type:text;not null;default:'{}'"`
	CreatedAt        time.Time
}

type candidateRow struct {
	ID              string `gorm:"primaryKey;size:36"`
	ProjectID       string `gorm:"index;not null;size:36"`
	ChapterID       string `gorm:"index;not null;size:36"`
	RunID           string `gorm:"not null;size:128"`
	BodyMarkdown    string `gorm:"type:text;not null"`
	SourceIDsJSON   string `gorm:"type:text;not null"`
	BasisJSON       string `gorm:"type:text;not null"`
	Validity        string `gorm:"not null;size:16"`
	ReviewItemsJSON string `gorm:"type:text;not null"`
	WritingAction   string `gorm:"column:writing_action;not null;size:64;default:''"`
	Origin          string `gorm:"column:origin;not null;size:32;default:''"`
	RunMode         string `gorm:"column:run_mode;not null;size:32;default:''"`
	ModelID         string `gorm:"column:model_id;not null;size:128;default:''"`
	CreatedAt       time.Time
}

type projectAssetRow struct {
	ID            string     `gorm:"primaryKey;size:36"`
	ProjectID     string     `gorm:"index;not null;size:36"`
	AssetRevision int        `gorm:"not null;default:1"`
	DeletedAt     *time.Time `gorm:"index"`
}

type adoptionIdempotencyRow struct {
	ID               string `gorm:"primaryKey;size:36"`
	ProjectID        string `gorm:"index;not null;size:36"`
	ChapterID        string `gorm:"index;not null;size:36"`
	ActorID          string `gorm:"index;not null;size:128"`
	Key              string `gorm:"column:idempotency_key;not null;size:128"`
	RequestHash      string `gorm:"not null;size:64"`
	DigestVersion    int    `gorm:"not null;default:1"`
	ChapterVersionID string `gorm:"not null;size:36"`
	ResponseJSON     string `gorm:"type:text;not null"`
	CreatedAt        time.Time
}

type confirmationIdempotencyRow struct {
	ID           string `gorm:"primaryKey;size:36"`
	ProjectID    string `gorm:"uniqueIndex:uq_lingdoc_confirmation_request,priority:1;not null;size:36"`
	ChapterID    string `gorm:"uniqueIndex:uq_lingdoc_confirmation_request,priority:2;not null;size:36"`
	ActorID      string `gorm:"uniqueIndex:uq_lingdoc_confirmation_request,priority:3;not null;size:128"`
	Key          string `gorm:"column:idempotency_key;uniqueIndex:uq_lingdoc_confirmation_request,priority:4;not null;size:128"`
	RequestHash  string `gorm:"not null;size:64"`
	ResponseJSON string `gorm:"type:text;not null"`
	CreatedAt    time.Time
}

// projectAuditRow mirrors the workspace audit table without importing
// workspacecore (which would create a package cycle). The table is shared by
// the workspace and candidate-adoption transactions; keeping this small row
// local lets a chapter re-confirmation appear in the same project audit feed.
type projectAuditRow struct {
	ID          string `gorm:"primaryKey;size:36"`
	ProjectID   string `gorm:"not null;index;size:36"`
	ActorID     string `gorm:"not null;size:64"`
	Action      string `gorm:"not null;size:64"`
	Target      string `gorm:"not null;size:128"`
	DetailsJSON string `gorm:"column:details_json;not null;type:text;default:'{}'"`
	CreatedAt   time.Time
}

func (projectAuditRow) TableName() string { return "lingdoc_project_audits" }

func (projectRow) TableName() string                 { return "lingdoc_projects" }
func (chapterRow) TableName() string                 { return "lingdoc_chapters" }
func (chapterVersionRow) TableName() string          { return "lingdoc_chapter_versions" }
func (confirmationRow) TableName() string            { return "lingdoc_chapter_confirmations" }
func (candidateRow) TableName() string               { return "lingdoc_candidates" }
func (projectAssetRow) TableName() string            { return "lingdoc_project_assets" }
func (adoptionIdempotencyRow) TableName() string     { return "lingdoc_candidate_adoptions" }
func (confirmationIdempotencyRow) TableName() string { return "lingdoc_chapter_confirmation_requests" }

// AutoMigrate is used by focused store tests. Production startup uses the
// numbered migrations, where 000018 owns workspace tables and 000020 adds the
// T11 columns and records.
func (s *SQLiteCandidateAdoptionStore) AutoMigrate(ctx context.Context) error {
	db := s.db.WithContext(ctx)
	// This helper creates isolated test tables only. Existing T08/native tables
	// belong to numbered migrations: a partial projection must never rebuild
	// them or change their tenant constraints, columns, defaults or indexes.
	for _, model := range []any{&projectRow{}, &chapterRow{}, &chapterVersionRow{}, &projectAssetRow{}, &candidateRow{}, &confirmationRow{}, &adoptionIdempotencyRow{}, &confirmationIdempotencyRow{}} {
		if db.Migrator().HasTable(model) {
			continue
		}
		if err := db.Migrator().CreateTable(model); err != nil {
			return err
		}
	}
	s.focusedSchema = true
	return nil
}

// ConfirmChapter persists the confirmation and idempotent response atomically.
// It rechecks the current chapter and project revisions inside the write
// transaction so a concurrent edit cannot be confirmed from a stale read.
func (s *SQLiteCandidateAdoptionStore) ReplayConfirmation(ctx context.Context, in ConfirmChapterInput) (*Confirmation, error) {
	var previous confirmationIdempotencyRow
	err := s.db.WithContext(ctx).Where("project_id = ? AND chapter_id = ? AND actor_id = ? AND idempotency_key = ?",
		in.ProjectID, in.ChapterID, in.ActorID, in.IdempotencyKey).First(&previous).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if previous.RequestHash != confirmationRequestHash(in) {
		return nil, ErrIdempotencyConflict
	}
	var result Confirmation
	if err := json.Unmarshal([]byte(previous.ResponseJSON), &result); err != nil {
		return nil, fmt.Errorf("decode confirmation idempotency response: %w", err)
	}
	return &result, nil
}

func (s *SQLiteCandidateAdoptionStore) ConfirmChapter(ctx context.Context, in ConfirmChapterInput, workspace GenerationContext) (Confirmation, bool, error) {
	var result Confirmation
	replayed := false
	hash := confirmationRequestHash(in)
	// Candidate-adoption unit tests intentionally use a focused schema without
	// the workspace audit table. Production migrations do create it, so detect
	// the optional table once and write the review event only when it exists.
	auditEnabled := s.db.Migrator().HasTable((projectAuditRow{}).TableName())
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var previous confirmationIdempotencyRow
		err := tx.Where("project_id = ? AND chapter_id = ? AND actor_id = ? AND idempotency_key = ?", in.ProjectID, in.ChapterID, in.ActorID, in.IdempotencyKey).First(&previous).Error
		if err == nil {
			if previous.RequestHash != hash {
				return ErrIdempotencyConflict
			}
			if err := json.Unmarshal([]byte(previous.ResponseJSON), &result); err != nil {
				return err
			}
			replayed = true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var project projectRow
		if err := tx.Where("id = ?", in.ProjectID).First(&project).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		var chapter chapterRow
		if err := tx.Where("id = ? AND project_id = ?", in.ChapterID, in.ProjectID).First(&chapter).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if chapter.CurrentVersionID == nil || *chapter.CurrentVersionID != in.ExpectedChapterVersionID ||
			int(project.SpecRevision) != in.ExpectedSpecRevision || project.TemplateVersion != workspace.Basis.TemplateVersion {
			return ErrVersionConflict
		}
		var version chapterVersionRow
		if err := tx.Where("id = ? AND chapter_id = ? AND project_id = ?", in.ExpectedChapterVersionID, in.ChapterID, in.ProjectID).First(&version).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrVersionConflict
			}
			return err
		}
		result = newConfirmation(in, workspace)
		details, err := json.Marshal(result)
		if err != nil {
			return err
		}
		if err := tx.Create(&confirmationRow{ID: result.ID, ChapterID: in.ChapterID, ChapterVersionID: in.ExpectedChapterVersionID, Valid: true, DetailsJSON: string(details)}).Error; err != nil {
			return err
		}
		if err := tx.Model(&confirmationRow{}).
			Where("chapter_id = ? AND chapter_version_id = ? AND id <> ? AND valid = ?", in.ChapterID, in.ExpectedChapterVersionID, result.ID, true).
			Update("valid", false).Error; err != nil {
			return err
		}
		if err := tx.Model(&chapterVersionRow{}).Where("id = ? AND chapter_id = ? AND project_id = ?", in.ExpectedChapterVersionID, in.ChapterID, in.ProjectID).Update("confirmation_valid", true).Error; err != nil {
			return err
		}
		projectUpdate := tx.Model(&projectRow{}).Where("id = ? AND project_version = ?", in.ProjectID, project.ProjectVersion).
			Update("project_version", project.ProjectVersion+1)
		if projectUpdate.Error != nil {
			return projectUpdate.Error
		}
		if projectUpdate.RowsAffected != 1 {
			return ErrVersionConflict
		}
		if auditEnabled {
			details, err := json.Marshal(map[string]any{
				"chapter_id":         in.ChapterID,
				"chapter_version_id": in.ExpectedChapterVersionID,
				"spec_revision":      in.ExpectedSpecRevision,
				"confirmation_id":    result.ID,
			})
			if err != nil {
				return err
			}
			if err := tx.Create(&projectAuditRow{
				ID: uuid.NewString(), ProjectID: in.ProjectID, ActorID: in.ActorID,
				Action: "project.change_set.review", Target: in.ChapterID,
				DetailsJSON: string(details), CreatedAt: result.CreatedAt,
			}).Error; err != nil {
				return err
			}
		}
		response, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.Create(&confirmationIdempotencyRow{ID: uuid.NewString(), ProjectID: in.ProjectID, ChapterID: in.ChapterID,
			ActorID: in.ActorID, Key: in.IdempotencyKey, RequestHash: hash, ResponseJSON: string(response)}).Error
	})
	return result, replayed, err
}

func (s *SQLiteCandidateAdoptionStore) GetCandidate(ctx context.Context, projectID, candidateID string) (Candidate, error) {
	var row candidateRow
	if err := s.db.WithContext(ctx).Where("id = ? AND project_id = ?", candidateID, projectID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Candidate{}, ErrNotFound
		}
		return Candidate{}, err
	}
	return candidateFromRow(row)
}

func (s *SQLiteCandidateAdoptionStore) ListCandidates(ctx context.Context, projectID, chapterID string) ([]CandidateSummary, error) {
	var rows []candidateRow
	if err := s.db.WithContext(ctx).Where("project_id = ? AND chapter_id = ?", projectID, chapterID).
		Order("created_at DESC, id DESC").Limit(50).Find(&rows).Error; err != nil {
		return nil, err
	}
	candidates := make([]CandidateSummary, 0, len(rows))
	for _, row := range rows {
		var basis Basis
		if err := json.Unmarshal([]byte(row.BasisJSON), &basis); err != nil {
			return nil, fmt.Errorf("decode candidate basis: %w", err)
		}
		candidates = append(candidates, CandidateSummary{ID: row.ID, RunID: row.RunID, Validity: Validity(row.Validity), CreatedAt: row.CreatedAt, Basis: basis})
	}
	return candidates, nil
}

func (s *SQLiteCandidateAdoptionStore) GenerationContext(ctx context.Context, projectID, chapterID string) (GenerationContext, error) {
	tx := s.db.WithContext(ctx)
	var project projectRow
	if err := tx.Where("id = ?", projectID).First(&project).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return GenerationContext{}, ErrNotFound
		}
		return GenerationContext{}, err
	}
	var row chapterRow
	if err := tx.Where("id = ? AND project_id = ?", chapterID, projectID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return GenerationContext{}, ErrNotFound
		}
		return GenerationContext{}, err
	}
	chapter, err := s.chapterFromRow(tx, row)
	if err != nil {
		return GenerationContext{}, err
	}
	basis, err := currentAdoptionBasis(tx, project, chapter)
	if err != nil {
		return GenerationContext{}, err
	}
	return GenerationContext{
		ProjectID: projectID, ChapterID: chapterID, SpecRevision: int(project.SpecRevision),
		ChapterVersionID: chapter.CurrentVersionID, Basis: basis, Chapter: chapter,
	}, nil
}

func (s *SQLiteCandidateAdoptionStore) ListChapters(ctx context.Context, projectID string) ([]Chapter, error) {
	tx := s.db.WithContext(ctx)
	var rows []chapterRow
	if err := tx.Where("project_id = ?", projectID).Order("section_id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	chapters := make([]Chapter, 0, len(rows))
	for _, row := range rows {
		chapter, err := s.chapterFromRow(tx, row)
		if err != nil {
			return nil, err
		}
		chapters = append(chapters, chapter)
	}
	return chapters, nil
}

func (s *SQLiteCandidateAdoptionStore) ReplayCandidateAcceptance(ctx context.Context, in AcceptCandidateInput) (*AcceptResult, error) {
	var result *AcceptResult
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		replay, err := s.replayCandidateAcceptance(ctx, tx, in)
		result = replay
		return err
	})
	return result, err
}

func (s *SQLiteCandidateAdoptionStore) replayCandidateAcceptance(ctx context.Context, tx *gorm.DB, in AcceptCandidateInput) (*AcceptResult, error) {
	var row adoptionIdempotencyRow
	err := tx.Where("project_id = ? AND chapter_id = ? AND actor_id = ? AND idempotency_key = ?", in.ProjectID, in.ChapterID, in.ActorID, in.IdempotencyKey).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := compareAdoptionDigest(row, requestHash(in)); err != nil {
		return nil, err
	}
	candidate, err := NewSQLiteCandidateAdoptionStore(tx).GetCandidate(ctx, in.ProjectID, in.CandidateID)
	if err != nil {
		return nil, err
	}
	if candidate.ChapterID != in.ChapterID {
		return nil, ErrNotFound
	}
	if err := s.authorizeCandidateInTransaction(ctx, tx, in); err != nil {
		return nil, err
	}
	if err := s.validateEvidenceInTransaction(ctx, tx, in, candidate); err != nil {
		return nil, err
	}
	var chapter Chapter
	if err := json.Unmarshal([]byte(row.ResponseJSON), &chapter); err != nil {
		return nil, fmt.Errorf("decode idempotency response: %w", err)
	}
	return &AcceptResult{Chapter: chapter, Replayed: true}, nil
}

func (s *SQLiteCandidateAdoptionStore) AcceptCandidate(ctx context.Context, in AcceptCandidateInput, candidate Candidate) (AcceptResult, error) {
	var result AcceptResult
	hash := requestHash(in)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.authorizeCandidateInTransaction(ctx, tx, in); err != nil {
			return err
		}
		var existing adoptionIdempotencyRow
		lookupErr := tx.Where("project_id = ? AND chapter_id = ? AND actor_id = ? AND idempotency_key = ?", in.ProjectID, in.ChapterID, in.ActorID, in.IdempotencyKey).First(&existing).Error
		if lookupErr == nil {
			if err := compareAdoptionDigest(existing, hash); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(existing.ResponseJSON), &result.Chapter); err != nil {
				return fmt.Errorf("decode idempotency response: %w", err)
			}
			persisted, err := NewSQLiteCandidateAdoptionStore(tx).GetCandidate(ctx, in.ProjectID, in.CandidateID)
			if err != nil {
				return err
			}
			if persisted.ChapterID != in.ChapterID {
				return ErrNotFound
			}
			if err := s.validateEvidenceInTransaction(ctx, tx, in, persisted); err != nil {
				return err
			}
			result.Replayed = true
			return nil
		}
		if !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return lookupErr
		}

		var project projectRow
		if err := tx.Where("id = ?", in.ProjectID).First(&project).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if project.SpecRevision != int64(in.ExpectedSpecRevision) {
			return ErrVersionConflict
		}
		var chapter chapterRow
		if err := tx.Where("id = ? AND project_id = ?", in.ChapterID, in.ProjectID).First(&chapter).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if !sameOptionalString(chapter.CurrentVersionID, in.ExpectedChapterVersionID) {
			return ErrVersionConflict
		}
		current, err := s.chapterFromRow(tx, chapter)
		if err != nil {
			return err
		}
		if current.CurrentVersionID != nil && !in.ReplaceExisting {
			return ErrInvalidState
		}

		persisted, err := NewSQLiteCandidateAdoptionStore(tx).GetCandidate(ctx, in.ProjectID, in.CandidateID)
		if err != nil {
			return err
		}
		if !sameCandidateSnapshot(persisted, candidate) {
			return ErrVersionConflict
		}
		basis, err := currentAdoptionBasis(tx, project, current)
		if err != nil {
			return err
		}
		// The explicit focused schema retains older persistence-only fixtures.
		// Production and every candidate carrying a validity/new basis use full checks.
		if !s.focusedSchema || persisted.Validity != "" || candidateHasNewBasis(persisted.Basis) {
			if err := validateCandidate(in, GenerationContext{ProjectID: in.ProjectID, ChapterID: in.ChapterID, SpecRevision: int(project.SpecRevision), ChapterVersionID: chapter.CurrentVersionID, Basis: basis, Chapter: current}, persisted); err != nil {
				return err
			}
		} else if err := validateSourceReferences(persisted.BodyMarkdown, persisted.SourceIDs); err != nil {
			return err
		}
		if err := s.validateEvidenceInTransaction(ctx, tx, in, persisted); err != nil {
			return err
		}
		workingCopy, err := s.cleanWorkingCopy(tx, current)
		if err != nil {
			return err
		}

		oldVersionID := chapter.CurrentVersionID
		newVersionID := uuid.NewString()
		sourceIDs := slices.Compact(normalizeIDs(candidate.SourceIDs))
		// A new candidate has no researcher-authored purpose/limitation metadata.
		// Initialize every declared source as unknown now, rather than copying the
		// old version's interpretations or waiting for a later save to fill them.
		// SaveWorkingCopy/SaveChapter accept explicit usages from the researcher;
		// absent descriptions stay unknown, and prior immutable versions survive.
		citationUsages := make([]CitationUsage, 0, len(sourceIDs))
		for _, sourceID := range sourceIDs {
			citationUsages = append(citationUsages, CitationUsage{SourceID: sourceID})
		}
		usageJSON, err := json.Marshal(citationUsages)
		if err != nil {
			return err
		}
		sourceJSON, err := json.Marshal(sourceIDs)
		if err != nil {
			return err
		}
		reviewJSON, err := json.Marshal(candidate.ReviewItems)
		if err != nil {
			return err
		}
		version := chapterVersionRow{
			ID: newVersionID, ProjectID: in.ProjectID, ChapterID: in.ChapterID,
			ParentVersionID: oldVersionID, CandidateID: candidate.ID,
			BodyMarkdown: candidate.BodyMarkdown, SourceIDsJSON: string(sourceJSON),
			CitationUsagesJSON: string(usageJSON),
			ReviewItemsJSON:    string(reviewJSON), SpecRevision: int64(in.ExpectedSpecRevision),
			ConfirmationValid: false, CreatedBy: in.ActorID,
		}
		if err := tx.Create(&version).Error; err != nil {
			return err
		}
		if err := tx.Create(&confirmationRow{ID: uuid.NewString(), ChapterID: in.ChapterID, ChapterVersionID: newVersionID, Valid: false, DetailsJSON: "{}"}).Error; err != nil {
			return err
		}
		query := tx.Model(&chapterRow{}).Where("id = ? AND project_id = ?", in.ChapterID, in.ProjectID)
		if oldVersionID == nil {
			query = query.Where("current_version_id IS NULL")
		} else {
			query = query.Where("current_version_id = ?", *oldVersionID)
		}
		if result := query.Update("current_version_id", newVersionID); result.Error != nil {
			return result.Error
		} else if result.RowsAffected != 1 {
			return ErrVersionConflict
		}
		if result := tx.Model(&projectRow{}).Where("id = ? AND project_version = ?", in.ProjectID, project.ProjectVersion).Update("project_version", gorm.Expr("project_version + 1")); result.Error != nil {
			return result.Error
		} else if result.RowsAffected != 1 {
			return ErrVersionConflict
		}

		if err := storeAdoptedEvidenceDependencies(tx, in, persisted, newVersionID); err != nil {
			return err
		}
		if workingCopy != nil {
			if err := advanceAdoptedWorkingCopy(tx, *workingCopy, version); err != nil {
				return err
			}
		}

		current.CurrentVersionID = &newVersionID
		current.BodyMarkdown = candidate.BodyMarkdown
		current.SourceIDs = sourceIDs
		current.CitationUsages = citationUsages
		current.ReviewItems = candidate.ReviewItems
		current.ConfirmationValid = false
		result.Chapter = current
		responseJSON, err := json.Marshal(result.Chapter)
		if err != nil {
			return err
		}
		if err := tx.Create(&adoptionIdempotencyRow{
			ID: uuid.NewString(), ProjectID: in.ProjectID, ChapterID: in.ChapterID,
			ActorID: in.ActorID, Key: in.IdempotencyKey, RequestHash: hash,
			DigestVersion:    adoptionDigestVersion,
			ChapterVersionID: newVersionID, ResponseJSON: string(responseJSON),
		}).Error; err != nil {
			return err
		}
		return nil
	})
	return result, err
}

func (s *SQLiteCandidateAdoptionStore) UpsertProject(ctx context.Context, id string, version int) error {
	row := projectRow{
		ID: id, TenantID: 1, Name: id, Status: "draft", ProjectVersion: int64(version),
		SpecRevision: 0, SpecJSON: "{}", TemplateID: "template-demo", TemplateVersion: "1",
	}
	return s.db.WithContext(ctx).Save(&row).Error
}

func (s *SQLiteCandidateAdoptionStore) UpsertChapter(ctx context.Context, chapter Chapter, specRevision int) error {
	if err := s.db.WithContext(ctx).Save(&chapterRow{
		ID: chapter.ID, ProjectID: chapter.ProjectID, SectionID: chapter.SectionID,
		Title: chapter.Title, CurrentVersionID: chapter.CurrentVersionID,
	}).Error; err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(&projectRow{}).Where("id = ?", chapter.ProjectID).Update("spec_revision", specRevision).Error
}

func (s *SQLiteCandidateAdoptionStore) UpsertChapterWithBasis(ctx context.Context, chapter Chapter, specRevision int, _ Basis) error {
	if err := s.UpsertChapter(ctx, chapter, specRevision); err != nil {
		return err
	}
	return s.db.WithContext(ctx).Model(&projectRow{}).Where("id = ?", chapter.ProjectID).Update("spec_revision", specRevision).Error
}

func (s *SQLiteCandidateAdoptionStore) UpsertCandidate(ctx context.Context, candidate Candidate) error {
	sources, err := json.Marshal(normalizeIDs(candidate.SourceIDs))
	if err != nil {
		return err
	}
	basis, err := json.Marshal(candidate.Basis)
	if err != nil {
		return err
	}
	reviews, err := json.Marshal(candidate.ReviewItems)
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Save(&candidateRow{
		ID: candidate.ID, ProjectID: candidate.ProjectID, ChapterID: candidate.ChapterID,
		RunID: candidate.RunID, BodyMarkdown: candidate.BodyMarkdown,
		SourceIDsJSON: string(sources), BasisJSON: string(basis), Validity: string(candidate.Validity),
		ReviewItemsJSON: string(reviews),
		WritingAction:   string(candidate.WritingAction),
		Origin:          string(candidate.Origin),
		RunMode:         string(candidate.RunMode),
		ModelID:         candidate.ModelID,
	}).Error
}

func (s *SQLiteCandidateAdoptionStore) chapterFromRow(tx *gorm.DB, row chapterRow) (Chapter, error) {
	chapter := Chapter{
		ID: row.ID, ProjectID: row.ProjectID, SectionID: row.SectionID, Title: row.Title,
		CurrentVersionID: row.CurrentVersionID, SourceIDs: []string{}, ReviewItems: []ReviewItem{},
		CitationUsages: []CitationUsage{},
	}
	if row.CurrentVersionID == nil {
		return chapter, nil
	}
	var version chapterVersionRow
	if err := tx.Where("id = ? AND project_id = ? AND chapter_id = ?", *row.CurrentVersionID, row.ProjectID, row.ID).First(&version).Error; err != nil {
		return Chapter{}, err
	}
	chapter.BodyMarkdown = version.BodyMarkdown
	chapter.ConfirmationValid = version.ConfirmationValid
	if err := json.Unmarshal([]byte(version.SourceIDsJSON), &chapter.SourceIDs); err != nil {
		return Chapter{}, err
	}
	if version.CitationUsagesJSON != "" {
		if err := json.Unmarshal([]byte(version.CitationUsagesJSON), &chapter.CitationUsages); err != nil {
			return Chapter{}, err
		}
		if chapter.CitationUsages == nil {
			chapter.CitationUsages = []CitationUsage{}
		}
	}
	if err := json.Unmarshal([]byte(version.ReviewItemsJSON), &chapter.ReviewItems); err != nil {
		return Chapter{}, err
	}
	return chapter, nil
}

func candidateFromRow(row candidateRow) (Candidate, error) {
	var candidate Candidate
	candidate.ID, candidate.ProjectID, candidate.ChapterID, candidate.RunID = row.ID, row.ProjectID, row.ChapterID, row.RunID
	candidate.BodyMarkdown, candidate.Validity = row.BodyMarkdown, Validity(row.Validity)
	candidate.WritingAction = writingaction.Action(row.WritingAction)
	candidate.Origin = writingaction.Origin(row.Origin)
	candidate.RunMode = RunMode(row.RunMode)
	candidate.ModelID = row.ModelID
	if err := json.Unmarshal([]byte(row.SourceIDsJSON), &candidate.SourceIDs); err != nil {
		return Candidate{}, err
	}
	if err := json.Unmarshal([]byte(row.BasisJSON), &candidate.Basis); err != nil {
		return Candidate{}, err
	}
	if err := json.Unmarshal([]byte(row.ReviewItemsJSON), &candidate.ReviewItems); err != nil {
		return Candidate{}, err
	}
	return candidate, nil
}

// adoptionWorkingCopyRow mirrors the G7 row without introducing a package cycle.
type adoptionWorkingCopyRow struct {
	ProjectID            string `gorm:"primaryKey"`
	ChapterID            string `gorm:"primaryKey"`
	BaseChapterVersionID *string
	SpecRevision         int64
	WorkingCopyRevision  int64
	BodyMarkdown         string
	SourceIDsJSON        string `gorm:"column:source_ids_json"`
	CitationUsagesJSON   string `gorm:"column:citation_usages_json"`
	ReviewItemsJSON      string `gorm:"column:review_items_json"`
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func (adoptionWorkingCopyRow) TableName() string { return "lingdoc_working_copies" }

func equalAdoptionJSON(raw string, value any) bool {
	var stored, expected any
	encoded, err := json.Marshal(value)
	if err != nil {
		return false
	}
	if raw == "" || raw == "null" {
		raw = "[]"
	}
	if string(encoded) == "null" {
		encoded = []byte("[]")
	}
	if json.Unmarshal([]byte(raw), &stored) != nil || json.Unmarshal(encoded, &expected) != nil {
		return false
	}
	return reflect.DeepEqual(stored, expected)
}

func (s *SQLiteCandidateAdoptionStore) cleanWorkingCopy(tx *gorm.DB, current Chapter) (*adoptionWorkingCopyRow, error) {
	// Only the explicit focused schema helper permits an absent G7 table.
	// Production must fail on missing migrations instead of accepting a stale editor.
	if s.focusedSchema && !tx.Migrator().HasTable(&adoptionWorkingCopyRow{}) {
		return nil, nil
	}
	var row adoptionWorkingCopyRow
	if err := tx.Where("project_id = ? AND chapter_id = ?", current.ProjectID, current.ID).First(&row).Error; err != nil {
		return nil, err
	}
	if !sameOptionalString(row.BaseChapterVersionID, current.CurrentVersionID) ||
		row.BodyMarkdown != current.BodyMarkdown ||
		!equalAdoptionJSON(row.SourceIDsJSON, current.SourceIDs) ||
		!equalAdoptionJSON(row.CitationUsagesJSON, current.CitationUsages) ||
		!equalAdoptionJSON(row.ReviewItemsJSON, current.ReviewItems) {
		return nil, ErrVersionConflict
	}
	return &row, nil
}

func advanceAdoptedWorkingCopy(tx *gorm.DB, previous adoptionWorkingCopyRow, version chapterVersionRow) error {
	query := tx.Model(&adoptionWorkingCopyRow{}).Where("project_id = ? AND chapter_id = ? AND working_copy_revision = ? AND spec_revision = ?",
		previous.ProjectID, previous.ChapterID, previous.WorkingCopyRevision, previous.SpecRevision)
	if previous.BaseChapterVersionID == nil {
		query = query.Where("base_chapter_version_id IS NULL")
	} else {
		query = query.Where("base_chapter_version_id = ?", *previous.BaseChapterVersionID)
	}
	result := query.Updates(map[string]any{
		"base_chapter_version_id": version.ID, "spec_revision": version.SpecRevision,
		"working_copy_revision": previous.WorkingCopyRevision + 1,
		"body_markdown":         version.BodyMarkdown, "source_ids_json": version.SourceIDsJSON,
		"citation_usages_json": version.CitationUsagesJSON, "review_items_json": version.ReviewItemsJSON,
		"updated_at": time.Now().UTC(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return ErrVersionConflict
	}
	return nil
}
