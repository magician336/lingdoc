package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/clienterror"
	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	full *FullWorkflowDependencies
	// Optional G5/G7 extensions follow the transport direction as well; a
	// read dependency must never become the implicit review-anchor writer.
	machineReads       machineReadApplication
	reviewAnchorsRead  reviewAnchorReadApplication
	reviewAnchorsWrite reviewAnchorWriteApplication
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

func NewHandler(deps HandlerDependencies) *Handler {
	templates := deps.Templates
	if templates == nil {
		templates = delivery.NewFixedTemplateReader()
	}
	projects, members, chapters, templatesApp, changeSets := deps.Projects, deps.Members, deps.Chapters, deps.TemplateApp, deps.ChangeSets
	// ApplicationService is a compatibility edge for older composition roots.
	// New callers should inject the focused ports above; this adapter is the only
	// place where the legacy cross-use-case surface is widened again.
	var legacy ApplicationService = deps.Service
	// When both focused ports are present, keep them split even if a broad test
	// double or adapter happens to implement ApplicationService as well. Inferring
	// the legacy adapter from Read first would route mutations through the read port.
	if legacy == nil && (deps.Read == nil || deps.Write == nil) {
		if candidate, ok := any(deps.Read).(ApplicationService); ok {
			legacy = candidate
		} else if candidate, ok := any(deps.Write).(ApplicationService); ok {
			legacy = candidate
		}
	}
	// Keep the legacy extension bridge, but never infer the five route ports
	// from one side when explicit read/write dependencies were supplied.
	if legacy != nil && (deps.Service != nil || deps.Read == nil || deps.Write == nil) {
		adapter := legacyApplicationAdapter{ApplicationService: legacy}
		if projects == nil {
			projects = adapter
		}
		if members == nil {
			members = adapter
		}
		if chapters == nil {
			chapters = adapter
		}
		if templatesApp == nil {
			templatesApp = adapter
		}
		if changeSets == nil {
			changeSets = adapter
		}
	}
	// A focused test double may implement one port without carrying the legacy
	// interface. With both read/write ports present, use the split adapters below
	// rather than a broad interface inferred from just one dependency.
	if deps.Read == nil || deps.Write == nil {
		if projects == nil {
			if candidate, ok := any(deps.Read).(ProjectApplication); ok {
				projects = candidate
			} else if candidate, ok := any(deps.Write).(ProjectApplication); ok {
				projects = candidate
			}
		}
		if members == nil {
			if candidate, ok := any(deps.Write).(MembershipApplication); ok {
				members = candidate
			}
		}
		if chapters == nil {
			if candidate, ok := any(deps.Read).(ChapterApplication); ok {
				chapters = candidate
			} else if candidate, ok := any(deps.Write).(ChapterApplication); ok {
				chapters = candidate
			}
		}
		if templatesApp == nil {
			if candidate, ok := any(deps.Read).(TemplateApplication); ok {
				templatesApp = candidate
			} else if candidate, ok := any(deps.Write).(TemplateApplication); ok {
				templatesApp = candidate
			}
		}
		if changeSets == nil {
			if candidate, ok := any(deps.Read).(ChangeSetApplication); ok {
				changeSets = candidate
			} else if candidate, ok := any(deps.Write).(ChangeSetApplication); ok {
				changeSets = candidate
			}
		}
	}
	// A composition root may provide the intentionally split read and write
	// ports without also carrying the legacy ApplicationService. Compose the
	// five focused route ports explicitly at that seam so every registered
	// route has a non-nil dependency and the handler never widens the contract
	// again just to make a route work.
	if deps.Read != nil && deps.Write != nil {
		if projects == nil {
			projects = splitProjectApplication{read: deps.Read, write: deps.Write}
		}
		if members == nil {
			members = splitMembershipApplication{write: deps.Write}
		}
		if chapters == nil {
			chapters = splitChapterApplication{read: deps.Read, write: deps.Write}
		}
		if templatesApp == nil {
			templatesApp = splitTemplateApplication{read: deps.Read, write: deps.Write}
		}
		if changeSets == nil {
			changeSets = splitChangeSetApplication{read: deps.Read, write: deps.Write}
		}
	}
	// Explicit compatibility injection remains supported. Otherwise resolve
	// optional extensions independently so distinct read/write services stay split.
	var readExtension, writeExtension any = deps.Service, deps.Service
	if deps.Service == nil {
		readExtension, writeExtension = deps.Read, deps.Write
		if readExtension == nil {
			readExtension = deps.Write
		}
		if writeExtension == nil {
			writeExtension = deps.Read
		}
	}
	machineReads, _ := readExtension.(machineReadApplication)
	anchorReads, _ := readExtension.(reviewAnchorReadApplication)
	anchorWrites, _ := writeExtension.(reviewAnchorWriteApplication)
	return &Handler{
		full: deps.FullWorkflow, machineReads: machineReads,
		reviewAnchorsRead: anchorReads, reviewAnchorsWrite: anchorWrites,
		projects: projects, members: members, chapters: chapters, templatesApp: templatesApp, changeSets: changeSets,
		sources: deps.Sources, integration: deps.Integration,
		rewrites: deps.SelectedRewrites, templates: templates,
	}
}

