// Package generation owns the asynchronous generation task and candidate
// lifecycle. Provider, source, and workspace details are supplied through
// narrow interfaces so credentials and mutable provider state stay outside
// the task contract.
package generation

import (
	"context"
	"errors"
	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
	"github.com/Tencent/WeKnora/internal/lingdoc/sourcemarker"
	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/types"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/evidence"
	lingdocaccess "github.com/Tencent/WeKnora/internal/lingdoc/access"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/google/uuid"
)

// The failures this surface reports state their own outward form.
//
// Only the failures the generation surface answers with a named form are
// listed here. The remaining sentinels are internal states of the run and
// keep falling through to the transport's fallback.
var (
	ErrInvalidRequest       = clienterror.New(clienterror.InvalidRequest, "invalid_request")
	ErrNotFound             = clienterror.New(clienterror.NotFound, "not_found")
	ErrForbidden            = clienterror.New(clienterror.Forbidden, "forbidden")
	ErrInsufficientEvidence = clienterror.New(clienterror.Form{Status: 422, Code: "insufficient_evidence", Message: "允许资料不足以生成该章节。", Retryable: false}, "insufficient_evidence")
	ErrAssetNotReady        = clienterror.New(clienterror.Form{Status: 422, Code: "asset_not_ready", Message: "资料尚未处理完成。", Retryable: false}, "asset_not_ready")
	ErrPreflightBlocked     = clienterror.New(clienterror.PreflightBlocked, "preflight_blocked")
	ErrInvalidState         = clienterror.New(clienterror.StageNotAllowed, "invalid_state")
	ErrAssetNotAuthorized   = clienterror.New(clienterror.Form{Status: 422, Code: "asset_not_authorized", Message: "请求中存在未获授权的资料，未开始处理。", Retryable: false}, "asset_not_authorized")
	ErrVersionConflict      = clienterror.New(clienterror.Form{Status: 409, Code: "version_conflict", Message: "输入版本已变化，请重新读取。", Retryable: false}, "version_conflict")
	ErrSourceAccessDenied   = clienterror.New(clienterror.SourceAccessDenied, "source_access_denied")
	ErrSourceStale          = errors.New("source_stale")
	// ErrStaleInput is an input basis that changed before the run started.
	// It used to fall through the transport's error table and be answered
	// with a generic internal error; it now states the stale input form.
	ErrStaleInput          = clienterror.New(clienterror.CandidateStaleInput, "stale_input")
	ErrIdempotencyConflict = clienterror.New(clienterror.IdempotencyKeyConflict, "idempotency_conflict")
	// ErrIdempotencyKeyExpired 说这个幂等键属于旧版摘要规则。
	//
	// 它与 ErrIdempotencyConflict 是两件事：前者的请求本身没问题，是键属于这个
	// build 已经不说的规则——调用方换一个新键重试；后者是同一个键带了两份不同的
	// 请求——调用方要找的是一份根本没变过的请求。
	ErrIdempotencyKeyExpired       = clienterror.New(clienterror.IdempotencyKeyExpired, "idempotency_key_expired")
	ErrRunUnavailable              = errors.New("generation_run_unavailable")
	ErrGenerationCancelled         = errors.New("generation_cancelled")
	ErrDependencyUnavailable       = clienterror.New(clienterror.DependencyUnavailable, "dependency_unavailable")
	ErrGenerationIncomplete        = errors.New("generation_incomplete")
	ErrAcceptanceBudgetExceeded    = errors.New("acceptance_budget_exceeded")
	ErrAcceptanceBudgetUnavailable = errors.New("acceptance_budget_unavailable")
	ErrAcceptanceOutcomeUnknown    = errors.New("acceptance_outcome_unknown")
	ErrAcceptanceModelMismatch     = errors.New("acceptance_model_mismatch")
	// ErrUnauthenticated is a request without a usable actor.
	ErrUnauthenticated = clienterror.New(clienterror.Unauthenticated, "unauthenticated")
)

// DeniedAsset records the per-asset reason that prevented a generation
// request from entering the queue. It deliberately mirrors the public error
// contract without exposing evidence-layer implementation types.
type DeniedAsset struct {
	AssetID string `json:"asset_id"`
	Reason  string `json:"reason"`
}

