package router

import (
	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	"github.com/gin-gonic/gin"
	"strings"
)

// Every LingDoc route receives an explicit API-key policy and tenant role.
// Read-only POST operations must not accidentally acquire write semantics.
type lingDocRegistrar struct{ read, write *apiKeyRouteGroup }

func newLingDocRegistrar(v1 *gin.RouterGroup, guards *rbacGuards) *lingDocRegistrar {
	return &lingDocRegistrar{
		read:  guards.apiKeyGroup(v1.Group("/lingdoc", guards.Viewer()), apiKeyFullAccess()),
		write: guards.apiKeyGroup(v1.Group("/lingdoc", guards.Contributor()), apiKeyFullAccess()),
	}
}
func (r *lingDocRegistrar) GET(path string, h ...gin.HandlerFunc) gin.IRoutes {
	return r.read.GET(path, h...)
}
func (r *lingDocRegistrar) PUT(path string, h ...gin.HandlerFunc) gin.IRoutes {
	return r.write.PUT(path, h...)
}
func (r *lingDocRegistrar) POST(path string, h ...gin.HandlerFunc) gin.IRoutes {
	if strings.HasSuffix(path, "/retrieval") || strings.HasSuffix(path, "/checks") {
		return r.read.POST(path, h...)
	}
	return r.write.POST(path, h...)
}
func registerLingDocWorkspaceRoutes(v1 *gin.RouterGroup, guards *rbacGuards, h *workspace.Handler) {
	if h == nil {
		return
	}
	routes := newLingDocRegistrar(v1, guards)
	h.Register(workspace.RouteGroups{Read: routes, Write: routes})
}
