package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// The route-roadmap editing session over the real HTTP seam (#250).
//
// The fixture is the ordinary workspace fixture: one bound project with a
// bound template copy, the real GORM store and the real router. Nothing here
// calls the service directly, so the evidence covers the transport the
// acceptance console actually uses.

// ensureRoadmapTables creates the two tables the roadmap module owns. The
// workspace fixture builds its schema the same way the production migration
// chain does, so the fixture has to carry the roadmap tables as well.
func ensureRoadmapTables(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, ddl := range []string{
		`CREATE TABLE IF NOT EXISTS lingdoc_g3_roadmap_edit_sessions (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, status TEXT NOT NULL, baseline_json TEXT NOT NULL, node_proposals_json TEXT NOT NULL DEFAULT '[]', edge_proposals_json TEXT NOT NULL DEFAULT '[]', result_json TEXT NOT NULL DEFAULT '', actor_id TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS lingdoc_g3_route_edges (id TEXT PRIMARY KEY, project_id TEXT NOT NULL, edge_id TEXT NOT NULL, version INTEGER NOT NULL, source_id TEXT NOT NULL, target_id TEXT NOT NULL, relation TEXT NOT NULL, context_revision INTEGER NOT NULL DEFAULT 0, actor_id TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT '', created_at DATETIME NOT NULL)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatalf("roadmap fixture table: %v", err)
		}
	}
}

// seedRouteNode writes one frozen route node version into the shared
// versioned-record seam, the same shape the semantic-delta path writes.
func seedRouteNode(t *testing.T, db *gorm.DB, projectID, nodeID, name, status string) {
	t.Helper()
	node := core.RouteNode{ID: nodeID, ProjectID: projectID, Version: 1, Name: name, Status: status, Required: false,
		ContextRevision: 1, ActorID: "reader", Inputs: []core.ObjectVersionRef{}, Targets: []core.ObjectVersionRef{},
		Reason: "验收 合成路线", UpdatedAt: time.Now().UTC()}
	raw, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, row := range []struct{ kind, key string }{{"route_node", nodeID}, {"route_node_version", nodeID + "/1"}} {
		if err := db.Exec(`INSERT INTO lingdoc_g3_records(id,project_id,kind,record_key,version,body_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
			"http-"+row.kind+"-"+row.key, projectID, row.kind, row.key, 1, string(raw), now, now).Error; err != nil {
			t.Fatalf("seed route node %s: %v", nodeID, err)
		}
	}
}

type roadmapHTTPFixture struct {
	engine  *gin.Engine
	service *core.Service
	db      *gorm.DB
	project core.Project
}

func newRoadmapHTTPFixture(t *testing.T) roadmapHTTPFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	handler, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db,
		deliveryQuestionChapter(assetID, currentAssetRevision(t, handler, assetID), deliveryBody, []string{deliverySourceID}),
		deliveryMethodChapter(),
	)
	ensureImpactRouteTables(t, db)
	ensureRoadmapTables(t, db)
	if err := db.Exec("UPDATE lingdoc_projects SET current_context_revision = ? WHERE id = ?", 1, "project-1").Error; err != nil {
		t.Fatalf("seed context revision: %v", err)
	}
	seedDeliveryBoundTemplateCopy(t, db, "roadmap-http-copy")
	service, ok := testRuntime(handler).service.(*Service)
	if !ok {
		t.Fatalf("workspace handler service = %T, want *Service", testRuntime(handler).service)
	}
	engine := gin.New()
	RegisterRoadmapRoutes(engine.Group("/api"), impactPortsFor(service))
	RegisterImpactRoutes(engine.Group("/api"), impactPortsFor(service))
	project, err := service.GetProject(policyContext(), Actor{TenantID: 7, UserID: deliveryActorID}, "project-1")
	if err != nil {
		t.Fatalf("read fixture project: %v", err)
	}
	return roadmapHTTPFixture{engine: engine, service: service, db: db, project: project}
}

