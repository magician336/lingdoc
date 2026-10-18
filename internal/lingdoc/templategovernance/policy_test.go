package templategovernance

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"gorm.io/gorm"
)

func TestReviewedBindingPolicyHashCanonicalAndCatalogImmutable(t *testing.T) {
	policies := []BindingPolicy{{RuleID: "goal-present", MayDisable: true, AllowedSeverities: []string{"warning", "info"}, AllowedTargetIDs: []string{"research_goal"}}}
	h, err := HashBindingPolicies(policies)
	if err != nil {
		t.Fatal(err)
	}
	reversed := []BindingPolicy{{RuleID: "goal-present", MayDisable: true, AllowedSeverities: []string{"info", "warning"}, AllowedTargetIDs: []string{"research_goal"}}}
	other, err := HashBindingPolicies(reversed)
	if err != nil || other != h {
		t.Fatal("set order changed reviewed policy identity")
	}
	empty, _ := HashBindingPolicies(nil)
	array, _ := HashBindingPolicies([]BindingPolicy{})
	if empty == "" || empty != array || empty == h {
		t.Fatal("empty policy must have a distinct real hash")
	}
	s, db, _, actor, draft, _ := seedService(t)
	input := draftInput(draft.RuleSet, draft.Version, draft.Revision)
	input.BindingPolicies = policies
	changed, err := s.SaveDraft(context.Background(), actor, "policy-draft-change", input)
	if err != nil {
		t.Fatal(err)
	}
	if changed.PolicyHash != h || changed.ContentHash != draft.ContentHash || changed.Revision != draft.Revision+1 {
		t.Fatal("policy update did not own a revision/independent hash")
	}
	published := publishTestVersion(t, s, actor, changed)
	altered := published
	altered.BindingPolicies = nil
	altered.PolicyHash = empty
	altered.Revision++
	if err := db.Transaction(func(txDB *gorm.DB) error { return saveVersion(txDB, actor, published, altered) }); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("published policy mutation: %v", err)
	}
	if err := db.Transaction(func(txDB *gorm.DB) error {
		actual, hash, err := s.BindingPoliciesForCopy(context.Background(), txDB, actor, published.TemplateID, published.Version)
		if err != nil {
			return err
		}
		if len(actual) != 1 || hash != h {
			t.Fatal("copy policy reader lost identity")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
func TestMandatoryBindingPoliciesCannotGrantChanges(t *testing.T) {
	set := template.RuleSet{ID: "mandatory-policy", Version: "1", State: template.RuleSetDraft, Bindings: []template.RuleBinding{{RuleID: "required-fields", DefinitionID: "computed/required_fields", DefinitionVersion: "1", Origin: "builtin", Severity: template.SeverityBlocking, Enabled: true, TriggerScenes: []string{"preflight"}, Owner: "system"}}}
	set, err := template.PublishRuleSet(set)
	if err != nil {
		t.Fatal(err)
	}
	for _, policy := range []BindingPolicy{{RuleID: "required-fields", MayDisable: true}, {RuleID: "required-fields", AllowedSeverities: []string{"info"}}, {RuleID: "required-fields", AllowedParameterKeys: []string{"executor"}}} {
		if err := ValidateBindingPolicies(template.DemoTemplate(), set, []BindingPolicy{policy}); err == nil {
			t.Fatalf("mandatory policy grant accepted: %+v", policy)
		}
	}
}
func TestOptionalReviewedSeverityCanRestoreWithinSourceCeiling(t *testing.T) {
	s, _, _, _, v, _ := seedService(t)
	_ = s
	policy := []BindingPolicy{{RuleID: "goal-present", AllowedSeverities: []string{"warning", "info"}}}
	if err := ValidateBindingPolicies(v.Definition, v.RuleSetSnapshot, policy); err != nil {
		t.Fatal(err)
	}
	low, err := ApplyBindingOverrides(v.RuleSetSnapshot, "project-effective", "1", policy, []BindingOverride{{RuleID: "goal-present", Severity: "info"}})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ApplyBindingOverrides(low, "project-effective", "2", policy, []BindingOverride{{RuleID: "goal-present", Severity: "warning"}})
	if err != nil || restored.Bindings[0].Severity != "warning" {
		t.Fatalf("reviewed inverse severity failed: %v", err)
	}
	if err := ValidateBindingPolicies(v.Definition, v.RuleSetSnapshot, []BindingPolicy{{RuleID: "goal-present", AllowedSeverities: []string{"blocking"}}}); err == nil {
		t.Fatal("policy raised source ceiling without source review")
	}
}
func TestBindingOverrideDeletesOnlyReviewedParametersAndRestores(t *testing.T) {
	base := template.RuleSet{ID: "parameter-source", Version: "1", State: template.RuleSetDraft, Bindings: []template.RuleBinding{{RuleID: "goal-size", DefinitionID: "computed/character_count", DefinitionVersion: "1", Origin: "organization", TargetKind: "field", TargetID: "research_goal", Parameters: map[string]any{"minimum": float64(1), "maximum": float64(100)}, Severity: template.SeverityWarning, Enabled: true, TriggerScenes: []string{"preflight"}, Owner: "curator"}}}
	base, err := template.PublishRuleSet(base)
	if err != nil {
		t.Fatal(err)
	}
	policies := []BindingPolicy{{RuleID: "goal-size", AllowedParameterKeys: []string{"minimum", "maximum"}}}
	effective, err := ApplyBindingOverrides(base, "project-size", "1", policies, []BindingOverride{{RuleID: "goal-size", RemoveParameterKeys: []string{"minimum"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := effective.Bindings[0].Parameters["minimum"]; exists {
		t.Fatal("removed parameter remained")
	}
	if _, exists := base.Bindings[0].Parameters["minimum"]; !exists {
		t.Fatal("source snapshot mutated")
	}
	restored, err := ApplyBindingOverrides(effective, "project-size", "2", policies, []BindingOverride{{RuleID: "goal-size", Parameters: map[string]any{"minimum": float64(1)}}})
	if err != nil || restored.Bindings[0].Parameters["minimum"] != float64(1) {
		t.Fatal("typed inverse did not restore parameter", err)
	}
	// The reviewed policy cannot make an invalid rule legal. After removing the
	// optional minimum, deleting the last maximum must still fail compilation.
	if _, err := ApplyBindingOverrides(effective, "project-size", "3", policies, []BindingOverride{{RuleID: "goal-size", RemoveParameterKeys: []string{"maximum"}}}); !errors.Is(err, template.ErrInvalidRuleSet) {
		t.Fatalf("removing the final valid count bound must be rejected: %v", err)
	}
	if _, exists := effective.Bindings[0].Parameters["maximum"]; !exists {
		t.Fatal("rejected override mutated the effective source snapshot")
	}
	for _, override := range []BindingOverride{{RuleID: "goal-size", RemoveParameterKeys: []string{"minimum", "minimum"}}, {RuleID: "goal-size", RemoveParameterKeys: []string{"executor"}}, {RuleID: "goal-size", RemoveParameterKeys: []string{"minimum"}, Parameters: map[string]any{"minimum": float64(2)}}} {
		if _, err := ApplyBindingOverrides(base, "project-size", "3", policies, []BindingOverride{override}); err == nil {
			t.Fatalf("invalid removal accepted %+v", override)
		}
	}
}
