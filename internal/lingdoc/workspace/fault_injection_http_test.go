package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 受控故障注入的 HTTP seam 实跑（#256 / AC-G3-03）。
//
// 这些用例走真实 HTTP 路由（POST /projects/{id}/change-sets/{id}/apply），证明
// 组合根装配的注入在产品面上真的发生：apply 失败、失败可辨认为注入、并且项目
// revision、模板引用、ImpactTask 集合与业务审计都保持原样。
//
// 注入本身不是产品端点：这里没有任何新增路由，注入由组合根按窄 port 装配。

// newFaultInjectionHandler 装配一个带受控故障注入的 workspace handler。
// 这是组合根在测试里的等价物：默认组合（fault 为零值）与注入组合走同一条路。
func newFaultInjectionHandler(t *testing.T, db *gorm.DB, fault core.FaultInjection) (*Handler, *core.Service) {
	t.Helper()
	runtime := NewGORMSourceRuntime(db, nil, nil, nil, delivery.NewFixedTemplateReader())
	var policy core.AuthorizationPolicy
	if fault.Point == core.FaultPointAuthorization {
		policy = core.NewFaultInjectingAuthorizationPolicy(nil, fault)
	}
	service := core.NewServiceWithAuthorizationPolicy(core.NewGORMRepository(db), core.ContractDemoTemplate{}, runtime.WorkspaceSourcePolicy(), policy, nil, core.AuthorizationModeEnforce)
	service.InstallFaultInjection(fault)
	runtime.ConnectProjects(service)
	handler, err := NewHandler(HandlerDependencies{Read: service, Write: service, Sources: runtime, Integration: runtime})
	if err != nil {
		t.Fatalf("assemble the workspace handler: %v", err)
	}
	testRuntime(handler).gateway = evidence.NewAssetGateway(testRuntime(handler).bindings, allowTestAssetAuthorizer{})
	return handler, service
}

// faultInjectionFixture 落一个可应用变更集的工作区，并返回装配好的 handler。
func faultInjectionFixture(t *testing.T, fault core.FaultInjection) (*Handler, *core.Service, *gorm.DB) {
	t.Helper()
	db := newSourceCurrentnessDB(t)
	handler, service := newFaultInjectionHandler(t, db, fault)
	assetID := seedBoundSourceOn(t, handler, db)
	seedDeliveryWorkspace(t, db,
		deliveryQuestionChapter(assetID, currentAssetRevision(t, handler, assetID), deliveryBody, []string{deliverySourceID}),
		deliveryMethodChapter(),
	)
	ensureImpactRouteTables(t, db)
	seedDeliveryBoundTemplateCopy(t, db, "fault-http-copy")
	return handler, service, db
}

func faultInjectionEngine(handler *Handler, service *core.Service) *gin.Engine {
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	RegisterImpactRoutes(engine.Group("/api"), service)
	return engine
}

// faultInjectionState 是失败后必须保持不变的那组事实。
type faultInjectionState struct {
	ContextRevision     int64
	ProjectVersion      int64
	SpecRevision        int64
	TemplateID          string
	TemplateVersion     string
	TemplateCopyVersion int64
	ImpactTaskCount     int
	ApplyAuditCount     int
}

func readFaultInjectionState(t *testing.T, engine *gin.Engine) faultInjectionState {
	t.Helper()
	state := faultInjectionState{}
	request := httptest.NewRequest(http.MethodGet, "/api/projects/project-1", nil).WithContext(actorContext("reader"))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("read project status = %d; body=%s", response.Code, response.Body.String())
	}
	var project struct {
		Data struct {
			CurrentContextRevision int64  `json:"current_context_revision"`
			ProjectVersion         int64  `json:"project_version"`
			SpecRevision           int64  `json:"spec_revision"`
			TemplateID             string `json:"template_id"`
			TemplateVersion        string `json:"template_version"`
			TemplateCopyVersion    int64  `json:"template_copy_version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &project); err != nil {
		t.Fatalf("decode project response: %v", err)
	}
	state.ContextRevision = project.Data.CurrentContextRevision
	state.ProjectVersion = project.Data.ProjectVersion
	state.SpecRevision = project.Data.SpecRevision
	state.TemplateID = project.Data.TemplateID
	state.TemplateVersion = project.Data.TemplateVersion
	state.TemplateCopyVersion = project.Data.TemplateCopyVersion

	request = httptest.NewRequest(http.MethodGet, "/api/projects/project-1/impact-tasks", nil).WithContext(actorContext("reader"))
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("read impact tasks status = %d; body=%s", response.Code, response.Body.String())
	}
	var tasks struct {
		Data []core.ImpactTask `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &tasks); err != nil {
		t.Fatalf("decode impact tasks response: %v", err)
	}
	state.ImpactTaskCount = len(tasks.Data)

	request = httptest.NewRequest(http.MethodGet, "/api/projects/project-1/audit", nil).WithContext(actorContext("reader"))
	response = httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("read audit status = %d; body=%s", response.Code, response.Body.String())
	}
	var audit struct {
		Data []core.AuditEvent `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &audit); err != nil {
		t.Fatalf("decode audit response: %v", err)
	}
	for _, event := range audit.Data {
		if event.Action == "project.change_set.apply" {
			state.ApplyAuditCount++
		}
	}
	return state
}

// createAssessedChangeSet 通过 HTTP 建一条已评估的变更集。
func createAssessedChangeSet(t *testing.T, engine *gin.Engine, key string) string {
	t.Helper()
	body := `{"expected_context_revision":1,"fields":{"research_subject":{"old_value":"公开的合成调查样本","new_value":"新的研究对象"}},"affected_chapter_ids":["chapter-1","chapter-2"],"reason":"研究对象发生变化"}`
	request := httptest.NewRequest(http.MethodPost, "/api/projects/project-1/change-sets", strings.NewReader(body)).WithContext(actorContext("reader"))
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create change set status = %d; body=%s", response.Code, response.Body.String())
	}
	var created struct {
		Data ChangeSet `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Data.Status != "assessed" {
		t.Fatalf("change set status = %q, want assessed", created.Data.Status)
	}
	return created.Data.ID
}

