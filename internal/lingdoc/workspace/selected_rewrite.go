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
	var row selectedRewriteRow
	err := s.db.WithContext(ctx).Where("tenant_id = ? AND project_id = ? AND chapter_id = ? AND applied_key = ?",
		tenantID, projectID, chapterID, key).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return selectedRewriteRow{}, ErrRewriteNotFound
	}
	return row, err
}

func (s *GORMSelectedRewriteStore) update(ctx context.Context, row selectedRewriteRow) error {
	return s.db.WithContext(ctx).Model(&selectedRewriteRow{}).Where("id = ?", row.ID).Updates(map[string]any{
		"status": row.Status, "run_mode": row.RunMode, "replacement_markdown": row.ReplacementMarkdown,
		"source_ids_json": row.SourceIDsJSON, "authorized_sources_json": row.AuthorizedSourcesJSON,
		"review_items_json": row.ReviewItemsJSON, "error_code": row.ErrorCode, "applied_key": row.AppliedKey,
		"applied_response_json": row.AppliedResponseJSON, "apply_pending_key": row.ApplyPendingKey,
		"apply_pending_input_json": row.ApplyPendingInputJSON, "updated_at": row.UpdatedAt,
	}).Error
}

func (s *GORMSelectedRewriteStore) claimApply(ctx context.Context, row selectedRewriteRow, key, inputJSON string, now time.Time) (selectedRewriteRow, error) {
	result := s.db.WithContext(ctx).Model(&selectedRewriteRow{}).
		Where("id = ? AND applied_key = '' AND apply_pending_key = '' AND status = 'ready'", row.ID).
		Updates(map[string]any{"apply_pending_key": key, "apply_pending_input_json": inputJSON, "updated_at": now})
	if result.Error != nil {
		return selectedRewriteRow{}, result.Error
	}
	if result.RowsAffected == 1 {
		row.ApplyPendingKey, row.ApplyPendingInputJSON, row.UpdatedAt = key, inputJSON, now
		return row, nil
	}
	return s.byID(ctx, row.TenantID, row.ProjectID, row.ChapterID, row.ID)
}

type SelectedRewriteService struct {
	store     *GORMSelectedRewriteStore
	workspace ApplicationService
	sources   SourceApplicationService
	model     SelectedRewriteModel
	audit     AuditSink
	now       func() time.Time
}

func NewSelectedRewriteService(db *gorm.DB, workspace ApplicationService, sources SourceApplicationService, model SelectedRewriteModel, audits ...AuditSink) *SelectedRewriteService {
	var audit AuditSink
	if len(audits) > 0 {
		audit = audits[0]
	}
	return &SelectedRewriteService{store: NewGORMSelectedRewriteStore(db), workspace: workspace, sources: sources, model: model, audit: audit, now: func() time.Time { return time.Now().UTC() }}
}

func (s *SelectedRewriteService) recordAudit(ctx context.Context, actor Actor, projectID, capability, decision, reason string, details map[string]any) {
	if s == nil || s.audit == nil {
		return
	}
	_ = s.audit.Record(ctx, AuditEvent{TenantID: actor.TenantID, UserID: actor.UserID, Role: actor.Role,
		ProjectID: projectID, Capability: capability, Decision: decision, Reason: reason, Details: details})
}

var rewriteCitationMarker = regexp.MustCompile(`\[\[source:([A-Za-z0-9_-]+)\]\]`)

