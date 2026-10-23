package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/writingaction"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	full *FullWorkflowDependencies
	// Optional G5/G7 extensions follow the transport direction as well; a
	// read dependency must never become the implicit review-anchor writer.
	machineReads       MachineReadApplication
	reviewAnchorsRead  ReviewAnchorReadApplication
	reviewAnchorsWrite ReviewAnchorWriteApplication
	projects           ProjectApplication
	members            MembershipApplication
	chapters           ChapterApplication
	templatesApp       TemplateApplication
	changeSets         ChangeSetApplication
	sources            SourceApplicationService
	integration        WorkspaceIntegration
	rewrites           SelectedRewriteApplication
	templates          delivery.TemplateReader
}

// NewHandler composes the workspace transport.
//
// It fails when a required port is missing. A required port backs a family of
// routes that cannot answer without it, so a handler assembled without one
// would answer a request with a nil dereference. Failing here turns that into
// a startup error that names the missing port.
//
// The optional capabilities are the ones the caller asked for by name: see the
// With... methods on HandlerDependencies. A capability that was not asked for
// is not registered, and that is a decision the caller made by not writing the
// line.
func NewHandler(deps HandlerDependencies) (*Handler, error) {
	templates := deps.Templates
	if templates == nil {
		templates = delivery.NewFixedTemplateReader()
	}

	ports, err := resolvePorts(deps)
	if err != nil {
		return nil, err
	}

	return &Handler{
		full: deps.FullWorkflow, machineReads: deps.machineReads,
		reviewAnchorsRead: deps.anchorsRead, reviewAnchorsWrite: deps.anchorsWrite,
		projects: ports.projects, members: ports.members, chapters: ports.chapters,
		templatesApp: ports.templatesApp, changeSets: ports.changeSets,
		sources: deps.Sources, integration: deps.Integration,
		rewrites: deps.selectedRewrites, templates: templates,
	}, nil
}

// requiredPorts are the ports the registered routes cannot work without.
//
// Each one is named here rather than inferred from the handler's fields, so
// the list of what a workspace needs is written down once.
type requiredPorts struct {
	projects     ProjectApplication
	members      MembershipApplication
	chapters     ChapterApplication
	templatesApp TemplateApplication
	changeSets   ChangeSetApplication
	sources      SourceApplicationService
}

// resolvePorts finds the port each route family needs.
//
// The ports are named, not inferred: the composition root hands over the port
// of every context, and this function only checks that none of them is
// missing. It used to infer them from one broad application service or from
// a split read/write pair, which meant the same capability could be reached
// through several doors (Spec #47 决策 37).
func resolvePorts(deps HandlerDependencies) (requiredPorts, error) {
	ports := requiredPorts{
		projects:     deps.Projects,
		members:      deps.Members,
		chapters:     deps.Chapters,
		templatesApp: deps.TemplateApp,
		changeSets:   deps.ChangeSets,
		sources:      deps.Sources,
	}

	// Every port below backs a family of routes that cannot answer without it.
	// A handler assembled without one would answer a request with a nil
	// dereference, so the assembly fails here instead: the missing port is
	// named, and the service does not start.
	for _, required := range []struct {
		name string
		held bool
	}{
		{"projects", ports.projects != nil},
		{"members", ports.members != nil},
		{"chapters", ports.chapters != nil},
		{"templates", ports.templatesApp != nil},
		{"change sets", ports.changeSets != nil},
		{"sources", ports.sources != nil},
	} {
		if !required.held {
			return requiredPorts{}, fmt.Errorf("lingdoc workspace handler: the %s port is required", required.name)
		}
	}
	return ports, nil
}