// legacyApplicationAdapter makes the migration boundary explicit in the
// composition root while keeping old test doubles and transports working.
// Existing route families consume only the focused ports.
type legacyApplicationAdapter struct{ ApplicationService }

type splitProjectApplication struct {
	read  WorkspaceReadApplication
	write WorkspaceWriteApplication
}

func (a splitProjectApplication) ListProjects(ctx context.Context, actor Actor) ([]Project, bool, error) {
	return a.read.ListProjects(ctx, actor)
}
func (a splitProjectApplication) GetProject(ctx context.Context, actor Actor, projectID string) (Project, error) {
	return a.read.GetProject(ctx, actor, projectID)
}
func (a splitProjectApplication) CreateProjectResult(ctx context.Context, actor Actor, key string, input CreateProjectInput) (CreateProjectResult, error) {
	return a.write.CreateProjectResult(ctx, actor, key, input)
}
func (a splitProjectApplication) SaveSpecResult(ctx context.Context, actor Actor, projectID, key string, input SaveSpecInput) (SaveSpecResult, error) {
	return a.write.SaveSpecResult(ctx, actor, projectID, key, input)
}
func (a splitProjectApplication) ActivateProjectResult(ctx context.Context, actor Actor, projectID, key string, input ActivateProjectInput) (ProjectMutationResult, error) {
	return a.write.ActivateProjectResult(ctx, actor, projectID, key, input)
}
func (a splitProjectApplication) ListDraftCandidates(ctx context.Context, actor Actor, projectID string) ([]DraftCandidate, error) {
	return a.read.ListDraftCandidates(ctx, actor, projectID)
}
func (a splitProjectApplication) CreateDraftCandidateResult(ctx context.Context, actor Actor, projectID, key string, input DraftCandidateInput) (DraftCandidateMutationResult, error) {
	return a.write.CreateDraftCandidateResult(ctx, actor, projectID, key, input)
}
func (a splitProjectApplication) ListAuditEvents(ctx context.Context, actor Actor, projectID string) ([]AuditEvent, error) {
	return a.read.ListAuditEvents(ctx, actor, projectID)
}
func (a splitProjectApplication) ActivationDiff(ctx context.Context, actor Actor, projectID string, version int64) (ActivationDiff, error) {
	return a.read.ActivationDiff(ctx, actor, projectID, version)
}
func (a splitProjectApplication) DiscardProjectResult(ctx context.Context, actor Actor, projectID, key string, expected int64) (ProjectMutationResult, error) {
	return a.write.DiscardProjectResult(ctx, actor, projectID, key, expected)
}
func (a splitProjectApplication) RestoreProjectResult(ctx context.Context, actor Actor, projectID, key string, expected int64) (ProjectMutationResult, error) {
	return a.write.RestoreProjectResult(ctx, actor, projectID, key, expected)
}

type splitMembershipApplication struct{ write WorkspaceWriteApplication }