func (s *SelectedRewriteService) Create(ctx context.Context, actor Actor, projectID, chapterID, key string, input SelectedRewriteRequest) (SelectedRewriteCandidate, error) {
	auditDetails := map[string]any{"chapter_id": chapterID, "idempotency_key": key, "spec_revision": input.ExpectedSpecRevision,
		"working_copy_revision": input.ExpectedWorkingCopyRevision}
	auditDecision := "allow"
	auditReason := ""
	defer func() {
		s.recordAudit(ctx, actor, projectID, "selected_rewrite.create", auditDecision, auditReason, auditDetails)
	}()
	if s == nil || s.store == nil || s.workspace == nil || s.sources == nil || s.model == nil ||
		actor.TenantID == 0 || strings.TrimSpace(actor.UserID) == "" || strings.TrimSpace(projectID) == "" || strings.TrimSpace(chapterID) == "" {
		auditDecision, auditReason = "deny", ErrRewriteUnavailable.Error()
		return SelectedRewriteCandidate{}, ErrRewriteUnavailable
	}
	key = strings.TrimSpace(key)
	if err := validateSelectedRewriteInput(key, input); err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return SelectedRewriteCandidate{}, err
	}
	input.Instruction = strings.TrimSpace(input.Instruction)
	input.SourceIDs = slices.Clone(input.SourceIDs)
	if err := s.workspace.Authorize(ctx, actor, projectID, "write"); err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return SelectedRewriteCandidate{}, err
	}
	project, err := s.workspace.GetProject(ctx, actor, projectID)
	if err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return SelectedRewriteCandidate{}, selectedRewriteContextError(err)
	}
	copy, err := s.workspace.GetWorkingCopy(ctx, actor, projectID, chapterID)
	if err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return SelectedRewriteCandidate{}, selectedRewriteContextError(err)
	}
	if project.Status != "active" || project.SpecRevision != input.ExpectedSpecRevision ||
		copy.SpecRevision != input.ExpectedSpecRevision || copy.WorkingCopyRevision != input.ExpectedWorkingCopyRevision ||
		!sameOptionalVersion(copy.BaseChapterVersionID, input.BaseChapterVersionID) {
		auditDecision, auditReason = "deny", ErrRewriteConflict.Error()
		return SelectedRewriteCandidate{}, ErrRewriteConflict
	}
	_, _, before, after, ok := selectedRewriteContextWindow(copy.BodyMarkdown, input.Selection)
	if !ok {
		auditDecision, auditReason = "deny", ErrRewriteStale.Error()
		return SelectedRewriteCandidate{}, ErrRewriteStale
	}
	sources, snapshots, err := s.fetchSources(ctx, actor, projectID, input.SourceIDs)
	if err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return SelectedRewriteCandidate{}, err
	}
	mode := s.model.RunMode()
	if !validRewriteMode(mode) {
		auditDecision, auditReason = "deny", ErrRewriteUnavailable.Error()
		return SelectedRewriteCandidate{}, ErrRewriteUnavailable
	}
	hash, err := selectedRewriteRequestHash(input)
	if err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return SelectedRewriteCandidate{}, err
	}
	seed, err := s.createRow(actor, projectID, chapterID, key, input, hash, snapshots)
	if err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return SelectedRewriteCandidate{}, err
	}
	seed.RunMode = mode
	row, created, err := s.store.createOrReplay(ctx, seed)
	if err != nil {
		return SelectedRewriteCandidate{}, err
	}
	if !created {
		row, err = s.staleIfNeeded(ctx, actor, projectID, chapterID, row)
		if err != nil {
			auditDecision, auditReason = "deny", err.Error()
			return SelectedRewriteCandidate{}, selectedRewriteContextError(err)
		}
		if row.Status == "ready" && s.recheckSources(ctx, actor, projectID, mustDecodeSnapshots(row.AuthorizedSourcesJSON)) != nil {
			auditDecision, auditReason = "deny", ErrRewriteSourceDenied.Error()
			return SelectedRewriteCandidate{}, ErrRewriteSourceDenied
		}
		auditDetails["candidate_id"], auditDetails["run_mode"], auditDetails["status"] = row.ID, row.RunMode, row.Status
		return selectedRewriteView(row)
	}
	if err := s.store.update(ctx, row); err != nil {
		auditDecision, auditReason = "deny", err.Error()
		return SelectedRewriteCandidate{}, err
	}
	auditDetails["candidate_id"] = row.ID
	auditDetails["run_mode"] = row.RunMode
	output, modelErr := s.model.RewriteSelected(ctx, actor, SelectedRewritePrompt{
		Selection: input.Selection, Instruction: input.Instruction, ContextBefore: before, ContextAfter: after, Sources: sources,
	})
	if modelErr != nil {
		row.Status, row.ErrorCode, row.ReplacementMarkdown = "failed", "rewrite_failed", ""
		auditDecision, auditReason = "deny", row.ErrorCode
		auditDetails["status"], auditDetails["run_mode"] = row.Status, row.RunMode
		return s.updateCandidate(context.WithoutCancel(ctx), row)
	}
	if output.RunMode != "" {
		if !validRewriteMode(output.RunMode) {
			row.Status, row.ErrorCode, row.ReplacementMarkdown = "failed", "invalid_run_mode", ""
			auditDecision, auditReason = "deny", row.ErrorCode
			auditDetails["status"], auditDetails["run_mode"] = row.Status, row.RunMode
			return s.updateCandidate(context.WithoutCancel(ctx), row)
		}
		row.RunMode = output.RunMode
	}
	if err := validateRewriteOutput(output, input.SourceIDs); err != nil {
		row.Status, row.ErrorCode, row.ReplacementMarkdown = "failed", selectedRewriteFailureCode(err), ""
		auditDecision, auditReason = "deny", row.ErrorCode
		auditDetails["status"], auditDetails["run_mode"] = row.Status, row.RunMode
		return s.updateCandidate(context.WithoutCancel(ctx), row)
	}
	if err := s.recheckSources(ctx, actor, projectID, snapshots); err != nil {
		row.Status, row.ErrorCode, row.ReplacementMarkdown = "stale", "source_access_denied", ""
		auditDecision, auditReason = "deny", row.ErrorCode
		auditDetails["status"], auditDetails["run_mode"] = row.Status, row.RunMode
		return s.updateCandidate(context.WithoutCancel(ctx), row)
	}
	if err := s.workspace.Authorize(ctx, actor, projectID, "write"); err != nil {
		row.Status, row.ErrorCode, row.ReplacementMarkdown = "stale", "authorization_changed", ""
		auditDecision, auditReason = "deny", row.ErrorCode
		auditDetails["status"], auditDetails["run_mode"] = row.Status, row.RunMode
		return s.updateCandidate(context.WithoutCancel(ctx), row)
	}
	currentCopy, err := s.workspace.GetWorkingCopy(ctx, actor, projectID, chapterID)
	if err != nil || !workingCopyIsCandidateBasis(currentCopy, row) {
		row.Status, row.ErrorCode, row.ReplacementMarkdown = "stale", "stale_input", ""
		auditDecision, auditReason = "deny", row.ErrorCode
		auditDetails["status"], auditDetails["run_mode"] = row.Status, row.RunMode
		return s.updateCandidate(context.WithoutCancel(ctx), row)
	}
	if err := writeRewriteResult(&row, output); err != nil {
		row.Status, row.ErrorCode, row.ReplacementMarkdown = "failed", selectedRewriteFailureCode(err), ""
		auditDecision, auditReason = "deny", row.ErrorCode
		auditDetails["status"], auditDetails["run_mode"] = row.Status, row.RunMode
		return s.updateCandidate(context.WithoutCancel(ctx), row)
	}
	auditDetails["status"], auditDetails["run_mode"] = row.Status, row.RunMode
	return s.updateCandidate(context.WithoutCancel(ctx), row)
}

