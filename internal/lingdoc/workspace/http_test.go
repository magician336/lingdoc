package workspace

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type applicationServiceStub struct {
	ApplicationService
	actor   Actor
	called  bool
	project Project
}

func (s *applicationServiceStub) ListProjects(_ context.Context, actor Actor) ([]Project, bool, error) {
	s.called = true
	s.actor = actor
	return []Project{s.project}, false, nil
}

func TestHandlerUsesInjectedApplicationService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	service := &applicationServiceStub{project: Project{ID: "project-1", Name: "demo"}}
	handler := NewHandler(HandlerDependencies{Service: service})
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})

	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "user-7")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
	request := httptest.NewRequest(http.MethodGet, "/api/projects", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body.String())
	}
	if !service.called || service.actor != (Actor{TenantID: 42, UserID: "user-7"}) {
		t.Fatalf("injected service did not receive caller identity: called=%v actor=%#v", service.called, service.actor)
	}
}

