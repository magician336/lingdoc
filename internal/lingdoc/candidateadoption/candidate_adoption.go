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
	"sort"
	"strings"
)

var (
	ErrInvalidRequest        = errors.New("invalid_request")
	ErrNotFound              = errors.New("not_found")
	ErrForbidden             = errors.New("forbidden")
	ErrSourceAccessDenied    = errors.New("source_access_denied")
	ErrStaleInput            = errors.New("stale_input")
	ErrVersionConflict       = errors.New("version_conflict")
	ErrInvalidState          = errors.New("invalid_state")
	ErrIdempotencyConflict   = errors.New("idempotency_conflict")
	ErrDependencyUnavailable = errors.New("dependency_unavailable")
)

// ReviewItem is copied from the candidate into every adopted version. It is
// intentionally immutable here; T12 owns disposition and confirmation.
type ReviewItem struct {
	ID                string `json:"id"`
	Statement         string `json:"statement"`
	OriginCandidateID string `json:"origin_candidate_id"`
}

type AssetVersion struct {
	AssetID       string `json:"asset_id"`
	AssetRevision int    `json:"asset_revision"`
}

type Basis struct {
	SpecRevision     int            `json:"spec_revision"`
	ChapterVersionID *string        `json:"chapter_version_id"`
	TemplateID       string         `json:"template_id"`
	TemplateVersion  string         `json:"template_version"`
	RulesetHash      string         `json:"ruleset_hash"`
	AssetVersions    []AssetVersion `json:"asset_versions"`
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

type Chapter struct {
	ID                string       `json:"id"`
	ProjectID         string       `json:"project_id"`
	SectionID         string       `json:"section_id"`
	Title             string       `json:"title"`
	CurrentVersionID  *string      `json:"current_version_id"`
	BodyMarkdown      string       `json:"body_markdown"`
	SourceIDs         []string     `json:"source_ids"`
	ReviewItems       []ReviewItem `json:"review_items"`
	ConfirmationValid bool         `json:"confirmation_valid"`
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

type SourcePolicy interface {
	Validate(context.Context, string, string, []string) error
}

type Authorizer interface {
	// capability is "read" for candidate/chapter reads and "write" for adoption.
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
	if s.Sources == nil {
		return ErrSourceAccessDenied
	}
	return s.Sources.Validate(ctx, projectID, actorID, sourceIDs)
}

// NewCandidateAdoptionService wires the fixed SQLite implementation. Callers that already
// have real T08/T09/T10 adapters can construct CandidateAdoptionService directly instead.
func NewCandidateAdoptionService(store *SQLiteCandidateAdoptionStore, sources SourcePolicy, authorizer Authorizer) *CandidateAdoptionService {
	return &CandidateAdoptionService{
		Workspace: store, Candidates: store, Sources: sources, Authorizer: authorizer,
		Writer: store, Idempotency: store,
	}
}

func (s *CandidateAdoptionService) AcceptCandidate(ctx context.Context, in AcceptCandidateInput) (AcceptResult, error) {
	if err := validateInput(in); err != nil {
		return AcceptResult{}, err
	}
	if err := s.authorize(ctx, in.ActorID, in.ProjectID, "write"); err != nil {
		return AcceptResult{}, err
	}
	// Replay is checked before candidate/context version checks. This is what
	// lets a client recover a lost response with the original idempotency key.
	if s.Idempotency == nil || s.Workspace == nil || s.Candidates == nil || s.Writer == nil {
		return AcceptResult{}, ErrDependencyUnavailable
	}
	result, err := s.Idempotency.ReplayCandidateAcceptance(ctx, in)
	if err != nil {
		return AcceptResult{}, err
	}
	if result != nil {
		if err := s.validateSources(ctx, in.ProjectID, in.ActorID, result.Chapter.SourceIDs); err != nil {
			return AcceptResult{}, err
		}
		result.Replayed = true
		return *result, nil
	}
	workspace, err := s.Workspace.GenerationContext(ctx, in.ProjectID, in.ChapterID)
	if err != nil {
		return AcceptResult{}, err
	}
	candidate, err := s.Candidates.GetCandidate(ctx, in.ProjectID, in.CandidateID)
	if err != nil {
		return AcceptResult{}, err
	}
	if err := validateCandidate(in, workspace, candidate); err != nil {
		return AcceptResult{}, err
	}
	if err := s.validateSources(ctx, in.ProjectID, in.ActorID, candidate.SourceIDs); err != nil {
		return AcceptResult{}, err
	}
	return s.Writer.AcceptCandidate(ctx, in, candidate)
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
	if len(workspace.Basis.AssetVersions) > 0 && !sameAssetVersions(workspace.Basis.AssetVersions, candidate.Basis.AssetVersions) {
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

func sameAssetVersions(a, b []AssetVersion) bool {
	if len(a) != len(b) {
		return false
	}
	left := append([]AssetVersion(nil), a...)
	right := append([]AssetVersion(nil), b...)
	sort.Slice(left, func(i, j int) bool { return left[i].AssetID < left[j].AssetID })
	sort.Slice(right, func(i, j int) bool { return right[i].AssetID < right[j].AssetID })
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

func validateSourceReferences(body string, sourceIDs []string) error {
	want := make(map[string]struct{}, len(sourceIDs))
	for _, id := range sourceIDs {
		if strings.TrimSpace(id) == "" {
			return ErrInvalidRequest
		}
		want[id] = struct{}{}
	}
	got := make(map[string]struct{})
	for i := 0; i < len(body); {
		start := strings.Index(body[i:], "[[source:")
		if start < 0 {
			break
		}
		start += i + len("[[source:")
		end := strings.Index(body[start:], "]]")
		if end < 0 {
			return ErrInvalidRequest
		}
		id := strings.TrimSpace(body[start : start+end])
		if id == "" {
			return ErrInvalidRequest
		}
		got[id] = struct{}{}
		i = start + end + 2
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
