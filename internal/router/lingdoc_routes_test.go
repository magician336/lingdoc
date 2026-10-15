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
	registerLingDocWorkspaceRoutes(v1, guards, workspace.NewHandler(workspace.HandlerDependencies{}))
	registerLingDocRuleSetRoutes(v1, guards, rulesetstore.NewHTTPHandler(rulesetstore.NewStore(nil, nil), testRuleSetRequestInfo))
	workspace.RegisterDeliveryRoutes(routes, &workspace.DeliveryHandler{})
	workspace.RegisterDeliveryExportRoutes(routes, &workspace.DeliveryExportHandler{})
	candidateadoption.RegisterRoutes(routes, candidateadoption.NewCandidateAdoptionHandler(nil, nil, nil))
	generation.RegisterRoutes(routes, generation.NewHandler(nil, nil))
	guards.assertAPIKeyPoliciesMatchRoutes(engine)
	if got := len(engine.Routes()); got != 62 {
		t.Fatalf("registered %d LingDoc routes, want 62", got)
	}
	for _, route := range engine.Routes() {
		policy, ok := guards.apiKeyAuthorizer.Lookup(route.Method, route.Path)
		if !ok || !policy.RequireFullAccess {
			t.Errorf("missing full-access policy: %s %s", route.Method, route.Path)
		}
	}
}

type lingDocRouteServiceStub struct {
	workspace.ApplicationService
	createCalls int
	listCalls   int
}

func (s *lingDocRouteServiceStub) ListProjects(context.Context, workspace.Actor) ([]workspace.Project, bool, error) {
	s.listCalls++
	return []workspace.Project{{ID: "p1", Name: "demo"}}, false, nil
}

func (s *lingDocRouteServiceStub) CreateProject(_ context.Context, _ workspace.Actor, _ string, _ workspace.CreateProjectInput) (json.RawMessage, int, bool, error) {
	s.createCalls++
	return json.RawMessage(`{"id":"p2"}`), http.StatusCreated, false, nil
}

func TestLingDocRoutesDeclareRoleAndAPIKeyPolicies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	enabled := true
	guards := &rbacGuards{cfg: &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}, apiKeyAuthorizer: middleware.NewAPIKeyRouteAuthorizer()}
	v1.Use(guards.apiKeyAuthorizer.Middleware())
	registerLingDocWorkspaceRoutes(v1, guards, workspace.NewHandler(workspace.HandlerDependencies{}))
	registerLingDocRuleSetRoutes(v1, guards, rulesetstore.NewHTTPHandler(rulesetstore.NewStore(nil, nil), testRuleSetRequestInfo))
	guards.assertAPIKeyPoliciesMatchRoutes(engine)

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/lingdoc/templates/:templateId"},
		{http.MethodGet, "/api/v1/lingdoc/projects"},
		{http.MethodPost, "/api/v1/lingdoc/projects"},
		{http.MethodPost, "/api/v1/lingdoc/projects/:projectId/retrieval"},
		{http.MethodGet, "/api/v1/lingdoc/projects/:projectId/chapters/:chapterId/working-copy"},
		{http.MethodPut, "/api/v1/lingdoc/projects/:projectId/chapters/:chapterId/working-copy"},
		{http.MethodPost, "/api/v1/lingdoc/projects/:projectId/chapters/:chapterId/working-copy/commit"},
		{http.MethodGet, "/api/v1/lingdoc/rule-sets/definitions"},
		{http.MethodGet, "/api/v1/lingdoc/rule-sets/:ruleSetID/versions"},
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

func TestLingDocWorkspaceRouteRunsThroughRouterRoleGuards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	enabled := true
	guards := &rbacGuards{cfg: &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}, apiKeyAuthorizer: middleware.NewAPIKeyRouteAuthorizer()}
	v1.Use(guards.apiKeyAuthorizer.Middleware())
	service := &lingDocRouteServiceStub{}
	workspaceHandler := workspace.NewHandler(workspace.HandlerDependencies{Service: service})
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
