package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	service     ApplicationService
	sources     SourceApplicationService
	integration WorkspaceIntegration
}

func NewHandler(deps HandlerDependencies) *Handler {
	return &Handler{
		service: deps.Service, sources: deps.Sources, integration: deps.Integration,
	}
}

func (h *Handler) Service() ApplicationService { return h.service }

func (h *Handler) Register(routes RouteGroups) {
	routes.Read.GET("/projects", h.listProjects)
	routes.Write.POST("/projects", h.createProject)
	routes.Read.GET("/projects/:projectId", h.getProject)
	routes.Write.PUT("/projects/:projectId/spec", h.saveSpec)
	routes.Write.POST("/projects/:projectId/activate", h.activateProject)
	routes.Read.GET("/projects/:projectId/draft-candidates", h.listDraftCandidates)
	routes.Write.POST("/projects/:projectId/draft-candidates", h.createDraftCandidate)
	routes.Read.GET("/projects/:projectId/audit", h.listAuditEvents)
	routes.Read.GET("/projects/:projectId/activation-diff", h.activationDiff)
	routes.Read.GET("/projects/:projectId/template-migration/preview", h.previewTemplateMigration)
	routes.Write.POST("/projects/:projectId/template-migration/preview", h.previewTemplateMigrationPost)
	routes.Write.POST("/projects/:projectId/template-migration", h.changeTemplate)
	routes.Read.GET("/projects/:projectId/template-copies/:version", h.getTemplateCopy)
	routes.Write.POST("/projects/:projectId/template-copy/preview", h.previewTemplateCopyEdit)
	routes.Write.PUT("/projects/:projectId/template-copy", h.saveTemplateCopy)
	routes.Write.POST("/projects/:projectId/discard", h.discardProject)
	routes.Write.POST("/projects/:projectId/restore", h.restoreProject)
	routes.Write.POST("/projects/:projectId/owner-transfer", h.requestOwnerTransfer)
	routes.Write.POST("/projects/:projectId/owner-transfer/:transferId/accept", h.acceptOwnerTransfer)
	routes.Write.PUT("/projects/:projectId/members", h.saveMembers)
	routes.Read.GET("/projects/:projectId/chapters", h.listChapters)
	routes.Read.GET("/projects/:projectId/assets", h.listAssets)
	routes.Write.POST("/projects/:projectId/assets", h.bindAsset)
	routes.Read.POST("/projects/:projectId/retrieval", h.retrieveSources)
	routes.Read.GET("/projects/:projectId/sources/:sourceId", h.getSource)
	routes.Read.GET("/projects/:projectId/sources/:sourceId/context", h.getSourceContext)
	routes.Write.POST("/projects/:projectId/chapters/:chapterId/versions", h.saveChapter)
	routes.Read.GET("/projects/:projectId/access-status", h.accessStatus)
	routes.Read.GET("/projects/:projectId/change-sets", h.listChangeSets)
	routes.Read.GET("/projects/:projectId/change-sets/:changeSetId", h.getChangeSet)
	routes.Write.POST("/projects/:projectId/change-sets", h.createChangeSet)
	routes.Write.POST("/projects/:projectId/change-sets/:changeSetId/apply", h.applyChangeSet)
	routes.Write.POST("/projects/:projectId/change-sets/:changeSetId/reject", h.rejectChangeSet)
}

