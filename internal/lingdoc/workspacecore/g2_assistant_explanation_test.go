package workspacecore

import (
	"encoding/json"
	"strings"
	"testing"
)

// AC-G3-14: the assistant explains what a proposed change affects in two
// groups. Each entry carries an object reference, a reason and a basis. An
// entry that cannot cite a basis must never be presented as an affected
// object, in either group.

func TestAssistantAffectedExplanationKeepsTwoGroupsApart(t *testing.T) {
	raw := json.RawMessage(`{
		"definite_affected": [
			{
				"object": {"kind": "ChapterVersion", "id": "chapter-1", "version": "v3"},
				"reason": "该章正文引用了被修改的研究条件",
				"basis": {"source_type": "dependency_edge", "source_id": "edge-1", "detail": "uses"}
			}
		],
		"possible_affected": [
			{
				"object": {"kind": "EvidenceCard", "id": "card-1", "version": "2"},
				"reason": "证据卡适用范围可能覆盖新条件",
				"basis": {"source_type": "ruleset", "source_id": "ruleset-1"}
			}
		]
	}`)
	explanation, err := DecodeAssistantAffectedExplanation(raw)
	if err != nil {
		t.Fatalf("decode two groups: %v", err)
	}
	if len(explanation.DefiniteAffected) != 1 || len(explanation.PossibleAffected) != 1 {
		t.Fatalf("groups not kept apart: %+v", explanation)
	}
	definite := explanation.DefiniteAffected[0]
	if definite.Object.Kind != "ChapterVersion" || definite.Object.ID != "chapter-1" || definite.Object.Version != "v3" {
		t.Fatalf("object reference lost: %+v", definite.Object)
	}
	if definite.Reason == "" || definite.Basis.SourceType != "dependency_edge" || definite.Basis.SourceID != "edge-1" {
		t.Fatalf("reason or basis lost: %+v", definite)
	}
	possible := explanation.PossibleAffected[0]
	if possible.Object.ID != "card-1" || possible.Basis.SourceType != "ruleset" {
		t.Fatalf("possible group entry lost: %+v", possible)
	}
	// The assistant side never answers with the resolver's field name.
	encoded, err := json.Marshal(explanation)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "certainty") {
		t.Fatalf("assistant explanation impersonates the resolver: %s", encoded)
	}
	if !strings.Contains(string(encoded), "definite_affected") || !strings.Contains(string(encoded), "possible_affected") {
		t.Fatalf("group names missing: %s", encoded)
	}
}

func TestAssistantAffectedExplanationRejectsEntriesWithoutBasis(t *testing.T) {
	cases := map[string]string{
		"missing object kind":    `{"definite_affected":[{"object":{"id":"c1","version":"v1"},"reason":"r","basis":{"source_type":"dependency_edge","source_id":"e1"}}]}`,
		"missing object id":      `{"definite_affected":[{"object":{"kind":"ChapterVersion","version":"v1"},"reason":"r","basis":{"source_type":"dependency_edge","source_id":"e1"}}]}`,
		"missing object version": `{"definite_affected":[{"object":{"kind":"ChapterVersion","id":"c1"},"reason":"r","basis":{"source_type":"dependency_edge","source_id":"e1"}}]}`,
		"missing reason":         `{"definite_affected":[{"object":{"kind":"ChapterVersion","id":"c1","version":"v1"},"basis":{"source_type":"dependency_edge","source_id":"e1"}}]}`,
		"missing basis":          `{"definite_affected":[{"object":{"kind":"ChapterVersion","id":"c1","version":"v1"},"reason":"r"}]}`,
		"missing basis id":       `{"definite_affected":[{"object":{"kind":"ChapterVersion","id":"c1","version":"v1"},"reason":"r","basis":{"source_type":"dependency_edge"}}]}`,
		"missing basis type":     `{"definite_affected":[{"object":{"kind":"ChapterVersion","id":"c1","version":"v1"},"reason":"r","basis":{"source_id":"e1"}}]}`,
		"unknown basis type":     `{"definite_affected":[{"object":{"kind":"ChapterVersion","id":"c1","version":"v1"},"reason":"r","basis":{"source_type":"vibes","source_id":"e1"}}]}`,
		"unknown field":          `{"definite_affected":[{"object":{"kind":"ChapterVersion","id":"c1","version":"v1"},"reason":"r","basis":{"source_type":"dependency_edge","source_id":"e1"},"certainty":"definite"}]}`,
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeAssistantAffectedExplanation(json.RawMessage(payload)); err == nil {
				t.Fatalf("invalid explanation accepted: %s", payload)
			}
		})
	}
}

