package workspace

import (
	"errors"

	"github.com/Tencent/WeKnora/internal/lingdoc/rulesetstore"
	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	governance "github.com/Tencent/WeKnora/internal/lingdoc/templategovernance"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// RegisterG4FullRoutes uses the already authenticated LingDoc group. It adds
// no authentication middleware and never accepts caller identity from JSON.
func RegisterG4FullRoutes(group *gin.RouterGroup, runtime *TemplateGovernanceRuntime) {
	if group == nil {
		return
	}
	h := g4FullHTTP{runtime: runtime}
	catalog := group.Group("/template-catalog")
	catalog.GET("/replay-fixtures", h.listReplayFixtures)
	catalog.GET("/profiles", h.listProfiles)
	catalog.POST("/profiles", h.createProfile)
	catalog.GET("/profiles/:templateId/versions", h.listVersions)
	catalog.POST("/profiles/:templateId/versions", h.saveDraft)
	catalog.GET("/profiles/:templateId/versions/:version", h.getVersion)
	catalog.POST("/profiles/:templateId/versions/:version/publish", h.publish)
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

type g4FullHTTP struct{ runtime *TemplateGovernanceRuntime }

var ErrG4HumanDecisionRequired = errors.New("template_governance_human_decision_required")

func (h g4FullHTTP) actor(c *gin.Context, human bool) (governance.Actor, bool) {
	actor, ok := caller(c)
	if !ok {
		sendError(c, ErrNotFound)
		return governance.Actor{}, false
	}
	if h.runtime == nil || h.runtime.Service() == nil {
		sendG4Error(c, governance.ErrUnavailable)
		return governance.Actor{}, false
	}
	if human {
		if _, machine := types.TenantAPIKeyScopeFromContext(c.Request.Context()); machine {
			sendG4Error(c, ErrG4HumanDecisionRequired)
			return governance.Actor{}, false
		}
		if principal, exists := types.PrincipalFromContext(c.Request.Context()); exists {
			switch principal.Type {
			case types.PrincipalAPITenant, types.PrincipalAPIPlatform, types.PrincipalAPIExternalUser, types.PrincipalEmbedChannel, types.PrincipalEmbedSession, types.PrincipalEmbedVisitor:
				sendG4Error(c, ErrG4HumanDecisionRequired)
				return governance.Actor{}, false
			}
		}
	}
	c.Request = c.Request.WithContext(governance.ObserveOperationReplay(c.Request.Context(), func() { c.Set("g4_operation_replayed", true) }))
	return g4GovActor(actor), true
}
func g4PathBody(c *gin.Context, id, version string) bool {
	if id != c.Param("templateId") || version != c.Param("version") {
		sendError(c, ErrInvalidRequest)
		return false
	}
	return true
}
func (h g4FullHTTP) listReplayFixtures(c *gin.Context) {
	actor, ok := h.actor(c, false)
	if !ok {
		return
	}
	out, err := h.runtime.ListReplayFixtures(actor)
	if err != nil {
		sendG4Error(c, err)
		return
	}
	sendG4OK(c, 200, out, false)
}
func (h g4FullHTTP) listProfiles(c *gin.Context) {
	actor, ok := h.actor(c, false)
	if !ok {
		return
	}
	out, err := h.runtime.Service().ListProfiles(c.Request.Context(), actor, governance.CatalogFilter{Scope: c.Query("scope"), State: template.RuleSetState(c.Query("state"))})
	if err != nil {
		sendG4Error(c, err)
		return
	}
	sendG4OK(c, 200, out, false)
}
func (h g4FullHTTP) createProfile(c *gin.Context) {
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
		sendG4Error(c, err)
		return
	}
	sendG4OK(c, 201, out, false)
}
func (h g4FullHTTP) listVersions(c *gin.Context) {
	actor, ok := h.actor(c, false)
	if !ok {
		return
	}
	out, err := h.runtime.Service().ListVersions(c.Request.Context(), actor, c.Param("templateId"))
	if err != nil {
		sendG4Error(c, err)
		return
	}
	sendG4OK(c, 200, out, false)
}
func (h g4FullHTTP) getVersion(c *gin.Context) {
	actor, ok := h.actor(c, false)
	if !ok {
		return
	}
	out, err := h.runtime.Service().GetVersion(c.Request.Context(), actor, c.Param("templateId"), c.Param("version"))
	if err != nil {
		sendG4Error(c, err)
		return
	}
	sendG4OK(c, 200, out, false)
}
func (h g4FullHTTP) saveDraft(c *gin.Context) {
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
		sendG4Error(c, err)
		return
	}
	sendG4OK(c, 200, out, false)
}
func (h g4FullHTTP) publish(c *gin.Context) {
	actor, ok := h.actor(c, true)
	if !ok {
		return
	}
	var input governance.PublishInput
	if !decodeBody(c, &input) || !g4PathBody(c, input.TemplateID, input.Version) {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	out, err := h.runtime.Service().Publish(c.Request.Context(), actor, key, input)
	if err != nil {
		sendG4Error(c, err)
		return
	}
	sendG4OK(c, 200, out, false)
}
func (h g4FullHTTP) withdraw(c *gin.Context) {
	actor, ok := h.actor(c, true)
	if !ok {
		return
	}
	var input governance.GovernanceInput
	if !decodeBody(c, &input) || !g4PathBody(c, input.TemplateID, input.Version) {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	out, err := h.runtime.Service().Withdraw(c.Request.Context(), actor, key, input)
	if err != nil {
		sendG4Error(c, err)
		return
	}
	sendG4OK(c, 200, out, false)
}
func (h g4FullHTTP) recall(c *gin.Context) {
	actor, ok := h.actor(c, true)
	if !ok {
		return
	}
	var input governance.RecallInput
	if !decodeBody(c, &input) || !g4PathBody(c, input.TemplateID, input.Version) {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	out, err := h.runtime.Service().Recall(c.Request.Context(), actor, key, input)
	if err != nil {
		sendG4Error(c, err)
		return
	}
	sendG4OK(c, 200, out, false)
}
func (h g4FullHTTP) ruleDefinitions(c *gin.Context) {
	actor, ok := h.actor(c, false)
	if !ok {
		return
	}
	err := h.runtime.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		return h.runtime.AuthorizeTemplate(c.Request.Context(), tx, actor, governance.ActionRead, "")
	})
	if err != nil {
		sendG4Error(c, err)
		return
	}
	sendG4OK(c, 200, template.RuleDefinitions(), false)
}
func (h g4FullHTTP) listRuleSets(c *gin.Context) {
	actor, ok := h.actor(c, false)
	if !ok {
		return
	}
	out, err := h.runtime.Service().ListRuleSetVersions(c.Request.Context(), actor, c.Param("rulesetId"))
	if err != nil {
		sendG4Error(c, err)
		return
	}
	sendG4OK(c, 200, out, false)
}
func (h g4FullHTTP) createRuleSet(c *gin.Context) {
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
		sendG4Error(c, err)
		return
	}
	sendG4OK(c, 201, out, false)
}
func (h g4FullHTTP) ruleTransition(action string) gin.HandlerFunc {
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
			sendG4Error(c, err)
			return
		}
		sendG4OK(c, 200, out, false)
	}
}
func (h g4FullHTTP) listSuggestions(c *gin.Context) {
	actor, ok := h.actor(c, false)
	if !ok {
		return
	}
	out, err := h.runtime.Service().ListRuleSuggestions(c.Request.Context(), actor, c.Param("projectId"))
	if err != nil {
		sendG4Error(c, err)
		return
	}
	sendG4OK(c, 200, out, false)
}
func (h g4FullHTTP) getSuggestion(c *gin.Context) {
	actor, ok := h.actor(c, false)
	if !ok {
		return
	}
	out, err := h.runtime.Service().GetRuleSuggestion(c.Request.Context(), actor, c.Param("projectId"), c.Param("runId"))
	if err != nil {
		sendG4Error(c, err)
		return
	}
	sendG4OK(c, 200, out, false)
}
func (h g4FullHTTP) requestSuggestion(c *gin.Context) {
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
		sendG4Error(c, err)
		return
	}
	sendG4OK(c, 200, out, false)
}
func (h g4FullHTTP) acceptSuggestion(c *gin.Context) {
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
		sendG4Error(c, err)
		return
	}
	sendG4OK(c, 200, out, false)
}
func (h g4FullHTTP) rejectSuggestion(c *gin.Context) {
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
		sendG4Error(c, err)
		return
	}
	sendG4OK(c, 200, out, false)
}

