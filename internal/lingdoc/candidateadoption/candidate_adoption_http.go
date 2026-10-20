package candidateadoption

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/gin-gonic/gin"
)

type RouteRegistrar interface {
	GET(string, ...gin.HandlerFunc) gin.IRoutes
	POST(string, ...gin.HandlerFunc) gin.IRoutes
}

type ActorResolver func(*gin.Context) (string, bool)

type CandidateAdoptionHandler struct {
	Service       Application
	Confirmations ConfirmationApplication
	ResolveActor  ActorResolver
}

func NewCandidateAdoptionHandler(service Application, confirmations ConfirmationApplication, resolveActor ActorResolver) *CandidateAdoptionHandler {
	return &CandidateAdoptionHandler{
		Service:       newSurfaceApplication(service),
		Confirmations: newSurfaceConfirmationApplication(confirmations),
		ResolveActor:  resolveActor,
	}
}

// RegisterRoutes mounts the contract route below an existing authenticated
// group. Authentication and tenant membership remain the host application's
// responsibility; the actor is read from that authenticated context.
func RegisterRoutes(r RouteRegistrar, h *CandidateAdoptionHandler) {
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
		writeCandidateAdoptionError(c, requestID, ErrAdoptionServiceUnavailable)
		return
	}
	if h.ResolveActor == nil {
		writeCandidateAdoptionError(c, requestID, ErrUnauthenticated)
		return
	}
	actor, ok := h.ResolveActor(c)
	if !ok || strings.TrimSpace(actor) == "" {
		writeCandidateAdoptionError(c, requestID, ErrUnauthenticated)
		return
	}
	var req confirmChapterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeCandidateAdoptionError(c, requestID, ErrInvalidRequest)
		return
	}
	if req.ExpectedSpecRevision == nil || req.ReviewDecisions == nil {
		writeCandidateAdoptionError(c, requestID, ErrInvalidRequest)
		return
	}
	confirmation, replayed, err := h.Confirmations.ConfirmChapter(c.Request.Context(), ConfirmChapterInput{
		ProjectID: c.Param("projectId"), ChapterID: c.Param("chapterId"), ActorID: actor,
		IdempotencyKey:           strings.TrimSpace(c.GetHeader("Idempotency-Key")),
		ExpectedChapterVersionID: req.ExpectedChapterVersionID, ExpectedSpecRevision: *req.ExpectedSpecRevision,
		Decisions: *req.ReviewDecisions,
	})
	if err != nil {
		writeCandidateAdoptionError(c, requestID, err)
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
		writeCandidateAdoptionError(c, requestID, ErrAdoptionServiceUnavailable)
		return
	}
	if h.ResolveActor == nil {
		writeCandidateAdoptionError(c, requestID, ErrUnauthenticated)
		return
	}
	actor, ok := h.ResolveActor(c)
	if !ok || strings.TrimSpace(actor) == "" {
		writeCandidateAdoptionError(c, requestID, ErrUnauthenticated)
		return
	}
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	var req acceptCandidateRequest
	if err := decodeCandidateRequest(c, &req); err != nil {
		writeCandidateAdoptionError(c, requestID, ErrInvalidRequest)
		return
	}
	result, err := h.Service.AcceptCandidate(c.Request.Context(), AcceptCandidateInput{
		ProjectID: c.Param("projectId"), ChapterID: c.Param("chapterId"), CandidateID: req.CandidateID,
		ActorID: actor, IdempotencyKey: key, ExpectedChapterVersionID: req.ExpectedChapterVersionID,
		ExpectedSpecRevision: req.ExpectedSpecRevision, ReplaceExisting: req.ReplaceExisting,
	})
	if err != nil {
		writeCandidateAdoptionError(c, requestID, err)
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
	if h.Service == nil {
		writeCandidateAdoptionError(c, requestID, ErrAdoptionServiceUnavailable)
		return
	}
	if h.ResolveActor == nil {
		writeCandidateAdoptionError(c, requestID, ErrUnauthenticated)
		return
	}
	actor, ok := h.ResolveActor(c)
	if !ok || strings.TrimSpace(actor) == "" {
		writeCandidateAdoptionError(c, requestID, ErrUnauthenticated)
		return
	}
	candidate, err := h.Service.ReadCandidate(c.Request.Context(), actor, c.Param("projectId"), c.Param("candidateId"))
	if err != nil {
		writeCandidateAdoptionError(c, requestID, err)
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
		writeCandidateAdoptionError(c, requestID, ErrAdoptionServiceUnavailable)
		return
	}
	if h.ResolveActor == nil {
		writeCandidateAdoptionError(c, requestID, ErrUnauthenticated)
		return
	}
	actor, ok := h.ResolveActor(c)
	if !ok || strings.TrimSpace(actor) == "" {
		writeCandidateAdoptionError(c, requestID, ErrUnauthenticated)
		return
	}
	chapters, err := h.Service.ReadChapters(c.Request.Context(), actor, c.Param("projectId"))
	if err != nil {
		writeCandidateAdoptionError(c, requestID, err)
		return
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

// writeCandidateAdoptionError renders a business failure.
//
// The outward shape of a failure is declared by the error itself, at the place
// where that fact is known. This mapper therefore holds no table of business
// errors: it renders what the error declares, and falls back to a generic
// internal error for the failures that declare nothing.
func writeCandidateAdoptionError(c *gin.Context, requestID string, err error) {
	form, ok := clienterror.FormOf(err)
	if !ok {
		form = clienterror.InternalError
	}
	c.JSON(form.Status, gin.H{"error": gin.H{"code": form.Code, "message": form.Message, "retryable": form.Retryable}, "request_id": requestID})
}