func caller(c *gin.Context) (Actor, bool) {
	id, userOK := types.UserIDFromContext(c.Request.Context())
	tenant, tenantOK := types.TenantIDFromContext(c.Request.Context())
	return Actor{TenantID: tenant, UserID: id, Role: types.TenantRoleFromContext(c.Request.Context()), SystemAdmin: types.IsSystemAdminFromContext(c.Request.Context())}, userOK && tenantOK && tenant != 0
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
	if status, code, message, details, ok := sourceErrorStatus(err); ok {
		sendErrorDetails(c, status, code, message, details)
		return
	}
	status, code, message := 500, "internal_error", "操作失败，请稍后再试。"
	switch {
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
		// 三处共用这一个结论：保存带引用的正文时复核不过、取来源时该资料不放行、
		// 检索时缺资料底座。它们的共同点是「这批资料此刻不可用」。
		// 措辞不再提「尚未接入」——来源复核已经接入，答 403 是有判据的拒绝，不是缺席。
		status, code, message = 403, "source_access_denied", "资料不可用或未获授权。"
	case errors.Is(err, ErrInvalidState):
		status, code, message = 422, "invalid_state", "当前项目状态或研究条件不满足操作要求。"
	// 交付链（T12 候选采纳 / T13 冻结）的判定。这些是各自包里的 sentinel，
	// 名字与工作区那几个相同却是**不同的值**，所以必须逐个列出来——漏一个就是把
	// 409 答成 500。口径统一在传输层做，领域层不为了对上 HTTP 而改自己的错误。
	case errors.Is(err, candidateadoption.ErrInvalidRequest):
		status, code, message = 400, "invalid_request", "请求字段不符合约定。"
	case errors.Is(err, candidateadoption.ErrNotFound):
		status, code, message = 404, "not_found", "资源不存在或不可访问。"
	case errors.Is(err, delivery.ErrSnapshotNotFound):
		status, code, message = 404, "not_found", "资源不存在或不可访问。"
	case errors.Is(err, candidateadoption.ErrSourceAccessDenied):
		status, code, message = 403, "source_access_denied", "资料授权已不可用。"
	case errors.Is(err, candidateadoption.ErrVersionConflict):
		status, code, message = 409, "version_conflict", "内容已变化，请先读取当前版本。"
	case errors.Is(err, candidateadoption.ErrIdempotencyConflict), errors.Is(err, delivery.ErrIdempotencyConflict):
		status, code, message = 409, "idempotency_conflict", "同一个操作键对应不同请求。"
	case errors.Is(err, candidateadoption.ErrStaleInput), errors.Is(err, delivery.ErrSnapshotStaleInput):
		// 读输入与冻结之间工作区被人改过。契约 §7：发生竞争变更返回 409，重新读取。
		status, code, message = 409, "stale_input", "快照基于的输入已变化，请重新准备。"
	case errors.Is(err, candidateadoption.ErrInvalidState):
		status, code, message = 422, "invalid_state", "当前项目状态或研究条件不满足操作要求。"
	// T14 的导出与下载。与上面同一件事：这些是 delivery 包里**另一个** ErrInvalidRequest，
	// 必须单独列出来——名字相同、值不同，漏掉就是把 400 答成 500。
	case errors.Is(err, delivery.ErrInvalidRequest):
		status, code, message = 400, "invalid_request", "请求字段不符合约定。"
	case errors.Is(err, delivery.ErrExportNotFound):
		status, code, message = 404, "not_found", "资源不存在或不可访问。"
	case errors.Is(err, delivery.ErrExportPreflightBlocked):
		// F10：快照本来就是一份 blocked 的检查结论。要给的是那些 issue，
		// 而不是一次假下载。
		status, code, message = 422, "preflight_blocked", "请先处理交付阻断项。"
	case errors.Is(err, delivery.ErrExportStaleInput):
		// F11：快照基于的输入已经不是此刻的工作区了。
		status, code, message = 409, "stale_input", "快照基于的输入已变化，请重新准备。"
	case errors.Is(err, delivery.ErrExportUnavailable):
		// F13：产物存在但不可下载（failed，或字节已不在）。这不是 404——
		// 资源在，是它此刻不能交出去。
		status, code, message = 422, "invalid_state", "导出文件不可用或完整性校验失败，请重新导出。"
	case errors.Is(err, candidateadoption.ErrDependencyUnavailable):
		// 复核侧「答不出来」的那一档：资料底座读不出结论，或某条引用既没被判可用也没被
		// 判不可用。它和 403 的区别正是「不是你的授权有问题，是此刻判不了」——所以答 503
		// 且 retryable（契约 §6 单列了这一档）。
		//
		// 这原本是一条罕见路径，直到章节保存也走复核（workspaceSourcePolicy）：现在它成了
		// SaveChapter 的常规失败之一，漏掉映射就是把一句「请稍后重试」答成 500。
		// generation 包里另有一个同名的 sentinel，走的是 generation 自己的 handler，
		// 不经过这里——名字相同、值不同，这是本文件反复出现的那类陷阱。
		status, code, message = 503, "dependency_unavailable", "依赖的服务此刻不可用，请稍后重试。"
	}
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": message,
		"retryable": code == "request_in_progress" || code == "dependency_unavailable"},
		"request_id": requestID(c)})
}

