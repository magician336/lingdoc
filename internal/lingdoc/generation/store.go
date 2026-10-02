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
	ID              string  `gorm:"primaryKey;size:36"`
	TenantID        uint64  `gorm:"not null;uniqueIndex:idx_generation_idempotency,priority:1;index:idx_generation_owner,priority:1"`
	ActorID         string  `gorm:"not null;size:128;uniqueIndex:idx_generation_idempotency,priority:2;index:idx_generation_owner,priority:2"`
	ProjectID       string  `gorm:"not null;size:36;index;uniqueIndex:idx_generation_idempotency,priority:3"`
	ChapterID       string  `gorm:"not null;size:36;index"`
	IdempotencyKey  string  `gorm:"column:idempotency_key;not null;size:128;uniqueIndex:idx_generation_idempotency,priority:4"`
	RequestHash     string  `gorm:"not null;size:64"`
	Status          Status  `gorm:"not null;size:16;index"`
	ClaimToken      string  `gorm:"not null;size:36"`
	CancelRequested bool    `gorm:"not null;default:false"`
	CandidateID     *string `gorm:"size:36"`
	ErrorCode       string  `gorm:"size:64"`
	ErrorMessage    string  `gorm:"size:500"`
	Retryable       bool    `gorm:"not null;default:false"`
	InputJSON       string  `gorm:"type:text;not null"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (generationRow) TableName() string { return "lingdoc_generation_runs" }

type SQLiteRepository struct{ db *gorm.DB }

func NewSQLiteRepository(db *gorm.DB) *SQLiteRepository { return &SQLiteRepository{db: db} }

func (s *SQLiteRepository) AutoMigrate(ctx context.Context) error {
	return s.db.WithContext(ctx).AutoMigrate(&generationRow{})
}

func (s *SQLiteRepository) FindReplay(ctx context.Context, actor Actor, projectID, key, requestHash string) (Run, bool, error) {
	var row generationRow
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND actor_id = ? AND project_id = ? AND idempotency_key = ?",
		actor.TenantID, actor.UserID, projectID, key).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, err
	}
	if row.RequestHash != requestHash {
		return Run{}, false, ErrIdempotencyConflict
	}
	run := runFromRow(row)
	run.Replayed = true
	return run, true, nil
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
	claimed := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", runID).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		now := time.Now().UTC()
		if row.CancelRequested {
			if row.Status == StatusQueued || (row.Status == StatusRunning && !row.UpdatedAt.After(now.Add(-claimLeaseDuration))) {
				result := tx.Model(&generationRow{}).
					Where("id = ? AND status = ? AND claim_token = ? AND cancel_requested = ?", runID, row.Status, row.ClaimToken, true).
					Updates(map[string]any{
						"status": StatusInterrupted, "claim_token": "", "error_code": "generation_cancelled",
						"error_message": "生成任务已取消。", "retryable": false, "updated_at": now,
					})
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected == 1 {
					row.Status, row.ClaimToken = StatusInterrupted, ""
					row.ErrorCode, row.ErrorMessage, row.Retryable = "generation_cancelled", "生成任务已取消。", false
					row.UpdatedAt = now
				}
			}
			return nil
		}
		if row.Status != StatusQueued && (row.Status != StatusRunning || row.UpdatedAt.After(now.Add(-claimLeaseDuration))) {
			return nil
		}
		claimToken := uuid.NewString()
		query := tx.Model(&generationRow{}).Where("id = ? AND status = ?", runID, row.Status)
		if row.Status == StatusRunning {
			query = query.Where("updated_at = ?", row.UpdatedAt)
		}
		result := query.Updates(map[string]any{
			"status": StatusRunning, "claim_token": claimToken, "updated_at": now,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return nil
		}
		row.Status, row.ClaimToken, row.UpdatedAt = StatusRunning, claimToken, now
		if err := json.Unmarshal([]byte(row.InputJSON), &input); err != nil {
			return err
		}
		claimed = true
		return nil
	})
	return runFromRow(row), input, claimed, err
}

func (s *SQLiteRepository) Touch(ctx context.Context, runID, claimToken string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row generationRow
		if err := tx.Where("id = ?", runID).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if row.Status != StatusRunning || row.ClaimToken != claimToken {
			return ErrRunUnavailable
		}
		if row.CancelRequested {
			return ErrGenerationCancelled
		}
		result := tx.Model(&generationRow{}).
			Where("id = ? AND status = ? AND claim_token = ? AND cancel_requested = ?", runID, StatusRunning, claimToken, false).
			Update("updated_at", time.Now().UTC())
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrRunUnavailable
		}
		return nil
	})
}

func (s *SQLiteRepository) Cancel(ctx context.Context, actor Actor, projectID, runID string) (Run, error) {
	var row generationRow
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ? AND tenant_id = ? AND actor_id = ? AND project_id = ?", runID, actor.TenantID, actor.UserID, projectID).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if row.Status != StatusQueued && row.Status != StatusRunning {
			return nil
		}
		now := time.Now().UTC()
		updates := map[string]any{"cancel_requested": true, "updated_at": now}
		if row.Status == StatusQueued || row.UpdatedAt.Before(now.Add(-claimLeaseDuration)) {
			updates["status"] = StatusInterrupted
			updates["claim_token"] = ""
			updates["error_code"] = "generation_cancelled"
			updates["error_message"] = "生成任务已取消。"
			updates["retryable"] = false
		}
		result := tx.Model(&generationRow{}).Where("id = ? AND tenant_id = ? AND actor_id = ? AND project_id = ? AND status = ? AND claim_token = ?",
			runID, actor.TenantID, actor.UserID, projectID, row.Status, row.ClaimToken).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrRunUnavailable
		}
		row.CancelRequested = true
		row.UpdatedAt = now
		if status, ok := updates["status"].(Status); ok {
			row.Status = status
			row.ClaimToken, row.ErrorCode, row.ErrorMessage, row.Retryable = "", "generation_cancelled", "生成任务已取消。", false
		}
		return nil
	})
	return runFromRow(row), err
}

func (s *SQLiteRepository) Complete(ctx context.Context, runID, claimToken string, candidate candidateadoption.Candidate) (Run, error) {
	var row generationRow
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", runID).First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if row.CancelRequested {
			return ErrGenerationCancelled
		}
		if row.Status != StatusRunning || row.ClaimToken != claimToken || row.ProjectID != candidate.ProjectID || row.ChapterID != candidate.ChapterID || candidate.RunID != row.ID {
			return ErrRunUnavailable
		}
		if err := candidateadoption.NewSQLiteCandidateAdoptionStore(tx).UpsertCandidate(ctx, candidate); err != nil {
			return err
		}
		result := tx.Model(&generationRow{}).Where("id = ? AND status = ? AND claim_token = ? AND cancel_requested = ?", runID, StatusRunning, claimToken, false).Updates(map[string]any{
			"status": StatusSucceeded, "claim_token": "", "candidate_id": candidate.ID,
			"error_code": "", "error_message": "", "retryable": false, "updated_at": time.Now().UTC(),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			var latest generationRow
			if err := tx.Where("id = ?", runID).First(&latest).Error; err == nil && latest.CancelRequested {
				return ErrGenerationCancelled
			}
			return ErrRunUnavailable
		}
		row.Status, row.ClaimToken, row.CandidateID = StatusSucceeded, "", &candidate.ID
		row.ErrorCode, row.ErrorMessage, row.Retryable = "", "", false
		return nil
	})
	return runFromRow(row), err
}

func (s *SQLiteRepository) Fail(ctx context.Context, runID, claimToken string, status Status, failure RunError) (Run, error) {
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
		if row.Status != StatusRunning || row.ClaimToken != claimToken {
			return ErrRunUnavailable
		}
		if row.CancelRequested {
			status = StatusInterrupted
			failure = RunError{Code: "generation_cancelled", Message: "生成任务已取消。", Retryable: false}
		}
		result := tx.Model(&generationRow{}).Where("id = ? AND status = ? AND claim_token = ?", runID, StatusRunning, claimToken).Updates(map[string]any{
			"status": status, "claim_token": "", "error_code": failure.Code, "error_message": failure.Message,
			"retryable": failure.Retryable, "updated_at": time.Now().UTC(),
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrRunUnavailable
		}
		row.Status, row.ClaimToken, row.ErrorCode, row.ErrorMessage, row.Retryable = status, "", failure.Code, failure.Message, failure.Retryable
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

func (s *SQLiteRepository) ListCandidates(ctx context.Context, projectID, chapterID string) ([]candidateadoption.CandidateSummary, error) {
	return candidateadoption.NewSQLiteCandidateAdoptionStore(s.db).ListCandidates(ctx, projectID, chapterID)
}

func (s *SQLiteRepository) GetCandidate(ctx context.Context, projectID, runID string) (candidateadoption.Candidate, Input, error) {
	var row generationRow
	if err := s.db.WithContext(ctx).Where("id = ? AND project_id = ?", runID, projectID).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return candidateadoption.Candidate{}, Input{}, ErrNotFound
		}
		return candidateadoption.Candidate{}, Input{}, err
	}
	if row.Status != StatusSucceeded || row.CandidateID == nil {
		return candidateadoption.Candidate{}, Input{}, ErrNotFound
	}
	var input Input
	if err := json.Unmarshal([]byte(row.InputJSON), &input); err != nil {
		return candidateadoption.Candidate{}, Input{}, err
	}
	candidate, err := candidateadoption.NewSQLiteCandidateAdoptionStore(s.db).GetCandidate(ctx, projectID, *row.CandidateID)
	return candidate, input, err
}

func runFromRow(row generationRow) Run {
	run := Run{ID: row.ID, ProjectID: row.ProjectID, ChapterID: row.ChapterID, Status: row.Status,
		CandidateID: row.CandidateID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ClaimToken: row.ClaimToken}
	if row.ErrorCode != "" {
		run.Error = &RunError{Code: row.ErrorCode, Message: row.ErrorMessage, Retryable: row.Retryable}
	}
	return run
}
