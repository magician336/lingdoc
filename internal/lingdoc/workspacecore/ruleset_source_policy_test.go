package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	"github.com/Tencent/WeKnora/internal/types"
)

type ruleSetSourcePolicyCatalog struct{ value CatalogTemplateDefinition }

func (r ruleSetSourcePolicyCatalog) Resolve(context.Context, Transaction, Actor, string, string) (CatalogTemplateDefinition, error) {
	return r.value, nil
}
func (ruleSetSourcePolicyCatalog) RecordCopySuccess(context.Context, Transaction, Actor, TemplateCopyAudit) error {
	return nil
}
func (ruleSetSourcePolicyCatalog) RecordCopyFailure(context.Context, Actor, TemplateCopyFailure) error {
	return nil
}

type resolverWithoutCopyAudit struct{ value CatalogTemplateDefinition }

func (r resolverWithoutCopyAudit) Resolve(context.Context, Transaction, Actor, string, string) (CatalogTemplateDefinition, error) {
	return r.value, nil
}

func TestCreateProjectFailsClosedWhenCatalogCopyAuditAdapterIsMissing(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "missing-copy-audit.db"))
	actor := Actor{TenantID: 732, UserID: "copy-audit-owner", Role: types.TenantRoleOwner}
	seedTenantMember(t, svc, actor)
	svc.SetTemplateCatalogResolver(resolverWithoutCopyAudit{value: CatalogTemplateDefinition{Definition: lingdoctemplate.DemoTemplate()}})

	_, _, _, err := svc.CreateProject(context.Background(), actor, "missing-copy-audit", CreateProjectInput{Name: "must roll back", TemplateID: lingdoctemplate.DemoTemplateID})
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("catalog copy without audit adapter = %v, want fail-closed ErrInvalidState", err)
	}
	var projects int64
	if err := svc.testDB().Model(&projectRow{}).Where("tenant_id = ? AND name = ?", actor.TenantID, "must roll back").Count(&projects).Error; err != nil {
		t.Fatal(err)
	}
	if projects != 0 {
		t.Fatalf("project was persisted without its required copy audit: count=%d", projects)
	}
}

