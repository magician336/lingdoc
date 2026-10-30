package workspacecore

import (
	"context"
	"encoding/json"
	"testing"
)

func confirmedAgentContextFixture(t *testing.T) (agentFixture, EvidenceCard, AgentContextInput) {
	t.Helper()
	f := newAgentFixture(t, false)
	anchor := f.anchor(t, "source-1", "context-proof-anchor")
	claim := f.claim(t)
	card := f.card(t, []SourceAnchor{anchor}, &claim, "context-proof-card", "supports")
	raw, _, _, err := f.svc.ConfirmEvidenceCard(context.Background(), f.owner, f.project.ID, card.ID, "context-proof-confirm", ConfirmationInput{VersionExpectation: f.expectation(card.Version), Confirmed: true, Reason: "simulated human review"})
	card = decodeAgentJSON[EvidenceCard](t, raw, err)
	input := AgentContextInput{VersionExpectation: f.expectation(0), ChapterID: f.chapter.ID, CardRefs: []VersionRef{{ID: card.ID, Version: card.Version}}, SensitiveContextConfirmed: true}
	return f, card, input
}

func TestAgentContextRejectsChangedEvidenceWithoutLeakingSources(t *testing.T) {
	for name, mutate := range map[string]func(agentFixture, EvidenceCard){
		"changed revision": func(f agentFixture, _ EvidenceCard) {
			s := f.source.sources["source-1"]
			s.AssetRevision++
			f.source.sources["source-1"] = s
		},
		"changed quote": func(f agentFixture, _ EvidenceCard) {
			s := f.source.sources["source-1"]
			s.QuotedText += " changed"
			s.QuotedTextHash = sha256Hex(s.QuotedText)
			f.source.sources["source-1"] = s
		},
		"original no longer proven": func(f agentFixture, _ EvidenceCard) {
			s := f.source.sources["source-1"]
			s.StableOriginal = false
			f.source.sources["source-1"] = s
		},
		"permission revoked": func(f agentFixture, _ EvidenceCard) { f.source.denied["source-1"] = true },
		"confirmation tuple changed": func(f agentFixture, card EvidenceCard) {
			card.Confirmation.CardVersion++
			payload, err := json.Marshal(card)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.svc.testDB().Exec("UPDATE lingdoc_g2_objects SET payload_json=? WHERE project_id=? AND kind=? AND id=?", string(payload), f.project.ID, EvidenceCardKind, card.ID).Error; err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f, card, input := confirmedAgentContextFixture(t)
			before, err := f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, input)
			if err != nil || len(before.SourceRefs) != 1 {
				t.Fatalf("valid confirmed card: %+v %v", before, err)
			}
			mutate(f, card)
			got, err := f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, input)
			if err == nil {
				t.Fatalf("changed evidence accepted: %+v", got)
			}
			if len(got.SourceRefs) != 0 || len(got.CardRefs) != 0 {
				t.Fatalf("failure leaked partial context: %+v", got)
			}
			if _, err = f.svc.SelectGenerationEvidence(context.Background(), f.owner, f.project.ID, f.chapter.ID, []VersionRef{{ID: card.ID, Version: card.Version}}, true); err == nil {
				t.Fatal("changed evidence entered formal generation")
			}
		})
	}
}