func (s *SelectedRewriteService) Get(ctx context.Context, actor Actor, projectID, candidateID string) (result SelectedRewriteCandidate, err error) {
	details := map[string]any{"candidate_id": candidateID}
	defer func() {
		decision, reason := "allow", ""
		if err != nil {
			decision, reason = "deny", err.Error()
		}
		s.recordAudit(ctx, actor, projectID, "selected_rewrite.get", decision, reason, details)
	}()
	if s == nil || s.store == nil || s.workspace == nil || s.sources == nil || actor.TenantID == 0 {
		return SelectedRewriteCandidate{}, ErrRewriteUnavailable
	}
	if err := s.workspace.Authorize(ctx, actor, projectID, "read"); err != nil {
		return SelectedRewriteCandidate{}, err
	}
	row, err := s.store.byID(ctx, actor.TenantID, projectID, "", candidateID)
	if err != nil {
		return SelectedRewriteCandidate{}, err
	}
	details["run_mode"], details["status"] = row.RunMode, row.Status
	details["spec_revision"], details["working_copy_revision"] = row.SpecRevision, row.WorkingCopyRevision
	if row.ApplyPendingKey == "" {
		row, err = s.staleIfNeeded(ctx, actor, projectID, row.ChapterID, row)
		if err != nil {
			return SelectedRewriteCandidate{}, selectedRewriteContextError(err)
		}
	}
	snapshots := mustDecodeSnapshots(row.AuthorizedSourcesJSON)
	if len(snapshots) > 0 {
		if err := s.recheckSources(ctx, actor, projectID, snapshots); err != nil {
			return SelectedRewriteCandidate{}, ErrRewriteSourceDenied
		}
	}
	return selectedRewriteView(row)
}

