package workspace

import (
	"net/http"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/gin-gonic/gin"
)

func (h *Handler) createFieldArchive(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input core.CreateFieldArchiveInput
	if !decodeBody(c, &input) {
		return
	}
	result, err := h.templatesApp.CreateFieldArchiveResult(c.Request.Context(), actor, c.Param("projectId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusCreated, result.Archive, result.Replayed)
}

func (h *Handler) removeArchivedField(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input core.RemoveArchivedFieldInput
	if !decodeBody(c, &input) {
		return
	}
	result, err := h.templatesApp.RemoveArchivedFieldResult(c.Request.Context(), actor, c.Param("projectId"), c.Param("archiveId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, result.Archive, result.Replayed)
}

func (h *Handler) listFieldArchives(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	items, err := h.templatesApp.ListFieldArchives(c.Request.Context(), actor, c.Param("projectId"))
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, gin.H{"items": items}, false)
}

func (h *Handler) getFieldArchive(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	item, err := h.templatesApp.GetFieldArchive(c.Request.Context(), actor, c.Param("projectId"), c.Param("archiveId"))
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, item, false)
}
