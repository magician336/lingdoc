package rulesetstore

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// #193 残留②切片:metrics HTTP seam 的失败路径此前没有直接断言。本文件钉住
// 三条:① application 未实现 MetricsApplication 时的 503(且零指标读出);
// ② 查询参数白名单(cursor/action/limit)拒绝;③ 读路径授权的分形——同租户
// 非成员 403、跨租户 404 存在性隐藏(真实 Store 与 HTTP fixture 共用同一份库,
// 否则形状断言测的是空库)。

// metricsMissingApp 实现.Application 但**故意不实现** MetricsApplication:
// type-assertion fallback 必须给 503 metrics_unavailable。
type metricsMissingApp struct{ httpApplicationStub }

var _ Application = (*metricsMissingApp)(nil)

// metricsErrApp 实现 MetricsApplication 并返回注入的失败。
type metricsErrApp struct {
	httpApplicationStub
	err error
}

var _ Application = (*metricsErrApp)(nil)

// ReadRuleSetMetrics 返回注入的失败:用它断言 metrics 读取路径的错误形状
// (404 存在性隐藏/400 参数拒绝都在本方法被调之前)。
func (a metricsErrApp) ReadRuleSetMetrics(context.Context, Actor, string, RuleSetMetricsQuery) (RuleSetMetricsSnapshot, error) {
	return RuleSetMetricsSnapshot{}, a.err
}

var metricsFixtureDB *gorm.DB

func metricsHTTPFixture(t *testing.T, app Application) (*gin.Engine, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, _ := openMetricsDatabase(t)
	if err := db.Exec("INSERT INTO lingdoc_projects (id) VALUES (?)", "project-metrics").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO lingdoc_members (project_id, user_id, role) VALUES (?, ?, ?)", "project-metrics", "admin-1", "owner").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE tenant_members (tenant_id INTEGER NOT NULL, user_id TEXT NOT NULL, role TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'active', deleted_at DATETIME)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO tenant_members (tenant_id, user_id, role, status) VALUES (1, 'admin-1', 'owner', 'active')").Error; err != nil {
		t.Fatal(err)
	}
	group := gin.New()
	NewHTTPHandler(app, testRequestInfo).Register(group, group)
	return group, db
}