func (s *SelectedRewriteService) Apply(ctx context.Context, actor Actor, projectID, chapterID, candidateID, key string, input ApplySelectedRewriteInput) (result core.WorkingCopy, replayed bool, err error) {
	details := map[string]any{"chapter_id": chapterID, "candidate_id": candidateID, "idempotency_key": key,
		"spec_revision": input.ExpectedSpecRevision, "working_copy_revision": input.ExpectedWorkingCopyRevision}
	defer func() {
		decision, reason := "allow", ""
		if err != nil {
			decision, reason = "deny", err.Error()
		}
		s.recordAudit(ctx, actor, projectID, "selected_rewrite.apply", decision, reason, details)
	}()
	if s == nil || s.store == nil || s.workspace == nil || s.sources == nil || actor.TenantID == 0 || len(key) < 8 || len(key) > 128 {
		return core.WorkingCopy{}, false, ErrRewriteInvalidRequest
	}
	if err := s.workspace.Authorize(ctx, actor, projectID, "write"); err != nil {
		return core.WorkingCopy{}, false, err
	}
	row, err := s.store.byID(ctx, actor.TenantID, projectID, chapterID, candidateID)
	if err != nil {
		return core.WorkingCopy{}, false, err
	}
	details["run_mode"], details["status"] = row.RunMode, row.Status
	details["base_chapter_version_id"] = row.BaseChapterVersionID
	snapshots := mustDecodeSnapshots(row.AuthorizedSourcesJSON)
	if err := s.recheckSources(ctx, actor, projectID, snapshots); err != nil {
		return core.WorkingCopy{}, false, ErrRewriteSourceDenied
	}
	if row.AppliedKey == key && row.AppliedResponseJSON != "" {
		if input.ExpectedSpecRevision != row.SpecRevision || input.ExpectedWorkingCopyRevision != row.WorkingCopyRevision ||
			!sameOptionalVersion(input.BaseChapterVersionID, row.BaseChapterVersionID) {
			return core.WorkingCopy{}, false, ErrRewriteKeyConflict
		}
		var saved core.WorkingCopy
		if err := json.Unmarshal([]byte(row.AppliedResponseJSON), &saved); err != nil {
			return core.WorkingCopy{}, false, err
		}
		return saved, true, nil
	}
	if row.ApplyPendingKey != "" {
		if row.ApplyPendingKey != key {
			return core.WorkingCopy{}, false, ErrRewriteKeyConflict
		}
		if input.ExpectedSpecRevision != row.SpecRevision || input.ExpectedWorkingCopyRevision != row.WorkingCopyRevision ||
			!sameOptionalVersion(input.BaseChapterVersionID, row.BaseChapterVersionID) {
			return core.WorkingCopy{}, false, ErrRewriteKeyConflict
		}
		return s.completePendingApply(ctx, actor, projectID, chapterID, key, row)
	}
	if row.Status != "ready" || row.ReplacementMarkdown == "" {
		return core.WorkingCopy{}, false, ErrRewriteStale
	}
	project, err := s.workspace.GetProject(ctx, actor, projectID)
	if err != nil {
		return core.WorkingCopy{}, false, selectedRewriteContextError(err)
	}
	copy, err := s.workspace.GetWorkingCopy(ctx, actor, projectID, chapterID)
	if err != nil {
		return core.WorkingCopy{}, false, selectedRewriteContextError(err)
	}
	if project.Status != "active" || project.SpecRevision != input.ExpectedSpecRevision ||
		input.ExpectedSpecRevision != row.SpecRevision || input.ExpectedWorkingCopyRevision != row.WorkingCopyRevision ||
		!sameOptionalVersion(input.BaseChapterVersionID, row.BaseChapterVersionID) || !workingCopyIsCandidateBasis(copy, row) {
		return core.WorkingCopy{}, false, ErrRewriteStale
	}
	start, end, ok := selectionByteRange(copy.BodyMarkdown, RewriteSelection{
		StartUTF16: row.StartUTF16, EndUTF16: row.EndUTF16, SelectedText: row.SelectedText,
	})
	if !ok {
		return core.WorkingCopy{}, false, ErrRewriteStale
	}
	body := copy.BodyMarkdown[:start] + row.ReplacementMarkdown + copy.BodyMarkdown[end:]
	refs := make([]string, 0)
	for _, marker := range rewriteCitationMarker.FindAllStringSubmatch(body, -1) {
		refs = append(refs, marker[1])
	}
	if strings.Count(body, "[[source:") != len(refs) {
		return core.WorkingCopy{}, false, ErrRewriteInvalidRequest
	}
	reviewItems := mustDecodeReviewItems(row.ReviewItemsJSON)
	writeInput := core.SaveWorkingCopyInput{BaseChapterVersionID: row.BaseChapterVersionID,
		ExpectedSpecRevision: project.SpecRevision, ExpectedWorkingCopyRevision: copy.WorkingCopyRevision,
		BodyMarkdown: body, SourceIDs: stringSet(refs)}
	pendingJSON, err := json.Marshal(selectedRewritePendingApply{WorkingCopy: writeInput, ReviewItems: reviewItems})
	if err != nil {
		return core.WorkingCopy{}, false, err
	}
	row, err = s.store.claimApply(ctx, row, key, string(pendingJSON), s.now())
	if err != nil {
		return core.WorkingCopy{}, false, err
	}
	if row.ApplyPendingKey != key {
		if row.AppliedKey == key && row.AppliedResponseJSON != "" {
			var saved core.WorkingCopy
			if err := json.Unmarshal([]byte(row.AppliedResponseJSON), &saved); err != nil {
				return core.WorkingCopy{}, false, err
			}
			return saved, true, nil
		}
		return core.WorkingCopy{}, false, ErrRewriteKeyConflict
	}
	return s.completePendingApply(ctx, actor, projectID, chapterID, key, row)
}

