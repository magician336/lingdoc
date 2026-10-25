package workspace

import (
	"context"
	"encoding/json"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/gin-gonic/gin"
)

// The manual-judgement channel of the F01 runner, over HTTP (issue #649).
//
// A prose assertion in the F01 contract is one no machine can read.  Until a
// human judges it, the runner records it as ``not_run`` and the provider's
// semantics stay unverified.  The runner can only believe such a judgement
// when it can check it against a **server-side audit record** (issue #633:
// ``manual_decision_audit.verify_against_audit``).  These two routes are that
// server side:
//
//	POST /projects/{projectId}/manual-judgements
//	    The Owner submits one judgement.  The answer is the authoritative
//	    record: the exact ten fields the runner's ledger stores.  The operator
//	    writes that answer into the ledger verbatim, and the export below
//	    proves it byte for byte.
//
//	GET  /projects/{projectId}/manual-judgements/audit-records
//	    The export the runner checks judgements against.  Every record carries
//	    the seven audit fields **at the top level**, next to the audit
//	    identifier the judgement names.
//
// The two answers must agree byte for byte, and they do so by construction
// rather than by two places writing the same shape: the domain side stores
// one record and projects the very same strings back out of it, and this
// transport publishes what it is given without re-serializing it.
//
// What the transport does **not** do is decide who judged, through which
// channel, under which authorization, or when.  Those four facts are the
// server's to derive, so a caller cannot supply them: the request body has
// five fields and the decoder refuses every other one.

// ManualJudgementApplication is the narrow port the two manual-judgement
// routes speak to.
//
// The domain side owns the judgement itself: who may record it, what is
// written down, how the channel is classified, and where the audit record
// lives.  The transport only asks for the authoritative answer and for the
// export, and it publishes them as they are.
//
// The two methods are the domain side's own, so the implementation satisfies
// this port without an adapter.
type ManualJudgementApplication interface {
	// SubmitManualJudgement records one judgement for a project.  It returns
	// the authoritative record as raw JSON, the status code to answer with,
	// and whether an earlier receipt was replayed.
	SubmitManualJudgement(ctx context.Context, actor core.Actor, projectID, key string, input core.ManualJudgementInput) (json.RawMessage, int, bool, error)
	// ExportManualJudgements reads the audit records of a project.
	ExportManualJudgements(ctx context.Context, actor core.Actor, projectID string) ([]core.ManualJudgementAudit, error)
}

// RegisterManualJudgementRoutes registers the two manual-judgement routes.
//
// The routes are registered only when the port is actually there: a family
// of routes that cannot answer without its port must not be registered at
// all, so an absent port is an absent endpoint rather than a nil
// dereference on the first request.
func RegisterManualJudgementRoutes(routes RouteGroups, app ManualJudgementApplication) {
	if nilPort(routes.Read) || nilPort(routes.Write) || nilPort(app) {
		return
	}
	handler := manualJudgementRoutes{app: app}
	routes.Write.POST("/projects/:projectId/manual-judgements", handler.submitManualJudgement)
	routes.Read.GET("/projects/:projectId/manual-judgements/audit-records", handler.exportManualJudgements)
}

type manualJudgementRoutes struct{ app ManualJudgementApplication }

// submitManualJudgement answers with the authoritative judgement record.
//
// The body is decoded strictly: a request that carries one of the four
// server-derived fields is refused with 400 rather than answered with the
// caller's version of it.  The caller cannot make the server say it judged
// as somebody else, through a channel it chose, at a time it picked.
//
// The answer is the domain's own bytes.  They are published as they are:
// re-serializing them here would put a second spelling of the record on the
// wire, and the runner compares the ledger against the export byte for byte.
func (h manualJudgementRoutes) submitManualJudgement(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input core.ManualJudgementInput
	if !decodeBody(c, &input) {
		return
	}
	record, status, replayed, err := h.app.SubmitManualJudgement(c.Request.Context(), actor, c.Param("projectId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, status, record, replayed)
}

// exportManualJudgements answers with the project's audit export.
//
// The domain side already answers with an empty list rather than with
// nothing, so an empty project publishes an empty array.
func (h manualJudgementRoutes) exportManualJudgements(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	records, err := h.app.ExportManualJudgements(c.Request.Context(), actor, c.Param("projectId"))
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, 200, records, false)
}
