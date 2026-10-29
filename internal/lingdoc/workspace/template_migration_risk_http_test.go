package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// These tests exercise the registered HTTP handlers, the real application
// service and migrated SQLite tables. Authentication is a context fixture;
// httptest sends requests in process, so this is not a browser/network test.
type migrationRiskHarness struct {
	t       *testing.T
	db      *gorm.DB
	router  *gin.Engine
	ctx     context.Context
	actor   core.Actor
	project core.Project
}

type migrationRiskReader struct{ target template.Template }

func (r migrationRiskReader) Get(id, version string) (core.Template, error) {
	if id == r.target.ID && (version == "" || version == r.target.Version) {
		return template.Clone(r.target), nil
	}
	return (core.ContractDemoTemplate{}).Get(id, version)
}

func newMigrationRiskHarness(t *testing.T, target *template.Template) *migrationRiskHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	_, projects, db, actor, project, ctx := governanceAdapterFixtureWithProjects(t)
	if target != nil {
		projects = core.NewService(core.NewGORMRepository(db), migrationRiskReader{target: *target})
	}
	handler, err := NewHandler(HandlerDependencies{
		Projects: projects, Members: newMembershipService(projects, core.NewGORMRepository(db)),
		Chapters: projects, TemplateApp: projects, ChangeSets: projects, Sources: emptySourcesStub{},
	})
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	handler.Register(RouteGroups{Read: router.Group("/api/v1/lingdoc"), Write: router.Group("/api/v1/lingdoc")})
	return &migrationRiskHarness{t: t, db: db, router: router, ctx: ctx, actor: actor, project: project}
}