func TestAgentContextLegacyAnchorRequiresProofOfWholeSource(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "full selection remains compatible", true: "partial selection needs new anchor"}[partial], func(t *testing.T) {
			f, card, input := confirmedAgentContextFixture(t)
			var payload string
			if err := f.svc.testDB().Raw("SELECT payload_json FROM lingdoc_g2_objects WHERE project_id=? AND kind=? AND id=?", f.project.ID, SourceAnchorKind, card.AnchorRefs[0].ID).Scan(&payload).Error; err != nil {
				t.Fatal(err)
			}
			var anchor SourceAnchor
			if err := json.Unmarshal([]byte(payload), &anchor); err != nil {
				t.Fatal(err)
			}
			anchor.SourceTextHash = "" // Simulate a pre-upgrade stored anchor.
			if partial {
				anchor.SelectionEnd = 3
				anchor.QuotedTextHash = sha256Hex(string([]rune(f.source.sources[anchor.SourceID].QuotedText)[:3]))
			}
			raw, err := json.Marshal(anchor)
			if err != nil {
				t.Fatal(err)
			}
			if err = f.svc.testDB().Exec("UPDATE lingdoc_g2_objects SET payload_json=? WHERE project_id=? AND kind=? AND id=?", string(raw), f.project.ID, SourceAnchorKind, anchor.ID).Error; err != nil {
				t.Fatal(err)
			}
			got, err := f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, input)
			if !partial && (err != nil || len(got.SourceRefs) != 1) {
				t.Fatalf("whole-source legacy anchor rejected: %+v %v", got, err)
			}
			if partial && (err == nil || len(got.SourceRefs) != 0 || len(got.CardRefs) != 0) {
				t.Fatalf("unproven legacy snapshot accepted or leaked: %+v %v", got, err)
			}
			_, formalErr := f.svc.SelectGenerationEvidence(context.Background(), f.owner, f.project.ID, f.chapter.ID, []VersionRef{{ID: card.ID, Version: card.Version}}, true)
			if (formalErr != nil) != partial {
				t.Fatalf("legacy formal selection partial=%v err=%v", partial, formalErr)
			}
		})
	}
}

func TestLegacyPartialAnchorRemainsDraftButCannotBeConfirmed(t *testing.T) {
	f := newAgentFixture(t, false)
	source := f.source.sources["source-1"]
	raw, _, _, err := f.svc.SaveSourceAnchor(context.Background(), f.owner, f.project.ID, "legacy-partial-anchor", SaveSourceAnchorInput{VersionExpectation: f.expectation(0), SourceID: source.SourceID, SelectionEnd: 3, ExtractionType: "native_text", Confirmed: true})
	anchor := decodeAgentJSON[SourceAnchor](t, raw, err)
	anchor.SourceTextHash = ""
	payload, err := json.Marshal(anchor)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.testDB().Exec("UPDATE lingdoc_g2_objects SET payload_json=? WHERE project_id=? AND kind=? AND id=?", string(payload), f.project.ID, SourceAnchorKind, anchor.ID).Error; err != nil {
		t.Fatal(err)
	}
	claim := f.claim(t)
	card := f.card(t, []SourceAnchor{anchor}, &claim, "legacy-partial-card", "supports")
	if _, _, _, err = f.svc.ConfirmEvidenceCard(context.Background(), f.owner, f.project.ID, card.ID, "legacy-partial-confirm", ConfirmationInput{VersionExpectation: f.expectation(card.Version), Confirmed: true, Reason: "simulated review"}); err == nil {
		t.Fatal("legacy partial anchor was confirmed without full snapshot proof")
	}
	// Recreating the same partial anchor must not deduplicate to an unproven
	// legacy record; a reviewer can then review a fresh snapshot.
	raw, _, _, err = f.svc.SaveSourceAnchor(context.Background(), f.owner, f.project.ID, "legacy-partial-reanchor", SaveSourceAnchorInput{VersionExpectation: f.expectation(0), SourceID: source.SourceID, SelectionEnd: 3, ExtractionType: "native_text", Confirmed: true})
	fresh := decodeAgentJSON[SourceAnchor](t, raw, err)
	if fresh.ID == anchor.ID || fresh.SourceTextHash != sha256Hex(source.QuotedText) {
		t.Fatalf("legacy anchor was reused without proof: %+v", fresh)
	}
	freshCard := f.card(t, []SourceAnchor{fresh}, &claim, "legacy-reanchored-card", "supports")
	raw, _, _, err = f.svc.ConfirmEvidenceCard(context.Background(), f.owner, f.project.ID, freshCard.ID, "legacy-reanchored-confirm", ConfirmationInput{VersionExpectation: f.expectation(freshCard.Version), Confirmed: true, Reason: "review new source snapshot"})
	freshCard = decodeAgentJSON[EvidenceCard](t, raw, err)
	if _, err = f.svc.SelectGenerationEvidence(context.Background(), f.owner, f.project.ID, f.chapter.ID, []VersionRef{{ID: freshCard.ID, Version: freshCard.Version}}, true); err != nil {
		t.Fatalf("reanchored evidence was not accepted: %v", err)
	}
}

