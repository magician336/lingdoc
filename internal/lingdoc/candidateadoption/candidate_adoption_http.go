package candidateadoption

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type ActorResolver func(*gin.Context) (string, bool)

type CandidateAdoptionHandler struct {
	Service       *CandidateAdoptionService
	Confirmations *ConfirmationService
	ResolveActor  ActorResolver
}

func NewCandidateAdoptionHandler(service *CandidateAdoptionService, resolveActor ActorResolver) *CandidateAdoptionHandler {
	handler := &CandidateAdoptionHandler{Service: service, ResolveActor: resolveActor}
	if service != nil {
		if store, ok := service.Writer.(*SQLiteCandidateAdoptionStore); ok {
			var sources ConfirmationSourcePolicy
			if current, ok := service.Sources.(ConfirmationSourcePolicy); ok {
				sources = current
			}
			handler.Confirmations = NewConfirmationService(store, sources, service.Authorizer)
		}
	}
	return handler
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
	r.POST("/projects/:projectId/chapters/:chapterId/confirmations", h.ConfirmChapter)
}

type confirmChapterRequest struct {
	ExpectedChapterVersionID string            `json:"expected_chapter_version_id" binding:"required"`
	ExpectedSpecRevision     *int              `json:"expected_spec_revision"`
	ReviewDecisions          *[]ReviewDecision `json:"review_decisions"`
}

func (h *CandidateAdoptionHandler) ConfirmChapter(c *gin.Context) {
	requestID := c.GetString("request_id")
	if requestID == "" {
		requestID = c.GetHeader("X-Request-ID")
	}
	if requestID == "" {
		requestID = "unknown"
	}
	if h.Confirmations == nil {
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
	var req confirmChapterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeCandidateAdoptionError(c, requestID, http.StatusBadRequest, ErrInvalidRequest)
		return
	}
	if req.ExpectedSpecRevision == nil || req.ReviewDecisions == nil {
		writeCandidateAdoptionError(c, requestID, http.StatusBadRequest, ErrInvalidRequest)
		return
	}
	confirmation, replayed, err := h.Confirmations.ConfirmChapter(c.Request.Context(), ConfirmChapterInput{
		ProjectID: c.Param("projectId"), ChapterID: c.Param("chapterId"), ActorID: actor,
		IdempotencyKey:           strings.TrimSpace(c.GetHeader("Idempotency-Key")),
		ExpectedChapterVersionID: req.ExpectedChapterVersionID, ExpectedSpecRevision: *req.ExpectedSpecRevision,
		Decisions: *req.ReviewDecisions,
	})
	if err != nil {
		writeCandidateAdoptionError(c, requestID, candidateAdoptionHTTPStatus(err), err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"data": confirmation, "request_id": requestID,
		"meta": gin.H{"replayed": replayed, "refresh_required": replayed}})
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
	if err := decodeCandidateRequest(c, &req); err != nil {
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
	if err := h.Service.authorize(c.Request.Context(), actor, c.Param("projectId"), "read"); err != nil {
		writeCandidateAdoptionError(c, requestID, candidateAdoptionHTTPStatus(err), err)
		return
	}
	candidate, err := h.Service.Candidates.GetCandidate(c.Request.Context(), c.Param("projectId"), c.Param("candidateId"))
	if err != nil {
		writeCandidateAdoptionError(c, requestID, candidateAdoptionHTTPStatus(err), err)
		return
	}
	if err := h.Service.validateSources(c.Request.Context(), c.Param("projectId"), actor, candidate.SourceIDs); err != nil {
		writeCandidateAdoptionError(c, requestID, candidateAdoptionHTTPStatus(err), err)
		return
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
	actor, ok := h.ResolveActor(c)
	if !ok || strings.TrimSpace(actor) == "" {
		writeCandidateAdoptionError(c, requestID, http.StatusUnauthorized, errors.New("unauthenticated"))
		return
	}
	if err := h.Service.authorize(c.Request.Context(), actor, c.Param("projectId"), "read"); err != nil {
		writeCandidateAdoptionError(c, requestID, candidateAdoptionHTTPStatus(err), err)
		return
	}
	chapters, err := reader.ListChapters(c.Request.Context(), c.Param("projectId"))
	if err != nil {
		writeCandidateAdoptionError(c, requestID, candidateAdoptionHTTPStatus(err), err)
		return
	}
	for _, chapter := range chapters {
		if err := h.Service.validateSources(c.Request.Context(), c.Param("projectId"), actor, chapter.SourceIDs); err != nil {
			writeCandidateAdoptionError(c, requestID, candidateAdoptionHTTPStatus(err), err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"data": chapters, "request_id": requestID, "meta": gin.H{"replayed": false, "refresh_required": false}})
}

func decodeCandidateRequest(c *gin.Context, dst any) error {
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var tail any
	if err := decoder.Decode(&tail); err != io.EOF {
		return errors.New("request body must contain one JSON value")
	}
	return nil
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
	case errors.Is(err, ErrDependencyUnavailable):
		return http.StatusServiceUnavailable
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
	case errors.Is(err, ErrDependencyUnavailable):
		code, message, retryable = "dependency_unavailable", "候选采纳依赖尚未就绪，请稍后重试。", true
	case status == http.StatusUnauthorized:
		code, message = "unauthenticated", "请登录后重试。"
	}
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": message, "retryable": retryable}, "request_id": requestID})
}
