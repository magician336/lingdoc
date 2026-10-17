package router

import (
	"github.com/Tencent/WeKnora/internal/lingdoc/workspace"
	"github.com/gin-gonic/gin"
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
	// The parent registrar is the write policy. Read-only POST operations are
	// registered through groups().Read; route names never select policy.
	return r.write.POST(path, h...)
}

func (r *lingDocRegistrar) groups() workspace.RouteGroups {
	return workspace.RouteGroups{Read: r.read, Write: r.write}
}
func registerLingDocWorkspaceRoutes(v1 *gin.RouterGroup, guards *rbacGuards, h *workspace.Handler) {
	if h == nil {
		return
	}
	routes := newLingDocRegistrar(v1, guards)
	h.Register(routes.groups())
	// Exact machine-read routes retain current grant and source rechecks.
	h.RegisterMachineReadRoutes(routes.read.With(apiKeyRetrieve(apiKeyFullAccess())))
	// Human workflow routes retain API-key default denial.
	h.RegisterFullRoutes(v1.Group("/lingdoc", guards.Viewer()))
}
