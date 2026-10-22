package workspacecore

import (
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
)

func TestInitializationUsesEffectiveCanonicalRulesWithSourceSidecar(t *testing.T) {
	set, err := template.PublishRuleSet(template.RuleSet{ID: "initialization-rules", Version: "1", State: template.RuleSetDraft,
		Bindings: []template.RuleBinding{{RuleID: "goal-pattern", DefinitionID: "pattern", DefinitionVersion: "1", Origin: "project", TargetKind: "field", TargetID: "research_goal", Severity: "blocking", TriggerScenes: []string{"write_check", "preflight"}, Enabled: true, Owner: "synthetic", Parameters: map[string]any{"pattern": "^unoverridden$"}}}})
	if err != nil {
		t.Fatal(err)
	}
	definition := template.DemoTemplate()
	definition.RuleSetSnapshot = &set
	definition.RuleSetRef = &template.RuleSetReference{ID: set.ID, Version: set.Version, Hash: set.RulesetHash}
	check := func(overrides []template.RuleBindingOverride) error {
		bound := template.Clone(definition)
		bound.RuleBindingOverrides = overrides
		bound, err = template.WithHashes(bound)
		if err != nil {
			t.Fatal(err)
		}
		return validateInitializationRules(Project{ID: "synthetic-project", ProjectVersion: 1,
			Spec:         map[string]string{"research_subject": "synthetic subject", "research_goal": "reviewed goal"},
			TemplateCopy: &ProjectTemplateCopy{Definition: bound, RuleSetSnapshot: &set}})
	}
	if err := check(nil); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("unoverridden blocking source rule must reject: %v", err)
	}
	if err := check([]template.RuleBindingOverride{{RuleID: "goal-pattern", Parameters: map[string]any{"pattern": ".+"}}}); err != nil {
		t.Fatalf("effective allowed override must be evaluated rather than its source sidecar: %v", err)
	}
}
