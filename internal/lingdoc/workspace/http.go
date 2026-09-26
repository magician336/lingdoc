package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Handler struct {
	service   *Service
	bindings  *evidence.Bindings
	gateway   evidence.AssetGateway
	kbShares  interfaces.KBShareService
	knowledge interfaces.KnowledgeBaseService
	db        *gorm.DB
}

func NewHandler(db *gorm.DB, kbShares interfaces.KBShareService, knowledge interfaces.KnowledgeBaseService) *Handler {
	bindings := evidence.NewBindings(db)
	bindings.SetKnowledgeSignalReader(dbKnowledgeReader{db: db})
	authorizer := evidence.NewFixedAuthorizer(bindings, kbReadChecker{shares: kbShares})
	h := &Handler{
		bindings:  bindings,
		gateway:   evidence.NewAssetGateway(bindings, authorizer),
		kbShares:  kbShares,
		knowledge: knowledge,
		db:        db,
	}
	// service 要等 h 建好之后再装：章节保存的来源复核拿的是**这个** h，不是一份快照
	//（WorkspaceSourcePolicy 每次调用现取 h 上的 gateway）。一行结构体字面量绑不死这件事。
	h.service = NewService(db, ContractDemoTemplate{}, h.WorkspaceSourcePolicy())
	return h
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
	group.POST("/projects/:projectId/assets", h.bindAsset)
	group.POST("/projects/:projectId/retrieval", h.retrieveSources)
	group.GET("/projects/:projectId/sources/:sourceId", h.getSource)
	group.POST("/projects/:projectId/chapters/:chapterId/versions", h.saveChapter)
	group.GET("/projects/:projectId/access-status", h.accessStatus)
}

type kbReadChecker struct{ shares interfaces.KBShareService }

func (a kbReadChecker) CanReadKB(ctx context.Context, actor evidence.Actor, knowledgeBaseID string, ownerTenantID uint64) (bool, error) {
	caller := types.CallerFromContext(ctx)
	if caller.UserID != actor.UserID || strconv.FormatUint(caller.TenantID, 10) != actor.TenantID || a.shares == nil {
		return false, nil
	}
	return access.NewKBPermissions(ctx, a.shares).Check(knowledgeBaseID, ownerTenantID, types.OrgRoleViewer)
}

