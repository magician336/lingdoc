package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type g5C1SaveSpecErrorStub struct {
	ApplicationService
	err error
}

func (s *g5C1SaveSpecErrorStub) SaveSpecResult(context.Context, Actor, string, string, SaveSpecInput) (SaveSpecResult, error) {
	return SaveSpecResult{}, s.err
}

// TestG5C1RepresentativeHTTPSpecContract pins the transport half of the
// characterization matrix. The handler consumes the focused write port and
// maps typed application outcomes without asking the domain for an HTTP code.
func TestG5C1RepresentativeHTTPSpecContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.WithValue(context.Background(), types.UserIDContextKey, "owner-7")
	ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))

	t.Run("replay keeps transport envelope", func(t *testing.T) {
		service := &saveSpecApplicationStub{result: SaveSpecResult{
			Project:  Project{ID: "project-1", SpecRevision: 1, ProjectVersion: 2},
			Replayed: true,
		}}
		handler := NewHandler(HandlerDependencies{Write: service})
		engine := gin.New()
		handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})

		request := httptest.NewRequest(http.MethodPut, "/api/projects/project-1/spec", strings.NewReader(`{"expected_spec_revision":0,"fields":{"research_subject":"subject"}}`)).WithContext(ctx)
		request.Header.Set("Idempotency-Key", "g5-c1-http-replay")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)

		if response.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
		}
		var envelope struct {
			Data Project `json:"data"`
			Meta struct {
				Replayed        bool `json:"replayed"`
				RefreshRequired bool `json:"refresh_required"`
			} `json:"meta"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode replay envelope: %v", err)
		}
		if envelope.Data.ID != "project-1" || !envelope.Meta.Replayed || !envelope.Meta.RefreshRequired {
			t.Fatalf("replay envelope = %+v, want typed project and refresh metadata", envelope)
		}
	})

	t.Run("version conflict remains a domain error mapping", func(t *testing.T) {
		service := &g5C1SaveSpecErrorStub{err: ErrVersionConflict}
		handler := NewHandler(HandlerDependencies{Write: service})
		engine := gin.New()
		handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})

		request := httptest.NewRequest(http.MethodPut, "/api/projects/project-1/spec", strings.NewReader(`{"expected_spec_revision":0,"fields":{"research_subject":"subject"}}`)).WithContext(ctx)
		request.Header.Set("Idempotency-Key", "g5-c1-http-conflict")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)

		if response.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409; body=%s", response.Code, response.Body.String())
		}
		var envelope struct {
			Data  json.RawMessage `json:"data"`
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode conflict envelope: %v", err)
		}
		if envelope.Error.Code != "version_conflict" || len(envelope.Data) != 0 {
			t.Fatalf("conflict envelope = %+v, want version_conflict without data", envelope)
		}
	})
}
