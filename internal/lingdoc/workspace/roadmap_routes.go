package workspace

import (
	"encoding/json"
	"strconv"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/gin-gonic/gin"
)

// The route-roadmap editing session surface (#250).
//
// The routes are registered next to the impact routes because they belong to
// the same project-scoped family, but they are a separate registration
// function: the roadmap module owns its own vocabulary, and a future change
// to the impact surface must not drag the roadmap surface along.
//
// Every write goes through the idempotency key and the human-decision check
// the shared impact write helper already applies, so the roadmap cannot be
// edited by a machine principal.
func RegisterRoadmapRoutes(group *gin.RouterGroup, ports ImpactRoutePorts) {
	if group == nil || nilPort(ports.Roadmap) {
		return
	}
	h := impactRoutesHandler{ports: ports}
	r := group.Group("/projects/:projectId")

	// The frozen roadmap: only committed versions are readable here.
	r.GET("/roadmap", impactRouteRead(h, func(c *gin.Context, a core.Actor) (core.RoadmapBaseline, error) {
		return ports.Roadmap.ReadRoadmap(c.Request.Context(), a, c.Param("projectId"))
	}))
	r.GET("/roadmap/edges", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.RouteEdge, error) {
		return ports.Roadmap.ReadRouteEdges(c.Request.Context(), a, c.Param("projectId"))
	}))
	r.GET("/roadmap/edges/:edgeId/versions", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.RouteEdge, error) {
		return ports.Roadmap.ReadRouteEdgeVersions(c.Request.Context(), a, c.Param("projectId"), c.Param("edgeId"))
	}))
	r.GET("/roadmap/nodes/:nodeId/versions", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.RoadmapVersionEntry, error) {
		return ports.Roadmap.ReadRoadmapNodeVersions(c.Request.Context(), a, c.Param("projectId"), c.Param("nodeId"))
	}))
	r.GET("/roadmap/nodes/:nodeId/compare", impactRouteRead(h, func(c *gin.Context, a core.Actor) (core.RoadmapComparison, error) {
		from, err := strconv.ParseInt(c.Query("from"), 10, 64)
		if err != nil {
			return core.RoadmapComparison{}, core.ErrInvalidRequest
		}
		to, err := strconv.ParseInt(c.Query("to"), 10, 64)
		if err != nil {
			return core.RoadmapComparison{}, core.ErrInvalidRequest
		}
		return ports.Roadmap.CompareRoadmapNodeVersions(c.Request.Context(), a, c.Param("projectId"), c.Param("nodeId"), from, to)
	}))

	// The unfrozen editing session.
	r.GET("/roadmap/edits", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.RoadmapEditSession, error) {
		return ports.Roadmap.ListRoadmapSessions(c.Request.Context(), a, c.Param("projectId"))
	}))
	r.GET("/roadmap/edits/:sessionId", impactRouteRead(h, func(c *gin.Context, a core.Actor) (core.RoadmapEditSession, error) {
		return ports.Roadmap.GetRoadmapSession(c.Request.Context(), a, c.Param("projectId"), c.Param("sessionId"))
	}))
	r.POST("/roadmap/edits", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, i core.RoadmapEditBeginInput) (json.RawMessage, int, bool, error) {
		return ports.Roadmap.BeginRoadmapEdit(c.Request.Context(), a, c.Param("projectId"), k, i)
	}))
	r.POST("/roadmap/edits/:sessionId/nodes", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, i core.RoadmapNodeProposalInput) (json.RawMessage, int, bool, error) {
		return ports.Roadmap.ProposeRoadmapNode(c.Request.Context(), a, c.Param("projectId"), c.Param("sessionId"), k, i)
	}))
	r.POST("/roadmap/edits/:sessionId/edges", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, i core.RoadmapEdgeProposalInput) (json.RawMessage, int, bool, error) {
		return ports.Roadmap.ProposeRoadmapEdge(c.Request.Context(), a, c.Param("projectId"), c.Param("sessionId"), k, i)
	}))
	r.POST("/roadmap/edits/:sessionId/commit", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, i core.RoadmapCommitInput) (json.RawMessage, int, bool, error) {
		return ports.Roadmap.CommitRoadmapEdit(c.Request.Context(), a, c.Param("projectId"), c.Param("sessionId"), k, i)
	}))
	r.POST("/roadmap/edits/:sessionId/cancel", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, i core.RoadmapCancelInput) (json.RawMessage, int, bool, error) {
		return ports.Roadmap.CancelRoadmapEdit(c.Request.Context(), a, c.Param("projectId"), c.Param("sessionId"), k, i)
	}))
}
