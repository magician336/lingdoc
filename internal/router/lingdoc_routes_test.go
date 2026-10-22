package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

func TestAllLingDocRoutesDeclareAPIKeyPolicies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	enabled := true
	guards := &rbacGuards{cfg: &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}}
	routes := newLingDocRegistrar(v1, guards)
	service := &lingDocRouteServiceStub{}
	deps := workspace.HandlerDependencies{
		Projects:    service,
		Members:     service,
		Chapters:    service,
		TemplateApp: service,
		ChangeSets:  service,
		Sources:     routeSourcesStub{},
	}
	// The machine read and the review anchor endpoints are capabilities the
	// caller names, so their presence is a decision rather than an accident.
	deps.WithMachineReads(service)
	deps.WithReviewAnchors(service, service)
	// The rewrite capability carries the review endpoints with it: a language
	// review is a report, and its application entry point lives beside the
	// rewrite one.
	deps.WithSelectedRewrites(&lingDocRewriteRouteStub{})
	lingdocHandler, err := workspace.NewHandler(deps)
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
	registerLingDocWorkspaceRoutes(v1, guards, lingdocHandler)
	registerLingDocRuleSetRoutes(v1, guards, rulesetstore.NewHTTPHandler(rulesetstore.NewStore(nil, nil), testRuleSetRequestInfo))
	workspace.RegisterDeliveryRoutesWithGroups(routes.groups(), &workspace.DeliveryHandler{})
	workspace.RegisterDeliveryExportRoutes(routes.write, &workspace.DeliveryExportHandler{})
	candidateadoption.RegisterRoutes(routes.write, candidateadoption.NewCandidateAdoptionHandler(nil, nil, nil))
	generation.RegisterRoutes(routes.write, generation.NewHandler(nil, nil))
	guards.assertAPIKeyPoliciesMatchRoutes(engine)
	// The baseline is the route surface a fully-capable composition root
	// serves: the six required ports plus the machine read and the review
	// anchor capabilities, plus the rewrite capability and the review
	// endpoints it carries. The base tree measured the same 70 routes with the
	// same capability set, so the surface did not change. The older 68 came
	// from a handler assembled with no ports at all, which never had the
	// machine read endpoints - the production composition root always had
	// them. The project archive entry adds one route of its own.
	if got := len(engine.Routes()); got != 79 {
		t.Fatalf("registered %d LingDoc routes, want 79", got)
	}
	for _, route := range engine.Routes() {
		policy, ok := guards.apiKeyAuthorizer.Lookup(route.Method, route.Path)
		if !ok || !policy.RequireFullAccess {
			t.Errorf("missing full-access policy: %s %s", route.Method, route.Path)
		}
	}
}

type lingDocRouteServiceStub struct {
	core.ProjectApplication
	core.CapabilityAuthorizer
	core.GenerationContextApplication
	workspace.MembershipApplication
	workspace.ChapterApplication
	workspace.TemplateApplication
	workspace.ChangeSetApplication
	createCalls int
	listCalls   int
}

// lingDocRewriteRouteStub 站在改写与评审这两条能力的门口。
//
// 路由面测试要的是「这些端点在生产装配里真的存在」：改写能力带着评审的四个
// 端点一起注册，因此这个桩同时实现两组接口。
type lingDocRewriteRouteStub struct{}

func (*lingDocRewriteRouteStub) Create(context.Context, workspace.Actor, string, string, string, workspace.SelectedRewriteRequest) (workspace.SelectedRewriteCandidate, error) {
	return workspace.SelectedRewriteCandidate{}, nil
}

func (*lingDocRewriteRouteStub) Get(context.Context, workspace.Actor, string, string) (workspace.SelectedRewriteCandidate, error) {
	return workspace.SelectedRewriteCandidate{}, nil
}

func (*lingDocRewriteRouteStub) Apply(context.Context, workspace.Actor, string, string, string, string, workspace.ApplySelectedRewriteInput) (core.WorkingCopy, bool, error) {
	return core.WorkingCopy{}, false, nil
}

func (*lingDocRewriteRouteStub) CreateReviewReport(context.Context, workspace.Actor, string, string, string, workspace.ReviewReportRequest) (workspace.ReviewReport, error) {
	return workspace.ReviewReport{}, nil
}

func (*lingDocRewriteRouteStub) GetReviewReport(context.Context, workspace.Actor, string, string) (workspace.ReviewReport, error) {
	return workspace.ReviewReport{}, nil
}

