package workspace

import (
	"context"
	"encoding/json"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type FullWorkflowDependencies struct {
	Core       *core.Service
	Governance *TemplateGovernanceRuntime
}

func (h *Handler) RegisterFullRoutes(group *gin.RouterGroup) {
	if h.full == nil || h.full.Core == nil || group == nil {
		return
	}
	RegisterG2FullRoutes(group, h.full.Core)
	RegisterImpactRoutes(group, h.full.Core)
	RegisterG4FullRoutes(group, h.full.Governance)
	registerG5G7FullRoutes(group, h.full.Core)
}

func registerG5G7FullRoutes(group *gin.RouterGroup, s *core.Service) {
	h := impactRoutesHandler{service: s}
	group.GET("/discarded-projects", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.Project, error) {
		return s.ListDiscardedProjects(c.Request.Context(), a)
	}))

	group.GET("/project-invitations", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.MemberInvitation, error) {
		return s.ListMyProjectInvitations(c.Request.Context(), a)
	}))
	group.GET("/projects/:projectId/owner-transfers", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.OwnerTransfer, error) {
		return s.ListOwnerTransfers(c.Request.Context(), a, c.Param("projectId"))
	}))
	group.POST("/projects/:projectId/owner-transfer/:transferId/reject", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, in core.OwnerTransferDecisionInput) (json.RawMessage, int, bool, error) {
		return s.RejectOwnerTransfer(c.Request.Context(), a, c.Param("projectId"), c.Param("transferId"), k, in)
	}))
	group.GET("/projects/:projectId/permissions", impactRouteRead(h, func(c *gin.Context, a core.Actor) (core.EffectivePermission, error) {
		return s.EffectivePermission(c.Request.Context(), a, c.Param("projectId"), c.DefaultQuery("capability", "read"))
	}))
	group.GET("/projects/:projectId/members/directory", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.MemberDirectoryEntry, error) {
		return s.ProjectMemberDirectory(c.Request.Context(), a, c.Param("projectId"))
	}))
	group.GET("/projects/:projectId/invitations", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.MemberInvitation, error) {
		return s.ListProjectInvitations(c.Request.Context(), a, c.Param("projectId"))
	}))
	group.POST("/projects/:projectId/invitations", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, in core.InviteMemberInput) (json.RawMessage, int, bool, error) {
		return s.InviteProjectMember(c.Request.Context(), a, c.Param("projectId"), k, in)
	}))
	group.POST("/projects/:projectId/invitations/:invitationId/accept", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, in core.AcceptMemberInput) (json.RawMessage, int, bool, error) {
		return s.AcceptProjectInvitation(c.Request.Context(), a, c.Param("projectId"), c.Param("invitationId"), k, in)
	}))
	group.PUT("/projects/:projectId/members/:userId/assignment", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, in core.MemberAssignmentInput) (json.RawMessage, int, bool, error) {
		return s.SetMemberAssignment(c.Request.Context(), a, c.Param("projectId"), c.Param("userId"), k, in)
	}))
	group.POST("/projects/:projectId/members/:userId/state", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, in core.MemberLifecycleInput) (json.RawMessage, int, bool, error) {
		return s.ChangeMemberState(c.Request.Context(), a, c.Param("projectId"), c.Param("userId"), k, in)
	}))
	group.PUT("/projects/:projectId/api-key-grants", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, in core.ProjectAPIKeyGrant) (json.RawMessage, int, bool, error) {
		return s.SaveProjectAPIKeyGrant(c.Request.Context(), a, c.Param("projectId"), k, in)
	}))
	group.POST("/projects/:projectId/activation-read", func(c *gin.Context) {
		a, ok := h.actor(c, true)
		if !ok {
			return
		}
		var in struct {
			ExpectedProjectVersion int64 `json:"expected_project_version"`
		}
		if !decodeBody(c, &in) {
			return
		}
		result, err := s.RecordActivationRead(c.Request.Context(), a, c.Param("projectId"), in.ExpectedProjectVersion)
		if err != nil {
			sendError(c, err)
			return
		}
		sendOK(c, 201, result, false)
	})
	group.GET("/projects/:projectId/chapters/:chapterId/review", impactRouteRead(h, func(c *gin.Context, a core.Actor) (core.ChapterReviewState, error) {
		return s.GetChapterReview(c.Request.Context(), a, c.Param("projectId"), c.Param("chapterId"))
	}))
	group.POST("/projects/:projectId/chapters/:chapterId/review", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, in core.ChapterReviewInput) (json.RawMessage, int, bool, error) {
		return s.ChangeChapterReview(c.Request.Context(), a, c.Param("projectId"), c.Param("chapterId"), k, in)
	}))
	group.GET("/projects/:projectId/chapters/:chapterId/comments", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.ChapterComment, error) {
		return s.ListChapterComments(c.Request.Context(), a, c.Param("projectId"), c.Param("chapterId"), c.Query("version_id"))
	}))
	group.POST("/projects/:projectId/chapters/:chapterId/comments", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, in core.ChapterCommentInput) (json.RawMessage, int, bool, error) {
		return s.SaveChapterComment(c.Request.Context(), a, c.Param("projectId"), c.Param("chapterId"), k, in)
	}))
}

// Preserve the Actor in domain calls; do not manufacture Owner authority.
func fullWorkflowActor(ctx context.Context, user string) (core.Actor, bool) {
	tenant, ok := types.TenantIDFromContext(ctx)
	return core.Actor{TenantID: tenant, UserID: user, Role: types.TenantRoleFromContext(ctx)}, ok && tenant != 0 && user != ""
}
