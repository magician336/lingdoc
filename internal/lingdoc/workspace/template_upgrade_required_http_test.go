package workspace

import (
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// Exercise the production HTTP handler, application service and migrated
// SQLite store using an authenticated-context fixture and in-process requests.
func TestActiveTemplateUpgradeHTTPRejectsUnlistedRequiredFieldWithoutWrites(t *testing.T) {
	h := newMigrationRiskHarness(t, nil)
	h.saveSpec("active-required-spec", map[string]string{"research_subject": "subject", "research_goal": "goal"})
	h.call(h.ctx, http.MethodPost, "/activate", "active-required-activate", core.ActivateProjectInput{
		ExpectedSpecRevision: h.project.SpecRevision, ExpectedProjectVersion: h.project.ProjectVersion,
		ReviewedProjectVersion: h.project.ProjectVersion,
	}, http.StatusOK, &h.project)
	if h.project.Status != "active" {
		t.Fatalf("fixture was not activated: %+v", h.project)
	}
	edit := h.edit()
	edit.Fields = append(edit.Fields, template.Field{ID: "new_required", Label: "New required field", Type: "string", Required: true})
	// Existing required_fields remains nonempty and intentionally does not list
	// the newly required field; the field's own flag must still be enforced.
	input := core.CreateChangeSetInput{
		ExpectedContextRevision: h.project.CurrentContextRevision,
		Reason:                  "Exercise unlisted required field",
		TemplateUpgrade: &core.TemplateUpgradeInput{
			ExpectedProjectVersion: edit.ExpectedProjectVersion, ExpectedTemplateCopyVersion: edit.ExpectedTemplateCopyVersion,
			Fields: edit.Fields, Sections: edit.Sections, Terms: edit.Terms, RequiredFields: edit.RequiredFields, Rules: edit.Rules,
		},
	}
	count := func(table string) int64 {
		t.Helper()
		var n int64
		if err := h.db.Table(table).Where("project_id = ?", h.project.ID).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}
	changesBefore, impactBefore := count("lingdoc_change_sets"), count("lingdoc_g3_records")
	before, copies, audits := h.snapshot("project.change_set.create")
	key := "active-required-unlisted"
	h.call(h.ctx, http.MethodPost, "/change-sets", key, input, http.StatusBadRequest, nil)
	h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project.change_set.create", key)
	if changes, impacts := count("lingdoc_change_sets"), count("lingdoc_g3_records"); changes != changesBefore || impacts != impactBefore {
		t.Fatalf("failed assessment left partial writes: change sets %d->%d, impact records %d->%d", changesBefore, changes, impactBefore, impacts)
	}
	input.TemplateUpgrade.FieldValues = map[string]string{"new_required": "resolved"}
	var created core.ChangeSet
	if h.call(h.ctx, http.MethodPost, "/change-sets", key, input, http.StatusCreated, &created) {
		t.Fatal("corrected assessment was incorrectly marked as replayed")
	}
	if created.TemplateUpgrade == nil || created.TemplateUpgrade.FieldValues["new_required"] != "resolved" || created.Status != "assessed" {
		t.Fatalf("corrected assessment did not retain the required mapping: %+v", created)
	}
}
