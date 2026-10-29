package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	template "github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	"github.com/Tencent/WeKnora/internal/types"
)

func legacyOverrideProject(t *testing.T, path string) (*Service, Actor, Project, template.RuleSet) {
	t.Helper()
	svc := testStore(t, path)
	owner := Actor{TenantID: 9851, UserID: "legacy-override-owner", Role: types.TenantRoleOwner}
	seedTenantMember(t, svc, owner)
	set, err := template.PublishRuleSet(template.RuleSet{ID: "legacy-source", Version: "1", State: template.RuleSetDraft, Bindings: []template.RuleBinding{
		{RuleID: "goal-pattern", DefinitionID: "pattern", DefinitionVersion: "1", Origin: "organization", TargetKind: "field", TargetID: "research_goal", Parameters: map[string]any{"pattern": ".+"}, Severity: "warning", TriggerScenes: []string{"preflight"}, Enabled: true, Owner: "office"},
		{RuleID: "mandatory-goal", DefinitionID: "presence", DefinitionVersion: "1", Origin: "builtin", TargetKind: "field", TargetID: "research_goal", Severity: "blocking", TriggerScenes: []string{"preflight"}, Enabled: true, Owner: "office"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	definition := template.DemoTemplate()
	definition.Rules, err = template.CompileRuleSet(set)
	if err != nil {
		t.Fatal(err)
	}
	definition, err = template.WithHashes(definition)
	if err != nil {
		t.Fatal(err)
	}
	policies := []governance.BindingPolicy{{RuleID: "goal-pattern", MayDisable: true, AllowedTargetKinds: []string{"field"}, AllowedTargetIDs: []string{"research_goal", "research_subject"}, AllowedParameterKeys: []string{"pattern"}, AllowedSeverities: []string{"warning", "info"}}}
	policyHash, err := governance.HashBindingPolicies(policies)
	if err != nil {
		t.Fatal(err)
	}
	if err := governance.ValidateBindingPolicies(definition, set, policies); err != nil {
		t.Fatal(err)
	}
	svc.SetTemplateCatalogResolver(ruleSetSourcePolicyCatalog{value: CatalogTemplateDefinition{
		Definition: definition, RuleSetSnapshot: &set, SourceRuleSetID: set.ID, SourceRuleSetVersion: set.Version,
		SourceRuleSetHash: set.RulesetHash, BindingPolicies: policies, SourcePolicyHash: policyHash,
	}})
	created, _, _, err := svc.CreateProject(context.Background(), owner, "legacy-override-create", CreateProjectInput{Name: "legacy override fixture", TemplateID: template.DemoTemplateID})
	if err != nil {
		t.Fatal(err)
	}
	project := asProject(t, created)
	saved, _, _, err := svc.SaveSpec(context.Background(), owner, project.ID, "legacy-override-spec", SaveSpecInput{ExpectedSpecRevision: project.SpecRevision, Fields: map[string]string{"research_subject": "subject", "research_goal": "goal"}})
	if err != nil {
		t.Fatal(err)
	}
	project = asProject(t, saved)
	if !legacyProjectionMatches(project.TemplateCopy) {
		t.Fatal("fixture must start with a consistent legacy rule projection")
	}
	return svc, owner, project, set
}

func legacyOverrideInput(p Project, changes ...governance.BindingOverride) LegacyRuleOverridesInput {
	return LegacyRuleOverridesInput{ExpectedProjectVersion: p.ProjectVersion, ExpectedTemplateCopyVersion: p.TemplateCopyVersion, Reason: "reviewed project adjustment", Overrides: changes}
}

func TestLegacyRuleOverridesPersistSourceAcrossEditsAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-overrides.db")
	svc, owner, original, source := legacyOverrideProject(t, path)
	input := legacyOverrideInput(original, governance.BindingOverride{RuleID: "goal-pattern", Severity: "info", Parameters: map[string]any{"pattern": "^goal$"}})
	firstRaw, status, replayed, err := svc.ApplyLegacyRuleOverrides(context.Background(), owner, original.ID, "legacy-override-one", input)
	if err != nil || status != 200 || replayed {
		t.Fatalf("first override: status=%d replay=%v err=%v", status, replayed, err)
	}
	first := asProject(t, firstRaw)
	if first.TemplateCopyVersion != original.TemplateCopyVersion+1 || !legacyProjectionMatches(first.TemplateCopy) ||
		first.TemplateCopy.RulesetHash == source.RulesetHash || first.TemplateCopy.SourceRuleSetHash != source.RulesetHash {
		t.Fatalf("effective copy lost source/effective identity: %+v", first.TemplateCopy)
	}
	response, _, replayed, err := svc.ApplyLegacyRuleOverrides(context.Background(), owner, original.ID, "legacy-override-one", input)
	if err != nil || !replayed || string(response) != string(firstRaw) {
		t.Fatalf("idempotent retry did not replay: replay=%v err=%v", replayed, err)
	}
	if _, _, _, err := svc.ApplyLegacyRuleOverrides(context.Background(), owner, original.ID, "legacy-override-stale", input); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("old version accepted: %v", err)
	}
	// Reopen without any source catalog; inherited edits must validate against
	// the persisted original source, not the current downgraded effective set.
	svc = reopenSourcePolicyStore(t, svc, path)
	readback, err := svc.GetProject(context.Background(), owner, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if readback.TemplateCopy.legacySourceRuleSetSnapshot == nil || !reflect.DeepEqual(*readback.TemplateCopy.legacySourceRuleSetSnapshot, source) {
		t.Fatal("original source proof did not survive restart")
	}
	definition := template.Clone(readback.TemplateCopy.Definition)
	definition.Fields[0].Label += " revised"
	structural, _, _, err := svc.SaveTemplateCopy(context.Background(), owner, original.ID, "legacy-after-override-structure", TemplateCopyDefinitionInput{
		ExpectedProjectVersion: readback.ProjectVersion, ExpectedTemplateCopyVersion: readback.TemplateCopyVersion,
		Fields: definition.Fields, Sections: definition.Sections, Terms: append([]template.Term{}, definition.Terms...), RequiredFields: definition.RequiredFields, Rules: definition.Rules,
	})
	if err != nil {
		t.Fatalf("structural edit after downgrade: %v", err)
	}
	structured := asProject(t, structural)
	secondInput := legacyOverrideInput(structured, governance.BindingOverride{RuleID: "goal-pattern", Severity: "warning"})
	secondRaw, _, _, err := svc.ApplyLegacyRuleOverrides(context.Background(), owner, original.ID, "legacy-override-two", secondInput)
	if err != nil {
		t.Fatalf("restore reviewed source severity: %v", err)
	}
	second := asProject(t, secondRaw)
	if second.TemplateCopy.RuleSetSnapshot.Bindings[0].Severity != "warning" || !legacyProjectionMatches(second.TemplateCopy) || second.TemplateCopy.SourceRuleSetHash != source.RulesetHash {
		t.Fatalf("second override changed source or lost effective rules: %+v", second.TemplateCopy)
	}
	historical, err := svc.GetTemplateCopy(context.Background(), owner, original.ID, original.TemplateCopyVersion)
	if err != nil || historical.RuleSetSnapshot == nil || historical.RuleSetSnapshot.RulesetHash != source.RulesetHash {
		t.Fatalf("original history changed: %+v err=%v", historical, err)
	}
}

func TestLegacyRuleOverridesRejectUnsafeChangesWithoutWrites(t *testing.T) {
	svc, owner, project, _ := legacyOverrideProject(t, filepath.Join(t.TempDir(), "legacy-overrides-reject.db"))
	disabled := false
	cases := []struct {
		name  string
		input LegacyRuleOverridesInput
	}{
		{"unknown", legacyOverrideInput(project, governance.BindingOverride{RuleID: "other", Enabled: &disabled})},
		{"mandatory", legacyOverrideInput(project, governance.BindingOverride{RuleID: "mandatory-goal", Enabled: &disabled})},
		{"unapproved-parameter", legacyOverrideInput(project, governance.BindingOverride{RuleID: "goal-pattern", Parameters: map[string]any{"script": "bad"}})},
		{"unapproved-target", legacyOverrideInput(project, governance.BindingOverride{RuleID: "goal-pattern", TargetID: "other"})},
		{"unapproved-severity", legacyOverrideInput(project, governance.BindingOverride{RuleID: "goal-pattern", Severity: "blocking"})},
		{"duplicate", legacyOverrideInput(project, governance.BindingOverride{RuleID: "goal-pattern", Severity: "info"}, governance.BindingOverride{RuleID: "goal-pattern", Enabled: &disabled})},
		{"empty", legacyOverrideInput(project)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, err := svc.ApplyLegacyRuleOverrides(context.Background(), owner, project.ID, "reject-"+tc.name, tc.input); err == nil {
				t.Fatal("unsafe override accepted")
			}
		})
	}
	viewer := owner
	viewer.UserID, viewer.Role = "unauthorized-viewer", types.TenantRoleViewer
	if _, _, _, err := svc.ApplyLegacyRuleOverrides(context.Background(), viewer, project.ID, "reject-viewer", legacyOverrideInput(project, governance.BindingOverride{RuleID: "goal-pattern", Severity: "info"})); err == nil {
		t.Fatal("unauthorized override accepted")
	}
	after, err := svc.GetProject(context.Background(), owner, project.ID)
	if err != nil || after.ProjectVersion != project.ProjectVersion || after.TemplateCopyVersion != project.TemplateCopyVersion {
		t.Fatalf("rejected overrides changed project: %+v err=%v", after, err)
	}
	var copies, operations int64
	if err := svc.testDB().Model(&projectTemplateCopyRow{}).Where("project_id = ?", project.ID).Count(&copies).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Model(&operationRow{}).Where("key LIKE ?", "reject-%").Count(&operations).Error; err != nil {
		t.Fatal(err)
	}
	if copies != 1 || operations != 0 {
		t.Fatalf("rejected overrides wrote copies=%d operation records=%d", copies, operations)
	}
}

func TestLegacyRuleSourceEvidenceIsNotExposedInProjectJSON(t *testing.T) {
	copy := ProjectTemplateCopy{legacySourceRuleSetSnapshot: &template.RuleSet{ID: "not-client-input"}}
	raw, err := json.Marshal(copy)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	if _, ok := value["legacy_source_rule_set_snapshot"]; ok {
		t.Fatal("internal source proof leaked into project JSON")
	}
}

func seedLegacyEffectiveWithoutSourceProof(t *testing.T, svc *Service, owner Actor, original Project) Project {
	t.Helper()
	effective, err := governance.ApplyBindingOverrides(*original.TemplateCopy.RuleSetSnapshot, "project-"+original.ID,
		strconv.FormatInt(original.TemplateCopyVersion+1, 10), original.TemplateCopy.BindingPolicies,
		[]governance.BindingOverride{{RuleID: "goal-pattern", Parameters: map[string]any{"pattern": "^goal$"}}})
	if err != nil {
		t.Fatal(err)
	}
	definition := template.Clone(original.TemplateCopy.Definition)
	definition.Rules, err = template.CompileRuleSet(effective)
	if err != nil {
		t.Fatal(err)
	}
	definition, err = template.WithHashes(definition)
	if err != nil {
		t.Fatal(err)
	}
	copy, err := projectTemplateCopy(owner, original.ID, original.TemplateCopyVersion+1, definition, TemplateCopyDraft)
	if err != nil {
		t.Fatal(err)
	}
	copy.RuleSetSnapshot, copy.RulesetHash = &effective, effective.RulesetHash
	copy.SourceRuleSetID, copy.SourceRuleSetVersion, copy.SourceRuleSetHash = original.TemplateCopy.SourceRuleSetID, original.TemplateCopy.SourceRuleSetVersion, original.TemplateCopy.SourceRuleSetHash
	copy.SourcePolicyHash, copy.BindingPolicies = original.TemplateCopy.SourcePolicyHash, cloneRuleSetSourcePolicies(original.TemplateCopy.BindingPolicies)
	if err := svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		next := original
		next.ProjectVersion++
		next.TemplateCopyVersion, next.TemplateCopy = copy.Version, copy
		return tx.UpdateProject(owner.TenantID, original, next)
	}); err != nil {
		t.Fatal(err)
	}
	current, err := svc.GetProject(context.Background(), owner, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	return current
}

func TestLegacyOverrideRecoversOriginalFromHistoricalCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-source-history.db")
	svc, owner, original, source := legacyOverrideProject(t, path)
	current := seedLegacyEffectiveWithoutSourceProof(t, svc, owner, original)
	svc = reopenSourcePolicyStore(t, svc, path)
	if _, _, _, err := svc.ApplyLegacyRuleOverrides(context.Background(), owner, original.ID, "legacy-history-source", legacyOverrideInput(current, governance.BindingOverride{RuleID: "goal-pattern", Severity: "info"})); err != nil {
		t.Fatalf("could not recover historical source inside transaction: %v", err)
	}
	readback, err := svc.GetProject(context.Background(), owner, original.ID)
	if err != nil || readback.TemplateCopy.legacySourceRuleSetSnapshot == nil || !reflect.DeepEqual(*readback.TemplateCopy.legacySourceRuleSetSnapshot, source) {
		t.Fatalf("historical source was not stored with the new effective copy: %+v err=%v", readback.TemplateCopy, err)
	}
}