// testStore installs the complete production SQLite migration scripts. These
// regressions use the real project-copy rows and lingdoc_g5_records sidecars.
func sourcePolicyProject(t *testing.T, path string) (*Service, Actor, Project, lingdoctemplate.RuleSet) {
	t.Helper()
	svc := testStore(t, path)
	owner := Actor{TenantID: 731, UserID: "source-policy-owner", Role: types.TenantRoleOwner}
	seedTenantMember(t, svc, owner)
	set := publishedWorkspaceRuleSet(t)
	legacy := lingdoctemplate.Clone(lingdoctemplate.Template{RuleSetSnapshot: &set}).RuleSetSnapshot
	legacy.ID, legacy.Version = "catalog-rules", "v0"
	legacy.State, legacy.RulesetHash = lingdoctemplate.RuleSetDraft, ""
	legacySet, err := lingdoctemplate.PublishRuleSet(*legacy)
	if err != nil {
		t.Fatal(err)
	}
	policies := []governance.BindingPolicy{{RuleID: "goal-required", MayDisable: true,
		AllowedTargetKinds: []string{"field"}, AllowedTargetIDs: []string{"research_goal", "research_subject"},
		AllowedSeverities: []string{"warning", "info"}}}
	policyHash, err := governance.HashBindingPolicies(policies)
	if err != nil {
		t.Fatal(err)
	}
	if err := governance.ValidateBindingPolicies(lingdoctemplate.DemoTemplate(), legacySet, policies); err != nil {
		t.Fatalf("source policy fixture: %v", err)
	}
	svc.SetTemplateCatalogResolver(ruleSetSourcePolicyCatalog{value: CatalogTemplateDefinition{
		Definition: lingdoctemplate.DemoTemplate(), BindingPolicies: policies, SourcePolicyHash: policyHash,
		RuleSetSnapshot: &legacySet, SourceRuleSetID: legacySet.ID, SourceRuleSetVersion: legacySet.Version, SourceRuleSetHash: legacySet.RulesetHash,
	}})
	raw, status, replay, err := svc.CreateProject(context.Background(), owner, "policy-create-project", CreateProjectInput{
		Name: "RuleSet source policy persistence", TemplateID: lingdoctemplate.DemoTemplateID,
	})
	if err != nil || status != 201 || replay {
		t.Fatalf("create policy-bearing project: status=%d replay=%v err=%v", status, replay, err)
	}
	project := asProject(t, raw)
	assertSourcePolicySidecar(t, svc, project.TemplateCopy)
	// The tests below exercise binding and source-policy history, so give the
	// draft the required values before binding its next template-copy version.
	raw, _, _, err = svc.SaveSpec(context.Background(), owner, project.ID, "policy-fixture-required", SaveSpecInput{
		ExpectedSpecRevision: project.SpecRevision,
		Fields:               map[string]string{"research_subject": "policy fixture subject", "research_goal": "policy fixture goal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	project = asProject(t, raw)
	return svc, owner, project, set
}

func sourcePolicySidecarBytes(t *testing.T, svc *Service, copy *ProjectTemplateCopy) string {
	t.Helper()
	var row projectRecordRow
	if err := svc.testDB().Where("project_id = ? AND kind = ? AND id = ?", copy.ProjectID, "template_binding", copy.ID).Take(&row).Error; err != nil {
		t.Fatalf("persisted binding sidecar for copy %d: %v", copy.Version, err)
	}
	if row.Version != 1 {
		t.Fatalf("immutable sidecar version = %d, want 1", row.Version)
	}
	return row.PayloadJSON
}

func assertSourcePolicySidecar(t *testing.T, svc *Service, copy *ProjectTemplateCopy) {
	t.Helper()
	var stored ProjectTemplateCopy
	if err := json.Unmarshal([]byte(sourcePolicySidecarBytes(t, svc, copy)), &stored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored.BindingPolicies, copy.BindingPolicies) || stored.SourcePolicyHash != copy.SourcePolicyHash ||
		!reflect.DeepEqual(stored.RuleSetSnapshot, copy.RuleSetSnapshot) || stored.SourceRuleSetID != copy.SourceRuleSetID ||
		stored.SourceRuleSetVersion != copy.SourceRuleSetVersion || stored.SourceRuleSetHash != copy.SourceRuleSetHash {
		t.Fatalf("persisted source metadata differs from the returned copy: stored=%+v copy=%+v", stored, copy)
	}
}

func assertInheritedCanonicalPolicy(t *testing.T, previous, next *ProjectTemplateCopy, set lingdoctemplate.RuleSet) {
	t.Helper()
	if previous == nil || next == nil || previous.SourcePolicyHash == "" || len(previous.BindingPolicies) == 0 {
		t.Fatal("test requires two copies and a nonempty reviewed source policy")
	}
	if !reflect.DeepEqual(previous.BindingPolicies, next.BindingPolicies) || previous.SourcePolicyHash != next.SourcePolicyHash {
		t.Fatalf("reviewed policy identity was lost or changed: previous=%+v next=%+v", previous, next)
	}
	wantRef := &lingdoctemplate.RuleSetReference{ID: set.ID, Version: set.Version, Hash: set.RulesetHash}
	if !reflect.DeepEqual(next.TemplateVersionRuleSetRef, wantRef) || !reflect.DeepEqual(lingdoctemplate.RuleSetReferenceFor(next.Definition), wantRef) ||
		!reflect.DeepEqual(next.Definition.RuleSetSnapshot, &set) || !reflect.DeepEqual(next.RuleSetSnapshot, &set) ||
		next.SourceRuleSetID != set.ID || next.SourceRuleSetVersion != set.Version || next.SourceRuleSetHash != set.RulesetHash {
		t.Fatalf("source sidecar does not match the exact canonical RuleSet: %+v", next)
	}
	_, effectiveHash, err := lingdoctemplate.Hashes(next.Definition)
	if err != nil || next.RulesetHash != effectiveHash || next.RulesetHash != next.Definition.RulesetHash || next.RulesetHash == set.RulesetHash {
		t.Fatalf("effective template hash was replaced by the source hash: copy=%s effective=%s source=%s err=%v", next.RulesetHash, effectiveHash, set.RulesetHash, err)
	}
	if err := validateProjectTemplateCopy(next); err != nil {
		t.Fatalf("canonical copy with source policy cannot be read: %v", err)
	}
}

func reopenSourcePolicyStore(t *testing.T, svc *Service, path string) *Service {
	t.Helper()
	db, err := svc.testDB().DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return testStore(t, path)
}

func assertHistoricalSourcePolicy(t *testing.T, svc *Service, owner Actor, original *ProjectTemplateCopy, originalSidecar string) {
	t.Helper()
	copy, err := svc.GetTemplateCopy(context.Background(), owner, original.ProjectID, original.Version)
	if err != nil {
		t.Fatalf("read historical copy: %v", err)
	}
	if copy.ID != original.ID || copy.ContentHash != original.ContentHash || copy.RulesetHash != original.RulesetHash ||
		!reflect.DeepEqual(copy.Definition, original.Definition) || !reflect.DeepEqual(copy.TemplateVersionRuleSetRef, original.TemplateVersionRuleSetRef) ||
		!reflect.DeepEqual(copy.BindingPolicies, original.BindingPolicies) || copy.SourcePolicyHash != original.SourcePolicyHash ||
		!reflect.DeepEqual(copy.RuleSetSnapshot, original.RuleSetSnapshot) || copy.SourceRuleSetID != original.SourceRuleSetID ||
		copy.SourceRuleSetVersion != original.SourceRuleSetVersion || copy.SourceRuleSetHash != original.SourceRuleSetHash ||
		sourcePolicySidecarBytes(t, svc, &copy) != originalSidecar {
		t.Fatalf("historical source policy or immutable template data changed: original=%+v stored=%+v", original, copy)
	}
}

func TestBindRuleSetPersistsSourcePolicyAndCanonicalSidecar(t *testing.T) {
	for _, overridden := range []bool{false, true} {
		name := "bind"
		if overridden {
			name = "nonempty_override"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "policy-bind.db")
			svc, owner, project, set := sourcePolicyProject(t, path)
			original := project.TemplateCopy
			oldSidecar := sourcePolicySidecarBytes(t, svc, original)
			svc.WithRuleSetSnapshotReader(&ruleSetSnapshotReaderStub{set: set})
			input := BindTemplateCopyRuleSetInput{ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
				RuleSetRef: &lingdoctemplate.RuleSetReference{ID: set.ID, Version: set.Version, Hash: set.RulesetHash}}
			if overridden {
				disabled := false
				input.Overrides = []lingdoctemplate.RuleBindingOverride{{RuleID: "goal-required", Enabled: &disabled}}
			}
			raw, status, replay, err := svc.BindTemplateCopyRuleSet(context.Background(), owner, project.ID, "policy-bind-ruleset", input)
			if err != nil || status != 200 || replay {
				t.Fatalf("bind: status=%d replay=%v err=%v", status, replay, err)
			}
			updated := asProject(t, raw)
			if updated.TemplateCopyVersion != original.Version+1 {
				t.Fatalf("copy version = %d", updated.TemplateCopyVersion)
			}
			assertInheritedCanonicalPolicy(t, original, updated.TemplateCopy, set)
			if !reflect.DeepEqual(updated.TemplateCopy.Definition.RuleBindingOverrides, input.Overrides) || !updated.TemplateCopy.RuleSetSnapshot.Bindings[0].Enabled {
				t.Fatal("override was lost or mutated the immutable source snapshot")
			}
			assertSourcePolicySidecar(t, svc, updated.TemplateCopy)
			assertHistoricalSourcePolicy(t, svc, owner, original, oldSidecar)
			reopened := reopenSourcePolicyStore(t, svc, path)
			persisted, err := reopened.GetProject(context.Background(), owner, project.ID)
			if err != nil {
				t.Fatalf("reopen bound project: %v", err)
			}
			assertInheritedCanonicalPolicy(t, original, persisted.TemplateCopy, set)
			if !reflect.DeepEqual(persisted.TemplateCopy.Definition.RuleBindingOverrides, input.Overrides) {
				t.Fatal("nonempty override did not survive reopening")
			}
			assertSourcePolicySidecar(t, reopened, persisted.TemplateCopy)
			assertHistoricalSourcePolicy(t, reopened, owner, original, oldSidecar)
		})
	}
}

func TestSaveTemplateCopyPersistsLegacySourcePolicyAndHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy-save-template.db")
	svc, owner, project, _ := sourcePolicyProject(t, path)
	// Populate the existing required fields before editing the copy; unresolved
	// migration risks now block the write, including unrelated structural edits.
	filled, _, _, err := svc.SaveSpec(context.Background(), owner, project.ID, "policy-required-spec", SaveSpecInput{
		ExpectedSpecRevision: project.SpecRevision,
		Fields:               map[string]string{"research_subject": "Synthetic subject", "research_goal": "Synthetic goal"},
	})
	if err != nil {
		t.Fatalf("prepare required fields before editing: %v", err)
	}
	project = asProject(t, filled)
	original := project.TemplateCopy
	oldSidecar := sourcePolicySidecarBytes(t, svc, original)
	definition := lingdoctemplate.Clone(original.Definition)
	definition.Fields[0].Label += " (reviewed label)"
	raw, status, replay, err := svc.SaveTemplateCopy(context.Background(), owner, project.ID, "policy-edit-template-label", TemplateCopyDefinitionInput{
		ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
		Fields: definition.Fields, Sections: definition.Sections, Terms: append([]lingdoctemplate.Term{}, definition.Terms...), Rules: definition.Rules, RequiredFields: definition.RequiredFields,
	})
	if err != nil || status != 200 || replay {
		t.Fatalf("save legal template edit: status=%d replay=%v err=%v", status, replay, err)
	}
	assertSaved := func(copy *ProjectTemplateCopy) {
		t.Helper()
		if copy == nil || copy.Version != original.Version+1 || copy.Definition.Fields[0].Label != definition.Fields[0].Label ||
			copy.ContentHash == original.ContentHash || copy.Definition.RuleSetSnapshot != nil || copy.TemplateVersionRuleSetRef != nil ||
			copy.RulesetHash != original.RulesetHash || !reflect.DeepEqual(copy.BindingPolicies, original.BindingPolicies) ||
			copy.SourcePolicyHash != original.SourcePolicyHash || !reflect.DeepEqual(copy.RuleSetSnapshot, original.RuleSetSnapshot) ||
			copy.SourceRuleSetID != original.SourceRuleSetID || copy.SourceRuleSetVersion != original.SourceRuleSetVersion || copy.SourceRuleSetHash != original.SourceRuleSetHash {
			t.Fatalf("legal structural edit lost legacy source metadata: %+v", copy)
		}
		if err := validateProjectTemplateCopy(copy); err != nil {
			t.Fatalf("saved legacy copy cannot be read: %v", err)
		}
	}
	updated := asProject(t, raw)
	assertSaved(updated.TemplateCopy)
	assertSourcePolicySidecar(t, svc, updated.TemplateCopy)
	assertHistoricalSourcePolicy(t, svc, owner, original, oldSidecar)
	reopened := reopenSourcePolicyStore(t, svc, path)
	persisted, err := reopened.GetProject(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatalf("reopen saved template: %v", err)
	}
	assertSaved(persisted.TemplateCopy)
	assertSourcePolicySidecar(t, reopened, persisted.TemplateCopy)
	assertHistoricalSourcePolicy(t, reopened, owner, original, oldSidecar)
}

