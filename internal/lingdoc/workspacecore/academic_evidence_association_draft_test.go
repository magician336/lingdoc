package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func seedDraftAssociation(t *testing.T, f agentFixture, id string, source ResearchSource) AcademicSourceAssociation {
	t.Helper()
	a := AcademicSourceAssociation{ID: id, ProjectID: f.project.ID, Version: 1, SourceID: source.SourceID, AssetID: source.AssetID, AssetRevision: source.AssetRevision, Status: "recorded"}
	if err := f.svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		return storeStoredObject(tx, StoredObject{}, f.project.ID, AcademicSourceAssociationKind, id, 1, a)
	}); err != nil {
		t.Fatal(err)
	}
	return a
}
func TestAcademicEvidenceAssociationDraftPinsSourceAndFailsClosedForFormalUse(t *testing.T) {
	f := newAgentFixture(t, false)
	anchor := f.anchor(t, "source-1", "assoc-anchor")
	claim := f.claim(t)
	assoc := seedDraftAssociation(t, f, "assoc-evidence", f.source.sources["source-1"])
	in := SaveEvidenceCardInput{VersionExpectation: f.expectation(0), ClaimRef: &VersionRef{ID: claim.ID, Version: claim.Version}, AnchorRefs: []VersionRef{{ID: anchor.ID, Version: anchor.Version}}, AcademicAssociationRefs: []VersionRef{{ID: assoc.ID, Version: assoc.Version}}, SupportType: "supports", Scope: "synthetic", ScopeChapterIDs: []string{f.chapter.ID}, Confirmed: true}
	raw, _, _, err := f.svc.SaveEvidenceCard(context.Background(), f.owner, f.project.ID, "assoc-evidence-card", in)
	card := decodeAgentJSON[EvidenceCard](t, raw, err)
	if len(card.AcademicAssociationRefs) != 1 || card.AcademicAssociationRefs[0].ID != assoc.ID || card.AcademicAssociationRefs[0].Version != 1 {
		t.Fatalf("lost association lineage: %+v", card)
	}
	if _, _, _, err = f.svc.ConfirmEvidenceCard(context.Background(), f.owner, f.project.ID, card.ID, "assoc-evidence-confirm", ConfirmationInput{VersionExpectation: f.expectation(card.Version), Confirmed: true, Reason: "human confirms"}); !errors.Is(err, ErrEvidenceIncomplete) {
		t.Fatalf("unproven external original became formal: %v", err)
	}
	// A poisoned/stale persisted confirmed card must also fail the native
	// transaction path used by the generation selection seam.
	card.Status = "CONFIRMED"
	card.Confirmation = &EvidenceConfirmation{CardVersion: card.Version, ClaimRef: *card.ClaimRef, AnchorRefs: card.AnchorRefs, ScopeChapterIDs: card.ScopeChapterIDs}
	payload, err := json.Marshal(card)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.testDB().Exec("UPDATE lingdoc_g2_objects SET payload_json=? WHERE project_id=? AND kind=? AND id=?", string(payload), f.project.ID, EvidenceCardKind, card.ID).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.SelectGenerationEvidence(context.Background(), f.owner, f.project.ID, f.chapter.ID, []VersionRef{{ID: card.ID, Version: card.Version}}, true); !errors.Is(err, ErrEvidenceIncomplete) {
		t.Fatalf("formal selection accepted unproven association: %v", err)
	}
	err = f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		state, e := tx.(interface {
			CurrentEvidenceObject(string, string, string) (ImpactObjectState, error)
		}).CurrentEvidenceObject(f.project.ID, "EvidenceCard", card.ID)
		if e != nil {
			return e
		}
		if state.Confirmed {
			t.Fatal("transaction currentness accepted unproven association")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAcademicEvidenceAssociationDraftRejectsMismatchAndStaleVersion(t *testing.T) {
	f := newAgentFixture(t, false)
	anchor := f.anchor(t, "source-1", "assoc-anchor-reject")
	assoc := seedDraftAssociation(t, f, "assoc-wrong-source", f.source.sources["source-2"])
	in := SaveEvidenceCardInput{VersionExpectation: f.expectation(0), AnchorRefs: []VersionRef{{ID: anchor.ID, Version: anchor.Version}}, AcademicAssociationRefs: []VersionRef{{ID: assoc.ID, Version: 1}}, Confirmed: true}
	if _, _, _, err := f.svc.SaveEvidenceCard(context.Background(), f.owner, f.project.ID, "assoc-bad-source", in); !errors.Is(err, ErrEvidenceIncomplete) {
		t.Fatalf("unrelated association accepted: %v", err)
	}
	in.AcademicAssociationRefs[0].Version++
	if _, _, _, err := f.svc.SaveEvidenceCard(context.Background(), f.owner, f.project.ID, "assoc-bad-version", in); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale association accepted: %v", err)
	}
	in.AcademicAssociationRefs[0].Version = assoc.Version
	in.AcademicAssociationRefs[0].Kind = "OtherKind"
	if _, _, _, err := f.svc.SaveEvidenceCard(context.Background(), f.owner, f.project.ID, "assoc-bad-kind", in); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("wrong association kind accepted: %v", err)
	}
}

func TestAcademicEvidenceAssociationCannotBeSilentlyRemoved(t *testing.T) {
	f := newAgentFixture(t, false)
	anchor := f.anchor(t, "source-1", "assoc-anchor-edit")
	assoc := seedDraftAssociation(t, f, "assoc-edit", f.source.sources["source-1"])
	input := SaveEvidenceCardInput{VersionExpectation: f.expectation(0), AnchorRefs: []VersionRef{{ID: anchor.ID, Version: anchor.Version}}, AcademicAssociationRefs: []VersionRef{{ID: assoc.ID, Version: assoc.Version}}, Confirmed: true}
	raw, _, _, err := f.svc.SaveEvidenceCard(context.Background(), f.owner, f.project.ID, "assoc-card-create", input)
	card := decodeAgentJSON[EvidenceCard](t, raw, err)
	input.CardID = card.ID
	input.ExpectedVersion = card.Version
	input.AcademicAssociationRefs = nil
	if _, _, _, err = f.svc.SaveEvidenceCard(context.Background(), f.owner, f.project.ID, "assoc-card-strip", input); !errors.Is(err, ErrEvidenceIncomplete) {
		t.Fatalf("legacy client erased association: %v", err)
	}
}