func TestAssistantAffectedExplanationRejectsUnusablePayloads(t *testing.T) {
	for _, payload := range []string{"", "null", "[]", `{"definite_affected":{}}`, `{"definite_affected":[],"possible_affected":[]} trailing`, `{"definite_affected":[]}`, `{"possible_affected":[]}`} {
		if _, err := DecodeAssistantAffectedExplanation(json.RawMessage(payload)); err == nil {
			t.Fatalf("unusable explanation accepted: %s", payload)
		}
	}
}

// A turn that carries no explanation yields no groups at all: the server never
// invents an affected object the assistant did not propose.
func TestAssistantAffectedExplanationFromTurnWithoutExplanationIsEmpty(t *testing.T) {
	turn := AgentTurn{ID: "turn-1", ProjectID: "project-1", Agent: "task_assistant",
		Suggestions: json.RawMessage(`{"reply":"只有自然语言回复"}`)}
	explanation, err := AssistantAffectedFromTurn(turn)
	if err != nil {
		t.Fatalf("read turn without explanation: %v", err)
	}
	if explanation != nil {
		t.Fatalf("server invented an explanation: %+v", explanation)
	}
}

// The explanation is re-derived from the persisted turn, so a client that
// tampers with the stored suggestion cannot smuggle an unsupported object in.
func TestAssistantAffectedExplanationFromTurnReadsPersistedSuggestion(t *testing.T) {
	turn := AgentTurn{ID: "turn-1", ProjectID: "project-1", Agent: "task_assistant",
		Suggestions: json.RawMessage(`{"reply":"候选","affected_explanation":{"definite_affected":[{"object":{"kind":"ChapterVersion","id":"chapter-1","version":"v3"},"reason":"引用了被改条件","basis":{"source_type":"dependency_edge","source_id":"edge-1"}}],"possible_affected":[]}}`)}
	explanation, err := AssistantAffectedFromTurn(turn)
	if err != nil {
		t.Fatalf("read persisted explanation: %v", err)
	}
	if explanation == nil || len(explanation.DefiniteAffected) != 1 || len(explanation.PossibleAffected) != 0 {
		t.Fatalf("persisted explanation not re-derived: %+v", explanation)
	}
	if explanation.DefiniteAffected[0].Object.ID != "chapter-1" {
		t.Fatalf("wrong object: %+v", explanation.DefiniteAffected[0])
	}
}

// A stored turn whose explanation cannot cite a basis is refused, so the
// unsupported object reaches neither group.
func TestAssistantAffectedExplanationFromTurnRejectsUnbasedEntry(t *testing.T) {
	turn := AgentTurn{ID: "turn-1", ProjectID: "project-1", Agent: "task_assistant",
		Suggestions: json.RawMessage(`{"reply":"候选","affected_explanation":{"definite_affected":[{"object":{"kind":"ChapterVersion","id":"chapter-1","version":"v3"},"reason":"引用了被改条件"}]}}`)}
	if _, err := AssistantAffectedFromTurn(turn); err == nil {
		t.Fatal("unbased explanation accepted from a stored turn")
	}
}

