package template

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func validGovernanceTemplate() Template {
	return Template{
		ID: "grant-demo", Name: "Synthetic grant template", Version: "1",
		Sections:       []Section{{ID: "question", Title: "Question", Required: true}},
		Fields:         []Field{{ID: "research_goal", Label: "Research goal", Type: "text", Required: true}},
		RequiredFields: []string{"research_goal"},
		Terms:          []Term{{ID: "clinical-trial", Preferred: "clinical trial", Variants: []string{"clinical experiment"}}},
		Rules: []Rule{
			{ID: "field-present", Kind: "presence", Severity: "blocking", Parameters: map[string]any{"target_kind": "field", "target_id": "research_goal"}},
			{ID: "goal-pattern", Kind: "pattern", Severity: "warning", Parameters: map[string]any{"target_kind": "field", "target_id": "research_goal", "pattern": `^Study .+`}},
			{ID: "term-consistency", Kind: "consistency", Severity: "warning", Parameters: map[string]any{"target_kind": "field", "target_id": "research_goal", "preferred": "clinical trial", "variants": []string{"clinical experiment"}}},
			{ID: "required-fields", Kind: "computed", Severity: "blocking", Evaluator: "required_fields", Parameters: map[string]any{}},
		},
	}
}

func TestValidateRejectsUnlistedCodeAndRuleKinds(t *testing.T) {
	for _, mutate := range []func(*Template){
		func(template *Template) {
			template.Rules[0].Parameters["script"] = `return true`
		},
		func(template *Template) {
			template.Rules[0].Kind = "javascript"
		},
		func(template *Template) {
			template.Rules[0].Parameters["target_kind"] = "all"
		},
		func(template *Template) {
			template.Rules[1].Parameters["pattern"] = "["
		},
		func(template *Template) {
			template.Rules[3].Evaluator = "arbitrary_expression"
		},
	} {
		definition := validGovernanceTemplate()
		mutate(&definition)
		if err := Validate(definition); !errors.Is(err, ErrInvalidDefinition) {
			t.Fatalf("Validate() error = %v, want ErrInvalidDefinition", err)
		}
	}
}

func TestHashesAreDeterministicAndServerOwned(t *testing.T) {
	definition := validGovernanceTemplate()
	firstContent, firstRules, err := Hashes(definition)
	if err != nil {
		t.Fatal(err)
	}
	definition.RulesetHash = "client-controlled"
	definition.ContentHash = "client-controlled"
	secondContent, secondRules, err := Hashes(definition)
	if err != nil {
		t.Fatal(err)
	}
	if firstContent != secondContent || firstRules != secondRules {
		t.Fatalf("hashes changed when only caller hash fields changed: (%s,%s) vs (%s,%s)", firstContent, firstRules, secondContent, secondRules)
	}

	definition.Rules[0], definition.Rules[1] = definition.Rules[1], definition.Rules[0]
	_, reorderedRules, err := Hashes(definition)
	if err != nil {
		t.Fatal(err)
	}
	if firstRules != reorderedRules {
		t.Fatalf("ruleset hash depends on input rule order: %s vs %s", firstRules, reorderedRules)
	}
}

func TestEvaluateProducesStableVersionBoundResults(t *testing.T) {
	definition := validGovernanceTemplate()
	input := EvaluationInput{
		ProjectID: "project-1", TargetVersion: "copy/1",
		Fields: map[string]string{"research_goal": "Study a synthetic clinical experiment"},
	}
	first, err := Evaluate(definition, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Evaluate(definition, input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("evaluation is not deterministic:\n%#v\n%#v", first, second)
	}
	if len(first) != 4 {
		t.Fatalf("got %d results; want presence, pattern, consistency, and required-fields", len(first))
	}
	for _, result := range first {
		if result.TargetVersion == "" || result.RulesetHash == "" || result.EvaluatorVersion != EvaluatorVersion {
			t.Fatalf("result is missing server-owned version evidence: %#v", result)
		}
		if result.RuleID == "term-consistency" && result.Status != EvaluationIssue {
			t.Fatalf("term result = %s, want ISSUE", result.Status)
		}
	}

	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []Evaluation
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, decoded) {
		t.Fatalf("evaluation JSON round-trip differs: %#v != %#v", first, decoded)
	}
}

func TestEvaluateUsesSameEvaluatorForFrozenProjection(t *testing.T) {
	definition := validGovernanceTemplate()
	live := EvaluationInput{
		ProjectID: "project-1", TargetVersion: "chapter-v3",
		Fields:   map[string]string{"research_goal": "Study a clinical trial"},
		Chapters: []ChapterInput{{ID: "question", Version: "chapter-v3", Body: "A question", Confirmed: true}},
	}
	frozen := live
	frozen.Fields = map[string]string{"research_goal": "Study a clinical trial"}
	frozen.Chapters = append([]ChapterInput(nil), live.Chapters...)
	liveResults, err := Evaluate(definition, live)
	if err != nil {
		t.Fatal(err)
	}
	frozenResults, err := Evaluate(definition, frozen)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(liveResults, frozenResults) {
		t.Fatalf("equivalent live and frozen input produced different results:\n%#v\n%#v", liveResults, frozenResults)
	}
}

func TestEvaluateRejectsMissingTargetVersion(t *testing.T) {
	if _, err := Evaluate(validGovernanceTemplate(), EvaluationInput{ProjectID: "project-1"}); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("Evaluate() error = %v, want ErrInvalidDefinition", err)
	}
}
