package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type applicationServiceStub struct {
	ApplicationService
	actor   Actor
	called  bool
	project Project
}

type sourceApplicationServiceStub struct {
	SourceApplicationService
	actor     Actor
	projectID string
	input     RetrieveSourcesInput
	err       error
	called    bool
}

func (s *sourceApplicationServiceStub) RetrieveSources(_ context.Context, actor Actor, projectID string, input RetrieveSourcesInput) ([]evidence.Source, error) {
	s.called, s.actor, s.projectID, s.input = true, actor, projectID, input
	return nil, s.err
}

func (s *applicationServiceStub) ListProjects(_ context.Context, actor Actor) ([]Project, bool, error) {
	s.called = true
	s.actor = actor
	return []Project{s.project}, false, nil
}

func TestHandlerDelegatesSourceUseCaseAndMapsDeniedDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sources := &sourceApplicationServiceStub{err: &DeniedAssetsError{
		Denied: []evidence.DeniedAsset{{AssetID: "asset-private", Reason: evidence.DenyNotAuthorized}},
	}}
	handler := NewHandler(HandlerDependencies{Sources: sources})
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api/lingdoc"), Write: engine.Group("/api/lingdoc")})

	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "user-7")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
	request := httptest.NewRequest(http.MethodPost, "/api/lingdoc/projects/project-1/retrieval", strings.NewReader(`{"query":"budget","asset_ids":["asset-private"]}`)).WithContext(ctx)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusUnprocessableEntity, response.Body.String())
	}
	if !sources.called || sources.actor != (Actor{TenantID: 42, UserID: "user-7"}) || sources.projectID != "project-1" || sources.input.Query != "budget" || len(sources.input.AssetIDs) != 1 || sources.input.AssetIDs[0] != "asset-private" {
		t.Fatalf("source use case input = called:%v actor:%#v project:%q input:%#v", sources.called, sources.actor, sources.projectID, sources.input)
	}
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Denied []struct {
					AssetID string `json:"asset_id"`
					Reason  string `json:"reason"`
				} `json:"denied"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if body.Error.Code != "asset_not_authorized" || len(body.Error.Details.Denied) != 1 || body.Error.Details.Denied[0].AssetID != "asset-private" || body.Error.Details.Denied[0].Reason != "not_authorized" {
		t.Fatalf("error response = %#v, want the stable item-level denial contract", body.Error)
	}
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
