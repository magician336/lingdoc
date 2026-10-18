package templategovernance

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
)

func TestUpgradePlanPreservesMigrationFailuresAndEmitsAllDomainFacts(t *testing.T) {
	s, _, auth, actor, draft, _ := seedService(t)
	target := publishTestVersion(t, s, actor, draft)
	oldDefinition := template.Clone(target.Definition)
	oldDefinition.Sections[0].Title = "Old method"
	oldDefinition.Fields[0].Label = "Old goal"
	oldSet := target.RuleSetSnapshot
	oldSet.Version = "old-1"
	oldSet.Bindings = append([]template.RuleBinding{}, oldSet.Bindings...)
	oldSet.Bindings[0].Message = "previous message"
	oldSet.State = template.RuleSetDraft
	oldSet.RulesetHash = ""
	oldSet, err := template.PublishRuleSet(oldSet)
	if err != nil {
		t.Fatal(err)
	}
	basis := auth.basis
	basis.RuleSet = RuleSetRef{ID: oldSet.ID, Version: oldSet.Version, Hash: oldSet.RulesetHash}
	input := UpgradeImpactInput{ProjectID: "project-1", SourceBasis: basis, SourceDefinition: oldDefinition, SourceRuleSet: oldSet, Target: target, ExpectedCurrentSnapshotID: basis.CurrentSnapshotID,
		FieldMigrations: []FieldMigrationFact{{FieldID: "research_goal", Status: "missing", Required: true}},
		Chapters:        []ChapterFact{{ID: "chapter-1", SectionID: "method", VersionID: "chapter-v1", ConfirmationValid: true}},
		Evidence:        []EvidenceFact{{Kind: "evidence_card", ID: "card-1", Version: "2", FieldIDs: []string{"research_goal"}}, {Kind: "research_claim", ID: "claim-1", Version: "3", RuleIDs: []string{"goal-present"}}},
		Issues:          []IssueFact{{ID: "old-issue", TargetVersion: "chapter-v1", RulesetHash: oldSet.RulesetHash}},
	}
	plan, err := BuildUpgradePlan(input)
	if err != nil {
		t.Fatal(err)
	}
	categories := map[string]bool{}
	for _, fact := range plan.Facts {
		categories[fact.Category] = true
	}
	for _, category := range []string{"field_migration", "chapter_structure", "ruleset", "evidence", "claim", "confirmation", "validation", "delivery"} {
		if !categories[category] {
			t.Fatalf("missing %s", category)
		}
	}
	if plan.PreviewHash == "" || len(plan.BlockingReasons) != 1 {
		t.Fatal("migration failure/hash missing")
	}
	if err := ValidateUpgradePlan(plan, basis, target); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("unresolved migration = %v", err)
	}
	input.FieldMigrations[0].Resolved = true
	input.FieldMigrations[0].Mapping = "explicit compatible value"
	plan, err = BuildUpgradePlan(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateUpgradePlan(plan, basis, target); err != nil {
		t.Fatal(err)
	}
	changed := basis
	changed.ProjectVersion++
	if err := ValidateUpgradePlan(plan, changed, target); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale project = %v", err)
	}
	plan.Facts[0].Required = false
	if err := ValidateUpgradePlan(plan, basis, target); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("tampered preview = %v", err)
	}
}
func TestUpgradePlanStableOrderingAndTargetPublicationGate(t *testing.T) {
	s, _, auth, actor, draft, _ := seedService(t)
	target := publishTestVersion(t, s, actor, draft)
	input := UpgradeImpactInput{ProjectID: "project-1", SourceBasis: auth.basis, SourceDefinition: target.Definition, SourceRuleSet: target.RuleSetSnapshot, Target: target, ExpectedCurrentSnapshotID: auth.basis.CurrentSnapshotID, FieldMigrations: []FieldMigrationFact{{FieldID: "research_goal", Status: "preserved"}, {FieldID: "new_field", Status: "added"}}}
	first, err := BuildUpgradePlan(input)
	if err != nil {
		t.Fatal(err)
	}
	input.FieldMigrations[0], input.FieldMigrations[1] = input.FieldMigrations[1], input.FieldMigrations[0]
	second, err := BuildUpgradePlan(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.PreviewHash != second.PreviewHash || !reflect.DeepEqual(first.Facts, second.Facts) {
		t.Fatal("input order changed canonical plan")
	}
	input.Target.State = template.RuleSetWithdrawn
	if _, err := BuildUpgradePlan(input); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("withdrawn target = %v", err)
	}
}

func TestBindingOverridesRequireExplicitPolicyAndPreserveMandatoryRules(t *testing.T) {
	base, err := template.PublishRuleSet(template.RuleSet{ID: "base-rules", Version: "1", State: template.RuleSetDraft, Bindings: []template.RuleBinding{
		{RuleID: "source-available", DefinitionID: "computed/source_available", DefinitionVersion: "1", Origin: "builtin", Severity: template.SeverityBlocking, TriggerScenes: []string{"write_check", "preflight"}, Enabled: true, Owner: "system"},
		{RuleID: "goal-pattern", DefinitionID: "pattern", DefinitionVersion: "1", Origin: "organization", TargetKind: "field", TargetID: "research_goal", Parameters: map[string]any{"pattern": "^Study"}, Severity: template.SeverityWarning, TriggerScenes: []string{"write_check", "preflight"}, Enabled: true, Owner: "rules-admin"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	off := false
	if _, err := ApplyBindingOverrides(base, "project-rules", "2", nil, []BindingOverride{{RuleID: "goal-pattern", Enabled: &off}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("implicit disable policy accepted")
	}
	policy := []BindingPolicy{{RuleID: "source-available", MayDisable: true, AllowedSeverities: []string{template.SeverityInfo}}, {RuleID: "goal-pattern", MayDisable: true, AllowedParameterKeys: []string{"pattern"}, AllowedSeverities: []string{template.SeverityInfo}}}
	if _, err := ApplyBindingOverrides(base, "project-rules", "2", policy, []BindingOverride{{RuleID: "source-available", Enabled: &off}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("mandatory source gate disabled")
	}
	effective, err := ApplyBindingOverrides(base, "project-rules", "2", policy, []BindingOverride{{RuleID: "goal-pattern", Parameters: map[string]any{"pattern": "^Investigate"}, Severity: template.SeverityInfo}})
	if err != nil {
		t.Fatal(err)
	}
	if effective.RulesetHash == base.RulesetHash || base.Bindings[1].Parameters["pattern"] != "^Study" || effective.Bindings[0].Severity != template.SeverityBlocking {
		t.Fatal("override changed source snapshot or mandatory severity")
	}
	if _, err := ApplyBindingOverrides(base, "project-rules", "2", policy, []BindingOverride{{RuleID: "goal-pattern", Parameters: map[string]any{"script": "unsafe"}}}); err == nil {
		t.Fatal("arbitrary parameter accepted")
	}
}