func (*lingDocRewriteRouteStub) RejectReviewReport(context.Context, workspace.Actor, string, string, string) (workspace.ReviewReport, error) {
	return workspace.ReviewReport{}, nil
}

func (*lingDocRewriteRouteStub) ApplyReviewReport(context.Context, workspace.Actor, string, string, string, string, workspace.ApplyReviewInput) (core.WorkingCopy, bool, error) {
	return core.WorkingCopy{}, false, nil
}

// routeSourcesStub stands in for the sources port when a test does not
// exercise the source routes. The port is required, so a handler assembled
// without it would fail at startup; this keeps the other routes usable.
type routeSourcesStub struct {
	workspace.SourceApplicationService
}

// The machine read and the review anchor capabilities are named by the
// caller, so the stub carries them: the route surface this test snapshots
// is the one a fully-capable composition root serves.
func (s *lingDocRouteServiceStub) ReadMachineProjectSummary(context.Context, string) (core.MachineProjectSummary, error) {
	return core.MachineProjectSummary{ID: "p1", Name: "demo"}, nil
}
func (s *lingDocRouteServiceStub) ReadMachineChapter(context.Context, string, string) (core.MachineChapterView, error) {
	return core.MachineChapterView{ID: "c1", ProjectID: "p1"}, nil
}
func (s *lingDocRouteServiceStub) SetReviewAnchor(context.Context, workspace.Actor, string, string, string, string, core.SetReviewAnchorInput) (json.RawMessage, int, bool, error) {
	return json.RawMessage(`{"project_id":"p1"}`), http.StatusOK, false, nil
}
func (s *lingDocRouteServiceStub) GetCurrentConfirmation(context.Context, workspace.Actor, string, string) (json.RawMessage, error) {
	return json.RawMessage(`null`), nil
}
func (s *lingDocRouteServiceStub) ListProjects(context.Context, workspace.Actor) ([]workspace.Project, bool, error) {
	s.listCalls++
	return []workspace.Project{{ID: "p1", Name: "demo"}}, false, nil
}

func (s *lingDocRouteServiceStub) CreateProjectResult(_ context.Context, _ workspace.Actor, _ string, input workspace.CreateProjectInput) (workspace.CreateProjectResult, error) {
	s.createCalls++
	return workspace.CreateProjectResult{Project: workspace.Project{ID: "p2", Name: input.Name}}, nil
}

func TestLingDocRoutesDeclareRoleAndAPIKeyPolicies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	enabled := true
	guards := &rbacGuards{cfg: &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}, apiKeyAuthorizer: middleware.NewAPIKeyRouteAuthorizer()}
	v1.Use(guards.apiKeyAuthorizer.Middleware())
	service := &lingDocRouteServiceStub{}
	deps := workspace.HandlerDependencies{
		Projects:    service,
		Members:     service,
		Chapters:    service,
		TemplateApp: service,
		ChangeSets:  service,
		Sources:     routeSourcesStub{},
	}
	// The machine read and the review anchor endpoints are capabilities the
	// caller names, so their presence is a decision rather than an accident.
	deps.WithMachineReads(service)
	deps.WithReviewAnchors(service, service)
	lingdocHandler, err := workspace.NewHandler(deps)
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
	registerLingDocWorkspaceRoutes(v1, guards, lingdocHandler)
	registerLingDocRuleSetRoutes(v1, guards, rulesetstore.NewHTTPHandler(rulesetstore.NewStore(nil, nil), testRuleSetRequestInfo))
	guards.assertAPIKeyPoliciesMatchRoutes(engine)

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/lingdoc/templates/:templateId"},
		{http.MethodGet, "/api/v1/lingdoc/projects"},
		{http.MethodPost, "/api/v1/lingdoc/projects"},
		{http.MethodPost, "/api/v1/lingdoc/projects/:projectId/retrieval"},
		{http.MethodGet, "/api/v1/lingdoc/projects/:projectId/chapters/:chapterId/confirmations/current"},
		{http.MethodGet, "/api/v1/lingdoc/projects/:projectId/chapters/:chapterId/working-copy"},
		{http.MethodPut, "/api/v1/lingdoc/projects/:projectId/chapters/:chapterId/working-copy"},
		{http.MethodPut, "/api/v1/lingdoc/projects/:projectId/chapters/:chapterId/working-copy/review-items/:itemId/anchor"},
		{http.MethodPost, "/api/v1/lingdoc/projects/:projectId/chapters/:chapterId/working-copy/commit"},
		{http.MethodGet, "/api/v1/lingdoc/rule-sets/definitions"},
		{http.MethodGet, "/api/v1/lingdoc/rule-sets/:ruleSetID/versions"},
		{http.MethodGet, "/api/v1/lingdoc/projects/:projectId/rule-set-use-audits"},
		{http.MethodGet, "/api/v1/lingdoc/projects/:projectId/rule-set-use-audit-metrics"},
		{http.MethodPost, "/api/v1/lingdoc/rule-sets"},
		{http.MethodPost, "/api/v1/lingdoc/rule-sets/:ruleSetID/versions/:version/publish"},
	} {
		policy, ok := guards.apiKeyAuthorizer.Lookup(route.method, route.path)
		if !ok || !policy.RequireFullAccess {
			t.Errorf("API-key policy for %s %s = %#v, registered=%v; want full access", route.method, route.path, policy, ok)
		}
	}
}