func TestLegacyOverrideWithoutOriginalHistoryFailsClosedWithoutBreakingOldCopy(t *testing.T) {
	svc, owner, original, _ := legacyOverrideProject(t, filepath.Join(t.TempDir(), "legacy-source-missing.db"))
	seedLegacyEffectiveWithoutSourceProof(t, svc, owner, original)
	// Simulate an older imported data set that retained only the effective copy.
	if err := svc.testDB().Where("id = ?", original.TemplateCopy.ID).Delete(&projectTemplateCopyRow{}).Error; err != nil {
		t.Fatal(err)
	}
	current, err := svc.GetProject(context.Background(), owner, original.ID)
	if err != nil {
		t.Fatalf("existing effective copy became unreadable: %v", err)
	}
	if _, _, _, err := svc.ApplyLegacyRuleOverrides(context.Background(), owner, original.ID, "missing-source", legacyOverrideInput(current, governance.BindingOverride{RuleID: "goal-pattern", Severity: "info"})); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("missing source proof should fail closed: %v", err)
	}
	definition := template.Clone(current.TemplateCopy.Definition)
	definition.Fields[0].Label += " still editable"
	if _, _, _, err := svc.SaveTemplateCopy(context.Background(), owner, original.ID, "missing-source-existing-edit", TemplateCopyDefinitionInput{
		ExpectedProjectVersion: current.ProjectVersion, ExpectedTemplateCopyVersion: current.TemplateCopyVersion,
		Fields: definition.Fields, Sections: definition.Sections, Terms: append([]template.Term{}, definition.Terms...), RequiredFields: definition.RequiredFields, Rules: definition.Rules,
	}); err != nil {
		t.Fatalf("existing structural edit acquired a new source dependency: %v", err)
	}
}
