package delivery

import (
	"errors"
	"reflect"
	"testing"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
)

func independentFrozenInput(t *testing.T) DeliveryInput {
	t.Helper()
	input := validDeliveryInput()
	set := lingdoctemplate.RuleSet{ID: "delivery-independent", Version: "1", State: lingdoctemplate.RuleSetDraft, Bindings: []lingdoctemplate.RuleBinding{}}
	for _, rule := range input.Template.Rules {
		set.Bindings = append(set.Bindings, lingdoctemplate.RuleBinding{RuleID: rule.ID, DefinitionID: "computed/" + rule.Evaluator, DefinitionVersion: "1", Origin: "builtin", Parameters: rule.Parameters, Severity: rule.Severity, Enabled: true, TriggerScenes: []string{"write_check", "preflight"}, Owner: "server-fixture"})
	}
	set, err := lingdoctemplate.PublishRuleSet(set)
	if err != nil {
		t.Fatal(err)
	}
	input.Template.Rules, err = lingdoctemplate.CompileRuleSet(set)
	if err != nil {
		t.Fatal(err)
	}
	input.Template, err = lingdoctemplate.WithHashes(input.Template)
	if err != nil {
		t.Fatal(err)
	}
	input.RuleSetSnapshot = &set
	input.TemplateCopyID = "copy-independent"
	input.TemplateCopyVersion = 1
	input.TemplateCopyContentHash = input.Template.ContentHash
	input.TemplateCopyRulesetHash = set.RulesetHash
	input.SourceRuleSetID = set.ID
	input.SourceRuleSetVersion = set.Version
	input.SourceRuleSetHash = set.RulesetHash
	input.SourcePolicyHash, err = governance.HashBindingPolicies(nil)
	if err != nil {
		t.Fatal(err)
	}
	return input
}
func TestIndependentRuleSnapshotPersistsAndReplaysOneHistoricalIdentity(t *testing.T) {
	forEachSnapshotStore(t, func(t *testing.T, store snapshotStoreUnderTest) {
		input := independentFrozenInput(t)
		service := NewReleaseService(store)
		snapshot, err := service.Prepare(input)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Check.Status != CheckPassed || snapshot.Check.RulesetHash != input.RuleSetSnapshot.RulesetHash || snapshot.Check.RulesetHash == input.Template.RulesetHash {
			t.Fatal("independent identity collapsed into compiled hash")
		}
		input.RuleSetSnapshot.Bindings[0].Severity = SeverityInfo
		stored, err := store.Get(snapshot.ProjectID, snapshot.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.FrozenInput.RuleSetSnapshot == nil || stored.FrozenInput.TemplateCopyID != "copy-independent" || stored.FrozenInput.SourcePolicyHash != snapshot.FrozenInput.SourcePolicyHash || stored.FrozenInput.RuleSetSnapshot.Bindings[0].Severity != SeverityBlocking {
			t.Fatal("snapshot sidecar was lost or aliased")
		}
		if !reflect.DeepEqual(Evaluate(stored.FrozenInput), snapshot.Check) {
			t.Fatal("persisted historical check changed")
		}
		if digest, err := digestFrozenInput(stored.FrozenInput); err != nil || digest != snapshot.SnapshotDigest {
			t.Fatal("historical digest cannot be recomputed", err)
		}
		if err := ValidateRuleSnapshotReceipt(stored); err != nil {
			t.Fatal(err)
		}
	})
}
func TestIndependentRuleSnapshotFailsClosedOnCopyRulePolicyOrReceiptTamper(t *testing.T) {
	mutations := []func(*DeliveryInput){func(in *DeliveryInput) { in.TemplateCopyRulesetHash = in.Template.RulesetHash }, func(in *DeliveryInput) { in.SourceRuleSetHash = sha256Text("wrong source identity") }, func(in *DeliveryInput) { in.SourcePolicyHash = "" }, func(in *DeliveryInput) { in.RuleSetSnapshot.Bindings[0].Severity = SeverityInfo }}
	for n, mutate := range mutations {
		input := independentFrozenInput(t)
		mutate(&input)
		if _, err := NewReleaseService(NewMemorySnapshotStore()).Freeze(input); err == nil {
			t.Fatalf("tamper %d froze", n)
		}
	}
	store := NewMemorySnapshotStore()
	snapshot, err := NewReleaseService(store).Prepare(independentFrozenInput(t))
	if err != nil {
		t.Fatal(err)
	}
	snapshot.FrozenInput.Spec["research_goal"] = "tampered after frozen"
	if err := ValidateRuleSnapshotReceipt(snapshot); err == nil {
		t.Fatal("passed flag overrode digest tamper")
	}
	fresh, err := NewReleaseService(NewMemorySnapshotStore()).Freeze(independentFrozenInput(t))
	if err != nil {
		t.Fatal(err)
	}
	fresh.Check.RulesetHash = fresh.FrozenInput.Template.RulesetHash
	if err := ValidateRuleSnapshotReceipt(fresh); err == nil {
		t.Fatal("compiled hash overrode independent receipt")
	}
}
func TestIndependentRuleSnapshotUnknownModelResultBlocksBothChecksAndExport(t *testing.T) {
	input := independentFrozenInput(t)
	set := *input.RuleSetSnapshot
	set.State = lingdoctemplate.RuleSetDraft
	set.RulesetHash = ""
	set.Bindings = append(set.Bindings, lingdoctemplate.RuleBinding{RuleID: "human-model-check", DefinitionID: "model_assisted", DefinitionVersion: "1", Origin: "organization", TargetKind: "field", TargetID: "research_goal", Severity: SeverityWarning, Enabled: true, TriggerScenes: []string{"preflight"}, Owner: "curator"})
	var err error
	set, err = lingdoctemplate.PublishRuleSet(set)
	if err != nil {
		t.Fatal(err)
	}
	input.RuleSetSnapshot = &set
	input.TemplateCopyRulesetHash = set.RulesetHash
	input.SourceRuleSetHash = set.RulesetHash
	input.Template.Rules, err = lingdoctemplate.CompileRuleSet(set)
	if err != nil {
		t.Fatal(err)
	}
	input.Template, err = lingdoctemplate.WithHashes(input.Template)
	if err != nil {
		t.Fatal(err)
	}
	input.TemplateCopyContentHash = input.Template.ContentHash
	check, err := EvaluateTemplate(input)
	if err != nil || check.Status != TemplateCheckBlocked {
		t.Fatalf("UNKNOWN template check: %+v %v", check, err)
	}
	snapshot, err := NewReleaseService(NewMemorySnapshotStore()).Freeze(input)
	if err != nil || snapshot.Check.Status != CheckBlocked {
		t.Fatalf("UNKNOWN preflight: %+v %v", snapshot, err)
	}
	snapshot.Check.Status = CheckPassed
	if !errors.Is(ValidateRuleSnapshotReceipt(snapshot), ErrExportPreflightBlocked) {
		t.Fatal("client PASS bypassed actual unknown result")
	}
}
func TestIndependentHistoricalWithdrawnSnapshotStillEvaluatesItsExactBindings(t *testing.T) {
	input := independentFrozenInput(t)
	input.RuleSetSnapshot.State = lingdoctemplate.RuleSetWithdrawn
	check, err := EvaluateTemplate(input)
	if err != nil || check.Status != TemplateCheckPassed || check.RulesetHash != input.TemplateCopyRulesetHash {
		t.Fatalf("historical withdrawn value failed replay: %+v %v", check, err)
	}
}
