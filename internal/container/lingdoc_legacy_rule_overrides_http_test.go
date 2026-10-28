package container

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	template "github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/gin-gonic/gin"
)

// The initial SQL seed represents a historical sidecar created by an older
// catalog path. All operations under test use the production HTTP handler,
// application service and SQLite transaction, including a service restart.
func TestLegacyRuleOverrideHTTPPersistsAndRollsBackSQLite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	path := filepath.Join(t.TempDir(), "legacy-rule-override-http.sqlite")
	db := openRuleSetSQLite(t, path)
	applyRuleSetSQLiteSchema(t, db)
	fixture := buildRuleSetHTTPFixture(t, db)
	request := func(router *gin.Engine, method, endpoint, key string, body any, want int) ruleSetHTTPEnvelope {
		t.Helper()
		var data []byte
		if body != nil {
			var err error
			data, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
		req := httptest.NewRequest(method, "/api/v1/lingdoc"+endpoint, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, req)
		if response.Code != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, endpoint, response.Code, want, response.Body.String())
		}
		var envelope ruleSetHTTPEnvelope
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope
	}
	decodeProject := func(envelope ruleSetHTTPEnvelope) core.Project {
		t.Helper()
		var project core.Project
		if err := json.Unmarshal(envelope.Data, &project); err != nil {
			t.Fatal(err)
		}
		return project
	}
	created := request(fixture.router, http.MethodPost, "/projects", "legacy-http-create", map[string]any{"name": "Legacy HTTP fixture", "template_id": template.DemoTemplateID}, http.StatusCreated)
	project := decodeProject(created)
	setDraft := template.RuleSet{ID: "legacy-http-rules", Version: "1", State: template.RuleSetDraft, Bindings: []template.RuleBinding{
		{RuleID: "goal-pattern", DefinitionID: "pattern", DefinitionVersion: "1", Origin: "organization", TargetKind: "field", TargetID: "research_goal", Parameters: map[string]any{"pattern": ".+"}, Severity: "warning", TriggerScenes: []string{"preflight"}, Enabled: true, Owner: "office"},
	}}
	actor := rulesetstore.Actor{TenantID: ruleSetTenantID, UserID: ruleSetActorID, Role: "admin"}
	if err := fixture.ruleSetStore.CreateDraft(context.Background(), actor, setDraft); err != nil {
		t.Fatal(err)
	}
	set, err := fixture.ruleSetStore.Publish(context.Background(), actor, setDraft.ID, setDraft.Version)
	if err != nil {
		t.Fatal(err)
	}
	policies := []governance.BindingPolicy{{RuleID: "goal-pattern", MayDisable: true, AllowedTargetKinds: []string{"chapter"}, AllowedTargetIDs: []string{"question"}, AllowedParameterKeys: []string{"pattern"}, AllowedSeverities: []string{"warning", "info"}}}
	policyHash, err := governance.HashBindingPolicies(policies)
	if err != nil {
		t.Fatal(err)
	}
	definition := template.Clone(project.TemplateCopy.Definition)
	definition.Rules, err = template.CompileRuleSet(set)
	if err != nil {
		t.Fatal(err)
	}
	definition, err = template.WithHashes(definition)
	if err != nil {
		t.Fatal(err)
	}
	if err := governance.ValidateBindingPolicies(definition, set, policies); err != nil {
		t.Fatal(err)
	}
	definitionJSON, _ := json.Marshal(definition)
	sidecarJSON, _ := json.Marshal(core.ProjectTemplateCopy{RuleSetSnapshot: &set, BindingPolicies: policies, SourcePolicyHash: policyHash,
		SourceRuleSetID: set.ID, SourceRuleSetVersion: set.Version, SourceRuleSetHash: set.RulesetHash})
	if err := db.Exec("UPDATE lingdoc_project_template_copies SET definition_json = ?, content_hash = ?, ruleset_hash = ? WHERE id = ?", string(definitionJSON), definition.ContentHash, set.RulesetHash, project.TemplateCopy.ID).Error; err != nil {
		t.Fatal(err)
	}
	// CreateProject already persists a template_binding sidecar for this copy
	// (source-kind persistence); the fixture REPLACES it with the historical
	// payload the scenario simulates, so the unique key must be cleared first.
	if err := db.Exec("DELETE FROM lingdoc_g5_records WHERE project_id = ? AND kind = 'template_binding' AND id = ?", project.ID, project.TemplateCopy.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO lingdoc_g5_records (project_id, kind, id, version, payload_json) VALUES (?, ?, ?, 1, ?)", project.ID, "template_binding", project.TemplateCopy.ID, string(sidecarJSON)).Error; err != nil {
		t.Fatal(err)
	}
	spec := core.SaveSpecInput{ExpectedSpecRevision: project.SpecRevision, Fields: map[string]string{"research_subject": "subject", "research_goal": "goal"}}
	project = decodeProject(request(fixture.router, http.MethodPut, "/projects/"+project.ID+"/spec", "legacy-http-spec", spec, http.StatusOK))
	// A client must not bypass the reviewed-policy entry by editing the
	// projected rules through the ordinary preview/save endpoints.
	forged := template.Clone(project.TemplateCopy.Definition)
	forged.Rules[0].Message = "unreviewed direct edit"
	forgedInput := core.TemplateCopyDefinitionInput{ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
		Fields: forged.Fields, Sections: forged.Sections, Terms: append([]template.Term{}, forged.Terms...), RequiredFields: forged.RequiredFields, Rules: forged.Rules}
	var editsBefore, editsAfter, rejectedOperations int64
	if err := db.Table("lingdoc_project_audits").Where("project_id = ? AND action = ?", project.ID, "project_template.edit").Count(&editsBefore).Error; err != nil {
		t.Fatal(err)
	}
	request(fixture.router, http.MethodPost, "/projects/"+project.ID+"/template-copy/preview", "", forgedInput, http.StatusBadRequest)
	request(fixture.router, http.MethodPut, "/projects/"+project.ID+"/template-copy", "legacy-http-forged", forgedInput, http.StatusBadRequest)
	if err := db.Table("lingdoc_project_audits").Where("project_id = ? AND action = ?", project.ID, "project_template.edit").Count(&editsAfter).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("lingdoc_operations").Where("key = ?", "legacy-http-forged").Count(&rejectedOperations).Error; err != nil {
		t.Fatal(err)
	}
	if editsBefore != editsAfter || rejectedOperations != 0 {
		t.Fatalf("forged edit left audit or idempotency records: audits=%d/%d operations=%d", editsBefore, editsAfter, rejectedOperations)
	}
	endpoint := "/projects/" + project.ID + "/template-copy/legacy-rule-overrides"
	input := core.LegacyRuleOverridesInput{ExpectedProjectVersion: project.ProjectVersion, ExpectedTemplateCopyVersion: project.TemplateCopyVersion,
		Reason: "reviewed adjustment", Overrides: []governance.BindingOverride{{RuleID: "goal-pattern", Severity: "info", Parameters: map[string]any{"pattern": "^goal$"}}}}
	invalid := input
	invalid.Overrides = []governance.BindingOverride{{RuleID: "goal-pattern", Parameters: map[string]any{"script": "bad"}}}
	request(fixture.router, http.MethodPut, endpoint, "legacy-http-invalid", invalid, http.StatusBadRequest)
	invalidKind := input
	invalidKind.Overrides = []governance.BindingOverride{{RuleID: "goal-pattern", TargetKind: "chapter"}}
	request(fixture.router, http.MethodPut, endpoint, "legacy-http-invalid-kind", invalidKind, http.StatusBadRequest)
	invalidID := input
	invalidID.Overrides = []governance.BindingOverride{{RuleID: "goal-pattern", TargetID: "question"}}
	request(fixture.router, http.MethodPut, endpoint, "legacy-http-invalid-id", invalidID, http.StatusBadRequest)
	setRuleSetAuditFailureTrigger(t, db, "sqlite", "lingdoc_project_audits", "project_template.override_ruleset", "legacy_http_fail_audit", true)
	request(fixture.router, http.MethodPut, endpoint, "legacy-http-audit-fail", input, http.StatusInternalServerError)
	setRuleSetAuditFailureTrigger(t, db, "sqlite", "lingdoc_project_audits", "project_template.override_ruleset", "legacy_http_fail_audit", false)
	afterFailure := decodeProject(request(fixture.router, http.MethodGet, "/projects/"+project.ID, "", nil, http.StatusOK))
	if afterFailure.ProjectVersion != project.ProjectVersion || afterFailure.TemplateCopyVersion != project.TemplateCopyVersion {
		t.Fatal("failed policy or audit changed the project")
	}
	var count int64
	if err := db.Table("lingdoc_project_template_copies").Where("project_id = ?", project.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("failure wrote partial copy count=%d err=%v", count, err)
	}
	var badOperations, overrideAudits, sidecars int64
	if err := db.Table("lingdoc_operations").Where("key IN ?", []string{"legacy-http-invalid-kind", "legacy-http-invalid-id"}).Count(&badOperations).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("lingdoc_project_audits").Where("project_id = ? AND action = ?", project.ID, "project_template.override_ruleset").Count(&overrideAudits).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("lingdoc_g5_records").Where("project_id = ? AND kind = ?", project.ID, "template_binding").Count(&sidecars).Error; err != nil {
		t.Fatal(err)
	}
	if badOperations != 0 || overrideAudits != 0 || sidecars != 1 {
		t.Fatalf("invalid targets left operations=%d override audits=%d sidecars=%d", badOperations, overrideAudits, sidecars)
	}
	first := request(fixture.router, http.MethodPut, endpoint, "legacy-http-override", input, http.StatusOK)
	updated := decodeProject(first)
	if updated.TemplateCopyVersion != project.TemplateCopyVersion+1 || updated.TemplateCopy.SourceRuleSetHash != set.RulesetHash || updated.TemplateCopy.RulesetHash == set.RulesetHash {
		t.Fatalf("incorrect source/effective identity: %+v", updated.TemplateCopy)
	}
	if retry := request(fixture.router, http.MethodPut, endpoint, "legacy-http-override", input, http.StatusOK); !retry.Meta.Replayed {
		t.Fatal("same request did not replay")
	}
	request(fixture.router, http.MethodPut, endpoint, "legacy-http-stale", input, http.StatusConflict)
	closeRuleSetDatabase(t, db)
	db = openRuleSetSQLite(t, path)
	fixture = buildRuleSetHTTPFixture(t, db)
	updated = decodeProject(request(fixture.router, http.MethodGet, "/projects/"+project.ID, "", nil, http.StatusOK))
	if updated.TemplateCopy.SourceRuleSetHash != set.RulesetHash || updated.TemplateCopy.RuleSetSnapshot.Bindings[0].Severity != "info" {
		t.Fatalf("restart lost effective snapshot or source pin: %+v", updated.TemplateCopy)
	}
	edit := core.TemplateCopyDefinitionInput{ExpectedProjectVersion: updated.ProjectVersion, ExpectedTemplateCopyVersion: updated.TemplateCopyVersion,
		Fields: updated.TemplateCopy.Definition.Fields, Sections: updated.TemplateCopy.Definition.Sections,
		Terms: append([]template.Term{}, updated.TemplateCopy.Definition.Terms...), RequiredFields: updated.TemplateCopy.Definition.RequiredFields, Rules: updated.TemplateCopy.Definition.Rules}
	edit.Fields[0].Label = "Revised field"
	updated = decodeProject(request(fixture.router, http.MethodPut, "/projects/"+project.ID+"/template-copy", "legacy-http-structural", edit, http.StatusOK))
	restore := core.LegacyRuleOverridesInput{ExpectedProjectVersion: updated.ProjectVersion, ExpectedTemplateCopyVersion: updated.TemplateCopyVersion,
		Reason: "restore reviewed severity", Overrides: []governance.BindingOverride{{RuleID: "goal-pattern", Severity: "warning"}}}
	updated = decodeProject(request(fixture.router, http.MethodPut, endpoint, "legacy-http-restore", restore, http.StatusOK))
	if updated.TemplateCopy.RuleSetSnapshot.Bindings[0].Severity != "warning" || updated.TemplateCopy.SourceRuleSetHash != set.RulesetHash {
		t.Fatalf("restoring approved value failed: %+v", updated.TemplateCopy)
	}
	validTarget := core.LegacyRuleOverridesInput{ExpectedProjectVersion: updated.ProjectVersion, ExpectedTemplateCopyVersion: updated.TemplateCopyVersion,
		Reason: "use the reviewed chapter target", Overrides: []governance.BindingOverride{{RuleID: "goal-pattern", TargetKind: "chapter", TargetID: "question"}}}
	updated = decodeProject(request(fixture.router, http.MethodPut, endpoint, "legacy-http-target", validTarget, http.StatusOK))
	binding := updated.TemplateCopy.RuleSetSnapshot.Bindings[0]
	if binding.TargetKind != "chapter" || binding.TargetID != "question" || updated.TemplateCopy.SourceRuleSetHash != set.RulesetHash {
		t.Fatalf("valid target was not persisted with its original source: %+v", updated.TemplateCopy)
	}
	var audits []core.AuditEvent
	if err := json.Unmarshal(request(fixture.router, http.MethodGet, "/projects/"+project.ID+"/audit", "", nil, http.StatusOK).Data, &audits); err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, event := range audits {
		if event.Action == "project_template.override_ruleset" {
			found++
		}
	}
	if found != 3 {
		t.Fatalf("expected 3 durable override audits, got %d", found)
	}

	// Seed a second historical sidecar, then exercise the active semantic
	// change entry through the same production handler and SQLite database.
	active := decodeProject(request(fixture.router, http.MethodPost, "/projects", "active-target-create", map[string]any{"name": "Active target fixture", "template_id": template.DemoTemplateID}, http.StatusCreated))
	if err := db.Exec("UPDATE lingdoc_project_template_copies SET definition_json = ?, content_hash = ?, ruleset_hash = ? WHERE id = ?", string(definitionJSON), definition.ContentHash, set.RulesetHash, active.TemplateCopy.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM lingdoc_g5_records WHERE project_id = ? AND kind = 'template_binding' AND id = ?", active.ID, active.TemplateCopy.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO lingdoc_g5_records (project_id, kind, id, version, payload_json) VALUES (?, ?, ?, 1, ?)", active.ID, "template_binding", active.TemplateCopy.ID, string(sidecarJSON)).Error; err != nil {
		t.Fatal(err)
	}
	active = decodeProject(request(fixture.router, http.MethodPut, "/projects/"+active.ID+"/spec", "active-target-spec", core.SaveSpecInput{
		ExpectedSpecRevision: active.SpecRevision, Fields: map[string]string{"research_subject": "subject", "research_goal": "goal"},
	}, http.StatusOK))
	var receipt core.ActivationReadReceipt
	read := request(fixture.router, http.MethodPost, "/projects/"+active.ID+"/activation-read", "", map[string]any{"expected_project_version": active.ProjectVersion}, http.StatusCreated)
	if err := json.Unmarshal(read.Data, &receipt); err != nil || receipt.ID == "" {
		t.Fatalf("activation receipt unavailable: %+v err=%v", receipt, err)
	}
	active = decodeProject(request(fixture.router, http.MethodPost, "/projects/"+active.ID+"/activate", "active-target-activate", map[string]any{
		"expected_spec_revision": active.SpecRevision, "expected_project_version": active.ProjectVersion,
		"reviewed_project_version": active.ProjectVersion, "activation_read_id": receipt.ID,
	}, http.StatusOK))
	changeInput := core.ExtendedChangeSetInput{ExpectedContextRevision: active.CurrentContextRevision, Reason: "review target",
		SemanticDeltas: []core.SemanticDelta{{Kind: "RuleBinding", TargetID: "goal-pattern", ExpectedVersion: active.TemplateCopyVersion,
			ProposedBinding: &governance.BindingOverride{RuleID: "goal-pattern", TargetID: "question"}}}}
	changeEndpoint := "/projects/" + active.ID + "/change-sets/drafts"
	request(fixture.router, http.MethodPost, changeEndpoint, "active-target-invalid-id", changeInput, http.StatusBadRequest)
	changeInput.SemanticDeltas[0].ProposedBinding = &governance.BindingOverride{RuleID: "goal-pattern", TargetKind: "chapter"}
	// Source discovery runs before proposal pinning, so the absent chapter is
	// rejected as not found before the same target is checked at write time.
	request(fixture.router, http.MethodPost, changeEndpoint, "active-target-invalid-kind", changeInput, http.StatusNotFound)
	readBack := decodeProject(request(fixture.router, http.MethodGet, "/projects/"+active.ID, "", nil, http.StatusOK))
	if readBack.ProjectVersion != active.ProjectVersion || readBack.SpecRevision != active.SpecRevision ||
		readBack.CurrentContextRevision != active.CurrentContextRevision || readBack.TemplateCopyVersion != active.TemplateCopyVersion ||
		readBack.TemplateCopy.RulesetHash != active.TemplateCopy.RulesetHash {
		t.Fatal("rejected active target changed the project")
	}
	var activeCopies, activeSidecars int64
	if err := db.Table("lingdoc_project_template_copies").Where("project_id = ?", active.ID).Count(&activeCopies).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Table("lingdoc_g5_records").Where("project_id = ? AND kind = ?", active.ID, "template_binding").Count(&activeSidecars).Error; err != nil {
		t.Fatal(err)
	}
	if activeCopies != 1 || activeSidecars != 1 {
		t.Fatalf("rejected active target changed copies=%d or sidecars=%d", activeCopies, activeSidecars)
	}
	for _, check := range []struct {
		table, where string
		args         []any
	}{
		{"lingdoc_operations", "key IN ?", []any{[]string{"active-target-invalid-id", "active-target-invalid-kind"}}},
		{"lingdoc_change_sets", "project_id = ?", []any{active.ID}},
		{"lingdoc_project_audits", "project_id = ? AND action IN ?", []any{active.ID, []string{"change_set.draft", "semantic.apply"}}},
	} {
		var records int64
		if err := db.Table(check.table).Where(check.where, check.args...).Count(&records).Error; err != nil || records != 0 {
			t.Fatalf("rejected active target left %s records=%d err=%v", check.table, records, err)
		}
	}
	changeInput.SemanticDeltas[0].ProposedBinding = &governance.BindingOverride{RuleID: "goal-pattern", TargetKind: "chapter", TargetID: "question"}
	createdChange := request(fixture.router, http.MethodPost, changeEndpoint, "active-target-valid", changeInput, http.StatusCreated)
	var change core.ChangeSet
	if err := json.Unmarshal(createdChange.Data, &change); err != nil || change.ID == "" {
		t.Fatalf("valid target draft unavailable: %+v err=%v", change, err)
	}
	request(fixture.router, http.MethodPost, "/projects/"+active.ID+"/change-sets/"+change.ID+"/assess", "active-target-assess", map[string]any{}, http.StatusOK)
	request(fixture.router, http.MethodPost, "/projects/"+active.ID+"/change-sets/"+change.ID+"/apply", "active-target-apply", map[string]any{}, http.StatusOK)
	readBack = decodeProject(request(fixture.router, http.MethodGet, "/projects/"+active.ID, "", nil, http.StatusOK))
	if readBack.TemplateCopy.RuleSetSnapshot.Bindings[0].TargetKind != "chapter" || readBack.TemplateCopy.RuleSetSnapshot.Bindings[0].TargetID != "question" {
		t.Fatalf("valid active target not applied: %+v", readBack.TemplateCopy)
	}
}
