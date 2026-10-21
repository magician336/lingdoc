package workspace

import (
	"encoding/json"

	"github.com/Tencent/WeKnora/internal/clienterror"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/gin-gonic/gin"
)

// g2FullError renders a business failure of the G2 surface.
//
// The outward shape of a failure is declared by the error itself, at the place
// where that fact is known. This mapper therefore holds no table of business
// errors: it renders what the error declares, and leaves the failures that
// declare nothing to the shared workspace mapper.
func g2FullError(c *gin.Context, err error) {
	if form, ok := clienterror.FormOf(err); ok {
		c.JSON(form.Status, gin.H{"error": gin.H{"code": form.Code, "message": form.Message,
			"retryable": form.Retryable}, "request_id": requestID(c)})
		return
	}
	sendError(c, err)
}
func g2FullRead(call func(*gin.Context, Actor) (any, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, ok := g2FullIdentity(c)
		if !ok {
			return
		}
		value, err := call(c, actor)
		if err != nil {
			g2FullError(c, g2SurfaceError(err))
			return
		}
		sendOK(c, 200, value, false)
	}
}
func g2FullWrite[I any](call func(*gin.Context, Actor, string, I) (json.RawMessage, int, bool, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, ok := g2FullIdentity(c)
		if !ok {
			return
		}
		key, ok := idempotencyKey(c)
		if !ok {
			return
		}
		var input I
		if !decodeBody(c, &input) {
			return
		}
		value, status, replayed, err := call(c, actor, key, input)
		if err != nil {
			g2FullError(c, g2SurfaceError(err))
			return
		}
		sendOK(c, status, value, replayed)
	}
}
func g2FullPostRead[I any](call func(*gin.Context, Actor, I) (any, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, ok := g2FullIdentity(c)
		if !ok {
			return
		}
		var input I
		if !decodeBody(c, &input) {
			return
		}
		value, err := call(c, actor, input)
		if err != nil {
			g2FullError(c, g2SurfaceError(err))
			return
		}
		sendOK(c, 200, value, false)
	}
}

