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

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/google/uuid"
)

var (
	ErrInvalidRequest        = errors.New("invalid_request")
	ErrNotFound              = errors.New("not_found")
	ErrForbidden             = errors.New("forbidden")
	ErrVersionConflict       = errors.New("version_conflict")
	ErrSourceAccessDenied    = errors.New("source_access_denied")
	ErrStaleInput            = errors.New("stale_input")
	ErrIdempotencyConflict   = errors.New("idempotency_conflict")
	ErrRunUnavailable        = errors.New("generation_run_unavailable")
	ErrDependencyUnavailable = errors.New("dependency_unavailable")
)

type Status string

const (
	StatusQueued      Status = "queued"
	StatusRunning     Status = "running"
	StatusSucceeded   Status = "succeeded"
	StatusFailed      Status = "failed"
	StatusInterrupted Status = "interrupted"
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
	Actor     Actor                               `json:"actor"`
	Workspace candidateadoption.GenerationContext `json:"workspace"`
	Basis     candidateadoption.Basis             `json:"basis"`
	Sources   []Source                            `json:"sources"`
	Request   Request                             `json:"request"`
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
	CreateOrReplay(context.Context, Actor, string, string, string, Input) (Run, error)
	Claim(context.Context, string) (Run, Input, bool, error)
	Complete(context.Context, string, candidateadoption.Candidate) (Run, error)
	Fail(context.Context, string, Status, RunError) (Run, error)
	Get(context.Context, Actor, string, string) (Run, error)
}

type Service struct {
	Authorizer  Authorizer
	Inputs      InputResolver
	Sources     SourceValidator
	Currentness CurrentnessChecker
	Model       ModelAdapter
	Repository  Repository
	NewID       func() string
}

