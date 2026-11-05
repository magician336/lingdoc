package workspace

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type DOCXHTTPHandler struct {
	Runtime  *SourceRuntime
	Delivery *DOCXDeliveryService
}

func (h *DOCXHTTPHandler) Register(router gin.IRoutes) {
	router.GET("/api/docx/projects/:project/versions/latest", h.latest)
	router.POST("/api/docx/projects/:project/versions", h.save)
	router.POST("/api/docx/projects/:project/confirmations", h.confirm)
	router.GET("/api/docx/projects/:project/versions/:version/delivery", h.deliveryStatus)
	router.POST("/api/docx/projects/:project/freezes", h.freeze)
	router.POST("/api/docx/projects/:project/exports", h.startExport)
	router.GET("/api/docx/projects/:project/exports/:export", h.downloadExport)
}
func docxHTTPActor(c *gin.Context) (Actor, bool) {
	caller := types.CallerFromContext(c.Request.Context())
	if caller.UserID == "" || caller.TenantID == 0 || !caller.Role.IsValid() {
		return Actor{}, false
	}
	return Actor{TenantID: caller.TenantID, UserID: caller.UserID, Role: caller.Role}, true
}
func docxHTTPError(c *gin.Context, err error) {
	var ruleFailure *docxRuleBlockedError
	if errors.As(err, &ruleFailure) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "RULE_CHECK_FAILED", "message": "当前保存版本未通过交付检查。", "details": ruleFailure.Failures,
		})
		return
	}
	status, code := http.StatusInternalServerError, "INTERNAL_ERROR"
	message := err.Error()
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		status, code = http.StatusNotFound, "NOT_FOUND"
	case errors.Is(err, delivery.ErrSnapshotNotFound), errors.Is(err, delivery.ErrExportNotFound):
		status, code = http.StatusNotFound, "NOT_FOUND"
	case errors.Is(err, delivery.ErrIdempotencyConflict):
		status, code = http.StatusConflict, "IDEMPOTENCY_CONFLICT"
	case errors.Is(err, delivery.ErrExportStaleInput), errors.Is(err, delivery.ErrSnapshotStaleInput):
		status, code = http.StatusConflict, "STALE_INPUT"
	case errors.Is(err, delivery.ErrExportPreflightBlocked):
		status, code = http.StatusUnprocessableEntity, "PREFLIGHT_BLOCKED"
	case errors.Is(err, delivery.ErrExportUnavailable):
		status, code = http.StatusUnprocessableEntity, "EXPORT_UNAVAILABLE"
	case errors.Is(err, delivery.ErrInvalidRequest):
		status, code = http.StatusBadRequest, "INVALID_REQUEST"
	case strings.Contains(message, "VERSION_CHANGED"), strings.Contains(message, "PROJECTION_VERSION_MISMATCH"):
		status, code = 409, "VERSION_CHANGED"
	case strings.Contains(message, "IDEMPOTENCY_CONFLICT"):
		status, code = 409, "IDEMPOTENCY_CONFLICT"
	case strings.Contains(message, "STRUCTURE_INVALID"):
		status, code = 422, "STRUCTURE_INVALID"
	case strings.Contains(message, "SOURCE_UNAVAILABLE"):
		status, code = 422, "SOURCE_INVALID"
	case strings.Contains(message, "DOCX_CITATION_INVALID_REQUEST"):
		status, code = 400, "INVALID_REQUEST"
	case strings.Contains(message, "DOCX_CITATION_REQUIRED"):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "CITATION_INVALID", "message": "正文中有尚未关联证据卡的来源。请从资料面板选用已确认且适用于本章的证据卡后重试。"})
		return
	case strings.Contains(message, "DOCX_CITATION_INVALID"):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "CITATION_INVALID", "message": "引用所用证据卡已变化、不可读取或不适用于当前章节。请重新核对证据卡；本次未登记新的保存版本。"})
		return
	case strings.Contains(message, "RULE_BLOCKED"):
		status, code = 422, "RULE_CHECK_FAILED"
	case strings.Contains(message, "permission"), strings.Contains(message, "not_found"), strings.Contains(message, "forbidden"), strings.Contains(message, "removed"):
		status, code = 403, "PERMISSION_DENIED"
	case strings.Contains(message, "INVALID_REQUEST"):
		status, code = 400, "INVALID_REQUEST"
	}
	c.JSON(status, gin.H{"error": code})
}
func (h *DOCXHTTPHandler) latest(c *gin.Context) {
	actor, ok := docxHTTPActor(c)
	if !ok {
		c.JSON(401, gin.H{"error": "UNAUTHENTICATED"})
		return
	}
	ctx := c.Request.Context()
	projectID := c.Param("project")
	runtime := h.Runtime.CloneWithDatabase(h.Runtime.db)
	projects := core.NewServiceWithSources(core.NewGORMRepository(h.Runtime.db), h.Runtime.templates, runtime.WorkspaceSourcePolicy())
	runtime.ConnectProjects(projects)
	runtime.EnableFullWorkflowPolicy()
	if err := projects.Authorize(ctx, actor, projectID, "read"); err != nil {
		docxHTTPError(c, err)
		return
	}
	version, err := NewDOCXVersionStore(h.Runtime.db).Latest(ctx, projectID)
	if err != nil {
		docxHTTPError(c, err)
		return
	}
	c.JSON(200, version)
}
func (h *DOCXHTTPHandler) save(c *gin.Context) {
	actor, ok := docxHTTPActor(c)
	if !ok {
		c.JSON(401, gin.H{"error": "UNAUTHENTICATED"})
		return
	}
	var input struct {
		ExpectedDiskVersion int64             `json:"expectedDiskVersion"`
		Data                string            `json:"data"`
		Citations           []DOCXCitationRef `json:"citations,omitempty"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		docxHTTPError(c, errors.New("DOCX_SAVE_INVALID_REQUEST"))
		return
	}
	data, err := base64.StdEncoding.DecodeString(input.Data)
	if err != nil {
		docxHTTPError(c, errors.New("DOCX_SAVE_INVALID_REQUEST"))
		return
	}
	version, replayed, err := h.Runtime.SaveDOCXVersion(c.Request.Context(), actor, c.Param("project"), c.GetHeader("Idempotency-Key"), input.ExpectedDiskVersion, data, input.Citations)
	if err != nil {
		docxHTTPError(c, err)
		return
	}
	c.JSON(200, gin.H{"version": version, "replayed": replayed})
}
func (h *DOCXHTTPHandler) confirm(c *gin.Context) {
	actor, ok := docxHTTPActor(c)
	if !ok {
		c.JSON(401, gin.H{"error": "UNAUTHENTICATED"})
		return
	}
	var input DOCXChapterConfirmationRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		docxHTTPError(c, errors.New("DOCX_CONFIRM_INVALID_REQUEST"))
		return
	}
	input.ProjectID = c.Param("project")
	input.IdempotencyKey = c.GetHeader("Idempotency-Key")
	confirmation, replayed, err := h.Runtime.ConfirmSavedDOCXChapter(c.Request.Context(), actor, input)
	if err != nil {
		docxHTTPError(c, err)
		return
	}
	c.JSON(200, gin.H{"confirmation": confirmation, "replayed": replayed})
}

func (h *DOCXHTTPHandler) deliveryStatus(c *gin.Context) {
	actor, ok := docxHTTPActor(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHENTICATED"})
		return
	}
	if h.Delivery == nil {
		docxHTTPError(c, errors.New("DOCX_DELIVERY_DEPENDENCY_MISSING"))
		return
	}
	status, err := h.Delivery.Status(c.Request.Context(), actor, c.Param("project"), c.Param("version"))
	if err != nil {
		docxHTTPError(c, err)
		return
	}
	c.JSON(http.StatusOK, status)
}

func (h *DOCXHTTPHandler) freeze(c *gin.Context) {
	actor, ok := docxHTTPActor(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHENTICATED"})
		return
	}
	if h.Delivery == nil {
		docxHTTPError(c, errors.New("DOCX_DELIVERY_DEPENDENCY_MISSING"))
		return
	}
	var input struct {
		SavedVersionID string `json:"savedVersionId"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		docxHTTPError(c, errors.New("DOCX_FREEZE_INVALID_REQUEST"))
		return
	}
	result, err := h.Delivery.FreezeSavedDOCXVersion(c.Request.Context(), actor, c.Param("project"), input.SavedVersionID, c.GetHeader("Idempotency-Key"))
	if err != nil {
		docxHTTPError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *DOCXHTTPHandler) startExport(c *gin.Context) {
	actor, ok := docxHTTPActor(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHENTICATED"})
		return
	}
	if h.Delivery == nil {
		docxHTTPError(c, errors.New("DOCX_DELIVERY_DEPENDENCY_MISSING"))
		return
	}
	var input struct {
		SnapshotID string `json:"snapshotId"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		docxHTTPError(c, errors.New("DOCX_EXPORT_INVALID_REQUEST"))
		return
	}
	artifact, replayed, err := h.Delivery.StartExport(c.Request.Context(), actor, c.Param("project"), input.SnapshotID, c.GetHeader("Idempotency-Key"))
	if err != nil {
		docxHTTPError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"export": artifact, "replayed": replayed})
}

func (h *DOCXHTTPHandler) downloadExport(c *gin.Context) {
	actor, ok := docxHTTPActor(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHENTICATED"})
		return
	}
	if h.Delivery == nil {
		docxHTTPError(c, errors.New("DOCX_DELIVERY_DEPENDENCY_MISSING"))
		return
	}
	artifact, data, err := h.Delivery.Download(c.Request.Context(), actor, c.Param("project"), c.Param("export"))
	if err != nil {
		docxHTTPError(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="lingdoc-`+artifact.ID+`.docx"`)
	c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", data)
}
