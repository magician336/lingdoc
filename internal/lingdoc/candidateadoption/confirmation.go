package candidateadoption

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ReviewDecision struct {
	ReviewItemID string `json:"review_item_id"`
	Disposition  string `json:"disposition"`
	Reason       string `json:"reason"`
}

type Confirmation struct {
	ID               string           `json:"id"`
	ChapterID        string           `json:"chapter_id"`
	ChapterVersionID string           `json:"chapter_version_id"`
	SpecRevision     int              `json:"spec_revision"`
	AssetVersions    []AssetVersion   `json:"asset_versions"`
	TemplateVersion  string           `json:"template_version"`
	ActorUserID      string           `json:"actor_user_id"`
	CreatedAt        time.Time        `json:"created_at"`
	Valid            bool             `json:"valid"`
	ReviewDecisions  []ReviewDecision `json:"review_decisions"`
}

type ConfirmChapterInput struct {
	ProjectID                string
	ChapterID                string
	ActorID                  string
	IdempotencyKey           string
	ExpectedChapterVersionID string
	ExpectedSpecRevision     int
	Decisions                []ReviewDecision
}

type ConfirmationWorkspace interface {
	GenerationContext(context.Context, string, string) (GenerationContext, error)
}

// ConfirmationSourcePolicy must check that the chapter's exact source IDs are
// still current, authorized, ready, and belong to the project, and return the
// current asset revisions used by those sources. T09 adapters should use
// persisted source/asset revisions rather than reconstructing them from IDs.
type ConfirmationSourcePolicy interface {
	ValidateCurrent(context.Context, string, string, []string) ([]AssetVersion, error)
}

type ConfirmationWriter interface {
	ConfirmChapter(context.Context, ConfirmChapterInput, GenerationContext) (Confirmation, bool, error)
}

type ConfirmationReplayReader interface {
	ReplayConfirmation(context.Context, ConfirmChapterInput) (*Confirmation, error)
}

type ConfirmationService struct {
	Workspace   ConfirmationWorkspace
	Sources     ConfirmationSourcePolicy
	Authorizer  Authorizer
	Writer      ConfirmationWriter
	Idempotency ConfirmationReplayReader
}

func NewConfirmationService(store *SQLiteCandidateAdoptionStore, sources ConfirmationSourcePolicy, authorizer Authorizer) *ConfirmationService {
	return &ConfirmationService{Workspace: store, Sources: sources, Authorizer: authorizer, Writer: store, Idempotency: store}
}

func (s *ConfirmationService) ConfirmChapter(ctx context.Context, in ConfirmChapterInput) (Confirmation, bool, error) {
	if strings.TrimSpace(in.ProjectID) == "" || strings.TrimSpace(in.ChapterID) == "" ||
		strings.TrimSpace(in.ActorID) == "" || len(strings.TrimSpace(in.IdempotencyKey)) < 8 ||
		len(in.IdempotencyKey) > 128 || strings.TrimSpace(in.ExpectedChapterVersionID) == "" || in.ExpectedSpecRevision < 0 {
		return Confirmation{}, false, ErrInvalidRequest
	}
	if s.Authorizer == nil {
		return Confirmation{}, false, ErrInvalidState
	}
	if err := s.Authorizer.Authorize(ctx, in.ActorID, in.ProjectID, in.ChapterID); err != nil {
		return Confirmation{}, false, err
	}
	// Check replay after current authorization but before reading mutable state,
	// so a lost response can be recovered even if the chapter later changes.
	if s.Idempotency != nil {
		replay, err := s.Idempotency.ReplayConfirmation(ctx, in)
		if err != nil {
			return Confirmation{}, false, err
		}
		if replay != nil {
			return *replay, true, nil
		}
	}
	if s.Workspace == nil || s.Writer == nil {
		return Confirmation{}, false, ErrInvalidState
	}
	workspace, err := s.Workspace.GenerationContext(ctx, in.ProjectID, in.ChapterID)
	if err != nil {
		return Confirmation{}, false, err
	}
	if workspace.ProjectID != in.ProjectID || workspace.ChapterID != in.ChapterID {
		return Confirmation{}, false, ErrNotFound
	}
	if workspace.ChapterVersionID == nil || *workspace.ChapterVersionID != in.ExpectedChapterVersionID ||
		workspace.SpecRevision != in.ExpectedSpecRevision || workspace.Basis.SpecRevision != in.ExpectedSpecRevision {
		return Confirmation{}, false, ErrVersionConflict
	}
	if len(workspace.Chapter.SourceIDs) > 0 {
		if s.Sources == nil {
			return Confirmation{}, false, fmt.Errorf("%w: current source policy is unavailable", ErrInvalidState)
		}
		assetVersions, err := s.Sources.ValidateCurrent(ctx, in.ProjectID, in.ActorID, workspace.Chapter.SourceIDs)
		if err != nil {
			return Confirmation{}, false, err
		}
		workspace.Basis.AssetVersions = append([]AssetVersion{}, assetVersions...)
	}
	if err := validateReviewDecisions(workspace.Chapter.ReviewItems, in.Decisions); err != nil {
		return Confirmation{}, false, err
	}
	return s.Writer.ConfirmChapter(ctx, in, workspace)
}

func validateReviewDecisions(items []ReviewItem, decisions []ReviewDecision) error {
	known := make(map[string]struct{}, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.ID) == "" {
			return ErrInvalidState
		}
		if _, ok := known[item.ID]; ok {
			return ErrInvalidState
		}
		known[item.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(decisions))
	for _, d := range decisions {
		if _, ok := known[d.ReviewItemID]; !ok {
			return ErrInvalidRequest
		}
		if _, ok := seen[d.ReviewItemID]; ok {
			return ErrInvalidRequest
		}
		seen[d.ReviewItemID] = struct{}{}
		if d.Disposition != "resolved" && d.Disposition != "retained_warning" {
			return ErrInvalidRequest
		}
		if strings.TrimSpace(d.Reason) == "" || len([]rune(d.Reason)) > 2000 {
			return ErrInvalidRequest
		}
	}
	if len(seen) != len(known) {
		return ErrInvalidRequest
	}
	return nil
}

func confirmationRequestHash(in ConfirmChapterInput) string {
	decisions := append([]ReviewDecision(nil), in.Decisions...)
	sort.Slice(decisions, func(i, j int) bool { return decisions[i].ReviewItemID < decisions[j].ReviewItemID })
	b, _ := json.Marshal(struct {
		Project, Chapter, Version string
		Revision                  int
		Decisions                 []ReviewDecision
	}{
		in.ProjectID, in.ChapterID, in.ExpectedChapterVersionID, in.ExpectedSpecRevision, decisions,
	})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func newConfirmation(in ConfirmChapterInput, workspace GenerationContext) Confirmation {
	decisions := append([]ReviewDecision(nil), in.Decisions...)
	sort.Slice(decisions, func(i, j int) bool { return decisions[i].ReviewItemID < decisions[j].ReviewItemID })
	return Confirmation{ID: uuid.NewString(), ChapterID: in.ChapterID,
		ChapterVersionID: in.ExpectedChapterVersionID, SpecRevision: in.ExpectedSpecRevision,
		AssetVersions:   append([]AssetVersion{}, workspace.Basis.AssetVersions...),
		TemplateVersion: workspace.Basis.TemplateVersion, ActorUserID: in.ActorID,
		CreatedAt: time.Now().UTC(), Valid: true, ReviewDecisions: decisions}
}
