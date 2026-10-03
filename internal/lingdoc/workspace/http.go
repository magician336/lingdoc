package workspace

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
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
	rewrites    SelectedRewriteApplication
}

func NewHandler(deps HandlerDependencies) *Handler {
	return &Handler{
		service: deps.Service, sources: deps.Sources, integration: deps.Integration, rewrites: deps.SelectedRewrites,
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
	routes.Write.POST("/projects/:projectId/owner-transfer", h.transferOwner)
	routes.Read.GET("/projects/:projectId/chapters", h.listChapters)
	routes.Read.GET("/projects/:projectId/chapters/:chapterId/working-copy", h.getWorkingCopy)
	routes.Read.GET("/projects/:projectId/chapters/:chapterId/versions", h.listChapterVersions)
	routes.Read.GET("/projects/:projectId/assets", h.listAssets)
	routes.Write.POST("/projects/:projectId/assets", h.bindAsset)
	routes.Read.POST("/projects/:projectId/retrieval", h.retrieveSources)
	routes.Read.GET("/projects/:projectId/sources/:sourceId", h.getSource)
	routes.Read.GET("/projects/:projectId/sources/:sourceId/context", h.getSourceContext)
	routes.Write.POST("/projects/:projectId/chapters/:chapterId/versions", h.saveChapter)
	routes.Write.PUT("/projects/:projectId/chapters/:chapterId/working-copy", h.saveWorkingCopy)
	routes.Write.POST("/projects/:projectId/chapters/:chapterId/working-copy/commit", h.commitWorkingCopy)
	routes.Write.POST("/projects/:projectId/chapters/:chapterId/working-copy/restore", h.restoreWorkingCopy)
	routes.Read.GET("/projects/:projectId/access-status", h.accessStatus)
	if h.rewrites != nil {
		routes.Read.GET("/projects/:projectId/rewrite-candidates/:candidateId", h.getSelectedRewrite)
		routes.Write.POST("/projects/:projectId/chapters/:chapterId/rewrite-candidates", h.createSelectedRewrite)
		routes.Write.POST("/projects/:projectId/chapters/:chapterId/rewrite-candidates/:candidateId/apply", h.applySelectedRewrite)
	}
}

func caller(c *gin.Context) (Actor, bool) {
	id, userOK := types.UserIDFromContext(c.Request.Context())
	tenant, tenantOK := types.TenantIDFromContext(c.Request.Context())
	return Actor{TenantID: tenant, UserID: id, Role: types.TenantRoleFromContext(c.Request.Context())}, userOK && tenantOK && tenant != 0
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
	case errors.Is(err, ErrRewriteInvalidRequest):
		status, code, message = 400, "invalid_request", "改写请求字段不符合约定。"
	case errors.Is(err, ErrRewriteNotFound):
		status, code, message = 404, "not_found", "候选不存在或不可访问。"
	case errors.Is(err, ErrRewriteConflict), errors.Is(err, ErrRewriteStale):
		status, code, message = 409, "stale_input", "选区或工作副本已变化，请重新读取后再试。"
	case errors.Is(err, ErrRewriteKeyConflict):
		status, code, message = 409, "idempotency_conflict", "同一个操作键对应不同请求。"
	case errors.Is(err, ErrRewriteSourceDenied):
		status, code, message = 403, "source_access_denied", "改写候选的来源已失效或当前不可访问。"
	case errors.Is(err, ErrRewriteUnavailable):
		status, code, message = 503, "dependency_unavailable", "改写模型暂不可用，请稍后重试。"
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
	// T14 的导出与下载。与上面同一件事：这些是 delivery 包里**另一个** ErrInvalidReques