// AssetNotAuthorizedError preserves the denied asset list required by the
// startGeneration 422 response. The sentinel lets transport code classify the
// error while the concrete value keeps the details for the response body.
type AssetNotAuthorizedError struct {
	Denied []DeniedAsset
}

func (e *AssetNotAuthorizedError) Error() string { return ErrAssetNotAuthorized.Error() }

func (e *AssetNotAuthorizedError) Unwrap() error { return ErrAssetNotAuthorized }

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
	// InputSummary says what this run was started from.
	//
	// It is a summary, not the input: a failed or cancelled run has to be
	// readable back (what was asked, over what, at which revision) without the
	// service handing out the raw instruction again. It is absent only when
	// the stored input cannot be read at all.
	InputSummary *RunInputSummary `json:"input_summary,omitempty"`
	// DroppedContext is the optional context the user selected that did not
	// enter the model request.
	//
	// It is reported on the response that starts the run: a silent drop would
	// let the user believe the context was used.
	DroppedContext []writingaction.DroppedContextEntry `json:"dropped_context,omitempty"`
	CreatedAt      time.Time                           `json:"-"`
	UpdatedAt      time.Time                           `json:"-"`
	Replayed       bool                                `json:"-"`
	ClaimToken     string                              `json:"-"`
}

// Actor is the authenticated owner identity passed from the HTTP boundary.
type Actor struct {
	Role     types.TenantRole `json:"role,omitempty"`
	TenantID uint64           `json:"tenant_id"`
	UserID   string           `json:"user_id"`
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
	EvidenceCardRefs         []EvidenceCardRef `json:"evidence_card_refs,omitempty"`
	ChapterID                string            `json:"chapter_id"`
	AssetIDs                 []string          `json:"asset_ids"`
	Instruction              string            `json:"instruction"`
	ExpectedSpecRevision     int               `json:"expected_spec_revision"`
	ExpectedChapterVersionID *string           `json:"expected_chapter_version_id"`

	// 下面四个字段把这次请求从「自由文本说明」变成「按动作发起」。
	//
	// 它们都是**指针**加 omitempty：指针为 nil 时字段整个不出现，请求的 JSON
	// 与它的摘要值逐字不变——历史幂等键的重放语义不能被这次改动碰坏。用非指针
	// 的结构体字段做不到这一点：omitempty 对结构体不起作用，旧请求会多出一个
	// "output_params":{...}，摘要全变，重试从「重放」变成「冲突」。
	//
	// 四个字段全部缺省 ⇒ 旧路径：自由文本说明驱动，行为逐字不变。
	// 任一字段出现 ⇒ 动作路径：动作与产出参数必齐，缺一即拒。
	WritingAction *writingaction.Action       `json:"writing_action,omitempty"`
	OutputParams  *writingaction.OutputParams `json:"output_params,omitempty"`
	Scope         *writingaction.Scope        `json:"scope,omitempty"`
	// Context 是用户对这次执行要纳入的可选上下文的选择。
	//
	// 它是**指针**加 omitempty：缺省即「没选」，旧请求的 JSON 与它的摘要值逐字
	// 不变。它只在动作路径下有意义——旧路径带上它会被拒。
	Context *writingaction.ContextSelection `json:"context,omitempty"`
}

// Input is the immutable, server-resolved generation basis captured before
// queueing. The client never supplies project spec, template, or source text.
type Input struct {
	EvidenceConstraints []EvidenceConstraint                `json:"evidence_constraints,omitempty"`
	HumanInitiated      bool                                `json:"human_initiated,omitempty"`
	TemplateDefinition  *lingdoctemplate.Template           `json:"template_definition,omitempty"`
	EvidenceCardRefs    []EvidenceCardRef                   `json:"evidence_card_refs,omitempty"`
	Actor               Actor                               `json:"actor"`
	Workspace           candidateadoption.GenerationContext `json:"workspace"`
	Basis               candidateadoption.Basis             `json:"basis"`
	ProjectSpec         map[string]string                   `json:"project_spec"`
	Sources             []Source                            `json:"sources"`
	Request             Request                             `json:"request"`
	// Instruction 是写给模型的指令。
	//
	// 旧路径下它是客户端的自由文本，逐字不变；动作路径下它是执行计划渲染出来
	// 的动作指令。写给模型的指令只有这一个来源。
	Instruction string `json:"instruction,omitempty"`
	// SupplementaryNote 是客户端的补充说明。
	//
	// 旧路径恒为空：旧路径的说明本身就是指令。动作路径下它不参与动作判定，
	// 也不与动作指令混成一段。
	SupplementaryNote string `json:"supplementary_note,omitempty"`
}

