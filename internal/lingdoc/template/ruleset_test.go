package template

import (
	"errors"
	"reflect"
	"strings"
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

func TestRuleSetRejectsInvalidSeverityBeforePublishOrBinding(t *testing.T) {
	for _, severity := range []string{"", "fatal"} {
		t.Run("severity_"+severity, func(t *testing.T) {
			draft := testRuleSet()
			draft.Bindings[0].Severity = severity
			if _, err := PublishRuleSet(draft); !errors.Is(err, ErrInvalidRuleSet) {
				t.Fatalf("PublishRuleSet() error = %v, want ErrInvalidRuleSet", err)
			}

			published := draft
			published.State = RuleSetPublished
			published.RulesetHash = strings.Repeat("a", 64)
			if err := CanBindRuleSet(published); !errors.Is(err, ErrInvalidRuleSet) {
				t.Fatalf("CanBindRuleSet() error = %v, want ErrInvalidRuleSet", err)
			}
		})
	}
}

func TestRuleSetIdentityMatchesDatabaseLengthContract(t *testing.T) {
	valid := testRuleSet()
	valid.ID, valid.Version = strings.Repeat("r", 64), strings.Repeat("v", 64)
	if _, err := PublishRuleSet(valid); err != nil {
		t.Fatalf("64-character ruleset identity rejected: %v", err)
	}

	for _, field := range []string{"id", "version"} {
		t.Run(field, func(t *testing.T) {
			invalid := testRuleSet()
			if field == "id" {
				invalid.ID = strings.Repeat("r", 65)
			} else {
				invalid.Version = strings.Repeat("v", 65)
			}
			if err := ValidateRuleSet(invalid); !errors.Is(err, ErrInvalidRuleSet) {
				t.Fatalf("ValidateRuleSet() error = %v, want ErrInvalidRuleSet", err)
			}
		})
	}
}

func TestEvaluateRuleSetHonorsTriggerScenesAndPinsRuleSetHash(t *testing.T) {
	published, err := PublishRuleSet(testRuleSet())
	if err != nil {
		t.Fatal(err)
	}
	input := EvaluationInput{
		ProjectID: "project-1", TargetVersion: "copy/1",
		Fields:   map[string]string{"research_goal": "Study"},
		Chapters: []ChapterInput{{ID: "method", Version: "chapter-v2", Body: "too short"}},
	}

	writeChecks, err := EvaluateRuleSet(DemoTemplate(), published, "write_check", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(writeChecks) != 2 {
		t.Fatalf("write_check evaluated %d rules; want only the two active bindings for that scene: %#v", len(writeChecks), writeChecks)
	}
	for _, result := range writeChecks {
		if result.RulesetHash != published.RulesetHash {
			t.Fatalf("write_check result used template rule hash %q instead of RuleSet hash %q", result.RulesetHash, published.RulesetHash)
		}
		if result.RuleID == "human-review" {
			t.Fatal("preflight-only model-assisted binding ran during write_check")
		}
	}

	preflight, err := EvaluateRuleSet(DemoTemplate(), published, "preflight", input)
	if err != nil {
		t.Fatal(err)
	}
	if len(preflight) != 2 {
		t.Fatalf("preflight evaluated %d rules; want only its two active bindings: %#v", len(preflight), preflight)
	}
	var modelResult *Evaluation
	for i := range preflight {
		if preflight[i].RuleID == "human-review" {
			modelResult = &preflight[i]
		}
		if preflight[i].RulesetHash != published.RulesetHash {
			t.Fatalf("preflight result used rule hash %q instead of RuleSet hash %q", preflight[i].RulesetHash, published.RulesetHash)
		}
	}
	if modelResult == nil || modelResult.Status != EvaluationUnknown || modelResult.Severity == SeverityBlocking {
		t.Fatalf("model-assisted rule did not remain a non-blocking unknown result: %#v", modelResult)
	}
}

func TestEvaluateRuleSetRejectsUnsupportedScene(t *testing.T) {
	if _, err := EvaluateRuleSet(DemoTemplate(), testRuleSet(), "export", EvaluationInput{ProjectID: "project-1", TargetVersion: "copy/1"}); !errors.Is(err, ErrInvalidRuleSet) {
		t.Fatalf("EvaluateRuleSet() error = %v, want ErrInvalidRuleSet", err)
	}
}

func TestBoundRuleSetSnapshotCombinesTemplateRulesAndPinsCombinedHash(t *testing.T) {
	published, err := PublishRuleSet(testRuleSet())
	if err != nil {
		t.Fatal(err)
	}
	definition := DemoTemplate()
	definition.RuleSetSnapshot = &published
	definition, err = WithHashes(definition)
	if err != nil {
		t.Fatal(err)
	}
	if definition.RulesetHash == published.RulesetHash {
		t.Fatal("combined template hash must include both legacy rules and exact RuleSet reference")
	}

	input := EvaluationInput{
		ProjectID: "project-1", TargetVersion: "copy/1",
		Fields:   map[string]string{"research_subject": "subject", "research_goal": "Study"},
		Chapters: []ChapterInput{{ID: "question", Version: "q-v1", Body: "question"}, {ID: "method", Version: "m-v1", Body: "short"}},
	}
	writeChecks, err := EvaluateScene(definition, "write_check", input)
	if err != nil {
		t.Fatal(err)
	}
	preflight, err := EvaluateScene(definition, "preflight", input)
	if err != nil {
		t.Fatal(err)
	}
	contains := func(results []Evaluation, id string) bool {
		for _, result := range results {
			if result.RuleID == id {
				if result.RulesetHash != definition.RulesetHash {
					t.Fatalf("evaluation %q used hash %q, want combined hash %q", id, result.RulesetHash, definition.RulesetHash)
				}
				return true
			}
		}
		return false
	}
	if !contains(writeChecks, RuleRequiredFields) || !contains(writeChecks, "goal-required") || !contains(writeChecks, "method-length") {
		t.Fatalf("write check must run legacy and write-scoped rules: %#v", writeChecks)
	}
	if !contains(preflight, RuleRequiredFields) || !contains(preflight, "goal-required") || !contains(preflight, "human-review") {
		t.Fatalf("preflight must run legacy and preflight-scoped rules: %#v", preflight)
	}
	if contains(preflight, "method-length") || contains(writeChecks, "human-review") {
		t.Fatalf("scene-scoped rule ran in the wrong scene: write=%#v preflight=%#v", writeChecks, preflight)
	}
}

func TestProjectRuleSetOverridesAreAllowlistedHashedAndEvaluated(t *testing.T) {
	published, err := PublishRuleSet(testRuleSet())
	if err != nil {
		t.Fatal(err)
	}
	definition := DemoTemplate()
	definition.RuleSetSnapshot = &published
	definition.RuleSetRef = &RuleSetReference{ID: published.ID, Version: published.Version, Hash: published.RulesetHash}
	base, err := WithHashes(definition)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	definition.RuleBindingOverrides = []RuleBindingOverride{{
		RuleID: "disabled-pattern", Enabled: &enabled,
		Parameters: map[string]any{"pattern": "^Grant"},
	}}
	configured, err := WithHashes(definition)
	if err != nil {
		t.Fatal(err)
	}
	if configured.RulesetHash == base.RulesetHash || configured.ContentHash == base.ContentHash {
		t.Fatal("project overrides must affect the effective rule and content hashes")
	}
	if published.Bindings[3].Parameters["pattern"] != "^Study" || ValidateRuleSet(published) != nil {
		t.Fatal("project override mutated the published RuleSet snapshot")
	}
	results, err := EvaluateScene(configured, "write_check", EvaluationInput{
		ProjectID: "project-1", TargetVersion: "copy/2", Fields: map[string]string{"research_goal": "Study"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if result.RuleID == "disabled-pattern" {
			if result.Status != EvaluationIssue || result.RuleSetID != published.ID || result.RuleSetVersion != published.Version || result.RulesetHash != configured.RulesetHash {
				t.Fatalf("overridden rule did not report its exact effective identity: %#v", result)
			}
			return
		}
	}
	t.Fatal("allowlisted enable and pattern override were not applied by the evaluator")
}

func TestRuleSetOverrideRejectsUnknownIdentityAndUnsafeParameters(t *testing.T) {
	published, err := PublishRuleSet(testRuleSet())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name     string
		override RuleBindingOverride
	}{
		{name: "unknown rule", override: RuleBindingOverride{RuleID: "not-in-set", Enabled: boolPointer(true)}},
		{name: "unknown parameter", override: RuleBindingOverride{RuleID: "disabled-pattern", Parameters: map[string]any{"expression": "true"}}},
		{name: "severity forgery", override: RuleBindingOverride{RuleID: "disabled-pattern", Parameters: map[string]any{"severity": "info"}}},
		{name: "invalid pattern", override: RuleBindingOverride{RuleID: "disabled-pattern", Parameters: map[string]any{"pattern": "("}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateRuleBindingOverrides(published, []RuleBindingOverride{test.override}); !errors.Is(err, ErrInvalidRuleSet) {
				t.Fatalf("unsafe override error = %v, want ErrInvalidRuleSet", err)
			}
		})
	}
}

func boolPointer(value bool) *bool { return &value }

func TestTemplateRejectsNonPublishedOrConflictingRuleSetSnapshot(t *testing.T) {
	draft := testRuleSet()
	definition := DemoTemplate()
	definition.RuleSetSnapshot = &draft
	if _, err := WithHashes(definition); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("draft RuleSet binding error = %v, want ErrInvalidDefinition", err)
	}

	published, err := PublishRuleSet(testRuleSet())
	if err != nil {
		t.Fatal(err)
	}
	published.Bindings[0].RuleID = RuleRequiredFields
	if err := ValidateRuleSet(published); !errors.Is(err, ErrInvalidRuleSet) {
		t.Fatal("mutating a published RuleSet without a new hash must fail")
	}

	published, err = PublishRuleSet(testRuleSet())
	if err != nil {
		t.Fatal(err)
	}
	published.Bindings[0].RuleID = RuleRequiredFields
	published.RulesetHash, err = HashRuleSet(published)
	if err != nil {
		t.Fatal(err)
	}
	definition = DemoTemplate()
	definition.RuleSetSnapshot = &published
	if _, err := WithHashes(definition); !errors.Is(err, ErrInvalidDefinition) {
		t.Fatalf("colliding rule IDs error = %v, want ErrInvalidDefinition", err)
	}
}

func TestEvaluatePinsLegacyDemoHashOnlyForUnchangedDemoRules(t *testing.T) {
	input := EvaluationInput{ProjectID: "project-1", TargetVersion: "copy/1"}
	baseline, err := Evaluate(DemoTemplate(), input)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range baseline {
		if result.RulesetHash != DemoRulesetHash {
			t.Fatalf("fixed demo result lost its published ruleset hash: %#v", result)
		}
	}

	changed := DemoTemplate()
	changed.Rules[0].Message = "changed rule message"
	_, wantHash, err := Hashes(changed)
	if err != nil {
		t.Fatal(err)
	}
	evaluated, err := Evaluate(changed, input)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range evaluated {
		if result.RuleID == RuleRequiredFields && (result.RulesetHash == DemoRulesetHash || result.RulesetHash != wantHash) {
			t.Fatalf("modified demo rule incorrectly reused fixed demo hash: %#v", result)
		}
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
