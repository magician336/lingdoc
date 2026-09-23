package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Handler struct {
	service  *Service
	bindings *evidence.Bindings
	gateway  evidence.AssetGateway
}

func NewHandler(db *gorm.DB) *Handler {
	bindings := evidence.NewBindings(db)
	return &Handler{
		service:  NewService(db, ContractDemoTemplate{}),
		bindings: bindings,
		gateway:  evidence.NewAssetGateway(bindings, tenantAssetAuthorizer{bindings: bindings}),
	}
}

func (h *Handler) Service() *Service { return h.service }

func (h *Handler) Register(v1 *gin.RouterGroup) {
	group := v1.Group("/lingdoc")
	group.GET("/projects", h.listProjects)
	group.POST("/projects", h.createProject)
	group.GET("/projects/:projectId", h.getProject)
	group.PUT("/projects/:projectId/spec", h.saveSpec)
	group.POST("/projects/:projectId/activate", h.activateProject)
	group.PUT("/projects/:projectId/members", h.saveMembers)
	group.GET("/projects/:projectId/chapters", h.listChapters)
	group.GET("/projects/:projectId/assets", h.listAssets)
	group.POST("/projects/:projectId/chapters/:chapterId/versions", h.saveChapter)
	group.GET("/projects/:projectId/access-status", h.accessStatus)
}

// tenantAssetAuthorizer is the conservative first production adapter for the
// evidence domain. Project membership is checked by workspacecore before this
// handler runs; this adapter additionally requires that the bound asset still
// belongs to the caller's tenant. A future KB role adapter can replace it
// without changing AssetGateway or the HTTP contract.
type tenantAssetAuthorizer struct{ bindings *evidence.Bindings }

func (a tenantAssetAuthorizer) CanAccessAsset(ctx context.Context, actor evidence.Actor, projectID string, asset evidence.Asset) (bool, error) {
	scope, err := a.bindings.AssetScope(ctx, projectID, asset.ID)
	if errors.Is(err, evidence.ErrAssetNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	tenantID, err := strconv.ParseUint(actor.TenantID, 10, 64)
	if err != nil {
		return false, nil
	}
	return scope.OwnerTenantID == tenantID, nil
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
	switch {
	case errors.Is(err, ErrInvalidRequest):
		status, code, message = 400, "invalid_request", "请求字段不符合约定。"
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
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": message, "retryable": code == "request_in_progress"},
		"request_id": requestID(c)})
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
	projectID := c.Param("projectId")
	if err := h.service.Authorize(c.Request.Context(), actor, projectID, "read"); err != nil {
		sendError(c, err)
		return
	}
	assets, err := h.bindings.BoundAssets(c.Request.Context(), projectID)
	if err != nil {
		sendError(c, err)
		return
	}
	requested := make([]string, 0, len(assets))
	for _, asset := range assets {
		requested = append(requested, asset.ID)
	}
	resolved, err := h.gateway.ResolveAllowed(c.Request.Context(), projectID, evidence.Actor{UserID: actor.UserID, TenantID: strconv.FormatUint(actor.TenantID, 10)}, requested)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, resolved.Allowed, false)
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
	id := c.Param("projectId")
	if err := h.service.Authorize(c.Request.Context(), actor, id, "read"); err != nil {
		sendError(c, err)
		return
	}
	// Source authorization is not integrated in this slice. Until T09 provides
	// SourcePolicy, report unknown and do not signal content access.
	sendOK(c, http.StatusOK, gin.H{"project_id": id, "content_access": "unknown",
		"recovery_actions": []string{}, "can_create_project": true}, false)
}
