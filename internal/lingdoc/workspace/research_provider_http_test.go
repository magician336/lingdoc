package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// research-task 指定不存在的 provider_id 必须在真实 HTTP 面上得到可读的 4xx
// （#306）。
//
// 这里走的是生产组合：真实路由、真实 core service、真实 G2ResearchAdapter、
// 真实 SQLite 库。缺陷的原样是 provider 行查不到时 gorm 的 ErrRecordNotFound
// 直接漏出，共享错误映射表不命中，于是落到 500 internal_error。

// newResearchProviderFixture 装出 research-task 路由的真实组合。
//
// seedTenantRow=false 用来覆盖「租户配置行缺失」那一条：provider 查得到，
// 租户行查不到，两者是同一族的未映射「记录未找到」。
func newResearchProviderFixture(t *testing.T, seedTenantRow bool) (*gin.Engine, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := newSourceCurrentnessDB(t)
	ensureImpactRouteTables(t, db)
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS tenants (id INTEGER PRIMARY KEY, web_search_config BLOB, deleted_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS web_search_providers (id TEXT PRIMARY KEY, tenant_id INTEGER NOT NULL, provider TEXT, updated_at DATETIME, deleted_at DATETIME)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatalf("建检索来源表失败：%v", err)
		}
	}
	if seedTenantRow {
		if err := db.Exec("INSERT INTO tenants (id, web_search_config) VALUES (?, ?)", 7, []byte(`{"max_results":3}`)).Error; err != nil {
			t.Fatalf("种入租户配置行失败：%v", err)
		}
	}
	// 当前租户的检索来源：合法，用来把「provider 查得到、租户行查不到」这一条
	// 与「provider 查不到」那条分开。
	if err := db.Exec("INSERT INTO web_search_providers (id, tenant_id, provider, updated_at) VALUES (?, ?, ?, ?)",
		"own-provider", 7, "bing", time.Date(2026, time.October, 20, 0, 0, 0, 0, time.UTC)).Error; err != nil {
		t.Fatalf("种入本租户检索来源失败：%v", err)
	}
	// 另一个租户的检索来源：它存在，但不属于当前租户。
	if err := db.Exec("INSERT INTO web_search_providers (id, tenant_id, provider, updated_at) VALUES (?, ?, ?, ?)",
		"other-tenant-provider", 8, "bing", time.Date(2026, time.October, 20, 0, 0, 0, 0, time.UTC)).Error; err != nil {
		t.Fatalf("种入他租户检索来源失败：%v", err)
	}

	runtime := NewGORMSourceRuntime(db, ProjectPorts{}, nil, nil, delivery.NewFixedTemplateReader())
	projects := core.NewServiceWithSources(core.NewGORMRepository(db), core.ContractDemoTemplate{}, runtime.WorkspaceSourcePolicy())
	runtime.ConnectProjects(ProjectPorts{Authorizer: projects, Projects: projects, Context: projects})
	runtime.EnableFullWorkflowPolicy()
	adapter := NewG2ResearchAdapter(db, projects, runtime, nil, nil, nil)
	// 生产组合根把同一个适配器同时装成 Sources 与 Research；只有 Research
	// 走的是目录版本这条路径（core.G2Runtime.Research 的 G2ResearchCatalog）。
	projects.ConfigureG2(core.G2Runtime{Sources: adapter, Research: adapter})

	engine := gin.New()
	RegisterG2FullRoutes(engine.Group("/api"), G2RoutePorts{
		Research:      projects,
		Imports:       projects,
		EvidenceCards: projects,
		Claims:        projects,
		Anchors:       projects,
		AgentSessions: projects,
		Proposals:     projects,
		Context:       projects,
		ChangeSets:    projects,
	})
	return engine, db
}

// researchTaskError 是调用方看到的那一份对外形态。
type researchTaskError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

// postResearchTask 走真实路由保存一条研究任务，返回状态码与错误信封。
func postResearchTask(t *testing.T, engine *gin.Engine, key, body string) (int, researchTaskError, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/projects/project-1/research-tasks", strings.NewReader(body)).WithContext(actorContext("reader"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	envelope := struct {
		Error researchTaskError `json:"error"`
	}{}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		// 响应体不是错误信封时保留原文，便于断言时给出真实对照。
		return response.Code, researchTaskError{}, response.Body.String()
	}
	return response.Code, envelope.Error, response.Body.String()
}