func TestActiveRuleSetUpgradePersistsSourcePolicyAndExactHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy-upgrade.db")
	svc, owner, project, v1 := sourcePolicyProject(t, path)
	reader := &ruleSetSnapshotReaderStub{set: v1}
	svc.WithRuleSetSnapshotReader(reader)
	raw, _, _, err := svc.BindTemplateCopyRuleSet(context.Background(), owner, project.ID, "policy-active-bind", BindTemplateCopyRuleSetInput{
		ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
		RuleSetRef: &lingdoctemplate.RuleSetReference{ID: v1.ID, Version: v1.Version, Hash: v1.RulesetHash},
	})
	if err != nil {
		t.Fatal(err)
	}
	project = asProject(t, raw)
	raw, _, _, err = svc.SaveSpec(context.Background(), owner, project.ID, "policy-active-spec", SaveSpecInput{
		ExpectedSpecRevision: project.SpecRevision, Fields: map[string]string{"research_subject": "Synthetic subject", "research_goal": "Synthetic goal"},
	})
	if err != nil {
		t.Fatal(err)
	}
	project = asProject(t, raw)
	raw, _, _, err = svc.ActivateProject(context.Background(), owner, project.ID, "policy-activate-project", ActivateProjectInput{
		ExpectedSpecRevision: project.SpecRevision, ExpectedProjectVersion: project.ProjectVersion, ReviewedProjectVersion: project.ProjectVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	project = asProject(t, raw)
	original := project.TemplateCopy
	oldSidecar := sourcePolicySidecarBytes(t, svc, original)
	draft := lingdoctemplate.Clone(lingdoctemplate.Template{RuleSetSnapshot: &v1}).RuleSetSnapshot
	draft.Version, draft.State, draft.RulesetHash = "v2", lingdoctemplate.RuleSetDraft, ""
	draft.Bindings[0].Message = "Updated source rule message"
	v2, err := lingdoctemplate.PublishRuleSet(*draft)
	if err != nil {
		t.Fatal(err)
	}
	reader.set = v2
	disabled := false
	definition := lingdoctemplate.Clone(project.TemplateCopy.Definition)
	upgrade := &TemplateUpgradeInput{ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
		Fields: definition.Fields, Sections: definition.Sections, Terms: append([]lingdoctemplate.Term{}, definition.Terms...), Rules: definition.Rules, RequiredFields: definition.RequiredFields,
		RuleSetRef:           &lingdoctemplate.RuleSetReference{ID: v2.ID, Version: v2.Version, Hash: v2.RulesetHash},
		RuleBindingOverrides: []lingdoctemplate.RuleBindingOverride{{RuleID: "goal-required", Enabled: &disabled}},
	}
	created, status, replay, err := svc.CreateChangeSet(context.Background(), owner, project.ID, "policy-upgrade-assess", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision, Reason: "Adopt the exact reviewed RuleSet version", TemplateUpgrade: upgrade,
	})
	if err != nil || status != 201 || replay {
		t.Fatalf("assess upgrade: status=%d replay=%v err=%v", status, replay, err)
	}
	var change ChangeSet
	if err := json.Unmarshal(created, &change); err != nil || change.TemplateUpgrade == nil {
		t.Fatalf("upgrade payload: %+v err=%v", change, err)
	}
	if _, status, replay, err := svc.ApplyChangeSet(context.Background(), owner, project.ID, change.ID, "policy-upgrade-apply"); err != nil || status != 200 || replay {
		t.Fatalf("apply upgrade: status=%d replay=%v err=%v", status, replay, err)
	}
	updated, err := svc.GetProject(context.Background(), owner, project.ID)
	if err != nil || updated.TemplateCopyVersion != original.Version+1 || updated.TemplateCopy == nil || updated.TemplateCopy.Status != TemplateCopyBound {
		t.Fatalf("upgraded copy: %+v err=%v", updated.TemplateCopy, err)
	}
	assertInheritedCanonicalPolicy(t, original, updated.TemplateCopy, v2)
	assertSourcePolicySidecar(t, svc, updated.TemplateCopy)
	assertHistoricalSourcePolicy(t, svc, owner, original, oldSidecar)
	reopened := reopenSourcePolicyStore(t, svc, path)
	persisted, err := reopened.GetProject(context.Background(), owner, project.ID)
	if err != nil {
		t.Fatalf("reopen upgraded project: %v", err)
	}
	assertInheritedCanonicalPolicy(t, original, persisted.TemplateCopy, v2)
	if !reflect.DeepEqual(persisted.TemplateCopy.Definition.RuleBindingOverrides, upgrade.RuleBindingOverrides) {
		t.Fatal("upgrade overrides did not survive reopening")
	}
	assertSourcePolicySidecar(t, reopened, persisted.TemplateCopy)
	assertHistoricalSourcePolicy(t, reopened, owner, original, oldSidecar)
}

