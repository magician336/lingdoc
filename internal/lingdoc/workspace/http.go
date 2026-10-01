package workspace

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	service ApplicationService
	sources SourceApplicationService
}

func NewHandler(deps HandlerDependencies) *Handler {
	return &Handler{
		service: deps.Service, sources: deps.Sources,
	}
}

func (h *Handler) Service() ApplicationService { return h.service }

func (h *Handler) Register(routes RouteGroups) {
	routes.Read.GET("/projects", h.listProjects)
	routes.Write.POST("/projects", h.createProject)
	routes.Read.GET("/projects/:projectId", h.getProject)
	routes.Write.PUT("/projects/:projectId/spec", h.saveSpec)
	routes.Write.POST("/projects/:projectId/activate", h.activateProject)
	routes.Write.PUT("/projects/:projectId/members", h.saveMembers)
	routes.Read.GET("/projects/:projectId/chapters", h.listChapters)
	routes.Read.GET("/projects/:projectId/assets", h.listAssets)
	routes.Write.POST("/projects/:projectId/assets", h.bindAsset)
	routes.Read.POST("/projects/:projectId/retrieval", h.retrieveSources)
	routes.Read.GET("/projects/:projectId/sources/:sourceId", h.getSource)
	routes.Write.POST("/projects/:projectId/chapters/:chapterId/versions", h.saveChapter)
	routes.Read.GET("/projects/:projectId/access-status", h.accessStatus)
}

func caller(c *gin.Context) (Actor, bool) {
	id, userOK := types.UserIDFromContext(c.Request.Context())
	tenant, tenantOK := types.TenantIDFromContext(c.Request.Context())
	return Actor{TenantID: tenant, UserID: id}, userOK && tenantOK && tenant != 0
}

func requestID(c *gin.Context) string {
	id, _ := types.RequestIDFromContext(c.Request.Context())
	return id
}

func sendOK(c *gin.Context, code int, data any, replay bool) {
	c.JSON(code, gin.H{"data": data, "request_id": requestID(c),
		"meta": gin.H{"replayed": replay, "refresh_required": replay}})
}

func sendError(c *gin.Context, err error) {
	status, code, message := 500, "internal_error", "操作失败，请稍后再试。"
	var details any
	if sourceStatus, sourceCode, sourceMessage, sourceDetails, ok := sourceErrorStatus(err); ok {
		status, code, message, details = sourceStatus, sourceCode, sourceMessage, sourceDetails
	}
	switch {
	case details != nil:
	case errors.Is(err, ErrInvalidRequest):
		status, code, message = 400, "invalid_request", "请求字段不符合约定。"
	case errors.Is(err, evidence.ErrInvalidBinding):
		status, code, message = 400, "invalid_request", "请求字段不符合约定。"
	case errors.Is(err, evidence.ErrIdempotencyConflict):
		status, code, message = 409, "idempotency_conflict", "同一个操作键对应不同请求。"
	case errors.Is(err, evidence.ErrAssetNotFound):
		status, code, message = 404, "not_found", "资源不存在或不可访问。"
	case errors.Is(err, ErrNotFound):
		status, code, message = 404, "not_found", "资源不存在或不可访问。"
	case errors.Is(err, ErrVersionConflict):
		status, code, message = 409, "version_conflict", "内容已变化，请先读取当前版本。"
	case errors.Is(err, ErrIdempotencyConflict):
		status, code, message = 409, "idempotency_conflict", "同一个操作键对应不同请求。"
	case errors.Is(err, ErrRequestInProgress):
		status, code, message = 409, "request_in_progress", "原请求仍在提交，请稍后用相同操作键重试。"
		c.Header("Retry-After", "1")
	case errors.Is(err, ErrSourceUnavailable):
		status, code, message = 403, "source_access_denied", "来源授权尚未接入，不能保存带引用的正文。"
	case errors.Is(err, ErrInvalidState):
		status, code, message = 422, "invalid_state", "当前项目状态或研究条件不满足操作要求。"
	}
	errorBody := gin.H{"code": code, "message": message, "retryable": code == "request_in_progress"}
	if details != nil {
		errorBody["details"] = details
	}
	c.JSON(status, gin.H{"error": errorBody, "request_id": requestID(c)})
}

