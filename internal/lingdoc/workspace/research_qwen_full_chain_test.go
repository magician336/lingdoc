package workspace

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/academic/crossref"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// TestIssue682QwenResearchHTTP is explicitly opt-in: it calls a real, potentially
// billable Qwen API. The Crossref transport, policy, quotas and confirmation are
// synthetic and isolated; this is NOT real publication or researcher acceptance.
// Crossref only yields metadata. The separate source-anchor subtest uses another
// synthetic project, and must not be represented as an original for this DOI.
// Required: LINGDOC_QWEN_RESEARCH_E2E=1, LINGDOC_QWEN_BASE_URL and
// LINGDOC_QWEN_API_KEY. Secrets are never included in test diagnostics.
func TestIssue682QwenResearchHTTP(t *testing.T) {
	if os.Getenv("LINGDOC_QWEN_RESEARCH_E2E") != "1" {
		t.Skip("set LINGDOC_QWEN_RESEARCH_E2E=1 to run the real Qwen acceptance test")
	}
	baseURL := strings.TrimSpace(os.Getenv("LINGDOC_QWEN_BASE_URL"))
	key := strings.TrimSpace(os.Getenv("LINGDOC_QWEN_API_KEY"))
	if baseURL == "" || key == "" {
		t.Fatal("real Qwen test requires LINGDOC_QWEN_BASE_URL and LINGDOC_QWEN_API_KEY")
	}
	client, err := chat.NewRemoteAPIChat(&chat.ChatConfig{
		Source: types.ModelSourceRemote, BaseURL: baseURL, APIKey: key,
		ModelName: "qwen3.8-flash", ModelID: "qwen3.8-flash",
	})
	if err != nil {
		t.Fatal("Qwen model configuration or URL validation failed")
	}
	engine, db, adapter := newResearchProviderFixture(t, true)
	applySQLiteMigrations(t, db, "000055_lingdoc_model_call_ledger.up.sql", "000058_lingdoc_model_call_ledger_cost_basis.up.sql")
	projects := adapter.projects.(*core.Service)
	now := time.Now().UTC()
	policy := &httpResearchQuotaPolicy{core.ResearchQuotaPolicy{
		Version: "synthetic-682-v1", ApprovedBy: "synthetic-fixture-only", WindowID: "synthetic-682-window",
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour),
		Limit: core.ResearchQuotaAmount{Searches: 2, Results: 40, ModelCalls: 1},
	}}
	ledger := core.NewResearchQuotaLedger(projects, policy)
	if err := adapter.ConfigureAcademicResearchPolicy(syntheticGlobalAcademicCatalog(), ledger); err != nil {
		t.Fatal("could not configure the synthetic catalog and project quota")
	}
	adapter.academic = crossref.Client{HTTPClient: &http.Client{Transport: adapterRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.crossref.org" || r.URL.Path != "/works" {
			t.Error("unexpected synthetic Crossref destination")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"status":"ok","message":{"items":[{"DOI":"10.5555/integration.1","title":["A controlled metadata record"],"author":[{"given":"Ada","family":"Lovelace"}],"published-print":{"date-parts":[[2024,3,9]]},"container-title":["Journal of Integration Tests"],"ISSN":["0378-5955"],"type":"journal-article","license":[{"URL":"https://creativecommons.org/licenses/by/4.0/"}]}]}}`)), Request: r}, nil
	})}, Timeout: 2 * time.Second}
	registered := qwenApprovedModel()
	registered.Name = "qwen3.8-flash"
	registered.Parameters.BaseURL = baseURL
	models := &acceptanceModelServiceStub{models: []*types.Model{registered}, chatModel: client}
	// Construct the host adapter directly so this test does not inherit an
	// unrelated externally configured acceptance profile. Project budget and
	// production model-call ledger remain active in this isolated fixture.
	model := &HostAgentModel{models: models, ledger: core.NewGORMModelCallLedger(db)}
	projects.ConfigureAgentRuntime(core.AgentRuntime{Sources: adapter, Research: adapter, Imports: adapter, Model: model})

	request := func(method, path, idempotency, body string, expected int, dst any) {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(actorContext("reader"))
		r.Header.Set("Content-Type", "application/json")
		if idempotency != "" {
			r.Header.Set("Idempotency-Key", idempotency)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		if w.Code != expected {
			var failure struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &failure)
			t.Fatalf("%s %s: status=%d want=%d error_code=%q", method, path, w.Code, expected, failure.Error.Code)
		}
		if dst != nil {
			var envelope struct {
				Data json.RawMessage `json:"data"`
			}
			if json.Unmarshal(w.Body.Bytes(), &envelope) != nil || json.Unmarshal(envelope.Data, dst) != nil {
				t.Fatalf("%s %s: invalid response envelope", method, path)
			}
		}
	}
	expectation := func() string {
		t.Helper()
		var row struct {
			ProjectVersion         int64
			CurrentContextRevision int64
		}
		if err := db.Table("lingdoc_projects").Select("project_version,current_context_revision").Where("id = ?", "project-1").Take(&row).Error; err != nil {
			t.Fatal("could not read fixture project version")
		}
		return `"expected_project_version":` + strconv.FormatInt(row.ProjectVersion, 10) + `,"expected_context_revision":` + strconv.FormatInt(row.CurrentContextRevision, 10)
	}
	const p = "/api/projects/project-1"
	var cat core.AcademicSourceCatalog
	request(http.MethodPut, p+"/academic-source-catalog", "qwen-catalog", `{`+expectation()+`,"expected_version":0,"rules":[{"id":"integration-journal","version":0,"source_type":"journal","venue_name":"Journal of Integration Tests","venue_ids":["0378-5955"],"allowed_license_urls":[]}]}`, http.StatusOK, &cat)
	var session core.AgentSession
	request(http.MethodPost, p+"/agents/research_agent/sessions", "qwen-session", `{`+expectation()+`}`, http.StatusCreated, &session)
	var turn core.AgentTurn
	request(http.MethodPost, p+"/agent-sessions/"+session.ID+"/turns", "qwen-turn", `{`+expectation()+`,"source_ids":[],"card_refs":[],"sensitive_context_confirmed":false,"user_input":"为一个合成软件验证案例提出两条学术检索词，不要编造论文。"}`, http.StatusOK, &turn)
	if turn.Status != "completed" || turn.ModelID != registered.ID || models.selected != registered.ID || turn.RunMode != "real" || strings.TrimSpace(turn.Reply) == "" || turn.InputTokens <= 0 || turn.OutputTokens <= 0 {
		t.Fatalf("research model turn failed: status=%q model=%q mode=%q failure=%q tokens=%d/%d", turn.Status, turn.ModelID, turn.RunMode, turn.FailureCode, turn.InputTokens, turn.OutputTokens)
	}
	var task core.ResearchTask
	request(http.MethodPost, p+"/research-tasks", "qwen-task", `{`+expectation()+`,"goal":"合成软件验证调研","question_type":"source_discovery","scope":"metadata only","exclusions":[],"queries":["software validation","software verification"],"asset_ids":[],"provider_id":"academic:crossref","expected_output":"synthetic metadata"}`, http.StatusOK, &task)
	var approved core.ResearchTask
	request(http.MethodPost, p+"/research-tasks/"+task.ID+"/approve", "qwen-approve", `{`+expectation()+`,"expected_version":1,"confirmed":true,"reason":"仅测试夹具模拟人工确认"}`, http.StatusOK, &approved)
	var run core.ResearchRun
	request(http.MethodPost, p+"/research-tasks/"+task.ID+"/run", "qwen-run", `{`+expectation()+`,"expected_version":2}`, http.StatusOK, &run)
	if run.Status != "completed" || len(run.Results) != 2 || run.Results[0].Status != "metadata_only" || run.Results[1].Status != "duplicate" {
		t.Fatalf("synthetic metadata search failed: status=%q results=%d", run.Status, len(run.Results))
	}
	var candidate core.ImportCandidate
	request(http.MethodPost, p+"/import-candidates", "qwen-candidate", `{`+expectation()+`,"run_id":"`+run.ID+`","result_id":"`+run.Results[0].ID+`","purpose":"仅模拟书目保留","save_mode":"metadata_only","scope":"personal","model_use_allowed":false,"license":"synthetic metadata only"}`, http.StatusCreated, &candidate)
	if candidate.SelectionType != "academic_metadata" || candidate.Locator != "" || candidate.URL != "" {
		t.Fatal("synthetic metadata unexpectedly became an original or locator")
	}
	var confirmed core.ImportCandidate
	request(http.MethodPost, p+"/import-candidates/"+candidate.ID+"/confirm", "qwen-confirm", `{`+expectation()+`,"expected_version":1,"confirmed":true,"reason":"仅测试夹具模拟人工确认"}`, http.StatusOK, &confirmed)
	if confirmed.Status != "imported" || confirmed.Locator != "" {
		t.Fatal("metadata confirmation did not preserve the original boundary")
	}
	actor := core.Actor{TenantID: 7, UserID: "reader", Role: types.TenantRoleAdmin}
	snapshot, err := ledger.Snapshot(context.Background(), actor, "project-1")
	if err != nil || snapshot.Measured.ModelCalls != 1 || snapshot.Measured.Searches != 2 {
		t.Fatalf("fixture quota ledger did not record the model and search: error=%v measured=%+v", err, snapshot.Measured)
	}
	var modelCalls int64
	if db.Table("lingdoc_model_call_ledger").Where("project_id = ? AND run_mode = ?", "project-1", "real").Count(&modelCalls).Error != nil || modelCalls != 1 {
		t.Fatal("real model call was not recorded exactly once in the model ledger")
	}
	t.Logf("real model=qwen3.8-flash tokens=%d/%d; source=synthetic Crossref transport; quota=synthetic; no DOI-to-original adapter", turn.InputTokens, turn.OutputTokens)
	// Independent original-source fixture; not a continuation of the metadata
	// record above and never evidence of real researcher review.
	t.Run("independent_synthetic_original_anchor", TestSaveSourceAnchorAcceptsStorageBackedUploadedMaterial)
}
