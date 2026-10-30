package workspace

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/academic/crossref"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// research-task 指定不存在的 provider_id 必须在真实 HTTP 面上得到可读的 4xx
// （#306）。
//
// 这里走的是生产组合：真实路由、真实 core service、真实 ResearchAdapter、
// 真实 SQLite 库。缺陷的原样是 provider 行查不到时 gorm 的 ErrRecordNotFound
// 直接漏出，共享错误映射表不命中，于是落到 500 internal_error。

// newResearchProviderFixture 装出 research-task 路由的真实组合。
//
// seedTenantRow=false 用来覆盖「租户配置行缺失」那一条：provider 查得到，
// 租户行查不到，两者是同一族的未映射「记录未找到」。
func newResearchProviderFixture(t *testing.T, seedTenantRow bool) (*gin.Engine, *gorm.DB, *ResearchAdapter) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db := newSourceCurrentnessDB(t)
	ensureImpactRouteTables(t, db)
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS tenants (id INTEGER PRIMARY KEY, web_search_config BLOB, deleted_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS web_search_providers (id TEXT PRIMARY KEY, tenant_id INTEGER NOT NULL, provider TEXT, updated_at DATETIME, deleted_at DATETIME)`,
		`CREATE TABLE IF NOT EXISTS lingdoc_g2_objects (project_id TEXT NOT NULL, kind TEXT NOT NULL, id TEXT NOT NULL, version INTEGER NOT NULL, payload_json TEXT NOT NULL, PRIMARY KEY (project_id, kind, id))`,
		`CREATE TABLE IF NOT EXISTS lingdoc_g2_object_versions (project_id TEXT NOT NULL, kind TEXT NOT NULL, id TEXT NOT NULL, version INTEGER NOT NULL, payload_json TEXT NOT NULL, PRIMARY KEY (project_id, kind, id, version))`,
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

	runtime := NewGORMSourceRuntime(db, ProjectPorts{}, stubKBShareService{}, nil, delivery.NewFixedTemplateReader())
	projects := core.NewServiceWithSources(core.NewGORMRepository(db), core.ContractDemoTemplate{}, runtime.WorkspaceSourcePolicy())
	runtime.ConnectProjects(ProjectPorts{Authorizer: projects, Resources: projects, Projects: projects, Context: projects})
	runtime.EnableFullWorkflowPolicy()
	adapter := NewResearchAdapter(db, projects, runtime, nil, nil, nil, WithAcademicSourceCatalogReader(projects))
	// 生产组合根把同一个适配器同时装成 Sources 与 Research；只有 Research
	// 走的是目录版本这条路径（core.AgentRuntime.Research 的 ResearchCatalog）。
	projects.ConfigureAgentRuntime(core.AgentRuntime{Sources: adapter, Research: adapter})

	engine := gin.New()
	RegisterAgentRoutes(engine.Group("/api"), AgentRoutePorts{
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
	return engine, db, adapter
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
	engine, _, _ := newResearchProviderFixture(t, true)
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
	engine, _, _ := newResearchProviderFixture(t, true)
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
	engine, _, _ := newResearchProviderFixture(t, true)
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
	engine, _, _ := newResearchProviderFixture(t, false)
	status, failure, body := postResearchTask(t, engine, "provider-no-tenant-row",
		`{"goal":"合成研究目标","queries":["定义","限制"],"expected_project_version":1,"expected_context_revision":0,"provider_id":"own-provider"}`)

	if status == http.StatusInternalServerError {
		t.Fatalf("租户配置行缺失落到了 500：status=%d body=%s", status, body)
	}
	if failure.Code == "" || failure.Code == "internal_error" {
		t.Fatalf("租户配置行缺失的错误码不可读：code=%q body=%s", failure.Code, body)
	}
}

// TestCrossrefResearchWorkflowThroughHTTPAndSQLite follows the public G2
// routes, production ResearchAdapter, Crossref's JSON contract, and persisted
// run/candidate readback. Crossref's HTTP transport is synthetic and
// in-process, so the test does not claim a live provider or browser acceptance.
func TestCrossrefResearchWorkflowThroughHTTPAndSQLite(t *testing.T) {
	engine, db, adapter := newResearchProviderFixture(t, true)
	pool, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	// Keep the source transport and approved policy synthetic, but exercise the
	// real quota implementation in the same production Workspace transaction.
	now := time.Now().UTC()
	policy := &httpResearchQuotaPolicy{core.ResearchQuotaPolicy{Version: "synthetic-http-v1", ApprovedBy: "synthetic-fixture-only", WindowID: "synthetic-http-window", StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), Limit: core.ResearchQuotaAmount{Searches: 2, Results: 40}}}
	ledger := core.NewResearchQuotaLedger(adapter.projects.(*core.Service), policy)
	projectExpectation := func() string {
		t.Helper()
		var current struct {
			ProjectVersion int64 `gorm:"column:project_version"`
		}
		if err := db.Table("lingdoc_projects").Select("project_version").Where("id = ?", "project-1").Take(&current).Error; err != nil {
			t.Fatalf("read project version for the next operation: %v", err)
		}
		return `"expected_project_version":` + strconv.FormatInt(current.ProjectVersion, 10)
	}
	var mu sync.Mutex
	queries := make([]string, 0, 2)
	filters := make([]string, 0, 2)
	transport := adapterRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.crossref.org" || r.URL.Path != "/works" {
			t.Errorf("Crossref endpoint = %s, want api.crossref.org/works", r.URL)
		}
		mu.Lock()
		queries = append(queries, r.URL.Query().Get("query.bibliographic"))
		filters = append(filters, r.URL.Query().Get("filter"))
		mu.Unlock()
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"status":"ok","message":{"items":[{"DOI":"10.5555/integration.1","title":["A controlled metadata record"],"author":[{"given":"Ada","family":"Lovelace"}],"published-print":{"date-parts":[[2024,3,9]]},"container-title":["Journal of Integration Tests"],"ISSN":["0378-5955"],"type":"journal-article","license":[{"URL":"https://creativecommons.org/licenses/by/4.0/"}] }]}}`)), Request: r}, nil
	})
	adapter.academic = crossref.Client{HTTPClient: &http.Client{Transport: transport}, Timeout: 2 * time.Second}
	if err := adapter.ConfigureAcademicResearchPolicy(syntheticGlobalAcademicCatalog(), ledger); err != nil {
		t.Fatalf("configure synthetic-only global policy and budget for HTTP integration: %v", err)
	}

	request := func(method, path, key, body string) *httptest.ResponseRecorder {
		t.Helper()
		ctx, cancel := context.WithTimeout(actorContext("reader"), 2*time.Second)
		defer cancel()
		req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, req)
		return response
	}
	readData := func(response *httptest.ResponseRecorder, wantStatus int, dst any) {
		t.Helper()
		if response.Code != wantStatus {
			t.Fatalf("HTTP status = %d, want %d: %s", response.Code, wantStatus, response.Body.String())
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode response envelope: %v; body=%s", err, response.Body.String())
		}
		if err := json.Unmarshal(envelope.Data, dst); err != nil {
			t.Fatalf("decode response data: %v; body=%s", err, response.Body.String())
		}
	}

	var catalog core.AcademicSourceCatalog
	readData(request(http.MethodPut, "/api/projects/project-1/academic-source-catalog", "crossref-catalog",
		`{"expected_project_version":1,"expected_context_revision":0,"expected_version":0,"rules":[{"id":"integration-journal","version":0,"source_type":"journal","venue_name":"Journal of Integration Tests","venue_ids":["0378-5955"],"allowed_license_urls":[]}]}`), http.StatusOK, &catalog)
	if catalog.Version != 1 || len(catalog.Rules) != 1 || catalog.Rules[0].Maintainer != "reader" || catalog.SourceCatalogVersion == "" {
		t.Fatalf("project catalog was not versioned and stamped with the authenticated maintainer: %+v", catalog)
	}

	var task core.ResearchTask
	readData(request(http.MethodPost, "/api/projects/project-1/research-tasks", "crossref-task",
		`{`+projectExpectation()+`,"expected_context_revision":0,"goal":"Verify controlled academic search","question_type":"source_discovery","scope":"metadata only","exclusions":[],"queries":["query first","query second"],"date_from":"2020-01-01","date_to":"2025-12-31","asset_ids":[],"provider_id":"academic:crossref","expected_output":"bounded metadata results"}`), http.StatusOK, &task)
	if task.ProviderID != core.AcademicCrossrefProviderID || task.DateFrom != "2020-01-01" || task.DateTo != "2025-12-31" {
		t.Fatalf("saved research plan did not preserve provider/date scope: %+v", task)
	}

	var approved core.ResearchTask
	readData(request(http.MethodPost, "/api/projects/project-1/research-tasks/"+task.ID+"/approve", "crossref-approve",
		`{`+projectExpectation()+`,"expected_context_revision":0,"expected_version":1,"confirmed":true,"reason":"approve the bounded metadata-only plan"}`), http.StatusOK, &approved)
	if approved.Status != "approved" || approved.Version != 2 {
		t.Fatalf("plan was not explicitly approved: %+v", approved)
	}

	var run core.ResearchRun
	readData(request(http.MethodPost, "/api/projects/project-1/research-tasks/"+task.ID+"/run", "crossref-run",
		`{`+projectExpectation()+`,"expected_context_revision":0,"expected_version":2}`), http.StatusOK, &run)
	if run.Status != "completed" || len(run.Rounds) != 2 || len(run.Results) != 2 || run.Usage.SearchesUsed != 2 || run.Usage.ResultsUsed != 2 {
		t.Fatalf("bounded Crossref run was not recorded as expected: status=%q failure=%q rounds=%+v results=%d usage=%+v", run.Status, run.FailureCode, run.Rounds, len(run.Results), run.Usage)
	}
	snapshot, quotaErr := ledger.Snapshot(context.Background(), core.Actor{TenantID: 7, UserID: "reader", Role: types.TenantRoleAdmin}, "project-1")
	if quotaErr != nil || snapshot.Charged != (core.ResearchQuotaAmount{Searches: 2, Results: 2}) || snapshot.Measured != snapshot.Charged {
		t.Fatalf("HTTP research and durable project quota disagree: snapshot=%+v err=%v", snapshot, quotaErr)
	}
	if run.Results[0].PolicyStatus != "approved" || run.Results[0].Status != "metadata_only" || run.Results[1].DuplicateStatus != "duplicate" || run.Results[1].Status != "duplicate" {
		t.Fatalf("allowlist decision or duplicate status was lost: %+v", run.Results)
	}
	mu.Lock()
	if len(queries) != 2 || queries[0] != "query first" || queries[1] != "query second" || len(filters) != 2 || filters[0] != "from-pub-date:2020-01-01,until-pub-date:2025-12-31" || filters[1] != filters[0] {
		mu.Unlock()
		t.Fatalf("Crossref queries/date filters = %v / %v", queries, filters)
	}
	mu.Unlock()

	var readBack core.ResearchRun
	readData(request(http.MethodGet, "/api/projects/project-1/research-runs/"+run.ID, "", ""), http.StatusOK, &readBack)
	if readBack.ID != run.ID || readBack.Basis.SourceCatalogVersion != run.Basis.SourceCatalogVersion || len(readBack.Results) != 2 {
		t.Fatalf("persisted run readback differs from HTTP result: %+v", readBack)
	}
	if run.Basis.SourceCatalogVersion == "" {
		t.Fatal("research run did not retain its source catalog basis")
	}
	var currentProject struct {
		ProjectVersion int64 `gorm:"column:project_version"`
	}
	if err := db.Table("lingdoc_projects").Select("project_version").Where("id = ?", "project-1").Take(&currentProject).Error; err != nil {
		t.Fatal(err)
	}
	if currentProject.ProjectVersion != run.Basis.ProjectVersion {
		t.Fatalf("project version after run = %d, run basis = %d", currentProject.ProjectVersion, run.Basis.ProjectVersion)
	}

	var candidate core.ImportCandidate
	candidateBody := `{` + projectExpectation() + `,"expected_context_revision":0,"run_id":"` + run.ID + `","result_id":"` + run.Results[0].ID + `","purpose":"retain bibliographic metadata for later human review","save_mode":"metadata_only","scope":"personal","model_use_allowed":false,"license":"Crossref metadata only"}`
	readData(request(http.MethodPost, "/api/projects/project-1/import-candidates", "crossref-candidate", candidateBody), http.StatusCreated, &candidate)
	if candidate.SelectionType != "academic_metadata" || candidate.DOI != "10.5555/integration.1" || candidate.URL != "" || candidate.Locator != "" || candidate.Status != "awaiting_consent" {
		t.Fatalf("Crossref result escaped metadata-only candidate boundary: %+v", candidate)
	}
	var replay core.ImportCandidate
	readData(request(http.MethodPost, "/api/projects/project-1/import-candidates", "crossref-candidate", candidateBody), http.StatusCreated, &replay)
	if replay.ID != candidate.ID {
		t.Fatalf("idempotent retry returned a different candidate: %q vs %q", replay.ID, candidate.ID)
	}
	// A policy change must fail closed even when the project catalog is read
	// through the active single-connection transaction.
	oldVenue := adapter.globalAcademicCatalog.Rules[0].VenueName
	adapter.globalAcademicCatalog.Rules[0].VenueName = oldVenue + " changed"
	stale := request(http.MethodPost, "/api/projects/project-1/import-candidates/"+candidate.ID+"/confirm", "crossref-stale-confirm",
		`{`+projectExpectation()+`,"expected_context_revision":0,"expected_version":1,"confirmed":true,"reason":"policy changed"}`)
	adapter.globalAcademicCatalog.Rules[0].VenueName = oldVenue
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale policy confirmation status = %d, want 409: %s", stale.Code, stale.Body.String())
	}
	var candidates []core.ImportCandidate
	readData(request(http.MethodGet, "/api/projects/project-1/import-candidates", "", ""), http.StatusOK, &candidates)
	if len(candidates) != 1 || candidates[0].ID != candidate.ID || candidates[0].Status != "awaiting_consent" || candidates[0].Version != 1 {
		t.Fatalf("stale policy confirmation changed candidate: %+v", candidates)
	}
	var confirmed core.ImportCandidate
	readData(request(http.MethodPost, "/api/projects/project-1/import-candidates/"+candidate.ID+"/confirm", "crossref-confirm",
		`{`+projectExpectation()+`,"expected_context_revision":0,"expected_version":1,"confirmed":true,"reason":"synthetic human confirmation for metadata retention"}`), http.StatusOK, &confirmed)
	if confirmed.Status != "imported" || confirmed.Version != 2 || confirmed.URL != "" || confirmed.Locator != "" {
		t.Fatalf("confirmed metadata candidate crossed its locator/full-text boundary: %+v", confirmed)
	}
	// Real SQLite asset/chunk and a local synthetic original; the Crossref
	// transport and all contents remain synthetic. Human association only.
	if err := db.Exec(`CREATE TABLE knowledge_bases (id TEXT PRIMARY KEY, tenant_id INTEGER NOT NULL, name TEXT, chunking_config TEXT, deleted_at DATETIME)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO knowledge_bases(id,tenant_id,name,chunking_config) VALUES ('kb-assoc',7,'association fixture','{}')`).Error; err != nil {
		t.Fatal(err)
	}
	const original = "A controlled metadata record\nDOI: 10.5555/integration.1\nA synthetic uploaded paper original."
	path := filepath.Join(t.TempDir(), "paper.txt")
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	processed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fileDigest := md5.Sum([]byte(original))
	uploadHash := hex.EncodeToString(fileDigest[:])
	if err := db.Exec(`INSERT INTO knowledges(id,tenant_id,knowledge_base_id,type,title,file_type,file_size,file_hash,file_path,parse_status,processed_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, "paper-knowledge", 7, "kb-assoc", "file", "Synthetic paper", "txt", len(original), uploadHash, path, types.ParseStatusCompleted, processed).Error; err != nil {
		t.Fatal(err)
	}
	bindings := evidence.NewBindings(db)
	bindings.SetKnowledgeSignalReader(dbKnowledgeReader{db: db})
	asset, err := bindings.Bind(context.Background(), evidence.BindInput{TenantID: 7, ProjectID: "project-1", KnowledgeID: "paper-knowledge", KnowledgeBaseID: "kb-assoc", Title: "Synthetic paper", CreatedBy: "reader", Signal: evidence.KnowledgeSignal{KnowledgeID: "paper-knowledge", ParseStatus: types.ParseStatusCompleted, FileHash: uploadHash, FileSize: int64(len(original)), ProcessedAt: processed}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO chunks(id,tenant_id,knowledge_id,knowledge_base_id,content,content_revision,chunk_index,start_at,end_at) VALUES (?,?,?,?,?,0,0,0,?)`, "paper-chunk", 7, "paper-knowledge", "kb-assoc", original, len([]rune(original))).Error; err != nil {
		t.Fatal(err)
	}
	seedFixtureOriginProof(t, db, "paper-chunk", "paper-knowledge", 7, []byte(original))
	sourceView, err := adapter.ReadResearchSource(actorContext("reader"), core.Actor{TenantID: 7, UserID: "reader", Role: types.TenantRoleAdmin}, "project-1", "paper-chunk")
	if err != nil {
		t.Fatal(err)
	}
	body := `{` + projectExpectation() + `,"expected_context_revision":0,"expected_version":2,"candidate_id":"` + confirmed.ID + `","source_id":"paper-chunk","expected_asset_revision":` + strconv.Itoa(sourceView.AssetRevision) + `,"expected_quoted_text_hash":"` + sourceView.QuotedTextHash + `","expected_locator":"` + sourceView.Locator + `","confirmed":true,"reason":"Human checked bibliographic details in uploaded original"}`
	changedView := strings.Replace(body, `"expected_quoted_text_hash":"`+sourceView.QuotedTextHash+`"`, `"expected_quoted_text_hash":"previously-viewed-hash"`, 1)
	staleView := request(http.MethodPost, "/api/projects/project-1/academic-source-associations", "association-stale-view", changedView)
	if staleView.Code != http.StatusConflict {
		t.Fatalf("changed viewed source must fail with version conflict: %d %s", staleView.Code, staleView.Body.String())
	}
	var count int64
	if err := db.Table("lingdoc_g2_objects").Where("project_id = ? AND kind = ?", "project-1", core.AcademicSourceAssociationKind).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("stale source persisted association: %d %v", count, err)
	}
	missingConsent := strings.Replace(body, `"confirmed":true`, `"confirmed":false`, 1)
	denied := request(http.MethodPost, "/api/projects/project-1/academic-source-associations", "association-missing-consent", missingConsent)
	if denied.Code < 400 || denied.Code >= 500 {
		t.Fatalf("missing human confirmation must fail closed: %d %s", denied.Code, denied.Body.String())
	}
	var association core.AcademicSourceAssociation
	readData(request(http.MethodPost, "/api/projects/project-1/academic-source-associations", "association-create", body), http.StatusCreated, &association)
	if association.CandidateRef.ID != confirmed.ID || association.CandidateRef.Version != confirmed.Version || association.DOI != confirmed.DOI || association.AssetID != asset.ID || association.AssetRevision != asset.AssetRevision || association.ConfirmedBy != "reader" {
		t.Fatalf("association lost provenance: %+v", association)
	}
	var replayAssociation core.AcademicSourceAssociation
	readData(request(http.MethodPost, "/api/projects/project-1/academic-source-associations", "association-create", body), http.StatusCreated, &replayAssociation)
	if replayAssociation.ID != association.ID {
		t.Fatal("association replay produced another record")
	}
	var associationHistory []core.AcademicSourceAssociationView
	readData(request(http.MethodGet, "/api/projects/project-1/academic-source-associations", "", ""), http.StatusOK, &associationHistory)
	if len(associationHistory) != 1 || associationHistory[0].ReviewState != "historical" {
		t.Fatalf("association history: %+v", associationHistory)
	}
	if err := db.Exec(`UPDATE chunks SET content = 'changed' WHERE id = 'paper-chunk'`).Error; err != nil {
		t.Fatal(err)
	}
	staleReplay := request(http.MethodPost, "/api/projects/project-1/academic-source-associations", "association-create", body)
	if staleReplay.Code < 400 || staleReplay.Code >= 500 {
		t.Fatalf("changed source replay must fail closed: %d %s", staleReplay.Code, staleReplay.Body.String())
	}
	var revoked core.AssociationRevocationReceipt
	revokeBody := `{` + projectExpectation() + `,"expected_context_revision":0,"expected_version":1,"confirmed":true,"reason":"Withdraw previous human assertion"}`
	readData(request(http.MethodPost, "/api/projects/project-1/academic-source-associations/"+association.ID+"/revoke", "association-revoke", revokeBody), http.StatusOK, &revoked)
	if revoked.ID != association.ID || revoked.Status != "revoked" || revoked.Version != 2 {
		t.Fatalf("revocation receipt: %+v", revoked)
	}
}
