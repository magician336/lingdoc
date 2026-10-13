package template

import (
	"errors"
	"reflect"
	"testing"
)

func testRuleSet() RuleSet {
	return RuleSet{
		ID: "grant-rules", Version: "1", State: RuleSetDraft,
		Bindings: []RuleBinding{
			{
				RuleID: "goal-required", DefinitionID: "presence", DefinitionVersion: "1",
				Origin: "organization", TargetKind: "field", TargetID: "research_goal",
				Severity: "blocking", TriggerScenes: []string{"preflight", "write_check"},
				PositiveExampleRefs: []string{"goal-present"}, Enabled: true, Owner: "research-office",
			},
			{
				RuleID: "method-length", DefinitionID: "computed/word_count", DefinitionVersion: "1",
				Origin: "project", TargetKind: "chapter", TargetID: "method",
				Parameters: map[string]any{"minimum": 10, "maximum": 500}, Severity: "warning",
				TriggerScenes: []string{"write_check"}, NegativeExampleRefs: []string{"short-method"},
				Enabled: true, Owner: "project-owner",
			},
			{
				RuleID: "human-review", DefinitionID: "model_assisted", DefinitionVersion: "1",
				Origin: "builtin", TargetKind: "field", TargetID: "research_goal",
				Severity: "info", TriggerScenes: []string{"preflight"}, Enabled: true, Owner: "lingdoc",
			},
			{
				RuleID: "disabled-pattern", DefinitionID: "pattern", DefinitionVersion: "1",
				Origin: "project", TargetKind: "field", TargetID: "research_goal",
				Parameters: map[string]any{"pattern": "^Study"}, Severity: "warning",
				TriggerScenes: []string{"write_check"}, Enabled: false, Owner: "project-owner",
			},
		},
	}
}

func TestPublishRuleSetComputesHashAndCompilesAllowlistedRules(t *testing.T) {
	draft := testRuleSet()
	draft.RulesetHash = "client-controlled"
	published, err := PublishRuleSet(draft)
	if err != nil {
		t.Fatal(err)
	}
	if published.State != RuleSetPublished || published.RulesetHash == "" || published.RulesetHash == "client-controlled" {
		t.Fatalf("published ruleset was not server-stamped: %#v", published)
	}
	if err := ValidateRuleSet(published); err != nil {
		t.Fatalf("ValidateRuleSet(published) error = %v", err)
	}
	if err := CanBindRuleSet(published); err != nil {
		t.Fatalf("published ruleset cannot be bound: %v", err)
	}

	rules, err := CompileRuleSet(published)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 3 {
		t.Fatalf("CompileRuleSet() returned %d rules; want 3 enabled rules", len(rules))
	}
	if rules[0].ID != "goal-required" || rules[1].ID != "human-review" || rules[2].ID != "method-length" {
		t.Fatalf("compiled rules are not in stable ID order: %#v", rules)
	}
	if rules[1].Severity != "info" || rules[1].Kind != "model_assisted" {
		t.Fatalf("model-assisted rule lost its non-blocking contract: %#v", rules[1])
	}
	if _, ok := rules[0].Parameters["evaluator"]; ok {
		t.Fatal("compiled rule exposed a client-authored evaluator")
	}
}

func TestRuleSetHashIsStableAndIgnoresSuppliedHash(t *testing.T) {
	first := testRuleSet()
	firstHash, err := HashRuleSet(first)
	if err != nil {
		t.Fatal(err)
	}
	first.RulesetHash = "untrusted"
	first.Bindings[0], first.Bindings[1] = first.Bindings[1], first.Bindings[0]
	first.Bindings[1].TriggerScenes = []string{"write_check", "preflight"}
	secondHash, err := HashRuleSet(first)
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatalf("hash changed when binding or set-like scene order changed: %s != %s", firstHash, secondHash)
	}
}

