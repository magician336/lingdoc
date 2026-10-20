package template

import (
	"encoding/json"
	"testing"
)

// Empty evaluator evidence is a JSON array, as required by the published
// TemplateRuleEvaluation contract and the actual delivery evidence panel.
func TestEvaluationEmptyEvidenceSerializesAsArray(t *testing.T) {
	for _, input := range [][]string{nil, {}, {"source_id=synthetic"}} {
		got := result(Rule{ID: "synthetic", Severity: "warning"}, "project/p", "v1", "hash", EvaluationPass, "checked", input)
		raw, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]json.RawMessage
		if err := json.Unmarshal(raw, &wire); err != nil {
			t.Fatal(err)
		}
		if string(wire["evidence"]) == "null" {
			t.Fatalf("evidence must be an array, got %s", wire["evidence"])
		}
		var evidence []string
		if err := json.Unmarshal(wire["evidence"], &evidence); err != nil || len(evidence) != len(input) {
			t.Fatalf("evidence changed: %s, error %v", wire["evidence"], err)
		}
		// The wire representation must not mutate caller-owned evidence.
		if len(input) > 0 {
			got.Evidence[0] = "changed"
			if input[0] != "source_id=synthetic" {
				t.Fatal("evaluator retained caller evidence backing storage")
			}
		}
	}
	items, err := Evaluate(DemoTemplate(), EvaluationInput{ProjectID: "p", TargetVersion: "v1", Fields: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	seenEmpty := 0
	for _, item := range items {
		if item.RuleID != "source-available" && item.RuleID != "review-items-decided" {
			continue
		}
		seenEmpty++
		if item.Evidence == nil {
			t.Fatalf("real demo evaluator %s returned null evidence", item.RuleID)
		}
	}
	if seenEmpty != 2 {
		t.Fatalf("expected both real empty-evidence evaluators, got %d", seenEmpty)
	}
}