func (a kbReadChecker) canReadKnowledgeBase(ctx context.Context, actor Actor, kb *types.KnowledgeBase) (bool, error) {
	return a.CanReadKB(ctx, evidence.Actor{UserID: actor.UserID, TenantID: strconv.FormatUint(actor.TenantID, 10)}, kb.ID, kb.TenantID)
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
		status, code, message = 422, "invalid_state", "当前阶段不能执行该动作。"
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

type bindAssetInput struct {
	KnowledgeID string `json:"knowledge_id"`
}

func (h *Handler) bindAsset(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	projectID := c.Param("projectId")
	if err := h.service.Authorize(c.Request.Context(), actor, projectID, "write"); err != nil {
		sendError(c, err)
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input bindAssetInput
	if !decodeBody(c, &input) {
		return
	}
	input.KnowledgeID = strings.TrimSpace(input.KnowledgeID)
	if input.KnowledgeID == "" {
		sendError(c, ErrInvalidRequest)
		return
	}
	var knowledge types.Knowledge
	if err := h.db.WithContext(c.Request.Context()).Where("id = ? AND deleted_at IS NULL", input.KnowledgeID).First(&knowledge).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			sendError(c, evidence.ErrAssetNotFound)
		} else {
			sendError(c, err)
		}
		return
	}
	if h.knowledge == nil {
		sendError(c, evidence.ErrAssetNotFound)
		return
	}
	kb, err := h.knowledge.GetKnowledgeBaseByIDOnly(c.Request.Context(), knowledge.KnowledgeBaseID)
	if err != nil || kb == nil {
		sendError(c, evidence.ErrAssetNotFound)
		return
	}
	allowed, err := (kbReadChecker{shares: h.kbShares}).canReadKnowledgeBase(c.Request.Context(), actor, kb)
	if err != nil {
		sendError(c, err)
		return
	}
	if !allowed {
		sendError(c, evidence.ErrAssetNotFound)
		return
	}
	asset, replay, err := h.bindings.BindIdempotent(c.Request.Context(), actor.TenantID, actor.UserID, projectID, key, evidence.BindInput{
		TenantID: kb.TenantID, ProjectID: projectID, KnowledgeID: knowledge.ID,
		KnowledgeBaseID: knowledge.KnowledgeBaseID, Title: knowledge.Title, CreatedBy: actor.UserID,
		Signal: evidence.KnowledgeSignal{KnowledgeID: knowledge.ID, ParseStatus: knowledge.ParseStatus, FileHash: knowledge.FileHash, FileSize: knowledge.FileSize, ProcessedAt: valueTime(knowledge.ProcessedAt)},
	})
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
	projectID, sourceID := c.Param("projectId"), c.Param("sourceId")
	if err := h.service.Authorize(c.Request.Context(), actor, projectID, "read"); err != nil {
		sendError(c, err)
		return
	}
	var chunk types.Chunk
	if err := h.db.WithContext(c.Request.Context()).Where("id = ?", sourceID).First(&chunk).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			sendError(c, ErrNotFound)
		} else {
			sendError(c, err)
		}
		return
	}
	asset, err := h.bindings.AssetForKnowledge(c.Request.Context(), projectID, chunk.KnowledgeID)
	if err != nil {
		sendError(c, ErrNotFound)
		return
	}
	evidenceActor := evidence.Actor{UserID: actor.UserID, TenantID: strconv.FormatUint(actor.TenantID, 10)}
	resolved, err := h.gateway.ResolveAllowed(c.Request.Context(), projectID, evidenceActor, []string{asset.ID})
	if err != nil {
		sendError(c, err)
		return
	}
	if len(resolved.Allowed) != 1 {
		sendError(c, ErrSourceUnavailable)
		return
	}
	asset = resolved.Allowed[0]
	hit := &types.SearchResult{ID: chunk.ID, KnowledgeID: chunk.KnowledgeID, ChunkIndex: chunk.ChunkIndex,
		StartAt: chunk.StartAt, EndAt: chunk.EndAt, Content: chunk.Content,
		ContentRevision: chunk.ContentRevision, KnowledgeTitle: asset.Title}
	origins := evidence.NewOriginReader(dbKnowledgeReader{db: h.db})
	sources, err := evidence.NewSourceResolver(origins).Resolve(c.Request.Context(), asset, []*types.SearchResult{hit})
	if err != nil || len(sources) != 1 {
		if err != nil {
			sendError(c, err)
		} else {
			sendError(c, ErrNotFound)
		}
		return
	}
	source := sources[0]
	policy := evidence.NewSourcePolicy(h.gateway, origins, h.bindings)
	checked, err := policy.Validate(c.Request.Context(), projectID, evidenceActor, []evidence.Source{source})
	if err != nil {
		sendError(c, err)
		return
	}
	if len(checked.Unusable) > 0 {
		unusable := checked.Unusable[0]
		if unusable.AssetDeny != "" {
			sendError(c, ErrSourceUnavailable)
			return
		}
		source.Status = unusable.Status
	}
	sendOK(c, http.StatusOK, source, false)
}

type dbKnowledgeReader struct{ db *gorm.DB }

func (r dbKnowledgeReader) KnowledgeForOrigin(ctx context.Context, knowledgeID string) (*types.Knowledge, error) {
	var knowledge types.Knowledge
	err := r.db.WithContext(ctx).Where("id = ?", knowledgeID).First(&knowledge).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &knowledge, nil
}

