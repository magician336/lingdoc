package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type focusedReassessApplication struct {
	ChangeSetApplication
	result                      ChangeSetMutationResult
	err                         error
	actor                       Actor
	projectID, changeSetID, key string
}

func (s *focusedReassessApplication) ReassessChangeSetResult(_ context.Context, actor Actor, projectID, changeSetID, key string) (ChangeSetMutationResult, error) {
	s.actor, s.projectID, s.changeSetID, s.key = actor, projectID, changeSetID, key
	return s.result, s.err
}

func TestFocusedChangeSetReassessKeepsTypedHTTPContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name     string
		replayed bool
		err      error
		status   int
	}{
		{name: "first response", status: http.StatusCreated},
		{name: "replay", replayed: true, status: http.StatusCreated},
		{name: "conflict", err: ErrVersionConflict, status: http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := &focusedReassessApplication{result: ChangeSetMutationResult{ChangeSet: ChangeSet{ID: "reassessed-1"}, Replayed: test.replayed}, err: test.err}
			handler := NewHandler(HandlerDependencies{ChangeSets: app})
			engine := gin.New()
			handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
			ctx := context.WithValue(context.Background(), types.UserIDContextKey, "owner-1")
			ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
			request := httptest.NewRequest(http.MethodPost, "/api/projects/project-1/change-sets/changes-1/reassess", nil).WithContext(ctx)
			request.Header.Set("Idempotency-Key", "reassess-action-1")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.status, response.Body.String())
			}
			if app.actor.TenantID != 42 || app.actor.UserID != "owner-1" || app.projectID != "project-1" || app.changeSetID != "changes-1" || app.key != "reassess-action-1" {
				t.Fatalf("focused reassess received incorrect actor or operation identity: %+v", app)
			}
			var envelope struct {
				Data *ChangeSet `json:"data"`
				Meta struct {
					Replayed        bool `json:"replayed"`
					RefreshRequired bool `json:"refresh_required"`
				} `json:"meta"`
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if test.err != nil {
				if envelope.Data != nil || envelope.Error.Code != "version_conflict" {
					t.Fatalf("conflict leaked a result or changed domain error mapping: %s", response.Body.String())
				}
				return
			}
			if envelope.Data == nil || envelope.Data.ID != "reassessed-1" || envelope.Meta.Replayed != test.replayed || envelope.Meta.RefreshRequired != test.replayed {
				t.Fatalf("typed result/replay metadata changed: %s", response.Body.String())
			}
		})
	}
}

func TestFocusedPortsRetainG5G7CompatibilityBridge(t *testing.T) {
	legacy := &machineHTTPTestApplication{}
	projects := &focusedProjectPortStub{}
	handler := NewHandler(HandlerDependencies{Service: legacy, Projects: projects})
	if handler.service != legacy || handler.projects != projects {
		t.Fatalf("G5/G7 bridge replaced an explicitly supplied focused port: service=%T projects=%T", handler.service, handler.projects)
	}
}
