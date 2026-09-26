// Package generation owns the asynchronous generation task and candidate
// lifecycle. Provider, source, and workspace details are supplied through
// narrow interfaces so credentials and mutable provider state stay outside
// the task contract.
package generation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/google/uuid"
)

var (
	ErrInvalidRequest        = errors.New("invalid_request")
	ErrNotFound              = errors.New("not_found")
	ErrForbidden             = errors.New("forbidden")
	ErrVersionConflict       = errors.New("version_conflict")
	ErrSourceAccessDenied    = errors.New("source_access_denied")
	ErrSourceStale           = errors.New("source_stale")
	ErrStaleInput            = errors.New("stale_input")
	ErrIdempotencyConflict   = errors.New("idempotency_conflict")
	ErrRunUnavailable        = errors.New("generation_run_unavailable")
	ErrGenerationCancelled   = errors.New("generation_cancelled")
	ErrDependencyUnavailable = errors.New("dependency_unavailable")
)

type Status string

const (
	StatusQueued           Status = "queued"
	StatusRunning          Status = "running"
	StatusSucceeded        Status = "succeeded"
	StatusFailed           Status = "failed"
	StatusInterrupted      Status = "interrupted"
	claimLeaseDuration            = 5 * time.Minute
	claimHeartbeatInterval        = 30 * time.Second
)