func TestBindRuleSetRejectsIncompatibleSourcePolicyWithoutWrites(t *testing.T) {
	svc, owner, project, set := sourcePolicyProject(t, filepath.Join(t.TempDir(), "policy-reject.db"))
	original := project.TemplateCopy
	oldSidecar := sourcePolicySidecarBytes(t, svc, original)
	draft := lingdoctemplate.Clone(lingdoctemplate.Template{RuleSetSnapshot: &set}).RuleSetSnapshot
	draft.Bindings[0].RuleID = "renamed-rule"
	draft.State, draft.RulesetHash = lingdoctemplate.RuleSetDraft, ""
	changed, err := lingdoctemplate.PublishRuleSet(*draft)
	if err != nil {
		t.Fatal(err)
	}
	svc.WithRuleSetSnapshotReader(&ruleSetSnapshotReaderStub{set: changed})
	const key = "policy-rejected-binding"
	if _, _, replay, err := svc.BindTemplateCopyRuleSet(context.Background(), owner, project.ID, key, BindTemplateCopyRuleSetInput{
		ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
		RuleSetRef: &lingdoctemplate.RuleSetReference{ID: changed.ID, Version: changed.Version, Hash: changed.RulesetHash},
	}); !errors.Is(err, ErrInvalidRequest) || replay {
		t.Fatalf("incompatible policy: replay=%v err=%v", replay, err)
	}
	persisted, err := svc.GetProject(context.Background(), owner, project.ID)
	if err != nil || persisted.ProjectVersion != project.ProjectVersion || persisted.TemplateCopyVersion != project.TemplateCopyVersion {
		t.Fatalf("rejected binding changed project: %+v err=%v", persisted, err)
	}
	var copies, sidecars, operations int64
	if err := svc.testDB().Model(&projectTemplateCopyRow{}).Where("project_id = ?", project.ID).Count(&copies).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Model(&projectRecordRow{}).Where("project_id = ? AND kind = ?", project.ID, "template_binding").Count(&sidecars).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.testDB().Model(&operationRow{}).Where("key = ?", key).Count(&operations).Error; err != nil {
		t.Fatal(err)
	}
	if copies != 1 || sidecars != 1 || operations != 0 {
		t.Fatalf("rejected binding left writes: copies=%d sidecars=%d operations=%d", copies, sidecars, operations)
	}
	assertHistoricalSourcePolicy(t, svc, owner, original, oldSidecar)
}

