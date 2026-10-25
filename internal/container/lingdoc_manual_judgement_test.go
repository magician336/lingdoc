package container

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"go.uber.org/dig"
	"gorm.io/gorm"
)

// The manual-judgement channel of the F01 runner, under the PRODUCTION
// composition (#649).
//
// The HTTP seam is proven in the workspace package; what is proven here is
// the part only the composition root can answer: that the narrow port the
// two routes speak to is handed over by name, and that the two endpoints
// are therefore reachable on a production assembly - with the real service
// behind them, not a fake.
//
// Nothing about the judgement itself is seeded: the project, its Owner and
// the tenant member are, and the judgement is recorded by the real domain
// service over the real schema.

const (
	manualJudgementTenantID = 9142
	manualJudgementActorID  = "g4-e2e-admin"
	manualJudgementProject  = "project-manual-judgement-649"
	manualJudgementRunID    = "f01-20261025T013341Z-5105ae32"
	manualJudgementAssertID = "F01-06c"
)

// The production dependency graph hands the narrow port over, and it is the
// very instance that serves the workspace: the same project service, given
// to the route family by name.
func TestLingDocCompositionHandsTheManualJudgementPortToTheRouter(t *testing.T) {
	c := dig.New()
	for _, constructor := range []any{
		func() *gorm.DB { return &gorm.DB{} },
		func() interfaces.KBShareService { return nil },
		func() interfaces.KnowledgeBaseService { return nil },
		func() interfaces.AuditLogService { return nil },
		func() interfaces.ModelService { return nil },
		func() interfaces.KnowledgeService { return nil },
		func() interfaces.WebSearchService { return nil },
		NewLingDocTemplateCatalog,
		NewLingDocWorkspaceHandler,
	} {
		if err := c.Provide(constructor); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Invoke(func(port workspace.ManualJudgementApplication, projects core.ProjectApplication) {
		if port == nil {
			t.Fatal("the production dependency graph provides no manual-judgement port")
		}
		if reflect.ValueOf(port).Pointer() != reflect.ValueOf(projects).Pointer() {
			t.Fatal("the manual-judgement port is not the project service the composition serves")
		}
	}); err != nil {
		t.Fatal(err)
	}
}

// manualJudgementComposition builds the production LingDoc composition over
// a real SQLite database with the production migrations applied.
func manualJudgementComposition(t *testing.T) (LingDocWorkspace, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := openRuleSetSQLite(t, filepath.Join(t.TempDir(), "manual-judgement.sqlite"))
	applyRuleSetSQLiteSchema(t, db)
	composed, err := NewLingDocWorkspaceHandler(db, nil, nil, nil, nil, nil, nil, NewLingDocTemplateCatalog())
	if err != nil {
		t.Fatalf("build production LingDoc workspace composition: %v", err)
	}
	return composed, db
}

// seedManualJudgementProject writes an active project whose Owner is the
// caller.  The judgement itself is not seeded: the domain side records it.
func seedManualJudgementProject(t *testing.T, db *gorm.DB) {
	t.Helper()
	statements := []string{
		`INSERT INTO lingdoc_projects (id, tenant_id, name, status, project_version, spec_revision, spec_json, template_id, template_version)
		 VALUES ('` + manualJudgementProject + `', ` + strconv.Itoa(manualJudgementTenantID) + `, '人工判定验收课题', 'active', 1, 0, '{}', 'template-demo', 'v1')`,
		`INSERT INTO lingdoc_members (project_id, user_id, role) VALUES ('` + manualJudgementProject + `', '` + manualJudgementActorID + `', 'owner')`,
	}
	for _, statement := range statements {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("seed the production project: %v", err)
		}
	}
}

// manualJudgementContext is the caller context of an authenticated web user:
// a member of the tenant who owns the project.
func manualJudgementContext() context.Context {
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, manualJudgementActorID)
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(manualJudgementTenantID))
	ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleOwner)
	return context.WithValue(ctx, types.RequestIDContextKey, "manual-judgement-container")
}