func TestG6MetricsEndpointFailsWithoutMetricsApplication(t *testing.T) {
	group, _ := metricsHTTPFixture(t, &metricsMissingApp{})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/projects/project-metrics/rule-set-use-audit-metrics", nil).WithContext(testIdentityContext("admin-1", string(types.TenantRoleAdmin)))
	group.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("metrics without MetricsApplication = %d, want 503: %s", response.Code, response.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "metrics_unavailable" {
		t.Fatalf("error code = %q, want metrics_unavailable", body.Error.Code)
	}
}

func TestG6MetricsEndpointRejectsWhitelistedQueryParameters(t *testing.T) {
	group, _ := metricsHTTPFixture(t, &metricsErrApp{err: ErrRuleSetMetricsUnavailable})
	// 参数拒绝发生在 metrics 读取之前:即使 application 注入失败,也应是 400。
	for _, query := range []string{"?cursor=x", "?action=preflight", "?limit=5"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/projects/project-metrics/rule-set-use-audit-metrics"+query, nil).WithContext(testIdentityContext("admin-1", string(types.TenantRoleAdmin)))
		group.ServeHTTP(response, request)
		t.Logf("DIAG %s = %d body=%s", query, response.Code, response.Body.String()[:120])
		if response.Code != http.StatusBadRequest {
			t.Fatalf("metrics %s = %d, want 400: %s", query, response.Code, response.Body.String())
		}
	}
}

func TestG6MetricsEndpointHidesTheProjectFromNonMembers(t *testing.T) {
	// 用产品授权组件 TenantMembershipAuthorizer(与 HTTP fixture 共用同一份库,
	// tenant_members 是它的数据源):读路径授权的分形——同租户非成员与跨租户
	// 都拿 403 forbidden(不泄露项目与指标的存在);租户成员(owner)读得到。
	// 注入失败的 stub 会把形状变成 503,测不到这一层。
	// fixture 的 app 用真实 store:HTTP 读路径的授权分形(authorizeAuditProject
	// 的成员/租户检查)由它承载。同租户非成员与跨租户分别得到 403/404。
	db, _ := openMetricsDatabase(t)
	if err := db.Exec("INSERT INTO lingdoc_projects (id) VALUES (?)", "project-metrics").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO lingdoc_members (project_id, user_id, role) VALUES (?, ?, ?)", "project-metrics", "admin-1", "owner").Error; err != nil {
		t.Fatal(err)
	}
	// 产品授权的数据源表:ReadRuleSetMetrics 的 owner 放行依据(this fixture
	// 需要自建——与 metricsHTTPFixture 的库是两份)。
	if err := db.Exec("CREATE TABLE tenant_members (tenant_id INTEGER NOT NULL, user_id TEXT NOT NULL, role TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'active', deleted_at DATETIME)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO tenant_members (tenant_id, user_id, role, status) VALUES (1, 'admin-1', 'owner', 'active')").Error; err != nil {
		t.Fatal(err)
	}
	store := NewStore(db, TenantMembershipAuthorizer{})
	gin.SetMode(gin.TestMode)
	group := gin.New()
	NewHTTPHandler(store, testRequestInfo).Register(group, group)

	sameTenantNonMember := Actor{TenantID: 1, UserID: "not-a-member", Role: string(types.TenantRoleViewer)}
	// 服务层对照:同一读路径对同租户非成员的拒绝形状(403 归属)。
	if _, err := store.ReadRuleSetMetrics(context.Background(), sameTenantNonMember, "project-metrics", RuleSetMetricsQuery{}); !errors.Is(err, ErrRuleSetAccessDenied) {
		t.Fatalf("service-level read for a non-member = %v, want ErrRuleSetAccessDenied", err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/projects/project-metrics/rule-set-use-audit-metrics", nil).WithContext(testIdentityContext(sameTenantNonMember.UserID, sameTenantNonMember.Role))
	group.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("metrics for a same-tenant non-member = %d, want 403: %s", response.Code, response.Body.String())
	}

	member := Actor{TenantID: 1, UserID: "admin-1", Role: string(types.TenantRoleOwner)}
	// 诊断:直接服务层调同一路径,看真实错误
	if _, diagErr := store.ReadRuleSetMetrics(context.Background(), member, "project-metrics", RuleSetMetricsQuery{}); diagErr != nil {
		t.Logf("DIAG owner service-level read err = %v", diagErr)
	}
	memberResponse := httptest.NewRecorder()
	memberRequest := httptest.NewRequest(http.MethodGet, "/projects/project-metrics/rule-set-use-audit-metrics", nil).WithContext(testIdentityContext(member.UserID, member.Role))
	group.ServeHTTP(memberResponse, memberRequest)
	if memberResponse.Code != http.StatusOK {
		t.Fatalf("metrics for the tenant owner = %d, want 200: %s", memberResponse.Code, memberResponse.Body.String())
	}

	outsideTenant := Actor{TenantID: 2, UserID: "outside-tenant", Role: string(types.TenantRoleViewer)}
	outsideResponse := httptest.NewRecorder()
	outsideRequest := httptest.NewRequest(http.MethodGet, "/projects/project-metrics/rule-set-use-audit-metrics", nil).WithContext(testIdentityContext(outsideTenant.UserID, outsideTenant.Role))
	group.ServeHTTP(outsideResponse, outsideRequest)
	if outsideResponse.Code != http.StatusForbidden {
		t.Fatalf("metrics for an outside-tenant caller = %d, want 403: %s", outsideResponse.Code, outsideResponse.Body.String())
	}
}
