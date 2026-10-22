// Package candidateadoption implements LingDoc candidate adoption.
//
// The package deliberately depends on small reader/policy interfaces. T08,
// T09 and T10 can provide their real implementations without making the
// adoption rules depend on a particular HTTP or database representation.
package candidateadoption

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/clienterror"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/reviewanchor"
	"github.com/Tencent/WeKnora/internal/lingdoc/sourcemarker"
	"github.com/Tencent/WeKnora/internal/lingdoc/sourcepolicy"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/Tencent/WeKnora/internal/types"
)

var (
	ErrInvalidRequest        = clienterror.New(clienterror.Form{Status: 400, Code: "invalid_request", Message: "请求字段不符合约定。", Retryable: false}, "invalid_request")
	ErrNotFound              = clienterror.New(clienterror.Form{Status: 404, Code: "not_found", Message: "资源不存在或不可访问。", Retryable: false}, "not_found")
	ErrForbidden             = clienterror.New(clienterror.Forbidden, "forbidden")
	ErrSourceAccessDenied    = clienterror.New(clienterror.Form{Status: 403, Code: "source_access_denied", Message: "资料授权已不可用。", Retryable: false}, "source_access_denied")
	ErrStaleInput            = clienterror.New(clienterror.Form{Status: 409, Code: "stale_input", Message: "快照基于的输入已变化，请重新准备。", Retryable: false}, "stale_input")
	ErrVersionConflict       = clienterror.New(clienterror.Form{Status: 409, Code: "version_conflict", Message: "内容已变化，请先读取当前版本。", Retryable: false}, "version_conflict")
	ErrInvalidState          = clienterror.New(clienterror.Form{Status: 422, Code: "invalid_state", Message: "当前项目状态或研究条件不满足操作要求。", Retryable: false}, "invalid_state")
	ErrIdempotencyConflict   = clienterror.New(clienterror.Form{Status: 409, Code: "idempotency_conflict", Message: "同一个操作键对应不同请求。", Retryable: false}, "idempotency_conflict")
	ErrIdempotencyKeyExpired = clienterror.New(clienterror.IdempotencyKeyExpired, "idempotency_key_expired")
	ErrDependencyUnavailable = clienterror.New(clienterror.Form{Status: 503, Code: "dependency_unavailable", Message: "依赖的服务此刻不可用，请稍后重试。", Retryable: true}, "dependency_unavailable")
	// ErrCandidateActionUnknown 说清「这个候选没有记录自己是怎么来的」。
	//
	// 采纳是一次显式确认：确认载荷要说清采纳的是哪个动作的结果。候选自己
	// 没记动作，就没法被确认——它必须重新跑一次动作化路径，而不是被猜一个
	// 动作放行。这个形态单独存在，免得它落进泛泛的 400 里让人猜。
	ErrCandidateActionUnknown = clienterror.New(clienterror.CandidateActionUnknown, "candidate_action_unknown")
	// ErrCandidateStale says the candidate's inputs changed.
	//
	// It wraps ErrStaleInput, so the older reason stays reachable, and it
	// carries the recovery step in its own wording: the inputs changed means
	// run the action again.
	ErrCandidateStale = clienterror.Wrap(clienterror.CandidateStaleAction, "candidate_stale_input", ErrStaleInput)
	// ErrCandidateNotAuthorized says the authorization the candidate rests on
	// is gone.
	//
	// It wraps ErrSourceAccessDenied so the source-level reason stays
	// reachable, and it answers with its own form: the candidate-level state
	// is "not authorized", and the recovery step is "restore the
	// authorization and run the action again" -- not "find the permission that
	// was never missing".
	ErrCandidateNotAuthorized = clienterror.Wrap(clienterror.CandidateNotAuthorized, "candidate_not_authorized", ErrSourceAccessDenied)
	// ErrWorkingCopyDirty says the caller is not looking at the content the
	// server would replace.
	//
	// The caller declared which working copy it acts on -- the revision it read
	// and the digest of the body it saw. The server sees a different body:
	// either the caller's editor holds changes that were never saved, or the
	// draft moved under it. The form carries the next step: save the edits
	// first, or ask for the replacement to carry a recoverable snapshot.
	ErrWorkingCopyDirty = clienterror.New(clienterror.WorkingCopyDirty, "working_copy_dirty")
)

// ReviewItem is copied from the candidate into every adopted version. It is
// intentionally immutable here; T12 owns disposition and confirmation.
type ReviewItem struct {
	ID                string               `json:"id"`
	Statement         string               `json:"statement"`
	OriginCandidateID string               `json:"origin_candidate_id"`
	Anchor            *reviewanchor.Anchor `json:"anchor,omitempty"`
}