func TestFormalTransactionRejectsChangedUnselectedChunkText(t *testing.T) {
	f := newAgentFixture(t, false)
	seedNativeEvidence(t, f)
	source := f.source.sources["source-1"]
	raw, _, _, err := f.svc.SaveSourceAnchor(context.Background(), f.owner, f.project.ID, "transaction-partial-anchor", SaveSourceAnchorInput{VersionExpectation: f.expectation(0), SourceID: source.SourceID, SelectionEnd: 3, ExtractionType: "native_text", Confirmed: true})
	anchor := decodeAgentJSON[SourceAnchor](t, raw, err)
	claim := f.claim(t)
	card := f.card(t, []SourceAnchor{anchor}, &claim, "transaction-partial-card", "supports")
	raw, _, _, err = f.svc.ConfirmEvidenceCard(context.Background(), f.owner, f.project.ID, card.ID, "transaction-partial-confirm", ConfirmationInput{VersionExpectation: f.expectation(card.Version), Confirmed: true, Reason: "synthetic review"})
	card = decodeAgentJSON[EvidenceCard](t, raw, err)
	read := func() bool {
		t.Helper()
		var state ImpactObjectState
		err := f.svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
			var e error
			state, e = tx.(gormTransaction).CurrentEvidenceObject(f.project.ID, "EvidenceCard", card.ID)
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
		return state.Confirmed
	}
	if !read() {
		t.Fatal("unchanged source must remain confirmed")
	}
	if err = f.svc.testDB().Exec("UPDATE chunks SET content=? WHERE id=?", source.QuotedText+" unselected change", source.SourceID).Error; err != nil {
		t.Fatal(err)
	}
	if read() {
		t.Fatal("transaction accepted changed unselected chunk text")
	}
}

type changingContextSource struct {
	*testSourceReader
	reads int
	deny  bool
}

func (r *changingContextSource) ReadResearchSource(ctx context.Context, actor Actor, projectID, id string) (ResearchSource, error) {
	r.reads++
	if r.reads == 2 {
		if r.deny {
			r.denied[id] = true
		} else {
			source := r.sources[id]
			source.QuotedText += " changed during validation"
			source.QuotedTextHash = sha256Hex(source.QuotedText)
			r.sources[id] = source
		}
	}
	return r.testSourceReader.ReadResearchSource(ctx, actor, projectID, id)
}

func (r *changingContextSource) ReadResearchSourceForChapter(ctx context.Context, actor Actor, projectID, _, id string) (ResearchSource, error) {
	return r.ReadResearchSource(ctx, actor, projectID, id)
}

func TestAgentContextRejectsSourceChangedDuringAssembly(t *testing.T) {
	for _, deny := range []bool{false, true} {
		t.Run(map[bool]string{false: "changed", true: "revoked"}[deny], func(t *testing.T) {
			f, _, input := confirmedAgentContextFixture(t)
			f.svc.agentRuntime.Sources = &changingContextSource{testSourceReader: f.source, deny: deny}
			got, err := f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, input)
			if err == nil || len(got.SourceRefs) != 0 || len(got.CardRefs) != 0 {
				t.Fatalf("racing source was accepted or leaked: %+v %v", got, err)
			}
		})
	}
}
