package generation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/gin-gonic/gin"
)

const maxGenerationRequestBytes = 1 << 20

type RouteRegistrar interface {
	GET(string, ...gin.HandlerFunc) gin.IRoutes
	POST(string, ...gin.HandlerFunc) gin.IRoutes
}

type ActorResolver func(*gin.Context) (Actor, bool)

type Handler struct {
	Service      Application
	ResolveActor ActorResolver
}

func NewHandler(service Application, resolveActor ActorResolver) *Handler {
	return &Handler{Service: service, ResolveActor: resolveActor}
}

// RegisterRoutes mounts generation endpoints below the authenticated LingDoc group.
func RegisterRoutes(r RouteRegistrar, h *Handler) {
	if h == nil {
		return
	}
	r.POST("/projects/:projectId/generations", h.Start)
	r.GET("/projects/:projectId/generations/:runId", h.Get)
	r.GET("/projects/:projectId/generations/:runId/candidate", h.GetCandidate)
	r.POST("/projects/:projectId/generations/:runId/cancel", h.Cancel)
	r.GET("/projects/:projectId/chapters/:chapterId/candidates", h.ListCandidates)
}

func (h *Handler) Start(c *gin.Context) {
	requestID := generationRequestID(c)
	actor, ok := h.actor(c, requestID)
	if !ok {
		return
	}
	var req Request
	if decodeGenerationRequest(c.Request.Body, &req) != nil {
		writeGenerationError(c, requestID, ErrInvalidRequest)
		return
	}
	if h.Service == nil {
		writeGenerationError(c, requestID, ErrDependencyUnavailable)
		return
	}
	run, err := h.Service.Start(c.Request.Context(), actor, c.Param("projectId"), strings.TrimSpace(c.GetHeader("Idempotency-Key")), req)
	if err != nil {
		writeGenerationError(c, requestID, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"data": run, "request_id": requestID, "meta": gin.H{"replayed": run.Replayed, "refresh_required": run.Replayed}})
}

// decodeGenerationRequest applies the shared API contract at the HTTP edge:
// bound the body before parsing, reject unknown fields, and reject any second
// JSON value or trailing non-whitespace data.
func decodeGenerationRequest(body io.Reader, dst *Request) error {
	encoded, err := io.ReadAll(io.LimitReader(body, maxGenerationRequestBytes+1))
	if err != nil {
		return err
	}
	if len(encoded) > maxGenerationRequestBytes {
		return ErrInvalidRequest
	}
	// JSON permits only space, horizontal tab, carriage return, and line feed.
	// around a value. bytes.TrimSpace would also remove Unicode whitespace that
	// encoding/json correctly rejects, silently broadening the wire contract.
	encoded = bytes.Trim(encoded, " \t\r\n")
	if len(encoded) == 0 || encoded[0] != '{' {
		return ErrInvalidRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return ErrInvalidRequest
		}
		return err
	}
	return nil
}

func (h *Handler) Get(c *gin.Context) {
	requestID := generationRequestID(c)
	actor, ok := h.actor(c, requestID)
	if !ok {
		return
	}
	if h.Service == nil {
		writeGenerationError(c, requestID, ErrDependencyUnavailable)
		return
	}
	run, err := h.Service.Get(c.Request.Context(), actor, c.Param("projectId"), c.Param("runId"))
	if err != nil {
		writeGenerationError(c, requestID, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": run, "request_id": requestID, "meta": gin.H{"replayed": false, "refresh_required": false}})
}

func (h *Handler) Cancel(c *gin.Context) {
	requestID := generationRequestID(c)
	actor, ok := h.actor(c, requestID)
	if !ok {
		return
	}
	if h.Service == nil {
		writeGenerationError(c, requestID, ErrDependencyUnavailable)
		return
	}
	run, err := h.Service.Cancel(c.Request.Context(), actor, c.Param("projectId"), c.Param("runId"))
	if err != nil {
		writeGenerationError(c, requestID, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": run, "request_id": requestID, "meta": gin.H{"replayed": false, "refresh_required": false}})
}

func (h *Handler) ListCandidates(c *gin.Context) {
	requestID := generationRequestID(c)
	actor, ok := h.actor(c, requestID)
	if !ok {
		return
	}
	if h.Service == nil {
		writeGenerationError(c, requestID, ErrDependencyUnavailable)
		return
	}
	candidates, err := h.Service.ListCandidates(c.Request.Context(), actor, c.Param("projectId"), c.Param("chapterId"))
	if err != nil {
		writeGenerationError(c, requestID, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": candidates, "request_id": requestID, "meta": gin.H{"replayed": false, "refresh_required": false}})
}

func (h *Handler) GetCandidate(c *gin.Context) {
	requestID := generationRequestID(c)
	actor, ok := h.actor(c, requestID)
	if !ok {
		return
	}
	if h.Service == nil {
		writeGenerationError(c, requestID, ErrDependencyUnavailable)
		return
	}
	candidate, err := h.Service.GetCandidate(c.Request.Context(), actor, c.Param("projectId"), c.Param("runId"))
	if err != nil {
		writeGenerationError(c, requestID, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": candidate, "request_id": requestID, "meta": gin.H{"replayed": false, "refresh_required": false}})
}

func (h *Handler) actor(c *gin.Context, requestID string) (Actor, bool) {
	if h.ResolveActor == nil {
		writeGenerationError(c, requestID, ErrUnauthenticated)
		return Actor{}, false
	}
	actor, ok := h.ResolveActor(c)
	if !ok || actor.TenantID == 0 || strings.TrimSpace(actor.UserID) == "" {
		writeGenerationError(c, requestID, ErrUnauthenticated)
		return Actor{}, false
	}
	return actor, true
}

func generationRequestID(c *gin.Context) string {
	id := strings.TrimSpace(c.GetString("request_id"))
	if id == "" {
		id = strings.TrimSpace(c.GetHeader("X-Request-ID"))
	}
	if id == "" {
		return "unknown"
	}
	return id
}

// writeGenerationError renders a business failure.
//
// The outward shape of a failure is declared by the error itself, at the place
// where that fact is known. This mapper therefore holds no table of business
// errors: it renders what the error declares, and falls back to a generic
// internal error for the failures that declare nothing.
//
// The denied asset list is the one thing this surface still adds: it is part
// of the answer for an unauthorized asset, not a separate classification.
func writeGenerationError(c *gin.Context, requestID string, err error) {
	form, ok := clienterror.FormOf(err)
	if !ok {
		form = clienterror.InternalError
	}
	errorBody := gin.H{"code": form.Code, "message": form.Message, "retryable": form.Retryable}
	if details := deniedAssetDetails(err); details != nil {
		errorBody["details"] = details
	}
	c.JSON(form.Status, gin.H{"error": errorBody, "request_id": requestID})
}

// deniedAssetDetails reports the assets that were not authorized, when the
// failure carries them.
func deniedAssetDetails(err error) any {
	var denied *AssetNotAuthorizedError
	if !errors.As(err, &denied) {
		return nil
	}
	items := make([]map[string]string, 0, len(denied.Denied))
	for _, item := range denied.Denied {
		items = append(items, map[string]string{"asset_id": item.AssetID, "reason": item.Reason})
	}
	return map[string]any{"denied": items}
}