// G4ErrorStatus is additive to the shared transport mapper. Unknown/core errors
// continue through sendError, preserving existing source and workspace codes.
func G4ErrorStatus(err error) (int, string, string, bool) {
	switch {
	case errors.Is(err, ErrG4HumanDecisionRequired):
		return 403, "human_decision_required", "请使用当前人工账号复核并提交。", true
	case errors.Is(err, ErrG4CrossTenantSourceUnsupported):
		return 422, "cross_tenant_source_unsupported", "当前目录流程无法在同一事务复核跨租户共享授权。", true
	case errors.Is(err, governance.ErrUnauthorized):
		return 403, "template_governance_authorization_unavailable", "目录治理授权不可用。", true
	case errors.Is(err, governance.ErrSourceUnavailable):
		return 403, "source_access_denied", "来源已不可用或无当前授权。", true
	case errors.Is(err, governance.ErrUnavailable), errors.Is(err, governance.ErrModelUnavailable):
		return 503, "template_governance_unavailable", "目录或规则建议服务尚未配置。", true
	case errors.Is(err, governance.ErrNotFound), errors.Is(err, rulesetstore.ErrRuleSetNotFound):
		return 404, "not_found", "资源不存在或不可访问。", true
	case errors.Is(err, governance.ErrIdempotencyConflict):
		return 409, "idempotency_conflict", "同一个操作键对应不同请求。", true
	case errors.Is(err, governance.ErrVersionConflict), errors.Is(err, governance.ErrStale), errors.Is(err, rulesetstore.ErrRuleSetVersionExists), errors.Is(err, rulesetstore.ErrRuleSetStateConflict):
		return 409, "version_conflict", "内容或基线已变化，请先读取当前版本。", true
	case errors.Is(err, governance.ErrInvalidState), errors.Is(err, template.ErrRuleSetNotDraft), errors.Is(err, template.ErrRuleSetNotPublished):
		return 409, "invalid_state", "当前版本状态不允许此操作。", true
	case errors.Is(err, governance.ErrReplayRequired):
		return 422, "template_replay_required", "须用服务器注册且绑定当前版本的正反例完成回放。", true
	case errors.Is(err, governance.ErrInvalidOutput):
		return 502, "invalid_model_output", "模型输出不符合当前目标和规则约束。", true
	case errors.Is(err, governance.ErrInvalidRequest), errors.Is(err, template.ErrInvalidDefinition), errors.Is(err, template.ErrInvalidRuleSet), errors.Is(err, rulesetstore.ErrDraftHashMustBeEmpty):
		return 400, "invalid_request", "请求字段不符合约定。", true
	}
	return 0, "", "", false
}
func sendG4OK(c *gin.Context, status int, data any, _ bool) {
	replayed, _ := c.Get("g4_operation_replayed")
	sendOK(c, status, data, replayed == true)
}

func sendG4Error(c *gin.Context, err error) {
	if status, code, message, ok := G4ErrorStatus(err); ok {
		sendErrorDetails(c, status, code, message, nil)
		return
	}
	sendError(c, err)
}
