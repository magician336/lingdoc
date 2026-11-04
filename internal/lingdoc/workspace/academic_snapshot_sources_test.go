package workspace

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

type academicSnapshotSelectorStub struct {
	selected core.EvidenceSelection
	denied   bool
}

type academicSnapshotLiveSources struct {
	SourceApplicationService
	denied bool
}

func (s *academicSnapshotLiveSources) GetSource(_ context.Context, _ Actor, projectID, id string) (evidence.Source, error) {
	if s.denied {
		return evidence.Source{}, ErrRewriteSourceDenied
	}
	return evidence.Source{ID: id, ProjectID: projectID, AssetID: "asset", AssetRevision: 1, Status: evidence.SourceAvailable, Locator: "live location", QuotedText: "live text", QuotedTextHash: researchTextHash("live text"), Anchor: evidence.Anchor{ChunkID: id, StartAt: 3, EndAt: 5}}, nil
}

func (s *academicSnapshotSelectorStub) SelectGenerationEvidence(_ context.Context, _ core.Actor, _, _ string, refs []core.VersionRef, formal bool) (core.EvidenceSelection, error) {
	if s.denied || !formal || len(refs) != 1 || refs[0].ID != "card" || refs[0].Version != 2 {
		return core.EvidenceSelection{}, core.ErrEvidenceIncomplete
	}
	return s.selected, nil
}

func snapshotSelectionFixture() core.EvidenceSelection {
	text := "historical verified passage"
	textRef := core.AcademicOriginalSnapshotRef{SHA256: researchTextHash(text), Size: int64(len(text))}
	identity := core.AcademicEvidenceSnapshot{SourceID: "existing", AssetID: "asset", AssetRevision: 1, Locator: "historical page 2", TextRef: textRef, RawRef: core.AcademicOriginalSnapshotRef{SHA256: researchTextHash("raw original"), Size: 12}}
	return core.EvidenceSelection{
		Cards:   []core.EvidenceCard{{ID: "card", Version: 2, SupportType: "refutes", Scope: "synthetic scope", SourceIDs: []string{"existing"}, AnchorRefs: []core.VersionRef{{ID: "anchor", Version: 1}}, AcademicAssociationRefs: []core.VersionRef{{ID: "association", Version: 1}}, Confirmation: &core.EvidenceConfirmation{AcademicSnapshots: []core.AcademicEvidenceSnapshot{identity}}}},
		Anchors: []core.SourceAnchor{{ID: "anchor", Version: 1, SourceID: "existing", Locator: identity.Locator, SelectionEnd: 1, QuotedTextHash: researchTextHash("h")}},
		Sources: []core.AgentSourceRef{{SourceID: "existing", AssetID: "asset", AssetRevision: 1, Locator: identity.Locator, QuotedText: text, QuotedTextHash: textRef.SHA256, ModelUseAllowed: true}},
	}
}

func TestAcademicSnapshotGenerationRevalidatesSelectionAndLiveAccess(t *testing.T) {
	selected := snapshotSelectionFixture()
	selector := &academicSnapshotSelectorStub{selected: selected}
	live := evidence.Source{ID: "existing", ProjectID: "project", AssetID: "asset", AssetRevision: 1, Status: evidence.SourceAvailable, Locator: "different current locator", QuotedText: "different current text"}
	validator := generationSourceValidator{selector: selector, reviewer: fixedSourceReviewer{{SourceID: "existing", Source: live, Usable: true}}}
	captured := generation.Source{ID: "existing", ProjectID: "project", AssetID: "asset", AssetRevision: 1, Locator: selected.Sources[0].Locator, QuotedText: selected.Sources[0].QuotedText, Status: string(evidence.SourceAvailable)}
	refs := []generation.EvidenceCardRef{{ID: "card", Version: 2}}
	actor := generation.Actor{TenantID: 1, UserID: "owner"}
	if err := validator.ValidateGenerationSourcesWithEvidence(context.Background(), actor, "project", "chapter", refs, []generation.Source{captured}); err != nil {
		t.Fatalf("verified historical snapshot rejected: %v", err)
	}
	tampered := captured
	tampered.QuotedText = live.QuotedText
	if err := validator.ValidateGenerationSourcesWithEvidence(context.Background(), actor, "project", "chapter", refs, []generation.Source{tampered}); !errors.Is(err, generation.ErrSourceStale) {
		t.Fatalf("live text substituted for snapshot: %v", err)
	}
	selector.denied = true
	if err := validator.ValidateGenerationSourcesWithEvidence(context.Background(), actor, "project", "chapter", refs, []generation.Source{captured}); !errors.Is(err, generation.ErrSourceAccessDenied) {
		t.Fatalf("revoked selector was bypassed: %v", err)
	}
	selector.denied = false
	validator.reviewer = fixedSourceReviewer{{SourceID: "existing", AssetDeny: evidence.DenyNotAuthorized}}
	if err := validator.ValidateGenerationSourcesWithEvidence(context.Background(), actor, "project", "chapter", refs, []generation.Source{captured}); !errors.Is(err, generation.ErrSourceAccessDenied) {
		t.Fatalf("revoked live access was bypassed: %v", err)
	}
}

