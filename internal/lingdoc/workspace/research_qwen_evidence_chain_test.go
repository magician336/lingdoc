package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/lingdoc/generation"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// This is a separate synthetic PROJECT ASSET, not an original of any
// Crossref search result. Storage is a test port carrying actual synthetic
// bytes; source validation, HTTP routes, SQLite, authorization, evidence
// state, anchor and context assembly are production implementations.
// Human-verification flags are simulated and do not represent researcher review.
func TestIssue682SyntheticAssetEvidenceHTTP(t *testing.T) {
	_, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db, deliveryMethodChapter())
	ensureImpactRouteTables(t, db)
	applyCreateTablesFromMigration(t, db, "000000_init.up.sql", "knowledge_bases")
	applySQLiteMigrations(t, db, "000033_lingdoc_working_copies.up.sql")
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
	runtime := NewGORMSourceRuntime(db, ProjectPorts{}, shares, nil, delivery.NewFixedTemplateReader())
	projects := core.NewServiceWithSources(core.NewGORMRepository(db), core.ContractDemoTemplate{}, runtime.WorkspaceSourcePolicy())
	runtime.ConnectProjects(ProjectPorts{Authorizer: projects, Resources: projects, Projects: projects, Context: projects})
	connectAggregateCapabilities(runtime, projects)
	runtime.EnableFullWorkflowPolicy()
	storage := &adapterStorageStub{data: []byte(sourcePolicyQuote)}
	setFixtureUploadHash(t, db, storage.data)
	seedFixtureOriginProof(t, db, "source-1", "knowledge-1", 7, storage.data)
	runtime.ConnectOriginFiles(KnowledgeFileOriginReader{Service: storage})
	if err := runtime.bindings.RefreshAsset(context.Background(), "project-1", assetID); err != nil {
		t.Fatalf("establish original-byte revision baseline: %v", err)
	}
	currentAsset, err := runtime.bindings.CurrentAsset(context.Background(), "project-1", assetID)
	if err != nil {
		t.Fatalf("read original-byte revision baseline: %v", err)
	}
	adapter := NewResearchAdapter(db, projects, runtime, shares, storage, nil)
	projects.ConfigureAgentRuntime(core.AgentRuntime{Sources: adapter})

	actor := core.Actor{TenantID: 7, UserID: "reader", Role: types.TenantRoleOwner}
	ctx := types.WithExecutionTenant(types.WithCaller(policyContext(), types.Caller{TenantID: 7, UserID: "reader", Role: types.TenantRoleOwner}), 7)
	policy := core.ProjectAssetPolicy{ID: assetID, ProjectID: "project-1", AssetID: assetID, Version: 1, AssetRevision: int64(currentAsset.AssetRevision), PolicyVersion: 1, Scope: "project", Purpose: "合成资料验收", ModelUseAllowed: true, AllowedUserIDs: []string{}, CreatedBy: "reader", GrantedBy: "reader", Reason: "仅测试夹具模拟许可", ContextRevision: 1}
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
	var anchor core.SourceAnchor
	request(http.MethodPost, route+"/source-anchors", "synthetic-anchor", `{`+expectation(0)+`,"source_id":"source-1","selection_start":0,"selection_end":`+strconv.Itoa(len([]rune(sourcePolicyQuote)))+`,"extraction_type":"native_text","ocr_confidence":1,"human_verified":true,"confirmed":true}`, http.StatusCreated, &anchor)
	if anchor.ID == "" || anchor.SourceID != "source-1" || anchor.Locator == "" || storage.calls == 0 {
		t.Fatal("anchor did not use the synthetic asset and real original verification")
	}
	var claim core.ResearchClaim
	request(http.MethodPost, route+"/research-claims", "synthetic-claim", `{`+expectation(0)+`,"text":"合成资料只用于软件流程验证","scope":"仅此合成材料","reason":"仅测试夹具模拟人工输入","confirmed":true}`, http.StatusOK, &claim)
	// A simulated human association is stored only to exercise the real
	// evidence-card HTTP route. It is not a verified academic paper.
	association := core.AcademicSourceAssociation{ID: "synthetic-association", ProjectID: "project-1", Version: 1, SourceID: anchor.SourceID, AssetID: anchor.AssetID, AssetRevision: anchor.AssetRevision, Status: "recorded"}
	assocPayload, err := json.Marshal(association)
	if err != nil {
		t.Fatal(err)
	}
	if err = core.NewTransactionAdapter(db).(core.ObjectStore).InsertObject(core.StoredObject{ProjectID: "project-1", Kind: core.AcademicSourceAssociationKind, ID: association.ID, Version: 1, Payload: assocPayload}); err != nil {
		t.Fatal(err)
	}
	var associated core.EvidenceCard
	request(http.MethodPost, route+"/evidence-cards", "synthetic-associated-card", `{`+expectation(0)+`,"claim_ref":{"id":"`+claim.ID+`","version":`+strconv.FormatInt(claim.Version, 10)+`},"anchor_refs":[{"id":"`+anchor.ID+`","version":`+strconv.FormatInt(anchor.Version, 10)+`}],"academic_association_refs":[{"id":"synthetic-association","version":1}],"support_type":"supports","scope":"仅此合成材料","scope_chapter_ids":["chapter-2"],"limitations":"不可声称学术依据","confirmed":true}`, http.StatusCreated, &associated)
	if len(associated.AcademicAssociationRefs) != 1 || associated.AcademicAssociationRefs[0].ID != association.ID {
		t.Fatal("HTTP candidate did not preserve association version")
	}
	request(http.MethodPost, route+"/evidence-cards/"+associated.ID+"/confirm", "synthetic-associated-confirm", `{`+expectation(associated.Version)+`,"confirmed":true,"reason":"合成确认"}`, http.StatusUnprocessableEntity, nil)
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
		applySQLiteMigrations(t, db, "000055_lingdoc_model_call_ledger.up.sql", "000058_lingdoc_model_call_ledger_cost_basis.up.sql")
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

// The wrapper records only synthetic prompt data in memory; it does not log the
// API key, request headers, provider error or full model responses.
type issue682RecordingChat struct {
	inner  chat.Chat
	input  string
	output string
}

func (c *issue682RecordingChat) Chat(ctx context.Context, messages []chat.Message, opts *chat.ChatOptions) (*types.ChatResponse, error) {
	for _, m := range messages {
		if m.Role == "user" {
			c.input = m.Content
		}
	}
	response, err := c.inner.Chat(ctx, messages, opts)
	if response != nil {
		c.output = response.Content
	}
	return response, err
}
func (c *issue682RecordingChat) ChatStream(ctx context.Context, messages []chat.Message, opts *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return c.inner.ChatStream(ctx, messages, opts)
}
func (c *issue682RecordingChat) GetModelName() string { return c.inner.GetModelName() }
func (c *issue682RecordingChat) GetModelID() string   { return c.inner.GetModelID() }
