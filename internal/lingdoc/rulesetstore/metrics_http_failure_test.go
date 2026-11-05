package rulesetstore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// #193 残留②切片:metrics HTTP seam 的失败路径此前没有直接断言。本文件钉住
// 三条:① application 未实现 MetricsApplication 时的 503(且零指标读出);
// ② 查询参数白名单(cursor/action/limit)拒绝;③ 非项目成员的存在性隐藏
// (404)——与前两者的失败发生在任何指标行被读出之前。

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

func (a metricsErrApp) ReadRuleSetMetrics(context.Context, Actor, string, RuleSetMetricsQuery) (RuleSetMetricsSnapshot, error) {
	return RuleSetMetricsSnapshot{}, a.err
}

func metricsHTTPFixture(t *testing.T, app Application) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, _ := openMetricsDatabase(t)
	if err := db.Exec("INSERT INTO lingdoc_projects (id) VALUES (?)", "project-metrics").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO lingdoc_members (project_id, user_id, role) VALUES (?, ?, ?)", "project-metrics", "admin-1", "owner").Error; err != nil {
		t.Fatal(err)
	}
	group := gin.New()
	NewHTTPHandler(app, testRequestInfo).Register(group, group)
	return group
}

func memberContext(userID, role string) context.Context {
	// http 层的身份来自 testRequestInfo:身份与角色都以测试键注入。
	return testIdentityContext(userID, role)
}

func TestG6MetricsEndpointFailsWithoutMetricsApplication(t *testing.T) {
	group := metricsHTTPFixture(t, &metricsMissingApp{})
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
	group := metricsHTTPFixture(t, &metricsErrApp{err: ErrRuleSetMetricsUnavailable})
	// 参数拒绝发生在 metrics 读取之前:即使 application 注入失败,也应是 400。
	for _, query := range []string{"?cursor=x", "?action=preflight", "?limit=5"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/projects/project-metrics/rule-set-use-audit-metrics"+query, nil).WithContext(testIdentityContext("admin-1", string(types.TenantRoleAdmin)))
		group.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("metrics %s = %d, want 400: %s", query, response.Code, response.Body.String())
		}
	}
}

func TestG6MetricsEndpointHidesTheProjectFromNonMembers(t *testing.T) {
	// 用真实 Store:读路径里的 authorizeAuditProject 提供存在性隐藏(404);
	// 注入失败的 stub 会把断言变成 503,测不到这一层。
	db, _ := openMetricsDatabase(t)
	store := NewStore(db, testAuthorizer{allow: true})
	group := metricsHTTPFixture(t, store)
	// 同租户但不是项目成员:authorizeAuditProject 存在性隐藏(404),
	// 不泄露项目与指标的存在。
	stranger := Actor{TenantID: 1, UserID: "not-a-member", Role: string(types.TenantRoleViewer)}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/projects/project-metrics/rule-set-use-audit-metrics", nil).WithContext(testIdentityContextFor(stranger))
	group.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("metrics for a non-member = %d, want 404: %s", response.Code, response.Body.String())
	}
}

// testIdentityContextFor 用指定 Actor 构造 http 层身份上下文。
func testIdentityContextFor(actor Actor) context.Context {
	ctx := context.WithValue(context.Background(), testIdentityKey{}, testIdentity{
		actor: actor, requestID: "metrics-failure-test", valid: true,
	})
	return ctx
}