func (a splitMembershipApplication) SaveMembersResult(ctx context.Context, actor Actor, projectID, key string, input SaveMembersInput) (ProjectMutationResult, error) {
	return a.write.SaveMembersResult(ctx, actor, projectID, key, input)
}
func (a splitMembershipApplication) TransferOwnerResult(ctx context.Context, actor Actor, projectID, key string, input TransferOwnerInput) (ProjectMutationResult, error) {
	return a.write.TransferOwnerResult(ctx, actor, projectID, key, input)
}
func (a splitMembershipApplication) RequestOwnerTransferResult(ctx context.Context, actor Actor, projectID, key string, input OwnerTransferInput) (OwnerTransferResult, error) {
	return a.write.RequestOwnerTransferResult(ctx, actor, projectID, key, input)
}
func (a splitMembershipApplication) AcceptOwnerTransferResult(ctx context.Context, actor Actor, projectID, transferID, key string) (OwnerTransferResult, error) {
	return a.write.AcceptOwnerTransferResult(ctx, actor, projectID, transferID, key)
}

type splitChapterApplication struct {
	read  WorkspaceReadApplication
	write WorkspaceWriteApplication
}

func (a splitChapterApplication) ListChapters(ctx context.Context, actor Actor, projectID string) ([]Chapter, error) {
	return a.read.ListChapters(ctx, actor, projectID)
}
func (a splitChapterApplication) GetWorkingCopy(ctx context.Context, actor Actor, projectID, chapterID string) (WorkingCopy, error) {
	return a.read.GetWorkingCopy(ctx, actor, projectID, chapterID)
}
func (a splitChapterApplication) ListChapterVersions(ctx context.Context, actor Actor, projectID, chapterID string) ([]ChapterVersion, error) {
	return a.read.ListChapterVersions(ctx, actor, projectID, chapterID)
}
func (a splitChapterApplication) SaveChapterResult(ctx context.Context, actor Actor, projectID, chapterID, key string, input SaveChapterInput) (ChapterMutationResult, error) {
	return a.write.SaveChapterResult(ctx, actor, projectID, chapterID, key, input)
}
func (a splitChapterApplication) SaveWorkingCopyResult(ctx context.Context, actor Actor, projectID, chapterID, key string, input SaveWorkingCopyInput) (WorkingCopyMutationResult, error) {
	return a.write.SaveWorkingCopyResult(ctx, actor, projectID, chapterID, key, input)
}
func (a splitChapterApplication) ApplyRewriteResult(ctx context.Context, actor Actor, projectID, chapterID, key string, input core.SaveWorkingCopyInput, reviews []core.ReviewItem) (WorkingCopyMutationResult, error) {
	return a.write.ApplyRewriteResult(ctx, actor, projectID, chapterID, key, input, reviews)
}
func (a splitChapterApplication) CommitWorkingCopyResult(ctx context.Context, actor Actor, projectID, chapterID, key string, input CommitWorkingCopyInput) (CommittedChapterMutationResult, error) {
	return a.write.CommitWorkingCopyResult(ctx, actor, projectID, chapterID, key, input)
}
func (a splitChapterApplication) RestoreWorkingCopyResult(ctx context.Context, actor Actor, projectID, chapterID, key string, input RestoreWorkingCopyInput) (WorkingCopyMutationResult, error) {
	return a.write.RestoreWorkingCopyResult(ctx, actor, projectID, chapterID, key, input)
}

type splitTemplateApplication struct {
	read  WorkspaceReadApplication
	write WorkspaceWriteApplication
}

func (a splitTemplateApplication) PreviewTemplateMigration(ctx context.Context, actor Actor, projectID string, input TemplateMigrationInput) (TemplateMigrationPreview, error) {
	return a.read.PreviewTemplateMigration(ctx, actor, projectID, input)
}
func (a splitTemplateApplication) GetTemplateCopy(ctx context.Context, actor Actor, projectID string, version int64) (ProjectTemplateCopy, error) {
	return a.read.GetTemplateCopy(ctx, actor, projectID, version)
}
func (a splitTemplateApplication) PreviewTemplateCopyEdit(ctx context.Context, actor Actor, projectID string, input TemplateCopyDefinitionInput) (TemplateMigrationPreview, error) {
	return a.read.PreviewTemplateCopyEdit(ctx, actor, projectID, input)
}
func (a splitTemplateApplication) ChangeTemplateResult(ctx context.Context, actor Actor, projectID, key string, input TemplateMigrationInput) (ProjectMutationResult, error) {
	return a.write.ChangeTemplateResult(ctx, actor, projectID, key, input)
}
func (a splitTemplateApplication) SaveTemplateCopyResult(ctx context.Context, actor Actor, projectID, key string, input TemplateCopyDefinitionInput) (ProjectMutationResult, error) {
	return a.write.SaveTemplateCopyResult(ctx, actor, projectID, key, input)
}
func (a splitTemplateApplication) BindTemplateCopyRuleSetResult(ctx context.Context, actor Actor, projectID, key string, input BindTemplateCopyRuleSetInput) (ProjectMutationResult, error) {
	return a.write.BindTemplateCopyRuleSetResult(ctx, actor, projectID, key, input)
}