func (r dbKnowledgeReader) CurrentKnowledgeSignal(ctx context.Context, knowledgeID string) (evidence.KnowledgeSignal, bool, error) {
	var knowledge types.Knowledge
	err := r.db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", knowledgeID).First(&knowledge).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return evidence.KnowledgeSignal{}, false, nil
	}
	if err != nil {
		return evidence.KnowledgeSignal{}, false, err
	}
	return evidence.KnowledgeSignal{
		KnowledgeID: knowledge.ID,
		ParseStatus: knowledge.ParseStatus,
		FileHash:    knowledge.FileHash,
		FileSize:    knowledge.FileSize,
		ProcessedAt: valueTime(knowledge.ProcessedAt),
	}, true, nil
}

func (r dbKnowledgeReader) UsesBuiltinConverter(ctx context.Context, knowledgeID string) (bool, error) {
	var knowledge types.Knowledge
	if err := r.db.WithContext(ctx).Where("id = ?", knowledgeID).First(&knowledge).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	if knowledge.KnowledgeBaseID == "" {
		return false, nil
	}
	var kb types.KnowledgeBase
	if err := r.db.WithContext(ctx).Where("id = ? AND tenant_id = ?", knowledge.KnowledgeBaseID, knowledge.TenantID).First(&kb).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	chunking := kb.ChunkingConfig
	overrides, err := knowledge.ProcessOverrides()
	if err != nil {
		return false, nil
	}
	if overrides != nil && len(overrides.ParserEngineRules) > 0 {
		chunking.ParserEngineRules = overrides.ParserEngineRules
	}
	return chunking.ResolveParserEngine(knowledge.FileType) == "", nil
}

