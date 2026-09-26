package workspace

import (
	"net/http"

	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/gin-gonic/gin"
)

// docxMediaType 是契约给 downloadExport 200 标的内容类型。
const docxMediaType = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

// exportFormatDOCX 是 StartExport.format 在契约里的唯一取值。
const exportFormatDOCX = "docx"

// downloadPathPrefix 是 download_path 的绝对前缀。
//
// 契约把 download_path 发成绝对路径（/api/v1/lingdoc/projects/{projectId}/exports/{exportId}/file），
// 所以这一层必须知道服务端挂在哪儿——它推不出来，那条路径由网关与 Register 的 group
// 决定。Register 把路由挂在 v1.Group("/lingdoc") 下、v1 是 /api/v1，两处必须一致。
// delivery_export_http_test.go 会拿响应里的 download_path **原样**去请求那条路由，
// 所以它漂开的那天用例先红，而不是等前端拿着一个 404 的地址回来问。
const downloadPathPrefix = "/api/v1/lingdoc"

// DeliveryExportHandler 是 T14 的传输层：契约里的 startExport、getExport、
// downloadExport 与 listExports 四个操作。
//
// 为什么另立一个 handler 而不挂到 DeliveryHandler 上：本层要读的交付输入正是由
// workspace.Handler 的交付输入构建器产出的，挂回去就成环。这里只持有**已经装好**的
// 那台服务，不再回头拿东西。T13 那一批的结尾写着「导出的操作属于 T14」，
// 这里就是那句话兑现的地方。
//
// listExports 与另外三个不同：那三个是「对一个已知的快照做点什么」，它回答的是
// 「这个项目导出过什么」。界面上的产物列表只能从服务端来——契约 §7 要求刷新交付页
// 之后仍能按 snapshot_id 显示当前性，而前端自己记账撑不住刷新。
type DeliveryExportHandler struct {
	service *DeliveryExportService
}

// NewDeliveryExportHandler 依赖不齐时返回 nil，让装配处报错而不是交出一台会在
// 调用时空转的 handler。
func NewDeliveryExportHandler(service *DeliveryExportService) *DeliveryExportHandler {
	if service == nil {
		return nil
	}
	return &DeliveryExportHandler{service: service}
}

// RegisterDeliveryExportRoutes 把 T14 挂在已认证的 LingDoc 组下面。服务端前缀
// （契约里的 /api/v1/lingdoc）由调用方那一层的 group 承担。
func RegisterDeliveryExportRoutes(r gin.IRouter, h *DeliveryExportHandler) {
	if h == nil {
		return
	}
	r.POST("/projects/:projectId/releases/:snapshotId/exports", h.Start)
	r.GET("/projects/:projectId/exports", h.List)
	r.GET("/projects/:projectId/exports/:exportId", h.Get)
	// 契约里这条是 /file，不是 /download。
	r.GET("/projects/:projectId/exports/:exportId/file", h.Download)
}

// startExportRequest 是契约里的 StartExport。用指针是因为「没给 format」与
// 「format 是空串」都要与「给了 docx」分开：给个空串默认值就等于把缺字段悄悄放行。
type startExportRequest struct {
	Format *string `json:"format"`
}

// exportArtifactView 是契约里的 ExportArtifact。
//
// 它必须与内部的 delivery.ExportArtifact 分开，不能直接序列化后者：
//
//   - 契约的 additionalProperties:false 里没有 failure_code 与 created_at，
//     多发一个字段就是违约；
//   - file_sha256 与 download_path 是 required + nullable，而内部结构体上的
//     omitempty 会把空值整个删掉——「字段不在」与「字段是 null」在契约里是两回事，
//     前者连必填都没满足。
type exportArtifactView struct {
	ID           string         `json:"id"`
	ProjectID    string         `json:"project_id"`
	SnapshotID   string         `json:"snapshot_id"`
	Status       string         `json:"status"`
	FileSHA256   *string        `json:"file_sha256"`
	DownloadPath *string        `json:"download_path"`
	Error        *exportFailure `json:"error,omitempty"`
}

// exportFailure 是契约里那个 Error 对象在产物上的那一份。
//
// 它与 sendError 发出的错误信封不是同一个东西：失败产物是 **202/200 里的一次成功
// 响应**，说的是「这份文件没做成」，而不是「你这次请求没被受理」。请求本身是成功的
// ——产物已经落库，界面可以按它显示一个恢复状态。
type exportFailure struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

// Start 渲染一份交付文件。契约给这个操作只列了 202。
//
// 渲染与校验是**同步**做完的，所以响应里报的已经是终态（verified 或 failed），
// 而不是 queued：报 queued 是撒谎，客户端会去轮询一个早就结束了的任务。状态码照
// 契约走 202 不换成 200——只看状态码的消费者不该因为「这次跑得快」而收到一个契约里
// 没有的码。
func (h *DeliveryExportHandler) Start(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	// 幂等键在契约里是必填头，收下它就要照它办事：重试换回原来那一份产物，
	// 而不是再渲一份。
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	if !h.readStartExport(c) {
		return
	}
	artifact, replayed, err := h.service.Start(c.Request.Context(), actor.UserID,
		c.Param("projectId"), c.Param("snapshotId"), key)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusAccepted, artifactView(artifact), replayed)
}