// manualJudgementServe sends one request to the two routes.
func manualJudgementServe(t *testing.T, engine *gin.Engine, method, path, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(manualJudgementContext())
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

// The production composition serves the two endpoints, and the service
// behind them is the real one: a judgement recorded here is recorded by the
// domain side, and the export reads the very same strings back out of it.
func TestLingDocProductionCompositionServesTheManualJudgementRoutes(t *testing.T) {
	composed, db := manualJudgementComposition(t)
	seedManualJudgementProject(t, db)

	if composed.ManualJudgement == nil {
		t.Fatal("the production composition handed over no manual-judgement port")
	}
	if reflect.ValueOf(composed.ManualJudgement).Pointer() != reflect.ValueOf(composed.Projects).Pointer() {
		t.Fatal("the manual-judgement port is not the project service the composition serves")
	}

	engine := gin.New()
	workspace.RegisterManualJudgementRoutes(
		workspace.RouteGroups{Read: engine.Group("/api/v1/lingdoc"), Write: engine.Group("/api/v1/lingdoc")},
		composed.ManualJudgement)

	registered := map[string]bool{}
	for _, route := range engine.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	for _, want := range []string{
		"POST /api/v1/lingdoc/projects/:projectId/manual-judgements",
		"GET /api/v1/lingdoc/projects/:projectId/manual-judgements/audit-records",
	} {
		if !registered[want] {
			t.Fatalf("the production composition does not serve %s: %v", want, registered)
		}
	}

	body := `{"assertion_id":"` + manualJudgementAssertID + `",` +
		`"assertion_text":"变更集评估后才允许应用；服务端的合法前态只有 assessed。",` +
		`"verdict":"passed",` +
		`"run_id":"` + manualJudgementRunID + `",` +
		`"evidence_ref":"run:` + manualJudgementRunID + `/f01-report.json"}`

	recorded := manualJudgementServe(t, engine, http.MethodPost,
		"/api/v1/lingdoc/projects/"+manualJudgementProject+"/manual-judgements",
		body, "manual-judgement-container-1")
	if recorded.Code != http.StatusCreated {
		t.Fatalf("record judgement = %d, want 201: %s", recorded.Code, recorded.Body.String())
	}
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(recorded.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode the authoritative record: %v", err)
	}
	if len(envelope.Data) != 10 {
		t.Fatalf("the authoritative record carries %d fields (%v), want the ten ledger fields", len(envelope.Data), envelope.Data)
	}
	for _, field := range []string{"run_id", "assertion_id", "assertion_text", "verdict", "actor",
		"authorization", "channel", "occurred_at", "evidence_ref", "audit_id"} {
		if value, ok := envelope.Data[field]; !ok || value == "" {
			t.Fatalf("the authoritative record carries no %q: %v", field, envelope.Data)
		}
	}
	if envelope.Data["channel"] != "unverified_channel" {
		t.Fatalf("the recorded channel = %v, want unverified_channel", envelope.Data["channel"])
	}
	if envelope.Data["actor"] != manualJudgementActorID {
		t.Fatalf("the recorded actor = %v, want the authenticated member", envelope.Data["actor"])
	}

	exported := manualJudgementServe(t, engine, http.MethodGet,
		"/api/v1/lingdoc/projects/"+manualJudgementProject+"/manual-judgements/audit-records", "", "")
	if exported.Code != http.StatusOK {
		t.Fatalf("export audit records = %d, want 200: %s", exported.Code, exported.Body.String())
	}
	var export struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(exported.Body.Bytes(), &export); err != nil {
		t.Fatalf("decode the export: %v", err)
	}
	if len(export.Data) != 1 {
		t.Fatalf("the export holds %d records, want the one judgement: %s", len(export.Data), exported.Body.String())
	}
	record := export.Data[0]
	for _, field := range []string{"audit_id", "actor", "authorization", "channel", "occurred_at",
		"assertion_id", "run_id", "evidence_ref", "channel_class", "channel_ui_origin", "channel_automation"} {
		if _, ok := record[field]; !ok {
			t.Fatalf("the export record carries no %q: %v", field, record)
		}
	}
	if len(record) != 11 {
		t.Fatalf("the export record carries %d keys (%v), want eleven", len(record), record)
	}
	// The seven facts the runner compares are equal, byte for byte, between
	// the authoritative record and the exported row.
	for _, field := range []string{"actor", "authorization", "channel", "occurred_at",
		"assertion_id", "run_id", "evidence_ref"} {
		mine, err := json.Marshal(envelope.Data[field])
		if err != nil {
			t.Fatalf("encode the authoritative %q: %v", field, err)
		}
		theirs, err := json.Marshal(record[field])
		if err != nil {
			t.Fatalf("encode the exported %q: %v", field, err)
		}
		if string(mine) != string(theirs) {
			t.Fatalf("the export %q = %s, but the authoritative record says %s", field, theirs, mine)
		}
	}
	if record["channel_ui_origin"] != "unprovable" || record["channel_automation"] != "unprovable" {
		t.Fatalf("the export claims more about the channel than the server can prove: %v", record)
	}
}

// The two endpoints answer a caller who may not see the project with the
// repository's own refusal, not with a missing route.
func TestLingDocProductionManualJudgementRoutesRefuseAnUnknownProject(t *testing.T) {
	composed, _ := manualJudgementComposition(t)
	engine := gin.New()
	workspace.RegisterManualJudgementRoutes(
		workspace.RouteGroups{Read: engine.Group("/api/v1/lingdoc"), Write: engine.Group("/api/v1/lingdoc")},
		composed.ManualJudgement)

	response := manualJudgementServe(t, engine, http.MethodGet,
		"/api/v1/lingdoc/projects/no-such-project/manual-judgements/audit-records", "", "")
	if response.Code == http.StatusNotFound && strings.Contains(response.Body.String(), "page not found") {
		t.Fatalf("the route is not registered: %s", response.Body.String())
	}
	var envelope struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode the refusal: %v", err)
	}
	if envelope.Error == nil || envelope.Error["code"] == "" {
		t.Fatalf("the refusal carries no error code: %s", response.Body.String())
	}
}
