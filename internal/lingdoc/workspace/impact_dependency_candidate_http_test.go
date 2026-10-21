package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// The AI-proposed dependency candidates over the real HTTP seam
// (G3 gap closure #252, AC-G3-15).
//
// The assertions stay on externally visible status and payload: the batch
// shows the object versions, the evidence and the proposed rationale; the
// three states decide the batch; only the decided candidates reach the
// formal dependency graph.

// ensureDependencyCandidateTables creates the two candidate tables. The
// production schema comes from the migrations; the focused HTTP fixture
// builds its own tables the way the other workspace fixtures do.
func ensureDependencyCandidateTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS lingdoc_g3_dependency_candidate_batches (
			id TEXT PRIMARY KEY, project_id TEXT NOT NULL, actor_id TEXT NOT NULL, idempotency_key TEXT NOT NULL,
			scope_json TEXT NOT NULL DEFAULT '{}', status TEXT NOT NULL, suppressed_json TEXT NOT NULL DEFAULT '[]',
			content_hash TEXT NOT NULL DEFAULT '', project_version INTEGER NOT NULL DEFAULT 0,
			context_revision INTEGER NOT NULL DEFAULT 0, candidate_count INTEGER NOT NULL DEFAULT 0,
			suppressed_count INTEGER NOT NULL DEFAULT 0, reason TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
			UNIQUE (project_id, actor_id, idempotency_key))`,
		`CREATE TABLE IF NOT EXISTS lingdoc_g3_dependency_candidates (
			id TEXT PRIMARY KEY, project_id TEXT NOT NULL, batch_id TEXT NOT NULL,
			source_kind TEXT NOT NULL, source_id TEXT NOT NULL, source_version TEXT NOT NULL,
			target_kind TEXT NOT NULL, target_id TEXT NOT NULL, target_version TEXT NOT NULL,
			relation TEXT NOT NULL, action TEXT NOT NULL, evidence TEXT NOT NULL DEFAULT '',
			reason TEXT NOT NULL DEFAULT '', confidence REAL NOT NULL DEFAULT 0, status TEXT NOT NULL,
			decision TEXT NOT NULL DEFAULT '', decision_reason TEXT NOT NULL DEFAULT '',
			decided_by TEXT NOT NULL DEFAULT '', decided_at DATETIME, edge_id TEXT NOT NULL DEFAULT '',
			superseded_relation TEXT NOT NULL DEFAULT '', superseded_action TEXT NOT NULL DEFAULT '',
			project_version INTEGER NOT NULL DEFAULT 0, context_revision INTEGER NOT NULL DEFAULT 0,
			model_run_id TEXT NOT NULL DEFAULT '', actor_id TEXT NOT NULL DEFAULT '',
			version INTEGER NOT NULL DEFAULT 1, created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
	} {
		if err := db.Exec(statement).Error; err != nil {
			t.Fatalf("seed dependency candidate table: %v", err)
		}
	}
}

func candidateDataFrom(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if response.Code != http.StatusOK && response.Code != http.StatusCreated {
		t.Fatalf("unexpected status %d; body=%s", response.Code, response.Body.String())
	}
	var decoded struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode candidate response: %v (%s)", err, response.Body.String())
	}
	if err := json.Unmarshal(decoded.Data, target); err != nil {
		t.Fatalf("decode candidate payload: %v (%s)", err, string(decoded.Data))
	}
}

// candidateFixtureEngine wires the workspace routes on the delivery fixture
// and returns the engine plus the chapter references the candidates use.
func candidateFixtureEngine(t *testing.T) (*gin.Engine, *gorm.DB, core.ObjectVersionRef, core.ObjectVersionRef) {
	t.Helper()
	handler, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db,
		deliveryQuestionChapter(assetID, currentAssetRevision(t, handler, assetID), deliveryBody, []string{deliverySourceID}),
		deliveryMethodChapter(),
	)
	ensureImpactRouteTables(t, db)
	ensureDependencyCandidateTables(t, db)
	if err := db.Exec("UPDATE lingdoc_projects SET current_context_revision = ? WHERE id = ?", 1, "project-1").Error; err != nil {
		t.Fatalf("seed context revision: %v", err)
	}
	seedDeliveryBoundTemplateCopy(t, db, "change-http-copy")
	service, ok := testRuntime(handler).projects.(*core.Service)
	if !ok {
		t.Fatalf("workspace handler service = %T, want *core.Service", testRuntime(handler).projects)
	}
	actor := Actor{TenantID: 7, UserID: deliveryActorID}
	project, err := service.GetProject(policyContext(), actor, "project-1")
	if err != nil {
		t.Fatalf("read fixture project: %v", err)
	}
	chapters, err := service.ListChapters(policyContext(), actor, project.ID)
	if err != nil || len(chapters) < 2 {
		t.Fatalf("chapters: %v %+v", err, chapters)
	}
	for _, chapter := range chapters[:2] {
		if chapter.CurrentVersionID == nil {
			t.Fatalf("fixture chapter %s has no current version", chapter.ID)
		}
	}
	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	RegisterImpactRoutes(engine.Group("/api"), impactPortsFor(service))
	source := core.ObjectVersionRef{Kind: "ChapterVersion", ID: chapters[0].ID, Version: *chapters[0].CurrentVersionID}
	target := core.ObjectVersionRef{Kind: "ChapterVersion", ID: chapters[1].ID, Version: *chapters[1].CurrentVersionID}
	return engine, db, source, target
}

