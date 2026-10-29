package workspace

import (
	"net/http"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/gin-gonic/gin"
)

func (h *Handler) applyLegacyRuleOverrides(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input core.LegacyRuleOverridesInput
	if !decodeBody(c, &input) {
		return
	}
	result, err := h.templatesApp.ApplyLegacyRuleOverridesResult(c.Request.Context(), actor, c.Param("projectId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, result.Project, result.Replayed)
}
