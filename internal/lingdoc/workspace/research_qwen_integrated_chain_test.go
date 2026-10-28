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
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// TestIssue682QwenIntegratedSyntheticChain links two synthetic search inputs
// in the same project and SQLite database. The Crossref metadata and project
// asset are independent synthetic fixtures, with no asserted DOI-to-original
// relationship. Both external search and HybridSearch hits are synthetic;
// the Qwen API call is real. Confirmation is a simulated fixture action.
func TestIssue682QwenIntegratedSyntheticChain(t *testing.T) {
	if os.Getenv("LINGDOC_QWEN_RESEARCH_E2E") != "1" {
		t.Skip("explicit real Qwen opt-in required")
	}
	baseURL, key := strings.TrimSpace(os.Getenv("LINGDOC_QWEN_BASE_URL")), strings.TrimSpace(os.Getenv("LINGDOC_QWEN_API_KEY"))
	if baseURL == "" || key == "" {
		t.Fatal("Qwen endpoint and key are required")
	}
	client, err := chat.NewRemoteAPIChat(&chat.ChatConfig{Source: types.ModelSourceRemote, BaseURL: baseURL, APIKey: key, ModelName: "qwen3.8-flash", ModelID: "qwen3.8-flash"})
	if err != nil {
		t.Fatal("Qwen configuration or URL validation failed")
	}

	_, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db, deliveryMethodChapter())
	ensureImpactRouteTables(t, db)
	applyCreateTablesFromMigration(t, db, "000000_init.up.sql", "knowledge_bases")
	applySQLiteMigrations(t, db, "000033_lingdoc_working_copies.up.sql", "000055_lingdoc_model_call_ledger.up.sql", "000058_lingdoc_model_call_ledger_cost_basis.up.sql")
	applyCreateTablesFromMigration(t, db, "000037_lingdoc_full_workflow.up.sql", "lingdoc_g2_objects", "lingdoc_g2_object_versions")
	for _, statement := range []string{
		"ALTER TABLE chunks ADD COLUMN source_content TEXT NOT NULL DEFAULT ''",
		"INSERT INTO knowledge_bases(id,tenant_id,name,embedding_model_id,summary_model_id) VALUES('kb-1',7,'synthetic-kb','embed-1','summary-1')",
		"UPDATE knowledges SET file_type='txt', file_path='resource://synthetic-storage-object' WHERE id='knowledge-1'",
		"UPDATE chunks SET source_content=content WHERE id='source-1'",
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("fixture setup: %v", err)
		}
	}
	seedDeliveryBoundTemplateCopy(t, db, "evidence-synthetic-copy")
	shares := candidateOneConnShares{}
	search := generationHitKnowledgeStub{hits: []*types.SearchResult{{ID: "source-1", KnowledgeID: "knowledge-1", ChunkIndex: 0, StartAt: 0, EndAt: len([]rune(sourcePolicyQuote)), Content: sourcePolicyQuote}}}
	runtime := NewGORMSourceRuntime(db, ProjectPorts{}, shares, search, delivery.NewFixedTemplateReader())
	projects := core.NewServiceWithSources(core.NewGORMRepository(db), core.ContractDemoTemplate{}, runtime.WorkspaceSourcePolicy())
	runtime.ConnectProjects(ProjectPorts{Authorizer: projects, Resources: projects, Projects: projects, Context: projects})
	connectAggregateCapabilities(runtime, projects)
	runtime.EnableFullWorkflowPolicy()
	storage := &adapterStorageStub{data: []byte(sourcePolicyQuote)}
	runtime.ConnectOriginFiles(KnowledgeFileOriginReader{Service: storage})
	adapter := NewResearchAdapter(db, projects, runtime, shares, storage, nil)
	now := time.Now().UTC()
	policySource := &httpResearchQuotaPolicy{core.ResearchQuotaPolicy{Version: "synthetic-integrated-v1", ApprovedBy: "synthetic-fixture-only", WindowID: "synthetic-integrated", StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), Limit: core.ResearchQuotaAmount{Searches: 2, Results: 40, ModelCalls: 1}}}
	ledger := core.NewResearchQuotaLedger(projects, policySource)
	if err := adapter.ConfigureAcademicResearchPolicy(syntheticGlobalAcademicCatalog(), ledger); err != nil {
		t.Fatal("synthetic research policy setup failed")
	}
	adapter.academic = crossref.Client{HTTPClient: &http.Client{Transport: adapterRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.crossref.org" || r.URL.Path != "/works" {
			t.Error("unexpected synthetic Crossref destination")
		}
		body := `{"status":"ok","message":{"items":[{"DOI":"10.5555/integration.1","title":["Synthetic bibliographic record"],"author":[{"given":"Ada","family":"Lovelace"}],"published-print":{"date-parts":[[2024,3,9]]},"container-title":["Journal of Integration Tests"],"ISSN":["0378-5955"],"type":"journal-article","license":[{"URL":"https://creativecommons.org/licenses/by/4.0/"}]}]}}`
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}, Timeout: 2 * time.Second}
	registered := qwenApprovedModel()
	registered.Name = "qwen3.8-flash"
	registered.Parameters.BaseURL = baseURL
	researchRecording := &issue682RecordingChat{inner: client}
	registry := &acceptanceModelServiceStub{models: []*types.Model{registered}, chatModel: researchRecording}
	host := &HostAgentModel{models: registry, ledger: core.NewGORMModelCallLedger(db)}
	projects.ConfigureAgentRuntime(core.AgentRuntime{Sources: adapter, Research: adapter, Imports: adapter, Model: host})

	actor := core.Actor{TenantID: 7, UserID: "reader", Role: types.TenantRoleOwner}
	ctx := types.WithExecutionTenant(types.WithCaller(policyContext(), types.Caller{TenantID: 7, UserID: "reader", Role: types.TenantRoleOwner}), 7)
	policy := core.ProjectAssetPolicy{ID: assetID, ProjectID: "project-1", AssetID: assetID, Version: 1, AssetRevision: 1, PolicyVersion: 1, Scope: "project", Purpose: "合成资料验收", ModelUseAllowed: true, AllowedUserIDs: []string{}, CreatedBy: "reader", GrantedBy: "reader", Reason: "仅测试夹具模拟许可", ContextRevision: 1}
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if err = core.NewTransactionAdapter(db).(core.ObjectStore).InsertObject(core.StoredObject{ProjectID: "project-1", Kind: core.AssetPolicyKind, ID: assetID, Version: 1, Payload: policyJSON}); err != nil {
		t.Fatalf("synthetic asset policy: %v", err)
	}

	engine := gin.New()
	RegisterAgentRoutes(engine.Group("/api"), AgentRoutePorts{Research: projects, Imports: projects, EvidenceCards: projects, Claims: projects, Anchors: projects, AgentSessions: projects, Proposals: projects, Context: projects, ChangeSets: projects})
	request := func(method, path, id, body string, status int, dst any) {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(actorContext("reader"))
		r.Header.Set("Content-Type", "application/json")
		if id != "" {
			r.Header.Set("Idempotency-Key", id)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		if w.Code != status {
			var failure struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &failure)
			t.Fatalf("%s %s: HTTP %d want %d code=%q", method, path, w.Code, status, failure.Error.Code)
		}
		if dst != nil {
			var body struct {
				Data json.RawMessage `json:"data"`
			}
			if json.Unmarshal(w.Body.Bytes(), &body) != nil || json.Unmarshal(body.Data, dst) != nil {
				t.Fatalf("%s %s: invalid response", method, path)
			}
		}
	}
	expectation := func(version int64) string {
		t.Helper()
		p, err := projects.GetProject(ctx, actor, "project-1")
		if err != nil {
			t.Fatalf("read project version: %v", err)
		}
		return `"expected_project_version":` + strconv.FormatInt(p.ProjectVersion, 10) + `,"expected_context_revision":` + strconv.FormatInt(p.CurrentContextRevision, 10) + `,"expected_version":` + strconv.FormatInt(version, 10)
	}
	const route = "/api/projects/project-1"
	var catalog core.AcademicSourceCatalog
	request(http.MethodPut, route+"/academic-source-catalog", "integrated-catalog", `{`+expectation(0)+`,"rules":[{"id":"integration-journal","version":0,"source_type":"journal","venue_name":"Journal of Integration Tests","venue_ids":["0378-5955"],"allowed_license_urls":[]}]}`, http.StatusOK, &catalog)
	var session core.AgentSession
	request(http.MethodPost, route+"/agents/research_agent/sessions", "integrated-session", `{`+expectation(0)+`}`, http.StatusCreated, &session)
	var turn core.AgentTurn
	request(http.MethodPost, route+"/agent-sessions/"+session.ID+"/turns", "integrated-turn", `{`+expectation(0)+`,"source_ids":[],"card_refs":[],"sensitive_context_confirmed":false,"user_input":"请为合成项目材料与学术书目检索生成可编辑的调研任务卡 task_card，包含至少两个不同的检索词；不要编造论文或来源。"}`, http.StatusOK, &turn)
	if turn.Status != "completed" || turn.ModelID != registered.ID || turn.RunMode != "real" || turn.InputTokens <= 0 || turn.OutputTokens <= 0 || strings.TrimSpace(turn.Reply) == "" {
		t.Fatalf("research Qwen turn status=%s failure=%s", turn.Status, turn.FailureCode)
	}
	var suggestions map[string]json.RawMessage
	if err := json.Unmarshal(turn.Suggestions, &suggestions); err != nil || len(suggestions[core.ResearchProposalKey]) == 0 {
		// Inputs are generated fixtures; preserve the actual model output for
		// diagnosing contract mismatches without including credentials.
		t.Logf("synthetic research model response: %s", researchRecording.output)
		t.Fatalf("Qwen research turn did not provide a usable task proposal: failure=%q", turn.FailureCode)
	}
	proposal, err := core.DecodeResearchProposal(suggestions[core.ResearchProposalKey], turn.Context)
	if err != nil || len(proposal.Queries) < 2 {
		t.Fatal("Qwen research task proposal did not pass the server contract")
	}
	// Simulate the researcher editing a model-proposed query before approval.
	// The project index and the academic provider remain deterministic test stubs.
	editedQueries := []string{proposal.Queries[0] + " software", proposal.Queries[1]}
	queryJSON, err := json.Marshal(editedQueries)
	if err != nil {
		t.Fatal("encode edited research queries")
	}
	var task core.ResearchTask
	request(http.MethodPost, route+"/research-tasks", "integrated-task", `{`+expectation(0)+`,"goal":"合成项目材料与书目检索","question_type":"source_discovery","scope":"仅合成验收","exclusions":[],"queries":`+string(queryJSON)+`,"asset_ids":["`+assetID+`"],"provider_id":"academic:crossref","expected_output":"合成项目资料与模拟书目"}`, http.StatusOK, &task)
	if len(task.Queries) != 2 || task.Queries[0] != editedQueries[0] || task.Queries[1] != editedQueries[1] {
		t.Fatal("the edited model-proposed queries were not preserved")
	}
	var approved core.ResearchTask
	request(http.MethodPost, route+"/research-tasks/"+task.ID+"/approve", "integrated-approve", `{`+expectation(task.Version)+`,"confirmed":true,"reason":"仅测试夹具模拟人工确认"}`, http.StatusOK, &approved)
	var run core.ResearchRun
	request(http.MethodPost, route+"/research-tasks/"+task.ID+"/run", "integrated-run", `{`+expectation(approved.Version)+`}`, http.StatusOK, &run)
	var projectHit, metadataHit *core.ResearchResult
	for i := range run.Results {
		result := &run.Results[i]
		if result.SourceID == "source-1" && result.Provider == "weknora_hybrid" {
			projectHit = result
		}
		if result.Provider == "crossref" && result.Status == "metadata_only" {
			metadataHit = result
		}
	}
	if run.Status != "completed" || len(run.Rounds) != 2 || run.Rounds[0].Query != editedQueries[0] || run.Rounds[1].Query != editedQueries[1] || projectHit == nil || metadataHit == nil || projectHit.Locator == "" || projectHit.QuotedTextHash == "" {
		t.Fatalf("integrated synthetic search status=%q results=%d project_hit=%v metadata_hit=%v", run.Status, len(run.Results), projectHit != nil, metadataHit != nil)
	}
	var candidate core.ImportCandidate
	request(http.MethodPost, route+"/import-candidates", "integrated-candidate", `{`+expectation(0)+`,"run_id":"`+run.ID+`","result_id":"`+metadataHit.ID+`","purpose":"仅模拟书目保存","save_mode":"metadata_only","scope":"personal","model_use_allowed":false,"license":"synthetic metadata only"}`, http.StatusCreated, &candidate)
	if candidate.SelectionType != "academic_metadata" || candidate.Locator != "" || candidate.URL != "" {
		t.Fatal("synthetic academic metadata was misrepresented as located original")
	}
	var metadataConfirmed core.ImportCandidate
	request(http.MethodPost, route+"/import-candidates/"+candidate.ID+"/confirm", "integrated-metadata-confirm", `{`+expectation(candidate.Version)+`,"confirmed":true,"reason":"仅测试夹具模拟人工确认"}`, http.StatusOK, &metadataConfirmed)
	if metadataConfirmed.Status != "imported" || metadataConfirmed.Locator != "" {
		t.Fatal("metadata confirmation changed original-source boundary")
	}
	snapshot, err := ledger.Snapshot(context.Background(), actor, "project-1")
	if err != nil || snapshot.Measured.ModelCalls != 1 || snapshot.Measured.Searches != 2 {
		t.Fatal("synthetic project quota did not account for research Qwen and both searches")
	}
	t.Log("same project: Qwen research; synthetic HybridSearch project hit; independent synthetic Crossref metadata; simulated plan and metadata confirmation")
	var anchor core.SourceAnchor
	request(http.MethodPost, route+"/source-anchors", "synthetic-anchor", `{`+expectation(0)+`,"source_id":"source-1","selection_start":0,"selection_end":`+strconv.Itoa(len([]rune(sourcePolicyQuote)))+`,"extraction_type":"native_text","ocr_confidence":1,"human_verified":true,"confirmed":true}`, http.StatusCreated, &anchor)
	if anchor.ID == "" || anchor.SourceID != projectHit.SourceID || anchor.Locator == "" || storage.calls == 0 {
		t.Fatal("anchor did not use the synthetic asset and real original verification")
	}
	var claim core.ResearchClaim
	request(http.MethodPost, route+"/research-claims", "synthetic-claim", `{`+expectation(0)+`,"text":"合成资料只用于软件流程验证","scope":"仅此合成材料","reason":"仅测试夹具模拟人工输入","confirmed":true}`, http.StatusOK, &claim)
	var card core.EvidenceCard
	request(http.MethodPost, route+"/evidence-cards", "synthetic-card", `{`+expectation(0)+`,"claim_ref":{"id":"`+claim.ID+`","version":`+strconv.FormatInt(claim.Version, 10)+`},"anchor_refs":[{"id":"`+anchor.ID+`","version":`+strconv.FormatInt(anchor.Version, 10)+`}],"support_type":"supports","scope":"仅此合成材料","scope_chapter_ids":["chapter-2"],"limitations":"不用于真实研究","confirmed":true}`, http.StatusCreated, &card)
	if card.Status != "CANDIDATE" {
		t.Fatalf("unexpected unconfirmed card status %q", card.Status)
	}
	contextBody := func(card core.EvidenceCard) string {
		return `{` + expectation(0) + `,"chapter_id":"chapter-2","source_ids":[],"card_refs":[{"id":"` + card.ID + `","version":` + strconv.FormatInt(card.Version, 10) + `}],"sensitive_context_confirmed":true}`
	}
	request(http.MethodPost, route+"/agent-context", "", contextBody(card), http.StatusUnprocessableEntity, nil)
	var confirmed core.EvidenceCard
	request(http.MethodPost, route+"/evidence-cards/"+card.ID+"/confirm", "synthetic-card-confirm", `{`+expectation(card.Version)+`,"confirmed":true,"reason":"仅测试夹具模拟逐张审核"}`, http.StatusOK, &confirmed)
	if confirmed.Status != "CONFIRMED" || confirmed.Confirmation == nil || len(confirmed.Confirmation.AnchorRefs) != 1 || confirmed.Confirmation.AnchorRefs[0].ID != anchor.ID {
		t.Fatal("confirmation did not retain the original anchor")
	}
	var pack core.AgentContextPackage
	request(http.MethodPost, route+"/agent-context", "", contextBody(confirmed), http.StatusOK, &pack)
	if len(pack.CardRefs) != 1 || pack.CardRefs[0].ID != confirmed.ID || pack.CardRefs[0].Version != confirmed.Version || len(pack.SourceRefs) != 1 || pack.SourceRefs[0].SourceID != "source-1" || pack.SourceRefs[0].Locator != anchor.Locator || pack.SourceRefs[0].QuotedTextHash == "" || pack.SourceRefs[0].QuotedText != sourcePolicyQuote {
		t.Fatal("server context did not preserve selected confirmed card and its located source")
	}
	selection, err := projects.SelectGenerationEvidence(ctx, actor, "project-1", "chapter-2", []core.VersionRef{{ID: confirmed.ID, Version: confirmed.Version}}, true)
	if err != nil || len(selection.Cards) != 1 || len(selection.Sources) != 1 || selection.Sources[0].QuotedTextHash != pack.SourceRefs[0].QuotedTextHash {
		t.Fatalf("writing evidence selection failed: %v", err)
	}

	t.Run("real_qwen_writing_adapter", func(t *testing.T) {
		if os.Getenv("LINGDOC_QWEN_RESEARCH_E2E") != "1" {
			t.Skip("real Qwen model call requires explicit opt-in")
		}
		baseURL, key := strings.TrimSpace(os.Getenv("LINGDOC_QWEN_BASE_URL")), strings.TrimSpace(os.Getenv("LINGDOC_QWEN_API_KEY"))
		if baseURL == "" || key == "" {
			t.Fatal("Qwen endpoint and key are required")
		}
		client, err := chat.NewRemoteAPIChat(&chat.ChatConfig{Source: types.ModelSourceRemote, BaseURL: baseURL, APIKey: key, ModelName: "qwen3.8-flash", ModelID: "qwen3.8-flash"})
		if err != nil {
			t.Fatal("Qwen configuration or URL validation failed")
		}
		model := qwenApprovedModel()
		model.Name, model.Parameters.BaseURL = "qwen3.8-flash", baseURL
		recording := &issue682RecordingChat{inner: client}
		registry := &acceptanceModelServiceStub{models: []*types.Model{model}, chatModel: recording}
		writer := generationHostModel{models: registry, templates: delivery.NewFixedTemplateReader(), ledger: core.NewGORMModelCallLedger(db)}
		ref := generation.EvidenceCardRef{ID: confirmed.ID, Version: confirmed.Version}
		constraints, err := selectedEvidenceConstraints(selection, []generation.EvidenceCardRef{ref})
		if err != nil {
			t.Fatalf("evidence constraints: %v", err)
		}
		s := pack.SourceRefs[0]
		input := acceptanceGenerationInput()
		input.Actor = generation.Actor{TenantID: 7, UserID: "reader", Role: types.TenantRoleOwner}
		input.Workspace.ProjectID, input.Workspace.ChapterID = "project-1", "chapter-2"
		input.Workspace.Chapter.ID, input.Workspace.Chapter.ProjectID, input.Workspace.Chapter.SectionID = "chapter-2", "project-1", "method"
		input.EvidenceCardRefs = []generation.EvidenceCardRef{ref}
		input.EvidenceConstraints = constraints
		input.Sources = []generation.Source{{ID: s.SourceID, ProjectID: "project-1", AssetID: s.AssetID, AssetRevision: s.AssetRevision, Locator: s.Locator, QuotedText: s.QuotedText}}
		input.Request.Instruction = "仅依据给出的合成来源写一段简短说明，在正文用 [[source:source-1]] 标注引用。"
		input.Instruction = input.Request.Instruction
		draft, err := writer.Generate(ctx, input)
		if err != nil {
			t.Fatal("Qwen writing adapter failed; see controlled server logs for reason")
		}
		if !strings.Contains(recording.input, confirmed.ID) || !strings.Contains(recording.input, s.Locator) || !strings.Contains(recording.input, s.QuotedText) || !strings.Contains(draft.BodyMarkdown, "[[source:source-1]]") || len(draft.Sources) != 1 || draft.Sources[0].ID != "source-1" {
			t.Fatal("model input or output did not preserve selected synthetic card, locator, and citation")
		}
		t.Log("real Qwen writing adapter received a server-selected synthetic card and returned its citation; generation HTTP/job was not exercised")
	})
}
