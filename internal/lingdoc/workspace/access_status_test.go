package workspace

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/gin-gonic/gin"
)

// getAccessStatus 用某个调用者的身份问一次 access-status，交回状态码、**原始** JSON 与
// 解出来的对象。
//
// 原始 JSON 是必须的：这条用例要断言的正是「空集合被序列化成 [] 而不是 null」，
// 而解成 any 之后两者都变成「长度 0」，差别恰好被抹掉。
func getAccessStatus(t *testing.T, router *gin.Engine, userID string) (int, string, map[string]any) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/projects/project-1/access-status", nil).
		WithContext(policyContextWithUser(userID))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)

	decoded := map[string]any{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode access-status: %v (%s)", err, recorder.Body.String())
	}
	return recorder.Code, recorder.Body.String(), decoded
}

func accessStatusData(t *testing.T, raw string, body map[string]any) map[string]any {
	t.Helper()
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("access-status has no data object: %s", raw)
	}
	return data
}

// TestAccessStatusReportsContentAccess 走完三态：可用 / 受限 / 未知。判据一律来自资料
// 网关（ResolveAllowed 的 Denied），不在传输层另造一套。
func TestAccessStatusReportsContentAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, db := newSourceCurrentnessHandler(t)
	authorizer := &flipTestAssetAuthorizer{allow: true}
	handler.gateway = evidence.NewAssetGateway(handler.bindings, authorizer)
	router := gin.New()
	router.GET("/projects/:projectId/access-status", handler.accessStatus)

	// 一份资料都没绑：没有可拒绝的东西，就是可用。
	status, raw, body := getAccessStatus(t, router, "reader")
	if status != http.StatusOK {
		t.Fatalf("unbound project: %d %s", status, raw)
	}
	if access := accessStatusData(t, raw, body)["content_access"]; access != "available" {
		t.Fatalf("unbound project answered %v: %s", access, raw)
	}
	// 契约里 recovery_actions 是 array：nil 会序列化成 null，类型就不对了。
	if !strings.Contains(raw, `"recovery_actions":[]`) {
		t.Fatalf("empty recovery actions must be []: %s", raw)
	}
	if create, ok := accessStatusData(t, raw, body)["can_create_project"].(bool); !ok || !create {
		t.Fatalf("can_create_project must be true for a member: %s", raw)
	}

	// 绑定一份当前可用的资料：还是可用。
	bindTestSource(t, db, handler.bindings, "project-1")
	status, raw, body = getAccessStatus(t, router, "reader")
	if status != http.StatusOK {
		t.Fatalf("bound project: %d %s", status, raw)
	}
	if access := accessStatusData(t, raw, body)["content_access"]; access != "available" {
		t.Fatalf("bound project answered %v: %s", access, raw)
	}

	// 撤权：受限，且恢复动作恰好是契约那两条枚举，顺序也对上 example。
	authorizer.allow = false
	status, raw, body = getAccessStatus(t, router, "reader")
	if status != http.StatusOK {
		t.Fatalf("revoked project: %d %s", status, raw)
	}
	data := accessStatusData(t, raw, body)
	if access := data["content_access"]; access != "restricted" {
		t.Fatalf("revoked project answered %v: %s", access, raw)
	}
	if actions, ok := data["recovery_actions"].([]any); !ok || len(actions) != 2 ||
		actions[0] != "restore_source_authorization" || actions[1] != "create_clean_project" {
		t.Fatalf("revoked project offered %v: %s", data["recovery_actions"], raw)
	}

	// 网关自己也答不出来：unknown 兜所有错误。绝不能 swallow 成 available——
	// 「不知道」与「没问题」在界面上是两件事，前者让人多看一眼，后者直接放行。
	authorizer.verdict = errors.New("asset gateway is down")
	status, raw, body = getAccessStatus(t, router, "reader")
	if status != http.StatusOK {
		t.Fatalf("failing gateway: %d %s", status, raw)
	}
	data = accessStatusData(t, raw, body)
	if access := data["content_access"]; access != "unknown" {
		t.Fatalf("failing gateway answered %v: %s", access, raw)
	}
	if !strings.Contains(raw, `"recovery_actions":[]`) {
		t.Fatalf("unknown must not offer recovery actions: %s", raw)
	}
}

// TestAccessStatusHidesTheProjectFromNonMembers：非成员答 404，且响应体里不出现
// content_access。§8 要求非成员仍然看不到项目的存在性，而一个「项目存在、内容是受限的」
// 的回答本身就是一次泄露。
func TestAccessStatusHidesTheProjectFromNonMembers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _ := newSourceCurrentnessHandler(t)
	authorizer := &flipTestAssetAuthorizer{allow: false}
	handler.gateway = evidence.NewAssetGateway(handler.bindings, authorizer)
	router := gin.New()
	router.GET("/projects/:projectId/access-status", handler.accessStatus)

	// outsider 是本租户的 active 成员，但不是这个项目的成员：这正是「不该看到」的那一类。
	if err := handler.db.Exec("INSERT INTO tenant_members (tenant_id, user_id, status) VALUES (?, ?, ?)",
		7, "outsider", "active").Error; err != nil {
		t.Fatalf("seed outsider: %v", err)
	}

	status, raw, body := getAccessStatus(t, router, "outsider")
	if status != http.StatusNotFound {
		t.Fatalf("non-member got %d: %s", status, raw)
	}
	if strings.Contains(raw, "content_access") {
		t.Fatalf("the refusal leaked project state: %s", raw)
	}
	if _, present := body["data"]; present {
		t.Fatalf("a refusal must not carry data: %s", raw)
	}
}