// readChangeSetStatus 读回变更集自身的状态：失败的 apply 不能把它留在 applied。
func readChangeSetStatus(t *testing.T, engine *gin.Engine, changeSetID string) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/projects/project-1/change-sets/"+changeSetID, nil).WithContext(actorContext("reader"))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("read change set status = %d; body=%s", response.Code, response.Body.String())
	}
	var fetched struct {
		Data ChangeSet `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode change set response: %v", err)
	}
	return fetched.Data.Status
}

// applyChangeSetOverHTTP 走真实 apply 路由并返回状态码与响应体。
func applyChangeSetOverHTTP(t *testing.T, engine *gin.Engine, changeSetID, key string) (int, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/projects/project-1/change-sets/"+changeSetID+"/apply", strings.NewReader(`{}`)).WithContext(actorContext("reader"))
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response.Code, response.Body.String()
}

func assertInjectedFaultResponse(t *testing.T, status int, body string) {
	t.Helper()
	if status != http.StatusServiceUnavailable {
		t.Fatalf("apply status = %d, want 503; body=%s", status, body)
	}
	var failure struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &failure); err != nil {
		t.Fatalf("decode apply failure: %v", err)
	}
	if failure.Error.Code != "injected_fault" {
		t.Fatalf("apply failure code = %q, want injected_fault; body=%s", failure.Error.Code, body)
	}
	if !strings.Contains(failure.Error.Message, "受控故障注入") {
		t.Fatalf("apply failure message does not name the injected failure: %s", failure.Error.Message)
	}
}

func TestApplyFailsWithInjectedImpactTaskWriteAndRollsBack(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, service, _ := faultInjectionFixture(t, core.FaultInjection{
		Point:    core.FaultPointImpactTaskWrite,
		TenantID: 7,
		Reason:   "合成注入：ImpactTask 写入不可用",
	})
	engine := faultInjectionEngine(handler, service)
	changeSetID := createAssessedChangeSet(t, engine, "fault-http-create-task")
	before := readFaultInjectionState(t, engine)

	status, body := applyChangeSetOverHTTP(t, engine, changeSetID, "fault-http-apply-task")
	assertInjectedFaultResponse(t, status, body)

	after := readFaultInjectionState(t, engine)
	if after.ContextRevision != before.ContextRevision {
		t.Fatalf("失败的应用推进了上下文修订: %d -> %d", before.ContextRevision, after.ContextRevision)
	}
	if after.ProjectVersion != before.ProjectVersion || after.SpecRevision != before.SpecRevision {
		t.Fatalf("失败的应用推进了项目或规格修订: %+v -> %+v", before, after)
	}
	if after.TemplateID != before.TemplateID || after.TemplateVersion != before.TemplateVersion || after.TemplateCopyVersion != before.TemplateCopyVersion {
		t.Fatalf("失败的应用改动了模板引用: %+v -> %+v", before, after)
	}
	if after.ImpactTaskCount != before.ImpactTaskCount {
		t.Fatalf("失败的应用留下了 ImpactTask: %d -> %d", before.ImpactTaskCount, after.ImpactTaskCount)
	}
	if after.ApplyAuditCount != before.ApplyAuditCount {
		t.Fatalf("失败的应用留下了业务审计事件: %d -> %d", before.ApplyAuditCount, after.ApplyAuditCount)
	}
}

func TestApplyFailsWithInjectedAuditWriteAndRollsBack(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, service, _ := faultInjectionFixture(t, core.FaultInjection{
		Point:    core.FaultPointAuditWrite,
		TenantID: 7,
		Action:   "project.change_set.apply",
		Reason:   "合成注入：业务审计写入不可用",
	})
	engine := faultInjectionEngine(handler, service)
	changeSetID := createAssessedChangeSet(t, engine, "fault-http-create-audit")
	before := readFaultInjectionState(t, engine)

	status, body := applyChangeSetOverHTTP(t, engine, changeSetID, "fault-http-apply-audit")
	assertInjectedFaultResponse(t, status, body)

	after := readFaultInjectionState(t, engine)
	if after.ContextRevision != before.ContextRevision || after.ProjectVersion != before.ProjectVersion || after.SpecRevision != before.SpecRevision {
		t.Fatalf("审计写失败推进了修订: %+v -> %+v", before, after)
	}
	if after.TemplateCopyVersion != before.TemplateCopyVersion {
		t.Fatalf("审计写失败改动了模板引用: %d -> %d", before.TemplateCopyVersion, after.TemplateCopyVersion)
	}
	if after.ImpactTaskCount != before.ImpactTaskCount {
		t.Fatalf("审计写失败留下了 ImpactTask: %d -> %d", before.ImpactTaskCount, after.ImpactTaskCount)
	}
	if after.ApplyAuditCount != before.ApplyAuditCount {
		t.Fatalf("审计写失败留下了业务审计事件: %d -> %d", before.ApplyAuditCount, after.ApplyAuditCount)
	}
	// 业务审计是 apply 事务的最后一次写入：变更集本身在失败前已经被写成
	// applied，所以这里能读到 assessed，说明回滚不是空转。
	if status := readChangeSetStatus(t, engine, changeSetID); status == "applied" {
		t.Fatalf("审计写失败后变更集仍是 applied：回滚没有生效")
	}
}

func TestApplyFailsWithInjectedAuthorizationLapseAndKeepsState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 撤权场景：演示租户的 manage 能力在授权判定处失效。变更集的创建只需要
	// write 能力，因此它仍然可以被建出来；apply 需要 manage，于是失败。
	handler, service, _ := faultInjectionFixture(t, core.FaultInjection{
		Point:    core.FaultPointAuthorization,
		TenantID: 7,
		Action:   "manage",
		Reason:   "合成注入：授权失效",
	})
	engine := faultInjectionEngine(handler, service)
	changeSetID := createAssessedChangeSet(t, engine, "fault-http-create-auth")
	before := readFaultInjectionState(t, engine)

	status, body := applyChangeSetOverHTTP(t, engine, changeSetID, "fault-http-apply-auth")
	assertInjectedFaultResponse(t, status, body)

	after := readFaultInjectionState(t, engine)
	if after.ContextRevision != before.ContextRevision || after.ProjectVersion != before.ProjectVersion || after.SpecRevision != before.SpecRevision {
		t.Fatalf("授权失效推进了修订: %+v -> %+v", before, after)
	}
	if after.TemplateCopyVersion != before.TemplateCopyVersion {
		t.Fatalf("授权失效改动了模板引用: %d -> %d", before.TemplateCopyVersion, after.TemplateCopyVersion)
	}
	if after.ImpactTaskCount != before.ImpactTaskCount {
		t.Fatalf("授权失效留下了 ImpactTask: %d -> %d", before.ImpactTaskCount, after.ImpactTaskCount)
	}
	if after.ApplyAuditCount != before.ApplyAuditCount {
		t.Fatalf("授权失效留下了业务审计事件: %d -> %d", before.ApplyAuditCount, after.ApplyAuditCount)
	}
}

func TestApplySucceedsWhenNoFaultIsInstalled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 开关默认关闭：不装配任何注入时，apply 与本票之前完全一致。
	handler, service, _ := faultInjectionFixture(t, core.FaultInjection{})
	engine := faultInjectionEngine(handler, service)
	changeSetID := createAssessedChangeSet(t, engine, "fault-http-create-off")
	before := readFaultInjectionState(t, engine)

	status, body := applyChangeSetOverHTTP(t, engine, changeSetID, "fault-http-apply-off")
	if status != http.StatusOK {
		t.Fatalf("未开启注入时 apply 失败: status=%d body=%s", status, body)
	}

	after := readFaultInjectionState(t, engine)
	if after.ContextRevision != before.ContextRevision+1 {
		t.Fatalf("未开启注入时 apply 没有推进一次上下文修订: %d -> %d", before.ContextRevision, after.ContextRevision)
	}
	if after.ImpactTaskCount <= before.ImpactTaskCount {
		t.Fatalf("未开启注入时 apply 没有创建 ImpactTask: %d -> %d", before.ImpactTaskCount, after.ImpactTaskCount)
	}
	if after.ApplyAuditCount != before.ApplyAuditCount+1 {
		t.Fatalf("未开启注入时 apply 没有写下业务审计事件: %d -> %d", before.ApplyAuditCount, after.ApplyAuditCount)
	}
}

func TestFaultInjectionDoesNotReachAnotherTenant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// 注入只对被点名的租户生效：这里点名的是另一个租户，因此本项目不受影响。
	handler, service, _ := faultInjectionFixture(t, core.FaultInjection{
		Point:    core.FaultPointImpactTaskWrite,
		TenantID: 8,
		Reason:   "合成注入：另一个租户",
	})
	engine := faultInjectionEngine(handler, service)
	changeSetID := createAssessedChangeSet(t, engine, "fault-http-create-other")

	status, body := applyChangeSetOverHTTP(t, engine, changeSetID, "fault-http-apply-other")
	if status != http.StatusOK {
		t.Fatalf("非演示租户被注入了: status=%d body=%s", status, body)
	}
}