type ruleSetRouteStub struct {
	rulesetstore.Application
	createCalls int
}

func (s *ruleSetRouteStub) CreateDraft(context.Context, rulesetstore.Actor, template.RuleSet) error {
	s.createCalls++
	return nil
}

func TestLingDocRuleSetWritesRequireTenantAdmin(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	enabled := true
	guards := &rbacGuards{cfg: &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}, apiKeyAuthorizer: middleware.NewAPIKeyRouteAuthorizer()}
	v1.Use(guards.apiKeyAuthorizer.Middleware())
	service := &ruleSetRouteStub{}
	registerLingDocRuleSetRoutes(v1, guards, rulesetstore.NewHTTPHandler(service, testRuleSetRequestInfo))
	guards.assertAPIKeyPoliciesMatchRoutes(engine)

	request := func(role types.TenantRole, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		ctx := context.WithValue(context.Background(), types.UserIDContextKey, "user-1")
		ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(1))
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, role)
		req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
		res := httptest.NewRecorder()
		engine.ServeHTTP(res, req)
		return res
	}
	body := `{"ruleset_id":"route-rules","version":"v1","bindings":[]}`
	for _, role := range []types.TenantRole{types.TenantRoleViewer, types.TenantRoleContributor} {
		if res := request(role, http.MethodPost, "/api/v1/lingdoc/rule-sets", body); res.Code != http.StatusForbidden {
			t.Fatalf("%s POST rulesets = %d %s, want 403", role, res.Code, res.Body.String())
		}
	}
	if service.createCalls != 0 {
		t.Fatalf("denied callers reached application %d times, want 0", service.createCalls)
	}
	if res := request(types.TenantRoleAdmin, http.MethodPost, "/api/v1/lingdoc/rule-sets", body); res.Code != http.StatusCreated {
		t.Fatalf("admin POST rulesets = %d %s, want 201", res.Code, res.Body.String())
	}
	if service.createCalls != 1 {
		t.Fatalf("admin application calls = %d, want 1", service.createCalls)
	}
}

func testRuleSetRequestInfo(c *gin.Context) (rulesetstore.Actor, string, bool) {
	ctx := c.Request.Context()
	userID, userOK := types.UserIDFromContext(ctx)
	tenantID, tenantOK := types.TenantIDFromContext(ctx)
	requestID, _ := types.RequestIDFromContext(ctx)
	if !userOK || !tenantOK {
		return rulesetstore.Actor{}, requestID, false
	}
	return rulesetstore.Actor{TenantID: tenantID, UserID: userID, Role: string(types.TenantRoleFromContext(ctx))}, requestID, true
}

