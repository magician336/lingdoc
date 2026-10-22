package workspace

import (
	"context"
	"encoding/json"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type FullWorkflowDependencies struct {
	Governance *TemplateGovernanceRuntime
	Impact     ImpactRoutePorts
	Agent         AgentRoutePorts
	Routes     FullWorkflowRoutePorts
}

func (h *Handler) RegisterFullRoutes(group *gin.RouterGroup) {
	if h.full == nil || group == nil || !h.full.Routes.wired() {
		return
	}
	RegisterAgentRoutes(group, h.full.Agent)
	RegisterImpactRoutes(group, h.full.Impact)
	RegisterRoadmapRoutes(group, h.full.Impact)
	RegisterTemplateGovernanceRoutes(group, h.full.Governance)
	registerG5G7FullRoutes(group, h.full.Routes, h.full.Impact)
}

// registerG5G7FullRoutes 注册 G5/G7 的全流程协作路由。
//
// 三个上下文窄口承载这条面的十七个调用：成员与 Owner 十一个、章节复核四个、
// 项目生命周期两个。读写外壳仍然由影响中心那条面的 handler 提供，所以这里
// 还需要那份窄口束。
func registerG5G7FullRoutes(group *gin.RouterGroup, routes FullWorkflowRoutePorts, impact ImpactRoutePorts) {
	h := impactRoutesHandler{ports: impact}
	group.GET("/discarded-projects", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.Project, error) {
		return routes.Lifecycle.ListDiscardedProjects(c.Request.Context(), a)
	}))

	group.GET("/project-invitations", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.MemberInvitation, error) {
		return routes.Members.ListMyProjectInvitations(c.Request.Context(), a)
	}))
	group.GET("/projects/:projectId/owner-transfers", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.OwnerTransfer, error) {
		return routes.Members.ListOwnerTransfers(c.Request.Context(), a, c.Param("projectId"))
	}))
	group.POST("/projects/:projectId/owner-transfer/:transferId/reject", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, in core.OwnerTransferDecisionInput) (json.RawMessage, int, bool, error) {
		return routes.Members.RejectOwnerTransfer(c.Request.Context(), a, c.Param("projectId"), c.Param("transferId"), k, in)
	}))
	group.GET("/projects/:projectId/permissions", impactRouteRead(h, func(c *gin.Context, a core.Actor) (core.EffectivePermission, error) {
		return routes.Members.EffectivePermission(c.Request.Context(), a, c.Param("projectId"), c.DefaultQuery("capability", "read"))
	}))
	group.GET("/projects/:projectId/members/directory", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.MemberDirectoryEntry, error) {
		return routes.Members.ProjectMemberDirectory(c.Request.Context(), a, c.Param("projectId"))
	}))
	group.GET("/projects/:projectId/invitations", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.MemberInvitation, error) {
		return routes.Members.ListProjectInvitations(c.Request.Context(), a, c.Param("projectId"))
	}))
	group.POST("/projects/:projectId/invitations", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, in core.InviteMemberInput) (json.RawMessage, int, bool, error) {
		return routes.Members.InviteProjectMember(c.Request.Context(), a, c.Param("projectId"), k, in)
	}))
	group.POST("/projects/:projectId/invitations/:invitationId/accept", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, in core.AcceptMemberInput) (json.RawMessage, int, bool, error) {
		return routes.Members.AcceptProjectInvitation(c.Request.Context(), a, c.Param("projectId"), c.Param("invitationId"), k, in)
	}))
	group.PUT("/projects/:projectId/members/:userId/assignment", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, in core.MemberAssignmentInput) (json.RawMessage, int, bool, error) {
		return routes.Members.SetMemberAssignment(c.Request.Context(), a, c.Param("projectId"), c.Param("userId"), k, in)
	}))
	group.POST("/projects/:projectId/members/:userId/state", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, in core.MemberLifecycleInput) (json.RawMessage, int, bool, error) {
		return routes.Members.ChangeMemberState(c.Request.Context(), a, c.Param("projectId"), c.Param("userId"), k, in)
	}))
	group.PUT("/projects/:projectId/api-key-grants", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, in core.ProjectAPIKeyGrant) (json.RawMessage, int, bool, error) {
		return routes.Members.SaveProjectAPIKeyGrant(c.Request.Context(), a, c.Param("projectId"), k, in)
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
		result, err := routes.Lifecycle.RecordActivationRead(c.Request.Context(), a, c.Param("projectId"), in.ExpectedProjectVersion)
		if err != nil {
			sendError(c, err)
			return
		}
		sendOK(c, 201, result, false)
	})
	group.GET("/projects/:projectId/chapters/:chapterId/review", impactRouteRead(h, func(c *gin.Context, a core.Actor) (core.ChapterReviewState, error) {
		return routes.Reviews.GetChapterReview(c.Request.Context(), a, c.Param("projectId"), c.Param("chapterId"))
	}))
	group.POST("/projects/:projectId/chapters/:chapterId/review", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, in core.ChapterReviewInput) (json.RawMessage, int, bool, error) {
		return routes.Reviews.ChangeChapterReview(c.Request.Context(), a, c.Param("projectId"), c.Param("chapterId"), k, in)
	}))
	group.GET("/projects/:projectId/chapters/:chapterId/comments", impactRouteRead(h, func(c *gin.Context, a core.Actor) ([]core.ChapterComment, error) {
		return routes.Reviews.ListChapterComments(c.Request.Context(), a, c.Param("projectId"), c.Param("chapterId"), c.Query("version_id"))
	}))
	group.POST("/projects/:projectId/chapters/:chapterId/comments", impactRouteWrite(h, true, func(c *gin.Context, a core.Actor, k string, in core.ChapterCommentInput) (json.RawMessage, int, bool, error) {
		return routes.Reviews.SaveChapterComment(c.Request.Context(), a, c.Param("projectId"), c.Param("chapterId"), k, in)
	}))
}

// Preserve the Actor in domain calls; do not manufacture Owner authority.
func fullWorkflowActor(ctx context.Context, user string) (core.Actor, bool) {
	tenant, ok := types.TenantIDFromContext(ctx)
	return core.Actor{TenantID: tenant, UserID: user, Role: types.TenantRoleFromContext(ctx)}, ok && tenant != 0 && user != ""
}
