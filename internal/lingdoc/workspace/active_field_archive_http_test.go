package workspace

import (
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"net/http"
	"reflect"
	"testing"
)

func TestActiveArchiveHTTPAssessedApplyAndStaleReference(t *testing.T) {
	h := newMigrationRiskHarness(t, nil)
	h.saveSpec("active-archive-http-spec", map[string]string{"research_subject": "subject", "research_goal": "old goal"})
	h.call(h.ctx, http.MethodPost, "/activate", "active-archive-http-activate", core.ActivateProjectInput{ExpectedSpecRevision: h.project.SpecRevision, ExpectedProjectVersion: h.project.ProjectVersion, ReviewedProjectVersion: h.project.ProjectVersion}, http.StatusOK, &h.project)
	create, remove := archiveHTTPInput(h, "research_goal")
	d := create.TemplateCopy
	upgrade := core.TemplateUpgradeInput{ExpectedProjectVersion: d.ExpectedProjectVersion, ExpectedTemplateCopyVersion: d.ExpectedTemplateCopyVersion, Fields: d.Fields, Sections: d.Sections, Terms: d.Terms, RequiredFields: d.RequiredFields, Rules: d.Rules}
	create.TemplateCopy = core.TemplateCopyDefinitionInput{}
	create.TemplateUpgrade = &upgrade
	create.ExpectedContextRevision = h.project.CurrentContextRevision
	var receipt core.FieldArchiveReceipt
	h.call(h.outsider(), http.MethodPost, "/template-copy/field-archives", "active-archive-http-denied", create, http.StatusNotFound, nil)
	h.call(h.ctx, http.MethodPost, "/template-copy/field-archives", "active-archive-http-create", create, http.StatusCreated, &receipt)
	path := "/template-copy/field-archives/" + receipt.ID
	var archive core.FieldArchiveView
	h.call(h.ctx, http.MethodGet, path, "", nil, http.StatusOK, &archive)
	if archive.Value != "old goal" || archive.ContextRevision != h.project.CurrentContextRevision {
		t.Fatalf("archive mismatch: %+v", archive)
	}
	h.call(h.ctx, http.MethodPost, path+"/remove", "active-archive-http-direct", remove, http.StatusUnprocessableEntity, nil)
	upgrade.ArchivedRemovals = []core.ArchivedFieldReference{{FieldID: "research_goal", ArchiveID: receipt.ID, ExpectedArchiveVersion: 99}}
	input := core.CreateChangeSetInput{ExpectedContextRevision: h.project.CurrentContextRevision, Reason: "archive old field", TemplateUpgrade: &upgrade}
	h.call(h.ctx, http.MethodPost, "/change-sets", "active-archive-http-stale", input, http.StatusConflict, nil)
	var unchanged core.Project
	h.call(h.ctx, http.MethodGet, "", "", nil, http.StatusOK, &unchanged)
	if !reflect.DeepEqual(unchanged.Spec, h.project.Spec) {
		t.Fatal("rejected request changed spec")
	}
	upgrade.ArchivedRemovals[0].ExpectedArchiveVersion = receipt.ArchiveVersion
	var change core.ChangeSet
	h.call(h.ctx, http.MethodPost, "/change-sets", "active-archive-http-assess", input, http.StatusCreated, &change)
	h.call(h.ctx, http.MethodPost, "/change-sets/"+change.ID+"/apply", "active-archive-http-apply", nil, http.StatusOK, nil)
 unchanged = core.Project{}
	h.call(h.ctx, http.MethodGet, "", "", nil, http.StatusOK, &unchanged)
	if _, ok := unchanged.Spec["research_goal"]; ok {
		t.Fatal("applied field still present")
	}
	h.call(h.ctx, http.MethodGet, path, "", nil, http.StatusOK, &archive)
	if archive.Status != "removed" || archive.Value != "old goal" {
		t.Fatal("archive lost after apply")
	}
}
