package workspace

import (
	"context"
	"errors"
	"strconv"

	"github.com/Tencent/WeKnora/internal/evidence"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// The existing production SourcePolicy gains a machine extension in this file;
// its source resolver, revision reader and source-currentness rules are reused.
var _ core.MachineReadSourcePolicy = workspaceSourcePolicy{}

func (p workspaceSourcePolicy) ValidateMachineRead(ctx context.Context, a core.MachineReadAuthorization) error {
	r := p.handler
	if r == nil || r.db == nil || r.bindings == nil || r.gateway == nil ||
		r.kbShares == nil || r.knowledge == nil || r.service == nil {
		return core.ErrMachineReadDependency
	}
	if !a.MatchesContext(ctx) {
		return core.ErrNotFound
	}
	origins := r.origins()
	gateway := evidence.NewAssetGateway(r.bindings, machineDelegatedAssetAuthorizer{runtime: r, authorization: a})
	checker := sourceRevalidator{db: r.db, bindings: r.bindings, gateway: gateway, origins: origins,
		policy: evidence.NewSourcePolicy(gateway, origins, r.bindings)}
	ids := a.SourceIDs()
	verdicts, err := checker.Review(ctx, a.ProjectID(),
		evidence.Actor{UserID: a.Principal().StorageID(), TenantID: strconv.FormatUint(a.TenantID(), 10)}, ids)
	if err != nil {
		return err
	}
	if len(verdicts) != len(ids) {
		return core.ErrMachineReadDependency
	}
	for _, verdict := range verdicts {
		if !verdict.Usable {
			return core.ErrNotFound
		}
	}
	return nil
}

type machineDelegatedAssetAuthorizer struct {
	runtime       *SourceRuntime
	authorization core.MachineReadAuthorization
}

func (g machineDelegatedAssetAuthorizer) CanAccessAsset(ctx context.Context, actor evidence.Actor, pid string, asset evidence.Asset) (bool, error) {
	a := g.authorization
	if !a.MatchesContext(ctx) || actor.UserID != a.Principal().StorageID() ||
		actor.TenantID != strconv.FormatUint(a.TenantID(), 10) || pid != a.ProjectID() {
		return false, nil
	}
	scope, err := g.runtime.bindings.AssetScope(ctx, pid, asset.ID)
	if err != nil {
		return false, err
	}
	key := a.KeyScope()
	// This bridge requires an explicit KB envelope for source-bearing reads.
	if len(key.KnowledgeBaseIDs) == 0 || !key.AllowsKnowledgeBase(scope.KnowledgeBaseID) {
		return false, nil
	}
	policy, err := core.ReadProjectAssetPolicyFromDB(ctx, g.runtime.db, pid, asset.ID)
	if err != nil {
		return false, err
	}
	allowed := policy.Scope == "project" || policy.Scope == "team" ||
		policy.Scope == "personal" && policy.CreatedBy == a.GrantorID()
	if len(policy.AllowedUserIDs) > 0 {
		found := false
		for _, user := range policy.AllowedUserIDs {
			if user == a.GrantorID() {
				found = true
				break
			}
		}
		allowed = allowed && found
	}
	if !allowed {
		return false, nil
	}
	if scope.OwnerTenantID == a.TenantID() {
		return true, nil
	}
	// GrantorRole is a current DB permission ceiling used to evaluate the
	// delegation. Caller/Principal remain the authenticated machine throughout.
	role, shared, err := g.runtime.kbShares.CheckTenantKBPermission(ctx, scope.KnowledgeBaseID, a.TenantID(), a.GrantorRole())
	return err == nil && shared && role.IsValid() && role.HasPermission(types.OrgRoleViewer), err
}

type machineReadApplication interface {
	ReadMachineProjectSummary(context.Context, string) (core.MachineProjectSummary, error)
	ReadMachineChapter(context.Context, string, string) (core.MachineChapterView, error)
}
type MachineReadGetRegistrar interface {
	GET(string, ...gin.HandlerFunc) gin.IRoutes
}

// Registration has no effects for legacy/stub handlers without the actual seam.
func (h *Handler) RegisterMachineReadRoutes(group MachineReadGetRegistrar) {
	service := h.machineReads
	if service == nil || group == nil {
		return
	}
	reply := func(c *gin.Context, result any, err error) {
		if err != nil {
			if errors.Is(err, core.ErrMachineReadDependency) {
				sendErrorDetails(c, 503, "machine_read_unavailable", "机器读取依赖尚未就绪。", nil)
			} else {
				sendError(c, err)
			}
			return
		}
		sendOK(c, 200, result, false)
	}
	group.GET("/projects/:projectId/machine-read", func(c *gin.Context) {
		result, err := service.ReadMachineProjectSummary(c.Request.Context(), c.Param("projectId"))
		reply(c, result, err)
	})
	group.GET("/projects/:projectId/chapters/:chapterId/machine-read", func(c *gin.Context) {
		result, err := service.ReadMachineChapter(c.Request.Context(), c.Param("projectId"), c.Param("chapterId"))
		reply(c, result, err)
	})
}
