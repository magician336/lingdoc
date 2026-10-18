package workspace

import (
	"encoding/json"
	"errors"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

var ErrG3G6HumanDecisionRequired = errors.New("mainline_human_decision_required")

type g3g6FullHTTP struct{ service *core.Service }

func (h g3g6FullHTTP) actor(c *gin.Context, human bool) (core.Actor, bool) {
	actor, ok := identity(c)
	if !ok {
		return core.Actor{}, false
	}
	if h.service == nil {
		sendError(c, core.ErrInvalidState)
		return core.Actor{}, false
	}
	if human {
		if _, machine := types.TenantAPIKeyScopeFromContext(c.Request.Context()); machine {
			sendErrorDetails(c, 403, "human_decision_required", ErrG3G6HumanDecisionRequired.Error(), nil)
			return core.Actor{}, false
		}
		if principal, exists := types.PrincipalFromContext(c.Request.Context()); exists {
			switch principal.Type {
			case types.PrincipalAPITenant, types.PrincipalAPIPlatform, types.PrincipalAPIExternalUser, types.PrincipalEmbedChannel, types.PrincipalEmbedSession, types.PrincipalEmbedVisitor:
				sendErrorDetails(c, 403, "human_decision_required", ErrG3G6HumanDecisionRequired.Error(), nil)
				return core.Actor{}, false
			}
		}
	}
	return actor, true
}
func g3g6Read[T any](h g3g6FullHTTP, read func(*gin.Context, core.Actor) (T, error)) gin.HandlerFunc {
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
func g3g6Write[T any](h g3g6FullHTTP, human bool, write func(*gin.Context, core.Actor, string, T) (json.RawMessage, int, bool, error)) gin.HandlerFunc {
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
func RegisterG3G6FullRoutes(group *gin.RouterGroup, service *core.Service) {
	if group == nil {
		return
	}
	h := g3g6FullHTTP{service}
	r := group.Group("/projects/:projectId")
	r.POST("/change-sets/drafts", g3g6Write(h, false, func(c *gin.Context, a core.Actor, k string, i core.ExtendedChangeSetInput) (json.RawMessage, int, bool, error) {
		return service.CreateChangeDraft(c.Request.Context(), a, c.Param("projectId"), k, i)
	}))
	r.PUT("/change-sets/:changeSetId/draft", g3g6Write(h, false, func(c *gin.Context, a core.Actor, k string, i core.ExtendedChangeSetInput) (json.RawMessage, int, bool, error) {
		return service.EditChangeDraft(c.Request.Context(), a, c.Param("projectId"), c.Param("changeSetId"), k, i)
	}))
	r.POST("/change-sets/:changeSetId/assess", g3g6Write(h, false, func(c *gin.Context, a core.Actor, k string, i struct{}) (json.RawMessage, int, bool, error) {
		return service.AssessChangeSet(c.Request.Context(), a, c.Param("projectId"), c.Param("changeSetId"), k)
	}))
	r.GET("/change-sets/:changeSetId/impact-assessment", g3g6Read(h, func(c *gin.Context, a core.Actor) (core.ImpactAssessment, error) {
		return service.GetImpactAssessment(c.Request.Context(), a, c.Param("projectId"), c.Param("changeSetId"))
	}))
	r.POST("/change-sets/:changeSetId/stabilize", g3g6Write(h, true, func(c *gin.Context, a core.Actor, k string, i struct{}) (json.RawMessage, int, bool, error) {
		return service.StabilizeRevision(c.Request.Context(), a, c.Param("projectId"), c.Param("changeSetId"), k)
	}))
	r.POST("/change-sets/:changeSetId/reverse", g3g6Write(h, true, func(c *gin.Context, a core.Actor, k string, i struct{}) (json.RawMessage, int, bool, error) {
		return service.ReverseChangeSet(c.Request.Context(), a, c.Param("projectId"), c.Param("changeSetId"), k)
	}))
	r.POST("/change-sets/:changeSetId/preflight", g3g6Write(h, true, func(c *gin.Context, a core.Actor, k string, i struct {
		ProofID string `json:"proof_id"`
	}) (json.RawMessage, int, bool, error) {
		return service.AcknowledgeImpactPreflight(c.Request.Context(), a, c.Param("projectId"), c.Param("changeSetId"), i.ProofID, k)
	}))
	r.GET("/check-receipts/:receiptId", g3g6Read(h, func(c *gin.Context, a core.Actor) (core.ImpactComputationReceipt, error) {
		return service.ReadImpactComputationReceipt(c.Request.Context(), a, c.Param("projectId"), c.Param("receiptId"))
	}))
	r.GET("/context-revisions", g3g6Read(h, func(c *gin.Context, a core.Actor) ([]core.ContextRevisionRecord, error) {
		return service.ListContextRevisions(c.Request.Context(), a, c.Param("projectId"))
	}))
	r.GET("/route-nodes", g3g6Read(h, func(c *gin.Context, a core.Actor) ([]core.RouteNode, error) {
		return service.ListRouteNodes(c.Request.Context(), a, c.Param("projectId"))
	}))
	r.GET("/dependencies", g3g6Read(h, func(c *gin.Context, a core.Actor) ([]core.DependencyEdge, error) {
		return service.ListDependencyEdges(c.Request.Context(), a, c.Param("projectId"))
	}))
	r.POST("/dependencies", g3g6Write(h, true, func(c *gin.Context, a core.Actor, k string, i core.DependencyEdgeInput) (json.RawMessage, int, bool, error) {
		return service.CreateDependencyEdge(c.Request.Context(), a, c.Param("projectId"), k, i)
	}))
	r.POST("/dependencies/:edgeId/decision", g3g6Write(h, true, func(c *gin.Context, a core.Actor, k string, i core.DependencyDecisionInput) (json.RawMessage, int, bool, error) {
		return service.DecideDependencyEdge(c.Request.Context(), a, c.Param("projectId"), c.Param("edgeId"), k, i)
	}))
	r.GET("/impact-tasks", g3g6Read(h, func(c *gin.Context, a core.Actor) ([]core.ImpactTask, error) {
		return service.ListImpactTasks(c.Request.Context(), a, c.Param("projectId"))
	}))
	r.POST("/impact-tasks/:taskId/actions", g3g6Write(h, true, func(c *gin.Context, a core.Actor, k string, i core.ImpactTaskActionInput) (json.RawMessage, int, bool, error) {
		return service.ActOnImpactTask(c.Request.Context(), a, c.Param("projectId"), c.Param("taskId"), k, i)
	}))
	r.GET("/runtime/controls", g3g6Read(h, func(c *gin.Context, a core.Actor) ([]core.RuntimeControl, error) {
		return service.ListRuntimeControls(c.Request.Context(), a, c.Param("projectId"))
	}))
	r.POST("/runtime/controls", g3g6Write(h, true, func(c *gin.Context, a core.Actor, k string, i core.RuntimeControlInput) (json.RawMessage, int, bool, error) {
		return service.ControlRuntime(c.Request.Context(), a, c.Param("projectId"), k, i)
	}))
	r.GET("/runtime/events", g3g6Read(h, func(c *gin.Context, a core.Actor) ([]core.RuntimeEvent, error) {
		return service.ListRuntimeEvents(c.Request.Context(), a, c.Param("projectId"), c.Query("correlation_id"))
	}))
	r.GET("/runtime/metrics", g3g6Read(h, func(c *gin.Context, a core.Actor) (core.RuntimeMetrics, error) {
		return service.GetRuntimeMetrics(c.Request.Context(), a, c.Param("projectId"))
	}))
	r.GET("/runtime/alerts", g3g6Read(h, func(c *gin.Context, a core.Actor) ([]core.RuntimeAlert, error) {
		return service.ListRuntimeAlerts(c.Request.Context(), a, c.Param("projectId"))
	}))
	r.POST("/runtime/alert-rules", g3g6Write(h, true, func(c *gin.Context, a core.Actor, k string, i core.RuntimeAlertRule) (json.RawMessage, int, bool, error) {
		return service.SaveRuntimeAlertRule(c.Request.Context(), a, c.Param("projectId"), k, i)
	}))
	r.GET("/trial-windows", g3g6Read(h, func(c *gin.Context, a core.Actor) ([]core.TrialWindow, error) {
		return service.ListTrialWindows(c.Request.Context(), a, c.Param("projectId"))
	}))
	r.POST("/trial-windows", g3g6Write(h, true, func(c *gin.Context, a core.Actor, k string, i core.TrialWindow) (json.RawMessage, int, bool, error) {
		return service.CreateTrialWindow(c.Request.Context(), a, c.Param("projectId"), k, i)
	}))
	r.POST("/trial-windows/:windowId/actions", g3g6Write(h, true, func(c *gin.Context, a core.Actor, k string, i core.TrialWindowActionInput) (json.RawMessage, int, bool, error) {
		return service.ActOnTrialWindow(c.Request.Context(), a, c.Param("projectId"), c.Param("windowId"), k, i)
	}))
	r.GET("/trial-windows/:windowId/samples", g3g6Read(h, func(c *gin.Context, a core.Actor) ([]core.TrialSample, error) {
		return service.ListTrialSamples(c.Request.Context(), a, c.Param("projectId"), c.Param("windowId"))
	}))
	r.POST("/trial-windows/:windowId/samples", g3g6Write(h, true, func(c *gin.Context, a core.Actor, k string, i core.TrialSample) (json.RawMessage, int, bool, error) {
		return service.RecordTrialSample(c.Request.Context(), a, c.Param("projectId"), c.Param("windowId"), k, i)
	}))
	r.POST("/trial-windows/:windowId/samples/:sampleId/review", g3g6Write(h, true, func(c *gin.Context, a core.Actor, k string, i core.TrialReviewInput) (json.RawMessage, int, bool, error) {
		return service.ReviewTrialSample(c.Request.Context(), a, c.Param("projectId"), c.Param("windowId"), c.Param("sampleId"), k, i)
	}))
	r.GET("/trial-windows/:windowId/report", g3g6Read(h, func(c *gin.Context, a core.Actor) (core.TrialReport, error) {
		return service.GetTrialReport(c.Request.Context(), a, c.Param("projectId"), c.Param("windowId"))
	}))
	r.GET("/trial-windows/:windowId/decisions", g3g6Read(h, func(c *gin.Context, a core.Actor) ([]core.TrialDecisionRecord, error) {
		return service.ListTrialDecisions(c.Request.Context(), a, c.Param("projectId"), c.Param("windowId"))
	}))
	r.POST("/trial-windows/:windowId/decisions", g3g6Write(h, true, func(c *gin.Context, a core.Actor, k string, i core.TrialDecisionInput) (json.RawMessage, int, bool, error) {
		return service.CreateTrialDecision(c.Request.Context(), a, c.Param("projectId"), c.Param("windowId"), k, i)
	}))
	r.POST("/trial-decisions/:decisionId/sign", g3g6Write(h, true, func(c *gin.Context, a core.Actor, k string, i core.TrialDecisionSignInput) (json.RawMessage, int, bool, error) {
		return service.SignTrialDecision(c.Request.Context(), a, c.Param("projectId"), c.Param("decisionId"), k, i)
	}))
}