type splitChangeSetApplication struct {
	read  WorkspaceReadApplication
	write WorkspaceWriteApplication
}

func (a splitChangeSetApplication) CreateChangeSetResult(ctx context.Context, actor Actor, projectID, key string, input CreateChangeSetInput) (ChangeSetMutationResult, error) {
	return a.write.CreateChangeSetResult(ctx, actor, projectID, key, input)
}
func (a splitChangeSetApplication) ListChangeSets(ctx context.Context, actor Actor, projectID string) ([]ChangeSet, error) {
	return a.read.ListChangeSets(ctx, actor, projectID)
}
func (a splitChangeSetApplication) GetChangeSet(ctx context.Context, actor Actor, projectID, changeSetID string) (ChangeSet, error) {
	return a.read.GetChangeSet(ctx, actor, projectID, changeSetID)
}
func (a splitChangeSetApplication) ApplyChangeSetResult(ctx context.Context, actor Actor, projectID, changeSetID, key string) (ChangeSetMutationResult, error) {
	return a.write.ApplyChangeSetResult(ctx, actor, projectID, changeSetID, key)
}
func (a splitChangeSetApplication) RejectChangeSetResult(ctx context.Context, actor Actor, projectID, changeSetID, key string) (ChangeSetMutationResult, error) {
	return a.write.RejectChangeSetResult(ctx, actor, projectID, changeSetID, key)
}

func (a splitChangeSetApplication) ReassessChangeSetResult(ctx context.Context, actor Actor, projectID, changeSetID, key string) (ChangeSetMutationResult, error) {
	return a.write.ReassessChangeSetResult(ctx, actor, projectID, changeSetID, key)
}

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
	routes.Write.POST("/projects/:projectId/owner-transfer", h.requestOwnerTransfer)
	routes.Write.POST("/projects/:projectId/owner-transfer/:transferId/accept", h.acceptOwnerTransfer)
	routes.Write.PUT("/projects/:projectId/members", h.saveMembers)
	routes.Read.GET("/projects/:projectId/chapters", h.listChapters)
	routes.Read.GET("/projects/:projectId/chapters/:chapterId/working-copy", h.getWorkingCopy)
	routes.Read.GET("/projects/:projectId/chapters/:chapterId/confirmations/current", h.getCurrentConfirmation)
	routes.Read.GET("/projects/:projectId/chapters/:chapterId/versions", h.listChapterVersions)
	routes.Read.GET("/projects/:projectId/assets", h.listAssets)
	routes.Write.POST("/projects/:projectId/assets", h.bindAsset)
	routes.Read.POST("/projects/:projectId/retrieval", h.retrieveSources)
	routes.Read.GET("/projects/:projectId/sources/:sourceId", h.getSource)
	routes.Read.GET("/projects/:projectId/sources/:sourceId/context", h.getSourceContext)
	routes.Write.POST("/projects/:projectId/chapters/:chapterId/versions", h.saveChapter)
	routes.Write.PUT("/projects/:projectId/chapters/:chapterId/working-copy", h.saveWorkingCopy)
	routes.Write.PUT("/projects/:projectId/chapters/:chapterId/working-copy/review-items/:itemId/anchor", h.setReviewAnchor)
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
		c.JSON(form.Status, gin.H{"error": gin.H{"code": form.Code, "message": form.Message,
			"retryable": form.Retryable}, "request_id": requestID(c)})
		return
	}
	c.JSON(500, gin.H{"error": gin.H{"code": "internal_error", "message": "操作失败，请稍后再试。",
		"retryable": false}, "request_id": requestID(c)})
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