func TestLingDocRegistrarUsesExplicitPostPolicyGroups(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	enabled := true
	guards := &rbacGuards{cfg: &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}, apiKeyAuthorizer: middleware.NewAPIKeyRouteAuthorizer()}
	v1.Use(guards.apiKeyAuthorizer.Middleware())
	routes := newLingDocRegistrar(v1, guards)
	routeGroups := routes.groups()
	routeGroups.Read.POST("/projects/:projectId/read-probe", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	routeGroups.Write.POST("/projects/:projectId/write-probe", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	request := func(path string, role types.TenantRole) int {
		ctx := context.WithValue(context.Background(), types.UserIDContextKey, "user-1")
		ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(1))
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, role)
		req := httptest.NewRequest(http.MethodPost, path, nil).WithContext(ctx)
		res := httptest.NewRecorder()
		engine.ServeHTTP(res, req)
		return res.Code
	}
	if got := request("/api/v1/lingdoc/projects/p1/read-probe", types.TenantRoleViewer); got != http.StatusNoContent {
		t.Fatalf("viewer read probe = %d, want %d", got, http.StatusNoContent)
	}
	if got := request("/api/v1/lingdoc/projects/p1/write-probe", types.TenantRoleViewer); got != http.StatusForbidden {
		t.Fatalf("viewer write probe = %d, want %d", got, http.StatusForbidden)
	}
	if got := request("/api/v1/lingdoc/projects/p1/write-probe", types.TenantRoleContributor); got != http.StatusNoContent {
		t.Fatalf("contributor write probe = %d, want %d", got, http.StatusNoContent)
	}
}

func TestLingDocWorkspaceRouteRunsThroughRouterRoleGuards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	enabled := true
	guards := &rbacGuards{cfg: &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}, apiKeyAuthorizer: middleware.NewAPIKeyRouteAuthorizer()}
	v1.Use(guards.apiKeyAuthorizer.Middleware())
	service := &lingDocRouteServiceStub{}
	workspaceHandler, err := workspace.NewHandler(workspace.HandlerDependencies{
		Projects:    service,
		Members:     service,
		Chapters:    service,
		TemplateApp: service,
		ChangeSets:  service,
		Sources:     routeSourcesStub{},
	})
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
	registerLingDocWorkspaceRoutes(v1, guards, workspaceHandler)
	guards.assertAPIKeyPoliciesMatchRoutes(engine)

	requestWithAPIKey := func(method, path string, role types.TenantRole, scope *types.TenantAPIKeyScope) *httptest.ResponseRecorder {
		ctx := context.WithValue(context.Background(), types.UserIDContextKey, "user-1")
		ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(1))
		ctx = context.WithValue(ctx, types.TenantRoleContextKey, role)
		if scope != nil {
			ctx = types.WithTenantAPIKeyScope(ctx, *scope)
		}
		body := ""
		if method == http.MethodPost {
			body = `{}`
		}
		req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
		if method == http.MethodPost {
			req.Header.Set("Idempotency-Key", "create-project-1")
		}
		res := httptest.NewRecorder()
		engine.ServeHTTP(res, req)
		return res
	}
	request := func(method, path string, role types.TenantRole) *httptest.ResponseRecorder {
		return requestWithAPIKey(method, path, role, nil)
	}

	read := request(http.MethodGet, "/api/v1/lingdoc/projects", types.TenantRoleViewer)
	if read.Code != http.StatusOK || !strings.Contains(read.Body.String(), `"name":"demo"`) {
		t.Fatalf("viewer read = %d %s; want routed success", read.Code, read.Body.String())
	}
	deniedWrite := request(http.MethodPost, "/api/v1/lingdoc/projects", types.TenantRoleViewer)
	if deniedWrite.Code != http.StatusForbidden || service.createCalls != 0 {
		t.Fatalf("viewer write = %d calls=%d, want 403 without application call", deniedWrite.Code, service.createCalls)
	}
	write := request(http.MethodPost, "/api/v1/lingdoc/projects", types.TenantRoleContributor)
	if write.Code != http.StatusCreated || service.createCalls != 1 {
		t.Fatalf("contributor write = %d calls=%d body=%s, want one routed create", write.Code, service.createCalls, write.Body.String())
	}

	deniedScopedKey := requestWithAPIKey(http.MethodGet, "/api/v1/lingdoc/projects", types.TenantRoleViewer,
		&types.TenantAPIKeyScope{Capabilities: types.StringArray{string(types.APIKeyCapabilityChat)}})
	if deniedScopedKey.Code != http.StatusForbidden || service.listCalls != 1 {
		t.Fatalf("scoped API key read = %d calls=%d, want 403 before application call", deniedScopedKey.Code, service.listCalls)
	}
	fullAccessKey := requestWithAPIKey(http.MethodGet, "/api/v1/lingdoc/projects", types.TenantRoleViewer,
		&types.TenantAPIKeyScope{FullAccess: true})
	if fullAccessKey.Code != http.StatusOK || service.listCalls != 2 {
		t.Fatalf("full-access API key read = %d calls=%d, want routed success", fullAccessKey.Code, service.listCalls)
	}
}