func TestInheritedRuleSetPolicyRejectsRemovedStructuralTarget(t *testing.T) {
	svc, _, project, set := sourcePolicyProject(t, filepath.Join(t.TempDir(), "policy-removed-field.db"))
	originalSidecar := sourcePolicySidecarBytes(t, svc, project.TemplateCopy)
	definition := lingdoctemplate.Clone(project.TemplateCopy.Definition)
	definition.RuleSetSnapshot = &set
	definition.RuleSetRef = &lingdoctemplate.RuleSetReference{ID: set.ID, Version: set.Version, Hash: set.RulesetHash}
	for i, field := range definition.Fields {
		if field.ID == "research_subject" {
			definition.Fields = append(definition.Fields[:i], definition.Fields[i+1:]...)
			break
		}
	}
	for i, id := range definition.RequiredFields {
		if id == "research_subject" {
			definition.RequiredFields = append(definition.RequiredFields[:i], definition.RequiredFields[i+1:]...)
			break
		}
	}
	next := &ProjectTemplateCopy{Definition: definition}
	if err := inheritRuleSetSourcePolicy(project.TemplateCopy, next); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("removed policy target accepted: %v", err)
	}
	if next.SourcePolicyHash != "" || next.RuleSetSnapshot != nil || next.BindingPolicies != nil ||
		sourcePolicySidecarBytes(t, svc, project.TemplateCopy) != originalSidecar {
		t.Fatal("failed inheritance mutated either copy's source metadata")
	}
}

