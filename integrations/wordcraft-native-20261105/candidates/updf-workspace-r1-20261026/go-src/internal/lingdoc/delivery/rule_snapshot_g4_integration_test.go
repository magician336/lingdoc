package delivery

import (
	"reflect"
	"testing"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
)

func canonicalG4FrozenInput(t *testing.T) DeliveryInput {
	t.Helper()
	input := validDeliveryInput()
	input.ContextRevision = 13
	set, err := lingdoctemplate.PublishRuleSet(lingdoctemplate.RuleSet{
		ID: "g4-delivery-canonical", Version: "v1", State: lingdoctemplate.RuleSetDraft,
		Bindings: []lingdoctemplate.RuleBinding{{
			RuleID: "g4-goal-pattern", DefinitionID: "pattern", DefinitionVersion: "1",
			Origin: "organization", TargetKind: "field", TargetID: "research_goal",
			Severity: SeverityBlocking, TriggerScenes: []string{"write_check", "preflight"},
			Enabled: true, Owner: "fixture", Parameters: map[string]any{"pattern": "^unoverridden$"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	input.Template.RuleSetSnapshot = &set
	input.Template.RuleSetRef = &lingdoctemplate.RuleSetReference{ID: set.ID, Version: set.Version, Hash: set.RulesetHash}
	input.Template.RuleBindingOverrides = []lingdoctemplate.RuleBindingOverride{{
		RuleID: "g4-goal-pattern", Parameters: map[string]any{"pattern": "^验证$"},
	}}
	input.Template, err = lingdoctemplate.WithHashes(input.Template)
	if err != nil {
		t.Fatal(err)
	}
	input.TemplateCopyID, input.TemplateCopyVersion = "copy-g4-canonical", 7
	input.TemplateCopyContentHash = input.Template.ContentHash
	input.TemplateCopyRulesetHash = input.Template.RulesetHash
	return input
}

func TestCanonicalG4FrozenSnapshotPreservesOverrideAndExactReference(t *testing.T) {
	forEachSnapshotStore(t, func(t *testing.T, store snapshotStoreUnderTest) {
		input := canonicalG4FrozenInput(t)
		ref := *input.Template.RuleSetRef
		if input.Template.RulesetHash == ref.Hash {
			t.Fatal("fixture collapsed effective and source hash")
		}
		for _, scene := range []string{"write_check", "preflight"} {
			result, err := EvaluateTemplateScene(input, scene)
			if err != nil || result.Status != TemplateCheckPassed || result.RulesetHash != input.Template.RulesetHash || !reflect.DeepEqual(result.RuleSetRef, &ref) {
				t.Fatalf("%s override evaluation = %+v, err=%v", scene, result, err)
			}
			found := false
			for _, evaluation := range result.Evaluations {
				if evaluation.RuleID == "g4-goal-pattern" {
					found = true
					if evaluation.Status != lingdoctemplate.EvaluationPass || evaluation.RuleSetID != ref.ID || evaluation.RuleSetVersion != ref.Version || evaluation.RulesetHash != input.Template.RulesetHash {
						t.Fatalf("override did not drive the exact-version result: %+v", evaluation)
					}
				}
			}
			if !found {
				t.Fatal("bound pattern was not evaluated")
			}
		}
		snapshot, err := NewReleaseService(store).Prepare(input)
		if err != nil || snapshot.Check.Status != CheckPassed {
			t.Fatalf("canonical freeze = %+v, err=%v", snapshot, err)
		}
		// Simulate a later source lifecycle change on the caller's value. A
		// historical frozen receipt must replay its captured published snapshot.
		input.Template.RuleSetSnapshot.State = lingdoctemplate.RuleSetWithdrawn
		input.Template.RuleBindingOverrides[0].Parameters["pattern"] = "^changed$"
		snapshot.Check.RuleSetRef.Version = "caller-mutated"
		stored, err := store.Get(snapshot.ProjectID, snapshot.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.FrozenInput.ContextRevision != input.ContextRevision {
			t.Fatalf("context revision was lost during the actual snapshot round trip: got=%d want=%d", stored.FrozenInput.ContextRevision, input.ContextRevision)
		}
		if !reflect.DeepEqual(stored.RuleSetRef, &ref) || !reflect.DeepEqual(stored.Check.RuleSetRef, &ref) || stored.FrozenInput.Template.RuleSetSnapshot.State != lingdoctemplate.RuleSetPublished || stored.FrozenInput.Template.RuleBindingOverrides[0].Parameters["pattern"] != "^验证$" {
			t.Fatalf("frozen source/override/reference was lost or aliased: %+v", stored)
		}
		if err := ValidateRuleSnapshotReceipt(stored); err != nil {
			t.Fatalf("canonical historical receipt replay: %v", err)
		}
	})
}

func TestCanonicalG4FrozenSnapshotAcceptsAbsentOrConsistentLegacyPins(t *testing.T) {
	for _, mode := range []string{"canonical", "original-g4-no-effective-alias", "dual-written"} {
		t.Run(mode, func(t *testing.T) {
			input := canonicalG4FrozenInput(t)
			if mode == "original-g4-no-effective-alias" {
				input.TemplateCopyRulesetHash = ""
			}
			if mode == "dual-written" {
				input.RuleSetSnapshot = cloneRuleSnapshot(input.Template.RuleSetSnapshot)
				ref := input.Template.RuleSetRef
				input.SourceRuleSetID, input.SourceRuleSetVersion, input.SourceRuleSetHash = ref.ID, ref.Version, ref.Hash
				var err error
				input.SourcePolicyHash, err = governance.HashBindingPolicies(nil)
				if err != nil {
					t.Fatal(err)
				}
			}
			snapshot, err := NewReleaseService(NewMemorySnapshotStore()).Freeze(input)
			if err != nil || snapshot.Check.RulesetHash != input.Template.RulesetHash || snapshot.Check.Status != CheckPassed {
				t.Fatalf("canonical input with %s pins = %+v, err=%v", mode, snapshot, err)
			}
		})
	}
	legacy := independentFrozenInput(t)
	legacy.RuleSetSnapshot.State = lingdoctemplate.RuleSetWithdrawn
	if err := ValidateFrozenRuleSnapshot(legacy); err != nil {
		t.Fatalf("legacy historical validation changed: %v", err)
	}
	if FrozenRuleSetHash(legacy) != legacy.RuleSetSnapshot.RulesetHash {
		t.Fatal("legacy independent identity changed to the compiled projection")
	}
}

func TestCanonicalG4FrozenSnapshotRejectsIndependentIdentityTampering(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*DeliveryInput)
	}{
		{"reference", func(in *DeliveryInput) { in.Template.RuleSetRef.Version = "other" }},
		{"snapshot-content", func(in *DeliveryInput) { in.Template.RuleSetSnapshot.Bindings[0].Parameters["pattern"] = "forged" }},
		{"override", func(in *DeliveryInput) { in.Template.RuleBindingOverrides[0].Parameters["pattern"] = "forged" }},
		{"effective-hash", func(in *DeliveryInput) { in.Template.RulesetHash = in.Template.RuleSetRef.Hash }},
		{"copy-content", func(in *DeliveryInput) { in.TemplateCopyContentHash = sha256Text("forged") }},
		{"copy-effective", func(in *DeliveryInput) { in.TemplateCopyRulesetHash = in.Template.RuleSetRef.Hash }},
		{"source-pin", func(in *DeliveryInput) { in.SourceRuleSetID = "unrelated-source" }},
		{"mirror", func(in *DeliveryInput) {
			in.RuleSetSnapshot = cloneRuleSnapshot(in.Template.RuleSetSnapshot)
			in.RuleSetSnapshot.RulesetHash = sha256Text("forged")
		}},
		{"policy", func(in *DeliveryInput) { in.SourcePolicyHash = sha256Text("forged") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := canonicalG4FrozenInput(t)
			tc.mutate(&input)
			if _, err := NewReleaseService(NewMemorySnapshotStore()).Freeze(input); err == nil {
				t.Fatal("tampered canonical input froze")
			}
		})
	}
}

func TestCanonicalG4ReceiptReplaysEmbeddedSnapshotWithoutLegacySidecar(t *testing.T) {
	for _, kind := range []string{"digest", "snapshot-reference", "check-reference", "effective-hash"} {
		t.Run(kind, func(t *testing.T) {
			snapshot, err := NewReleaseService(NewMemorySnapshotStore()).Freeze(canonicalG4FrozenInput(t))
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.FrozenInput.RuleSetSnapshot != nil {
				t.Fatal("fixture unexpectedly uses the legacy sidecar")
			}
			if err := ValidateRuleSnapshotReceipt(snapshot); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "digest":
				snapshot.FrozenInput.Spec["research_goal"] = "forged"
			case "snapshot-reference":
				snapshot.RuleSetRef.Version = "forged"
			case "check-reference":
				snapshot.Check.RuleSetRef.Version = "forged"
			case "effective-hash":
				snapshot.Check.RulesetHash = snapshot.FrozenInput.Template.RuleSetRef.Hash
			}
			if err := ValidateRuleSnapshotReceipt(snapshot); err == nil {
				t.Fatal("stored PASS bypassed the embedded receipt replay")
			}
		})
	}
}
