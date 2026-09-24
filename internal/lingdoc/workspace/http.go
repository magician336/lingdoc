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
	authorizer := evidence.NewFixedAuthorizer(bindings, kbReadChecker{shares: kbShares})
	return &Handler{
		service:   NewService(db, ContractDemoTemplate{}),
		bindings:  bindings,
		gateway:   evidence.NewAssetGateway(bindings, authorizer),
		kbShares:  kbShares,
		knowledge: knowledge,
		db:        db,
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
		status, code, message = 403, "source_access_denied", "来源授权尚未接入，不能保存带引用的正文。"
	case errors.Is(err, ErrInvalidState):
		status, code, message = 422, "invalid_state", "当前项目状态或研究条件不满足操作要求。"
	}
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": message, "retryable": code == "request_in_progress"},
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
	if err := h.refreshAssets(c.Request.Context(), projectID, assets); err != nil {
		sendError(c, err)
		return
	}
	assets, err = h.bindings.BoundAssets(c.Request.Context(), projectID)
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
	if !decodeBody(c, &input) || strings.TrimSpace(input.KnowledgeID) == "" {
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
	if err := h.refreshAssets(c.Request.Context(), projectID, []evidence.Asset{asset}); err != nil {
		sendError(c, err)
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

// refreshProjectAssets snapshots current WeKnora signals before a read path
// exposes the durable project binding rows.
func (h *Handler) refreshProjectAssets(ctx context.Context, projectID string) error {
	assets, err := h.bindings.BoundAssets(ctx, projectID)
	if err != nil {
		return err
	}
	return h.refreshAssets(ctx, projectID, assets)
}

func (h *Handler) refreshAssets(ctx context.Context, projectID string, assets []evidence.Asset) error {
	for _, asset := range assets {
		var knowledge types.Knowledge
		err := h.db.WithContext(ctx).
			Where("id = ? AND deleted_at IS NULL", asset.KnowledgeID).
			First(&knowledge).Error
		var signal evidence.KnowledgeSignal
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// A deleted knowledge must stop being ready. ObserveAsset records the
			// loss as a new revision when the previous fingerprint was known.
			signal = evidence.KnowledgeSignal{
				KnowledgeID: asset.KnowledgeID,
				ParseStatus: types.ParseStatusDeleting,
			}
		} else if err != nil {
			return err
		} else {
			signal = evidence.KnowledgeSignal{
				KnowledgeID: knowledge.ID,
				ParseStatus: knowledge.ParseStatus,
				FileHash:    knowledge.FileHash,
				FileSize:    knowledge.FileSize,
				ProcessedAt: valueTime(knowledge.ProcessedAt),
			}
		}
		if _, err := h.bindings.ObserveAsset(ctx, projectID, asset.ID, signal); err != nil {
			return err
		}
	}
	return nil
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
	if err := h.refreshProjectAssets(c.Request.Context(), projectID); err != nil {
		sendError(c, err)
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
