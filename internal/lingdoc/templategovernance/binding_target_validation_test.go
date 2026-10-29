package templategovernance

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
)

func TestValidateChangedBindingTargets(t *testing.T) {
	definition := template.DemoTemplate()
	previous := template.RuleSet{Bindings: []template.RuleBinding{{
		RuleID: "goal-pattern", TargetKind: "field", TargetID: "research_goal",
	}}}
	cases := []struct {
		name, kind, id string
		wantError      bool
	}{
		{"unchanged", "field", "research_goal", false},
		{"existing field", "field", "research_subject", false},
		{"existing chapter", "chapter", "question", false},
		{"kind changed without compatible id", "chapter", "research_goal", true},
		{"id changed without compatible kind", "field", "question", true},
		{"missing field", "field", "missing", true},
		{"missing chapter", "chapter", "missing", true},
		{"dynamic source", "source", "runtime-source", false},
		{"dynamic review item", "review_item", "runtime-review", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			effective := template.RuleSet{Bindings: []template.RuleBinding{{
				RuleID: "goal-pattern", TargetKind: tc.kind, TargetID: tc.id,
			}}}
			if err := ValidateChangedBindingTargets(definition, previous, effective); (err != nil) != tc.wantError {
				t.Fatalf("changed target %s/%s: err=%v, want error=%t", tc.kind, tc.id, err, tc.wantError)
			}
		})
	}
}
