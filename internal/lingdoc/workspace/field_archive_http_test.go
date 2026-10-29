package workspace

import (
	"net/http"
	"slices"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

func archiveHTTPInput(h *migrationRiskHarness, fieldID string) (core.CreateFieldArchiveInput, core.RemoveArchivedFieldInput) {
	definition := h.edit()
	definition.Fields = slices.DeleteFunc(definition.Fields, func(f template.Field) bool { return f.ID == fieldID })
	definition.RequiredFields = slices.DeleteFunc(definition.RequiredFields, func(id string) bool { return id == fieldID })
	return core.CreateFieldArchiveInput{FieldID: fieldID, ExpectedSpecRevision: h.project.SpecRevision, TemplateCopy: definition},
		core.RemoveArchivedFieldInput{Confirmed: true, ExpectedArchiveVersion: 1, ExpectedSpecRevision: h.project.SpecRevision, TemplateCopy: definition}
}

// These requests use the registered HTTP handler, real application service,
// production migrations, and SQLite; httptest is in process, not a browser.
func TestFieldArchiveHTTPRequiresTwoStagesAndPreservesHistory(t *testing.T) {
	h := newMigrationRiskHarness(t, nil)
	h.saveSpec("archive-http-spec", map[string]string{"research_subject": "subject", "research_goal": "old goal"})
	create, remove := archiveHTTPInput(h, "research_goal")
	before, copies, audits := h.snapshot("project_template.archive_field")
	var receipt core.FieldArchiveReceipt
	h.call(h.ctx, http.MethodPost, "/template-copy/field-archives", "", create, http.StatusBadRequest, nil)
	outsider := h.outsider()
	h.call(outsider, http.MethodPost, "/template-copy/field-archives", "archive-http-denied", create, http.StatusNotFound, nil)
	h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.archive_field", "archive-http-denied")
	h.call(h.ctx, http.MethodPost, "/template-copy/field-archives", "archive-http-create", create, http.StatusCreated, &receipt)
	if receipt.ID == "" || receipt.Status != "recorded" || receipt.ArchiveVersion != 1 || receipt.ProjectVersion != h.project.ProjectVersion {
		t.Fatalf("incorrect creation receipt: %+v", receipt)
	}
	var list struct {
		Items []core.FieldArchiveView `json:"items"`
	}
	h.call(h.ctx, http.MethodGet, "/template-copy/field-archives", "", nil, http.StatusOK, &list)
	if len(list.Items) != 1 || list.Items[0].Value != "old goal" || list.Items[0].Metadata == nil {
		t.Fatalf("recorded value or metadata not visible: %+v", list)
	}
	path := "/template-copy/field-archives/" + receipt.ID
	h.call(outsider, http.MethodGet, path, "", nil, http.StatusNotFound, nil)
	var detail core.FieldArchiveView
	h.call(h.ctx, http.MethodGet, path, "", nil, http.StatusOK, &detail)
	if detail.Status != "recorded" || detail.ValueRedacted || !detail.ValuePresent || detail.Value != "old goal" {
		t.Fatalf("archive detail mismatch: %+v", detail)
	}
	wrong := remove
	wrong.TemplateCopy = create.TemplateCopy
	wrong.TemplateCopy.Fields = append(append([]template.Field{}, wrong.TemplateCopy.Fields...), template.Field{ID: "research_goal", Label: "Goal", Type: "string"})
	before, copies, audits = h.snapshot("project_template.remove_archived_field")
	h.call(h.ctx, http.MethodPost, path+"/remove", "archive-http-invalid", wrong, http.StatusBadRequest, nil)
	h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.remove_archived_field", "archive-http-invalid")
	changedDraft := remove
	changedDraft.TemplateCopy.Fields = append([]template.Field{}, remove.TemplateCopy.Fields...)
	changedDraft.TemplateCopy.Fields[0].Label += " changed after archival"
	h.call(h.ctx, http.MethodPost, path+"/remove", "archive-http-changed-target", changedDraft, http.StatusConflict, nil)
	h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.remove_archived_field", "archive-http-changed-target")
	if h.call(h.ctx, http.MethodPost, path+"/remove", "archive-http-remove", remove, http.StatusOK, &receipt) {
		t.Fatal("first removal was reported as a replay")
	}
	if receipt.Status != "removed" || receipt.ArchiveVersion != 2 || receipt.ProjectVersion != before.ProjectVersion+1 || receipt.SpecRevision != before.SpecRevision+1 {
		t.Fatalf("incorrect removal receipt: %+v", receipt)
	}
	if !h.call(h.ctx, http.MethodPost, path+"/remove", "archive-http-remove", remove, http.StatusOK, nil) {
		t.Fatal("remove retry did not replay")
	}
	var after core.Project
	h.call(h.ctx, http.MethodGet, "", "", nil, http.StatusOK, &after)
	h.project = after
	if _, exists := h.project.Spec["research_goal"]; exists {
		t.Fatal("removed value remains in current project")
	}
	h.call(h.ctx, http.MethodGet, path, "", nil, http.StatusOK, &detail)
	if detail.Status != "removed" || detail.Value != "old goal" || detail.Metadata == nil || detail.Metadata.Value != "old goal" {
		t.Fatalf("historical value not preserved: %+v", detail)
	}
	definition := h.edit()
	definition.Fields = slices.DeleteFunc(definition.Fields, func(f template.Field) bool { return f.ID == "research_goal" })
	definition.RequiredFields = slices.DeleteFunc(definition.RequiredFields, func(id string) bool { return id == "research_goal" })
	var preview core.TemplateMigrationPreview
	h.call(h.ctx, http.MethodPost, "/template-copy/preview", "", definition, http.StatusOK, &preview)
	if len(preview.MissingRequired)+len(preview.Incompatible)+len(preview.Orphaned) != 0 {
		t.Fatalf("archive did not resolve field risk: %+v", preview)
	}
	h.call(h.ctx, http.MethodPut, "/template-copy", "archive-http-template-save", definition, http.StatusOK, &h.project)
}

func TestFieldArchiveHTTPAuditFailuresRollBackBothStages(t *testing.T) {
	h := newMigrationRiskHarness(t, nil)
	h.saveSpec("archive-http-rollback-spec", map[string]string{"research_subject": "subject", "research_goal": "old goal"})
	create, remove := archiveHTTPInput(h, "research_goal")
	if err := h.db.Exec(`CREATE TRIGGER archive_create_abort BEFORE INSERT ON lingdoc_project_audits WHEN NEW.action = 'project_template.archive_field' BEGIN SELECT RAISE(ABORT,'archive audit failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	before, copies, audits := h.snapshot("project_template.archive_field")
	h.call(h.ctx, http.MethodPost, "/template-copy/field-archives", "archive-http-create-fail", create, http.StatusInternalServerError, nil)
	h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.archive_field", "archive-http-create-fail")
	var archives int64
	if err := h.db.Table("lingdoc_g5_records").Where("project_id = ? AND kind = ?", h.project.ID, "template_field_archive").Count(&archives).Error; err != nil || archives != 0 {
		t.Fatalf("archive failure left records=%d err=%v", archives, err)
	}
	if err := h.db.Exec("DROP TRIGGER archive_create_abort").Error; err != nil {
		t.Fatal(err)
	}
	var receipt core.FieldArchiveReceipt
	h.call(h.ctx, http.MethodPost, "/template-copy/field-archives", "archive-http-create-ok", create, http.StatusCreated, &receipt)
	if err := h.db.Exec(`CREATE TRIGGER archive_remove_abort BEFORE INSERT ON lingdoc_project_audits WHEN NEW.action = 'project_template.remove_archived_field' BEGIN SELECT RAISE(ABORT,'remove audit failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	before, copies, audits = h.snapshot("project_template.remove_archived_field")
	path := "/template-copy/field-archives/" + receipt.ID
	h.call(h.ctx, http.MethodPost, path+"/remove", "archive-http-remove-fail", remove, http.StatusInternalServerError, nil)
	h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.remove_archived_field", "archive-http-remove-fail")
	var detail core.FieldArchiveView
	h.call(h.ctx, http.MethodGet, path, "", nil, http.StatusOK, &detail)
	if detail.Status != "recorded" || detail.ArchiveVersion != 1 || detail.Value != "old goal" {
		t.Fatalf("failed removal modified archive: %+v", detail)
	}
}

func TestFieldArchiveHTTPPreservesLegacyWhitespaceKey(t *testing.T) {
	h := newMigrationRiskHarness(t, nil)
	h.saveSpec("archive-http-legacy-spec", map[string]string{"research_subject": "subject", "research_goal": "goal", " legacy": "old"})
	create, remove := archiveHTTPInput(h, " legacy")
	var receipt core.FieldArchiveReceipt
	h.call(h.ctx, http.MethodPost, "/template-copy/field-archives", "archive-http-legacy-create", create, http.StatusCreated, &receipt)
	var view core.FieldArchiveView
	h.call(h.ctx, http.MethodGet, "/template-copy/field-archives/"+receipt.ID, "", nil, http.StatusOK, &view)
	if view.FieldID != " legacy" || view.Value != "old" {
		t.Fatalf("legacy field ID or value changed: %+v", view)
	}
	h.call(h.ctx, http.MethodPost, "/template-copy/field-archives/"+receipt.ID+"/remove", "archive-http-legacy-remove", remove, http.StatusOK, &receipt)
}

func TestFieldArchiveHTTPRejectsActiveProject(t *testing.T) {
	h := newMigrationRiskHarness(t, nil)
	h.saveSpec("archive-http-active-spec", map[string]string{"research_subject": "subject", "research_goal": "goal"})
	h.call(h.ctx, http.MethodPost, "/activate", "archive-http-activate", map[string]any{
		"expected_spec_revision": h.project.SpecRevision, "expected_project_version": h.project.ProjectVersion,
		"reviewed_project_version": h.project.ProjectVersion,
	}, http.StatusOK, &h.project)
	create, _ := archiveHTTPInput(h, "research_goal")
	before, copies, audits := h.snapshot("project_template.archive_field")
	h.call(h.ctx, http.MethodPost, "/template-copy/field-archives", "archive-http-active", create, http.StatusUnprocessableEntity, nil)
	h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.archive_field", "archive-http-active")
}