// TestSaveResearchTaskWithUnknownProviderIsRejectedWithReadableClientError
// 断言：不存在的 provider_id 得到 4xx，且错误码可读，不是 500 internal_error。
func TestSaveResearchTaskWithUnknownProviderIsRejectedWithReadableClientError(t *testing.T) {
	engine, _ := newResearchProviderFixture(t, true)
	status, failure, body := postResearchTask(t, engine, "provider-unknown",
		`{"goal":"合成研究目标","queries":["定义","限制"],"expected_project_version":1,"expected_context_revision":0,"provider_id":"missing-provider"}`)

	if status == http.StatusInternalServerError {
		t.Fatalf("不存在的 provider_id 落到了 500：status=%d body=%s", status, body)
	}
	if status < 400 || status >= 500 {
		t.Fatalf("不存在的 provider_id 状态码 = %d，期望 4xx；body=%s", status, body)
	}
	if failure.Code == "" || failure.Code == "internal_error" {
		t.Fatalf("不存在的 provider_id 错误码不可读：code=%q body=%s", failure.Code, body)
	}
	if !strings.Contains(failure.Message, "provider_id") {
		t.Fatalf("错误文案没有点明 provider_id：message=%q", failure.Message)
	}
}

// TestSaveResearchTaskWithAnotherTenantProviderIsIndistinguishableFromUnknown
// 断言：属于别的租户的 provider_id 与不存在的 provider_id 给同一个对外形态，
// 不能用响应去探测他租户的检索来源目录。
func TestSaveResearchTaskWithAnotherTenantProviderIsIndistinguishableFromUnknown(t *testing.T) {
	engine, _ := newResearchProviderFixture(t, true)
	unknownStatus, unknownFailure, unknownBody := postResearchTask(t, engine, "provider-unknown-compare",
		`{"goal":"合成研究目标","queries":["定义","限制"],"expected_project_version":1,"expected_context_revision":0,"provider_id":"missing-provider"}`)
	otherStatus, otherFailure, otherBody := postResearchTask(t, engine, "provider-other-tenant",
		`{"goal":"合成研究目标","queries":["定义","限制"],"expected_project_version":1,"expected_context_revision":0,"provider_id":"other-tenant-provider"}`)

	if otherStatus != unknownStatus {
		t.Fatalf("他租户 provider 的状态码与不存在的不同：%d vs %d（unknown body=%s other body=%s）", otherStatus, unknownStatus, unknownBody, otherBody)
	}
	if otherFailure != unknownFailure {
		t.Fatalf("他租户 provider 的对外形态与不存在的不同：%+v vs %+v（unknown body=%s other body=%s）", otherFailure, unknownFailure, unknownBody, otherBody)
	}
	if otherStatus == http.StatusInternalServerError {
		t.Fatalf("他租户 provider 落到了 500：status=%d body=%s", otherStatus, otherBody)
	}
}

// TestSaveResearchTaskWithoutProviderOrAssetsKeepsInvalidRequest 断言既有行为
// 没有被改动：既不给资料也不给检索来源时，仍然是 400 invalid_request。
func TestSaveResearchTaskWithoutProviderOrAssetsKeepsInvalidRequest(t *testing.T) {
	engine, _ := newResearchProviderFixture(t, true)
	status, failure, body := postResearchTask(t, engine, "provider-empty",
		`{"goal":"合成研究目标","queries":["定义","限制"],"expected_project_version":1,"expected_context_revision":0}`)

	if status != http.StatusBadRequest {
		t.Fatalf("既无资料也无检索来源的状态码 = %d，期望 400；body=%s", status, body)
	}
	if failure.Code != "invalid_request" {
		t.Fatalf("既无资料也无检索来源的错误码 = %q，期望 invalid_request；body=%s", failure.Code, body)
	}
}

// TestSaveResearchTaskWithMissingTenantRowIsNotAnInternalError 断言同一族的另一
// 处「记录未找到」（租户配置行缺失）也不再落成 500 internal_error。
func TestSaveResearchTaskWithMissingTenantRowIsNotAnInternalError(t *testing.T) {
	engine, _ := newResearchProviderFixture(t, false)
	status, failure, body := postResearchTask(t, engine, "provider-no-tenant-row",
		`{"goal":"合成研究目标","queries":["定义","限制"],"expected_project_version":1,"expected_context_revision":0,"provider_id":"own-provider"}`)

	if status == http.StatusInternalServerError {
		t.Fatalf("租户配置行缺失落到了 500：status=%d body=%s", status, body)
	}
	if failure.Code == "" || failure.Code == "internal_error" {
		t.Fatalf("租户配置行缺失的错误码不可读：code=%q body=%s", failure.Code, body)
	}
}