func candidateRefJSON(ref core.ObjectVersionRef) string {
	raw, err := json.Marshal(ref)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func TestDependencyCandidateRoutesCoverBatchDecisionsAndIdempotency(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine, _, source, target := candidateFixtureEngine(t)
	sourceJSON := candidateRefJSON(source)
	targetJSON := candidateRefJSON(target)
	batchBody := `{"expected_context_revision":1,"reason":"研究条件、证据卡和第 2-4 章","scope":{"objects":[` +
		sourceJSON + `,` + targetJSON + `],"reason":"用户选择的范围"},"candidates":[` +
		`{"source":` + sourceJSON + `,"target":` + targetJSON + `,"relation":"supports","action":"REVALIDATE_EVIDENCE","evidence":"第 2 章结论引用了证据卡 E12","reason":"章节结论依赖该证据","confidence":0.6,"model_run_id":"run-1"},` +
		`{"source":` + targetJSON + `,"target":` + sourceJSON + `,"relation":"constrains","action":"REGENERATE","evidence":"证据卡 E12 的版本已经落后","reason":"证据变化需要重算章节","confidence":0.4,"model_run_id":"run-1"}]}`

	// Success: the batch is submitted and carries the versions, the evidence
	// and the proposed rationale.
	var batch struct {
		ID         string `json:"id"`
		Candidates []struct {
			ID       string                `json:"id"`
			Source   core.ObjectVersionRef `json:"source"`
			Target   core.ObjectVersionRef `json:"target"`
			Evidence string                `json:"evidence"`
			Reason   string                `json:"reason"`
			Status   string                `json:"status"`
			Version  int64                 `json:"version"`
		} `json:"candidates"`
		Suppressed []json.RawMessage `json:"suppressed"`
	}
	response := postDependencyJSON(t, engine, http.MethodPost, "/api/projects/project-1/dependency-candidates", deliveryActorID, "candidate-batch-1", batchBody)
	candidateDataFrom(t, response, &batch)
	if batch.ID == "" || len(batch.Candidates) != 2 || len(batch.Suppressed) != 0 {
		t.Fatalf("candidate batch = %+v", batch)
	}
	for _, candidate := range batch.Candidates {
		if candidate.Source.Version == "" || candidate.Target.Version == "" {
			t.Fatalf("candidate lost the object version: %+v", candidate)
		}
		if candidate.Evidence == "" || candidate.Reason == "" {
			t.Fatalf("candidate lost the evidence or the rationale: %+v", candidate)
		}
		if candidate.Status != "proposed" || candidate.Version != 1 {
			t.Fatalf("candidate state = %+v", candidate)
		}
	}

	// Idempotency: the same key replays the same batch.
	var replay struct {
		ID string `json:"id"`
	}
	response = postDependencyJSON(t, engine, http.MethodPost, "/api/projects/project-1/dependency-candidates", deliveryActorID, "candidate-batch-1", batchBody)
	candidateDataFrom(t, response, &replay)
	if replay.ID != batch.ID {
		t.Fatalf("the replay produced another batch: %s vs %s", replay.ID, batch.ID)
	}
	if !strings.Contains(response.Body.String(), `"replayed":true`) {
		t.Fatalf("the replay was not reported as a replay: %s", response.Body.String())
	}

	// A candidate is not an edge: the dependency graph is still empty.
	response = postDependencyJSON(t, engine, http.MethodGet, "/api/projects/project-1/dependencies", deliveryActorID, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list dependencies status = %d; body=%s", response.Code, response.Body.String())
	}
	var edges struct {
		Data []DependencyEdge `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &edges); err != nil {
		t.Fatalf("decode edges: %v", err)
	}
	if len(edges.Data) != 0 {
		t.Fatalf("candidates entered the dependency graph: %+v", edges.Data)
	}

	// The three states.
	decisionPath := "/api/projects/project-1/dependency-candidates/" + batch.Candidates[0].ID + "/decision"
	var confirmed struct {
		Candidate struct {
			Status string `json:"status"`
			EdgeID string `json:"edge_id"`
		} `json:"candidate"`
		Edge *DependencyEdge `json:"edge"`
	}
	response = postDependencyJSON(t, engine, http.MethodPost, decisionPath, deliveryActorID, "candidate-decide-confirm",
		`{"expected_version":1,"decision":"confirm","reason":"人工确认这条依赖"}`)
	candidateDataFrom(t, response, &confirmed)
	if confirmed.Candidate.Status != "confirmed" || confirmed.Edge == nil || confirmed.Edge.Status != "confirmed" {
		t.Fatalf("confirmed candidate = %+v", confirmed)
	}
	if confirmed.Edge.Reason != "人工确认这条依赖" || confirmed.Edge.Origin != "ai" {
		t.Fatalf("confirmed edge = %+v", confirmed.Edge)
	}

	secondPath := "/api/projects/project-1/dependency-candidates/" + batch.Candidates[1].ID + "/decision"
	var modified struct {
		Candidate struct {
			Status string `json:"status"`
		} `json:"candidate"`
		Edge *DependencyEdge `json:"edge"`
	}
	response = postDependencyJSON(t, engine, http.MethodPost, secondPath, deliveryActorID, "candidate-decide-modify",
		`{"expected_version":1,"decision":"modify","reason":"人工修正为强制复核","relation":"governs","action":"REVIEW_REQUIRED","confidence":0.9}`)
	candidateDataFrom(t, response, &modified)
	if modified.Candidate.Status != "modified" || modified.Edge == nil {
		t.Fatalf("modified candidate = %+v", modified)
	}
	if modified.Edge.Relation != "governs" || modified.Edge.Action != "REVIEW_REQUIRED" || modified.Edge.Confidence != 0.9 {
		t.Fatalf("modified edge lost the correction: %+v", modified.Edge)
	}
	if modified.Edge.SupersededRelation != "constrains" || modified.Edge.SupersededAction != "REGENERATE" {
		t.Fatalf("modified edge forgot the replaced tuple: %+v", modified.Edge)
	}

	// Only the decided candidates are in the formal dependency graph.
	response = postDependencyJSON(t, engine, http.MethodGet, "/api/projects/project-1/dependencies", deliveryActorID, "", "")
	if err := json.Unmarshal(response.Body.Bytes(), &edges); err != nil {
		t.Fatalf("decode edges: %v", err)
	}
	if len(edges.Data) != 2 {
		t.Fatalf("the dependency graph after the decisions = %+v", edges.Data)
	}
	for _, edge := range edges.Data {
		if edge.Status != "confirmed" {
			t.Fatalf("an undecided candidate reached the graph: %+v", edge)
		}
	}

	// The candidates read back with their human conclusions.
	response = postDependencyJSON(t, engine, http.MethodGet, "/api/projects/project-1/dependency-candidates", deliveryActorID, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list candidates status = %d; body=%s", response.Code, response.Body.String())
	}
	var listed struct {
		Data []struct {
			ID         string `json:"id"`
			Candidates []struct {
				ID       string `json:"id"`
				Status   string `json:"status"`
				Decision string `json:"decision"`
			} `json:"candidates"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode candidate list: %v", err)
	}
	if len(listed.Data) != 1 || len(listed.Data[0].Candidates) != 2 {
		t.Fatalf("candidate list = %+v", listed.Data)
	}
	decisions := map[string]string{}
	for _, candidate := range listed.Data[0].Candidates {
		decisions[candidate.ID] = candidate.Decision
	}
	if decisions[batch.Candidates[0].ID] != "confirm" || decisions[batch.Candidates[1].ID] != "modify" {
		t.Fatalf("candidate decisions = %+v", decisions)
	}
}

// A candidate whose basis moved on is not confirmed silently: the decision
// asks for a re-evaluation.
func TestDependencyCandidateDecisionRefusesAStaleBasis(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine, db, source, target := candidateFixtureEngine(t)
	sourceJSON := candidateRefJSON(source)
	targetJSON := candidateRefJSON(target)
	body := `{"expected_context_revision":1,"reason":"研究条件、证据卡和第 2-4 章","candidates":[` +
		`{"source":` + sourceJSON + `,"target":` + targetJSON + `,"relation":"supports","action":"REVALIDATE_EVIDENCE","evidence":"第 2 章结论引用了证据卡 E12","reason":"章节结论依赖该证据","confidence":0.6}]}`
	var batch struct {
		ID         string `json:"id"`
		Candidates []struct {
			ID string `json:"id"`
		} `json:"candidates"`
	}
	response := postDependencyJSON(t, engine, http.MethodPost, "/api/projects/project-1/dependency-candidates", deliveryActorID, "candidate-batch-stale", body)
	candidateDataFrom(t, response, &batch)
	if len(batch.Candidates) != 1 {
		t.Fatalf("candidate batch = %+v", batch)
	}

	// The target chapter moves on: the candidate's basis is behind.
	createdAt := time.Date(2026, time.October, 20, 9, 0, 0, 0, time.UTC)
	if err := db.Exec("INSERT INTO lingdoc_chapter_versions (id, project_id, chapter_id, body_markdown, source_ids_json, citation_usages_json, review_items_json, spec_revision, confirmation_valid, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		"version-moved", "project-1", target.ID, "新的章节正文", "[]", "[]", "[]", deliverySpecRevision, true, deliveryActorID, createdAt).Error; err != nil {
		t.Fatalf("insert chapter version: %v", err)
	}
	if err := db.Exec("UPDATE lingdoc_chapters SET current_version_id = ? WHERE id = ?", "version-moved", target.ID).Error; err != nil {
		t.Fatalf("move chapter version: %v", err)
	}

	response = postDependencyJSON(t, engine, http.MethodPost, "/api/projects/project-1/dependency-candidates/"+batch.Candidates[0].ID+"/decision", deliveryActorID, "candidate-decide-stale",
		`{"expected_version":1,"decision":"confirm","reason":"确认这条依赖"}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale decision status = %d; body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "dependency_candidate_requires_reevaluation") {
		t.Fatalf("stale decision body = %s", response.Body.String())
	}
	response = postDependencyJSON(t, engine, http.MethodGet, "/api/projects/project-1/dependencies", deliveryActorID, "", "")
	var edges struct {
		Data []DependencyEdge `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &edges); err != nil {
		t.Fatalf("decode edges: %v", err)
	}
	if len(edges.Data) != 0 {
		t.Fatalf("a stale candidate was confirmed anyway: %+v", edges.Data)
	}
}

// A candidate without its evidence is refused before anything is written.
func TestDependencyCandidateBatchRefusesACandidateWithoutEvidence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine, _, source, target := candidateFixtureEngine(t)
	sourceJSON := candidateRefJSON(source)
	targetJSON := candidateRefJSON(target)
	body := `{"expected_context_revision":1,"reason":"研究条件、证据卡和第 2-4 章","candidates":[` +
		`{"source":` + sourceJSON + `,"target":` + targetJSON + `,"relation":"supports","action":"REVALIDATE_EVIDENCE","reason":"没有依据片段","confidence":0.6}]}`
	response := postDependencyJSON(t, engine, http.MethodPost, "/api/projects/project-1/dependency-candidates", deliveryActorID, "candidate-batch-no-evidence", body)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	response = postDependencyJSON(t, engine, http.MethodGet, "/api/projects/project-1/dependency-candidates", deliveryActorID, "", "")
	if strings.Contains(response.Body.String(), `"candidates":[{"id"`) {
		t.Fatalf("a refused batch was written: %s", response.Body.String())
	}
}