// roadmapContext is the caller context of an authenticated web user. The
// tenant role has to be in the context, because that is where the transport
// takes the caller's role from.
func roadmapContext() context.Context {
	ctx := policyContext()
	ctx = context.WithValue(ctx, types.TenantRoleContextKey, types.TenantRoleOwner)
	return context.WithValue(ctx, types.RequestIDContextKey, "roadmap-http")
}

func roadmapServe(t *testing.T, f roadmapHTTPFixture, method, path, body, key string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(roadmapContext())
	if key != "" {
		recorder.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	f.engine.ServeHTTP(response, recorder)
	return response
}

func roadmapData(t *testing.T, recorder *httptest.ResponseRecorder, target any) {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope %q: %v", recorder.Body.String(), err)
	}
	if err := json.Unmarshal(envelope.Data, target); err != nil {
		t.Fatalf("decode data %q: %v", string(envelope.Data), err)
	}
}

// The unfrozen editing session is not part of the frozen roadmap, a commit
// freezes a new version, and the version sequence and the comparison are
// readable afterwards.
func TestRoadmapHTTPCommitFreezesANewVersionAndCancelRestoresTheBaseline(t *testing.T) {
	f := newRoadmapHTTPFixture(t)
	seedRouteNode(t, f.db, f.project.ID, "http-node-1", "合成路线 http-node-1", "ready")

	begin := roadmapServe(t, f, http.MethodPost, "/api/projects/"+f.project.ID+"/roadmap/edits", `{"expected_context_revision":1,"reason":"编辑路线图"}`, "roadmap-http-begin")
	if begin.Code != http.StatusCreated {
		t.Fatalf("begin edit = %d, want 201: %s", begin.Code, begin.Body.String())
	}
	var session core.RoadmapEditSession
	roadmapData(t, begin, &session)
	if session.Status != core.RoadmapEditSessionOpen {
		t.Fatalf("session status = %q, want open", session.Status)
	}

	propose := roadmapServe(t, f, http.MethodPost, "/api/projects/"+f.project.ID+"/roadmap/edits/"+session.ID+"/nodes",
		`{"node_id":"http-node-1","kind":"update","name":"改写后的路线","status":"ready","reason":"改写节点文字"}`, "roadmap-http-propose")
	if propose.Code != http.StatusOK {
		t.Fatalf("propose node = %d, want 200: %s", propose.Code, propose.Body.String())
	}

	// The frozen roadmap did not move: an unfrozen proposal is not readable
	// as a roadmap version.
	frozen := roadmapServe(t, f, http.MethodGet, "/api/projects/"+f.project.ID+"/roadmap", "", "")
	if frozen.Code != http.StatusOK {
		t.Fatalf("read roadmap = %d, want 200: %s", frozen.Code, frozen.Body.String())
	}
	var baseline core.RoadmapBaseline
	roadmapData(t, frozen, &baseline)
	for _, node := range baseline.Nodes {
		if node.ID == "http-node-1" {
			if node.Version != 1 || node.Name != "合成路线 http-node-1" {
				t.Fatalf("the unfrozen proposal reached the frozen roadmap: %+v", node)
			}
		}
	}

	commit := roadmapServe(t, f, http.MethodPost, "/api/projects/"+f.project.ID+"/roadmap/edits/"+session.ID+"/commit",
		`{"expected_baseline_version":`+strconv.FormatInt(session.Baseline.Version, 10)+`,"reason":"提交路线图编辑"}`, "roadmap-http-commit")
	if commit.Code != http.StatusOK {
		t.Fatalf("commit = %d, want 200: %s", commit.Code, commit.Body.String())
	}
	var result core.RoadmapCommitResult
	roadmapData(t, commit, &result)
	if result.Semantic || len(result.AppliedNodes) != 1 || result.AppliedNodes[0].Version != 2 {
		t.Fatalf("commit result = %+v, want one frozen version 2", result)
	}

	versions := roadmapServe(t, f, http.MethodGet, "/api/projects/"+f.project.ID+"/roadmap/nodes/http-node-1/versions", "", "")
	if versions.Code != http.StatusOK {
		t.Fatalf("read versions = %d, want 200: %s", versions.Code, versions.Body.String())
	}
	var entries []core.RoadmapVersionEntry
	roadmapData(t, versions, &entries)
	if len(entries) != 2 || entries[0].Version != 1 || entries[1].Version != 2 {
		t.Fatalf("version sequence = %+v, want two versions", entries)
	}

	compare := roadmapServe(t, f, http.MethodGet, "/api/projects/"+f.project.ID+"/roadmap/nodes/http-node-1/compare?from=1&to=2", "", "")
	if compare.Code != http.StatusOK {
		t.Fatalf("compare = %d, want 200: %s", compare.Code, compare.Body.String())
	}
	var comparison core.RoadmapComparison
	roadmapData(t, compare, &comparison)
	if len(comparison.Changes) == 0 {
		t.Fatalf("comparison = %+v, want the name change", comparison)
	}

	// A second session, a proposal and a cancellation: the frozen roadmap
	// stays at the version it had, and nothing keeps pointing at the
	// temporary version the session proposed.
	current, err := f.service.GetProject(policyContext(), Actor{TenantID: 7, UserID: deliveryActorID}, f.project.ID)
	if err != nil {
		t.Fatalf("read project: %v", err)
	}
	second := roadmapServe(t, f, http.MethodPost, "/api/projects/"+f.project.ID+"/roadmap/edits",
		`{"expected_context_revision":`+strconv.FormatInt(current.CurrentContextRevision, 10)+`,"reason":"编辑路线图"}`, "roadmap-http-begin-2")
	if second.Code != http.StatusCreated {
		t.Fatalf("begin second edit = %d, want 201: %s", second.Code, second.Body.String())
	}
	var secondSession core.RoadmapEditSession
	roadmapData(t, second, &secondSession)
	proposeSecond := roadmapServe(t, f, http.MethodPost, "/api/projects/"+f.project.ID+"/roadmap/edits/"+secondSession.ID+"/nodes",
		`{"node_id":"http-node-1","kind":"update","name":"被取消的路线","status":"ready","reason":"改写节点文字"}`, "roadmap-http-propose-2")
	if proposeSecond.Code != http.StatusOK {
		t.Fatalf("propose second = %d, want 200: %s", proposeSecond.Code, proposeSecond.Body.String())
	}
	cancel := roadmapServe(t, f, http.MethodPost, "/api/projects/"+f.project.ID+"/roadmap/edits/"+secondSession.ID+"/cancel",
		`{"expected_baseline_version":`+strconv.FormatInt(secondSession.Baseline.Version, 10)+`,"reason":"取消编辑"}`, "roadmap-http-cancel")
	if cancel.Code != http.StatusOK {
		t.Fatalf("cancel = %d, want 200: %s", cancel.Code, cancel.Body.String())
	}
	var cancelled core.RoadmapEditSession
	roadmapData(t, cancel, &cancelled)
	if cancelled.Status != core.RoadmapEditSessionCancelled {
		t.Fatalf("cancelled session status = %q", cancelled.Status)
	}

	after := roadmapServe(t, f, http.MethodGet, "/api/projects/"+f.project.ID+"/roadmap", "", "")
	var restored core.RoadmapBaseline
	roadmapData(t, after, &restored)
	for _, node := range restored.Nodes {
		if node.ID == "http-node-1" {
			if node.Version != 2 || node.Name != "改写后的路线" {
				t.Fatalf("the cancelled proposal reached the frozen roadmap: %+v", node)
			}
		}
	}

	// No impact task points at the cancelled temporary version.
	tasks := roadmapServe(t, f, http.MethodGet, "/api/projects/"+f.project.ID+"/impact-tasks", "", "")
	if tasks.Code != http.StatusOK {
		t.Fatalf("list impact tasks = %d, want 200: %s", tasks.Code, tasks.Body.String())
	}
	var impactTasks []core.ImpactTask
	roadmapData(t, tasks, &impactTasks)
	for _, task := range impactTasks {
		if task.Target.Kind == "RouteNode" && task.Target.ID == "http-node-1" && task.Target.Version == "3" {
			t.Fatalf("an impact task points at the cancelled temporary version: %+v", task)
		}
		for _, evidence := range task.ClosureEvidence {
			if evidence.Kind == "RouteNode" && evidence.ID == "http-node-1" && evidence.Version == "3" {
				t.Fatalf("closure evidence points at the cancelled temporary version: %+v", task)
			}
		}
	}
}

