package generation

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
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
	DigestVersion   int     `gorm:"column:digest_version;not null;default:1"`
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

// generationDigestVersion is the digest rule version this build writes.
//
// The version is bumped whenever the digest payload changes shape. A row written
// under an older rule carries a digest this build cannot interpret, so the
// version is compared before the digest is: see compareGenerationDigest.
//
// Version 1 is the rule that hashes the whole request with
// operationcore.RequestHash. The action path adds fields to that request, but
// they are pointers with omitempty: the legacy request payload stays byte
// identical, so the rows that exist today still replay under this version.
const generationDigestVersion = 1

// compareGenerationDigest decides what an existing idempotency row means for
// this request.
//
// The version is compared first, and that order is the whole point. A row
// written under an older digest rule carries a digest this build cannot
// interpret, so the answer is "this key belongs to an older rule" -- the caller
// retries with a new key -- and not "this key carries a different request",
// which would send the caller looking for a request that never changed.
//
// Every place that compares a generation digest goes through this one function:
// the replay path and the two write-transaction paths cannot disagree about the
// same key.
func compareGenerationDigest(row generationRow, digest string) error {
	if row.DigestVersion != generationDigestVersion {
		return ErrIdempotencyKeyExpired
	}
	if row.RequestHash != digest {
		return ErrIdempotencyConflict
	}
	return nil
}

type SQLiteRepository struct{ db *gorm.DB }

func NewSQLiteRepository(db *gorm.DB) *SQLiteRepository { return &SQLiteRepository{db: db} }

// ledgerTableMissing says the ledger read failed because the table is not
// there, not because the read itself went wrong.
//
// A database that predates the model call ledger has no such table, and a
// candidate written there simply has no model to name. That is not a failure
// worth a log line on every write; a read that failed for any other reason is.
func ledgerTableMissing(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "no such table") ||
		strings.Contains(message, "does not exist")
}

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
	if err := compareGenerationDigest(row, requestHash); err != nil {
		return Run{}, false, err
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
			if err := compareGenerationDigest(existing, requestHash); err != nil {
				return err
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
			IdempotencyKey: key, RequestHash: requestHash, DigestVersion: generationDigestVersion,
			Status: StatusQueued, InputJSON: string(inputJSON),
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
		if err := compareGenerationDigest(existing, requestHash); err != nil {
			return err
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
		if candidate.ModelID == "" {
			// 产物 → 模型：候选行自己记下产出它的模型，读产物的人不必再经
			// run → 账本绕一圈才推得出。
			//
			// 事实的来源是**模型调用账本**：那行账本是这个 run 的真实
			// provider 调用记录，它的 model_id 就是产出这个候选的模型。没有
			// 账本行 ⇒ 这次没有 provider 调用（mock 适配器不写账本）⇒ 候选行
			// 的 model_id 就该是空的。两个字段成对：账本有行则候选必有模型，
			// 账本无行则候选必无模型。
			//
			// 读账本失败**不**让这次运行失败：那是改闸门，不在本次范围。缺口
			// 留一行日志，由守护测试在代码路径上钉住「账本有行则候选必带模型」。
			entries, ledgerErr := workspacecore.NewGORMModelCallLedger(tx).
				ModelCallEntriesBySource(ctx, row.TenantID, workspacecore.ModelCallLedgerSourceGenerationRun, runID)
			if ledgerErr != nil && !ledgerTableMissing(ledgerErr) {
				log.Printf("[generation] candidate model id: ledger read failed for run %s: %v", runID, ledgerErr)
			}
			// 账本行按时间升序返回。产出这份候选的是**最后一次**调用：一个 run
			// 里换过模型时，先前那次不是产出者。
			for _, entry := range entries {
				if entry.ModelID != "" {
					candidate.ModelID = entry.ModelID
				}
			}
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
	// The input summary is derived from the stored input on read: the run row
	// keeps the whole input for a retry, and the summary is the part a caller
	// may read back.
	run.InputSummary = inputSummaryFromJSON(row.InputJSON)
	return run
}