type CitationUsage struct {
	SourceID   string `json:"source_id"`
	Purpose    string `json:"purpose,omitempty"`
	Limitation string `json:"limitation,omitempty"`
}

type AssetVersion struct {
	AssetID       string `json:"asset_id"`
	AssetRevision int    `json:"asset_revision"`
}

type Basis struct {
	TemplateCopyID          string            `json:"template_copy_id,omitempty"`
	TemplateCopyVersion     int64             `json:"template_copy_version,omitempty"`
	TemplateCopyContentHash string            `json:"template_copy_content_hash,omitempty"`
	ContextRevision         int64             `json:"context_revision,omitempty"`
	EvidenceCardRefs        []core.VersionRef `json:"evidence_card_refs,omitempty"`
	SpecRevision            int               `json:"spec_revision"`
	ChapterVersionID        *string           `json:"chapter_version_id"`
	TemplateID              string            `json:"template_id"`
	TemplateVersion         string            `json:"template_version"`
	RulesetHash             string            `json:"ruleset_hash"`
	AssetVersions           []AssetVersion    `json:"asset_versions"`
}

type Candidate struct {
	ID           string       `json:"id"`
	ProjectID    string       `json:"project_id"`
	ChapterID    string       `json:"chapter_id"`
	RunID        string       `json:"run_id"`
	BodyMarkdown string       `json:"body_markdown"`
	SourceIDs    []string     `json:"source_ids"`
	Basis        Basis        `json:"basis"`
	Validity     Validity     `json:"validity"`
	ReviewItems  []ReviewItem `json:"review_items"`

	// WritingAction is the writing action that produced this candidate.
	// The vocabulary belongs to the writingaction package: the action
	// semantics have exactly one owner, and a candidate only records which
	// action it came from.
	WritingAction writingaction.Action `json:"writing_action,omitempty"`
	// Origin says who the candidate text is from. The three values belong to
	// the writingaction package too, so the origin vocabulary has one owner.
	Origin writingaction.Origin `json:"origin,omitempty"`
	// RunMode says how the run that produced this candidate reached the
	// model.
	RunMode RunMode `json:"run_mode,omitempty"`
	// ModelID identifies the model that produced this candidate.
	ModelID string `json:"model_id,omitempty"`
}

type CandidateSummary struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	Validity  Validity  `json:"validity"`
	CreatedAt time.Time `json:"created_at"`
	Basis     Basis     `json:"-"`
}

type Chapter struct {
	ID                string          `json:"id"`
	ProjectID         string          `json:"project_id"`
	SectionID         string          `json:"section_id"`
	Title             string          `json:"title"`
	CurrentVersionID  *string         `json:"current_version_id"`
	BodyMarkdown      string          `json:"body_markdown"`
	SourceIDs         []string        `json:"source_ids"`
	CitationUsages    []CitationUsage `json:"citation_usages"`
	ReviewItems       []ReviewItem    `json:"review_items"`
	ConfirmationValid bool            `json:"confirmation_valid"`
}

type GenerationContext struct {
	ProjectID        string
	ChapterID        string
	SpecRevision     int
	ChapterVersionID *string
	Basis            Basis
	Chapter          Chapter
}

type AcceptCandidateInput struct {
	ProjectID                string
	ChapterID                string
	CandidateID              string
	ActorID                  string
	IdempotencyKey           string
	ExpectedChapterVersionID *string `json:"expected_chapter_version_id"`
	ExpectedSpecRevision     int     `json:"expected_spec_revision"`
	ReplaceExisting          bool    `json:"replace_existing"`
	// ExpectedWorkingCopyRevision 是「用户看到的是哪一份工作副本」。
	//
	// 它与服务端当前的工作副本修订号对不上，说明别人改过这份草稿：请重新读取。
	// 它不是可选项——没有这份声明，服务端无从判断自己替换的是不是用户正在看
	// 的那份内容。
	ExpectedWorkingCopyRevision int64 `json:"expected_working_copy_revision"`
	// ExpectedBodyHash 是「用户看到的正文摘要」。
	//
	// 算法：正文的 UTF-8 字节的 SHA-256，小写十六进制、64 个字符
	// （workingCopyBodyHash）。它与服务端当前的工作副本正文摘要对不上，说明
	// 用户编辑器里有没保存的内容，或者内容已经变了。
	ExpectedBodyHash string `json:"expected_body_hash"`
	// SnapshotID 是「带快照的替换」的快照标识。
	//
	// 带它，服务端会在替换前把当前工作副本正文与替换范围记进审计详情，然后
	// 允许在 dirty 的情况下继续。这**不是**一张可恢复快照表（那要新迁移），
	// 它是审计留痕：能追、能人工恢复。
	SnapshotID string `json:"snapshot_id"`
	// Confirmation 是这次采纳的显式确认载荷。
	//
	// 它是**指针**：缺省即「没确认」，与「确认了但字段为空」要能分开。它必填，
	// 没有例外路径——重放与待决探测也不例外。
	Confirmation *AdoptionConfirmation `json:"confirmation,omitempty"`
}