func (h *migrationRiskHarness) call(ctx context.Context, method, suffix, key string, input any, want int, result any) bool {
	h.t.Helper()
	var body string
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			h.t.Fatal(err)
		}
		body = string(raw)
	}
	response := governanceHTTPRequest(h.router, ctx, method, "/api/v1/lingdoc/projects/"+h.project.ID+suffix, body, key)
	if response.Code != want {
		h.t.Fatalf("%s %s: got %d, want %d: %s", method, suffix, response.Code, want, response.Body.String())
	}
	if want >= 400 {
		return false
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
		Meta struct {
			Replayed bool `json:"replayed"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		h.t.Fatal(err)
	}
	if result != nil {
		if err := json.Unmarshal(envelope.Data, result); err != nil {
			h.t.Fatal(err)
		}
	}
	return envelope.Meta.Replayed
}

func (h *migrationRiskHarness) saveSpec(key string, fields map[string]string) {
	h.t.Helper()
	h.call(h.ctx, http.MethodPut, "/spec", key, core.SaveSpecInput{
		ExpectedSpecRevision: h.project.SpecRevision, Fields: fields,
	}, http.StatusOK, &h.project)
}

func (h *migrationRiskHarness) edit() core.TemplateCopyDefinitionInput {
	h.t.Helper()
	if h.project.TemplateCopy == nil {
		h.t.Fatal("fixture has no current template copy")
	}
	d := template.Clone(h.project.TemplateCopy.Definition)
	return core.TemplateCopyDefinitionInput{
		ExpectedProjectVersion: h.project.ProjectVersion, ExpectedTemplateCopyVersion: h.project.TemplateCopyVersion,
		Fields: d.Fields, Sections: d.Sections, Terms: append([]template.Term{}, d.Terms...),
		RequiredFields: d.RequiredFields, Rules: d.Rules,
	}
}

func (h *migrationRiskHarness) snapshot(action string) (core.Project, int64, int64) {
	h.t.Helper()
	var project core.Project
	h.call(h.ctx, http.MethodGet, "", "", nil, http.StatusOK, &project)
	var copies, audits int64
	if err := h.db.Table("lingdoc_project_template_copies").Where("project_id = ?", project.ID).Count(&copies).Error; err != nil {
		h.t.Fatal(err)
	}
	if err := h.db.Table("lingdoc_project_audits").Where("project_id = ? AND action = ?", project.ID, action).Count(&audits).Error; err != nil {
		h.t.Fatal(err)
	}
	return project, copies, audits
}

func (h *migrationRiskHarness) assertRejectedWithoutBusinessWrites(before core.Project, copies, audits int64, action, key string) {
	h.t.Helper()
	after, afterCopies, afterAudits := h.snapshot(action)
	if after.ProjectVersion != before.ProjectVersion || after.SpecRevision != before.SpecRevision ||
		after.TemplateCopyVersion != before.TemplateCopyVersion || after.TemplateID != before.TemplateID ||
		after.TemplateVersion != before.TemplateVersion || !reflect.DeepEqual(after.Spec, before.Spec) ||
		!reflect.DeepEqual(after.TemplateCopy, before.TemplateCopy) || afterCopies != copies || afterAudits != audits {
		h.t.Fatalf("rejected request changed the project/copies/success audit: before=%+v after=%+v copies=%d/%d audits=%d/%d", before, after, copies, afterCopies, audits, afterAudits)
	}
	var operations int64
	if err := h.db.Table("lingdoc_operations").Where("key = ?", key).Count(&operations).Error; err != nil || operations != 0 {
		h.t.Fatalf("rejected request left operation record: count=%d err=%v", operations, err)
	}
}

func (h *migrationRiskHarness) outsider() context.Context {
	h.t.Helper()
	actor := core.Actor{TenantID: h.actor.TenantID, UserID: "unrelated-reader", Role: types.TenantRoleViewer}
	if err := h.db.Exec("INSERT INTO tenant_members(tenant_id,user_id,role,status) VALUES(?,?,?,'active')", actor.TenantID, actor.UserID, actor.Role).Error; err != nil {
		h.t.Fatal(err)
	}
	return governanceCallerContext(context.Background(), actor)
}

func setGoalRequired(input *core.TemplateCopyDefinitionInput, required bool) {
	for i := range input.Fields {
		if input.Fields[i].ID == "research_goal" {
			input.Fields[i].Required = required
		}
	}
	input.RequiredFields = slices.DeleteFunc(input.RequiredFields, func(id string) bool { return id == "research_goal" })
	if required {
		input.RequiredFields = append(input.RequiredFields, "research_goal")
	}
}

func TestTemplateCopyMigrationRiskIsRecheckedAtHTTPWrite(t *testing.T) {
	h := newMigrationRiskHarness(t, nil)
	h.saveSpec("migration-base-spec", map[string]string{"research_subject": "subject", "research_goal": "goal"})
	optional := h.edit()
	setGoalRequired(&optional, false)
	h.call(h.ctx, http.MethodPut, "/template-copy", "migration-goal-optional", optional, http.StatusOK, &h.project)
	var stale core.TemplateCopyDefinitionInput
	for i, value := range []string{"", " \t\n"} {
		h.saveSpec(fmt.Sprintf("migration-blank-spec-%d", i), map[string]string{"research_subject": "subject", "research_goal": value})
		input := h.edit()
		setGoalRequired(&input, true)
		var preview core.TemplateMigrationPreview
		h.call(h.ctx, http.MethodPost, "/template-copy/preview", "", input, http.StatusOK, &preview)
		if !slices.Contains(preview.MissingRequired, "research_goal") {
			t.Fatalf("blank value was not reported missing: %+v", preview)
		}
		for _, field := range preview.Fields {
			if field.FieldID == "research_goal" && field.Status != "missing" {
				t.Fatalf("blank goal status = %q, want missing", field.Status)
			}
		}
		key := fmt.Sprintf("migration-blank-copy-%d", i)
		before, copies, audits := h.snapshot("project_template.edit")
		h.call(h.ctx, http.MethodPut, "/template-copy", key, input, http.StatusBadRequest, nil)
		h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.edit", key)
		stale = input
	}
	h.saveSpec("migration-fill-goal", map[string]string{"research_subject": "subject", "research_goal": "resolved"})
	before, copies, audits := h.snapshot("project_template.edit")
	h.call(h.ctx, http.MethodPut, "/template-copy", "migration-stale-copy", stale, http.StatusConflict, nil)
	h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.edit", "migration-stale-copy")
	input := h.edit()
	setGoalRequired(&input, true)
	var preview core.TemplateMigrationPreview
	h.call(h.ctx, http.MethodPost, "/template-copy/preview", "", input, http.StatusOK, &preview)
	if len(preview.MissingRequired)+len(preview.Orphaned)+len(preview.Incompatible) != 0 {
		t.Fatalf("fixed value still blocks preview: %+v", preview)
	}
	// The failed operation did not claim its key; a corrected request can use it.
	if h.call(h.ctx, http.MethodPut, "/template-copy", "migration-blank-copy-1", input, http.StatusOK, &h.project) {
		t.Fatal("corrected write was incorrectly marked as a replay")
	}
	if !h.call(h.ctx, http.MethodPut, "/template-copy", "migration-blank-copy-1", input, http.StatusOK, nil) {
		t.Fatal("idempotent retry did not replay the successful operation")
	}
	denied := h.edit()
	denied.Fields[0].Label += " outsider edit"
	before, copies, audits = h.snapshot("project_template.edit")
	h.call(h.outsider(), http.MethodPut, "/template-copy", "migration-denied-copy", denied, http.StatusNotFound, nil)
	h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.edit", "migration-denied-copy")
}

func TestTemplateCopyRejectsOrphansAndIncompatibleValuesAtHTTPWrite(t *testing.T) {
	h := newMigrationRiskHarness(t, nil)
	h.saveSpec("migration-risk-spec", map[string]string{"research_subject": "subject", "research_goal": "goal"})
	for _, kind := range []string{"orphaned", "incompatible"} {
		input := h.edit()
		switch kind {
		case "orphaned":
			input.Fields = slices.DeleteFunc(input.Fields, func(f template.Field) bool { return f.ID == "research_goal" })
			input.RequiredFields = slices.DeleteFunc(input.RequiredFields, func(id string) bool { return id == "research_goal" })
		case "incompatible":
			for i := range input.Fields {
				if input.Fields[i].ID == "research_subject" {
					input.Fields[i].Type, input.Fields[i].Required = "number", false
				}
			}
			input.RequiredFields = slices.DeleteFunc(input.RequiredFields, func(id string) bool { return id == "research_subject" })
		}
		var preview core.TemplateMigrationPreview
		h.call(h.ctx, http.MethodPost, "/template-copy/preview", "", input, http.StatusOK, &preview)
		if kind == "orphaned" && !slices.Contains(preview.Orphaned, "research_goal") ||
			kind == "incompatible" && !slices.Contains(preview.Incompatible, "research_subject") {
			t.Fatalf("%s risk missing from preview: %+v", kind, preview)
		}
		before, copies, audits := h.snapshot("project_template.edit")
		key := "migration-" + kind + "-copy"
		h.call(h.ctx, http.MethodPut, "/template-copy", key, input, http.StatusBadRequest, nil)
		h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.edit", key)
	}
}

func TestTemplateCopyNumericMigrationAndOptionalBlankAtHTTPWrite(t *testing.T) {
	h := newMigrationRiskHarness(t, nil)
	h.saveSpec("numeric-base", map[string]string{"research_subject": "1.5", "research_goal": "goal"})
	input := h.edit()
	for i := range input.Fields {
		if input.Fields[i].ID == "research_subject" {
			input.Fields[i].Type = "integer"
		}
	}
	var preview core.TemplateMigrationPreview
	h.call(h.ctx, http.MethodPost, "/template-copy/preview", "", input, http.StatusOK, &preview)
	if !slices.Contains(preview.Incompatible, "research_subject") {
		t.Fatalf("fractional integer not marked incompatible: %+v", preview)
	}
	before, copies, audits := h.snapshot("project_template.edit")
	h.call(h.ctx, http.MethodPut, "/template-copy", "numeric-reject", input, http.StatusBadRequest, nil)
	h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.edit", "numeric-reject")

	h.saveSpec("numeric-repair", map[string]string{"research_subject": "2", "research_goal": "goal"})
	input = h.edit()
	for i := range input.Fields {
		if input.Fields[i].ID == "research_subject" {
			input.Fields[i].Type, input.Fields[i].Required = "integer", false
		}
	}
	input.RequiredFields = slices.DeleteFunc(input.RequiredFields, func(id string) bool { return id == "research_subject" })
	h.call(h.ctx, http.MethodPut, "/template-copy", "numeric-optional", input, http.StatusOK, &h.project)
	h.saveSpec("numeric-blank", map[string]string{"research_subject": "", "research_goal": "goal"})
	input = h.edit()
	input.Fields[0].Label += " updated"
	h.call(h.ctx, http.MethodPost, "/template-copy/preview", "", input, http.StatusOK, &preview)
	if slices.Contains(preview.Incompatible, "research_subject") || slices.Contains(preview.MissingRequired, "research_subject") {
		t.Fatalf("optional blank was incorrectly blocked: %+v", preview)
	}
	h.call(h.ctx, http.MethodPut, "/template-copy", "numeric-blank-edit", input, http.StatusOK, &h.project)
}

func TestChangeTemplateRechecksEachMigrationRiskAtHTTPWrite(t *testing.T) {
	for _, kind := range []string{"missing", "orphaned", "incompatible"} {
		t.Run(kind, func(t *testing.T) {
			target := template.DemoTemplate()
			target.ID, target.Version = "risk-target", "1"
			switch kind {
			case "missing":
				target.Fields = append(target.Fields, template.Field{ID: "new_required", Label: "New field", Type: "string", Required: true})
				target.RequiredFields = append(target.RequiredFields, "new_required")
			case "orphaned":
				target.Fields = slices.DeleteFunc(target.Fields, func(f template.Field) bool { return f.ID == "research_goal" })
				target.RequiredFields = slices.DeleteFunc(target.RequiredFields, func(id string) bool { return id == "research_goal" })
			case "incompatible":
				target.Fields[0].Type, target.Fields[0].Required = "number", false
				target.RequiredFields = slices.DeleteFunc(target.RequiredFields, func(id string) bool { return id == "research_subject" })
			}
			h := newMigrationRiskHarness(t, &target)
			h.saveSpec("change-base-spec", map[string]string{"research_subject": "subject", "research_goal": "goal"})
			input := core.TemplateMigrationInput{ExpectedProjectVersion: h.project.ProjectVersion, TemplateID: target.ID, TemplateVersion: target.Version}
			var preview core.TemplateMigrationPreview
			h.call(h.ctx, http.MethodPost, "/template-migration/preview", "", input, http.StatusOK, &preview)
			if kind == "missing" && !slices.Contains(preview.MissingRequired, "new_required") ||
				kind == "orphaned" && !slices.Contains(preview.Orphaned, "research_goal") ||
				kind == "incompatible" && !slices.Contains(preview.Incompatible, "research_subject") {
				t.Fatalf("%s risk missing from preview: %+v", kind, preview)
			}
			before, copies, audits := h.snapshot("project_template.change")
			h.call(h.ctx, http.MethodPost, "/template-migration", "change-risk", input, http.StatusBadRequest, nil)
			h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.change", "change-risk")
			h.call(h.outsider(), http.MethodPost, "/template-migration", "change-denied", input, http.StatusNotFound, nil)
			h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.change", "change-denied")
			if kind == "orphaned" {
				// Switching away from a field with a value cannot silently discard it.
				return
			}
			fields := map[string]string{"research_subject": "subject", "research_goal": "goal"}
			if kind == "missing" {
				fields["new_required"] = "resolved"
			} else {
				fields["research_subject"] = "42"
			}
			h.saveSpec("change-resolve-spec", fields)
			before, copies, audits = h.snapshot("project_template.change")
			h.call(h.ctx, http.MethodPost, "/template-migration", "change-stale", input, http.StatusConflict, nil)
			h.assertRejectedWithoutBusinessWrites(before, copies, audits, "project_template.change", "change-stale")
			input.ExpectedProjectVersion = h.project.ProjectVersion
			h.call(h.ctx, http.MethodPost, "/template-migration/preview", "", input, http.StatusOK, &preview)
			if len(preview.MissingRequired)+len(preview.Orphaned)+len(preview.Incompatible) != 0 {
				t.Fatalf("resolved switch remains blocked: %+v", preview)
			}
			if h.call(h.ctx, http.MethodPost, "/template-migration", "change-risk", input, http.StatusOK, &h.project) {
				t.Fatal("corrected switch was incorrectly replayed")
			}
			if h.project.TemplateID != target.ID || h.project.TemplateVersion != target.Version {
				t.Fatalf("switch did not persist target: %+v", h.project)
			}
			if !h.call(h.ctx, http.MethodPost, "/template-migration", "change-risk", input, http.StatusOK, nil) {
				t.Fatal("successful switch did not replay")
			}
		})
	}
}