func TestRuleSetRejectsUnsafeOrUnknownBindings(t *testing.T) {
	mutations := []struct {
		name   string
		mutate func(*RuleSet)
	}{
		{name: "unknown definition", mutate: func(set *RuleSet) { set.Bindings[0].DefinitionID = "javascript" }},
		{name: "wrong definition version", mutate: func(set *RuleSet) { set.Bindings[0].DefinitionVersion = "2" }},
		{name: "arbitrary expression", mutate: func(set *RuleSet) { set.Bindings[0].Parameters = map[string]any{"expression": "return true"} }},
		{name: "client evaluator", mutate: func(set *RuleSet) { set.Bindings[0].Parameters = map[string]any{"evaluator": "shell"} }},
		{name: "model-assisted blocker", mutate: func(set *RuleSet) { set.Bindings[2].Severity = "blocking" }},
		{name: "unsupported scene", mutate: func(set *RuleSet) { set.Bindings[0].TriggerScenes = []string{"export_without_preflight"} }},
		{name: "duplicate rule id", mutate: func(set *RuleSet) { set.Bindings[1].RuleID = set.Bindings[0].RuleID }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			set := testRuleSet()
			test.mutate(&set)
			if err := ValidateRuleSet(set); !errors.Is(err, ErrInvalidRuleSet) {
				t.Fatalf("ValidateRuleSet() error = %v, want ErrInvalidRuleSet", err)
			}
		})
	}
}

func TestPublishedRuleSetDetectsStaleHashAndCanBeWithdrawnOrRecalled(t *testing.T) {
	published, err := PublishRuleSet(testRuleSet())
	if err != nil {
		t.Fatal(err)
	}
	tampered := cloneRuleSet(published)
	tampered.Bindings[0].Severity = "info"
	if err := ValidateRuleSet(tampered); !errors.Is(err, ErrInvalidRuleSet) {
		t.Fatalf("mutated published content validated: %v", err)
	}

	withdrawn, err := WithdrawRuleSet(published)
	if err != nil {
		t.Fatal(err)
	}
	if withdrawn.State != RuleSetWithdrawn || withdrawn.RulesetHash != published.RulesetHash {
		t.Fatalf("withdrawal changed the historical snapshot: %#v", withdrawn)
	}
	if err := CanBindRuleSet(withdrawn); !errors.Is(err, ErrRuleSetNotPublished) {
		t.Fatalf("withdrawn ruleset bind error = %v, want ErrRuleSetNotPublished", err)
	}
	if _, err := CompileRuleSet(withdrawn); err != nil {
		t.Fatalf("withdrawn historical ruleset cannot be compiled for replay: %v", err)
	}

	recalled, err := RecallRuleSet(published)
	if err != nil {
		t.Fatal(err)
	}
	if recalled.State != RuleSetRecalled || recalled.RulesetHash != published.RulesetHash {
		t.Fatalf("recall changed the historical snapshot: %#v", recalled)
	}
}

func TestRuleDefinitionCatalogReturnsDefensiveCopies(t *testing.T) {
	first := RuleDefinitions()
	for i := range first {
		if first[i].ID == "presence" {
			first[i].AllowedTargetKinds[0] = "arbitrary"
		}
	}
	second := RuleDefinitions()
	for _, definition := range second {
		if definition.ID == "presence" && !reflect.DeepEqual(definition.AllowedTargetKinds, []string{"field", "chapter", "source", "review_item"}) {
			t.Fatalf("catalog was mutated through returned slice: %#v", definition)
		}
	}
}

func TestPublishRuleSetRequiresEnabledRule(t *testing.T) {
	draft := testRuleSet()
	for i := range draft.Bindings {
		draft.Bindings[i].Enabled = false
	}
	if _, err := PublishRuleSet(draft); !errors.Is(err, ErrInvalidRuleSet) {
		t.Fatalf("PublishRuleSet() error = %v, want ErrInvalidRuleSet", err)
	}
}