type AcceptResult struct {
	Chapter  Chapter `json:"data"`
	Replayed bool    `json:"replayed"`
}

type WorkspaceReader interface {
	GenerationContext(context.Context, string, string) (GenerationContext, error)
}

type ChapterReader interface {
	ListChapters(context.Context, string) ([]Chapter, error)
}

type CandidateReader interface {
	GetCandidate(context.Context, string, string) (Candidate, error)
}

type SourcePolicy = sourcepolicy.SourcePolicy

type Authorizer interface {
	// capability is "read" for reads and "write:<chapter>" for adoption.
	Authorize(context.Context, string, string, string) error
}

type WorkspaceWriter interface {
	AcceptCandidate(context.Context, AcceptCandidateInput, Candidate) (AcceptResult, error)
}

type IdempotencyReader interface {
	ReplayCandidateAcceptance(context.Context, AcceptCandidateInput) (*AcceptResult, error)
}

type CandidateAdoptionService struct {
	Workspace   WorkspaceReader
	Candidates  CandidateReader
	Sources     SourcePolicy
	Reviewer    sourcepolicy.Reviewer
	Evidence    CandidateEvidencePolicy
	Authorizer  Authorizer
	Writer      WorkspaceWriter
	Idempotency IdempotencyReader
}

func (s *CandidateAdoptionService) authorize(ctx context.Context, actorID, projectID, capability string) error {
	if s.Authorizer == nil {
		return ErrDependencyUnavailable
	}
	return s.Authorizer.Authorize(ctx, actorID, projectID, capability)
}

func (s *CandidateAdoptionService) validateSources(ctx context.Context, projectID, actorID string, sourceIDs []string) error {
	if len(sourceIDs) == 0 {
		return nil
	}
	if s.Reviewer == nil && s.Sources == nil {
		return ErrSourceAccessDenied
	}
	if s.Reviewer != nil {
		tenantID, ok := types.TenantIDFromContext(ctx)
		if !ok || tenantID == 0 || strings.TrimSpace(actorID) == "" {
			return ErrSourceAccessDenied
		}
		caller := types.CallerFromContext(ctx)
		if caller.TenantID != tenantID || caller.UserID != actorID {
			return ErrSourceAccessDenied
		}
		if id, ok := types.UserIDFromContext(ctx); ok && id != actorID {
			return ErrSourceAccessDenied
		}
		checked := types.WithExecutionTenant(ctx, tenantID)
		verdicts, err := s.Reviewer.Review(checked, projectID, evidence.Actor{UserID: actorID, TenantID: strconv.FormatUint(tenantID, 10)}, sourceIDs)
		if err != nil {
			return err
		}
		stale, denied := false, false
		for _, verdict := range verdicts {
			if verdict.Usable {
				continue
			}
			if verdict.AssetDeny == "" {
				stale = true
			} else {
				denied = true
			}
		}
		if stale || len(verdicts) != len(sourceIDs) {
			return ErrStaleInput
		}
		if denied {
			return ErrSourceAccessDenied
		}
		return nil
	}
	return legacySourcePolicyValidate(ctx, projectID, actorID, s.Sources, sourceIDs)
}

// legacySourcePolicyValidate is the explicit compatibility adapter for
// callers that have not yet supplied the shared Reviewer. Production
// composition injects Reviewer and never takes this path.
func legacySourcePolicyValidate(ctx context.Context, projectID, actorID string, policy SourcePolicy, sourceIDs []string) error {
	if policy == nil {
		return ErrSourceAccessDenied
	}
	return policy.Validate(ctx, projectID, actorID, sourceIDs)
}

// NewCandidateAdoptionService wires the fixed SQLite implementation. Callers that already
// have real T08/T09/T10 adapters can construct CandidateAdoptionService directly instead.
func NewCandidateAdoptionService(store *SQLiteCandidateAdoptionStore, sources SourcePolicy, authorizer Authorizer) *CandidateAdoptionService {
	reviewer, _ := sources.(sourcepolicy.Reviewer)
	evidence, _ := sources.(CandidateEvidencePolicy)
	if store != nil {
		store.evidencePolicy = evidence
		store.authorizer = authorizer
	}
	return &CandidateAdoptionService{
		Workspace: store, Candidates: store, Sources: sources, Reviewer: reviewer, Evidence: evidence, Authorizer: authorizer,
		Writer: store, Idempotency: store,
	}
}

