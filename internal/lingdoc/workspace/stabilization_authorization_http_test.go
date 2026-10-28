package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// #647 残留 3：稳定化的授权边界必须在传输面上钉住。
//
// 独立复核把整条授权链读了一遍，结论是「不存在越权」，但当时第 1 层（API
// 密钥闸门）与第 2 层（租户角色守卫）只做了静态读码，没有真的跑起来。这里
// 把它们跑起来：真实 gin 引擎、真实中间件、真实影响路由、真实存储。
//
// 机器主体被拒的事实有两道独立的闸：路由上的 API 密钥闸门（未声明策略的
// 路由默认拒绝，哪怕密钥是 full access）与路由自身的人机门禁。下面的用例
// 把两道分别钉住，并各带一条正对照，证明这些拒绝不是因为夹具坏了。
// stabilizationAuthorizationEngine 组装产品真实使用的那条链：API 密钥闸门、
// 租户角色守卫、真实的影响路由。
func stabilizationAuthorizationEngine(t *testing.T, f deliveryGuardFixture) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	v1 := engine.Group("/api/v1/lingdoc")
	v1.Use(middleware.NewAPIKeyRouteAuthorizer().Middleware())
	enabled := true
	v1.Use(middleware.RequireRole(types.TenantRoleViewer, &config.Config{Tenant: &config.TenantConfig{EnableRBAC: &enabled}}))
	RegisterImpactRoutes(v1, impactPortsFor(f.projects))
	return engine
}

// stabilizationRouteOnlyEngine 只装真实路由，不装 API 密钥闸门。用来把路由
// 自己的人机门禁与网关那道闸门分开看。
func stabilizationRouteOnlyEngine(t *testing.T, f deliveryGuardFixture) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	RegisterImpactRoutes(engine.Group("/api/v1/lingdoc"), impactPortsFor(f.projects))
	return engine
}

// stabilizationAPIKeyContext 造出一个最像真的、也最危险的机器主体：它带着
// 该租户自己的 API 密钥范围，带着租户 owner 角色，并且 user id 就是这个
// 项目 owner 的 user id。认证中间件在 full access 密钥上就是这样装配的
// （internal/middleware/auth.go 的 attachAPIKeyAuthContext）。
func stabilizationAPIKeyContext(base context.Context, tenantID uint64, userID string) context.Context {
	ctx := types.WithTenantAPIKeyScope(base, types.TenantAPIKeyScope{
		KeyID: 17, ScopeType: types.APIKeyScopeTenant, FullAccess: true,
	})
	ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalAPITenant, ID: strconv.FormatUint(tenantID, 10)})
	ctx = context.WithValue(ctx, types.UserIDContextKey, userID)
	ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleOwner)
	return ctx
}

// stabilizationHTTPAsk 发一次真实的稳定化请求。
func stabilizationHTTPAsk(engine *gin.Engine, ctx context.Context, projectID, changeID, key string) *httptest.ResponseRecorder {
	path := fmt.Sprintf("/api/v1/lingdoc/projects/%s/change-sets/%s/stabilize", projectID, changeID)
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
	request = request.WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

// TestStabilizeOverHTTPRefusesAnAPIKeyPrincipal 钉住：API 密钥主体在传输面
// 上被拒绝。请求带着真实中间件链走一遍，最坏的装配（全访问密钥 + 租户 owner
// 角色 + owner 的 user id）也必须被拒。
func TestStabilizeOverHTTPRefusesAnAPIKeyPrincipal(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	engine := stabilizationAuthorizationEngine(t, f)
	change := applyChangeForGate(t, f, "residual-auth-apikey")
	repairGateObligations(t, f, change.ID)

	ctx := stabilizationAPIKeyContext(f.ctx, f.actor.TenantID, f.actor.UserID)
	response := stabilizationHTTPAsk(engine, ctx, f.project.ID, change.ID, "residual-auth-apikey-stabilize")
	require.Equal(t, http.StatusForbidden, response.Code,
		"an API key principal must be refused over HTTP: body=%s", response.Body.String())
	require.NotContains(t, response.Body.String(), "human_decision_required",
		"the API key must be stopped at the gateway, not at the route gate: body=%s", response.Body.String())

	// 正对照：同一个引擎，同一条路由，Owner 的会话凭据必须能稳定化。
	owner := stabilizationHTTPAsk(engine, f.requestContext(f.actor.UserID, f.actor.Role, "residual-auth-owner"),
		f.project.ID, change.ID, "residual-auth-owner-stabilize")
	require.Equal(t, http.StatusOK, owner.Code,
		"the Owner must still be able to stabilize: body=%s", owner.Body.String())
	var payload struct {
		Data core.StabilizationResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(owner.Body.Bytes(), &payload))
	require.Equal(t, "STABILIZED", payload.Data.Status, "body=%s", owner.Body.String())
}

// TestStabilizeOverHTTPRefusesAMachinePrincipalAtTheRouteGate 钉住：路由
// 自己的人机门禁。即使没有网关那道闸（这里故意不装 API 密钥闸门，好把两道
// 闸分开看），带机器主体的请求仍然在路由上被拒。
func TestStabilizeOverHTTPRefusesAMachinePrincipalAtTheRouteGate(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	engine := stabilizationRouteOnlyEngine(t, f)
	change := applyChangeForGate(t, f, "residual-auth-route-gate")
	repairGateObligations(t, f, change.ID)

	ctx := stabilizationAPIKeyContext(f.ctx, f.actor.TenantID, f.actor.UserID)
	response := stabilizationHTTPAsk(engine, ctx, f.project.ID, change.ID, "residual-auth-route-gate-stabilize")
	require.Equal(t, http.StatusForbidden, response.Code,
		"a machine principal must be refused at the route gate: body=%s", response.Body.String())
	require.Contains(t, response.Body.String(), "human_decision_required",
		"the refusal must name the human decision gate: body=%s", response.Body.String())
}

// TestStabilizeOverHTTPRefusesAMemberWhoIsNotTheOwner 钉住：项目里的普通
// 成员不能稳定化。拒绝不能泄露变更集的存在，所以是 404 而不是 403。
func TestStabilizeOverHTTPRefusesAMemberWhoIsNotTheOwner(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	engine := stabilizationRouteOnlyEngine(t, f)
	change := applyChangeForGate(t, f, "residual-auth-member")
	repairGateObligations(t, f, change.ID)

	member := "residual-auth-collaborator"
	require.NoError(t, f.db.Exec("INSERT INTO users(id,username,is_active) VALUES(?,?,1)", member, member).Error)
	require.NoError(t, f.db.Exec(
		"INSERT INTO tenant_members(tenant_id,user_id,role,status) VALUES(?,?,'contributor','active')",
		f.actor.TenantID, member).Error)
	require.NoError(t, f.db.Exec(
		"INSERT INTO lingdoc_members(project_id,user_id,role) VALUES(?,?,'collaborator')",
		f.project.ID, member).Error)

	response := stabilizationHTTPAsk(engine, f.requestContext(member, types.TenantRoleContributor, "residual-auth-member-stabilize"),
		f.project.ID, change.ID, "residual-auth-member-stabilize")
	require.Equal(t, http.StatusNotFound, response.Code,
		"a member who is not the Owner must be refused: body=%s", response.Body.String())
	require.Contains(t, response.Body.String(), "not_found",
		"the refusal must not disclose the change set: body=%s", response.Body.String())
}
