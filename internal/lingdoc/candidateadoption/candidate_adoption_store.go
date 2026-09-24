package candidateadoption

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// SQLiteCandidateAdoptionStore owns the T11 candidate and adoption records
// while reusing the T08 workspace tables for projects, chapters, and versions.
type SQLiteCandidateAdoptionStore struct {
	db *gorm.DB
}

func NewSQLiteCandidateAdoptionStore(db *gorm.DB) *SQLiteCandidateAdoptionStore {
	return &SQLiteCandidateAdoptionStore{db: db}
}

func (s *SQLiteCandidateAdoptionStore) DB() *gorm.DB { return s.db }

type projectRow struct {
	ID              string `gorm:"primaryKey;size:36"`
	TenantID        uint64 `gorm:"not null;index"`
	Name            string `gorm:"not null;size:120"`
	Status          string `gorm:"not null;size:16"`
	ProjectVersion  int64  `gorm:"not null"`
	SpecRevision    int64  `gorm:"not null"`
	SpecJSON        string `gorm:"column:spec_json;not null;type:text"`
	TemplateID      string `gorm:"not null;size:80"`
	TemplateVersion string `gorm:"not null;size:40"`
	CreatedAt       time.Time
}

type chapterRow struct {
	ID               string  `gorm:"primaryKey;size:36"`
	ProjectID        string  `gorm:"not null;index;size:36"`
	SectionID        string  `gorm:"not null;size:80"`
	Title            string  `gorm:"not null;size:120"`
	CurrentVersionID *string `gorm:"size:36"`
}

type chapterVersionRow struct {
	ID                string  `gorm:"primaryKey;size:36"`
	ProjectID         string  `gorm:"not null;index;size:36"`
	ChapterID         string  `gorm:"not null;index;size:36"`
	ParentVersionID   *string `gorm:"size:36"`
	CandidateID       string  `gorm:"index;size:36"`
	BodyMarkdown      string  `gorm:"not null;type:text"`
	SourceIDsJSON     string  `gorm:"column:source_ids_json;not null;type:text"`
	ReviewItemsJSON   string  `gorm:"column:review_items_json;not null;type:text"`
	SpecRevision      int64   `gorm:"not null;default:0"`
	ConfirmationValid bool    `gorm:"not null;default:false"`
	CreatedBy         string  `gorm:"not null;default:'';size:128"`
	CreatedAt         time.Time
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
	CreatedAt       time.Time
}

type adoptionIdempotencyRow struct {
	ID               string `gorm:"primaryKey;size:36"`
	ProjectID        string `gorm:"index;not null;size:36"`
	ChapterID        string `gorm:"index;not null;size:36"`
	ActorID          string `gorm:"index;not null;size:128"`
	Key              string `gorm:"column:idempotency_key;not null;size:128"`
	RequestHash      string `gorm:"not null;size:64"`
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

func (projectRow) TableName() string                 { return "lingdoc_projects" }
func (chapterRow) TableName() string                 { return "lingdoc_chapters" }
func (chapterVersionRow) TableName() string          { return "lingdoc_chapter_versions" }
func (confirmationRow) TableName() string            { return "lingdoc_chapter_confirmations" }
func (candidateRow) TableName() string               { return "lingdoc_candidates" }
func (adoptionIdempotencyRow) TableName() string     { return "lingdoc_candidate_adoptions" }
func (confirmationIdempotencyRow) TableName() string { return "lingdoc_chapter_confirmation_requests" }

// AutoMigrate is used by focused store tests. Production startup uses the
// numbered migrations, where 000018 owns workspace tables and 000020 adds the
// T11 columns and records.
func (s *SQLiteCandidateAdoptionStore) AutoMigrate(ctx context.Context) error {
	return s.db.WithContext(ctx).AutoMigrate(
		&projectRow{}, &chapterRow{}, &chapterVersionRow{}, &candidateRow{},
		&confirmationRow{}, &adoptionIdempotencyRow{}, &confirmationIdempotencyRow{},
	)
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
		if err := tx.Model(&chapterVersionRow{}).Where("id = ? AND chapter_id = ? AND project_id = ?", in.ExpectedChapterVersionID, in.ChapterID, in.ProjectID).Update("confirmation_valid", true).Error; err != nil {
			return err
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
	basis := Basis{
		SpecRevision:     int(project.SpecRevision),
		ChapterVersionID: chapter.CurrentVersionID,
		TemplateID:       project.TemplateID,
		TemplateVersion:  project.TemplateVersion,
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
	var row adoptionIdempotencyRow
	err := s.db.WithContext(ctx).Where("project_id = ? AND chapter_id = ? AND actor_id = ? AND idempotency_key = ?", in.ProjectID, in.ChapterID, in.ActorID, in.IdempotencyKey).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if row.RequestHash != requestHash(in) {
		return nil, ErrIdempotencyConflict
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
		var existing adoptionIdempotencyRow
		lookupErr := tx.Where("project_id = ? AND chapter_id = ? AND actor_id = ? AND idempotency_key = ?", in.ProjectID, in.ChapterID, in.ActorID, in.IdempotencyKey).First(&existing).Error
		if lookupErr == nil {
			if existing.RequestHash != hash {
				return ErrIdempotencyConflict
			}
			if err := json.Unmarshal([]byte(existing.ResponseJSON), &result.Chapter); err != nil {
				return fmt.Errorf("decode idempotency response: %w", err)
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
		if strings.TrimSpace(current.BodyMarkdown) != "" && !in.ReplaceExisting {
			return ErrInvalidState
		}

		oldVersionID := chapter.CurrentVersionID
		newVersionID := uuid.NewString()
		sourceJSON, err := json.Marshal(normalizeIDs(candidate.SourceIDs))
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
			ReviewItemsJSON: string(reviewJSON), SpecRevision: int64(in.ExpectedSpecRevision),
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

		current.CurrentVersionID = &newVersionID
		current.BodyMarkdown = candidate.BodyMarkdown
		current.SourceIDs = normalizeIDs(candidate.SourceIDs)
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
		SourceIDsJSON: string(sources), BasisJSON: string(basis), Validity: candidate.Validity,
		ReviewItemsJSON: string(reviews),
	}).Error
}

func (s *SQLiteCandidateAdoptionStore) chapterFromRow(tx *gorm.DB, row chapterRow) (Chapter, error) {
	chapter := Chapter{
		ID: row.ID, ProjectID: row.ProjectID, SectionID: row.SectionID, Title: row.Title,
		CurrentVersionID: row.CurrentVersionID, SourceIDs: []string{}, ReviewItems: []ReviewItem{},
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
	if err := json.Unmarshal([]byte(version.ReviewItemsJSON), &chapter.ReviewItems); err != nil {
		return Chapter{}, err
	}
	return chapter, nil
}

func candidateFromRow(row candidateRow) (Candidate, error) {
	var candidate Candidate
	candidate.ID, candidate.ProjectID, candidate.ChapterID, candidate.RunID = row.ID, row.ProjectID, row.ChapterID, row.RunID
	candidate.BodyMarkdown, candidate.Validity = row.BodyMarkdown, row.Validity
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