type RunError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type Run struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"project_id"`
	ChapterID   string    `json:"chapter_id"`
	Status      Status    `json:"status"`
	CandidateID *string   `json:"candidate_id"`
	Error       *RunError `json:"error,omitempty"`
	CreatedAt   time.Time `json:"-"`
	UpdatedAt   time.Time `json:"-"`
	Replayed    bool      `json:"-"`
	ClaimToken  string    `json:"-"`
}

// Actor is the authenticated owner identity passed from the HTTP boundary.
type Actor struct {
	TenantID uint64 `json:"tenant_id"`
	UserID   string `json:"user_id"`
}

// Source contains only the verified source fields required by generation.
// Provider-specific anchors remain behind SourceValidator.
type Source struct {
	ID            string `json:"id"`
	ProjectID     string `json:"project_id"`
	AssetID       string `json:"asset_id"`
	AssetRevision int    `json:"asset_revision"`
	Locator       string `json:"locator"`
	QuotedText    string `json:"quoted_text"`
	Status        string `json:"status"`
	// Anchor is an internal-only verification snapshot. It is stored with the
	// queued input so the T09 source policy can re-check the exact coordinates
	// after generation; generation inputs are never returned by the HTTP API.
	Anchor evidence.Anchor `json:"anchor"`
}

type Request struct {
	ChapterID                string   `json:"chapter_id"`
	AssetIDs                 []string `json:"asset_ids"`
	Instruction              string   `json:"instruction"`
	ExpectedSpecRevision     int      `json:"expected_spec_revision"`
	ExpectedChapterVersionID *string  `json:"expected_chapter_version_id"`
}

// Input is the immutable, server-resolved generation basis captured before
// queueing. The client never supplies project spec, template, or source text.
type Input struct {
	Actor       Actor                               `json:"actor"`
	Workspace   candidateadoption.GenerationContext `json:"workspace"`
	Basis       candidateadoption.Basis             `json:"basis"`
	ProjectSpec map[string]string                   `json:"project_spec"`
	Sources     []Source                            `json:"sources"`
	Request     Request                             `json:"request"`
}

type Draft struct {
	BodyMarkdown string                         `json:"body_markdown"`
	Sources      []Source                       `json:"sources"`
	ReviewItems  []candidateadoption.ReviewItem `json:"review_items"`
}

type InputResolver interface {
	ResolveGenerationInput(context.Context, Actor, string, Request) (Input, error)
}

type Authorizer interface {
	AuthorizeGeneration(context.Context, Actor, string) error
}

// SourceValidator performs the T09 present-time permission/revision/locator
// check after model generation. Implementations must fail closed.
type SourceValidator interface {
	ValidateGenerationSources(context.Context, Actor, string, []Source) error
}

type CurrentnessChecker interface {
	GenerationInputIsCurrent(context.Context, Actor, string, string, candidateadoption.Basis) (bool, error)
}

type ModelAdapter interface {
	Generate(context.Context, Input) (Draft, error)
}

type Repository interface {
	FindReplay(context.Context, Actor, string, string, string) (Run, bool, error)
	CreateOrReplay(context.Context, Actor, string, string, string, Input) (Run, error)
	Claim(context.Context, string) (Run, Input, bool, error)
	Touch(context.Context, string, string) error
	Cancel(context.Context, Actor, string, string) (Run, error)
	ListCandidates(context.Context, string, string) ([]candidateadoption.CandidateSummary, error)
	GetCandidate(context.Context, string, string) (candidateadoption.Candidate, Input, error)
	Complete(context.Context, string, string, candidateadoption.Candidate) (Run, error)
	Fail(context.Context, string, string, Status, RunError) (Run, error)
	Get(context.Context, Actor, string, string) (Run, error)
}

// Enqueuer durably schedules an already-persisted queued run. Implementations
// must be idempotent at the task boundary; the repository claim is the final
// guard against duplicate delivery.
type Enqueuer interface {
	EnqueueGeneration(context.Context, uint64, string) error
}

// DelayedEnqueuer preserves a lease-wait retry instead of acknowledging a
// task while its previous worker may have disappeared.
type DelayedEnqueuer interface {
	EnqueueGenerationAfter(context.Context, uint64, string, time.Duration) error
}

type Service struct {
	Authorizer  Authorizer
	Inputs      InputResolver
	Sources     SourceValidator
	Currentness CurrentnessChecker
	Model       ModelAdapter
	Repository  Repository
	Enqueuer    Enqueuer
	NewID       func() string
}

func NewService(authorizer Authorizer, inputs InputResolver, sources SourceValidator, currentness CurrentnessChecker, model ModelAdapter, repo Repository, enqueuer Enqueuer) *Service {
	return &Service{Authorizer: authorizer, Inputs: inputs, Sources: sources, Currentness: currentness, Model: model, Repository: repo,
		Enqueuer: enqueuer, NewID: func() string { return uuid.NewString() }}
}

func (s *Service) Start(ctx context.Context, actor Actor, projectID, key string, request Request) (Run, error) {
	if actor.TenantID == 0 || strings.TrimSpace(actor.UserID) == "" || strings.TrimSpace(projectID) == "" ||
		len(key) < 8 || len(key) > 128 || strings.TrimSpace(request.ChapterID) == "" ||
		strings.TrimSpace(request.Instruction) == "" || request.ExpectedSpecRevision < 0 || len(request.AssetIDs) == 0 {
		return Run{}, ErrInvalidRequest
	}
	request.AssetIDs = normalizeIDs(request.AssetIDs)
	if len(request.AssetIDs) == 0 {
		return Run{}, ErrInvalidRequest
	}
	for i, id := range request.AssetIDs {
		if id == "" || (i > 0 && id == request.AssetIDs[i-1]) {
			return Run{}, ErrInvalidRequest
		}
	}
	if s.Authorizer == nil || s.Inputs == nil || s.Repository == nil || s.Enqueuer == nil {
		return Run{}, ErrDependencyUnavailable
	}
	if err := s.Authorizer.AuthorizeGeneration(ctx, actor, projectID); err != nil {
		return Run{}, err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return Run{}, ErrInvalidRequest
	}
	hash := sha256.Sum256(encoded)
	requestHash := hex.EncodeToString(hash[:])
	if run, found, err := s.Repository.FindReplay(ctx, actor, projectID, key, requestHash); err != nil {
		return Run{}, err
	} else if found {
		run.Replayed = true
		if run.Status == StatusQueued {
			if err := s.Enqueuer.EnqueueGeneration(ctx, actor.TenantID, run.ID); err != nil {
				return Run{}, ErrDependencyUnavailable
			}
		}
		return run, nil
	}
	input, err := s.Inputs.ResolveGenerationInput(ctx, actor, projectID, request)
	if err != nil {
		return Run{}, err
	}
	if input.Workspace.ProjectID != projectID || input.Workspace.ChapterID != request.ChapterID {
		return Run{}, ErrNotFound
	}
	if input.Workspace.SpecRevision != request.ExpectedSpecRevision || input.Basis.SpecRevision != request.ExpectedSpecRevision ||
		!sameOptionalString(input.Workspace.ChapterVersionID, request.ExpectedChapterVersionID) ||
		!sameOptionalString(input.Basis.ChapterVersionID, request.ExpectedChapterVersionID) {
		return Run{}, ErrVersionConflict
	}
	if !sameStringSet(request.AssetIDs, assetIDs(input.Basis.AssetVersions)) {
		return Run{}, ErrSourceAccessDenied
	}
	input.Request = request
	input.Actor = actor
	run, err := s.Repository.CreateOrReplay(ctx, actor, projectID, key, requestHash, input)
	if err != nil {
		return Run{}, err
	}
	if run.Status == StatusQueued {
		if err := s.Enqueuer.EnqueueGeneration(ctx, actor.TenantID, run.ID); err != nil {
			// The run is durable. Retrying the same request key returns the same
			// run and re-arms the queue without creating a duplicate candidate.
			return Run{}, ErrDependencyUnavailable
		}
	}
	return run, nil
}

// Execute processes one queued run. A conditional claim prevents duplicate
// workers from invoking the model for the same run.
func (s *Service) Execute(ctx context.Context, runID string) (Run, error) {
	if s.Repository == nil || s.Model == nil || s.Currentness == nil || s.Sources == nil || s.Authorizer == nil {
		return Run{}, ErrDependencyUnavailable
	}
	run, input, claimed, err := s.Repository.Claim(ctx, runID)
	if err != nil || !claimed {
		if err == nil && run.Status == StatusRunning {
			return run, ErrRunUnavailable
		}
		return run, err
	}
	workCtx, cancel := context.WithCancel(ctx)
	stopHeartbeat := make(chan struct{})
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(claimHeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopHeartbeat:
				return
			case <-workCtx.Done():
				return
			case <-ticker.C:
				if err := s.Repository.Touch(context.WithoutCancel(ctx), run.ID, run.ClaimToken); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	defer func() {
		close(stopHeartbeat)
		cancel()
		<-heartbeatDone
	}()
	if err := s.Authorizer.AuthorizeGeneration(ctx, input.Actor, run.ProjectID); err != nil {
		return s.failBeforeModel(ctx, run, err)
	}
	if err := s.Sources.ValidateGenerationSources(ctx, input.Actor, run.ProjectID, input.Sources); err != nil {
		return s.failBeforeModel(ctx, run, err)
	}
	current, err := s.Currentness.GenerationInputIsCurrent(ctx, input.Actor, run.ProjectID, run.ChapterID, input.Basis)
	if err != nil {
		return s.failBeforeModel(ctx, run, err)
	}
	if !current {
		return s.failBeforeModel(ctx, run, ErrStaleInput)
	}
	draft, generateErr := s.Model.Generate(workCtx, input)
	if generateErr != nil {
		status := StatusFailed
		failure := RunError{Code: "generation_failed", Message: "生成失败，原始资料和凭据未写入任务错误。", Retryable: true}
		if errors.Is(generateErr, context.Canceled) || errors.Is(generateErr, context.DeadlineExceeded) {
			status = StatusInterrupted
			failure = RunError{Code: "generation_interrupted", Message: "生成已中断；可重新发起任务。", Retryable: true}
		}
		failed, storeErr := s.Repository.Fail(context.WithoutCancel(ctx), runID, run.ClaimToken, status, failure)
		if storeErr != nil {
			return Run{}, storeErr
		}
		return failed, nil
	}
	if strings.TrimSpace(draft.BodyMarkdown) == "" {
		failed, err := s.Repository.Fail(context.WithoutCancel(ctx), runID, run.ClaimToken, StatusFailed, RunError{Code: "empty_candidate", Message: "生成结果为空。", Retryable: false})
		return failed, err
	}
	selectedAssets := make(map[string]int, len(input.Basis.AssetVersions))
	for _, asset := range input.Basis.AssetVersions {
		selectedAssets[asset.AssetID] = asset.AssetRevision
	}
	allowed := make(map[string]Source, len(input.Sources))
	for _, source := range input.Sources {
		assetRevision, selected := selectedAssets[source.AssetID]
		if source.ID == "" || source.ProjectID != run.ProjectID || source.Status != "available" || !selected || assetRevision != source.AssetRevision {
			continue
		}
		allowed[source.ID] = source
	}
	used := make([]Source, 0, len(draft.Sources))
	seen := make(map[string]struct{}, len(draft.Sources))
	for _, source := range draft.Sources {
		original, ok := allowed[source.ID]
		if !ok {
			failed, err := s.Repository.Fail(context.WithoutCancel(ctx), runID, run.ClaimToken, StatusFailed, RunError{Code: "invalid_source_reference", Message: "生成结果引用了本次允许来源之外的资料。", Retryable: false})
			return failed, err
		}
		if _, duplicate := seen[source.ID]; duplicate {
			continue
		}
		seen[source.ID] = struct{}{}
		used = append(used, original)
	}
	if len(used) == 0 {
		failed, err := s.Repository.Fail(context.WithoutCancel(ctx), runID, run.ClaimToken, StatusFailed, RunError{Code: "missing_source_reference", Message: "生成结果未引用可验证的项目资料。", Retryable: false})
		return failed, err
	}
	if err := s.Sources.ValidateGenerationSources(workCtx, input.Actor, run.ProjectID, used); err != nil {
		status := StatusFailed
		failure := RunError{Code: "source_access_denied", Message: "生成来源已失效或当前不可访问。", Retryable: false}
		if errors.Is(err, ErrSourceStale) {
			status = StatusInterrupted
			failure = RunError{Code: "stale_input", Message: "生成来源版本或定位已变化，需重新生成。", Retryable: false}
		} else if !errors.Is(err, ErrSourceAccessDenied) {
			status = StatusInterrupted
			failure = RunError{Code: "source_validation_unavailable", Message: "来源复核暂不可用，任务已中断。", Retryable: true}
		}
		failed, storeErr := s.Repository.Fail(context.WithoutCancel(ctx), runID, run.ClaimToken, status, failure)
		if storeErr != nil {
			return Run{}, storeErr
		}
		return failed, nil
	}
	current, err = s.Currentness.GenerationInputIsCurrent(workCtx, input.Actor, run.ProjectID, run.ChapterID, input.Basis)
	if err != nil {
		failed, storeErr := s.Repository.Fail(context.WithoutCancel(ctx), runID, run.ClaimToken, StatusInterrupted, RunError{Code: "currentness_unavailable", Message: "无法确认生成输入仍为当前版本，任务已中断。", Retryable: true})
		if storeErr != nil {
			return Run{}, storeErr
		}
		return failed, nil
	}
	if !current {
		failed, err := s.Repository.Fail(context.WithoutCancel(ctx), runID, run.ClaimToken, StatusInterrupted, RunError{Code: "stale_input", Message: "研究条件、章节或资料版本已变化，需重新生成。", Retryable: false})
		return failed, err
	}
	sourceIDs := make([]string, 0, len(used))
	for _, source := range used {
		sourceIDs = append(sourceIDs, source.ID)
	}
	sort.Strings(sourceIDs)
	references, err := sourceReferences(draft.BodyMarkdown)
	if err != nil || !sameStringSet(references, sourceIDs) {
		failed, failErr := s.Repository.Fail(context.WithoutCancel(ctx), runID, run.ClaimToken, StatusFailed, RunError{Code: "invalid_source_reference", Message: "正文引用与来源清单不一致。", Retryable: false})
		return failed, failErr
	}
	candidateID := s.id()
	reviewItems := append([]candidateadoption.ReviewItem(nil), draft.ReviewItems...)
	reviewIDs := make(map[string]struct{}, len(reviewItems))
	for i := range reviewItems {
		if strings.TrimSpace(reviewItems[i].ID) == "" || strings.TrimSpace(reviewItems[i].Statement) == "" {
			failed, failErr := s.Repository.Fail(context.WithoutCancel(ctx), runID, run.ClaimToken, StatusFailed, RunError{Code: "invalid_review_item", Message: "生成的待核事项格式无效。", Retryable: false})
			return failed, failErr
		}
		if _, exists := reviewIDs[reviewItems[i].ID]; exists {
			failed, failErr := s.Repository.Fail(context.WithoutCancel(ctx), runID, run.ClaimToken, StatusFailed, RunError{Code: "invalid_review_item", Message: "生成结果包含重复的待核事项编号。", Retryable: false})
			return failed, failErr
		}
		reviewIDs[reviewItems[i].ID] = struct{}{}
		reviewItems[i].OriginCandidateID = candidateID
	}
	candidate := candidateadoption.Candidate{
		ID: candidateID, ProjectID: run.ProjectID, ChapterID: run.ChapterID, RunID: run.ID,
		BodyMarkdown: draft.BodyMarkdown, SourceIDs: sourceIDs, Basis: input.Basis,
		Validity: "fresh", ReviewItems: reviewItems,
	}
	completed, err := s.Repository.Complete(workCtx, run.ID, run.ClaimToken, candidate)
	if errors.Is(err, ErrGenerationCancelled) {
		return s.Repository.Fail(context.WithoutCancel(ctx), run.ID, run.ClaimToken, StatusInterrupted,
			RunError{Code: "generation_cancelled", Message: "生成任务已取消。", Retryable: false})
	}
	return completed, err
}

func (s *Service) failBeforeModel(ctx context.Context, run Run, cause error) (Run, error) {
	status := StatusFailed
	failure := RunError{Code: "source_access_denied", Message: "生成输入当前不可访问。", Retryable: false}
	switch {
	case errors.Is(cause, ErrStaleInput), errors.Is(cause, ErrSourceStale):
		status = StatusInterrupted
		failure = RunError{Code: "stale_input", Message: "生成输入版本或来源已变化，需重新生成。", Retryable: false}
	case errors.Is(cause, ErrDependencyUnavailable):
		status = StatusInterrupted
		failure = RunError{Code: "generation_preflight_unavailable", Message: "生成前置校验暂不可用，任务已中断。", Retryable: true}
	case errors.Is(cause, ErrNotFound), errors.Is(cause, ErrForbidden), errors.Is(cause, ErrSourceAccessDenied):
	default:
		status = StatusInterrupted
		failure = RunError{Code: "generation_preflight_unavailable", Message: "生成前置校验失败，任务已中断。", Retryable: true}
	}
	failed, err := s.Repository.Fail(context.WithoutCancel(ctx), run.ID, run.ClaimToken, status, failure)
	if err != nil {
		return Run{}, err
	}
	return failed, nil
}

func (s *Service) Get(ctx context.Context, actor Actor, projectID, runID string) (Run, error) {
	if s.Repository == nil || s.Authorizer == nil || actor.TenantID == 0 || actor.UserID == "" {
		return Run{}, ErrNotFound
	}
	if err := s.Authorizer.AuthorizeGeneration(ctx, actor, projectID); err != nil {
		return Run{}, ErrNotFound
	}
	return s.Repository.Get(ctx, actor, projectID, runID)
}

func (s *Service) Cancel(ctx context.Context, actor Actor, projectID, runID string) (Run, error) {
	if s.Repository == nil || s.Authorizer == nil || actor.TenantID == 0 || strings.TrimSpace(actor.UserID) == "" {
		return Run{}, ErrNotFound
	}
	if err := s.Authorizer.AuthorizeGeneration(ctx, actor, projectID); err != nil {
		return Run{}, err
	}
	return s.Repository.Cancel(ctx, actor, projectID, runID)
}

func (s *Service) ListCandidates(ctx context.Context, actor Actor, projectID, chapterID string) ([]candidateadoption.CandidateSummary, error) {
	if s.Repository == nil || s.Authorizer == nil || actor.TenantID == 0 || strings.TrimSpace(actor.UserID) == "" {
		return nil, ErrNotFound
	}
	if err := s.Authorizer.AuthorizeGeneration(ctx, actor, projectID); err != nil {
		return nil, err
	}
	candidates, err := s.Repository.ListCandidates(ctx, projectID, chapterID)
	if err != nil {
		return nil, err
	}
	for i := range candidates {
		candidates[i].Validity = s.candidateValidity(ctx, actor, projectID, chapterID, candidates[i].Basis)
	}
	return candidates, nil
}

func (s *Service) GetCandidate(ctx context.Context, actor Actor, projectID, runID string) (candidateadoption.Candidate, error) {
	if s.Repository == nil || s.Authorizer == nil || actor.TenantID == 0 || strings.TrimSpace(actor.UserID) == "" {
		return candidateadoption.Candidate{}, ErrNotFound
	}
	if err := s.Authorizer.AuthorizeGeneration(ctx, actor, projectID); err != nil {
		return candidateadoption.Candidate{}, err
	}
	candidate, input, err := s.Repository.GetCandidate(ctx, projectID, runID)
	if err != nil {
		return candidateadoption.Candidate{}, err
	}
	if s.Sources == nil {
		return candidateadoption.Candidate{}, ErrDependencyUnavailable
	}
	allowedSources := make(map[string]Source, len(input.Sources))
	for _, source := range input.Sources {
		allowedSources[source.ID] = source
	}
	usedSources := make([]Source, 0, len(candidate.SourceIDs))
	for _, sourceID := range candidate.SourceIDs {
		source, ok := allowedSources[sourceID]
		if !ok {
			return candidateadoption.Candidate{}, ErrSourceAccessDenied
		}
		usedSources = append(usedSources, source)
	}
	if err := s.Sources.ValidateGenerationSources(ctx, actor, projectID, usedSources); err != nil {
		if errors.Is(err, ErrSourceStale) {
			candidate.Validity = "stale"
			return candidate, nil
		}
		if errors.Is(err, ErrSourceAccessDenied) {
			return candidateadoption.Candidate{}, ErrSourceAccessDenied
		}
		return candidateadoption.Candidate{}, ErrDependencyUnavailable
	}
	candidate.Validity = s.candidateValidity(ctx, actor, projectID, candidate.ChapterID, candidate.Basis)
	return candidate, nil
}

func (s *Service) candidateValidity(ctx context.Context, actor Actor, projectID, chapterID string, basis candidateadoption.Basis) string {
	if s.Currentness == nil {
		return "stale"
	}
	current, err := s.Currentness.GenerationInputIsCurrent(ctx, actor, projectID, chapterID, basis)
	if err != nil || !current {
		return "stale"
	}
	return "fresh"
}

func (s *Service) id() string {
	if s.NewID != nil {
		return s.NewID()
	}
	return uuid.NewString()
}

func normalizeIDs(ids []string) []string {
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		result = append(result, strings.TrimSpace(id))
	}
	sort.Strings(result)
	return result
}

func assetIDs(versions []candidateadoption.AssetVersion) []string {
	ids := make([]string, 0, len(versions))
	for _, item := range versions {
		ids = append(ids, item.AssetID)
	}
	return normalizeIDs(ids)
}

func sameStringSet(a, b []string) bool {
	left, right := normalizeIDs(a), normalizeIDs(b)
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func sameOptionalString(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func sourceReferences(markdown string) ([]string, error) {
	const prefix, suffix = "[[source:", "]]"
	var result []string
	seen := make(map[string]struct{})
	for offset := 0; ; {
		start := strings.Index(markdown[offset:], prefix)
		if start < 0 {
			break
		}
		start += offset + len(prefix)
		end := strings.Index(markdown[start:], suffix)
		if end < 0 {
			return nil, ErrInvalidRequest
		}
		id := strings.TrimSpace(markdown[start : start+end])
		if id == "" {
			return nil, ErrInvalidRequest
		}
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			result = append(result, id)
		}
		offset = start + end + len(suffix)
	}
	return normalizeIDs(result), nil
}
