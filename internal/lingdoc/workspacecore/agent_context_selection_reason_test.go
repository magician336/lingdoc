package workspacecore

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// TestBuildAgentContextSelectionReasonsCarryStableEntryKeys pins the contract
// #432 asked for: every reason names the entry it is about, by kind and id,
// plus the rule that pulled it in. The prose is for the reader and may change;
// these three fields may not.
func TestBuildAgentContextSelectionReasonsCarryStableEntryKeys(t *testing.T) {
	f := newAgentFixture(t, false)
	task := f.task(t)
	anchor := f.anchor(t, "source-1", "g2-reason-anchor")
	claim := f.claim(t)
	card := f.card(t, []SourceAnchor{anchor}, &claim, "g2-reason-card", "supports")
	// Selection reasons describe usable context entries. The fixture must pass
	// the same explicit confirmation gate as a real evidence card (#682).
	raw, _, _, err := f.svc.ConfirmEvidenceCard(context.Background(), f.owner, f.project.ID, card.ID, "g2-reason-confirm", ConfirmationInput{
		VersionExpectation: f.expectation(card.Version), Confirmed: true, Reason: "synthetic fixture: checked quote and scope",
	})
	card = decodeAgentJSON[EvidenceCard](t, raw, err)

	current, err := f.svc.GetProject(context.Background(), f.owner, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	pack, err := f.svc.BuildAgentContext(context.Background(), f.owner, f.project.ID, AgentContextInput{
		VersionExpectation:        VersionExpectation{ExpectedProjectVersion: current.ProjectVersion, ExpectedContextRevision: current.CurrentContextRevision, ExpectedVersion: 0},
		TaskID:                    task.ID,
		SourceIDs:                 []string{"source-2"},
		CardRefs:                  []VersionRef{{ID: card.ID, Version: card.Version}},
		SensitiveContextConfirmed: true,
	})
	if err != nil {
		t.Fatalf("build context: %v", err)
	}

	// The source the card depends on is not the source the user picked: the
	// code says which rule brought each one in, while both keep one entry key.
	wanted := []SelectionReason{
		{Kind: SelectionReasonKindTask, ID: task.ID, Code: SelectionReasonUserSelectedTask, Text: "用户选择的当前调研任务"},
		{Kind: SelectionReasonKindCard, ID: card.ID, Code: SelectionReasonUserSelectedCard, Text: "用户选择证据卡；层级=" + card.Status},
		{Kind: SelectionReasonKindSource, ID: "source-1", Code: SelectionReasonCardDependency, Text: "用户选择或卡片明确依赖的当前来源：" + f.source.sources["source-1"].Locator},
		{Kind: SelectionReasonKindSource, ID: "source-2", Code: SelectionReasonUserSelectedSource, Text: "用户选择或卡片明确依赖的当前来源：" + f.source.sources["source-2"].Locator},
	}
	if !slices.Equal(pack.SelectionReasons, wanted) {
		t.Fatalf("selection reasons lost their entry keys:\n got: %+v\nwant: %+v", pack.SelectionReasons, wanted)
	}

	// Every reason answers an entry that is actually in the package.
	for _, reason := range pack.SelectionReasons {
		switch reason.Kind {
		case SelectionReasonKindTask:
			if reason.ID != pack.TaskID || pack.TaskID == "" {
				t.Fatalf("task reason does not name the package's task: %+v", reason)
			}
		case SelectionReasonKindCard:
			if !slices.ContainsFunc(pack.CardRefs, func(ref VersionRef) bool { return ref.ID == reason.ID }) {
				t.Fatalf("card reason does not name a card in the package: %+v", reason)
			}
		case SelectionReasonKindSource:
			if !slices.ContainsFunc(pack.SourceRefs, func(ref AgentSourceRef) bool { return ref.SourceID == reason.ID }) {
				t.Fatalf("source reason does not name a source in the package: %+v", reason)
			}
		default:
			t.Fatalf("unknown reason kind: %+v", reason)
		}
	}
}

// legacyTurnContext builds the JSON a turn carried before #432: reasons were
// free text, so there is no id to attach them to. It is written as a raw map on
// purpose -- the point is to read bytes that the current struct cannot have
// produced.
func legacyTurnContext(projectID, reason string) map[string]any {
	return map[string]any{
		"project_id":          projectID,
		"project_version":     1,
		"spec_revision":       0,
		"context_revision":    1,
		"template_id":         "template-demo",
		"template_version":    "1",
		"source_refs":         []any{},
		"card_refs":           []any{},
		"selection_reasons":   []string{reason},
		"authorization_state": "allowed",
		"output_constraints":  []any{},
		"correlation_id":      "legacy-correlation",
	}
}

func seedLegacyTurn(t *testing.T, s *Service, a Actor, projectID, sessionID, turnID, reason string) {
	t.Helper()
	err := s.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		store := tx.(ObjectStore)
		session := AgentSession{ID: sessionID, ProjectID: projectID, Version: 1, Agent: "research", CreatedBy: a.UserID, CreatedAt: time.Now().UTC()}
		turn := map[string]any{
			"id":           turnID,
			"project_id":   projectID,
			"session_id":   sessionID,
			"version":      1,
			"agent":        "research",
			"user_input":   "旧轮次的问题",
			"reply":        "旧轮次的回复",
			"context":      legacyTurnContext(projectID, reason),
			"status":       "completed",
			"run_mode":     "mock",
			"input_tokens": 0, "output_tokens": 0,
			"created_by": a.UserID,
			"created_at": time.Now().UTC().Format(time.RFC3339Nano),
		}
		for _, entry := range []struct {
			kind, id string
			value    any
		}{{AgentSessionKind, sessionID, session}, {AgentTurnKind, turnID, turn}} {
			raw, err := json.Marshal(entry.value)
			if err != nil {
				return err
			}
			if err = store.InsertObject(StoredObject{ProjectID: projectID, Kind: entry.kind, ID: entry.id, Version: 1, Payload: raw}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAgentContextPackageUnmarshalKeepsLegacyStringReasons(t *testing.T) {
	const text = "用户选择证据卡；层级=CONFIRMED"
	raw, err := json.Marshal(legacyTurnContext("project-legacy", text))
	if err != nil {
		t.Fatal(err)
	}
	var pack AgentContextPackage
	if err := json.Unmarshal(raw, &pack); err != nil {
		t.Fatalf("a turn stored before #432 no longer decodes: %v", err)
	}
	if pack.ProjectID != "project-legacy" || pack.ContextRevision != 1 {
		t.Fatalf("other fields changed while decoding legacy reasons: %+v", pack)
	}
	if len(pack.SelectionReasons) != 1 {
		t.Fatalf("legacy reasons lost: %+v", pack.SelectionReasons)
	}
	reason := pack.SelectionReasons[0]
	if reason.Kind != SelectionReasonKindLegacy || reason.ID != "" || reason.Code != SelectionReasonLegacyUnstructured || reason.Text != text {
		t.Fatalf("legacy reason was not kept as read-only text: %+v", reason)
	}
}

func TestAgentContextPackageUnmarshalKeepsStructuredReasons(t *testing.T) {
	wanted := SelectionReason{Kind: SelectionReasonKindCard, ID: "card-1", Code: SelectionReasonUserSelectedCard, Text: "用户选择证据卡；层级=CONFIRMED"}
	raw, err := json.Marshal(AgentContextPackage{ProjectID: "project-new", SelectionReasons: []SelectionReason{wanted}})
	if err != nil {
		t.Fatal(err)
	}
	var pack AgentContextPackage
	if err := json.Unmarshal(raw, &pack); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(pack.SelectionReasons, []SelectionReason{wanted}) {
		t.Fatalf("structured reasons changed on decode: %+v", pack.SelectionReasons)
	}
	// Encoding still writes the structured shape; the legacy form is read-only.
	encoded, err := json.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(encoded) {
		t.Fatalf("unexpected encoding: %s", encoded)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	var reasons []SelectionReason
	if err := json.Unmarshal(fields["selection_reasons"], &reasons); err != nil {
		t.Fatalf("serialization is not the structured shape: %s", fields["selection_reasons"])
	}
	if !slices.Equal(reasons, []SelectionReason{wanted}) {
		t.Fatalf("serialization changed the reason: %+v", reasons)
	}
}

// The real seam: a legacy payload is already in the object store, and the
// service is asked for the session's turns. This is what used to fail with
// "cannot unmarshal string into ... SelectionReason".
func TestGetAgentTurnsReadsLegacyStringSelectionReasons(t *testing.T) {
	s := testStore(t, filepath.Join(t.TempDir(), "legacy-selection-reason.db"))
	actor := Actor{TenantID: 9331, UserID: "owner"}
	seedTenantMember(t, s, actor)
	raw, _, _, err := s.CreateProject(context.Background(), actor, "legacy-reason-project", CreateProjectInput{Name: "旧轮次合成项目", TemplateID: "template-demo"})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, raw)
	const text = "用户选择证据卡；层级=CONFIRMED"
	seedLegacyTurn(t, s, actor, project.ID, "legacy-session", "legacy-turn", text)

	turns, err := s.GetAgentTurns(context.Background(), actor, project.ID, "legacy-session")
	if err != nil {
		t.Fatalf("reading a turn stored before #432 failed: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("legacy turn lost: %+v", turns)
	}
	reasons := turns[0].Context.SelectionReasons
	if len(reasons) != 1 || reasons[0].Kind != SelectionReasonKindLegacy || reasons[0].ID != "" || reasons[0].Code != SelectionReasonLegacyUnstructured || reasons[0].Text != text {
		t.Fatalf("legacy reason did not survive the read path: %+v", reasons)
	}
}
