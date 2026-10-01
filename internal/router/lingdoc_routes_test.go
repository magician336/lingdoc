package router

import (
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	"github.com/gin-gonic/gin"
)

func TestLingDocRoutesDeclareRoleAndAPIKeyPolicies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v1 := engine.Group("/api/v1")
	guards := &rbacGuards{}
	root := v1.Group("/lingdoc")
	read := guards.apiKeyGroup(root.Group("", guards.Viewer()), apiKeyFullAccess())
	write := guards.apiKeyGroup(root.Group("", guards.Contributor()), apiKeyFullAccess())

	workspace.NewHandler(workspace.HandlerDependencies{}).Register(workspace.RouteGroups{Read: read, Write: write})
	guards.assertAPIKeyPoliciesMatchRoutes(engine)

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/lingdoc/projects"},
		{http.MethodPost, "/api/v1/lingdoc/projects"},
		{http.MethodPost, "/api/v1/lingdoc/projects/:projectId/retrieval"},
	} {
		policy, ok := guards.apiKeyAuthorizer.Lookup(route.method, route.path)
		if !ok || !policy.RequireFullAccess {
			t.Errorf("API-key policy for %s %s = %#v, registered=%v; want full access", route.method, route.path, policy, ok)
		}
	}
}

