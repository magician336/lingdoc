package workspace

import (
	"strconv"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterTemplateGovernanceRoutes uses the already authenticated LingDoc group. It adds
// no authentication middleware and never accepts caller identity from JSON.
func RegisterTemplateGovernanceRoutes(group *gin.RouterGroup, runtime *TemplateGovernanceRuntime) {
	if group == nil {
		return
	}
	h := governanceRoutesHandler{runtime: runtime}
	catalog := group.Group("/template-catalog")
	catalog.GET("/replay-fixtures", h.listReplayFixtures)
	catalog.GET("/profiles", h.listProfiles)
	catalog.GET("/profiles/:templateId/audit-events", h.listAuditHistory)
	catalog.POST("/profiles", h.createProfile)
	catalog.GET("/profiles/:templateId/versions", h.listVersions)
	catalog.POST("/profiles/:templateId/versions", h.saveDraft)
	catalog.GET("/profiles/:templateId/versions/:version", h.getVersion)
	catalog.POST("/profiles/:templateId/versions/:version/publish", h.publish)
	catalog.POST("/profiles/:templateId/versions/:version/replay-fixtures", h.registerReplayFixtures)
	catalog.GET("/profiles/:templateId/versions/:version/replay-coverage", h.replayCoverage)
	catalog.POST("/profiles/:templateId/versions/:version/withdraw", h.withdraw)
	catalog.POST("/profiles/:templateId/versions/:version/recall", h.recall)
	catalog.GET("/rule-definitions", h.ruleDefinitions)
	catalog.GET("/rulesets/:rulesetId/versions", h.listRuleSets)
	catalog.POST("/rulesets/:rulesetId/versions", h.createRuleSet)
	catalog.POST("/rulesets/:rulesetId/versions/:version/publish", h.ruleTransition("publish"))
	catalog.POST("/rulesets/:rulesetId/versions/:version/withdraw", h.ruleTransition("withdraw"))
	catalog.POST("/rulesets/:rulesetId/versions/:version/recall", h.ruleTransition("recall"))
	suggestions := group.Group("/projects/:projectId/rule-suggestions")
	suggestions.GET("", h.listSuggestions)
	suggestions.POST("", h.requestSuggestion)
	suggestions.GET("/:runId", h.getSuggestion)
	suggestions.POST("/:runId/accept", h.acceptSuggestion)
	suggestions.POST("/:runId/reject", h.rejectSuggestion)
}

type governanceRoutesHandler struct{ runtime *TemplateGovernanceRuntime }

// ErrGovernanceHumanDecisionRequired is a governance decision that has to be made by a
// human account. It states its own outward form.
var ErrGovernanceHumanDecisionRequired = clienterror.New(clienterror.Form{Status: 403, Code: "human_decision_required", Message: "请使用当前人工账号复核并提交。", Retryable: false}, "template_governance_human_decision_required")

func (h governanceRoutesHandler) actor(c *gin.Context, human bool) (governance.Actor, bool) {
	actor, ok := caller(c)
	if !ok {
		sendError(c, ErrNotFound)
		return governance.Actor{}, false
	}
	if h.runtime == nil || h.runtime.Service() == nil {
		sendGovernanceError(c, governance.ErrUnavailable)
		return governance.Actor{}, false
	}
	if human {
		if _, machine := types.TenantAPIKeyScopeFromContext(c.Request.Context()); machine {
			sendGovernanceError(c, ErrGovernanceHumanDecisionRequired)
			return governance.Actor{}, false
		}
		if principal, exists := types.PrincipalFromContext(c.Request.Context()); exists {
			switch principal.Type {
			case types.PrincipalAPITenant, types.PrincipalAPIPlatform, types.PrincipalAPIExternalUser, types.PrincipalEmbedChannel, types.PrincipalEmbedSession, types.PrincipalEmbedVisitor:
				sendGovernanceError(c, ErrGovernanceHumanDecisionRequired)
				return governance.Actor{}, false
			}
		}
	}
	c.Request = c.Request.WithContext(governance.ObserveOperationReplay(c.Request.Context(), func() { c.Set("g4_operation_replayed", true) }))
	return governanceActor(actor), true
}
func matchesPathBody(c *gin.Context, id, version string) bool {
	if id != c.Param("templateId") || version != c.Param("version") {
		sendError(c, ErrInvalidRequest)
		return false
	}
	return true
}
func (h governanceRoutesHandler) listReplayFixtures(c *gin.Context) {
	actor, ok := h.actor(c, false)
	if !ok {
		return
	}
	out, err := h.runtime.ListReplayFixtures(c.Request.Context(), actor)
	if err != nil {
		sendGovernanceError(c, err)
		return
	}
	sendGovernanceOK(c, 200, out, false)
}

// registerReplayFixtures is the thin transport spelling of the one
// registration operation: it binds the path identity to the body, requires a
// human decision (machines get 403 human_decision_required like publish), and
// renders what the operation and its errors declare. All validation, trial
// replay, atomicity, idempotency and audit live in
// governance.Service.RegisterFixtures.
func (h governanceRoutesHandler) registerReplayFixtures(c *gin.Context) {
	actor, ok := h.actor(c, true)
	if !ok {
		return
	}
	var input governance.RegisterFixturesInput
	if !decodeBody(c, &input) || !matchesPathBody(c, input.TemplateID, input.Version) {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	out, err := h.runtime.Service().RegisterFixtures(c.Request.Context(), actor, key, input)
	if err != nil {
		sendGovernanceError(c, err)
		return
	}
	sendGovernanceOK(c, 201, out, false)
}

// replayCoverage is the manager-only coverage readback; the operation itself
// answers non-managers with the anti-enumerating not-found.
func (h governanceRoutesHandler) replayCoverage(c *gin.Context) {
	actor, ok := h.actor(c, true)
	if !ok {
		return
	}
	out, err := h.runtime.Service().ReplayCoverage(c.Request.Context(), actor, c.Param("templateId"), c.Param("version"))
	if err != nil {
		sendGovernanceError(c, err)
		return
	}
	sendGovernanceOK(c, 200, out, false)
}
func (h governanceRoutesHandler) listAuditHistory(c *gin.Context) {
	actor, ok := h.actor(c, false)
	if !ok {
		return
	}
	filter := governance.AuditHistoryFilter{Limit: 0}
	if raw := c.Query("after_id"); raw != "" {
		cursor, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || cursor == 0 {
			sendGovernanceError(c, governance.ErrInvalidRequest)
			return
		}
		filter.AfterID = cursor
	}
	if raw := c.Query("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			sendGovernanceError(c, governance.ErrInvalidRequest)
			return
		}
		filter.Limit = limit
	}
	out, err := h.runtime.Service().ListAuditHistory(c.Request.Context(), actor, c.Param("templateId"), filter)
	if err != nil {
		sendGovernanceError(c, err)
		return
	}
	sendGovernanceOK(c, 200, out, false)
}

func (h governanceRoutesHandler) listProfiles(c *gin.Context) {
	actor, ok := h.actor(c, false)
	if !ok {
		return
	}
	out, err := h.runtime.Service().ListProfiles(c.Request.Context(), actor, governance.CatalogFilter{Scope: c.Query("scope"), State: template.RuleSetState(c.Query("state"))})
	if err != nil {
		sendGovernanceError(c, err)
		return
	}
	sendGovernanceOK(c, 200, out, false)
}
func (h governanceRoutesHandler) createProfile(c *gin.Context) {
	actor, ok := h.actor(c, false)
	if !ok {
		return
	}
	var input governance.CreateProfileInput
	if !decodeBody(c, &input) {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	out, err := h.runtime.Service().CreateProfile(c.Request.Context(), actor, key, input)
	if err != nil {
		sendGovernanceError(c, err)
		return
	}
	sendGovernanceOK(c, 201, out, false)
}
func (h governanceRoutesHandler) listVersions(c *gin.Context) {
	actor, ok := h.actor(c, false)
	if !ok {
		return
	}
	out, err := h.runtime.Service().ListVersions(c.Request.Context(), actor, c.Param("templateId"))
	if err != nil {
		sendGovernanceError(c, err)
		return
	}
	sendGovernanceOK(c, 200, out, false)
}
func (h governanceRoutesHandler) getVersion(c *gin.Context) {
	actor, ok := h.actor(c, false)
	if !ok {
		return
	}
	out, err := h.runtime.Service().GetVersion(c.Request.Context(), actor, c.Param("templateId"), c.Param("version"))
	if err != nil {
		sendGovernanceError(c, err)
		return
	}
	sendGovernanceOK(c, 200, out, false)
}
func (h governanceRoutesHandler) saveDraft(c *gin.Context) {
	actor, ok := h.actor(c, false)
	if !ok {
		return
	}
	var input governance.SaveDraftInput
	if !decodeBody(c, &input) {
		return
	}
	if input.TemplateID != c.Param("templateId") {
		sendError(c, ErrInvalidRequest)
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	out, err := h.runtime.Service().SaveDraft(c.Request.Context(), actor, key, input)
	if err != nil {
		sendGovernanceError(c, err)
		return
	}
	sendGovernanceOK(c, 200, out, false)
}
func (h governanceRoutesHandler) publish(c *gin.Context) {
	actor, ok := h.actor(c, true)
	if !ok {
		return
	}
	var input governance.PublishInput
	if !decodeBody(c, &input) || !matchesPathBody(c, input.TemplateID, input.Version) {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	out, err := h.runtime.Service().Publish(c.Request.Context(), actor, key, input)
	if err != nil {
		sendGovernanceError(c, err)
		return
	}
	sendGovernanceOK(c, 200, out, false)
}
func (h governanceRoutesHandler) withdraw(c *gin.Context) {
	actor, ok := h.actor(c, true)
	if !ok {
		return
	}
	var input governance.GovernanceInput
	if !decodeBody(c, &input) || !matchesPathBody(c, input.TemplateID, input.Version) {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	out, err := h.runtime.Service().Withdraw(c.Request.Context(), actor, key, input)
	if err != nil {
		sendGovernanceError(c, err)
		return
	}
	sendGovernanceOK(c, 200, out, false)
}
func (h governanceRoutesHandler) recall(c *gin.Context) {
	actor, ok := h.actor(c, true)
	if !ok {
		return
	}
	var input governance.RecallInput
	if !decodeBody(c, &input) || !matchesPathBody(c, input.TemplateID, input.Version) {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	out, err := h.runtime.Service().Recall(c.Request.Context(), actor, key, input)
	if err != nil {
		sendGovernanceError(c, err)
		return
	}
	sendGovernanceOK(c, 200, out, false)
}
func (h governanceRoutesHandler) ruleDefinitions(c *gin.Context) {
	actor, ok := h.actor(c, false)
	if !ok {
		return
	}
	err := h.runtime.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		return h.runtime.AuthorizeTemplate(c.Request.Context(), tx, actor, governance.ActionRead, "")
	})
	if err != nil {
		sendGovernanceError(c, err)
		return
	}
	sendGovernanceOK(c, 200, template.RuleDefinitions(), false)
}
func (h governanceRoutesHandler) listRuleSets(c *gin.Context) {
	actor, ok := h.actor(c, false)
	if !ok {
		return
	}
	out, err := h.runtime.Service().ListRuleSetVersions(c.Request.Context(), actor, c.Param("rulesetId"))
	if err != nil {
		sendGovernanceError(c, err)
		return
	}
	sendGovernanceOK(c, 200, out, false)
}
func (h governanceRoutesHandler) createRuleSet(c *gin.Context) {
	actor, ok := h.actor(c, false)
	if !ok {
		return
	}
	var input governance.RuleDraftInput
	if !decodeBody(c, &input) {
		return
	}
	if input.Draft.ID != c.Param("rulesetId") {
		sendError(c, ErrInvalidRequest)
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	out, err := h.runtime.Service().CreateRuleSetDraft(c.Request.Context(), actor, key, input)
	if err != nil {
		sendGovernanceError(c, err)
		return
	}
	sendGovernanceOK(c, 201, out, false)
}
func (h governanceRoutesHandler) ruleTransition(action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor, ok := h.actor(c, true)
		if !ok {
			return
		}
		var input governance.RuleGovernanceInput
		if !decodeBody(c, &input) {
			return
		}
		key, ok := idempotencyKey(c)
		if !ok {
			return
		}
		svc := h.runtime.Service()
		var out template.RuleSet
		var err error
		switch action {
		case "publish":
			out, err = svc.PublishRuleSet(c.Request.Context(), actor, c.Param("rulesetId"), c.Param("version"), key, input)
		case "withdraw":
			out, err = svc.WithdrawRuleSet(c.Request.Context(), actor, c.Param("rulesetId"), c.Param("version"), key, input)
		case "recall":
			out, err = svc.RecallRuleSet(c.Request.Context(), actor, c.Param("rulesetId"), c.Param("version"), key, input)
		default:
			err = ErrInvalidRequest
		}
		if err != nil {
			sendGovernanceError(c, err)
			return
		}
		sendGovernanceOK(c, 200, out, false)
	}
}
func (h governanceRoutesHandler) listSuggestions(c *gin.Context) {
	actor, ok := h.actor(c, false)
	if !ok {
		return
	}
	out, err := h.runtime.Service().ListRuleSuggestions(c.Request.Context(), actor, c.Param("projectId"))
	if err != nil {
		sendGovernanceError(c, err)
		return
	}
	sendGovernanceOK(c, 200, out, false)
}
func (h governanceRoutesHandler) getSuggestion(c *gin.Context) {
	actor, ok := h.actor(c, false)
	if !ok {
		return
	}
	out, err := h.runtime.Service().GetRuleSuggestion(c.Request.Context(), actor, c.Param("projectId"), c.Param("runId"))
	if err != nil {
		sendGovernanceError(c, err)
		return
	}
	sendGovernanceOK(c, 200, out, false)
}
func (h governanceRoutesHandler) requestSuggestion(c *gin.Context) {
	actor, ok := h.actor(c, false)
	if !ok {
		return
	}
	var scope governance.SuggestionScope
	if !decodeBody(c, &scope) {
		return
	}
	if scope.ProjectID != c.Param("projectId") {
		sendError(c, ErrInvalidRequest)
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	out, err := h.runtime.Service().RequestRuleSuggestion(c.Request.Context(), actor, key, scope)
	if err != nil {
		sendGovernanceError(c, err)
		return
	}
	sendGovernanceOK(c, 200, out, false)
}
func (h governanceRoutesHandler) acceptSuggestion(c *gin.Context) {
	actor, ok := h.actor(c, true)
	if !ok {
		return
	}
	var input governance.AcceptSuggestionInput
	if !decodeBody(c, &input) {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	out, err := h.runtime.Service().AcceptRuleSuggestion(c.Request.Context(), actor, c.Param("projectId"), c.Param("runId"), key, input)
	if err != nil {
		sendGovernanceError(c, err)
		return
	}
	sendGovernanceOK(c, 200, out, false)
}
func (h governanceRoutesHandler) rejectSuggestion(c *gin.Context) {
	actor, ok := h.actor(c, true)
	if !ok {
		return
	}
	var input governance.RejectSuggestionInput
	if !decodeBody(c, &input) {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	out, err := h.runtime.Service().RejectRuleSuggestion(c.Request.Context(), actor, c.Param("projectId"), c.Param("runId"), key, input)
	if err != nil {
		sendGovernanceError(c, err)
		return
	}
	sendGovernanceOK(c, 200, out, false)
}

// sendGovernanceError renders a business failure of the rule governance surface.
//
// The outward shape of a failure is declared by the error itself, at the place
// where that fact is known. This mapper therefore holds no table of business
// errors: it renders what the error declares, and falls back to a generic
// internal error for the failures that declare nothing.
//
// A failure that carries no form of its own keeps the shared workspace
// answers, including the denied asset list.
func sendGovernanceError(c *gin.Context, err error) {
	if form, ok := clienterror.FormOf(err); ok {
		c.JSON(form.Status, gin.H{"error": gin.H{"code": form.Code, "message": form.Message,
			"retryable": form.Retryable}, "request_id": requestID(c)})
		return
	}
	if status, code, message, details, ok := sourceErrorStatus(err); ok {
		sendErrorDetails(c, status, code, message, details)
		return
	}
	c.JSON(500, gin.H{"error": gin.H{"code": "internal_error", "message": "操作失败，请稍后再试。",
		"retryable": true}, "request_id": requestID(c)})
}
func sendGovernanceOK(c *gin.Context, status int, data any, _ bool) {
	replayed, _ := c.Get("g4_operation_replayed")
	sendOK(c, status, data, replayed == true)
}