func NewService(authorizer Authorizer, inputs InputResolver, sources SourceValidator, currentness CurrentnessChecker, model ModelAdapter, repo Repository) *Service {
	return &Service{Authorizer: authorizer, Inputs: inputs, Sources: sources, Currentness: currentness, Model: model, Repository: repo,
		NewID: func() string { return uuid.NewString() }}
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
	if s.Authorizer == nil || s.Inputs == nil || s.Repository == nil {
		return Run{}, ErrDependencyUnavailable
	}
	if err := s.Authorizer.AuthorizeGeneration(ctx, actor, projectID); err != nil {
		return Run{}, err
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
	encoded, err := json.Marshal(request)
	if err != nil {
		return Run{}, ErrInvalidRequest
	}
	hash := sha256.Sum256(encoded)
	return s.Repository.CreateOrReplay(ctx, actor, projectID, key, hex.EncodeToString(hash[:]), input)
}

// Execute processes one queued run. A conditional claim prevents duplicate
// workers from invoking the model for the same run.
func (s *Service) Execute(ctx context.Context, runID string) (Run, error) {
	if s.Repository == nil || s.Model == nil || s.Currentness == nil || s.Sources == nil {
		return Run{}, ErrDependencyUnavailable
	}
	run, input, claimed, err := s.Repository.Claim(ctx, runID)
	if err != nil || !claimed {
		return run, err
	}
	draft, generateErr := s.Model.Generate(ctx, input)
	if generateErr != nil {
		status := StatusFailed
		failure := RunError{Code: "generation_failed", Message: "生成失败，原始资料和凭据未写入任务错误。", Retryable: true}
		if errors.Is(generateErr, context.Canceled) || errors.Is(generateErr, context.DeadlineExceeded) {
			status = StatusInterrupted
			failure = RunError{Code: "generation_interrupted", Message: "生成已中断；可重新发起任务。", Retryable: true}
		}
		failed, storeErr := s.Repository.Fail(context.WithoutCancel(ctx), runID, status, failure)
		if storeErr != nil {
			return Run{}, storeErr
		}
		return failed, nil
	}
	if strings.TrimSpace(draft.BodyMarkdown) == "" {
		failed, err := s.Repository.Fail(context.WithoutCancel(ctx), runID, StatusFailed, RunError{Code: "empty_candidate", Message: "生成结果为空。", Retryable: false})
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
			failed, err := s.Repository.Fail(context.WithoutCancel(ctx), runID, StatusFailed, RunError{Code: "invalid_source_reference", Message: "生成结果引用了本次允许来源之外的资料。", Retryable: false})
			return failed, err
		}
		if _, duplicate := seen[source.ID]; duplicate {
			continue
		}
		seen[source.ID] = struct{}{}
		used = append(used, original)
	}
	if len(used) == 0 {
		failed, err := s.Repository.Fail(context.WithoutCancel(ctx), runID, StatusFailed, RunError{Code: "missing_source_reference", Message: "生成结果未引用可验证的项目资料。", Retryable: false})
		return failed, err
	}
	if err := s.Sources.ValidateGenerationSources(ctx, input.Actor, run.ProjectID, used); err != nil {
		status := StatusFailed
		failure := RunError{Code: "source_access_denied", Message: "生成来源已失效或当前不可访问。", Retryable: false}
		if !errors.Is(err, ErrSourceAccessDenied) {
			status = StatusInterrupted
			failure = RunError{Code: "source_validation_unavailable", Message: "来源复核暂不可用，任务已中断。", Retryable: true}
		}
		failed, storeErr := s.Repository.Fail(context.WithoutCancel(ctx), runID, status, failure)
		if storeErr != nil {
			return Run{}, storeErr
		}
		return failed, nil
	}
	current, err := s.Currentness.GenerationInputIsCurrent(ctx, input.Actor, run.ProjectID, run.ChapterID, input.Basis)
	if err != nil {
		failed, storeErr := s.Repository.Fail(context.WithoutCancel(ctx), runID, StatusInterrupted, RunError{Code: "currentness_unavailable", Message: "无法确认生成输入仍为当前版本，任务已中断。", Retryable: true})
		if storeErr != nil {
			return Run{}, storeErr
		}
		return failed, nil
	}
	if !current {
		failed, err := s.Repository.Fail(context.WithoutCancel(ctx), runID, StatusInterrupted, RunError{Code: "stale_input", Message: "研究条件、章节或资料版本已变化，需重新生成。", Retryable: false})
		return failed, err
	}
	sourceIDs := make([]string, 0, len(used))
	for _, source := range used {
		sourceIDs = append(sourceIDs, source.ID)
	}
	sort.Strings(sourceIDs)
	references, err := sourceReferences(draft.BodyMarkdown)
	if err != nil || !sameStringSet(references, sourceIDs) {
		failed, failErr := s.Repository.Fail(context.WithoutCancel(ctx), runID, StatusFailed, RunError{Code: "invalid_source_reference", Message: "正文引用与来源清单不一致。", Retryable: false})
		return failed, failErr
	}
	candidateID := s.id()
	reviewItems := append([]candidateadoption.ReviewItem(nil), draft.ReviewItems...)
	reviewIDs := make(map[string]struct{}, len(reviewItems))
	for i := range reviewItems {
		if strings.TrimSpace(reviewItems[i].ID) == "" || strings.TrimSpace(reviewItems[i].Statement) == "" {
			failed, failErr := s.Repository.Fail(context.WithoutCancel(ctx), runID, StatusFailed, RunError{Code: "invalid_review_item", Message: "生成的待核事项格式无效。", Retryable: false})
			return failed, failErr
		}
		if _, exists := reviewIDs[reviewItems[i].ID]; exists {
			failed, failErr := s.Repository.Fail(context.WithoutCancel(ctx), runID, StatusFailed, RunError{Code: "invalid_review_item", Message: "生成结果包含重复的待核事项编号。", Retryable: false})
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
	return s.Repository.Complete(ctx, run.ID, candidate)
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
		result = append(result, id)
		offset = start + end + len(suffix)
	}
	return normalizeIDs(result), nil
}