type selectedRewritePendingApply struct {
	WorkingCopy core.SaveWorkingCopyInput `json:"working_copy"`
	ReviewItems []core.ReviewItem         `json:"review_items"`
}

func (s *SelectedRewriteService) completePendingApply(ctx context.Context, actor Actor, projectID, chapterID, key string, row selectedRewriteRow) (core.WorkingCopy, bool, error) {
	var pending selectedRewritePendingApply
	if err := json.Unmarshal([]byte(row.ApplyPendingInputJSON), &pending); err != nil {
		return core.WorkingCopy{}, false, err
	}
	operationKey := operationKeyForApply(key, row.ID)
	raw, _, replayed, err := s.workspace.ApplyRewrite(ctx, actor, projectID, chapterID, operationKey, pending.WorkingCopy, pending.ReviewItems)
	if err != nil {
		if errors.Is(err, core.ErrVersionConflict) {
			row.Status, row.ReplacementMarkdown, row.ErrorCode = "stale", "", "stale_input"
			row.ApplyPendingKey, row.ApplyPendingInputJSON, row.UpdatedAt = "", "", s.now()
			if persistErr := s.store.update(context.WithoutCancel(ctx), row); persistErr != nil {
				return core.WorkingCopy{}, false, persistErr
			}
		}
		return core.WorkingCopy{}, false, selectedRewriteContextError(err)
	}
	var saved core.WorkingCopy
	if err := json.Unmarshal(raw, &saved); err != nil {
		return core.WorkingCopy{}, false, err
	}
	row.AppliedKey, row.AppliedResponseJSON, row.ApplyPendingKey, row.ApplyPendingInputJSON, row.UpdatedAt = key, string(raw), "", "", s.now()
	if err := s.store.update(context.WithoutCancel(ctx), row); err != nil {
		return core.WorkingCopy{}, false, err
	}
	return saved, replayed, nil
}

func selectedRewriteView(row selectedRewriteRow) (SelectedRewriteCandidate, error) {
	var sourceIDs []string
	var snapshots []RewriteSourceSnapshot
	var reviewItems []core.ReviewItem
	if err := json.Unmarshal([]byte(row.SourceIDsJSON), &sourceIDs); err != nil {
		return SelectedRewriteCandidate{}, err
	}
	if err := json.Unmarshal([]byte(row.AuthorizedSourcesJSON), &snapshots); err != nil {
		return SelectedRewriteCandidate{}, err
	}
	if err := json.Unmarshal([]byte(row.ReviewItemsJSON), &reviewItems); err != nil {
		return SelectedRewriteCandidate{}, err
	}
	if sourceIDs == nil {
		sourceIDs = []string{}
	}
	if snapshots == nil {
		snapshots = []RewriteSourceSnapshot{}
	}
	if reviewItems == nil {
		reviewItems = []core.ReviewItem{}
	}
	status := row.Status
	if row.ApplyPendingKey != "" && row.AppliedKey == "" {
		status = "applying"
	}
	return SelectedRewriteCandidate{CandidateID: row.ID, ProjectID: row.ProjectID, ChapterID: row.ChapterID,
		Status: status, RunMode: row.RunMode, BaseChapterVersionID: row.BaseChapterVersionID,
		SpecRevision: row.SpecRevision, WorkingCopyRevision: row.WorkingCopyRevision,
		Selection:           RewriteSelection{StartUTF16: row.StartUTF16, EndUTF16: row.EndUTF16, SelectedText: row.SelectedText},
		ReplacementMarkdown: row.ReplacementMarkdown, SourceIDs: sourceIDs, AuthorizedSources: snapshots,
		ReviewItems: reviewItems, ErrorCode: row.ErrorCode, CreatedAt: row.CreatedAt}, nil
}