func sourceErrorStatus(err error) (int, string, string, any, bool) {
	var denied *DeniedAssetsError
	if !errors.As(err, &denied) {
		return 0, "", "", nil, false
	}
	items := make([]map[string]string, 0, len(denied.Denied))
	for _, item := range denied.Denied {
		items = append(items, map[string]string{"asset_id": item.AssetID, "reason": string(item.Reason)})
	}
	return 422, "asset_not_authorized", "请求中存在未获授权的资料，未开始处理。", map[string]any{"denied": items}, true
}

func identity(c *gin.Context) (Actor, bool) {
	actor, ok := caller(c)
	if !ok {
		sendError(c, ErrNotFound)
	}
	return actor, ok
}

func decodeBody(c *gin.Context, dst any) bool {
	decoder := json.NewDecoder(io.LimitReader(c.Request.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		sendError(c, ErrInvalidRequest)
		return false
	}
	var tail any
	if err := decoder.Decode(&tail); err != io.EOF {
		sendError(c, ErrInvalidRequest)
		return false
	}
	return true
}

func idempotencyKey(c *gin.Context) (string, bool) {
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if len(key) < 8 || len(key) > 128 {
		sendError(c, ErrInvalidRequest)
		return "", false
	}
	return key, true
}

func (h *Handler) listProjects(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	items, truncated, err := h.service.ListProjects(c.Request.Context(), actor)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, 200, gin.H{"items": items, "truncated": truncated}, false)
}

func (h *Handler) createProject(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input CreateProjectInput
	if !decodeBody(c, &input) {
		return
	}
	data, status, replay, err := h.service.CreateProject(c.Request.Context(), actor, key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, status, data, replay)
}

func (h *Handler) getProject(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	project, err := h.service.GetProject(c.Request.Context(), actor, c.Param("projectId"))
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, 200, project, false)
}

func (h *Handler) saveSpec(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input SaveSpecInput
	if !decodeBody(c, &input) {
		return
	}
	data, status, replay, err := h.service.SaveSpec(c.Request.Context(), actor, c.Param("projectId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, status, data, replay)
}

func (h *Handler) activateProject(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input ActivateProjectInput
	if !decodeBody(c, &input) {
		return
	}
	data, status, replay, err := h.service.ActivateProject(c.Request.Context(), actor, c.Param("projectId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, status, data, replay)
}

func (h *Handler) saveMembers(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input SaveMembersInput
	if !decodeBody(c, &input) {
		return
	}
	data, status, replay, err := h.service.SaveMembers(c.Request.Context(), actor, c.Param("projectId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, status, data, replay)
}

func (h *Handler) listChapters(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	data, err := h.service.ListChapters(c.Request.Context(), actor, c.Param("projectId"))
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, 200, data, false)
}

func (h *Handler) listAssets(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	assets, err := h.sources.ListAssets(c.Request.Context(), actor, c.Param("projectId"))
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, assets, false)
}

func (h *Handler) bindAsset(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input struct {
		KnowledgeID string `json:"knowledge_id"`
	}
	if !decodeBody(c, &input) {
		return
	}
	asset, replay, err := h.sources.BindAsset(c.Request.Context(), actor, c.Param("projectId"), key, input.KnowledgeID)
	if err != nil {
		sendError(c, err)
		return
	}
	status := http.StatusCreated
	if replay {
		status = http.StatusOK
	}
	sendOK(c, status, asset, replay)
}

func (h *Handler) getSource(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	source, err := h.sources.GetSource(c.Request.Context(), actor, c.Param("projectId"), c.Param("sourceId"))
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, source, false)
}

func (h *Handler) retrieveSources(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	var input RetrieveSourcesInput
	if !decodeBody(c, &input) {
		return
	}
	result, err := h.sources.RetrieveSources(c.Request.Context(), actor, c.Param("projectId"), input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, result, false)
}

func (h *Handler) saveChapter(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input SaveChapterInput
	if !decodeBody(c, &input) {
		return
	}
	data, status, replay, err := h.service.SaveChapter(c.Request.Context(), actor,
		c.Param("projectId"), c.Param("chapterId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, status, data, replay)
}

func (h *Handler) accessStatus(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	status, err := h.sources.AccessStatus(c.Request.Context(), actor, c.Param("projectId"))
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, status, false)
}
