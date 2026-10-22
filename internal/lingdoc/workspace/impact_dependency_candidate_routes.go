package workspace

import (
	"encoding/json"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/gin-gonic/gin"
)

// The AI-proposed dependency candidates (G3 gap closure #252, AC-G3-15).
//
// Three routes:
//
//   - POST /projects/{projectId}/dependency-candidates submits a batch of
//     proposals. It is the AI side of the seam, so a machine principal may
//     call it: proposing is not deciding.
//   - GET /projects/{projectId}/dependency-candidates reads the proposals of
//     the project (optionally one batch), with the human conclusions.
//   - POST /projects/{projectId}/dependency-candidates/{candidateId}/decision
//     applies the human decision. It reuses the three states of the
//     dependency edge decision: confirm, modify, reject.
func RegisterDependencyCandidateRoutes(group *gin.RouterGroup, ports ImpactRoutePorts) {
	if group == nil {
		return
	}
	h := impactRoutesHandler{ports: ports}
	r := group.Group("/projects/:projectId")
	r.POST("/dependency-candidates", impactRouteWrite(h, false, func(c *gin.Context, a core.Actor, k string, i core.DependencyCandidateBatchInput) (json.RawMessage, int, bool, error) {
		return ports.Dependency.SubmitDependencyCandidateBatch(c.Request.Context(), a, c.Param("projectId"), k, i)
	}))
	r.GET("/dependency-candidates", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.DependencyCandidateBatch, error) {
		return ports.Dependency.ListDependencyCandidates(c.Request.Context(), a, c.Param("projectId"), c.Query("batch_id"))
	}))
	r.POST("/dependency-candidates/:candidateId/decision", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, i core.DependencyDecisionInput) (json.RawMessage, int, bool, error) {
		return ports.Dependency.DecideDependencyCandidate(c.Request.Context(), a, c.Param("projectId"), c.Param("candidateId"), k, i)
	}))
}
