package workspace

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type machineHTTPTestApplication struct {
	ProjectApplication
	MembershipApplication
	ChapterApplication
	TemplateApplication
	ChangeSetApplication
	seen       types.Principal
	seenCaller types.Caller
}

func (s *machineHTTPTestApplication) ReadMachineProjectSummary(ctx context.Context, pid string) (core.MachineProjectSummary, error) {
	s.seen, _ = types.PrincipalFromContext(ctx)
	s.seenCaller = types.CallerFromContext(ctx)
	key, known := types.TenantAPIKeyScopeFromContext(ctx)
	if !known || key.KeyID != 17 || s.seen.Type != types.PrincipalAPITenant {
		return core.MachineProjectSummary{}, core.ErrNotFound
	}
	return core.MachineProjectSummary{ID: pid, Name: "synthetic machine summary", Status: "active", ProjectVersion: 3}, nil
}
func (s *machineHTTPTestApplication) ReadMachineChapter(ctx context.Context, pid, cid string) (core.MachineChapterView, error) {
	if _, err := s.ReadMachineProjectSummary(ctx, pid); err != nil {
		return core.MachineChapterView{}, err
	}
	return core.MachineChapterView{ID: cid, ProjectID: pid, Title: "synthetic chapter", BodyMarkdown: "synthetic authorized body", SourceIDs: []string{}}, nil
}
func TestMachineReadHTTPPreservesAuthenticatedPrincipalAndMinimalShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	app := &machineHTTPTestApplication{}
	engine := gin.New()
	engine.Use(func(c *gin.Context) {
		ctx := types.WithPrincipal(c.Request.Context(), types.Principal{Type: types.PrincipalAPITenant, ID: "1"})
		ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(1))
		ctx = types.WithTenantAPIKeyScope(ctx, types.TenantAPIKeyScope{KeyID: 17, ScopeType: types.APIKeyScopeTenant,
			Capabilities: types.StringArray{string(types.APIKeyCapabilityRetrieve)}})
		ctx = types.WithCaller(ctx, types.Caller{TenantID: 1, UserID: "legacy-compatibility-alias", Role: types.TenantRoleViewer})
		c.Request = c.Request.WithContext(ctx)
	})
	deps := contextPorts(app, emptySourcesStub{})
	// The machine read capability is named by the caller.
	deps.WithMachineReads(app)
	handler, err := NewHandler(deps)
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
	handler.RegisterMachineReadRoutes(engine.Group("/lingdoc"))
	for _, path := range []string{"/lingdoc/projects/p/machine-read", "/lingdoc/projects/p/chapters/c/machine-read"} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != 200 {
			t.Fatalf("machine GET: %d %s", response.Code, response.Body.String())
		}
		for _, privateField := range []string{"members", "function_scopes", "spec_fields", "grantor_id"} {
			if strings.Contains(response.Body.String(), privateField) {
				t.Fatalf("machine response exposed governance field %s: %s", privateField, response.Body.String())
			}
		}
		if app.seen.Type != types.PrincipalAPITenant || app.seen.ID != "1" || app.seenCaller.UserID != "legacy-compatibility-alias" {
			t.Fatalf("HTTP replaced machine/Caller identity: %+v %+v", app.seen, app.seenCaller)
		}
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/lingdoc/projects/p/machine-read", nil))
	if response.Code != 404 {
		t.Fatalf("unexpected machine write route: %d", response.Code)
	}
}
func TestMachineReadSourceBridgeMissingDependenciesFailsClosed(t *testing.T) {
	if err := (workspaceSourcePolicy{}).ValidateMachineRead(context.Background(), core.MachineReadAuthorization{}); !errors.Is(err, core.ErrMachineReadDependency) {
		t.Fatalf("missing source bridge allowed: %v", err)
	}
}