// Get 读一份产物的状态。它不返回字节——下载要单独走一次，因为**下载那一刻**的
// 授权必须重查一次。
// List 列出项目导出过的产物，新的在前。契约 §3：列表设了上限就必须显式提示截断。
//
// 交出去的是每一项都过一遍 artifactView 的视图，不是内部结构体——列表里同样要满足
// 「file_sha256 与 download_path 是 required + nullable」这条，不能因为「只是列表」
// 就少走一次翻译。
func (h *DeliveryExportHandler) List(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	artifacts, truncated, err := h.service.List(c.Request.Context(), actor.UserID, c.Param("projectId"))
	if err != nil {
		sendError(c, err)
		return
	}
	items := make([]exportArtifactView, 0, len(artifacts))
	for _, artifact := range artifacts {
		items = append(items, artifactView(artifact))
	}
	sendOK(c, http.StatusOK, gin.H{"items": items, "truncated": truncated}, false)
}

func (h *DeliveryExportHandler) Get(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	artifact, err := h.service.Get(c.Request.Context(), actor.UserID, c.Param("projectId"), c.Param("exportId"))
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, artifactView(artifact), false)
}

// Download 交出文件本身。这是这一组里唯一一条不返回 JSON 的响应：契约把它标成
// docx 的二进制类型、带 Content-Disposition，所以它既不套信封也没有 data 字段。
//
// 文件名取的是**产物自己的** ID（服务把它一并交回来），不是请求路径上那个字符串。
func (h *DeliveryExportHandler) Download(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	artifact, file, err := h.service.Download(c.Request.Context(), actor.UserID, c.Param("projectId"), c.Param("exportId"))
	if err != nil {
		sendError(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="`+artifact.ID+`.docx"`)
	c.Data(http.StatusOK, docxMediaType, file)
}

func (h *DeliveryExportHandler) readStartExport(c *gin.Context) bool {
	var request startExportRequest
	if !decodeBody(c, &request) {
		return false
	}
	if request.Format == nil || *request.Format != exportFormatDOCX {
		sendError(c, ErrInvalidRequest)
		return false
	}
	return true
}

// artifactView 把内部产物翻译成契约发布的形状。
//
// 两个可空字段只在真的有一份可下载文件时才填：契约允许它们是 null，而一个指向
// 不存在文件的下载地址比 null 更糟——界面会照着它给出一个点了就 422 的按钮。
func artifactView(artifact delivery.ExportArtifact) exportArtifactView {
	view := exportArtifactView{
		ID:         artifact.ID,
		ProjectID:  artifact.ProjectID,
		SnapshotID: artifact.SnapshotID,
		Status:     string(artifact.Status),
	}
	switch artifact.Status {
	case delivery.ExportVerified:
		sum := artifact.FileSHA256
		path := exportDownloadPath(artifact.ProjectID, artifact.ID)
		view.FileSHA256, view.DownloadPath = &sum, &path
	case delivery.ExportFailed:
		view.Error = &exportFailure{
			Code:      artifact.FailureCode,
			Message:   exportFailureMessage(artifact.FailureCode),
			Retryable: exportFailureRetryable(artifact.FailureCode),
		}
	}
	return view
}

// exportDownloadPath 拼出下载地址。两个 ID 都由服务端生成（不透明 ID：前缀 + 32 位
// 十六进制），本身就是 URL 安全的，不需要再转义一遍。
func exportDownloadPath(projectID, exportID string) string {
	return downloadPathPrefix + "/projects/" + projectID + "/exports/" + exportID + "/file"
}

func exportFailureMessage(code string) string {
	switch code {
	case delivery.FailureRenderFailed:
		return "文件生成失败。"
	case delivery.FailureEmptyFile:
		return "文件生成结果为空。"
	case delivery.FailureValidationFailed:
		// 这一条要说得比「生成失败」具体：它没失败在生成上，是生成出来的东西
		// 与冻结内容对不上——§7 要求校验通过才提供下载，这里就是那道闸拦下的。
		return "文件内容与冻结版本不一致，已阻止下载。"
	default:
		return "文件生成失败。"
	}
}

// exportFailureRetryable 说「另起一个新动作再试一次值不值得」。契约 §6 说重试是
// 明确的新动作，所以它问的不是「拿旧键再来一次」——那永远换回同一个结果，包括失败。
//
//   - render_failed / empty_file 是渲染这一侧出的岔子（进程被杀、依赖抖动），
//     换个新动作重渲一次往往就成了；
//   - validation_failed 不是。文件是照着同一份冻结输入确定地渲出来的，输入没变就
//     还是同一个错；要改的是输入，不是重试。
func exportFailureRetryable(code string) bool {
	return code == delivery.FailureRenderFailed || code == delivery.FailureEmptyFile
}