func TestAcademicSnapshotGenerationResolverUsesOnlyHistoricalText(t *testing.T) {
	selector := &academicSnapshotSelectorStub{selected: snapshotSelectionFixture()}
	live := &academicSnapshotLiveSources{}
	resolver := generationInputResolver{selector: selector, sources: live}
	request := generation.Request{ChapterID: "chapter", EvidenceCardRefs: []generation.EvidenceCardRef{{ID: "card", Version: 2}}}
	sources, _, err := resolver.resolveFormalEvidence(context.Background(), generation.Actor{TenantID: 1, UserID: "owner"}, "project", request)
	if err != nil || len(sources) != 1 || sources[0].QuotedText != "historical verified passage" || sources[0].Locator != "historical page 2" || sources[0].Anchor != (evidence.Anchor{}) {
		t.Fatalf("snapshot resolution failed or mixed live anchor: sources=%+v err=%v", sources, err)
	}
	live.denied = true
	if _, _, err = resolver.resolveFormalEvidence(context.Background(), generation.Actor{TenantID: 1, UserID: "owner"}, "project", request); !errors.Is(err, generation.ErrSourceAccessDenied) {
		t.Fatalf("revoked live access was bypassed: %v", err)
	}
}

func TestAcademicSnapshotRewriteUsesSnapshotAndRechecksAfterModel(t *testing.T) {
	for _, revokeDuringModel := range []bool{false, true} {
		t.Run(map[bool]string{false: "snapshot", true: "revoked after model"}[revokeDuringModel], func(t *testing.T) {
			s, _, model, input := formalRewriteFixture(t)
			selector := &academicSnapshotSelectorStub{selected: snapshotSelectionFixture()}
			s.selector = selector
			if revokeDuringModel {
				s.model = &changingRewriteModelStub{base: model, onCall: func() { selector.denied = true }}
			}
			result, err := s.Create(context.Background(), Actor{TenantID: 1, UserID: "owner"}, "project", "chapter", "academic-rewrite", input)
			if err != nil {
				t.Fatal(err)
			}
			if len(model.prompt.Sources) != 1 || model.prompt.Sources[0].QuotedText != "historical verified passage" || model.prompt.Sources[0].Locator != "historical page 2" {
				t.Fatal("model did not receive the verified historical source")
			}
			if revokeDuringModel {
				if result.Validity != candidateadoption.ValidityNotAuthorized || result.ReplacementMarkdown != "" {
					t.Fatalf("revoked async result remained usable: %+v", result)
				}
			} else if result.Validity != candidateadoption.ValidityFresh {
				t.Fatalf("snapshot result was not usable: %+v", result)
			}
		})
	}
}

func TestAcademicSnapshotMarkerCannotBypassSelector(t *testing.T) {
	s, _, _, _ := formalRewriteFixture(t)
	identity := academicSnapshotIdentity{TextRef: core.AcademicOriginalSnapshotRef{SHA256: researchTextHash("forged"), Size: 6}}
	snapshot := RewriteSourceSnapshot{SourceID: "existing", AssetID: "asset", AssetRevision: 1, Locator: "page/1", QuotedTextHash: "hash", AcademicSnapshot: &identity}
	err := s.recheckEvidenceSources(context.Background(), Actor{TenantID: 1, UserID: "owner"}, "project", "chapter", nil, []RewriteSourceSnapshot{snapshot})
	if !errors.Is(err, ErrRewriteSourceDenied) {
		t.Fatalf("untrusted marker bypassed evidence selection: %v", err)
	}
}