func (s *CandidateAdoptionService) AcceptCandidate(ctx context.Context, in AcceptCandidateInput) (AcceptResult, error) {
	// 人工门禁先于一切：机器身份与代他人操作不得发起采纳。
	//
	// 它是一次身份检查，不是客户端的「已确认」标志——所以它放在请求内容
	// 的校验之前：连一次格式不对的机器请求也不该走到写路径。
	if err := core.RequireHumanDecision(ctx); err != nil {
		return AcceptResult{}, err
	}
	if err := validateInput(in); err != nil {
		return AcceptResult{}, err
	}
	if err := s.authorize(ctx, in.ActorID, in.ProjectID, "write:"+in.ChapterID); err != nil {
		return AcceptResult{}, err
	}
	if s.Idempotency == nil || s.Workspace == nil || s.Candidates == nil || s.Writer == nil {
		return AcceptResult{}, ErrDependencyUnavailable
	}
	pending := true
	if probe, ok := s.Idempotency.(acceptanceReplayProbe); ok {
		var err error
		pending, err = probe.CandidateAcceptancePending(ctx, in)
		if err != nil {
			return AcceptResult{}, err
		}
	}
	candidate, err := s.Candidates.GetCandidate(ctx, in.ProjectID, in.CandidateID)
	if err != nil {
		return AcceptResult{}, err
	}
	if candidate.ProjectID != in.ProjectID || candidate.ChapterID != in.ChapterID {
		return AcceptResult{}, ErrNotFound
	}
	// 确认载荷必须与候选对得上：确认的动作与来源要和候选实际记录的一致。
	if err := validateConfirmationAgainstCandidate(in, candidate); err != nil {
		return AcceptResult{}, err
	}
	// 候选还能不能用，要按**当前授权**真算一次：两种失效要能分开说，
	// 下一步也要跟着说——「输入变了」重跑动作，「授权没了」先恢复授权。
	validity, err := s.candidateValidityUnderCurrentAuthorization(ctx, in, candidate)
	if err != nil {
		return AcceptResult{}, err
	}
	switch validity {
	case ValidityNotAuthorized:
		return AcceptResult{}, ErrCandidateNotAuthorized
	case ValidityStale:
		return AcceptResult{}, ErrCandidateStale
	}
	if !pending {
		workspace, e := s.Workspace.GenerationContext(ctx, in.ProjectID, in.ChapterID)
		if e != nil {
			return AcceptResult{}, e
		}
		if e = validateCandidate(in, workspace, candidate); e != nil {
			return AcceptResult{}, e
		}
	}
	prepared, err := s.prepareCandidateEvidence(ctx, in, candidate)
	if err != nil {
		return AcceptResult{}, err
	}
	replay, err := s.Idempotency.ReplayCandidateAcceptance(prepared, in)
	if err != nil {
		return AcceptResult{}, err
	}
	if replay != nil {
		if err = s.recheckCandidateAcceptance(ctx, in, candidate); err != nil {
			return AcceptResult{}, err
		}
		replay.Replayed = true
		return *replay, nil
	}
	if pending {
		workspace, e := s.Workspace.GenerationContext(ctx, in.ProjectID, in.ChapterID)
		if e != nil {
			return AcceptResult{}, e
		}
		if e = validateCandidate(in, workspace, candidate); e != nil {
			return AcceptResult{}, e
		}
	}
	result, err := s.Writer.AcceptCandidate(prepared, in, candidate)
	if err != nil {
		return AcceptResult{}, err
	}
	if err = s.recheckCandidateAcceptance(ctx, in, candidate); err != nil {
		return AcceptResult{}, fmt.Errorf("candidate acceptance committed; current access recheck failed: %w", err)
	}
	return result, nil
}

