package workspacecore

import (
	"context"
	"encoding/json"
	"testing"
)

// Losing source visibility must never make a persisted rejection look like a
// reviewable candidate, nor expose a human's potentially sensitive reason.
func TestEvidenceCardListPreservesExplicitDispositionWhenSourceUnavailable(t *testing.T) {
	f := newAgentFixture(t, false)
	ctx := context.Background()
	anchor := f.anchor(t, "source-1", "disposition-anchor")
	claim := f.claim(t)
	statuses := []string{"INSUFFICIENT", "SUPERSEDED"}
	cards := make(map[string]EvidenceCard, len(statuses))
	for _, status := range statuses {
		card := f.card(t, []SourceAnchor{anchor}, &claim, "disposition-card-"+status, "supports")
		raw, _, _, err := f.svc.ReviewEvidenceCard(ctx, f.owner, f.project.ID, card.ID, "disposition-review-"+status, ReviewEvidenceCardInput{
			VersionExpectation: f.expectation(card.Version), Status: status, Confirmed: true, Reason: "sensitive source details",
		})
		cards[status] = decodeAgentJSON[EvidenceCard](t, raw, err)
	}
	// Older persisted cards may already contain STALE; do not rewrite their
	// meaning merely because their source cannot be checked today.
	legacy := f.card(t, []SourceAnchor{anchor}, &claim, "disposition-legacy", "supports")
	if err := f.svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		value, old, err := loadStoredObject[EvidenceCard](tx, f.project.ID, EvidenceCardKind, legacy.ID)
		if err != nil {
			return err
		}
		value.Version++
		value.Status = "STALE"
		value.InvalidReason = "sensitive source details"
		legacy = value
		return storeStoredObject(tx, old, f.project.ID, EvidenceCardKind, value.ID, value.Version, value)
	}); err != nil {
		t.Fatal(err)
	}
	statuses = append(statuses, "STALE")
	cards["STALE"] = legacy
	f.source.denied[anchor.SourceID] = true
	f.policy.verdict = ErrSourceUnavailable
	listed, err := f.svc.ListEvidenceCards(ctx, f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, got := range listed {
		for status, card := range cards {
			if got.ID != card.ID {
				continue
			}
			seen[status] = true
			if got.Status != status || got.Version != card.Version || got.Confirmation != nil {
				t.Errorf("persisted %s disposition was changed by read: %+v", status, got)
			}
			if got.InvalidReason != "来源、主张、定位或权限需要复核" || got.Scope != "" || got.Limitations != "" || got.Gaps != "" || got.AlternativeExplanation != "" {
				t.Errorf("unavailable source leaked details: %+v", got)
			}
			_, err := f.svc.SelectGenerationEvidence(ctx, f.owner, f.project.ID, f.chapter.ID, []VersionRef{{ID: got.ID, Version: got.Version}}, true)
			if err == nil {
				t.Errorf("%s entered formal generation", status)
			}
		}
	}
	if len(seen) != len(statuses) {
		t.Fatalf("missing cards: %v", seen)
	}

	// The read-time projection must not mutate the saved version or history.
	if err := f.svc.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		port, _ := agentObjectStore(tx)
		for status, card := range cards {
			row, err := port.ObjectVersion(f.project.ID, EvidenceCardKind, card.ID, card.Version)
			if err != nil {
				return err
			}
			var stored EvidenceCard
			if err := json.Unmarshal(row.Payload, &stored); err != nil {
				return err
			}
			if stored.Status != status || stored.InvalidReason != "sensitive source details" {
				t.Errorf("history was changed by redacted read: %+v", stored)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	delete(f.source.denied, anchor.SourceID)
	f.policy.verdict = nil
	restored, err := f.svc.ListEvidenceCards(ctx, f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range restored {
		for status, card := range cards {
			if got.ID == card.ID && (got.Status != status || got.Version != card.Version || got.Confirmation != nil) {
				t.Errorf("restoring access silently changed disposition: %+v", got)
			}
		}
	}
}