func stringSet(values []string) []string {
	result := slices.Clone(values)
	sort.Strings(result)
	return slices.Compact(result)
}

func selectionByteRange(body string, selection RewriteSelection) (int, int, bool) {
	if selection.StartUTF16 < 0 || selection.EndUTF16 <= selection.StartUTF16 || selection.SelectedText == "" {
		return 0, 0, false
	}
	units := utf16.Encode([]rune(body))
	if selection.EndUTF16 > len(units) {
		return 0, 0, false
	}
	selected := string(utf16.Decode(units[selection.StartUTF16:selection.EndUTF16]))
	if selected != selection.SelectedText {
		return 0, 0, false
	}
	start, end := -1, -1
	unitOffset, byteOffset := 0, 0
	for _, r := range body {
		if unitOffset == selection.StartUTF16 {
			start = byteOffset
		}
		unitOffset += len(utf16.Encode([]rune{r}))
		byteOffset += len(string(r))
		if unitOffset == selection.EndUTF16 {
			end = byteOffset
		}
	}
	if selection.StartUTF16 == 0 {
		start = 0
	}
	if selection.EndUTF16 == len(units) {
		end = len(body)
	}
	return start, end, start >= 0 && end >= start
}

func selectedRewriteContext(body string, start, end int, maxRunes int) (string, string) {
	beforeRunes, afterRunes := []rune(body[:start]), []rune(body[end:])
	if len(beforeRunes) > maxRunes {
		beforeRunes = beforeRunes[len(beforeRunes)-maxRunes:]
	}
	if len(afterRunes) > maxRunes {
		afterRunes = afterRunes[:maxRunes]
	}
	return string(beforeRunes), string(afterRunes)
}

func sourceSnapshots(sources []evidence.Source, now time.Time) []RewriteSourceSnapshot {
	result := make([]RewriteSourceSnapshot, 0, len(sources))
	for _, source := range sources {
		result = append(result, RewriteSourceSnapshot{SourceID: source.ID, AssetID: source.AssetID,
			AssetRevision: source.AssetRevision, Locator: source.Locator, QuotedTextHash: source.QuotedTextHash, AuthorizedAt: now})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].SourceID < result[j].SourceID })
	return result
}

func validRewriteMode(mode string) bool {
	return mode == "mock" || mode == "real_api_fake_model" || mode == "real"
}

func validateRewriteOutput(output SelectedRewriteOutput, requested []string) error {
	if strings.TrimSpace(output.ReplacementMarkdown) == "" || len(output.ReplacementMarkdown) > 40000 ||
		(output.RunMode != "" && !validRewriteMode(output.RunMode)) {
		return ErrRewriteInvalidRequest
	}
	allowed := make(map[string]bool, len(requested))
	for _, id := range requested {
		allowed[id] = true
	}
	refs := make([]string, 0)
	for _, item := range rewriteCitationMarker.FindAllStringSubmatch(output.ReplacementMarkdown, -1) {
		refs = append(refs, item[1])
	}
	if strings.Count(output.ReplacementMarkdown, "[[source:") != len(refs) {
		return ErrRewriteInvalidRequest
	}
	for _, id := range output.SourceIDs {
		if !allowed[id] {
			return ErrRewriteSourceDenied
		}
	}
	if !slices.Equal(stringSet(refs), stringSet(output.SourceIDs)) {
		return ErrRewriteInvalidRequest
	}
	for _, statement := range output.ReviewItems {
		if strings.TrimSpace(statement) == "" {
			return ErrRewriteInvalidRequest
		}
	}
	return nil
}