// RegisterG2FullRoutes adds only the G2 domain routes to the authenticated
// /api/v1/lingdoc group. Shared workspace handlers remain unchanged.
func RegisterG2FullRoutes(group *gin.RouterGroup, service *core.Service) {
	if service == nil {
		panic("lingdoc G2 routes require core service")
	}
	group.GET("/projects/:projectId/research-tasks", g2FullRead(func(c *gin.Context, actor Actor) (any, error) {
		return service.ListResearchTasks(c.Request.Context(), actor, c.Param("projectId"))
	}))
	group.GET("/projects/:projectId/research-runs", g2FullRead(func(c *gin.Context, actor Actor) (any, error) {
		return service.ListResearchRuns(c.Request.Context(), actor, c.Param("projectId"))
	}))
	group.GET("/projects/:projectId/research-runs/:runID", g2FullRead(func(c *gin.Context, actor Actor) (any, error) {
		return service.GetResearchRun(c.Request.Context(), actor, c.Param("projectId"), c.Param("runID"))
	}))
	group.GET("/projects/:projectId/import-candidates", g2FullRead(func(c *gin.Context, actor Actor) (any, error) {
		return service.ListImportCandidates(c.Request.Context(), actor, c.Param("projectId"))
	}))
	group.GET("/projects/:projectId/source-anchors", g2FullRead(func(c *gin.Context, actor Actor) (any, error) {
		return service.ListSourceAnchors(c.Request.Context(), actor, c.Param("projectId"))
	}))
	group.GET("/projects/:projectId/research-claims", g2FullRead(func(c *gin.Context, actor Actor) (any, error) {
		return service.ListResearchClaims(c.Request.Context(), actor, c.Param("projectId"))
	}))
	group.GET("/projects/:projectId/evidence-cards", g2FullRead(func(c *gin.Context, actor Actor) (any, error) {
		return service.ListEvidenceCards(c.Request.Context(), actor, c.Param("projectId"))
	}))
	group.GET("/projects/:projectId/agents/:agent/sessions", g2FullRead(func(c *gin.Context, actor Actor) (any, error) {
		return service.ListAgentSessions(c.Request.Context(), actor, c.Param("projectId"), c.Param("agent"))
	}))
	group.GET("/projects/:projectId/agent-sessions/:sessionID/turns", g2FullRead(func(c *gin.Context, actor Actor) (any, error) {
		return service.GetAgentTurns(c.Request.Context(), actor, c.Param("projectId"), c.Param("sessionID"))
	}))
	group.GET("/projects/:projectId/action-proposals", g2FullRead(func(c *gin.Context, actor Actor) (any, error) {
		return service.ListActionProposals(c.Request.Context(), actor, c.Param("projectId"))
	}))
	group.GET("/projects/:projectId/assets/:assetID/policy", g2FullRead(func(c *gin.Context, actor Actor) (any, error) {
		return service.GetProjectAssetPolicy(c.Request.Context(), actor, c.Param("projectId"), c.Param("assetID"))
	}))
	group.POST("/projects/:projectId/research-tasks", g2FullWrite(func(c *gin.Context, actor Actor, key string, input core.SaveResearchTaskInput) (json.RawMessage, int, bool, error) {
		return service.SaveResearchTask(c.Request.Context(), actor, c.Param("projectId"), key, input)
	}))
	group.PUT("/projects/:projectId/research-tasks/:taskID", g2FullWrite(func(c *gin.Context, actor Actor, key string, input core.SaveResearchTaskInput) (json.RawMessage, int, bool, error) {
		if input.TaskID != "" && input.TaskID != c.Param("taskID") {
			return nil, 0, false, core.ErrInvalidRequest
		}
		input.TaskID = c.Param("taskID")
		return service.SaveResearchTask(c.Request.Context(), actor, c.Param("projectId"), key, input)
	}))
	group.POST("/projects/:projectId/research-tasks/:taskID/approve", g2FullWrite(func(c *gin.Context, actor Actor, key string, input core.G2ConfirmedInput) (json.RawMessage, int, bool, error) {
		return service.ApproveResearchTask(c.Request.Context(), actor, c.Param("projectId"), c.Param("taskID"), key, input)
	}))
	group.POST("/projects/:projectId/research-tasks/:taskID/run", g2FullWrite(func(c *gin.Context, actor Actor, key string, input core.RunResearchTaskInput) (json.RawMessage, int, bool, error) {
		return service.RunResearchTask(c.Request.Context(), actor, c.Param("projectId"), c.Param("taskID"), key, input)
	}))
	group.POST("/projects/:projectId/research-tasks/:taskID/control", g2FullWrite(func(c *gin.Context, actor Actor, key string, input core.ControlResearchTaskInput) (json.RawMessage, int, bool, error) {
		return service.ControlResearchTask(c.Request.Context(), actor, c.Param("projectId"), c.Param("taskID"), key, input)
	}))
	group.POST("/projects/:projectId/import-candidates", g2FullWrite(func(c *gin.Context, actor Actor, key string, input core.CreateImportCandidateInput) (json.RawMessage, int, bool, error) {
		return service.CreateImportCandidate(c.Request.Context(), actor, c.Param("projectId"), key, input)
	}))
	group.POST("/projects/:projectId/import-candidates/:candidateID/confirm", g2FullWrite(func(c *gin.Context, actor Actor, key string, input core.G2ConfirmedInput) (json.RawMessage, int, bool, error) {
		return service.ConfirmImportCandidate(c.Request.Context(), actor, c.Param("projectId"), c.Param("candidateID"), key, input)
	}))
	group.POST("/projects/:projectId/import-candidates/:candidateID/reject", g2FullWrite(func(c *gin.Context, actor Actor, key string, input core.G2ConfirmedInput) (json.RawMessage, int, bool, error) {
		return service.RejectImportCandidate(c.Request.Context(), actor, c.Param("projectId"), c.Param("candidateID"), key, input)
	}))
	group.POST("/projects/:projectId/source-anchors", g2FullWrite(func(c *gin.Context, actor Actor, key string, input core.SaveSourceAnchorInput) (json.RawMessage, int, bool, error) {
		return service.SaveSourceAnchor(c.Request.Context(), actor, c.Param("projectId"), key, input)
	}))
	group.POST("/projects/:projectId/research-claims", g2FullWrite(func(c *gin.Context, actor Actor, key string, input core.SaveResearchClaimInput) (json.RawMessage, int, bool, error) {
		return service.SaveResearchClaim(c.Request.Context(), actor, c.Param("projectId"), key, input)
	}))
	group.POST("/projects/:projectId/evidence-cards", g2FullWrite(func(c *gin.Context, actor Actor, key string, input core.SaveEvidenceCardInput) (json.RawMessage, int, bool, error) {
		return service.SaveEvidenceCard(c.Request.Context(), actor, c.Param("projectId"), key, input)
	}))
	group.POST("/projects/:projectId/evidence-cards/:cardID/confirm", g2FullWrite(func(c *gin.Context, actor Actor, key string, input core.G2ConfirmedInput) (json.RawMessage, int, bool, error) {
		return service.ConfirmEvidenceCard(c.Request.Context(), actor, c.Param("projectId"), c.Param("cardID"), key, input)
	}))
	group.POST("/projects/:projectId/evidence-cards/:cardID/review", g2FullWrite(func(c *gin.Context, actor Actor, key string, input core.ReviewEvidenceCardInput) (json.RawMessage, int, bool, error) {
		return service.ReviewEvidenceCard(c.Request.Context(), actor, c.Param("projectId"), c.Param("cardID"), key, input)
	}))
	group.POST("/projects/:projectId/agents/:agent/sessions", g2FullWrite(func(c *gin.Context, actor Actor, key string, input core.G2Expectation) (json.RawMessage, int, bool, error) {
		return service.CreateAgentSession(c.Request.Context(), actor, c.Param("projectId"), c.Param("agent"), key, input)
	}))
	group.POST("/projects/:projectId/agent-sessions/:sessionID/turns", g2FullWrite(func(c *gin.Context, actor Actor, key string, input core.AgentTurnInput) (json.RawMessage, int, bool, error) {
		return service.SendAgentTurn(c.Request.Context(), actor, c.Param("projectId"), c.Param("sessionID"), key, input)
	}))
	group.POST("/projects/:projectId/action-proposals", g2FullWrite(func(c *gin.Context, actor Actor, key string, input core.CreateActionProposalInput) (json.RawMessage, int, bool, error) {
		return service.CreateActionProposal(c.Request.Context(), actor, c.Param("projectId"), key, input)
	}))
	group.POST("/projects/:projectId/action-proposals/:proposalID/confirm", g2FullWrite(func(c *gin.Context, actor Actor, key string, input core.G2ConfirmedInput) (json.RawMessage, int, bool, error) {
		return service.ConfirmActionProposal(c.Request.Context(), actor, c.Param("projectId"), c.Param("proposalID"), key, input)
	}))
	group.POST("/projects/:projectId/action-proposals/:proposalID/reject", g2FullWrite(func(c *gin.Context, actor Actor, key string, input core.G2ConfirmedInput) (json.RawMessage, int, bool, error) {
		return service.RejectActionProposal(c.Request.Context(), actor, c.Param("projectId"), c.Param("proposalID"), key, input)
	}))

	group.POST("/projects/:projectId/research-runs/:runID/results/:resultID/inspect", g2FullPostRead(func(c *gin.Context, actor Actor, _ struct{}) (any, error) {
		return service.InspectResearchResult(c.Request.Context(), actor, c.Param("projectId"), c.Param("runID"), c.Param("resultID"))
	}))
	group.POST("/projects/:projectId/import-candidates/:candidateID/refresh", g2FullPostRead(func(c *gin.Context, actor Actor, _ struct{}) (any, error) {
		return service.RefreshImportCandidate(c.Request.Context(), actor, c.Param("projectId"), c.Param("candidateID"))
	}))
	group.POST("/projects/:projectId/agent-context", g2FullPostRead(func(c *gin.Context, actor Actor, input core.AgentContextInput) (any, error) {
		return service.BuildAgentContext(c.Request.Context(), actor, c.Param("projectId"), input)
	}))
	group.GET("/projects/:projectId/evidence-cards/:cardID", g2FullRead(func(c *gin.Context, actor Actor) (any, error) {
		cards, err := service.ListEvidenceCards(c.Request.Context(), actor, c.Param("projectId"))
		if err != nil {
			return nil, err
		}
		for _, card := range cards {
			if card.ID == c.Param("cardID") {
				return card, nil
			}
		}
		return nil, core.ErrNotFound
	}))
	group.POST("/projects/:projectId/assets/:assetID/policy/proposals", g2FullWrite(func(c *gin.Context, actor Actor, key string, input g2PolicyProposalInput) (json.RawMessage, int, bool, error) {
		if err := core.RequireHumanDecision(c.Request.Context()); err != nil {
			return nil, 0, false, err
		}
		if !input.Confirmed {
			return nil, 0, false, core.ErrG2ConsentRequired
		}
		current, err := service.GetProjectAssetPolicy(c.Request.Context(), actor, c.Param("projectId"), c.Param("assetID"))
		if err != nil {
			return nil, 0, false, err
		}
		if current.Version != input.ExpectedVersion {
			return nil, 0, false, core.ErrVersionConflict
		}
		input.Policy.Reason = input.Reason
		return service.CreateChangeDraft(c.Request.Context(), actor, c.Param("projectId"), key, core.ExtendedChangeSetInput{ExpectedContextRevision: input.ExpectedContextRevision, Reason: input.Reason, SemanticDeltas: []core.SemanticDelta{{Kind: "ProjectAssetScope", TargetID: c.Param("assetID"), ExpectedVersion: current.Version, ProposedAssetPolicy: &input.Policy}}})
	}))
}

type g2PolicyProposalInput struct {
	core.G2Expectation
	Policy    core.ProjectAssetPolicy `json:"policy"`
	Confirmed bool                    `json:"confirmed"`
	Reason    string                  `json:"reason"`
}

func g2FullIdentity(c *gin.Context) (Actor, bool) {
	actor, ok := caller(c)
	if !ok {
		sendError(c, ErrNotFound)
	}
	return actor, ok
}
