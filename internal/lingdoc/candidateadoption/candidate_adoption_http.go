package candidateadoption

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type ActorResolver func(*gin.Context) (string, bool)

type CandidateAdoptionHandler struct {
	Service      *CandidateAdoptionService
	ResolveActor ActorResolver
}

func NewCandidateAdoptionHandler(service *CandidateAdoptionService, resolveActor ActorResolver) *CandidateAdoptionHandler {
	return &CandidateAdoptionHandler{Service: service, ResolveActor: resolveActor}
}

// RegisterRoutes mounts the contract route below an existing authenticated
// group. Authentication and tenant membership remain the host application's
// responsibility; the actor is read from that authenticated context.
func RegisterRoutes(r gin.IRouter, h *CandidateAdoptionHandler) {
	if h == nil {
		return
	}
	r.POST("/projects/:projectId/chapters/:chapterId/acceptances", h.AcceptCandidate)
	r.GET("/projects/:projectId/candidates/:candidateId", h.GetCandidate)
	r.GET("/projects/:projectId/chapters", h.ListChapters)
}

type acceptCandidateRequest struct {
	CandidateID              string  `json:"candidate_id" binding:"required"`
	ExpectedChapterVersionID *string `json:"expected_chapter_version_id"`
	ExpectedSpecRevision     int     `json:"expected_spec_revision"`
	ReplaceExisting          bool    `json:"replace_existing"`
}

func (h *CandidateAdoptionHandler) AcceptCandidate(c *gin.Context) {
	requestID := c.GetString("request_id")
	if requestID == "" {
		requestID = c.GetHeader("X-Request-ID")
	}
	if requestID == "" {
		requestID = "unknown"
	}
	if h.Service == nil {
		writeCandidateAdoptionError(c, requestID, http.StatusServiceUnavailable, ErrInvalidState)
		return
	}
	if h.ResolveActor == nil {
		writeCandidateAdoptionError(c, requestID, http.StatusUnauthorized, errors.New("unauthenticated"))
		return
	}
	actor, ok := h.ResolveActor(c)
	if !ok || strings.TrimSpace(actor) == "" {
		writeCandidateAdoptionError(c, requestID, http.StatusUnauthorized, errors.New("unauthenticated"))
		return
	}
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	var req acceptCandidateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeCandidateAdoptionError(c, requestID, http.StatusBadRequest, ErrInvalidRequest)
		return
	}
	result, err := h.Service.AcceptCandidate(c.Request.Context(), AcceptCandidateInput{
		ProjectID: c.Param("projectId"), ChapterID: c.Param("chapterId"), CandidateID: req.CandidateID,
		ActorID: actor, IdempotencyKey: key, ExpectedChapterVersionID: req.ExpectedChapterVersionID,
		ExpectedSpecRevision: req.ExpectedSpecRevision, ReplaceExisting: req.ReplaceExisting,
	})
	if err != nil {
		writeCandidateAdoptionError(c, requestID, candidateAdoptionHTTPStatus(err), err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{
		"data":       result.Chapter,
		"request_id": requestID,
		"meta":       gin.H{"replayed": result.Replayed, "refresh_required": result.Replayed},
	})
}