func (s *SelectedRewriteService) fetchSources(ctx context.Context, actor Actor, projectID string, ids []string) ([]evidence.Source, []RewriteSourceSnapshot, error) {
	if len(ids) > 12 || !slices.Equal(ids, stringSet(ids)) {
		return nil, nil, ErrRewriteInvalidRequest
	}
	items := make([]evidence.Source, 0, len(ids))
	for _, id := range ids {
		source, err := s.sources.GetSource(ctx, actor, projectID, id)
		if err != nil || source.ProjectID != projectID || source.Status != evidence.SourceAvailable {
			return nil, nil, ErrRewriteSourceDenied
		}
		items = append(items, source)
	}
	return items, sourceSnapshots(items, s.now()), nil
}

func (s *SelectedRewriteService) recheckSources(ctx context.Context, actor Actor, projectID string, snapshots []RewriteSourceSnapshot) error {
	for _, snapshot := range snapshots {
		source, err := s.sources.GetSource(ctx, actor, projectID, snapshot.SourceID)
		if err != nil || source.ProjectID != projectID || source.Status != evidence.SourceAvailable ||
			source.AssetID != snapshot.AssetID || source.AssetRevision != snapshot.AssetRevision ||
			source.Locator != snapshot.Locator || source.QuotedTextHash != snapshot.QuotedTextHash {
			return ErrRewriteSourceDenied
		}
	}
	return nil
}

func workingCopyIsCandidateBasis(copy core.WorkingCopy, row selectedRewriteRow) bool {
	return copy.WorkingCopyRevision == row.WorkingCopyRevision && copy.SpecRevision == row.SpecRevision &&
		sameOptionalVersion(copy.BaseChapterVersionID, row.BaseChapterVersionID)
}