// Issue #315: the assistant's answer about what a change affects has three
// states, and "the assistant gave no two groups" is one of them - a fact that
// is recorded, not a reason to drop the draft.
func TestAssistantAffectedStateForTurnTellsPresentFromMissing(t *testing.T) {
	withGroups := AgentTurn{ID: "turn-1", ProjectID: "project-1", Agent: "task_assistant",
		Suggestions: json.RawMessage(`{"reply":"候选","change_draft":{"expected_context_revision":1,"reason":"调整条件","fields":{"research_goal":"新条件"}},"affected_explanation":{"definite_affected":[{"object":{"kind":"ChapterVersion","id":"chapter-1","version":"v3"},"reason":"引用了被改条件","basis":{"source_type":"dependency_edge","source_id":"edge-1"}}],"possible_affected":[]}}`)}
	state, err := AssistantAffectedStateForTurn(withGroups)
	if err != nil {
		t.Fatalf("state of a turn with groups: %v", err)
	}
	if state != AssistantAffectedStatePresent {
		t.Fatalf("turn with groups reported as %s", state)
	}

	// The assistant proposed a change but gave no two groups.
	withoutGroups := AgentTurn{ID: "turn-2", ProjectID: "project-1", Agent: "task_assistant",
		Suggestions: json.RawMessage(`{"reply":"候选","change_draft":{"expected_context_revision":1,"reason":"调整条件","fields":{"research_goal":"新条件"}}}`)}
	state, err = AssistantAffectedStateForTurn(withoutGroups)
	if err != nil {
		t.Fatalf("state of a turn without groups: %v", err)
	}
	if state != AssistantAffectedStateMissing {
		t.Fatalf("turn without groups reported as %s", state)
	}

	// No draft at all: no group was due.
	plain := AgentTurn{ID: "turn-3", ProjectID: "project-1", Agent: "task_assistant",
		Suggestions: json.RawMessage(`{"reply":"只有自然语言回复"}`)}
	state, err = AssistantAffectedStateForTurn(plain)
	if err != nil {
		t.Fatalf("state of a plain turn: %v", err)
	}
	if state != AssistantAffectedStateNotApplicable {
		t.Fatalf("plain turn reported as %s", state)
	}
}

// A turn whose explanation cannot cite a basis is refused, not reported as
// missing: missing is about the assistant having said nothing.
func TestAssistantAffectedStateForTurnRefusesUnbasedEntry(t *testing.T) {
	turn := AgentTurn{ID: "turn-1", ProjectID: "project-1", Agent: "task_assistant",
		Suggestions: json.RawMessage(`{"reply":"候选","change_draft":{"expected_context_revision":1,"reason":"调整条件","fields":{"research_goal":"新条件"}},"affected_explanation":{"definite_affected":[{"object":{"kind":"ChapterVersion","id":"chapter-1","version":"v3"},"reason":"引用了被改条件"}],"possible_affected":[]}}`)}
	if _, err := AssistantAffectedStateForTurn(turn); err == nil {
		t.Fatal("unbased explanation accepted as a state")
	}
}

// "Absent" means the assistant said nothing about the two groups: the key is
// missing or null. A stated but invalid explanation is not absent.
func TestAssistantAffectedExplanationAbsentOnlyCoversSilence(t *testing.T) {
	if !AssistantAffectedExplanationAbsent(nil) {
		t.Fatal("a missing key is not absent")
	}
	if !AssistantAffectedExplanationAbsent(json.RawMessage("null")) {
		t.Fatal("a null value is not absent")
	}
	if AssistantAffectedExplanationAbsent(json.RawMessage(`{"definite_affected":[],"possible_affected":[]}`)) {
		t.Fatal("a stated explanation is absent")
	}
	if AssistantAffectedExplanationAbsent(json.RawMessage(`{"definite_affected":[{"object":{"kind":"ChapterVersion","id":"c1","version":"v1"},"reason":"r"}]}`)) {
		t.Fatal("an invalid explanation is absent")
	}
}
