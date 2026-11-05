package generation

import (
	"context"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"testing"
)

type generationApplicationStub struct {
	Application
	actor        Actor
	project, run string
	err          error
}

func (s *generationApplicationStub) Get(_ context.Context, actor Actor, project, run string) (Run, error) {
	s.actor, s.project, s.run = actor, project, run
	return Run{ID: run}, s.err
}
func TestGenerationHandlerUsesApplicationPort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		err    error
		status int
	}{
		{nil, http.StatusOK}, {ErrNotFound, http.StatusNotFound}, {ErrDependencyUnavailable, http.StatusServiceUnavailable},
	} {
		service := &generationApplicationStub{err: test.err}
		actor := Actor{UserID: "actor-1", TenantID: 1}
		handler := NewHandler(service, func(*gin.Context) (Actor, bool) { return actor, true })
		router := gin.New()
		RegisterRoutes(router.Group("/lingdoc"), handler)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/lingdoc/projects/p1/generations/r1", nil))
		if response.Code != test.status || service.actor != actor || service.project != "p1" || service.run != "r1" {
			t.Fatalf("status=%d body=%s input=%+v", response.Code, response.Body.String(), service)
		}
	}
}