func sameOptionalVersion(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (s *SelectedRewriteService) staleIfNeeded(ctx context.Context, actor Actor, projectID, chapterID string, row selectedRewriteRow) (selectedRewriteRow, error) {
	copy, err := s.workspace.GetWorkingCopy(ctx, actor, projectID, chapterID)
	if err != nil {
		return selectedRewriteRow{}, err
	}
	project, err := s.workspace.GetProject(ctx, actor, projectID)
	if err != nil {
		return selectedRewriteRow{}, err
	}
	if !workingCopyIsCandidateBasis(copy, row) || project.SpecRevision != row.SpecRevision {
		row.Status, row.ReplacementMarkdown = "stale", ""
		row.ErrorCode, row.UpdatedAt = "stale_input", s.now()
		if err := s.store.update(ctx, row); err != nil {
			return selectedRewriteRow{}, err
		}
	}
	return row, nil
}

func (s *SelectedRewriteService) createRow(actor Actor, projectID, chapterID, key string, input SelectedRewriteRequest, hash string, sources []RewriteSourceSnapshot) (selectedRewriteRow, error) {
	sourceJSON, _ := json.Marshal(input.SourceIDs)
	snapshotJSON, _ := json.Marshal(sources)
	row := selectedRewriteRow{ID: uuid.NewString(), TenantID: actor.TenantID, ActorID: actor.UserID,
		ProjectID: projectID, ChapterID: chapterID, IdempotencyKey: key, RequestHash: hash,
		Status: "queued", RunMode: "", BaseChapterVersionID: input.BaseChapterVersionID,
		SpecRevision: input.ExpectedSpecRevision, WorkingCopyRevision: input.ExpectedWorkingCopyRevision,
		StartUTF16: input.Selection.StartUTF16, EndUTF16: input.Selection.EndUTF16, SelectedText: input.Selection.SelectedText,
		Instruction: input.Instruction, SourceIDsJSON: string(sourceJSON), AuthorizedSourcesJSON: string(snapshotJSON),
		ReviewItemsJSON: "[]", CreatedAt: s.now(), UpdatedAt: s.now()}
	return row, nil
}

func writeRewriteResult(row *selectedRewriteRow, output SelectedRewriteOutput) error {
	if err := validateRewriteOutput(output, mustDecodeIDs(row.SourceIDsJSON)); err != nil {
		return err
	}
	items := make([]core.ReviewItem, 0, len(output.ReviewItems))
	for _, statement := range output.ReviewItems {
		items = append(items, core.ReviewItem{ID: uuid.NewString(), Statement: strings.TrimSpace(statement), OriginCandidateID: row.ID})
	}
	ids := stringSet(output.SourceIDs)
	idsJSON, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	allowedSnapshots := make(map[string]RewriteSourceSnapshot)
	for _, snapshot := range mustDecodeSnapshots(row.AuthorizedSourcesJSON) {
		allowedSnapshots[snapshot.SourceID] = snapshot
	}
	usedSnapshots := make([]RewriteSourceSnapshot, 0, len(ids))
	for _, id := range ids {
		snapshot, ok := allowedSnapshots[id]
		if !ok {
			return ErrRewriteSourceDenied
		}
		usedSnapshots = append(usedSnapshots, snapshot)
	}
	snapshotsJSON, err := json.Marshal(usedSnapshots)
	if err != nil {
		return err
	}
	review, err := json.Marshal(items)
	if err != nil {
		return err
	}
	row.ReplacementMarkdown, row.SourceIDsJSON, row.AuthorizedSourcesJSON, row.ReviewItemsJSON = output.ReplacementMarkdown, string(idsJSON), string(snapshotsJSON), string(review)
	row.Status, row.RunMode, row.ErrorCode = "ready", output.RunMode, ""
	return nil
}

func mustDecodeIDs(raw string) []string {
	var ids []string
	_ = json.Unmarshal([]byte(raw), &ids)
	return ids
}

func mustDecodeSnapshots(raw string) []RewriteSourceSnapshot {
	var snapshots []RewriteSourceSnapshot
	_ = json.Unmarshal([]byte(raw), &snapshots)
	return snapshots
}

func mustDecodeReviewItems(raw string) []core.ReviewItem {
	var items []core.ReviewItem
	_ = json.Unmarshal([]byte(raw), &items)
	return items
}

func (s *SelectedRewriteService) updateCandidate(ctx context.Context, row selectedRewriteRow) (SelectedRewriteCandidate, error) {
	if err := s.store.update(ctx, row); err != nil {
		return SelectedRewriteCandidate{}, err
	}
	return selectedRewriteView(row)
}

func selectedRewriteRequestHash(input SelectedRewriteRequest) (string, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", ErrRewriteInvalidRequest
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func selectedRewriteFailureCode(err error) string {
	switch {
	case errors.Is(err, ErrRewriteSourceDenied):
		return "source_access_denied"
	case errors.Is(err, ErrRewriteStale):
		return "stale_input"
	case errors.Is(err, ErrRewriteUnavailable):
		return "model_unavailable"
	default:
		return "rewrite_failed"
	}
}

func selectedRewriteContextError(err error) error {
	if errors.Is(err, core.ErrNotFound) {
		return ErrRewriteNotFound
	}
	if errors.Is(err, core.ErrVersionConflict) {
		return ErrRewriteConflict
	}
	if errors.Is(err, core.ErrSourceUnavailable) {
		return ErrRewriteSourceDenied
	}
	return err
}

func validateSelectedRewriteInput(key string, in SelectedRewriteRequest) error {
	if len(key) < 8 || len(key) > 128 || in.ExpectedSpecRevision < 0 || in.ExpectedWorkingCopyRevision < 1 ||
		strings.TrimSpace(in.Instruction) == "" || len([]rune(in.Instruction)) > 2000 || len(in.SourceIDs) > 12 ||
		in.Selection.EndUTF16 <= in.Selection.StartUTF16 || in.Selection.StartUTF16 < 0 || in.Selection.SelectedText == "" {
		return ErrRewriteInvalidRequest
	}
	ids := slices.Clone(in.SourceIDs)
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			return ErrRewriteInvalidRequest
		}
	}
	if !slices.Equal(ids, stringSet(ids)) {
		return ErrRewriteInvalidRequest
	}
	return nil
}

func operationKeyForApply(key, candidateID string) string {
	digest := sha256.Sum256([]byte(candidateID + "\x00" + key))
	return "g7-apply-" + hex.EncodeToString(digest[:])
}

func selectedRewriteContextWindow(body string, selection RewriteSelection) (int, int, string, string, bool) {
	start, end, ok := selectionByteRange(body, selection)
	if !ok {
		return 0, 0, "", "", false
	}
	before, after := selectedRewriteContext(body, start, end, 500)
	return start, end, before, after, true
}

func (s *SelectedRewriteService) String() string {
	return fmt.Sprintf("selected-rewrite(%T)", s.model)
}
