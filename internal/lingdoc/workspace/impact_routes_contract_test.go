package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"gorm.io/gorm"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// Acceptance: the impact/trial routes and the OpenAPI contract had only been
// checked against each other one path at a time, by hand, in earlier tickets.
// Nothing proved that (a) every route the server registers is published, (b)
// every published impact operation is actually reachable, or (c) the bytes the
// server really returns satisfy the schema the contract advertises. This file
// closes that loop in both directions against the running router.

const publishedResourceFor = "urn:lingdoc:openapi"

// contractDocument loads the published contract and compiles it once per test.
func contractDocument(t *testing.T) (map[string]any, *jsonschema.Compiler) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "08-本轮实施方案", "contracts", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	// The contract is a repository artifact, not untrusted input: resolve its
	// internal $refs but never fetch anything remote.
	compiler.UseLoader(nil)
	if err := compiler.AddResource(publishedResourceFor, doc); err != nil {
		t.Fatalf("register OpenAPI document: %v", err)
	}
	return doc, compiler
}

// contractOperation returns the OpenAPI operation object for a method+path.
func contractOperation(t *testing.T, doc map[string]any, method, path string) (map[string]any, bool) {
	t.Helper()
	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		t.Fatal("contract has no paths object")
	}
	item, ok := paths[path].(map[string]any)
	if !ok {
		return nil, false
	}
	operation, ok := item[strings.ToLower(method)].(map[string]any)
	return operation, ok
}

var ginParamFor = regexp.MustCompile(`:([A-Za-z0-9_]+)`)

// publishedPathFor converts a registered gin path into its published contract
// form, so both sides are compared on the same vocabulary instead of on a
// hand-maintained list.
func publishedPathFor(registered string) string {
	path := registered
	if trimmed := strings.TrimPrefix(path, "/api"); trimmed != path {
		path = trimmed
	}
	return ginParamFor.ReplaceAllString(path, "{$1}")
}

// routedOperations returns the method -> published path pairs the running
// server actually serves.
func routedOperations(engine *gin.Engine) map[string]map[string]bool {
	routes := engine.Routes()
	result := make(map[string]map[string]bool)
	for _, route := range routes {
		method := strings.ToLower(route.Method)
		if result[method] == nil {
			result[method] = map[string]bool{}
		}
		result[method][publishedPathFor(route.Path)] = true
	}
	return result
}