// A semantic change does not reach the roadmap over HTTP either: the commit
// answers with a change set instead.
func TestRoadmapHTTPSemanticCommitAnswersWithAChangeSet(t *testing.T) {
	f := newRoadmapHTTPFixture(t)
	begin := roadmapServe(t, f, http.MethodPost, "/api/projects/"+f.project.ID+"/roadmap/edits", `{"expected_context_revision":1,"reason":"编辑路线图"}`, "roadmap-http-begin-3")
	if begin.Code != http.StatusCreated {
		t.Fatalf("begin edit = %d, want 201: %s", begin.Code, begin.Body.String())
	}
	var session core.RoadmapEditSession
	roadmapData(t, begin, &session)
	propose := roadmapServe(t, f, http.MethodPost, "/api/projects/"+f.project.ID+"/roadmap/edits/"+session.ID+"/nodes",
		`{"node_id":"http-node-2","kind":"add","name":"新增路线节点","status":"planned","reason":"新增节点"}`, "roadmap-http-propose-3")
	if propose.Code != http.StatusOK {
		t.Fatalf("propose node = %d, want 200: %s", propose.Code, propose.Body.String())
	}
	commit := roadmapServe(t, f, http.MethodPost, "/api/projects/"+f.project.ID+"/roadmap/edits/"+session.ID+"/commit",
		`{"expected_baseline_version":`+strconv.FormatInt(session.Baseline.Version, 10)+`,"reason":"提交路线图编辑"}`, "roadmap-http-commit-3")
	if commit.Code != http.StatusOK {
		t.Fatalf("commit = %d, want 200: %s", commit.Code, commit.Body.String())
	}
	var result core.RoadmapCommitResult
	roadmapData(t, commit, &result)
	if !result.Semantic || result.ChangeSetID == "" {
		t.Fatalf("commit result = %+v, want a change set", result)
	}
	frozen := roadmapServe(t, f, http.MethodGet, "/api/projects/"+f.project.ID+"/roadmap", "", "")
	var baseline core.RoadmapBaseline
	roadmapData(t, frozen, &baseline)
	for _, node := range baseline.Nodes {
		if node.ID == "http-node-2" {
			t.Fatalf("a semantic change landed in the roadmap directly: %+v", node)
		}
	}
}

// The roadmap routes need a human principal and a project the caller may
// write.
func TestRoadmapHTTPWriteRefusesAMachinePrincipal(t *testing.T) {
	f := newRoadmapHTTPFixture(t)
	request := httptest.NewRequest(http.MethodPost, "/api/projects/"+f.project.ID+"/roadmap/edits",
		strings.NewReader(`{"expected_context_revision":1,"reason":"编辑路线图"}`)).WithContext(machineContext())
	request.Header.Set("Idempotency-Key", "roadmap-http-machine")
	response := httptest.NewRecorder()
	f.engine.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("machine principal = %d, want 403: %s", response.Code, response.Body.String())
	}
}

// machineContext is a caller context with an API principal: a machine
// caller, not a person.
func machineContext() context.Context {
	ctx := roadmapContext()
	return types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalAPITenant, ID: "reader"})
}
