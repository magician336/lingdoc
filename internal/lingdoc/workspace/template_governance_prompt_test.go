package workspace

import (
	"encoding/json"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	"strings"
	"testing"
)

func TestRuleSuggestionPromptExamplesMatchStrictServerDefinitions(t *testing.T) {
	for _, target := range []string{"field", "chapter", "source", "review_item"} {
		definitions := ruleSuggestionDefinitions(target)
		if len(definitions) == 0 {
			t.Fatalf("missing applicable definitions for %s", target)
		}
		for _, definition := range definitions {
			// Validate after a JSON round trip, as actual model parameters arrive.
			raw, _ := json.Marshal(definition.ParameterExample)
			parameters := map[string]any{}
			if err := json.Unmarshal(raw, &parameters); err != nil {
				t.Fatal(err)
			}
			set := template.RuleSet{ID: "prompt-check", Version: "1", State: template.RuleSetDraft,
				Bindings: []template.RuleBinding{{RuleID: "candidate", DefinitionID: definition.ID,
					DefinitionVersion: definition.Version, Origin: "project", TargetKind: target,
					TargetID: "selected-target", Parameters: parameters, Severity: template.SeverityInfo,
					TriggerScenes: []string{"write_check", "preflight"}, Enabled: true, Owner: "human"}}}
			if err := template.ValidateRuleSet(set); err != nil {
				t.Fatalf("model syntax example %s/%s is rejected by server: %v", target, definition.ID, err)
			}
		}
	}
	if len(ruleSuggestionDefinitions("hidden-or-unknown-target")) != 0 {
		t.Fatal("unsupported target offered definitions")
	}
}

func TestRuleSuggestionPromptKeepsSyntheticTemplateAndAuthorizedScope(t *testing.T) {
	rules, err := TemplateGovernanceDemoRuleSet()
	if err != nil {
		t.Fatal(err)
	}
	request := governance.RuleSuggestionRequest{
		Scope:    governance.SuggestionScope{ProjectID: "selected-project", TargetKind: "field", TargetID: "research_subject", Sources: []governance.SourceRef{}},
		Template: template.DemoTemplate(), RuleSet: rules, PromptVersion: governance.RuleSuggestionPromptVersion,
	}
	raw, err := ruleSuggestionStructuredInput(request)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Scope       governance.SuggestionScope     `json:"scope"`
		Template    template.Template              `json:"template"`
		Rules       template.RuleSet               `json:"ruleset"`
		Definitions []suggestionDefinitionContract `json:"allowed_definitions"`
		Example     governance.SuggestionPayload   `json:"output_schema"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Template.Fields) != 2 || len(envelope.Template.Sections) != 2 || len(envelope.Rules.Bindings) != 5 || len(envelope.Scope.Sources) != 0 {
		t.Fatal("prompt expanded synthetic template or authorized sources")
	}
	if len(envelope.Example.Proposals) != 1 || envelope.Example.Proposals[0].DefinitionID != "presence" || envelope.Example.Proposals[0].TargetID != "research_subject" {
		t.Fatal("missing concrete target-compatible output example")
	}
	for _, definition := range envelope.Definitions {
		if definition.ID == "computed/required_fields" {
			t.Fatal("global check offered for a field-specific candidate")
		}
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"spec", "chapter_body", "history", "other_projects", "evidence_cards"} {
		if _, ok := top[forbidden]; ok {
			t.Fatalf("unexpected project data: %s", forbidden)
		}
	}
	if strings.Contains(string(raw), "allowlisted identifier") {
		t.Fatal("unresolved definition placeholder remains")
	}
}
