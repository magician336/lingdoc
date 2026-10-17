package workspace

import (
	"context"
	"encoding/json"
	"github.com/Tencent/WeKnora/internal/lingdoc/reviewanchor"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/gin-gonic/gin"
	"net/http"
)

type reviewAnchorWriteApplication interface {
	SetReviewAnchor(context.Context, Actor, string, string, string, string, core.SetReviewAnchorInput) (json.RawMessage, int, bool, error)
}
type reviewAnchorReadApplication interface {
	GetCurrentConfirmation(context.Context, Actor, string, string) (json.RawMessage, error)
}

func (h *Handler) setReviewAnchor(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var wire struct {
		ExpectedChapterVersionID    *string         `json:"expected_chapter_version_id"`
		ExpectedSpecRevision        int64           `json:"expected_spec_revision"`
		ExpectedWorkingCopyRevision int64           `json:"expected_working_copy_revision"`
		Anchor                      json.RawMessage `json:"anchor"`
	}
	if !decodeBody(c, &wire) {
		return
	}
	if len(wire.Anchor) == 0 {
		sendError(c, ErrInvalidRequest)
		return
	}
	var anchor *reviewanchor.Selection
	if err := json.Unmarshal(wire.Anchor, &anchor); err != nil {
		sendError(c, ErrInvalidRequest)
		return
	}
	app := h.reviewAnchorsWrite
	if app == nil {
		sendError(c, ErrInvalidState)
		return
	}
	input := core.SetReviewAnchorInput{ExpectedChapterVersionID: wire.ExpectedChapterVersionID, ExpectedSpecRevision: wire.ExpectedSpecRevision, ExpectedWorkingCopyRevision: wire.ExpectedWorkingCopyRevision, Anchor: anchor}
	data, status, replay, err := app.SetReviewAnchor(c.Request.Context(), actor, c.Param("projectId"), c.Param("chapterId"), c.Param("itemId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, status, data, replay)
}
func (h *Handler) getCurrentConfirmation(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	app := h.reviewAnchorsRead
	if app == nil {
		sendError(c, ErrInvalidState)
		return
	}
	data, err := app.GetCurrentConfirmation(c.Request.Context(), actor, c.Param("projectId"), c.Param("chapterId"))
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, data, false)
}