// Register registers the workspace's routes.
//
// The core families are always registered: the ports behind them are
// required, so a handler that exists has them. The optional families are
// registered only when the caller asked for the capability - see the With...
// methods on HandlerDependencies. An endpoint's absence is then a decision
// the caller made, not an accident of which field happened to be filled in.
func (h *Handler) Register(routes RouteGroups) {
	routes.Read.GET("/templates/:templateId", h.getTemplate)
	routes.Read.GET("/projects", h.listProjects)
	routes.Write.POST("/projects", h.createProject)
	routes.Read.GET("/projects/:projectId", h.getProject)
	routes.Write.PUT("/projects/:projectId/spec", h.saveSpec)
	routes.Write.POST("/projects/:projectId/activate", h.activateProject)
	routes.Read.GET("/projects/:projectId/draft-candidates", h.listDraftCandidates)
	routes.Write.POST("/projects/:projectId/draft-candidates", h.createDraftCandidate)
	routes.Read.GET("/projects/:projectId/change-sets", h.listChangeSets)
	routes.Read.GET("/projects/:projectId/change-sets/:changeSetId", h.getChangeSet)
	routes.Write.POST("/projects/:projectId/change-sets", h.createChangeSet)
	routes.Write.POST("/projects/:projectId/change-sets/:changeSetId/reassess", h.reassessChangeSet)
	routes.Write.POST("/projects/:projectId/change-sets/:changeSetId/apply", h.applyChangeSet)
	routes.Write.POST("/projects/:projectId/change-sets/:changeSetId/reject", h.rejectChangeSet)
	routes.Read.GET("/projects/:projectId/audit", h.listAuditEvents)
	routes.Read.GET("/projects/:projectId/activation-diff", h.activationDiff)
	routes.Read.GET("/projects/:projectId/template-migration/preview", h.previewTemplateMigration)
	routes.Write.POST("/projects/:projectId/template-migration/preview", h.previewTemplateMigrationPost)
	routes.Write.POST("/projects/:projectId/template-migration", h.changeTemplate)
	routes.Read.GET("/projects/:projectId/template-copies/:version", h.getTemplateCopy)
	routes.Write.POST("/projects/:projectId/template-copy/preview", h.previewTemplateCopyEdit)
	routes.Write.PUT("/projects/:projectId/template-copy", h.saveTemplateCopy)
	routes.Write.PUT("/projects/:projectId/template-copy/rule-set", h.bindTemplateCopyRuleSet)
	routes.Write.POST("/projects/:projectId/discard", h.discardProject)
	routes.Write.POST("/projects/:projectId/restore", h.restoreProject)
	routes.Write.POST("/projects/:projectId/archive", h.archiveProject)
	routes.Write.POST("/projects/:projectId/owner-transfer", h.requestOwnerTransfer)
	routes.Write.POST("/projects/:projectId/owner-transfer/:transferId/accept", h.acceptOwnerTransfer)
	routes.Write.PUT("/projects/:projectId/members", h.saveMembers)
	routes.Read.GET("/projects/:projectId/chapters", h.listChapters)
	routes.Read.GET("/projects/:projectId/chapters/:chapterId/working-copy", h.getWorkingCopy)
	if h.reviewAnchorsRead != nil {
		routes.Read.GET("/projects/:projectId/chapters/:chapterId/confirmations/current", h.getCurrentConfirmation)
	}
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
	// The writing action table is static: it belongs to the vocabulary package,
	// so the panel reads it instead of keeping a second copy of the actions.
	routes.Read.GET("/projects/:projectId/writing-actions", h.listWritingActions)
	// The review anchor and the rewrite endpoints exist because the caller
	// asked for them. A capability that was not asked for has no route, and
	// that is the caller's decision rather than an accident of which field
	// happened to be filled in.
	if h.reviewAnchorsWrite != nil {
		routes.Write.PUT("/projects/:projectId/chapters/:chapterId/working-copy/review-items/:itemId/anchor", h.setReviewAnchor)
	}
	if h.rewrites != nil {
		routes.Read.GET("/projects/:projectId/rewrite-candidates/:candidateId", h.getSelectedRewrite)
		routes.Write.POST("/projects/:projectId/chapters/:chapterId/rewrite-candidates", h.createSelectedRewrite)
		routes.Write.POST("/projects/:projectId/chapters/:chapterId/rewrite-candidates/:candidateId/apply", h.applySelectedRewrite)
		// The review endpoints exist when the rewrite capability also speaks
		// the review path: a language review produces a report, not a
		// candidate, and it is applied through its own entry point.
		if _, ok := h.rewrites.(ReviewReportApplication); ok {
			routes.Write.POST("/projects/:projectId/chapters/:chapterId/review-reports", h.createReviewReport)
			routes.Read.GET("/projects/:projectId/review-reports/:reportId", h.getReviewReport)
			routes.Write.POST("/projects/:projectId/review-reports/:reportId/reject", h.rejectReviewReport)
			routes.Write.POST("/projects/:projectId/chapters/:chapterId/review-reports/:reportId/apply", h.applyReviewReport)
		}
	}
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

// sendError renders a business failure.
//
// The outward shape of a failure is declared by the error itself, at the place
// where that fact is known. This mapper therefore holds no table of business
// errors: it renders what the error declares, and falls back to a generic
// internal error for the failures that declare nothing.
//
// A table like the one this replaces had to grow a branch per business failure,
// and a branch that was missed did not fail loudly — it silently answered an
// internal error for a failure the caller could have been told about.
func sendError(c *gin.Context, err error) {
	if status, code, message, details, ok := sourceErrorStatus(err); ok {
		sendErrorDetails(c, status, code, message, details)
		return
	}
	if form, ok := clienterror.FormOf(err); ok {
		if form.RetryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(form.RetryAfter))
		}
		failure := gin.H{"code": form.Code, "message": form.Message, "retryable": form.Retryable}
		// A failure that can name its reason carries it in the optional
		// details. The failures that have nothing extra to say keep the shape
		// they always had.
		if form.Details != nil {
			failure["details"] = form.Details
		}
		c.JSON(form.Status, gin.H{"error": failure, "request_id": requestID(c)})
		return
	}
	// The fallback is a 5xx, so it is retryable (DR-47-03: 4xx is not, 5xx is).
	// Every surface answers an unknown failure the same way, and the same
	// semantic must not answer differently depending on the route.
	c.JSON(500, gin.H{"error": gin.H{"code": "internal_error", "message": "操作失败，请稍后再试。",
		"retryable": true}, "request_id": requestID(c)})
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
	allDenied := denied.RequestedCount == 0 || len(denied.Denied) >= denied.RequestedCount
	allSame := len(denied.Denied) > 0
	firstReason := evidence.DenyReason("")
	for i, item := range denied.Denied {
		if i == 0 {
			firstReason = item.Reason
		} else if item.Reason != firstReason {
			allSame = false
		}
	}
	details := map[string]any{"denied": items}
	if !allSame {
		return 422, "asset_access_denied", "请求中存在未获授权、不可用或不存在的资料，未开始处理。", details, true
	}
	switch firstReason {
	case evidence.DenyNotFound:
		if !allDenied {
			return 422, "asset_access_denied", "请求中存在未获授权、不可用或不存在的资料，未开始处理。", details, true
		}
		return 404, "asset_not_found", "资料不存在或不属于当前项目。", details, true
	case evidence.DenyNotAuthorized:
		if allDenied {
			return 403, "asset_not_authorized", "当前成员无权读取所请求的资料。", details, true
		}
		// A mixed request remains an all-or-nothing retrieval rejection. Preserve
		// the established 422 code while still identifying each denied asset.
		return 422, "asset_not_authorized", "当前成员无权读取所请求的资料。", details, true
	case evidence.DenyNotReady:
		return 422, "asset_not_ready", "资料尚未就绪，未开始处理。", details, true
	default:
		return 422, "asset_access_denied", "请求中的资料不可用，未开始处理。", details, true
	}
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
	if !operationcore.ValidKey(key) {
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
	items, truncated, err := h.projects.ListProjects(c.Request.Context(), actor)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, 200, gin.H{"items": items, "truncated": truncated}, false)
}