// The published contract must describe the candidate routes, and the bytes the
// running server returns must satisfy the schema the contract advertises.
func TestDependencyCandidateContractMatchesTheRuntime(t *testing.T) {
	gin.SetMode(gin.TestMode)
	doc, compiler := contractDocument(t)
	engine, _, source, target := candidateFixtureEngine(t)
	contractBase := "/projects/{projectId}"

	// Every candidate route is published and served.
	registered := routedOperations(engine)
	for _, route := range []struct {
		method string
		path   string
	}{
		{"get", contractBase + "/dependency-candidates"},
		{"post", contractBase + "/dependency-candidates"},
		{"post", contractBase + "/dependency-candidates/{candidateId}/decision"},
	} {
		if _, ok := contractOperation(t, doc, route.method, route.path); !ok {
			t.Errorf("%s %s is published nowhere in the contract", route.method, route.path)
		}
		if !registered[route.method][route.path] {
			t.Errorf("%s %s is served nowhere", route.method, route.path)
		}
	}

	sourceJSON := candidateRefJSON(source)
	targetJSON := candidateRefJSON(target)
	body := `{"expected_context_revision":1,"reason":"研究条件、证据卡和第 2-4 章","scope":{"objects":[` +
		sourceJSON + `,` + targetJSON + `]},"candidates":[` +
		`{"source":` + sourceJSON + `,"target":` + targetJSON + `,"relation":"supports","action":"REVALIDATE_EVIDENCE","evidence":"第 2 章结论引用了证据卡 E12","reason":"章节结论依赖该证据","confidence":0.6}]}`
	response := postDependencyJSON(t, engine, http.MethodPost, "/api/projects/project-1/dependency-candidates", deliveryActorID, "candidate-contract-batch", body)
	if response.Code != http.StatusCreated {
		t.Fatalf("submit status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationResponse(t, doc, compiler, http.MethodPost, contractBase+"/dependency-candidates", "201", response.Body.Bytes())

	var batch struct {
		ID         string `json:"id"`
		Candidates []struct {
			ID string `json:"id"`
		} `json:"candidates"`
	}
	candidateDataFrom(t, response, &batch)
	if len(batch.Candidates) != 1 {
		t.Fatalf("candidate batch = %+v", batch)
	}

	response = postDependencyJSON(t, engine, http.MethodGet, "/api/projects/project-1/dependency-candidates", deliveryActorID, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationResponse(t, doc, compiler, http.MethodGet, contractBase+"/dependency-candidates", "200", response.Body.Bytes())

	response = postDependencyJSON(t, engine, http.MethodPost, "/api/projects/project-1/dependency-candidates/"+batch.Candidates[0].ID+"/decision", deliveryActorID, "candidate-contract-decision",
		`{"expected_version":1,"decision":"confirm","reason":"人工确认这条依赖"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("decision status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationResponse(t, doc, compiler, http.MethodPost, contractBase+"/dependency-candidates/{candidateId}/decision", "200", response.Body.Bytes())
}

// Proposing is not deciding: a machine principal may submit a batch, but it
// may not decide one.
func TestDependencyCandidateDecisionRefusesAMachinePrincipal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine, _, source, target := candidateFixtureEngine(t)
	sourceJSON := candidateRefJSON(source)
	targetJSON := candidateRefJSON(target)
	body := `{"expected_context_revision":1,"reason":"研究条件、证据卡和第 2-4 章","candidates":[` +
		`{"source":` + sourceJSON + `,"target":` + targetJSON + `,"relation":"supports","action":"REVALIDATE_EVIDENCE","evidence":"第 2 章结论引用了证据卡 E12","reason":"章节结论依赖该证据","confidence":0.6}]}`
	var batch struct {
		Candidates []struct {
			ID string `json:"id"`
		} `json:"candidates"`
	}
	response := postDependencyJSON(t, engine, http.MethodPost, "/api/projects/project-1/dependency-candidates", deliveryActorID, "candidate-batch-machine", body)
	candidateDataFrom(t, response, &batch)
	if len(batch.Candidates) != 1 {
		t.Fatalf("candidate batch = %+v", batch)
	}

	machine := types.WithTenantAPIKeyScope(actorContext(deliveryActorID), types.TenantAPIKeyScope{KeyID: 12, FullAccess: true})
	request := httptest.NewRequest(http.MethodPost, "/api/projects/project-1/dependency-candidates/"+batch.Candidates[0].ID+"/decision", strings.NewReader(`{"expected_version":1,"decision":"confirm","reason":"机器确认"}`)).WithContext(machine)
	request.Header.Set("Idempotency-Key", "candidate-machine-decide")
	recorded := httptest.NewRecorder()
	engine.ServeHTTP(recorded, request)
	if recorded.Code != http.StatusForbidden || !strings.Contains(recorded.Body.String(), "human_decision_required") {
		t.Fatalf("machine decision status = %d; body=%s", recorded.Code, recorded.Body.String())
	}
	response = postDependencyJSON(t, engine, http.MethodGet, "/api/projects/project-1/dependencies", deliveryActorID, "", "")
	var edges struct {
		Data []DependencyEdge `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &edges); err != nil {
		t.Fatalf("decode edges: %v", err)
	}
	if len(edges.Data) != 0 {
		t.Fatalf("a machine principal confirmed a candidate: %+v", edges.Data)
	}
}
