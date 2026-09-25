package workspace

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// DeliveryHandler 是 T13 的传输层：契约里的 checkCurrent、prepareRelease、
// getRelease 三个操作。
//
// 为什么另立一个 handler 而不是把这三个方法挂到 workspace.Handler 上：本层要读的
// 交付输入正是由 workspace.Handler 的交付输入构建器产出的，挂回去就成环——服务依赖
// handler，handler 又依赖服务。这里只持有**已经装好**的那台服务，不再回头拿东西。
//
// 契约把这三个入口标为「容量有余才启用的独立 HTTP 入口，核心流程不依赖本接口」，
// 同一句话里也写了领域函数必须实现。领域函数在 DeliveryReleaseService 里；这里做的是
// 把身份、幂等键、版本号这些线上形状翻译成它的入参，再把它的结论按契约的信封发出去。
// 导出的三个操作属于 T14（真实导出与下载），它还需要 T05 那台渲染器的生产实现，
// 不在这里假装可用。
type DeliveryHandler struct {
	service *DeliveryReleaseService
}

// NewDeliveryHandler 依赖不齐时返回 nil，让装配处报错而不是交出一台会在调用时
// 空转的 handler。
func NewDeliveryHandler(service *DeliveryReleaseService) *DeliveryHandler {
	if service == nil {
		return nil
	}
	return &DeliveryHandler{service: service}
}

// RegisterDeliveryRoutes 把 T13 挂在已认证的 LingDoc 组下面。服务端前缀
// （契约里的 /api/v1/lingdoc）由调用方那一层的 group 承担。
func RegisterDeliveryRoutes(r gin.IRouter, h *DeliveryHandler) {
	if h == nil {
		return
	}
	r.POST("/projects/:projectId/checks", h.Check)
	r.POST("/projects/:projectId/releases", h.Prepare)
	r.GET("/projects/:projectId/releases/:snapshotId", h.Get)
}

// readVersion 是契约里 /checks 与 /releases 的请求体，只有 expected_project_version。
// 用指针是因为「没给」与「给了 0」是两回事：契约把该字段标成必填，缺字段要报
// invalid_request，而不是当成「我以为版本是 0」。
type readVersion struct {
	ExpectedProjectVersion *int64 `json:"expected_project_version"`
}

// Check 回答「此刻冻结会得到什么结论」，不落快照，所以它不需要幂等键——
// 契约也没给这个操作标 Idempotency-Key。
func (h *DeliveryHandler) Check(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	request, ok := h.readVersion(c)
	if !ok {
		return
	}
	result, err := h.service.Check(c.Request.Context(), actor.UserID, c.Param("projectId"), *request.ExpectedProjectVersion)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, result, false)
}

// Prepare 冻结一份快照。
func (h *DeliveryHandler) Prepare(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	// 幂等键在契约里是必填头。收下它就要照它办事：重试换回原来那一份快照，
	// 而不是再冻一份——交付历史里一条用户动作只该有一条。
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	request, ok := h.readVersion(c)
	if !ok {
		return
	}
	snapshot, replayed, err := h.service.Prepare(c.Request.Context(), actor.UserID,
		c.Param("projectId"), key, *request.ExpectedProjectVersion)
	if err != nil {
		sendError(c, err)
		return
	}
	// 重放沿用 201：契约给 /releases 只列了 201，重放与否由 meta.replayed 说明，
	// 换个 200 会让只看状态码的消费者以为这是两种不同的成功。
	sendOK(c, http.StatusCreated, snapshot, replayed)
}

// Get 取一份已冻结的快照。is_current 由服务按**此刻**的工作区重算：冻结的内容
// 与摘要不再变，但「它还对不对得上现在的工作区」从写下的那一刻就在过期。
func (h *DeliveryHandler) Get(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	snapshot, err := h.service.Get(c.Request.Context(), actor.UserID, c.Param("projectId"), c.Param("snapshotId"))
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, snapshot, false)
}

func (h *DeliveryHandler) readVersion(c *gin.Context) (readVersion, bool) {
	var request readVersion
	if !decodeBody(c, &request) {
		return readVersion{}, false
	}
	if request.ExpectedProjectVersion == nil || *request.ExpectedProjectVersion < 0 {
		sendError(c, ErrInvalidRequest)
		return readVersion{}, false
	}
	return request, true
}
