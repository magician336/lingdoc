package workspace

import (
	"encoding/json"
	"errors"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

var ErrImpactHumanDecisionRequired = errors.New("mainline_human_decision_required")

type impactRoutesHandler struct{ ports ImpactRoutePorts }

func (h impactRoutesHandler) actor(c *gin.Context, human bool) (core.Actor, bool) {
	actor, ok := identity(c)
	if !ok {
		return core.Actor{}, false
	}
	if !h.ports.wired() {
		sendError(c, core.ErrInvalidState)
		return core.Actor{}, false
	}
	if human {
		if _, machine := types.TenantAPIKeyScopeFromContext(c.Request.Context()); machine {
			sendErrorDetails(c, 403, "human_decision_required", ErrImpactHumanDecisionRequired.Error(), nil)
			return core.Actor{}, false
		}
		if principal, exists := types.PrincipalFromContext(c.Request.Context()); exists {
			switch principal.Type {
			case types.PrincipalAPITenant, types.PrincipalAPIPlatform, types.PrincipalAPIExternalUser, types.PrincipalEmbedChannel, types.PrincipalEmbedSession, types.PrincipalEmbedVisitor:
				sendErrorDetails(c, 403, "human_decision_required", ErrImpactHumanDecisionRequired.Error(), nil)
				return core.Actor{}, false
			}
		}
	}
	return actor, true
}
func impactRouteRead[T any](h impactRoutesHandler, read func(*gin.Context, core.Actor) (T, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, ok := h.actor(c, false)
		if !ok {
			return
		}
		out, err := read(c, actor)
		if err != nil {
			sendError(c, err)
			return
		}
		sendOK(c, 200, out, false)
	}
}
func impactRouteWrite[T any](h impactRoutesHandler, human bool, write func(*gin.Context, core.Actor, string, T) (json.RawMessage, int, bool, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, ok := h.actor(c, human)
		if !ok {
			return
		}
		key, ok := idempotencyKey(c)
		if !ok {
			return
		}
		var input T
		if !decodeBody(c, &input) {
			return
		}
		body, status, replay, err := write(c, actor, key, input)
		if err != nil {
			sendError(c, err)
			return
		}
		sendOK(c, status, body, replay)
	}
}

// Only the new routes are registered here. The existing ChangeSet read,
// apply/reject/reassess paths remain registered by the workspace handler.
func RegisterImpactRoutes(group *gin.RouterGroup, ports ImpactRoutePorts) {
	if group == nil {
		return
	}
	h := impactRoutesHandler{ports: ports}
	r := group.Group("/projects/:projectId")
	// The AI-proposed dependency candidates are registered here so that they
	// live in the same project-scoped group as the dependency edges they
	// become.
	RegisterDependencyCandidateRoutes(group, ports)
	r.POST("/change-sets/drafts", impactRouteWrite(h, false, func(c *gin.Context, a core.Actor, k string, i core.ExtendedChangeSetInput) (json.RawMessage, int, bool, error) {
		return ports.ChangeSets.CreateChangeDraft(c.Request.Context(), a, c.Param("projectId"), k, i)
	}))
	r.PUT("/change-sets/:changeSetId/draft", impactRouteWrite(h, false, func(c *gin.Context, a core.Actor, k string, i core.ExtendedChangeSetInput) (json.RawMessage, int, bool, error) {
		return ports.ChangeSets.EditChangeDraft(c.Request.Context(), a, c.Param("projectId"), c.Param("changeSetId"), k, i)
	}))
	r.POST("/change-sets/:changeSetId/assess", impactRouteWrite(h, false, func(c *gin.Context, a core.Actor, k string, i struct{}) (json.RawMessage, int, bool, error) {
		return ports.ChangeSets.AssessChangeSet(c.Request.Context(), a, c.Param("projectId"), c.Param("changeSetId"), k)
	}))
	r.GET("/change-sets/:changeSetId/impact-assessment", impactRouteRead(h, func(c *gin.Context, a core.Actor) (core.ImpactAssessment, error) {
		return ports.Impact.GetImpactAssessment(c.Request.Context(), a, c.Param("projectId"), c.Param("changeSetId"))
	}))
	r.POST("/change-sets/:changeSetId/stabilize", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, i struct{}) (json.RawMessage, int, bool, error) {
		return ports.ChangeSets.StabilizeRevision(c.Request.Context(), a, c.Param("projectId"), c.Param("changeSetId"), k)
	}))
	r.POST("/change-sets/:changeSetId/reverse", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, i struct{}) (json.RawMessage, int, bool, error) {
		return ports.ChangeSets.ReverseChangeSet(c.Request.Context(), a, c.Param("projectId"), c.Param("changeSetId"), k)
	}))
	r.POST("/change-sets/:changeSetId/preflight", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, i struct {
		ProofID string `json:"proof_id"`
	}) (json.RawMessage, int, bool, error) {
		return ports.Impact.AcknowledgeImpactPreflight(c.Request.Context(), a, c.Param("projectId"), c.Param("changeSetId"), i.ProofID, k)
	}))
	r.GET("/check-receipts/:receiptId", impactRouteRead(h, func(c *gin.Context, a core.Actor) (core.ImpactComputationReceipt, error) {
		return ports.Impact.ReadImpactComputationReceipt(c.Request.Context(), a, c.Param("projectId"), c.Param("receiptId"))
	}))
	r.GET("/context-revisions", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.ContextRevisionRecord, error) {
		return ports.Impact.ListContextRevisions(c.Request.Context(), a, c.Param("projectId"))
	}))
	r.GET("/route-nodes", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.RouteNode, error) {
		return ports.Impact.ListRouteNodes(c.Request.Context(), a, c.Param("projectId"))
	}))
	r.GET("/dependencies", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.DependencyEdge, error) {
		return ports.Dependency.ListDependencyEdges(c.Request.Context(), a, c.Param("projectId"))
	}))
	r.POST("/dependencies", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, i core.DependencyEdgeInput) (json.RawMessage, int, bool, error) {
		return ports.Dependency.CreateDependencyEdge(c.Request.Context(), a, c.Param("projectId"), k, i)
	}))
	r.POST("/dependencies/:edgeId/decision", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, i core.DependencyDecisionInput) (json.RawMessage, int, bool, error) {
		return ports.Dependency.DecideDependencyEdge(c.Request.Context(), a, c.Param("projectId"), c.Param("edgeId"), k, i)
	}))
	r.GET("/impact-tasks", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.ImpactTask, error) {
		return ports.Impact.ListImpactTasks(c.Request.Context(), a, c.Param("projectId"))
	}))
	r.POST("/impact-tasks/:taskId/actions", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, i core.ImpactTaskActionInput) (json.RawMessage, int, bool, error) {
		return ports.Impact.ActOnImpactTask(c.Request.Context(), a, c.Param("projectId"), c.Param("taskId"), k, i)
	}))
	r.GET("/runtime/controls", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.RuntimeControl, error) {
		return ports.Runtime.ListRuntimeControls(c.Request.Context(), a, c.Param("projectId"))
	}))
	r.POST("/runtime/controls", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, i core.RuntimeControlInput) (json.RawMessage, int, bool, error) {
		return ports.Runtime.ControlRuntime(c.Request.Context(), a, c.Param("projectId"), k, i)
	}))
	r.GET("/runtime/events", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.RuntimeEvent, error) {
		return ports.Runtime.ListRuntimeEvents(c.Request.Context(), a, c.Param("projectId"), c.Query("correlation_id"))
	}))
	r.GET("/runtime/metrics", impactRouteRead(h, func(c *gin.Context, a core.Actor) (core.RuntimeMetrics, error) {
		return ports.Runtime.GetRuntimeMetrics(c.Request.Context(), a, c.Param("projectId"))
	}))
	r.GET("/runtime/alerts", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.RuntimeAlert, error) {
		return ports.Runtime.ListRuntimeAlerts(c.Request.Context(), a, c.Param("projectId"))
	}))
	r.POST("/runtime/alert-rules", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, i core.RuntimeAlertRule) (json.RawMessage, int, bool, error) {
		return ports.Runtime.SaveRuntimeAlertRule(c.Request.Context(), a, c.Param("projectId"), k, i)
	}))
	r.GET("/trial-windows", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.TrialWindow, error) {
		return ports.Trials.ListTrialWindows(c.Request.Context(), a, c.Param("projectId"))
	}))
	r.POST("/trial-windows", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, i core.TrialWindow) (json.RawMessage, int, bool, error) {
		return ports.Trials.CreateTrialWindow(c.Request.Context(), a, c.Param("projectId"), k, i)
	}))
	r.POST("/trial-windows/:windowId/actions", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, i core.TrialWindowActionInput) (json.RawMessage, int, bool, error) {
		return ports.Trials.ActOnTrialWindow(c.Request.Context(), a, c.Param("projectId"), c.Param("windowId"), k, i)
	}))
	r.GET("/trial-windows/:windowId/samples", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.TrialSample, error) {
		return ports.Trials.ListTrialSamples(c.Request.Context(), a, c.Param("projectId"), c.Param("windowId"))
	}))
	r.POST("/trial-windows/:windowId/samples", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, i core.TrialSample) (json.RawMessage, int, bool, error) {
		return ports.Trials.RecordTrialSample(c.Request.Context(), a, c.Param("projectId"), c.Param("windowId"), k, i)
	}))
	r.POST("/trial-windows/:windowId/samples/:sampleId/review", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, i core.TrialReviewInput) (json.RawMessage, int, bool, error) {
		return ports.Trials.ReviewTrialSample(c.Request.Context(), a, c.Param("projectId"), c.Param("windowId"), c.Param("sampleId"), k, i)
	}))
	r.GET("/trial-windows/:windowId/report", impactRouteRead(h, func(c *gin.Context, a core.Actor) (core.TrialReport, error) {
		return ports.Trials.GetTrialReport(c.Request.Context(), a, c.Param("projectId"), c.Param("windowId"))
	}))
	r.GET("/trial-windows/:windowId/decisions", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.TrialDecisionRecord, error) {
		return ports.Trials.ListTrialDecisions(c.Request.Context(), a, c.Param("projectId"), c.Param("windowId"))
	}))
	r.POST("/trial-windows/:windowId/decisions", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, i core.TrialDecisionInput) (json.RawMessage, int, bool, error) {
		return ports.Trials.CreateTrialDecision(c.Request.Context(), a, c.Param("projectId"), c.Param("windowId"), k, i)
	}))
	r.POST("/trial-decisions/:decisionId/sign", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, i core.TrialDecisionSignInput) (json.RawMessage, int, bool, error) {
		return ports.Trials.SignTrialDecision(c.Request.Context(), a, c.Param("projectId"), c.Param("decisionId"), k, i)
	}))
}
