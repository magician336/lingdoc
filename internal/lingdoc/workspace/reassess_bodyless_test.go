package workspace

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type r25UnreadableReassessBody struct{}

func (r25UnreadableReassessBody) Read([]byte) (int, error) {
	return 0, errors.New("fixture body read failed")
}
func (r25UnreadableReassessBody) Close() error { return nil }

func TestReassessChangeSetValidatesActualBodyNotLengthHint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name       string
		body       io.ReadCloser
		length     int64
		wantStatus int
	}{
		{"empty-unknown-length", http.NoBody, -1, http.StatusCreated},
		{"bytes-with-zero-length-hint", io.NopCloser(strings.NewReader(`{}`)), 0, http.StatusBadRequest},
		{"whitespace-with-zero-length-hint", io.NopCloser(strings.NewReader(" ")), 0, http.StatusBadRequest},
		{"bytes-with-unknown-length", io.NopCloser(strings.NewReader(`{}`)), -1, http.StatusBadRequest},
		{"body-read-failure", r25UnreadableReassessBody{}, -1, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &applicationServiceStub{}
			handler, err := NewHandler(HandlerDependencies{Projects: service, Members: service, Chapters: service, TemplateApp: service, ChangeSets: service, Sources: emptySourcesStub{}})
			if err != nil {
				t.Fatalf("assemble the workspace handler: %v", err)
			}
			engine := gin.New()
			handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
			ctx := context.WithValue(context.Background(), types.UserIDContextKey, "owner-7")
			ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
			request := httptest.NewRequest(http.MethodPost, "/api/projects/project-1/change-sets/change-1/reassess", nil).WithContext(ctx)
			request.Body, request.ContentLength = test.body, test.length
			request.Header.Set("Idempotency-Key", "reassess-bodyless-001")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			wantDelegated := test.wantStatus == http.StatusCreated
			if response.Code != test.wantStatus || service.reassessCalled != wantDelegated {
				t.Fatalf("status=%d want=%d delegated=%v want=%v body=%s", response.Code, test.wantStatus, service.reassessCalled, wantDelegated, response.Body.String())
			}
			if wantDelegated && (service.changeSetID != "change-1" || service.changeSetKey != "reassess-bodyless-001" || service.actor.UserID != "owner-7" || service.actor.TenantID != 42) {
				t.Fatalf("bodyless request lost actor/path/key: %+v", service)
			}
		})
	}
}
