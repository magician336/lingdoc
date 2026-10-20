// Package candidateadoption implements LingDoc candidate adoption.
//
// The package deliberately depends on small reader/policy interfaces. T08,
// T09 and T10 can provide their real implementations without making the
// adoption rules depend on a particular HTTP or database representation.
package candidateadoption

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Tencent/WeKnora/internal/clienterror"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/reviewanchor"
	"github.com/Tencent/WeKnora/internal/lingdoc/sourcemarker"
	"github.com/Tencent/WeKnora/internal/lingdoc/sourcepolicy"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
)

var (
	ErrInvalidRequest        = clienterror.New(clienterror.Form{Status: 400, Code: "invalid_request", Message: "请求字段不符合约定。", Retryable: false}, "invalid_request")
	ErrNotFound              = clienterror.New(clienterror.Form{Status: 404, Code: "not_found", Message: "资源不存在或不可访问。", Retryable: false}, "not_found")
	ErrForbidden             = errors.New("forbidden")
	ErrSourceAccessDenied    = clienterror.New(clienterror.Form{Status: 403, Code: "source_access_denied", Message: "资料授权已不可用。", Retryable: false}, "source_access_denied")
	ErrStaleInput            = clienterror.New(clienterror.Form{Status: 409, Code: "stale_input", Message: "快照基于的输入已变化，请重新准备。", Retryable: false}, "stale_input")
	ErrVersionConflict       = clienterror.New(clienterror.Form{Status: 409, Code: "version_conflict", Message: "内容已变化，请先读取当前版本。", Retryable: false}, "version_conflict")
	ErrInvalidState          = clienterror.New(clienterror.Form{Status: 422, Code: "invalid_state", Message: "当前项目状态或研究条件不满足操作要求。", Retryable: false}, "invalid_state")
	ErrIdempotencyConflict   = clienterror.New(clienterror.Form{Status: 409, Code: "idempotency_conflict", Message: "同一个操作键对应不同请求。", Retryable: false}, "idempotency_conflict")
	ErrDependencyUnavailable = clienterror.New(clienterror.Form{Status: 503, Code: "dependency_unavailable", Message: "依赖的服务此刻不可用，请稍后重试。", Retryable: true}, "dependency_unavailable")
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
	TemplateCopyID          string              `json:"template_copy_id,omitempty"`
	TemplateCopyVersion     int64               `json:"template_copy_version,omitempty"`
	TemplateCopyContentHash string              `json:"template_copy_content_hash,omitempty"`
	ContextRevision         int64               `json:"context_revision,omitempty"`
	EvidenceCardRefs        []core.G2VersionRef `json:"evidence_card_refs,omitempty"`
	SpecRevision            int                 `json:"spec_revision"`
	ChapterVersionID        *string             `json:"chapter_version_id"`
	TemplateID              string              `json:"template_id"`
	TemplateVersion         string              `json:"template_version"`
	RulesetHash             string              `json:"ruleset_hash"`
	AssetVersions           []AssetVersion      `json:"asset_versions"`
}

type Candidate struct {
	ID           string       `json:"id"`
	ProjectID    string       `json:"project_id"`
	ChapterID    string       `json:"chapter_id"`
	RunID        string       `json:"run_id"`
	BodyMarkdown string       `json:"body_markdown"`
	SourceIDs    []string     `json:"source_ids"`
	Basis        Basis        `json:"basis"`
	Validity     string       `json:"validity"`
	ReviewItems  []ReviewItem `json:"review_items"`
}

type CandidateSummary struct {
	ID        string    `json:"id"`
	RunID     string    `json:"run_id"`
	Validity  string    `json:"validity"`
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
	return nil
}

func validateCandidate(in AcceptCandidateInput, workspace GenerationContext, candidate Candidate) error {
	if workspace.ProjectID != in.ProjectID || workspace.ChapterID != in.ChapterID ||
		candidate.ProjectID != in.ProjectID || candidate.ChapterID != in.ChapterID {
		return ErrNotFound
	}
	if err := validateNewBasis(workspace.Basis, candidate.Basis); err != nil {
		return err
	}
	if candidate.Validity != "fresh" {
		return ErrStaleInput
	}
	if workspace.SpecRevision != in.ExpectedSpecRevision || candidate.Basis.SpecRevision != in.ExpectedSpecRevision {
		return ErrVersionConflict
	}
	if workspace.Basis.TemplateID != "" && candidate.Basis.TemplateID != workspace.Basis.TemplateID {
		return ErrStaleInput
	}
	if workspace.Basis.TemplateVersion != "" && candidate.Basis.TemplateVersion != workspace.Basis.TemplateVersion {
		return ErrStaleInput
	}
	if workspace.Basis.RulesetHash != "" && candidate.Basis.RulesetHash != workspace.Basis.RulesetHash {
		return ErrStaleInput
	}
	if len(candidate.Basis.AssetVersions) > 0 {
		if !currentAssetSubset(workspace.Basis.AssetVersions, candidate.Basis.AssetVersions) {
			return ErrStaleInput
		}
	} else if candidateHasNewBasis(candidate.Basis) && len(candidate.SourceIDs) > 0 {
		return ErrStaleInput
	}
	if !sameOptionalString(candidate.Basis.ChapterVersionID, in.ExpectedChapterVersionID) {
		return ErrVersionConflict
	}
	if !sameOptionalString(workspace.ChapterVersionID, in.ExpectedChapterVersionID) {
		return ErrVersionConflict
	}
	if workspace.Chapter.CurrentVersionID != nil && !in.ReplaceExisting {
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

func requestHash(in AcceptCandidateInput) string {
	b, _ := json.Marshal(struct {
		ProjectID                string  `json:"project_id"`
		ChapterID                string  `json:"chapter_id"`
		CandidateID              string  `json:"candidate_id"`
		ExpectedChapterVersionID *string `json:"expected_chapter_version_id"`
		ExpectedSpecRevision     int     `json:"expected_spec_revision"`
		ReplaceExisting          bool    `json:"replace_existing"`
	}{in.ProjectID, in.ChapterID, in.CandidateID, in.ExpectedChapterVersionID, in.ExpectedSpecRevision, in.ReplaceExisting})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
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