func (h *CandidateAdoptionHandler) GetCandidate(c *gin.Context) {
	requestID := c.GetString("request_id")
	if requestID == "" {
		requestID = c.GetHeader("X-Request-ID")
	}
	if requestID == "" {
		requestID = "unknown"
	}
	if h.Service == nil || h.Service.Candidates == nil {
		writeCandidateAdoptionError(c, requestID, http.StatusServiceUnavailable, ErrInvalidState)
		return
	}
	if h.ResolveActor == nil {
		writeCandidateAdoptionError(c, requestID, http.StatusUnauthorized, errors.New("unauthenticated"))
		return
	}
	actor, ok := h.ResolveActor(c)
	if !ok || strings.TrimSpace(actor) == "" {
		writeCandidateAdoptionError(c, requestID, http.StatusUnauthorized, errors.New("unauthenticated"))
		return
	}
	_ = actor
	candidate, err := h.Service.Candidates.GetCandidate(c.Request.Context(), c.Param("projectId"), c.Param("candidateId"))
	if err != nil {
		writeCandidateAdoptionError(c, requestID, candidateAdoptionHTTPStatus(err), err)
		return
	}
	if h.Service.Authorizer != nil {
		if err := h.Service.Authorizer.Authorize(c.Request.Context(), actor, c.Param("projectId"), candidate.ChapterID); err != nil {
			writeCandidateAdoptionError(c, requestID, candidateAdoptionHTTPStatus(err), err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"data": candidate, "request_id": requestID, "meta": gin.H{"replayed": false, "refresh_required": false}})
}

func (h *CandidateAdoptionHandler) ListChapters(c *gin.Context) {
	requestID := c.GetString("request_id")
	if requestID == "" {
		requestID = c.GetHeader("X-Request-ID")
	}
	if requestID == "" {
		requestID = "unknown"
	}
	if h.Service == nil {
		writeCandidateAdoptionError(c, requestID, http.StatusServiceUnavailable, ErrInvalidState)
		return
	}
	reader, ok := h.Service.Workspace.(ChapterReader)
	if !ok {
		writeCandidateAdoptionError(c, requestID, http.StatusServiceUnavailable, ErrInvalidState)
		return
	}
	if h.ResolveActor == nil {
		writeCandidateAdoptionError(c, requestID, http.StatusUnauthorized, errors.New("unauthenticated"))
		return
	}
	if actor, ok := h.ResolveActor(c); !ok || strings.TrimSpace(actor) == "" {
		writeCandidateAdoptionError(c, requestID, http.StatusUnauthorized, errors.New("unauthenticated"))
		return
	}
	chapters, err := reader.ListChapters(c.Request.Context(), c.Param("projectId"))
	if err != nil {
		writeCandidateAdoptionError(c, requestID, candidateAdoptionHTTPStatus(err), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": chapters, "request_id": requestID, "meta": gin.H{"replayed": false, "refresh_required": false}})
}

func candidateAdoptionHTTPStatus(err error) int {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		return http.StatusBadRequest
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrForbidden), errors.Is(err, ErrSourceAccessDenied):
		return http.StatusForbidden
	case errors.Is(err, ErrVersionConflict), errors.Is(err, ErrStaleInput), errors.Is(err, ErrIdempotencyConflict):
		return http.StatusConflict
	case errors.Is(err, ErrInvalidState):
		return http.StatusUnprocessableEntity
	default:
		return http.StatusInternalServerError
	}
}

func writeCandidateAdoptionError(c *gin.Context, requestID string, status int, err error) {
	code, message, retryable := "internal_error", "服务器暂时无法完成请求。", false
	switch {
	case errors.Is(err, ErrInvalidRequest):
		code, message = "invalid_request", "请求字段不符合约定。"
	case errors.Is(err, ErrNotFound):
		code, message = "not_found", "资源不存在或不可访问。"
	case errors.Is(err, ErrForbidden):
		code, message = "forbidden", "无权执行该动作。"
	case errors.Is(err, ErrSourceAccessDenied):
		code, message = "source_access_denied", "资料授权已不可用。"
	case errors.Is(err, ErrStaleInput):
		code, message = "stale_input", "候选稿或快照基于旧输入。"
	case errors.Is(err, ErrVersionConflict):
		code, message = "version_conflict", "内容已变化，请先读取当前版本。"
	case errors.Is(err, ErrInvalidState):
		code, message = "invalid_state", "当前阶段不能执行该动作。"
	case errors.Is(err, ErrIdempotencyConflict):
		code, message = "idempotency_conflict", "同一幂等键对应不同请求。"
	case status == http.StatusUnauthorized:
		code, message = "unauthenticated", "请登录后重试。"
	}
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": message, "retryable": retryable}, "request_id": requestID})
}