// validateOperationRequest compiles the request schema the contract publishes
// for a method+path and validates a real request body against it, so a request
// schema that drifted from the Go input type fails here rather than in a
// generated client.
func validateOperationRequest(t *testing.T, doc map[string]any, compiler *jsonschema.Compiler, method, path string, body []byte) {
	t.Helper()
	operation, ok := contractOperation(t, doc, method, path)
	if !ok {
		t.Fatalf("%s %s is published nowhere in the contract", strings.ToUpper(method), path)
	}
	requestBody, ok := operation["requestBody"].(map[string]any)
	if !ok {
		t.Fatalf("%s %s publishes no request body", strings.ToUpper(method), path)
	}
	media, ok := requestBody["content"].(map[string]any)["application/json"].(map[string]any)
	if !ok {
		t.Fatalf("%s %s has no application/json request schema", strings.ToUpper(method), path)
	}
	ref, ok := media["schema"].(map[string]any)["$ref"].(string)
	if !ok {
		t.Fatalf("%s %s uses an inline request schema; this contract check resolves $refs", strings.ToUpper(method), path)
	}
	schema, err := compiler.Compile(publishedResourceFor + ref)
	if err != nil {
		t.Fatalf("compile %s: %v", ref, err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("%s %s sent unparsable JSON: %v", strings.ToUpper(method), path, err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("%s %s request does not satisfy %s:\n%s", strings.ToUpper(method), path, ref, string(body))
	}
}

// validateOperationResponse compiles the response schema the contract
// publishes for a method+path+status and validates a real response body
// against it, so a schema drift fails here rather than in a generated client.
func validateOperationResponse(t *testing.T, doc map[string]any, compiler *jsonschema.Compiler, method, path, status string, body []byte) {
	t.Helper()
	operation, ok := contractOperation(t, doc, method, path)
	if !ok {
		t.Fatalf("%s %s is published nowhere in the contract", strings.ToUpper(method), path)
	}
	responses, ok := operation["responses"].(map[string]any)
	if !ok {
		t.Fatalf("%s %s publishes no responses object", strings.ToUpper(method), path)
	}
	entry, ok := responses[status].(map[string]any)
	if !ok {
		t.Fatalf("%s %s publishes no %s response", strings.ToUpper(method), path, status)
	}
	media, ok := entry["content"].(map[string]any)["application/json"].(map[string]any)
	if !ok {
		t.Fatalf("%s %s %s has no application/json schema", strings.ToUpper(method), path, status)
	}
	ref, ok := media["schema"].(map[string]any)["$ref"].(string)
	if !ok {
		t.Fatalf("%s %s %s uses an inline schema; this contract check resolves $refs", strings.ToUpper(method), path, status)
	}
	schema, err := compiler.Compile(publishedResourceFor + ref)
	if err != nil {
		t.Fatalf("compile %s: %v", ref, err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("%s %s returned unparsable JSON: %v", strings.ToUpper(method), path, err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("%s %s response does not satisfy %s: %v\n%s", strings.ToUpper(method), path, ref, err, string(body))
	}
}

// g3203Engine builds the router over a real SQLite-backed workspace service,
// exactly the way the application registers it.
type routeContractFixture struct {
	engine   *gin.Engine
	service  *core.Service
	db       *gorm.DB
	project  Project
	chapters []Chapter
}

func newRouteContractFixture(t *testing.T) routeContractFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	handler, db, assetID := seedBoundSource(t)
	seedDeliveryWorkspace(t, db,
		deliveryQuestionChapter(assetID, currentAssetRevision(t, handler, assetID), deliveryBody, []string{deliverySourceID}),
		deliveryMethodChapter(),
	)
	ensureImpactRouteTables(t, db)
	// The recommendation trigger writes a candidate batch, so the candidate
	// tables the focused fixtures build are part of this fixture too. It also
	// reads the evidence-card object store: the production schema for that
	// table comes from the migrations (000037_lingdoc_full_workflow), and the
	// copy below is the focused HTTP fixture's aligned subset, matching the
	// candidate fixture's convention.
	ensureDependencyCandidateTables(t, db)
	if err := db.Exec(`CREATE TABLE IF NOT EXISTS lingdoc_g2_objects(project_id TEXT NOT NULL,kind TEXT NOT NULL,id TEXT NOT NULL,version INTEGER NOT NULL,payload_json TEXT NOT NULL,PRIMARY KEY(project_id,kind,id))`).Error; err != nil {
		t.Fatalf("seed g2 objects table: %v", err)
	}
	if err := db.Exec("UPDATE lingdoc_projects SET current_context_revision = ? WHERE id = ?", 1, "project-1").Error; err != nil {
		t.Fatalf("seed context revision: %v", err)
	}
	seedDeliveryBoundTemplateCopy(t, db, "accept-copy")
	service, ok := testRuntime(handler).projects.(*core.Service)
	if !ok {
		t.Fatalf("workspace handler service = %T, want *core.Service", testRuntime(handler).projects)
	}
	engine := gin.New()
	RegisterImpactRoutes(engine.Group("/api"), impactPortsFor(service))
	project, err := service.GetProject(policyContext(), Actor{TenantID: 7, UserID: deliveryActorID}, "project-1")
	if err != nil {
		t.Fatalf("read fixture project: %v", err)
	}
	chapters, err := service.ListChapters(policyContext(), Actor{TenantID: 7, UserID: deliveryActorID}, project.ID)
	if err != nil {
		t.Fatalf("read fixture chapters: %v", err)
	}
	return routeContractFixture{engine: engine, service: service, db: db, project: project, chapters: chapters}
}

// createChangeForAssessment creates a real ChangeSet without applying it and
// returns its ID. CreateChangeSet assesses the change as part of the same
// transaction, so the stored assessment is genuine production output.
//
// The caller must read the assessment before applying: ApplyChangeSet advances
// the context revision, after which the server answers GET impact-assessment
// with 409 version_conflict because the stored input_hash no longer describes
// the current baseline. That refusal is the designed behaviour, so the read
// below sits ahead of the apply rather than working around it.
func createChangeForAssessment(t *testing.T, f routeContractFixture, chapterID, key string) string {
	t.Helper()
	actor := Actor{TenantID: 7, UserID: deliveryActorID}
	p, err := f.service.GetProject(policyContext(), actor, f.project.ID)
	if err != nil {
		t.Fatalf("read project: %v", err)
	}
	raw, _, _, err := f.service.CreateChangeSet(policyContext(), actor, p.ID, key, core.CreateChangeSetInput{
		ExpectedContextRevision: p.CurrentContextRevision,
		Fields:                  map[string]core.ChangeFieldInput{"research_subject": {OldValue: p.Spec["research_subject"], NewValue: "验收 合同校验主题"}},
		AffectedChapterIDs:      []string{chapterID},
		Reason:                  "验收 合同校验",
	})
	if err != nil {
		t.Fatalf("create changeset: %v", err)
	}
	var change core.ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil {
		t.Fatal(err)
	}
	return change.ID
}

// applyChangeForAcceptance drives the real create+apply path so the published task
// payload comes from production commits, not from a hand-seeded row.
func applyChangeForAcceptance(t *testing.T, f routeContractFixture, chapterID string) string {
	t.Helper()
	change := createChangeForAssessment(t, f, chapterID, "accept-change")
	if _, _, _, err := f.service.ApplyChangeSet(policyContext(), Actor{TenantID: 7, UserID: deliveryActorID}, f.project.ID, change, "accept-apply"); err != nil {
		t.Fatalf("apply changeset: %v", err)
	}
	return change
}

func acceptanceTasks(t *testing.T, f routeContractFixture) []core.ImpactTask {
	t.Helper()
	tasks, err := f.service.ListImpactTasks(policyContext(), Actor{TenantID: 7, UserID: deliveryActorID}, f.project.ID)
	if err != nil {
		t.Fatalf("list impact tasks: %v", err)
	}
	return tasks
}

// impactOwnedPathSuffixes is the impact acceptance surface named by Spec #198,
// and it is the complete G3 published surface of RegisterImpactRoutes. Within
// this list the coverage is bidirectional and total: every registered path must
// be published, every published path must be served, and every response body
// must satisfy the schema (see the three tests below). `impact-assessment`
// joined this list in #207, which closed the gap #203 recorded here.
//
// The G6 runtime/trial families are registered by the same helper and used to
// be left out of this file as G6 debt. #471 closed that debt: they are checked
// by the same two directions (isG6OwnedPath below), with their own exact count,
// so the G6 segment and the contract cannot drift apart unnoticed either.
//
// `/assess` joined this list in #223. It was registered but unpublished, so a
// generated client had no way to trigger an assessment; the contract now
// publishes it and the bidirectional coverage below keeps it that way.
var impactOwnedPathSuffixes = []string{"/impact-tasks", "/impact-assessment", "/assess", "/dependencies", "/dependency-candidates", "/dependency-recommendations", "/stabilize", "/preflight", "/check-receipts"}

func isImpactOwnedPath(path string, owned []string) bool {
	for _, suffix := range owned {
		if strings.HasSuffix(path, suffix) || strings.Contains(path, suffix+"/") {
			return true
		}
	}
	return false
}

// roadmapOwnedOperationCount is the number of roadmap operations #250
// registered. The roadmap routes are not a suffix family (they are
// /roadmap, /roadmap/edges, /roadmap/nodes/{nodeId}/compare,
// /roadmap/edits/{sessionId}/commit, ...), so they get their own predicate
// instead of being folded into the impact suffix list.
//
// The count is asserted on its own in the two directions below: a filter that
// matches nothing would turn the coverage into a silent skip, which is the
// same defect class the `outputs:` whitelist had. The assertion is **exact
// equality**, not "at least": a filter that only matched part of the roadmap
// surface must fail too, otherwise a partial match still passes as coverage.
const roadmapOwnedOperationCount = 12

// isRoadmapOwnedPath tells whether a path belongs to the roadmap surface. Both
// the contract form ({projectId}) and the registered gin form (:projectId) are
// accepted, so the same predicate works on both sides of the comparison.
func isRoadmapOwnedPath(path string) bool {
	for _, prefix := range []string{"/projects/{projectId}/roadmap", "/projects/:projectId/roadmap"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// runtimeOwnedOperationCount is the number of runtime/trial operations #382/#383
// registered through RegisterImpactRoutes. Like the roadmap count, it is
// asserted as **exact equality** in both directions: a predicate that matched
// part of the family would otherwise read as coverage while leaving the rest
// unchecked, which is the same silent-skip defect the G6 debt itself was.
const runtimeOwnedOperationCount = 18

// isG6OwnedPath tells whether a path belongs to the G6 runtime/trial surface.
// It accepts the contract form ({projectId}) and the registered gin form
// (:projectId) for the same reason isRoadmapOwnedPath does.
//
// The prefix `/trial-` also covers `/trial-decisions/{decisionId}/sign`, which
// sits outside the `/trial-windows` subtree but belongs to the same family.
func isG6OwnedPath(path string) bool {
	for _, prefix := range []string{
		"/projects/{projectId}/runtime/", "/projects/:projectId/runtime/",
		"/projects/{projectId}/trial-", "/projects/:projectId/trial-",
	} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// Direction 1: every impact/dependency/stabilize/preflight route the server
// registers is published in the contract, and the same holds for the roadmap
// routes #250 added and for the G6 runtime/trial family #471 folded in here. An
// unpublished route is a route no generated client can call.
func TestEveryImpactRouteIsPublished(t *testing.T) {
	doc, _ := contractDocument(t)
	// The roadmap fixture registers the impact routes and the roadmap routes on
	// one router, so both surfaces are checked against the same contract.
	engine := newRoadmapHTTPFixture(t).engine
	registered := routedOperations(engine)
	var missing, checked []string
	roadmapChecked, g6Checked := 0, 0
	for method, paths := range registered {
		for path := range paths {
			roadmap := isRoadmapOwnedPath(path)
			g6 := isG6OwnedPath(path)
			if !roadmap && !g6 && !isImpactOwnedPath(path, impactOwnedPathSuffixes) {
				continue
			}
			checked = append(checked, strings.ToUpper(method)+" "+path)
			if roadmap {
				roadmapChecked++
			}
			if g6 {
				g6Checked++
			}
			if _, ok := contractOperation(t, doc, method, path); !ok {
				missing = append(missing, strings.ToUpper(method)+" "+path)
			}
		}
	}
	if len(checked) == 0 {
		t.Fatal("no impact routes were checked; the path filter matched nothing")
	}
	t.Logf("direction 1: %d registered operations checked, %d of them roadmap, %d runtime/trial", len(checked), roadmapChecked, g6Checked)
	sort.Strings(missing)
	for _, operation := range missing {
		t.Errorf("registered route %s is absent from the OpenAPI contract", operation)
	}
	if roadmapChecked != roadmapOwnedOperationCount {
		t.Fatalf("only %d roadmap operations were checked; the roadmap path filter did not match exactly the %d routes #250 registered", roadmapChecked, roadmapOwnedOperationCount)
	}
	if g6Checked != runtimeOwnedOperationCount {
		t.Fatalf("%d runtime/trial operations were checked; the G6 path filter did not match exactly the %d routes #382/#383 registered", g6Checked, runtimeOwnedOperationCount)
	}
}

// Direction 2: every published impact/dependency/stabilize/preflight
// operation is actually served, and the same holds for the published roadmap
// operations and for the published G6 runtime/trial operations. A contract path
// with no handler is a promise the server does not keep.
func TestEveryPublishedImpactOperationIsRouted(t *testing.T) {
	doc, _ := contractDocument(t)
	engine := newRoadmapHTTPFixture(t).engine
	registered := routedOperations(engine)

	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		t.Fatal("contract has no paths object")
	}
	// The impact surface, the roadmap surface and the G6 runtime/trial family
	// are all registered by the fixtures above; the G6 family joined this
	// direction in #471.
	owned := impactOwnedPathSuffixes
	checked := 0
	roadmapChecked := 0
	g6Checked := 0
	for path, item := range paths {
		if !strings.HasPrefix(path, "/projects/{projectId}/") {
			continue
		}
		roadmap := isRoadmapOwnedPath(path)
		g6 := isG6OwnedPath(path)
		if !roadmap && !g6 && !isImpactOwnedPath(path, owned) {
			continue
		}
		item, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for method := range item {
			checked++
			if roadmap {
				roadmapChecked++
			}
			if g6 {
				g6Checked++
			}
			if registered[strings.ToLower(method)][path] {
				continue
			}
			t.Errorf("contract publishes %s %s but no route serves it", strings.ToUpper(method), path)
		}
	}
	if checked == 0 {
		t.Fatal("no impact contract operations were checked; the path filter matched nothing")
	}
	if roadmapChecked != roadmapOwnedOperationCount {
		t.Fatalf("only %d roadmap contract operations were checked; the roadmap path filter did not match exactly the %d operations #297 published", roadmapChecked, roadmapOwnedOperationCount)
	}
	if g6Checked != runtimeOwnedOperationCount {
		t.Fatalf("only %d runtime/trial contract operations were checked; the G6 path filter did not match exactly the %d operations #382/#383 published", g6Checked, runtimeOwnedOperationCount)
	}
	t.Logf("direction 2: %d published operations checked, %d of them roadmap, %d runtime/trial", checked, roadmapChecked, g6Checked)
}

// Direction 3: the bytes the running server returns satisfy the schema the
// contract advertises, for each impact/dependency operation. This is what
// makes the contract usable for code generation rather than documentation.
func TestRuntimeResponsesSatisfyPublishedSchemas(t *testing.T) {
	doc, compiler := contractDocument(t)
	f := newRouteContractFixture(t)
	engine := f.engine

	send := func(method, path, userID, key, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(actorContext(userID))
		if key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		return response
	}
	base := "/api/projects/" + f.project.ID
	contractBase := "/projects/{projectId}"

	// The fixture already materialised two chapter versions; read one back so
	// the edge is created over a real current version.
	if len(f.chapters) < 2 {
		t.Fatalf("fixture produced %d chapters", len(f.chapters))
	}
	chapter := f.chapters[0]
	if chapter.CurrentVersionID == nil {
		t.Fatalf("fixture chapter %s has no current version", chapter.ID)
	}
	target := `{"kind":"ChapterVersion","id":"` + chapter.ID + `","version":"` + *chapter.CurrentVersionID + `"}`
	source := `{"kind":"ProjectSpec","id":"` + f.project.ID + `","version":"1"}`

	// GET /impact-tasks (empty projection is still a contract-shaped payload).
	response := send(http.MethodGet, base+"/impact-tasks", "reader", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list impact tasks status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationResponse(t, doc, compiler, http.MethodGet, contractBase+"/impact-tasks", "200", response.Body.Bytes())

	// POST /dependencies then the decision route, including the `modify`
	// decision #199 added.
	response = send(http.MethodPost, base+"/dependencies", "reader", "accept-edge",
		`{"expected_context_revision":1,"source":`+source+`,"target":`+target+`,"relation":"supports","action":"REVALIDATE_EVIDENCE","origin":"ai","reason":"模型候选","confidence":0.2}`)
	if response.Code != http.StatusOK && response.Code != http.StatusCreated {
		t.Fatalf("create dependency status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationResponse(t, doc, compiler, http.MethodPost, contractBase+"/dependencies", "201", response.Body.Bytes())
	var created struct {
		Data DependencyEdge `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created edge: %v", err)
	}

	response = send(http.MethodGet, base+"/dependencies", "reader", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list dependencies status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationResponse(t, doc, compiler, http.MethodGet, contractBase+"/dependencies", "200", response.Body.Bytes())

	// The decision route, including the `modify` decision #199 added. This runs
	// before the ChangeSet is applied on purpose: applying one advances the
	// context revision, after which the edge's recorded basis is superseded.
	response = send(http.MethodPost, base+"/dependencies/"+created.Data.ID+"/decision", "reader", "accept-decide-modify",
		`{"expected_version":1,"decision":"modify","reason":"人工修正","relation":"constrains","action":"REGENERATE","confidence":0.9}`)
	if response.Code != http.StatusOK {
		t.Fatalf("modify decision status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationResponse(t, doc, compiler, http.MethodPost, contractBase+"/dependencies/{edgeId}/decision", "200", response.Body.Bytes())

	// #751: the recommendation trigger produces the batch/dropped/model
	// payload a generated client has to understand. The fixture service gets
	// a stub recommender so the route runs its real seam without contacting a
	// provider.
	f.service.SetDependencyRecommendationModel(&contractRecommendationStub{
		proposals: []core.ModelProposedDependency{{
			SourceKind: "ProjectSpec", SourceID: f.project.ID,
			TargetKind: "ChapterVersion", TargetID: f.chapters[0].ID,
			Relation: "uses", Action: "REGENERATE",
			Evidence: "契约校验：章节正文引用研究条件", Reason: "研究条件变化需要重生成",
			Confidence: 0.6,
		}},
	})
	recommendBody := `{"reason":"契约校验 冷启动推荐"}`
	response = send(http.MethodPost, base+"/dependency-recommendations", "reader", "accept-recommend", recommendBody)
	if response.Code != http.StatusCreated {
		t.Fatalf("recommend dependencies status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationRequest(t, doc, compiler, http.MethodPost, contractBase+"/dependency-recommendations", []byte(recommendBody))
	validateOperationResponse(t, doc, compiler, http.MethodPost, contractBase+"/dependency-recommendations", "201", response.Body.Bytes())

	// #207: the assessment route is published, so what the running server
	// returns for it must satisfy the published ImpactAssessment schema. Path
	// existence alone would not catch a field the server emits but the contract
	// omits (exactly the drift the TS type hid), nor a required field the
	// contract promises but no real run ever produces.
	assessed := createChangeForAssessment(t, f, chapter.ID, "accept-assess")
	response = send(http.MethodGet, base+"/change-sets/"+assessed+"/impact-assessment", "reader", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("impact assessment status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationResponse(t, doc, compiler, http.MethodGet, contractBase+"/change-sets/{changeSetId}/impact-assessment", "200", response.Body.Bytes())

	// The ImpactTask payload #200 extended with STALE/reason must be
	// representable against the published schema, so the task list is
	// revalidated after a real commit produced it.
	change := applyChangeForAcceptance(t, f, chapter.ID)

	tasks := acceptanceTasks(t, f)
	if len(tasks) == 0 {
		t.Fatalf("the applied change raised no impact task to publish: %s", change)
	}
	task := tasks[0]

	response = send(http.MethodGet, base+"/impact-tasks", "reader", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list impact tasks status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationResponse(t, doc, compiler, http.MethodGet, contractBase+"/impact-tasks", "200", response.Body.Bytes())

	response = send(http.MethodPost, base+"/impact-tasks/"+task.ID+"/actions", "reader", "accept-task-assign",
		`{"expected_version":`+strconv.FormatInt(task.Version, 10)+`,"target_revision":`+strconv.FormatInt(task.TargetRevision, 10)+
			`,"action":"assign","assignee_id":"reader","reason":"验收 指派"}`)
	if response.Code != http.StatusOK && response.Code != http.StatusCreated {
		t.Fatalf("task action status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationResponse(t, doc, compiler, http.MethodPost, contractBase+"/impact-tasks/{taskId}/actions", "200", response.Body.Bytes())

	// Stabilize publishes the server's own verdict; BLOCKED is the real answer
	// for this fixture, and it must satisfy the published schema.
	response = send(http.MethodPost, base+"/change-sets/"+change+"/stabilize", "reader", "accept-stabilize", `{}`)
	if response.Code != http.StatusOK {
		t.Fatalf("stabilize status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationResponse(t, doc, compiler, http.MethodPost, contractBase+"/change-sets/{changeSetId}/stabilize", "200", response.Body.Bytes())

	// #297: the roadmap routes #250 registered are published in the contract,
	// so the bytes the running server returns for them have to satisfy the
	// published schemas as well. Path existence alone would not catch a field
	// the server emits but the contract omits.
	//
	// The roadmap writes are guarded by the human-decision check, so these
	// requests carry the caller's owner role instead of the plain actor
	// context the impact calls above use.
	roadmap := newRoadmapHTTPFixture(t)
	seedRouteNode(t, roadmap.db, roadmap.project.ID, "contract-node-1", "合成路线 contract-node-1", "ready")
	// The edge proposal needs both of its ends to be route nodes that exist.
	seedRouteNode(t, roadmap.db, roadmap.project.ID, "contract-node-2", "合成路线 contract-node-2", "ready")
	seedContractRouteEdge(t, roadmap.db, roadmap.project.ID, "contract-edge-1", "contract-node-1", "contract-node-2")

	roadmapSend := func(method, path, key, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(roadmapContext())
		if key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
		response := httptest.NewRecorder()
		roadmap.engine.ServeHTTP(response, request)
		return response
	}
	roadmapBase := "/api/projects/" + roadmap.project.ID

	// Every roadmap operation is validated against its published schema, and
	// the count of distinct operations is asserted, so a response check that
	// silently stopped running cannot leave the roadmap surface unverified.
	roadmapResponses := map[string]bool{}
	validateRoadmapResponse := func(method, path, status string, body []byte) {
		t.Helper()
		validateOperationResponse(t, doc, compiler, method, path, status, body)
		roadmapResponses[strings.ToUpper(method)+" "+path+" "+status] = true
	}

	// The frozen roadmap and its edges.
	response = roadmapSend(http.MethodGet, roadmapBase+"/roadmap", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("read roadmap status = %d; body=%s", response.Code, response.Body.String())
	}
	validateRoadmapResponse(http.MethodGet, contractBase+"/roadmap", "200", response.Body.Bytes())

	response = roadmapSend(http.MethodGet, roadmapBase+"/roadmap/edges", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("read roadmap edges status = %d; body=%s", response.Code, response.Body.String())
	}
	validateRoadmapResponse(http.MethodGet, contractBase+"/roadmap/edges", "200", response.Body.Bytes())

	response = roadmapSend(http.MethodGet, roadmapBase+"/roadmap/edges/contract-edge-1/versions", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("read roadmap edge versions status = %d; body=%s", response.Code, response.Body.String())
	}
	validateRoadmapResponse(http.MethodGet, contractBase+"/roadmap/edges/{edgeId}/versions", "200", response.Body.Bytes())

	// The editing session: open it, propose a node change, and commit it. The
	// request bodies are validated against the published request schemas too,
	// so a request schema that drifted from the Go input type fails here.
	beginBody := `{"expected_context_revision":1,"reason":"契约校验 打开编辑会话"}`
	response = roadmapSend(http.MethodPost, roadmapBase+"/roadmap/edits", "contract-roadmap-begin", beginBody)
	if response.Code != http.StatusCreated {
		t.Fatalf("begin roadmap edit status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationRequest(t, doc, compiler, http.MethodPost, contractBase+"/roadmap/edits", []byte(beginBody))
	validateRoadmapResponse(http.MethodPost, contractBase+"/roadmap/edits", "201", response.Body.Bytes())
	var opened struct {
		Data core.RoadmapEditSession `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &opened); err != nil {
		t.Fatalf("decode roadmap session: %v", err)
	}
	session := opened.Data
	if session.ID == "" {
		t.Fatalf("the roadmap session has no id: %s", response.Body.String())
	}

	proposeBody := `{"node_id":"contract-node-1","kind":"update","name":"契约校验改写","status":"ready","inputs":[],"targets":[],"reason":"契约校验 改写节点"}`
	response = roadmapSend(http.MethodPost, roadmapBase+"/roadmap/edits/"+session.ID+"/nodes", "contract-roadmap-propose", proposeBody)
	if response.Code != http.StatusOK {
		t.Fatalf("propose roadmap node status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationRequest(t, doc, compiler, http.MethodPost, contractBase+"/roadmap/edits/{sessionId}/nodes", []byte(proposeBody))
	validateRoadmapResponse(http.MethodPost, contractBase+"/roadmap/edits/{sessionId}/nodes", "200", response.Body.Bytes())

	commitBody := `{"expected_baseline_version":` + strconv.FormatInt(session.Baseline.Version, 10) + `,"reason":"契约校验 提交编辑"}`
	response = roadmapSend(http.MethodPost, roadmapBase+"/roadmap/edits/"+session.ID+"/commit", "contract-roadmap-commit", commitBody)
	if response.Code != http.StatusOK {
		t.Fatalf("commit roadmap edit status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationRequest(t, doc, compiler, http.MethodPost, contractBase+"/roadmap/edits/{sessionId}/commit", []byte(commitBody))
	validateRoadmapResponse(http.MethodPost, contractBase+"/roadmap/edits/{sessionId}/commit", "200", response.Body.Bytes())

	// The frozen versions and their comparison are readable after the commit.
	response = roadmapSend(http.MethodGet, roadmapBase+"/roadmap/nodes/contract-node-1/versions", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("read roadmap node versions status = %d; body=%s", response.Code, response.Body.String())
	}
	validateRoadmapResponse(http.MethodGet, contractBase+"/roadmap/nodes/{nodeId}/versions", "200", response.Body.Bytes())

	response = roadmapSend(http.MethodGet, roadmapBase+"/roadmap/nodes/contract-node-1/compare?from=1&to=2", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("compare roadmap node versions status = %d; body=%s", response.Code, response.Body.String())
	}
	validateRoadmapResponse(http.MethodGet, contractBase+"/roadmap/nodes/{nodeId}/compare", "200", response.Body.Bytes())

	// The session list and one session.
	response = roadmapSend(http.MethodGet, roadmapBase+"/roadmap/edits", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list roadmap sessions status = %d; body=%s", response.Code, response.Body.String())
	}
	validateRoadmapResponse(http.MethodGet, contractBase+"/roadmap/edits", "200", response.Body.Bytes())

	response = roadmapSend(http.MethodGet, roadmapBase+"/roadmap/edits/"+session.ID, "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("read roadmap session status = %d; body=%s", response.Code, response.Body.String())
	}
	validateRoadmapResponse(http.MethodGet, contractBase+"/roadmap/edits/{sessionId}", "200", response.Body.Bytes())

	// A second session proposes an edge and is cancelled: the frozen roadmap
	// stays where it was, and the cancelled proposals never reach it.
	current, err := roadmap.service.GetProject(policyContext(), Actor{TenantID: 7, UserID: deliveryActorID}, roadmap.project.ID)
	if err != nil {
		t.Fatalf("read project: %v", err)
	}
	secondBody := `{"expected_context_revision":` + strconv.FormatInt(current.CurrentContextRevision, 10) + `,"reason":"契约校验 第二次会话"}`
	response = roadmapSend(http.MethodPost, roadmapBase+"/roadmap/edits", "contract-roadmap-begin-2", secondBody)
	if response.Code != http.StatusCreated {
		t.Fatalf("begin second roadmap edit status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationRequest(t, doc, compiler, http.MethodPost, contractBase+"/roadmap/edits", []byte(secondBody))
	validateRoadmapResponse(http.MethodPost, contractBase+"/roadmap/edits", "201", response.Body.Bytes())
	var second struct {
		Data core.RoadmapEditSession `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode second roadmap session: %v", err)
	}

	edgeBody := `{"edge_id":"contract-edge-2","kind":"add","source_id":"contract-node-1","target_id":"contract-node-2","relation":"supports","reason":"契约校验 新增路线边"}`
	response = roadmapSend(http.MethodPost, roadmapBase+"/roadmap/edits/"+second.Data.ID+"/edges", "contract-roadmap-propose-edge", edgeBody)
	if response.Code != http.StatusOK {
		t.Fatalf("propose roadmap edge status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationRequest(t, doc, compiler, http.MethodPost, contractBase+"/roadmap/edits/{sessionId}/edges", []byte(edgeBody))
	validateRoadmapResponse(http.MethodPost, contractBase+"/roadmap/edits/{sessionId}/edges", "200", response.Body.Bytes())

	cancelBody := `{"expected_baseline_version":` + strconv.FormatInt(second.Data.Baseline.Version, 10) + `,"reason":"契约校验 取消编辑"}`
	response = roadmapSend(http.MethodPost, roadmapBase+"/roadmap/edits/"+second.Data.ID+"/cancel", "contract-roadmap-cancel", cancelBody)
	if response.Code != http.StatusOK {
		t.Fatalf("cancel roadmap edit status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationRequest(t, doc, compiler, http.MethodPost, contractBase+"/roadmap/edits/{sessionId}/cancel", []byte(cancelBody))
	validateRoadmapResponse(http.MethodPost, contractBase+"/roadmap/edits/{sessionId}/cancel", "200", response.Body.Bytes())

	if len(roadmapResponses) != roadmapOwnedOperationCount {
		t.Fatalf("%d roadmap operations were validated against the published schemas; the roadmap surface has %d published operations", len(roadmapResponses), roadmapOwnedOperationCount)
	}
	t.Logf("direction 3: %d roadmap operations validated against the published schemas", len(roadmapResponses))
}

// Direction 3 for the G6 runtime/trial family (#471): the two endpoints #382
// added and the #383 rollback are reachable over HTTP, and the bytes both sides
// exchange satisfy the schemas the contract advertises.
//
// Path existence alone (direction 2) would not catch a request field the Go
// input type accepts but the contract forbids, a response field the handler
// emits but the contract omits, or an error body whose shape drifted from the
// published ErrorResponse. Those are exactly the drifts a generated client
// breaks on, so each case below validates the real request and the real
// response body, not the handler in isolation.
//
// The same fixture and the same validators as the impact direction 3 are used
// on purpose: one contract document, one comparator, two families.
func TestRuntimeRoutesSatisfyPublishedSchemas(t *testing.T) {
	doc, compiler := contractDocument(t)
	f := newRouteContractFixture(t)
	base := "/api/projects/" + f.project.ID
	contractBase := "/projects/{projectId}"

	send := func(ctx context.Context, method, path, key, body string) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
		if key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
		response := httptest.NewRecorder()
		f.engine.ServeHTTP(response, request)
		return response
	}
	human := func(method, path, key, body string) *httptest.ResponseRecorder {
		return send(actorContext("reader"), method, path, key, body)
	}
	// requireError asserts the published status and code of a refusal, and
	// validates the refusal body against the schema the contract publishes for
	// that status.
	requireError := func(response *httptest.ResponseRecorder, method, path string, status int, code string) {
		t.Helper()
		if response.Code != status {
			t.Fatalf("%s %s = %d, want %d: %s", method, path, response.Code, status, response.Body.String())
		}
		// A body that is not JSON at all means the router answered, not the
		// handler: gin's own 404 page is text. Say so instead of letting the
		// schema compiler report "unparsable JSON", which reads like a contract
		// defect rather than a missing route.
		var envelope map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("%s %s answered %d with a non-JSON body (%s); no handler matched this route?", method, path, status, response.Body.String())
		}
		validateOperationResponse(t, doc, compiler, method, path, strconv.Itoa(status), response.Body.Bytes())
		failure, ok := envelope["error"].(map[string]any)
		if !ok {
			t.Fatalf("%s %s refusal has no error object: %s", method, path, response.Body.String())
		}
		if failure["code"] != code {
			t.Fatalf("%s %s refusal = %s, want code %q", method, path, response.Body.String(), code)
		}
	}

	// requireHumanDecisionRefusal asserts the 403 the identity guard returns.
	//
	// This one refusal cannot go through requireError: the guard answers with
	// sendErrorDetails, which always emits `details` (null when it has none),
	// while the published Error schema declares details `type: object,
	// nullable: true`. `nullable` is OpenAPI 3.0 vocabulary and the
	// JSON-Schema validator used here does not implement it, so real bytes with
	// `"details": null` fail at /error/details. Asserting the shape item by
	// item keeps the check honest instead of skipping the body; the nullable
	// mismatch is a contract defect of its own and is not #471's to rewrite.
	requireHumanDecisionRefusal := func(response *httptest.ResponseRecorder, method, path string) {
		t.Helper()
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s %s = %d, want 403: %s", method, path, response.Code, response.Body.String())
		}
		var envelope map[string]any
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode refusal %s: %v", response.Body.String(), err)
		}
		if _, ok := envelope["request_id"]; !ok {
			t.Fatalf("%s %s refusal has no request_id: %s", method, path, response.Body.String())
		}
		failure, ok := envelope["error"].(map[string]any)
		if !ok {
			t.Fatalf("%s %s refusal has no error object: %s", method, path, response.Body.String())
		}
		if failure["code"] != "human_decision_required" || failure["message"] == "" || failure["retryable"] != false {
			t.Fatalf("%s %s human-decision refusal = %s, want human_decision_required and retryable false", method, path, response.Body.String())
		}
		details, present := failure["details"]
		if !present || details != nil {
			// sendErrorDetails carries details explicitly; the value is null
			// because this refusal has nothing extra to disclose.
			t.Fatalf("%s %s human-decision refusal details = %v (present=%v), want an explicit null", method, path, details, present)
		}
	}

	// #382-A read-back: an empty projection is still a contract-shaped payload,
	// so the shape is checked before any rule exists.
	response := human(http.MethodGet, base+"/runtime/alert-rules", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list alert rules status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationResponse(t, doc, compiler, http.MethodGet, contractBase+"/runtime/alert-rules", "200", response.Body.Bytes())

	// #382-A create: the request is validated against the published input
	// schema, so a Go field the contract does not declare fails here.
	createBody := `{"signal":"audit/failed","severity":"P1","action":"notify","responsible_id":"reader"}`
	response = human(http.MethodPost, base+"/runtime/alert-rules", "g6-contract-rule", createBody)
	if response.Code != http.StatusCreated {
		t.Fatalf("create alert rule status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationRequest(t, doc, compiler, http.MethodPost, contractBase+"/runtime/alert-rules", []byte(createBody))
	validateOperationResponse(t, doc, compiler, http.MethodPost, contractBase+"/runtime/alert-rules", "201", response.Body.Bytes())
	var created struct {
		Data core.RuntimeAlertRule `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created rule: %v", err)
	}
	rule := created.Data
	if rule.ID == "" || rule.Status != "ACTIVE" || rule.Version != 1 {
		t.Fatalf("created rule = %+v, want an ACTIVE version 1 rule", rule)
	}

	// #382-B disable: the operator decision goes through the published action
	// route and bumps the rule's version. This runs before the refusal cases so
	// that a handler removed from the router fails here — on a success status —
	// rather than hiding behind the 404 that the out-of-scope caller also gets.
	disableBody := `{"expected_version":1,"operation":"disable","reason":"停用合成规则"}`
	response = human(http.MethodPost, base+"/runtime/alert-rules/"+rule.ID+"/actions", "g6-contract-disable", disableBody)
	if response.Code != http.StatusOK {
		t.Fatalf("disable alert rule status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationRequest(t, doc, compiler, http.MethodPost, contractBase+"/runtime/alert-rules/{ruleId}/actions", []byte(disableBody))
	validateOperationResponse(t, doc, compiler, http.MethodPost, contractBase+"/runtime/alert-rules/{ruleId}/actions", "200", response.Body.Bytes())
	var disabled struct {
		Data core.RuntimeAlertRule `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &disabled); err != nil {
		t.Fatalf("decode disabled rule: %v", err)
	}
	if disabled.Data.Status != "DISABLED" || disabled.Data.Version != 2 || disabled.Data.ID != rule.ID {
		t.Fatalf("disabled rule = %+v, want the same rule DISABLED at version 2", disabled.Data)
	}

	// #382-B refusal: a tenant member who is not a member of this project is
	// answered 404 — the refusal must not reveal that the project or its rules
	// exist — and it must leave the rule untouched.
	if err := f.db.Exec("INSERT INTO tenant_members (tenant_id, user_id, status) VALUES (?, ?, ?)", 7, "outsider", "active").Error; err != nil {
		t.Fatalf("seed outsider: %v", err)
	}
	outsider := func(method, path, key, body string) *httptest.ResponseRecorder {
		return send(actorContext("outsider"), method, path, key, body)
	}
	response = outsider(http.MethodGet, base+"/runtime/alert-rules", "", "")
	requireError(response, http.MethodGet, contractBase+"/runtime/alert-rules", http.StatusNotFound, "not_found")

	response = outsider(http.MethodPost, base+"/runtime/alert-rules/"+rule.ID+"/actions", "g6-contract-outsider",
		`{"expected_version":2,"operation":"enable","reason":"越权启用合成规则"}`)
	requireError(response, http.MethodPost, contractBase+"/runtime/alert-rules/{ruleId}/actions", http.StatusNotFound, "not_found")

	// #382-B refusal: an API-key principal is a machine, not a person, so the
	// human-decision guard refuses the action before it reaches the domain.
	response = send(machineContext(), http.MethodPost, base+"/runtime/alert-rules/"+rule.ID+"/actions", "g6-contract-machine",
		`{"expected_version":2,"operation":"enable","reason":"机器启用合成规则"}`)
	requireHumanDecisionRefusal(response, http.MethodPost, contractBase+"/runtime/alert-rules/{ruleId}/actions")

	// The two refusals above changed nothing: the owner still reads one rule,
	// still DISABLED at version 2. This is the read-back #382-A added, over the
	// same HTTP seam.
	response = human(http.MethodGet, base+"/runtime/alert-rules", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list alert rules after refusals status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationResponse(t, doc, compiler, http.MethodGet, contractBase+"/runtime/alert-rules", "200", response.Body.Bytes())
	var listed struct {
		Data []core.RuntimeAlertRule `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode rule list: %v", err)
	}
	if len(listed.Data) != 1 || listed.Data[0].Status != "DISABLED" || listed.Data[0].Version != 2 {
		t.Fatalf("rules after refusals = %+v, want the one rule DISABLED at version 2", listed.Data)
	}

	// A stale expected_version is a 409 with the published error shape, not a
	// silent overwrite.
	response = human(http.MethodPost, base+"/runtime/alert-rules/"+rule.ID+"/actions", "g6-contract-stale",
		`{"expected_version":1,"operation":"enable","reason":"过期版本启用"}`)
	requireError(response, http.MethodPost, contractBase+"/runtime/alert-rules/{ruleId}/actions", http.StatusConflict, "version_conflict")

	// An undeclared field is a 400 with the published error shape: the request
	// type does not silently accept a client-supplied "confirmed" flag.
	response = human(http.MethodPost, base+"/runtime/alert-rules/"+rule.ID+"/actions", "g6-contract-unknown",
		`{"expected_version":2,"operation":"enable","reason":"未知字段启用","confirmed":true}`)
	requireError(response, http.MethodPost, contractBase+"/runtime/alert-rules/{ruleId}/actions", http.StatusBadRequest, "invalid_request")

	// The refused writes above changed nothing either, and the disabled rule is
	// still readable: it is turned off, not deleted.
	response = human(http.MethodGet, base+"/runtime/alert-rules", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list alert rules after disable status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationResponse(t, doc, compiler, http.MethodGet, contractBase+"/runtime/alert-rules", "200", response.Body.Bytes())
	listed = struct {
		Data []core.RuntimeAlertRule `json:"data"`
	}{}
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode rule list: %v", err)
	}
	if len(listed.Data) != 1 || listed.Data[0].Status != "DISABLED" || listed.Data[0].Version != 2 {
		t.Fatalf("rules after disable = %+v, want the same rule DISABLED at version 2", listed.Data)
	}

	// #383 rollback: a rollback without a target is refused before anything is
	// written, and a machine principal is refused by the human-decision guard.
	response = human(http.MethodPost, base+"/runtime/controls", "g6-contract-rollback-empty",
		`{"expected_version":0,"scope":{"kind":"project","value":"`+f.project.ID+`"},"action":"rollback","reason":"没有目标"}`)
	requireError(response, http.MethodPost, contractBase+"/runtime/controls", http.StatusBadRequest, "invalid_request")

	rollbackBody := `{"expected_version":0,"scope":{"kind":"project","value":"` + f.project.ID + `"},"action":"rollback","reason":"回退到已验证配置","rollback":{"template_version":"` + f.project.TemplateVersion + `"}}`
	response = send(machineContext(), http.MethodPost, base+"/runtime/controls", "g6-contract-rollback-machine", rollbackBody)
	requireHumanDecisionRefusal(response, http.MethodPost, contractBase+"/runtime/controls")

	// The rollback records the configuration it points at and stops new writes;
	// both the request shape and the returned control are contract-checked.
	response = human(http.MethodPost, base+"/runtime/controls", "g6-contract-rollback", rollbackBody)
	if response.Code != http.StatusOK {
		t.Fatalf("rollback status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationRequest(t, doc, compiler, http.MethodPost, contractBase+"/runtime/controls", []byte(rollbackBody))
	validateOperationResponse(t, doc, compiler, http.MethodPost, contractBase+"/runtime/controls", "200", response.Body.Bytes())
	var controlled struct {
		Data core.RuntimeControl `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &controlled); err != nil {
		t.Fatalf("decode runtime control: %v", err)
	}
	control := controlled.Data
	if control.Status != "PAUSED" || control.Version != 1 || control.RollbackTo == nil ||
		control.RollbackTo.TemplateVersion != f.project.TemplateVersion {
		t.Fatalf("runtime control = %+v, want a PAUSED version 1 control pointing at %s", control, f.project.TemplateVersion)
	}

	// The control is readable back over HTTP, which is what makes the rollback
	// auditable rather than a write-only side effect.
	response = human(http.MethodGet, base+"/runtime/controls", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list runtime controls status = %d; body=%s", response.Code, response.Body.String())
	}
	validateOperationResponse(t, doc, compiler, http.MethodGet, contractBase+"/runtime/controls", "200", response.Body.Bytes())
	var controls struct {
		Data []core.RuntimeControl `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &controls); err != nil {
		t.Fatalf("decode runtime controls: %v", err)
	}
	if len(controls.Data) != 1 || controls.Data[0].Status != "PAUSED" {
		t.Fatalf("runtime controls = %+v, want the one PAUSED control", controls.Data)
	}
}

// seedContractRouteEdge writes one frozen route edge version into the table
// the roadmap store owns, so the edge reads answer with real content instead
// of an empty projection.
func seedContractRouteEdge(t *testing.T, db *gorm.DB, projectID, edgeID, sourceID, targetID string) {
	t.Helper()
	now := time.Now().UTC()
	if err := db.Exec(`INSERT INTO lingdoc_g3_route_edges(id,project_id,edge_id,version,source_id,target_id,relation,context_revision,actor_id,reason,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		"contract-edge-"+edgeID, projectID, edgeID, 1, sourceID, targetID, "supports", 1, deliveryActorID, "契约校验 合成路线边", now).Error; err != nil {
		t.Fatalf("seed contract route edge %s: %v", edgeID, err)
	}
}

// contractRecommendationStub stands in for the recommendation model inside the
// contract fixture: fixed proposals over the fixture's own objects, no
// provider.
type contractRecommendationStub struct {
	proposals []core.ModelProposedDependency
}

func (s *contractRecommendationStub) Recommend(context.Context, core.Actor, core.DependencyRecommendationRequest) (core.DependencyRecommendationResult, error) {
	return core.DependencyRecommendationResult{
		Proposals: s.proposals, ModelID: "contract-stub", RunMode: "real", ModelRunID: "contract-rec-run",
	}, nil
}