func TestCanonicalRuleSetCopyRejectsPersistedSourceTampering(t *testing.T) {
	for _, name := range []string{"legacy_source_id", "legacy_source_version", "legacy_source_hash", "legacy_snapshot", "effective_hash"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "policy-tamper.db")
			svc, owner, project, set := sourcePolicyProject(t, path)
			svc.WithRuleSetSnapshotReader(&ruleSetSnapshotReaderStub{set: set})
			raw, _, _, err := svc.BindTemplateCopyRuleSet(context.Background(), owner, project.ID, "policy-tamper-bind", BindTemplateCopyRuleSetInput{
				ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
				RuleSetRef: &lingdoctemplate.RuleSetReference{ID: set.ID, Version: set.Version, Hash: set.RulesetHash},
			})
			if err != nil {
				t.Fatal(err)
			}
			bound := asProject(t, raw)
			assertInheritedCanonicalPolicy(t, project.TemplateCopy, bound.TemplateCopy, set)
			if _, err := svc.GetProject(context.Background(), owner, project.ID); err != nil {
				t.Fatalf("untampered canonical copy is unreadable: %v", err)
			}
			if name == "effective_hash" {
				// A valid source hash still cannot replace the compiled hash.
				result := svc.testDB().Model(&projectTemplateCopyRow{}).Where("project_id = ? AND id = ?", project.ID, bound.TemplateCopy.ID).Update("ruleset_hash", set.RulesetHash)
				if result.Error != nil || result.RowsAffected != 1 {
					t.Fatalf("persist effective-hash tamper: rows=%d err=%v", result.RowsAffected, result.Error)
				}
			} else {
				var stored ProjectTemplateCopy
				if err := json.Unmarshal([]byte(sourcePolicySidecarBytes(t, svc, bound.TemplateCopy)), &stored); err != nil {
					t.Fatal(err)
				}
				switch name {
				case "legacy_source_id":
					stored.SourceRuleSetID = "different-source"
				case "legacy_source_version":
					stored.SourceRuleSetVersion = "different-version"
				case "legacy_source_hash":
					stored.SourceRuleSetHash = "0" + set.RulesetHash[1:]
					if stored.SourceRuleSetHash == set.RulesetHash {
						stored.SourceRuleSetHash = "1" + set.RulesetHash[1:]
					}
				case "legacy_snapshot":
					draft := lingdoctemplate.Clone(lingdoctemplate.Template{RuleSetSnapshot: &set}).RuleSetSnapshot
					draft.Bindings[0].Message = "Different valid published source"
					draft.State, draft.RulesetHash = lingdoctemplate.RuleSetDraft, ""
					changed, err := lingdoctemplate.PublishRuleSet(*draft)
					if err != nil {
						t.Fatal(err)
					}
					stored.RuleSetSnapshot = &changed
				}
				payload, err := json.Marshal(stored)
				if err != nil {
					t.Fatal(err)
				}
				result := svc.testDB().Model(&projectRecordRow{}).Where("project_id = ? AND kind = ? AND id = ?", project.ID, "template_binding", bound.TemplateCopy.ID).Update("payload_json", string(payload))
				if result.Error != nil || result.RowsAffected != 1 {
					t.Fatalf("persist source-sidecar tamper: rows=%d err=%v", result.RowsAffected, result.Error)
				}
			}
			reopened := reopenSourcePolicyStore(t, svc, path)
			if _, err := reopened.GetProject(context.Background(), owner, project.ID); !errors.Is(err, ErrInvalidState) {
				t.Fatalf("persisted %s tamper accepted after reopen: %v", name, err)
			}
			if _, err := reopened.GetTemplateCopy(context.Background(), owner, project.ID, bound.TemplateCopyVersion); !errors.Is(err, ErrInvalidState) {
				t.Fatalf("historical read accepted persisted %s tamper: %v", name, err)
			}
		})
	}
}