func valueTime(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

type retrieveSourcesInput struct {
	Query    string   `json:"query"`
	AssetIDs []string `json:"asset_ids"`
}

func (h *Handler) retrieveSources(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	projectID := c.Param("projectId")
	if err := h.service.Authorize(c.Request.Context(), actor, projectID, "read"); err != nil {
		sendError(c, err)
		return
	}
	var input retrieveSourcesInput
	if !decodeBody(c, &input) {
		return
	}
	input.Query = strings.TrimSpace(input.Query)
	hasAssetID := false
	for _, id := range input.AssetIDs {
		if strings.TrimSpace(id) != "" {
			hasAssetID = true
			break
		}
	}
	if input.Query == "" || !hasAssetID {
		sendError(c, ErrInvalidRequest)
		return
	}
	evidenceActor := evidence.Actor{UserID: actor.UserID, TenantID: strconv.FormatUint(actor.TenantID, 10)}
	resolved, err := h.gateway.ResolveAllowed(c.Request.Context(), projectID, evidenceActor, input.AssetIDs)
	if err != nil {
		sendError(c, err)
		return
	}
	if len(resolved.Denied) > 0 {
		denied := make([]gin.H, 0, len(resolved.Denied))
		for _, item := range resolved.Denied {
			denied = append(denied, gin.H{"asset_id": item.AssetID, "reason": item.Reason})
		}
		sendErrorDetails(c, http.StatusUnprocessableEntity, "asset_not_authorized", "请求中存在未获授权的资料，未开始处理。", gin.H{"denied": denied})
		return
	}
	if h.knowledge == nil {
		sendError(c, ErrSourceUnavailable)
		return
	}
	assetsByKnowledge := make(map[string]evidence.Asset, len(resolved.Allowed))
	assetsByKB := make(map[string][]string)
	for _, asset := range resolved.Allowed {
		assetsByKnowledge[asset.KnowledgeID] = asset
		scope, err := h.bindings.AssetScope(c.Request.Context(), projectID, asset.ID)
		if err != nil {
			sendError(c, err)
			return
		}
		assetsByKB[scope.KnowledgeBaseID] = append(assetsByKB[scope.KnowledgeBaseID], asset.KnowledgeID)
	}
	origins := evidence.NewOriginReader(dbKnowledgeReader{db: h.db})
	resolver := evidence.NewSourceResolver(origins)
	result := make([]evidence.Source, 0)
	kbIDs := make([]string, 0, len(assetsByKB))
	for kbID := range assetsByKB {
		kbIDs = append(kbIDs, kbID)
	}
	sort.Strings(kbIDs)
	for _, kbID := range kbIDs {
		knowledgeIDs := assetsByKB[kbID]
		hits, err := h.knowledge.HybridSearch(c.Request.Context(), kbID, types.SearchParams{QueryText: input.Query, MatchCount: 20, KnowledgeIDs: knowledgeIDs})
		if err != nil {
			sendError(c, err)
			return
		}
		for _, hit := range hits {
			asset, ok := assetsByKnowledge[hit.KnowledgeID]
			if !ok {
				continue
			}
			sources, err := resolver.Resolve(c.Request.Context(), asset, []*types.SearchResult{hit})
			if err != nil {
				sendError(c, err)
				return
			}
			result = append(result, sources...)
		}
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

// accessStatus 答的是「这个项目现在还能不能用」，供撤权之后给出一条恢复路径（§8）。
//
// 三态里 available 只在**一次都不拒绝**时给出：Denied 里混着 not_authorized / not_ready /
// not_found，任一被拒都答 restricted。not_ready（资料还在解析）严格说不是撤权，但它同样
// 意味着此刻读不到——报 restricted 只是让人多看一眼，报 available 会让界面放行一份读不
// 出来的内容。要把 not_authorized 单独挑出来也行，代价是在传输层抄一份 DenyReason 枚举，
// 多一处可漂移的地方，不划算。
//
// unknown 兜所有错误（绑定读不出、网关答不出）。契约 200 已发布这个取值，它就是为
// 「答不出但能如实说答不出」准备的；同路径的 503 留给调用方完全无法作答的情形。
func (h *Handler) accessStatus(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	projectID := c.Param("projectId")
	// 这道门本身就是判据的一部分：findProject 不区分「项目不存在」与「不是成员」，两条都答
	// 404。所以能走到下面的调用者，已经证明了他是该租户的 active 成员、也是本项目成员——
	// can_create_project 直接用这个事实，不必再查一次 tenant_members。
	if err := h.service.Authorize(c.Request.Context(), actor, projectID, "read"); err != nil {
		sendError(c, err)
		return
	}
	contentAccess := "available"
	assets, err := h.bindings.BoundAssets(c.Request.Context(), projectID)
	if err != nil {
		contentAccess = "unknown"
	} else {
		requested := make([]string, 0, len(assets))
		for _, asset := range assets {
			requested = append(requested, asset.ID)
		}
		resolved, err := h.gateway.ResolveAllowed(c.Request.Context(), projectID,
			evidence.Actor{UserID: actor.UserID, TenantID: strconv.FormatUint(actor.TenantID, 10)}, requested)
		switch {
		case err != nil:
			contentAccess = "unknown"
		case len(resolved.Denied) > 0:
			contentAccess = "restricted"
		}
	}
	// 判据落在**资料授权**上，不落在章节引用的「坐标还算不算数」上：这个端点回答的是
	// 「这个项目的资料我现在能不能用」。把 T09 的失效类结论算进来，会让「有资料被撤权」
	// 与「某条引用过期了」在界面上长得一样，而两者的恢复动作并不相同。
	//
	// 必须是空切片而不是 nil：nil 会序列化成 null，而契约里 recovery_actions 是 array。
	actions := []string{}
	if contentAccess == "restricted" {
		// 取值照 §8，本轮只有「恢复原资料权限」与「新建干净项目」两种，不做旧派生正文迁移。
		actions = []string{"restore_source_authorization", "create_clean_project"}
	}
	sendOK(c, http.StatusOK, gin.H{"project_id": projectID, "content_access": contentAccess,
		"recovery_actions": actions, "can_create_project": true}, false)
}