type Draft struct {
	BodyMarkdown string                         `json:"body_markdown"`
	Sources      []Source                       `json:"sources"`
	ReviewItems  []candidateadoption.ReviewItem `json:"review_items"`
	// RunMode 说这一稿是怎么到模型面前的。
	//
	// 它由模型适配器给出：适配器知道这一次到底有没有真的调用 provider。空值
	// 表示适配器没有报告，服务端按「没有 provider」处理。
	RunMode candidateadoption.RunMode `json:"run_mode,omitempty"`
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
	// RunMode 说这个适配器会不会真的调用 provider。
	//
	// 它是候选上 run_mode 的来源：没有 provider 的适配器必须报「mock」，不
	// 允许冒充真实证据（开发计划 A27）。
	RunMode() string
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
	RequireHumanInitiation bool
	Evidence               EvidenceInputValidator
	// Access is the production application policy. The legacy ports remain
	// available for adapters that are migrated independently.
	Access      lingdocaccess.Policy
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

// NewServiceWithAccessPolicy wires generation through the shared LingDoc
// authorization/currentness policy.
func NewServiceWithAccessPolicy(policy lingdocaccess.Policy, inputs InputResolver, sources SourceValidator, model ModelAdapter, repo Repository, enqueuer Enqueuer) *Service {
	return &Service{Access: policy, Inputs: inputs, Sources: sources, Model: model, Repository: repo, Enqueuer: enqueuer,
		NewID: func() string { return uuid.NewString() }}
}

func (s *Service) Start(ctx context.Context, actor Actor, projectID, key string, request Request) (Run, error) {
	if actor.TenantID == 0 || strings.TrimSpace(actor.UserID) == "" || strings.TrimSpace(projectID) == "" ||
		!operationcore.ValidKey(key) || strings.TrimSpace(request.ChapterID) == "" ||
		request.ExpectedSpecRevision < 0 {
		return Run{}, ErrInvalidRequest
	}
	// 旧路径的说明是必填的自由文本；动作路径的说明只是补充说明，必填的是动作
	// 与产出参数（由执行计划校验），所以这里不为动作路径要求它。
	if request.isLegacyPath() && strings.TrimSpace(request.Instruction) == "" {
		return Run{}, ErrInvalidRequest
	}
	if !validEvidenceCardRefs(request.EvidenceCardRefs) {
		return Run{}, ErrInvalidRequest
	}
	// 动作路径的声明先解析、再干活：产出参数与目标范围在模型调用之前就被拒，
	// 非法声明不产生 run，也不产生候选行。
	plan, err := resolveGenerationPlan(request)
	if err != nil {
		return Run{}, err
	}
	// 用户对可选上下文的选择先解析：类别越过策略的请求被拒；用户剔除的条目
	// 不进模型请求，并被点名。
	var droppedContext []writingaction.DroppedContextEntry
	if plan != nil && request.Context != nil {
		request, droppedContext, err = applyContextSelection(*plan, *request.Context, request)
		if err != nil {
			return Run{}, err
		}
	}
	request.EvidenceCardRefs = append([]EvidenceCardRef(nil), request.EvidenceCardRefs...)
	request.AssetIDs = normalizeIDs(request.AssetIDs)
	if len(request.AssetIDs) == 0 && len(request.EvidenceCardRefs) == 0 {
		return Run{}, ErrInvalidRequest
	}
	for i, id := range request.AssetIDs {
		if id == "" || (i > 0 && id == request.AssetIDs[i-1]) {
			return Run{}, ErrInvalidRequest
		}
	}
	if !s.hasAuthorization() || s.Inputs == nil || s.Repository == nil || s.Enqueuer == nil {
		return Run{}, ErrDependencyUnavailable
	}
	if s.RequireHumanInitiation {
		var authority any = s.Access
		if authority == nil {
			authority = s.Authorizer
		}
		human, ok := authority.(HumanInitiationAuthorizer)
		if !ok {
			return Run{}, ErrDependencyUnavailable
		}
		if err := human.AuthorizeHumanGeneration(ctx); err != nil {
			return Run{}, err
		}
	}
	caller := types.CallerFromContext(ctx)
	if caller.UserID == actor.UserID && caller.TenantID == actor.TenantID && caller.Role.IsValid() {
		actor.Role = caller.Role
	}
	if err := s.authorizeWrite(ctx, actor, projectID, request.ChapterID); err != nil {
		return Run{}, err
	}
	requestHash, err := operationcore.RequestHash(request)
	if err != nil {
		return Run{}, ErrInvalidRequest
	}
	var evidenceInput *Input
	if len(request.EvidenceCardRefs) > 0 {
		resolved, resolveErr := s.Inputs.ResolveGenerationInput(ctx, actor, projectID, request)
		if resolveErr != nil {
			return Run{}, resolveErr
		}
		resolved.Request = request
		resolved.Actor = actor
		resolved.EvidenceCardRefs = append([]EvidenceCardRef(nil), request.EvidenceCardRefs...)
		if resolved.Workspace.SpecRevision != request.ExpectedSpecRevision || !sameOptionalString(resolved.Basis.ChapterVersionID, request.ExpectedChapterVersionID) {
			return Run{}, ErrVersionConflict
		}
		if err := s.validateEvidence(ctx, actor, projectID, request.ChapterID, resolved); err != nil {
			return Run{}, err
		}
		evidenceInput = &resolved
	}
	if run, found, err := s.Repository.FindReplay(ctx, actor, projectID, key, requestHash); err != nil {
		return Run{}, err
	} else if found {
		if s.RequireHumanInitiation {
			if err := s.validateReplayInput(ctx, actor, projectID, run); err != nil {
				return Run{}, err
			}
		}
		run.Replayed = true
		if run.Status == StatusQueued {
			if err := s.Enqueuer.EnqueueGeneration(ctx, actor.TenantID, run.ID); err != nil {
				return Run{}, ErrDependencyUnavailable
			}
		}
		run.DroppedContext = droppedContext
		return run, nil
	}
	var input Input
	if evidenceInput != nil {
		input = *evidenceInput
	} else {
		var resolveErr error
		input, resolveErr = s.Inputs.ResolveGenerationInput(ctx, actor, projectID, request)
		if resolveErr != nil {
			return Run{}, resolveErr
		}
	}
	if input.Workspace.ProjectID != projectID || input.Workspace.ChapterID != request.ChapterID {
		return Run{}, ErrNotFound
	}
	if input.Workspace.SpecRevision != request.ExpectedSpecRevision || input.Basis.SpecRevision != request.ExpectedSpecRevision ||
		!sameOptionalString(input.Workspace.ChapterVersionID, request.ExpectedChapterVersionID) ||
		!sameOptionalString(input.Basis.ChapterVersionID, request.ExpectedChapterVersionID) {
		return Run{}, ErrVersionConflict
	}
	if len(request.AssetIDs) > 0 && !sameStringSet(request.AssetIDs, assetIDs(input.Basis.AssetVersions)) {
		return Run{}, ErrSourceAccessDenied
	}
	input.Request = request
	input.Actor = actor
	input.HumanInitiated = s.RequireHumanInitiation
	input.EvidenceCardRefs = append([]EvidenceCardRef(nil), request.EvidenceCardRefs...)
	copyEvidenceBasis(&input, request.EvidenceCardRefs)
	if err := s.validateEvidence(ctx, actor, projectID, request.ChapterID, input); err != nil {
		return Run{}, err
	}
	// 写给模型的指令在这里定下来：旧路径用客户端的自由文本，动作路径用执行
	// 计划渲染出来的动作指令。执行方不自己写动作指令。
	if err := applyGenerationInstruction(&input, request, plan); err != nil {
		return Run{}, err
	}
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
	run.DroppedContext = droppedContext
	return run, nil
}

// Execute processes one queued run. A conditional claim prevents duplicate
// workers from invoking the model for the same run.
func (s *Service) Execute(ctx context.Context, runID string) (Run, error) {
	if s.Repository == nil || s.Model == nil || !s.hasCurrentness() || s.Sources == nil || !s.hasAuthorization() {
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
	if s.RequireHumanInitiation && !input.HumanInitiated {
		return s.failBeforeModel(ctx, run, ErrForbidden)
	}
	if err := s.authorizeWrite(ctx, input.Actor, run.ProjectID, run.ChapterID); err != nil {
		return s.failBeforeModel(ctx, run, err)
	}
	if err := s.Sources.ValidateGenerationSources(ctx, input.Actor, run.ProjectID, input.Sources); err != nil {
		return s.failBeforeModel(ctx, run, err)
	}
	if err := s.validateEvidence(ctx, input.Actor, run.ProjectID, run.ChapterID, input); err != nil {
		return s.failBeforeModel(ctx, run, err)
	}
	current, err := s.current(ctx, input.Actor, run.ProjectID, run.ChapterID, input.Basis)
	if err != nil {
		return s.failBeforeModel(ctx, run, err)
	}
	if !current {
		return s.failBeforeModel(ctx, run, ErrStaleInput)
	}
	// 运行模式在模型调用之前就问一次：适配器报告不了模式时，候选不得冒充
	// 真实证据。
	runMode, modeErr := generationRunMode(s.Model.RunMode())
	if modeErr != nil {
		failed, failErr := s.Repository.Fail(context.WithoutCancel(ctx), runID, run.ClaimToken, StatusFailed,
			RunError{Code: "invalid_run_mode", Message: "模型适配器报告了未知的运行模式。", Retryable: false})
		return failed, failErr
	}
	draft, generateErr := s.Model.Generate(workCtx, input)
	if generateErr != nil {
		status := StatusFailed
		failure := RunError{Code: "generation_failed", Message: "生成失败，原始资料和凭据未写入任务错误。", Retryable: true}
		switch {
		case errors.Is(generateErr, context.Canceled) || errors.Is(generateErr, context.DeadlineExceeded):
			// The acceptance ledger is still reconciled as unknown by the model
			// adapter, but cancellation is an interrupted run and may be retried
			// with a fresh key after reconciliation.
			status = StatusInterrupted
			failure = RunError{Code: "generation_interrupted", Message: "生成已中断；可重新发起任务。", Retryable: true}
		case errors.Is(generateErr, ErrAcceptanceOutcomeUnknown):
			// A provider outcome or usage that cannot be confirmed remains
			// terminal and non-retryable until the ledger is reconciled.
			failure = RunError{Code: "generation_outcome_unknown", Message: "模型调用结果或用量未能确认；为避免重复扣费，后续调用已暂停，需先核对服务商用量与本地账本。", Retryable: false}
		case errors.Is(generateErr, ErrGenerationIncomplete):
			failure = RunError{Code: "generation_incomplete", Message: "本轮输出达到 6,000 token 上限，结果不完整；系统不会自动续写。缩小输入后重新发起会占用下一次调用额度。", Retryable: false}
		case errors.Is(generateErr, ErrAcceptanceBudgetExceeded):
			failure = RunError{Code: "generation_budget_exceeded", Message: "本轮验收预算已到上限或输入超限，系统未继续请求模型。", Retryable: false}
		case errors.Is(generateErr, ErrAcceptanceBudgetUnavailable):
			failure = RunError{Code: "generation_budget_unavailable", Message: "本轮预算账本不可用，模型调用已阻止。", Retryable: false}
		case errors.Is(generateErr, ErrAcceptanceModelMismatch):
			failure = RunError{Code: "generation_model_mismatch", Message: "验收模式未找到唯一且符合条件的 deepseek-flash 配置；未调用模型。", Retryable: false}
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
	// 适配器在这一次调用里知道的模式更准：它知道这一次到底调了哪个 provider。
	if draft.RunMode != "" {
		reported, reportedErr := generationRunMode(string(draft.RunMode))
		if reportedErr != nil {
			failed, failErr := s.Repository.Fail(context.WithoutCancel(ctx), runID, run.ClaimToken, StatusFailed,
				RunError{Code: "invalid_run_mode", Message: "模型适配器报告了未知的运行模式。", Retryable: false})
			return failed, failErr
		}
		runMode = reported
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
	if err := s.authorizeWrite(workCtx, input.Actor, run.ProjectID, run.ChapterID); err != nil {
		return s.failBeforeModel(ctx, run, err)
	}
	if err := s.validateEvidence(workCtx, input.Actor, run.ProjectID, run.ChapterID, input); err != nil {
		return s.failBeforeModel(ctx, run, err)
	}
	current, err = s.current(workCtx, input.Actor, run.ProjectID, run.ChapterID, input.Basis)
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
		Validity: candidateadoption.ValidityFresh, ReviewItems: reviewItems,
		// 这个生成端点产出的就是「生成」这个动作的结果，正文全部来自模型，
		// 来源是 AI 生成。候选必须记下自己是怎么来的：采纳是一次显式确认，
		// 确认载荷要能拿它和候选对上。动作与来源的词表归 writingaction 所有。
		//
		// 动作路径下是请求显式声明的那一个；旧路径下是这个端点实现的那个动作。
		WritingAction: candidateAction(input.Request),
		Origin:        writingaction.OriginGenerated,
		// 运行模式由模型适配器报告：没有 provider 的适配器报 mock，不冒充
		// 真实证据（开发计划 A27）。
		RunMode: runMode,
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
	if s.Repository == nil || !s.hasAuthorization() || actor.TenantID == 0 || actor.UserID == "" {
		return Run{}, ErrNotFound
	}
	if err := s.authorize(ctx, actor, projectID); err != nil {
		return Run{}, ErrNotFound
	}
	return s.Repository.Get(ctx, actor, projectID, runID)
}

func (s *Service) Cancel(ctx context.Context, actor Actor, projectID, runID string) (Run, error) {
	if s.Repository == nil || !s.hasAuthorization() || actor.TenantID == 0 || strings.TrimSpace(actor.UserID) == "" {
		return Run{}, ErrNotFound
	}
	run, err := s.Repository.Get(ctx, actor, projectID, runID)
	if err != nil {
		return Run{}, err
	}
	if err := s.authorizeWrite(ctx, actor, projectID, run.ChapterID); err != nil {
		return Run{}, err
	}
	return s.Repository.Cancel(ctx, actor, projectID, runID)
}

func (s *Service) ListCandidates(ctx context.Context, actor Actor, projectID, chapterID string) ([]candidateadoption.CandidateSummary, error) {
	if s.Repository == nil || !s.hasAuthorization() || actor.TenantID == 0 || strings.TrimSpace(actor.UserID) == "" {
		return nil, ErrNotFound
	}
	if err := s.authorize(ctx, actor, projectID); err != nil {
		return nil, err
	}
	candidates, err := s.Repository.ListCandidates(ctx, projectID, chapterID)
	if err != nil {
		return nil, err
	}
	for i := range candidates {
		candidates[i].Validity = s.candidateValidity(ctx, actor, projectID, chapterID, candidates[i].Basis)
		candidate, input, readErr := s.Repository.GetCandidate(ctx, projectID, candidates[i].RunID)
		if readErr != nil {
			candidates[i].Validity = candidateadoption.ValidityStale
			continue
		}
		if evidenceErr := s.validateEvidence(ctx, actor, projectID, chapterID, input); evidenceErr != nil {
			candidates[i].Validity = validityForSourceFailure(evidenceErr)
			continue
		}
		if sourceErr := s.validateCandidateSources(ctx, actor, projectID, candidate, input); sourceErr != nil {
			candidates[i].Validity = validityForSourceFailure(sourceErr)
		}
	}
	return candidates, nil
}

// validateCandidateSources 按**当前授权**复核候选引用的来源。
//
// 候选列表要能说清「这个候选为什么不能用了」：来源的授权没了与来源本身
// 变了，是两种要分开说的失效。
func (s *Service) validateCandidateSources(ctx context.Context, actor Actor, projectID string, candidate candidateadoption.Candidate, input Input) error {
	if s.Sources == nil || len(candidate.SourceIDs) == 0 {
		return nil
	}
	allowed := make(map[string]Source, len(input.Sources))
	for _, source := range input.Sources {
		allowed[source.ID] = source
	}
	used := make([]Source, 0, len(candidate.SourceIDs))
	for _, sourceID := range candidate.SourceIDs {
		source, ok := allowed[sourceID]
		if !ok {
			return ErrSourceAccessDenied
		}
		used = append(used, source)
	}
	return s.Sources.ValidateGenerationSources(ctx, actor, projectID, used)
}

// validityForSourceFailure 按失败原因区分两种失效。
//
// 「授权没了」与「输入变了」要能分开说：前者要先恢复授权再重跑，后者直接
// 重跑动作。把两者都算成 stale，会让用户去重跑一个注定还是被拒的动作。
func validityForSourceFailure(err error) candidateadoption.Validity {
	if errors.Is(err, ErrSourceAccessDenied) {
		return candidateadoption.ValidityNotAuthorized
	}
	return candidateadoption.ValidityStale
}

func (s *Service) GetCandidate(ctx context.Context, actor Actor, projectID, runID string) (candidateadoption.Candidate, error) {
	if s.Repository == nil || !s.hasAuthorization() || actor.TenantID == 0 || strings.TrimSpace(actor.UserID) == "" {
		return candidateadoption.Candidate{}, ErrNotFound
	}
	if err := s.authorize(ctx, actor, projectID); err != nil {
		return candidateadoption.Candidate{}, err
	}
	candidate, input, err := s.Repository.GetCandidate(ctx, projectID, runID)
	if err != nil {
		return candidateadoption.Candidate{}, err
	}
	if err := s.validateEvidence(ctx, actor, projectID, candidate.ChapterID, input); err != nil {
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
			candidate.Validity = candidateadoption.ValidityStale
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

func (s *Service) candidateValidity(ctx context.Context, actor Actor, projectID, chapterID string, basis candidateadoption.Basis) candidateadoption.Validity {
	if !s.hasCurrentness() {
		return candidateadoption.ValidityStale
	}
	current, err := s.current(ctx, actor, projectID, chapterID, basis)
	if err != nil || !current {
		return candidateadoption.ValidityStale
	}
	return candidateadoption.ValidityFresh
}

func (s *Service) hasAuthorization() bool {
	return s != nil && (s.Access != nil || s.Authorizer != nil)
}

func (s *Service) hasCurrentness() bool { return s != nil && (s.Access != nil || s.Currentness != nil) }

func (s *Service) authorize(ctx context.Context, actor Actor, projectID string) error {
	if s.Access != nil {
		return s.Access.Authorize(ctx, lingdocaccess.LingDocAccessContext{TenantID: actor.TenantID, UserID: actor.UserID, Role: string(actor.Role), ProjectID: projectID}, "read")
	}
	if s.Authorizer == nil {
		return ErrDependencyUnavailable
	}
	return s.Authorizer.AuthorizeGeneration(ctx, actor, projectID)
}

func (s *Service) current(ctx context.Context, actor Actor, projectID, chapterID string, basis candidateadoption.Basis) (bool, error) {
	if s.Access != nil {
		assetVersions := make([]lingdocaccess.AssetVersion, 0, len(basis.AssetVersions))
		for _, asset := range basis.AssetVersions {
			assetVersions = append(assetVersions, lingdocaccess.AssetVersion{AssetID: asset.AssetID, Revision: asset.AssetRevision})
		}
		return s.Access.Current(ctx, lingdocaccess.LingDocAccessContext{TenantID: actor.TenantID, UserID: actor.UserID, Role: string(actor.Role), ProjectID: projectID}, lingdocaccess.CurrentnessInput{
			Kind:      lingdocaccess.GenerationInput,
			ProjectID: projectID, ChapterID: chapterID, SpecRevision: int64(basis.SpecRevision), ChapterVersionID: basis.ChapterVersionID,
			TemplateID: basis.TemplateID, TemplateVersion: basis.TemplateVersion, RulesetHash: basis.RulesetHash, AssetVersions: assetVersions,
			TemplateCopyID: basis.TemplateCopyID, TemplateCopyVersion: basis.TemplateCopyVersion, TemplateCopyContentHash: basis.TemplateCopyContentHash, ContextRevision: basis.ContextRevision,
		})
	}
	if s.Currentness == nil {
		return false, ErrDependencyUnavailable
	}
	return s.Currentness.GenerationInputIsCurrent(ctx, actor, projectID, chapterID, basis)
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
	if err := sourcemarker.Validate(markdown); err != nil {
		return nil, ErrInvalidRequest
	}
	ids := sourcemarker.IDs(markdown)
	seen := make(map[string]struct{})
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			result = append(result, id)
		}
	}
	return normalizeIDs(result), nil
}

type EvidenceCardRef struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
}
type EvidenceInputValidator interface {
	ValidateGenerationEvidence(context.Context, Actor, string, string, []EvidenceCardRef, []Source) error
}
type HumanInitiationAuthorizer interface{ AuthorizeHumanGeneration(context.Context) error }
type ChapterAuthorizer interface {
	AuthorizeGenerationChapter(context.Context, Actor, string, string) error
}

func (s *Service) authorizeWrite(ctx context.Context, actor Actor, projectID, chapterID string) error {
	if s.Access != nil {
		if chapterID == "" {
			return ErrInvalidRequest
		}
		if err := s.Access.Authorize(ctx, lingdocaccess.LingDocAccessContext{TenantID: actor.TenantID, UserID: actor.UserID, Role: string(actor.Role), ProjectID: projectID}, "write:"+chapterID); err != nil {
			return err
		}
		return nil
	}
	if s.Authorizer == nil {
		return ErrDependencyUnavailable
	}
	if scoped, ok := s.Authorizer.(ChapterAuthorizer); ok {
		return scoped.AuthorizeGenerationChapter(ctx, actor, projectID, chapterID)
	}
	return s.Authorizer.AuthorizeGeneration(ctx, actor, projectID)
}
func validEvidenceCardRefs(refs []EvidenceCardRef) bool {
	if len(refs) > 20 {
		return false
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if strings.TrimSpace(ref.ID) == "" || ref.Version < 1 || seen[ref.ID] {
			return false
		}
		seen[ref.ID] = true
	}
	return true
}
func (s *Service) validateEvidence(ctx context.Context, actor Actor, projectID, chapterID string, input Input) error {
	if len(input.EvidenceCardRefs) == 0 && len(input.Request.EvidenceCardRefs) == 0 {
		if len(input.EvidenceConstraints) > 0 {
			return ErrSourceAccessDenied
		}
		return nil
	}
	if s.Evidence == nil || !validEvidenceCardRefs(input.EvidenceCardRefs) || !reflect.DeepEqual(input.EvidenceCardRefs, input.Request.EvidenceCardRefs) {
		return ErrSourceAccessDenied
	}
	if err := s.Evidence.ValidateGenerationEvidence(ctx, actor, projectID, chapterID, input.EvidenceCardRefs, input.Sources); err != nil {
		return err
	}
	if len(input.EvidenceConstraints) != len(input.EvidenceCardRefs) {
		return ErrSourceAccessDenied
	}
	port, ok := s.Evidence.(EvidenceConstraintValidator)
	if !ok {
		return ErrDependencyUnavailable
	}
	return port.ValidateGenerationEvidenceConstraints(ctx, actor, projectID, chapterID, input.EvidenceCardRefs, input.EvidenceConstraints)
}

type EvidenceConstraint struct {
	CardRef     EvidenceCardRef `json:"card_ref"`
	SupportType string          `json:"support_type"`
	Scope       string          `json:"scope"`
	Limitations string          `json:"limitations"`
	SourceIDs   []string        `json:"source_ids"`
}
type EvidenceConstraintValidator interface {
	ValidateGenerationEvidenceConstraints(context.Context, Actor, string, string, []EvidenceCardRef, []EvidenceConstraint) error
}
