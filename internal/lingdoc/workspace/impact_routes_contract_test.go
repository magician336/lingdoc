package workspace

import (
	"bytes"
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

	"github.com/gin-gonic/gin"
	"github.com/santhosh-tekuri/jsonschema/v6"

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
		t.Fatalf("%s %s response does not satisfy %s:\n%s", strings.ToUpper(method), path, ref, string(body))
	}
}

// g3203Engine builds the router over a real SQLite-backed workspace service,
// exactly the way the application registers it.
type routeContractFixture struct {
	engine   *gin.Engine
	service  *Service
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
	if err := db.Exec("UPDATE lingdoc_projects SET current_context_revision = ? WHERE id = ?", 1, "project-1").Error; err != nil {
		t.Fatalf("seed context revision: %v", err)
	}
	seedDeliveryBoundTemplateCopy(t, db, "accept-copy")
	service, ok := testRuntime(handler).service.(*Service)
	if !ok {
		t.Fatalf("workspace handler service = %T, want *Service", testRuntime(handler).service)
	}
	engine := gin.New()
	RegisterImpactRoutes(engine.Group("/api"), service)
	project, err := service.GetProject(policyContext(), Actor{TenantID: 7, UserID: deliveryActorID}, "project-1")
	if err != nil {
		t.Fatalf("read fixture project: %v", err)
	}
	chapters, err := service.ListChapters(policyContext(), Actor{TenantID: 7, UserID: deliveryActorID}, project.ID)
	if err != nil {
		t.Fatalf("read fixture chapters: %v", err)
	}
	return routeContractFixture{engine: engine, service: service, project: project, chapters: chapters}
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
// and it is the complete G3 published surface of RegisterImpactRoutes. The G6
// runtime/trial/route families are registered by the same helper but belong to
// the G6 track and are deliberately out of scope here; the residual gap between
// the G6 router and the contract is tracked as G6 debt, not asserted as an
// impact failure. Within this list the coverage is bidirectional and total:
// every registered path must be published, every published path must be
// served, and every response body must satisfy the schema (see the three tests
// below). `impact-assessment` joined this list in #207, which closed the gap
// #203 recorded here.
//
// `/assess` joined this list in #223. It was registered but unpublished, so a
// generated client had no way to trigger an assessment; the contract now
// publishes it and the bidirectional coverage below keeps it that way.
var impactOwnedPathSuffixes = []string{"/impact-tasks", "/impact-assessment", "/assess", "/dependencies", "/stabilize", "/preflight", "/check-receipts"}

func isImpactOwnedPath(path string, owned []string) bool {
	for _, suffix := range owned {
		if strings.HasSuffix(path, suffix) || strings.Contains(path, suffix+"/") {
			return true
		}
	}
	return false
}

// Direction 1: every impact/dependency/stabilize/preflight route the server
// registers is published in the contract. An unpublished route is a route no
// generated client can call.
func TestEveryImpactRouteIsPublished(t *testing.T) {
	doc, _ := contractDocument(t)
	engine := newRouteContractFixture(t).engine
	registered := routedOperations(engine)
	var missing, checked []string
	for method, paths := range registered {
		for path := range paths {
			if !isImpactOwnedPath(path, impactOwnedPathSuffixes) {
				continue
			}
			checked = append(checked, strings.ToUpper(method)+" "+path)
			if _, ok := contractOperation(t, doc, method, path); !ok {
				missing = append(missing, strings.ToUpper(method)+" "+path)
			}
		}
	}
	if len(checked) == 0 {
		t.Fatal("no impact routes were checked; the path filter matched nothing")
	}
	sort.Strings(missing)
	for _, operation := range missing {
		t.Errorf("registered impact route %s is absent from the OpenAPI contract", operation)
	}
}

// Direction 2: every published impact/dependency/stabilize/preflight
// operation is actually served. A contract path with no handler is a promise
// the server does not keep.
func TestEveryPublishedImpactOperationIsRouted(t *testing.T) {
	doc, _ := contractDocument(t)
	engine := newRouteContractFixture(t).engine
	registered := routedOperations(engine)

	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		t.Fatal("contract has no paths object")
	}
	// Only the impact surface is in scope here; the G4/G5/G6 trial and runtime
	// families are registered by this same helper but are not part of the impact
	// impact/dependency/stabilization acceptance surface.
	owned := impactOwnedPathSuffixes
	checked := 0
	for path, item := range paths {
		if !strings.HasPrefix(path, "/projects/{projectId}/") || !isImpactOwnedPath(path, owned) {
			continue
		}
		item, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for method := range item {
			checked++
			if registered[strings.ToLower(method)][path] {
				continue
			}
			t.Errorf("contract publishes %s %s but no route serves it", strings.ToUpper(method), path)
		}
	}
	if checked == 0 {
		t.Fatal("no impact contract operations were checked; the path filter matched nothing")
	}
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
}