func validateInput(in AcceptCandidateInput) error {
	if strings.TrimSpace(in.ProjectID) == "" || strings.TrimSpace(in.ChapterID) == "" ||
		strings.TrimSpace(in.CandidateID) == "" || strings.TrimSpace(in.ActorID) == "" ||
		len(strings.TrimSpace(in.IdempotencyKey)) < 8 || len(in.IdempotencyKey) > 128 ||
		in.ExpectedSpecRevision < 0 {
		return ErrInvalidRequest
	}
	// 客户端必须说清它动的是哪一份工作副本：修订号与正文摘要缺一不可。
	//
	// 缺了它们，服务端就没有判据去分辨「用户正在看的就是这份」与「用户编辑器
	// 里有没保存的东西」——而后者正是整段替换最不该悄悄吃掉的东西。
	if in.ExpectedWorkingCopyRevision < 0 || !validBodyHash(in.ExpectedBodyHash) {
		return ErrInvalidRequest
	}
	// 确认载荷必填：没有它就没有「这次采纳确认了什么」。
	return validateAdoptionConfirmation(in)
}

func validateCandidate(in AcceptCandidateInput, workspace GenerationContext, candidate Candidate) error {
	if workspace.ProjectID != in.ProjectID || workspace.ChapterID != in.ChapterID ||
		candidate.ProjectID != in.ProjectID || candidate.ChapterID != in.ChapterID {
		return ErrNotFound
	}
	if err := validateNewBasis(workspace.Basis, candidate.Basis); err != nil {
		return err
	}
	// 候选记下的可用性有两种失效，说法要分开：授权没了与输入变了。
	switch candidate.Validity {
	case ValidityNotAuthorized:
		return ErrCandidateNotAuthorized
	case ValidityFresh:
	default:
		return ErrCandidateStale
	}
	if workspace.SpecRevision != in.ExpectedSpecRevision || candidate.Basis.SpecRevision != in.ExpectedSpecRevision {
		return ErrVersionConflict
	}
	if workspace.Basis.TemplateID != "" && candidate.Basis.TemplateID != workspace.Basis.TemplateID {
		return ErrCandidateStale
	}
	if workspace.Basis.TemplateVersion != "" && candidate.Basis.TemplateVersion != workspace.Basis.TemplateVersion {
		return ErrCandidateStale
	}
	if workspace.Basis.RulesetHash != "" && candidate.Basis.RulesetHash != workspace.Basis.RulesetHash {
		return ErrCandidateStale
	}
	if len(candidate.Basis.AssetVersions) > 0 {
		if !currentAssetSubset(workspace.Basis.AssetVersions, candidate.Basis.AssetVersions) {
			return ErrCandidateStale
		}
	} else if candidateHasNewBasis(candidate.Basis) && len(candidate.SourceIDs) > 0 {
		return ErrCandidateStale
	}
	if !sameOptionalString(candidate.Basis.ChapterVersionID, in.ExpectedChapterVersionID) {
		return ErrVersionConflict
	}
	if !sameOptionalString(workspace.ChapterVersionID, in.ExpectedChapterVersionID) {
		return ErrVersionConflict
	}
	// 「章节已经有正式版本」这道门只管**正式版本**那条路：写工作副本的两个
	// 目标不碰正式版本，用户已写的正式正文不会被替换，所以不需要「replace_existing」
	// 这句同意。把它套到只写草稿的路径上，是拿一条不相干的门挡住一次无害的
	// 写入。
	if workspace.Chapter.CurrentVersionID != nil && !in.ReplaceExisting && writesFormalVersion(in) {
		return ErrInvalidState
	}
	if err := validateSourceReferences(candidate.BodyMarkdown, candidate.SourceIDs); err != nil {
		return err
	}
	return nil
}

func sameOptionalString(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func validateSourceReferences(body string, sourceIDs []string) error {
	want := make(map[string]struct{}, len(sourceIDs))
	for _, id := range sourceIDs {
		if strings.TrimSpace(id) == "" {
			return ErrInvalidRequest
		}
		want[id] = struct{}{}
	}
	got := make(map[string]struct{})
	if err := sourcemarker.Validate(body); err != nil {
		return ErrInvalidRequest
	}
	for _, id := range sourcemarker.IDs(body) {
		got[id] = struct{}{}
	}
	if len(want) != len(got) {
		return ErrInvalidRequest
	}
	for id := range want {
		if _, ok := got[id]; !ok {
			return ErrInvalidRequest
		}
	}
	return nil
}

func normalizeIDs(ids []string) []string {
	copyIDs := append([]string(nil), ids...)
	sort.Strings(copyIDs)
	return copyIDs
}

func currentAssetSubset(current, used []AssetVersion) bool {
	available := map[string]int{}
	for _, asset := range current {
		available[asset.AssetID] = asset.AssetRevision
	}
	seen := map[string]bool{}
	for _, asset := range used {
		if asset.AssetID == "" || asset.AssetRevision < 1 || seen[asset.AssetID] || available[asset.AssetID] != asset.AssetRevision {
			return false
		}
		seen[asset.AssetID] = true
	}
	return true
}