func TestRuleSetSourcePoliciesDeepCloneNestedBounds(t *testing.T) {
	minimum, maximum := 1.0, 20.0
	source := []governance.BindingPolicy{{RuleID: "test-rule", AllowedTargetKinds: []string{"field"}, AllowedTargetIDs: []string{"research_goal"},
		AllowedParameterKeys: []string{"minimum"}, AllowedSeverities: []string{"warning"},
		ParameterBounds: map[string]governance.ParameterBounds{"minimum": {Minimum: &minimum, Maximum: &maximum}},
	}}
	cloned := cloneRuleSetSourcePolicies(source)
	cloned[0].AllowedTargetKinds[0] = "section"
	cloned[0].AllowedTargetIDs[0] = "question"
	cloned[0].AllowedParameterKeys[0] = "changed"
	cloned[0].AllowedSeverities[0] = "info"
	*cloned[0].ParameterBounds["minimum"].Minimum = 5
	*cloned[0].ParameterBounds["minimum"].Maximum = 10
	delete(cloned[0].ParameterBounds, "minimum")
	if source[0].AllowedTargetKinds[0] != "field" || source[0].AllowedTargetIDs[0] != "research_goal" ||
		source[0].AllowedParameterKeys[0] != "minimum" || source[0].AllowedSeverities[0] != "warning" ||
		*source[0].ParameterBounds["minimum"].Minimum != 1 || *source[0].ParameterBounds["minimum"].Maximum != 20 {
		t.Fatal("nested policy data aliases the previous copy")
	}
}