func sendErrorDetails(c *gin.Context, status int, code, message string, details any) {
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": message, "retryable": false, "details": details},
		"request_id": requestID(c)})
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

func (h *Handler) listDraftCandidates(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	data, err := h.service.ListDraftCandidates(c.Request.Context(), actor, c.Param("projectId"))
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, data, false)
}

func (h *Handler) createDraftCandidate(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input DraftCandidateInput
	if !decodeBody(c, &input) {
		return
	}
	data, status, replay, err := h.service.CreateDraftCandidate(c.Request.Context(), actor, c.Param("projectId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, status, data, replay)
}

func (h *Handler) listAuditEvents(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	data, err := h.service.ListAuditEvents(c.Request.Context(), actor, c.Param("projectId"))
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, data, false)
}

func (h *Handler) activationDiff(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	since := int64(0)
	if raw := c.Query("since_project_version"); raw != "" {
		if _, err := fmt.Sscanf(raw, "%d", &since); err != nil || since < 0 {
			sendError(c, ErrInvalidRequest)
			return
		}
	}
	diff, err := h.service.ActivationDiff(c.Request.Context(), actor, c.Param("projectId"), since)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, diff, false)
}

func (h *Handler) previewTemplateMigration(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	input := TemplateMigrationInput{TemplateID: c.Query("template_id"), TemplateVersion: c.Query("template_version")}
	if raw := c.Query("expected_project_version"); raw != "" {
		if _, err := fmt.Sscanf(raw, "%d", &input.ExpectedProjectVersion); err != nil {
			sendError(c, ErrInvalidRequest)
			return
		}
	}
	data, err := h.service.PreviewTemplateMigration(c.Request.Context(), actor, c.Param("projectId"), input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, data, false)
}

func (h *Handler) previewTemplateMigrationPost(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	var input TemplateMigrationInput
	if !decodeBody(c, &input) {
		return
	}
	data, err := h.service.PreviewTemplateMigration(c.Request.Context(), actor, c.Param("projectId"), input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, data, false)
}

