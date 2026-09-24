package generation

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type generationRow struct {
	ID             string  `gorm:"primaryKey;size:36"`
	TenantID       uint64  `gorm:"not null;uniqueIndex:idx_generation_idempotency,priority:1;index:idx_generation_owner,priority:1"`
	ActorID        string  `gorm:"not null;size:128;uniqueIndex:idx_generation_idempotency,priority:2;index:idx_generation_owner,priority:2"`
	ProjectID      string  `gorm:"not null;size:36;index;uniqueIndex:idx_generation_idempotency,priority:3"`
	ChapterID      string  `gorm:"not null;size:36;index"`
	IdempotencyKey string  `gorm:"column:idempotency_key;not null;size:128;uniqueIndex:idx_generation_idempotency,priority:4"`
	RequestHash    string  `gorm:"not null;size:64"`
	Status         Status  `gorm:"not null;size:16;index"`
	CandidateID    *string `gorm:"size:36"`
	ErrorCode      string  `gorm:"size:64"`
	ErrorMessage   string  `gorm:"size:500"`
	Retryable      bool    `gorm:"not null;default:false"`
	InputJSON      string  `gorm:"type:text;not null"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (generationRow) TableName() string { return "lingdoc_generation_runs" }

type SQLiteRepository struct{ db *gorm.DB }

func NewSQLiteRepository(db *gorm.DB) *SQLiteRepository { return &SQLiteRepository{db: db} }

func (s *SQLiteRepository) AutoMigrate(ctx context.Context) error {
	return s.db.WithContext(ctx).AutoMigrate(&generationRow{})
}

func (s *SQLiteRepository) CreateOrReplay(ctx context.Context, actor Actor, projectID, key, requestHash string, input Input) (Run, error) {
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return Run{}, ErrInvalidRequest
	}
	var run Run
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing generationRow
		err := tx.Where("tenant_id = ? AND actor_id = ? AND project_id = ? AND idempotency_key = ?", actor.TenantID, actor.UserID, projectID, key).First(&existing).Error
		if err == nil {
			if existing.RequestHash != requestHash {
				return ErrIdempotencyConflict
			}
			run = runFromRow(existing)
			run.Replayed = true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		row := generationRow{
			ID: uuid.NewString(), TenantID: actor.TenantID, ActorID: actor.UserID,
			ProjectID: projectID, ChapterID: input.Request.ChapterID,
			IdempotencyKey: key, RequestHash: requestHash, Status: StatusQueued,
			InputJSON: string(inputJSON),
		}
		result := tx.Clauses(clause.OnConflict{Columns: []clause.Column{
			{Name: "tenant_id"}, {Name: "actor_id"}, {Name: "project_id"}, {Name: "idempotency_key"},
		}, DoNothing: true}).Create(&row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			run = runFromRow(row)
			return nil
		}
		if err := tx.Where("tenant_id = ? AND actor_id = ? AND project_id = ? AND idempotency_key = ?", actor.TenantID, actor.UserID, projectID, key).First(&existing).Error; err != nil {
			return err
		}
		if existing.RequestHash != requestHash {
			return ErrIdempotencyConflict
		}
		run = runFromRow(existing)
		run.Replayed = true
		return nil
	})
	return run, err
}

func (s *SQLiteRepository) Claim(ctx context.Context, runID string) (Run, Input, bool, error) {
	var row generationRow
	var input Input
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", runID).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if row.Status != StatusQueued {
			return nil
		}
		result := tx.Model(&generationRow{}).Where("id = ? AND status = ?", runID, StatusQueued).Updates(map[string]any{
			"status": StatusRunning, "updated_at": time.Now().UTC(),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return nil
		}
		row.Status = StatusRunning
		if err := json.Unmarshal([]byte(row.InputJSON), &input); err != nil {
			return err
		}
		return nil
	})
	return runFromRow(row), input, row.Status == StatusRunning && input.Workspace.ProjectID != "", err
}

func (s *SQLiteRepository) Complete(ctx context.Context, runID string, candidate candidateadoption.Candidate) (Run, error) {
	var row generationRow
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", runID).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if row.Status != StatusRunning || row.ProjectID != candidate.ProjectID || row.ChapterID != candidate.ChapterID || candidate.RunID != row.ID {
			return ErrRunUnavailable
		}
		if err := candidateadoption.NewSQLiteCandidateAdoptionStore(tx).UpsertCandidate(ctx, candidate); err != nil {
			return err
		}
		result := tx.Model(&generationRow{}).Where("id = ? AND status = ?", runID, StatusRunning).Updates(map[string]any{
			"status": StatusSucceeded, "candidate_id": candidate.ID,
			"error_code": "", "error_message": "", "retryable": false, "updated_at": time.Now().UTC(),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrRunUnavailable
		}
		row.Status, row.CandidateID = StatusSucceeded, &candidate.ID
		row.ErrorCode, row.ErrorMessage, row.Retryable = "", "", false
		return nil
	})
	return runFromRow(row), err
}

func (s *SQLiteRepository) Fail(ctx context.Context, runID string, status Status, failure RunError) (Run, error) {
	if status != StatusFailed && status != StatusInterrupted {
		return Run{}, ErrInvalidRequest
	}
	var row generationRow
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", runID).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if row.Status != StatusRunning {
			return ErrRunUnavailable
		}
		result := tx.Model(&generationRow{}).Where("id = ? AND status = ?", runID, StatusRunning).Updates(map[string]any{
			"status": status, "error_code": failure.Code, "error_message": failure.Message,
			"retryable": failure.Retryable, "updated_at": time.Now().UTC(),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrRunUnavailable
		}
		row.Status, row.ErrorCode, row.ErrorMessage, row.Retryable = status, failure.Code, failure.Message, failure.Retryable
		return nil
	})
	return runFromRow(row), err
}

func (s *SQLiteRepository) Get(ctx context.Context, actor Actor, projectID, runID string) (Run, error) {
	var row generationRow
	err := s.db.WithContext(ctx).Where("id = ? AND tenant_id = ? AND actor_id = ? AND project_id = ?", runID, actor.TenantID, actor.UserID, projectID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	return runFromRow(row), nil
}

func runFromRow(row generationRow) Run {
	run := Run{ID: row.ID, ProjectID: row.ProjectID, ChapterID: row.ChapterID, Status: row.Status,
		CandidateID: row.CandidateID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if row.ErrorCode != "" {
		run.Error = &RunError{Code: row.ErrorCode, Message: row.ErrorMessage, Retryable: row.Retryable}
	}
	return run
}