func (h *Handler) getTemplate(c *gin.Context) {
	_, ok := identity(c)
	if !ok {
		return
	}
	template, err := h.templates.Get(c.Param("templateId"), delivery.DemoTemplateVersion)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, template, false)
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
	result, err := h.projects.CreateProjectResult(c.Request.Context(), actor, key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusCreated, result.Project, result.Replayed)
}

func (h *Handler) getProject(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	project, err := h.projects.GetProject(c.Request.Context(), actor, c.Param("projectId"))
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
	result, err := h.projects.SaveSpecResult(c.Request.Context(), actor, c.Param("projectId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	// SaveSpec's established wire contract is 200; status selection stays
	// in this transport adapter rather than crossing the application seam.
	sendOK(c, http.StatusOK, result.Project, result.Replayed)
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
	result, err := h.projects.ActivateProjectResult(c.Request.Context(), actor, c.Param("projectId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, result.Project, result.Replayed)
}

func (h *Handler) listDraftCandidates(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	data, err := h.projects.ListDraftCandidates(c.Request.Context(), actor, c.Param("projectId"))
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
	result, err := h.projects.CreateDraftCandidateResult(c.Request.Context(), actor, c.Param("projectId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusCreated, result.Candidate, result.Replayed)
}

func (h *Handler) listAuditEvents(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	data, err := h.projects.ListAuditEvents(c.Request.Context(), actor, c.Param("projectId"))
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
	diff, err := h.projects.ActivationDiff(c.Request.Context(), actor, c.Param("projectId"), since)
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
	data, err := h.templatesApp.PreviewTemplateMigration(c.Request.Context(), actor, c.Param("projectId"), input)
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
	data, err := h.templatesApp.PreviewTemplateMigration(c.Request.Context(), actor, c.Param("projectId"), input)
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
	result, err := h.templatesApp.ChangeTemplateResult(c.Request.Context(), actor, c.Param("projectId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, result.Project, result.Replayed)
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
	copy, err := h.templatesApp.GetTemplateCopy(c.Request.Context(), actor, c.Param("projectId"), version)
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
	preview, err := h.templatesApp.PreviewTemplateCopyEdit(c.Request.Context(), actor, c.Param("projectId"), input)
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
	result, err := h.templatesApp.SaveTemplateCopyResult(c.Request.Context(), actor, c.Param("projectId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, result.Project, result.Replayed)
}

func (h *Handler) bindTemplateCopyRuleSet(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input BindTemplateCopyRuleSetInput
	if !decodeBody(c, &input) {
		return
	}
	result, err := h.templatesApp.BindTemplateCopyRuleSetResult(c.Request.Context(), actor, c.Param("projectId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, result.Project, result.Replayed)
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
	result, err := h.projects.DiscardProjectResult(c.Request.Context(), actor, c.Param("projectId"), key, input.ExpectedProjectVersion)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, result.Project, result.Replayed)
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
	result, err := h.projects.RestoreProjectResult(c.Request.Context(), actor, c.Param("projectId"), key, input.ExpectedProjectVersion)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, result.Project, result.Replayed)
}

// archiveProject ends a project. The project version the caller read travels
// with the request, and the idempotency key makes a retried request one
// action.
func (h *Handler) archiveProject(c *gin.Context) {
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
	result, err := h.projects.ArchiveProjectResult(c.Request.Context(), actor, c.Param("projectId"), key, input.ExpectedProjectVersion)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, result.Project, result.Replayed)
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
	if input.NewOwnerUserID != "" {
		result, err := h.members.TransferOwnerResult(c.Request.Context(), actor, c.Param("projectId"), key, TransferOwnerInput{ExpectedProjectVersion: input.ExpectedProjectVersion, NewOwnerUserID: input.NewOwnerUserID})
		if err != nil {
			sendError(c, err)
			return
		}
		sendOK(c, http.StatusOK, result.Project, result.Replayed)
		return
	} else {
		result, err := h.members.RequestOwnerTransferResult(c.Request.Context(), actor, c.Param("projectId"), key, OwnerTransferInput{ExpectedProjectVersion: input.ExpectedProjectVersion, ToUserID: input.ToUserID})
		if err != nil {
			sendError(c, err)
			return
		}
		sendOK(c, http.StatusCreated, result.Transfer, result.Replayed)
		return
	}
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
	result, err := h.members.AcceptOwnerTransferResult(c.Request.Context(), actor, c.Param("projectId"), c.Param("transferId"), key)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, result.Transfer, result.Replayed)
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
	result, err := h.members.SaveMembersResult(c.Request.Context(), actor, c.Param("projectId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, result.Project, result.Replayed)
}

func (h *Handler) listChapters(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	data, err := h.chapters.ListChapters(c.Request.Context(), actor, c.Param("projectId"))
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
	result, err := h.chapters.SaveChapterResult(c.Request.Context(), actor,
		c.Param("projectId"), c.Param("chapterId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusCreated, result.Chapter, result.Replayed)
}

func (h *Handler) getWorkingCopy(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	workingCopy, err := h.chapters.GetWorkingCopy(c.Request.Context(), actor, c.Param("projectId"), c.Param("chapterId"))
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, workingCopy, false)
}

// listWritingActions returns the writing actions available for a target scope.
//
// The action table belongs to the vocabulary package: the panel reads it from
// here instead of keeping a second copy of the actions. The scope is optional
// and defaults to the selection scope; an unknown scope is refused rather
// than silently answered with an empty table.
func (h *Handler) listWritingActions(c *gin.Context) {
	if _, ok := identity(c); !ok {
		return
	}
	scope := writingaction.Scope(c.Query("scope"))
	if scope == "" {
		scope = writingaction.ScopeSection
	}
	if scope != writingaction.ScopeSection && scope != writingaction.ScopeChapter {
		sendError(c, writingaction.ErrScopeNotAllowed)
		return
	}
	sendOK(c, http.StatusOK, writingaction.Describe(scope), false)
}

func (h *Handler) createSelectedRewrite(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input SelectedRewriteRequest
	if !decodeBody(c, &input) {
		return
	}
	candidate, err := h.rewrites.Create(c.Request.Context(), actor, c.Param("projectId"), c.Param("chapterId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusCreated, candidate, false)
}

func (h *Handler) getSelectedRewrite(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	candidate, err := h.rewrites.Get(c.Request.Context(), actor, c.Param("projectId"), c.Param("candidateId"))
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, candidate, false)
}

func (h *Handler) applySelectedRewrite(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input ApplySelectedRewriteInput
	if !decodeBody(c, &input) {
		return
	}
	workingCopy, replayed, err := h.rewrites.Apply(c.Request.Context(), actor, c.Param("projectId"), c.Param("chapterId"), c.Param("candidateId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, workingCopy, replayed)
}

// reviewReporter 取出评审报告的应用实现。
//
// 评审能力在不在由装配决定：装配里没有评审实现，这四个端点根本不注册。断言
// 失败按「评审暂不可用」答——调用方能据此重试，而不是拿到一个说不清原因的 500。
//
// 四个评审端点共用它：同一份断言、同一句错误，不各写各的。
func (h *Handler) reviewReporter(c *gin.Context) (ReviewReportApplication, bool) {
	reviewer, ok := h.rewrites.(ReviewReportApplication)
	if !ok {
		sendError(c, ErrRewriteUnavailable)
		return nil, false
	}
	return reviewer, true
}

// createReviewReport 生成一份语言评审报告。
//
// 报告不是候选：它不进候选表，也不进采纳那条路。用户预览之后显式确认，才由
// applyReviewReport 把它写进工作副本。
func (h *Handler) createReviewReport(c *gin.Context) {
	reviewer, ok := h.reviewReporter(c)
	if !ok {
		return
	}
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input ReviewReportRequest
	if !decodeBody(c, &input) {
		return
	}
	report, err := reviewer.CreateReviewReport(c.Request.Context(), actor, c.Param("projectId"), c.Param("chapterId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusCreated, report, false)
}

// getReviewReport 读回一份评审报告（预览）。
func (h *Handler) getReviewReport(c *gin.Context) {
	reviewer, ok := h.reviewReporter(c)
	if !ok {
		return
	}
	actor, ok := identity(c)
	if !ok {
		return
	}
	report, err := reviewer.GetReviewReport(c.Request.Context(), actor, c.Param("projectId"), c.Param("reportId"))
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, report, false)
}

// rejectReviewReport 拒绝一份评审报告：工作副本一个字符都不动。
func (h *Handler) rejectReviewReport(c *gin.Context) {
	reviewer, ok := h.reviewReporter(c)
	if !ok {
		return
	}
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	report, err := reviewer.RejectReviewReport(c.Request.Context(), actor, c.Param("projectId"), c.Param("reportId"), key)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, report, false)
}

// applyReviewReport 把一份评审报告显式应用到工作副本。
func (h *Handler) applyReviewReport(c *gin.Context) {
	reviewer, ok := h.reviewReporter(c)
	if !ok {
		return
	}
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input ApplyReviewInput
	if !decodeBody(c, &input) {
		return
	}
	workingCopy, replayed, err := reviewer.ApplyReviewReport(c.Request.Context(), actor, c.Param("projectId"),
		c.Param("chapterId"), c.Param("reportId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, workingCopy, replayed)
}

func (h *Handler) saveWorkingCopy(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input SaveWorkingCopyInput
	if !decodeBody(c, &input) {
		return
	}
	result, err := h.chapters.SaveWorkingCopyResult(c.Request.Context(), actor, c.Param("projectId"), c.Param("chapterId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, result.WorkingCopy, result.Replayed)
}

func (h *Handler) commitWorkingCopy(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input CommitWorkingCopyInput
	if !decodeBody(c, &input) {
		return
	}
	result, err := h.chapters.CommitWorkingCopyResult(c.Request.Context(), actor, c.Param("projectId"), c.Param("chapterId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusCreated, result.Version, result.Replayed)
}

func (h *Handler) listChapterVersions(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	versions, err := h.chapters.ListChapterVersions(c.Request.Context(), actor, c.Param("projectId"), c.Param("chapterId"))
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, gin.H{"items": versions}, false)
}

func (h *Handler) restoreWorkingCopy(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	var input RestoreWorkingCopyInput
	if !decodeBody(c, &input) {
		return
	}
	result, err := h.chapters.RestoreWorkingCopyResult(c.Request.Context(), actor, c.Param("projectId"), c.Param("chapterId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, result.WorkingCopy, result.Replayed)
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
	data, err := h.changeSets.ListChangeSets(c.Request.Context(), actor, c.Param("projectId"))
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
	data, err := h.changeSets.GetChangeSet(c.Request.Context(), actor, c.Param("projectId"), c.Param("changeSetId"))
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
	result, err := h.changeSets.CreateChangeSetResult(c.Request.Context(), actor, c.Param("projectId"), key, input)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusCreated, result.ChangeSet, result.Replayed)
}

func (h *Handler) reassessChangeSet(c *gin.Context) {
	actor, ok := identity(c)
	if !ok {
		return
	}
	key, ok := idempotencyKey(c)
	if !ok {
		return
	}
	// Reassessment has no client payload. Read at most one byte so even
	// whitespace or a misleading Content-Length cannot reach the service.
	if c.Request.Body != nil {
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1))
		if err != nil || len(body) != 0 {
			sendError(c, ErrInvalidRequest)
			return
		}
	}
	result, err := h.changeSets.ReassessChangeSetResult(c.Request.Context(), actor, c.Param("projectId"), c.Param("changeSetId"), key)
	if err != nil {
		sendError(c, err)
		return
	}
	// Reassessment creates a new assessment; retain the existing 201 contract,
	// including the persisted response status on idempotent replay.
	sendOK(c, http.StatusCreated, result.ChangeSet, result.Replayed)
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
	result, err := h.changeSets.ApplyChangeSetResult(c.Request.Context(), actor, c.Param("projectId"), c.Param("changeSetId"), key)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, result.ChangeSet, result.Replayed)
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
	result, err := h.changeSets.RejectChangeSetResult(c.Request.Context(), actor, c.Param("projectId"), c.Param("changeSetId"), key)
	if err != nil {
		sendError(c, err)
		return
	}
	sendOK(c, http.StatusOK, result.ChangeSet, result.Replayed)
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
