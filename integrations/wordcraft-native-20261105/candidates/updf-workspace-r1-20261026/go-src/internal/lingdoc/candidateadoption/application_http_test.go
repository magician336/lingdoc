package candidateadoption

import (
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"testing"
)

type candidateApplicationStub struct {
	Application
	actor, project, candidate string
	err                       error
}

func (s *candidateApplicationStub) ReadCandidate(_ context.Context, actor, project, candidate string) (Candidate, error) {
	s.actor, s.project, s.candidate = actor, project, candidate
	return Candidate{ID: candidate}, s.err
}
func TestCandidateHandlerUsesApplicationPort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{"success", nil, http.StatusOK},
		{"source revoked", ErrSourceAccessDenied, http.StatusForbidden},
		{"hidden project", ErrNotFound, http.StatusNotFound},
		{"dependency failure", errors.Join(ErrDependencyUnavailable, errors.New("adapter unavailable")), http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &candidateApplicationStub{err: test.err}
			handler := NewCandidateAdoptionHandler(service, nil, func(*gin.Context) (string, bool) { return "actor-1", true })
			router := gin.New()
			RegisterRoutes(router.Group("/lingdoc"), handler)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/lingdoc/projects/p1/candidates/c1", nil))
			if response.Code != test.status || service.actor != "actor-1" || service.project != "p1" || service.candidate != "c1" {
				t.Fatalf("status=%d body=%s input=%+v", response.Code, response.Body.String(), service)
			}
		})
	}
}