func (h *Handler) changeTemplate(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input TemplateMigrationInput
	if !decodeBody(c, &input) {
		return
	}
	data, status, replay, err := h.service.ChangeTemplate(c.Request.Context(), actor, c.Param("projectId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, status, data, replay)
}

func (h *Handler) getTemplateCopy(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	version, err := strconv.ParseInt(c.Param("version"), 10, 64)
	if err != nil {
		sendError(c, ErrInvalidRequest)
		return
	}
	copy, err := h.service.GetTemplateCopy(c.Request.Context(), actor, c.Param("projectId"), version)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, copy, false)
}

func (h *Handler) previewTemplateCopyEdit(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	var input TemplateCopyDefinitionInput
	if !decodeBody(c, &input) {
		return
	}
	preview, err := h.service.PreviewTemplateCopyEdit(c.Request.Context(), actor, c.Param("projectId"), input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, preview, false)
}

func (h *Handler) saveTemplateCopy(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input TemplateCopyDefinitionInput
	if !decodeBody(c, &input) {
		return
	}
	data, status, replay, err := h.service.SaveTemplateCopy(c.Request.Context(), actor, c.Param("projectId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, status, data, replay)
}

func (h *Handler) discardProject(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input struct {
		ExpectedProjectVersion int64 `json:"expected_project_version"`
	}
	if !decodeBody(c, &input) {
		return
	}
	data, status, replay, err := h.service.DiscardProject(c.Request.Context(), actor, c.Param("projectId"), key, input.ExpectedProjectVersion)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, status, data, replay)
}

func (h *Handler) restoreProject(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input struct {
		ExpectedProjectVersion int64 `json:"expected_project_version"`
	}
	if !decodeBody(c, &input) {
		return
	}
	data, status, replay, err := h.service.RestoreProject(c.Request.Context(), actor, c.Param("projectId"), key, input.ExpectedProjectVersion)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, status, data, replay)
}

func (h *Handler) requestOwnerTransfer(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input struct {
		ToUserID               string `json:"to_user_id"`
		NewOwnerUserID         string `json:"new_owner_user_id"`
		ExpectedProjectVersion int64  `json:"expected_project_version"`
	}
	if !decodeBody(c, &input) {
		return
	}
	var data json.RawMessage
	var status int
	var replay bool
	var err error
	if input.NewOwnerUserID != "" {
		data, status, replay, err = h.service.TransferOwner(c.Request.Context(), actor, c.Param("projectId"), key, TransferOwnerInput{ExpectedProjectVersion: input.ExpectedProjectVersion, NewOwnerUserID: input.NewOwnerUserID})
	} else {
		data, status, replay, err = h.service.RequestOwnerTransfer(c.Request.Context(), actor, c.Param("projectId"), key, OwnerTransferInput{ExpectedProjectVersion: input.ExpectedProjectVersion, ToUserID: input.ToUserID})
	}
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, status, data, replay)
}

func (h *Handler) acceptOwnerTransfer(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	data, status, replay, err := h.service.AcceptOwnerTransfer(c.Request.Context(), actor, c.Param("projectId"), c.Param("transferId"), key)
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

func (h *Handler) listChangeSets(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	data, err := h.service.ListChangeSets(c.Request.Context(), actor, c.Param("projectId"))
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, data, false)
}

func (h *Handler) getChangeSet(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	data, err := h.service.GetChangeSet(c.Request.Context(), actor, c.Param("projectId"), c.Param("changeSetId"))
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, data, false)
}

func (h *Handler) createChangeSet(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input CreateChangeSetInput
	if !decodeBody(c, &input) {
		return
	}
	data, status, replay, err := h.service.CreateChangeSet(c.Request.Context(), actor, c.Param("projectId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, status, data, replay)
}

func (h *Handler) applyChangeSet(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	data, status, replay, err := h.service.ApplyChangeSet(c.Request.Context(), actor, c.Param("projectId"), c.Param("changeSetId"), key)
	if err != nil {
		sendError(c, err)
		return
	}
	if status == http.StatusConflict {
		// The service commits the stale marker before returning its conflict
		// status, so the caller can GET the ChangeSet and inspect the durable
		// stale state. Keep the write endpoint on the workspace error envelope.
		sendError(c, ErrVersionConflict)
		return
	}
	sendOK(c, status, data, replay)
}

func (h *Handler) rejectChangeSet(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	data, status, replay, err := h.service.RejectChangeSet(c.Request.Context(), actor, c.Param("projectId"), c.Param("changeSetId"), key)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, status, data, replay)
}

func (h *Handler) getSourceContext(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	value, err := h.sources.GetSourceContext(c.Request.Context(), actor, c.Param("projectId"), c.Param("sourceId"))
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, 200, value, false)
}
func (h *Handler) CandidateAdoptionSourcePolicy() candidateadoption.SourcePolicy {
	if h.integration == nil {
		return nil
	}
	return h.integration.CandidateAdoptionSourcePolicy()
}
func (h *Handler) WorkspaceSourcePolicy() SourcePolicy {
	if h.integration == nil {
		return nil
	}
	return h.integration.WorkspaceSourcePolicy()
}
func (h *Handler) DeliveryInputBuilder() DeliveryInputAssembler {
	if h.integration == nil {
		return nil
	}
	return h.integration.DeliveryInputBuilder()
}
