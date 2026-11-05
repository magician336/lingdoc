package workspace

import (
	"context"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type releaseApplicationStub struct {
	ReleaseApplication
	actor, project string
	version        int64
	err            error
}

func (s *releaseApplicationStub) Check(_ context.Context, actor, project string, version int64) (delivery.CheckResult, error) {
	s.actor, s.project, s.version = actor, project, version
	return delivery.CheckResult{}, s.err
}
func TestDeliveryHandlerUsesApplicationPort(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		err    error
		status int
	}{{nil, http.StatusOK}, {ErrNotFound, http.StatusNotFound}} {
		service := &releaseApplicationStub{err: test.err}
		handler := NewDeliveryHandler(service)
		router := gin.New()
		RegisterDeliveryRoutes(router.Group("/lingdoc"), handler)
		ctx := context.WithValue(context.Background(), types.UserIDContextKey, "actor-1")
		ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(1))
		request := httptest.NewRequest(http.MethodPost, "/lingdoc/projects/p1/checks", strings.NewReader(`{"expected_project_version":7}`)).WithContext(ctx)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != test.status || service.actor != "actor-1" || service.project != "p1" || service.version != 7 {
			t.Fatalf("status=%d body=%s input=%+v", response.Code, response.Body.String(), service)
		}
	}
}
