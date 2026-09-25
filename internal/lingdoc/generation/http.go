package generation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const maxGenerationRequestBytes = 1 << 20

type ActorResolver func(*gin.Context) (Actor, bool)

type Handler struct {
	Service      *Service
	ResolveActor ActorResolver
}

func NewHandler(service *Service, resolveActor ActorResolver) *Handler {
	return &Handler{Service: service, ResolveActor: resolveActor}
}

// RegisterRoutes mounts generation endpoints below the authenticated LingDoc group.
func RegisterRoutes(r gin.IRouter, h *Handler) {
	if h == nil {
		return
	}
	r.POST("/projects/:projectId/generations", h.Start)
	r.GET("/projects/:projectId/generations/:runId", h.Get)
}

func (h *Handler) Start(c *gin.Context) {
	requestID := generationRequestID(c)
	actor, ok := h.actor(c, requestID)
	if !ok {
		return
	}
	var req Request
	if decodeGenerationRequest(c.Request.Body, &req) != nil {
		writeGenerationError(c, requestID, http.StatusBadRequest, ErrInvalidRequest)
		return
	}
	if h.Service == nil {
		writeGenerationError(c, requestID, http.StatusServiceUnavailable, ErrDependencyUnavailable)
		return
	}
	run, err := h.Service.Start(c.Request.Context(), actor, c.Param("projectId"), strings.TrimSpace(c.GetHeader("Idempotency-Key")), req)
	if err != nil {
		writeGenerationError(c, requestID, generationHTTPStatus(err), err)
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
		writeGenerationError(c, requestID, http.StatusServiceUnavailable, ErrDependencyUnavailable)
		return
	}
	run, err := h.Service.Get(c.Request.Context(), actor, c.Param("projectId"), c.Param("runId"))
	if err != nil {
		writeGenerationError(c, requestID, generationHTTPStatus(err), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": run, "request_id": requestID, "meta": gin.H{"replayed": false, "refresh_required": false}})
}

func (h *Handler) actor(c *gin.Context, requestID string) (Actor, bool) {
	if h.ResolveActor == nil {
		writeGenerationError(c, requestID, http.StatusUnauthorized, errors.New("unauthenticated"))
		return Actor{}, false
	}
	actor, ok := h.ResolveActor(c)
	if !ok || actor.TenantID == 0 || strings.TrimSpace(actor.UserID) == "" {
		writeGenerationError(c, requestID, http.StatusUnauthorized, errors.New("unauthenticated"))
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

func generationHTTPStatus(err error) int {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		return http.StatusBadRequest
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrForbidden), errors.Is(err, ErrSourceAccessDenied):
		return http.StatusForbidden
	case errors.Is(err, ErrVersionConflict), errors.Is(err, ErrStaleInput), errors.Is(err, ErrIdempotencyConflict):
		return http.StatusConflict
	case errors.Is(err, ErrDependencyUnavailable):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func writeGenerationError(c *gin.Context, requestID string, status int, err error) {
	code, message := "internal_error", "服务器暂时无法完成请求。"
	switch {
	case errors.Is(err, ErrInvalidRequest):
		code, message = "invalid_request", "请求字段不符合约定。"
	case errors.Is(err, ErrNotFound):
		code, message = "not_found", "资源不存在或不可访问。"
	case errors.Is(err, ErrForbidden):
		code, message = "forbidden", "无权执行该动作。"
	case errors.Is(err, ErrSourceAccessDenied):
		code, message = "source_access_denied", "资料授权已不可用。"
	case errors.Is(err, ErrVersionConflict):
		code, message = "version_conflict", "输入版本已变化，请重新读取。"
	case errors.Is(err, ErrIdempotencyConflict):
		code, message = "idempotency_conflict", "同一幂等键对应不同请求。"
	case errors.Is(err, ErrDependencyUnavailable):
		code, message = "dependency_unavailable", "依赖暂时不可用。"
	case status == http.StatusUnauthorized:
		code, message = "unauthenticated", "请登录后重试。"
	}
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": message, "retryable": status >= 500}, "request_id": requestID})
}
