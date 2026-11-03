package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	lingdoctemplate "github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/gin-gonic/gin"
)

// httptestJSON 走一次真实 HTTP handler 并返回响应（change-sets 面的先例是
// change_set_http_test.go 的手写展开；这里收成一个小 helper 让用例读得动）。
func httptestJSON(t *testing.T, engine *gin.Engine, method, path, body, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(actorContext("reader"))
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

// #324（收窄后的剩余条件）：变更集应用**之后**，带必需路线节点的项目上交付检查
// 仍要能读回规则问题——不是「带必需路线节点的项目一律 403」。
//
// PR #336 修掉了票面点名的成因（溯源校验改用原始规格 SpecSource），那条修复的
// 回归用例（delivery_route_node_test.go）钉的是「应用前 checks 200」。本用例把
// 时间轴往前推一格：先 apply 一个变更集（项目被改写：context_revision、project
// version、spec 都可能动），再做同一件事，交付检查仍然不能被来源访问拒绝。
func TestDeliveryCheckAfterChangeSetApplyStillReadsTheRuleIssues(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db,
		deliveryQuestionChapter(assetID, currentAssetRevision(t, handler, assetID), deliveryBody, []string{deliverySourceID}),
		deliveryMethodChapter(),
	)
	seedRequiredRouteNodeTemplateCopy(t, db)
	if err := db.Exec("UPDATE lingdoc_projects SET current_context_revision = ? WHERE id = ?", 1, "project-1").Error; err != nil {
		t.Fatalf("seed context revision: %v", err)
	}

	// 交付路由与变更集路由挂在同一个 engine 上：两个 seam 都走真实 HTTP handler。
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	RegisterDeliveryRoutes(engine.Group("/api/v1/lingdoc"), NewDeliveryHandler(newDeliveryReleaseService(t, handler)))

	projectVersion := func(t *testing.T) int64 {
		t.Helper()
		var version int64
		if err := db.Raw("SELECT project_version FROM lingdoc_projects WHERE id = ?", "project-1").Scan(&version).Error; err != nil {
			t.Fatalf("read project version: %v", err)
		}
		return version
	}
	checkBody := func() string {
		return fmt.Sprintf(`{"expected_project_version":%d}`, projectVersion(t))
	}
	// applyChangeSet 走真实 HTTP 面创建并应用一次变更集，返回应用后的响应。
	applyChangeSet := func(t *testing.T, expectedContextRevision int, fields, reason, key string) {
		t.Helper()
		body := fmt.Sprintf(`{"expected_context_revision":%d,"fields":%s,"affected_chapter_ids":["chapter-1","chapter-2"],"reason":%q}`,
			expectedContextRevision, fields, reason)
		created := httptestJSON(t, engine, http.MethodPost, "/api/projects/project-1/change-sets", body, key+"-create")
		if created.Code != http.StatusCreated {
			t.Fatalf("create change set = %d, want 201: %s", created.Code, created.Body.String())
		}
		var change struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(created.Body.Bytes(), &change); err != nil {
			t.Fatalf("decode create response: %v", err)
		}
		applied := httptestJSON(t, engine, http.MethodPost, "/api/projects/project-1/change-sets/"+change.Data.ID+"/apply", `{}`, key+"-apply")
		if applied.Code != http.StatusOK {
			t.Fatalf("apply change set = %d, want 200: %s", applied.Code, applied.Body.String())
		}
	}

	// 基线：应用前 checks 200（PR #336 修好的那条语义，先钉住再往前走）。
	before := deliveryServe(engine, deliveryRequest(http.MethodPost, deliveryRouteBase+"/checks", checkBody(), ""))
	if before.Code != http.StatusOK {
		t.Fatalf("POST checks before apply = %d, want 200: %s", before.Code, before.Body.String())
	}

	// 创建并应用一个变更集：改写研究条件字段（spec 与项目状态随之推进）。
	applyChangeSet(t, 1, `{"research_subject":{"old_value":"公开的合成调查样本","new_value":"变更后的研究对象"}}`, "研究对象发生变化", "route-apply-1")

	// 第二次应用:再改写一个普通研究条件字段,逼近评论区的「多次变更集应用
	// 改写,规格与项目状态都动过」场景。(路线节点字段本身由路线图供给,变更集
	// 的模板 Spec 校验不允许直写它——那是设计,不是缺陷。)
	applyChangeSet(t, 2, `{"research_goal":{"old_value":"验证资料到章节的工作流程","new_value":"变更后再次改写的研究目标"}}`, "研究目标再次变化", "route-apply-2")

	// 完成线：应用后的交付检查仍返回 200，规则问题读得到。
	after := deliveryServe(engine, deliveryRequest(http.MethodPost, deliveryRouteBase+"/checks", checkBody(), ""))
	if after.Code != http.StatusOK {
		t.Fatalf("POST checks after apply = %d, want 200: %s", after.Code, after.Body.String())
	}
	var result delivery.CheckResult
	if err := json.Unmarshal(decodeDeliveryEnvelope(t, after).Data, &result); err != nil {
		t.Fatalf("decode check result: %v", err)
	}
	var route *delivery.CheckIssue
	for index := range result.Issues {
		if result.Issues[index].RuleID == lingdoctemplate.RuleRouteNodeRequired {
			route = &result.Issues[index]
			break
		}
	}
	if route == nil {
		t.Fatalf("check issues = %+v, want a blocking route node issue", result.Issues)
	}
	if route.Severity != delivery.SeverityBlocking {
		t.Fatalf("route node issue = %+v, want a blocking issue", route)
	}
	if route.TargetID != lingdoctemplate.DemoRequiredRouteNodeID {
		t.Fatalf("route node issue = %+v, want the target %s", route, lingdoctemplate.DemoRequiredRouteNodeID)
	}
}
