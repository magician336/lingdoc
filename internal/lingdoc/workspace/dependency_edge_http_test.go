package workspace

import (
	"encoding/json"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// g3HTTPDependencyBody posts a JSON body to a dependency route and returns the
// recorded response. Assertions stay on externally visible status/payload
// rather than on any internal table.
func postDependencyJSON(t *testing.T, engine *gin.Engine, method, path, userID, key, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(actorContext(userID))
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func dependencyDataFrom(t *testing.T, response *httptest.ResponseRecorder) DependencyEdge {
	t.Helper()
	if response.Code != http.StatusOK && response.Code != http.StatusCreated {
		t.Fatalf("unexpected status %d; body=%s", response.Code, response.Body.String())
	}
	var decoded struct {
		Data DependencyEdge `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode dependency response: %v (%s)", err, response.Body.String())
	}
	return decoded.Data
}

func TestDependencyRoutesCoverConfirmModifyReject(t *testing.T) {
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

	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})
	// The dependency routes live in the impact/trial group, registered separately from
	// the main ChangeSet routes above.
	RegisterImpactRoutes(engine.Group("/api"), impactPortsFor(service))

	// Use the chapter versions the delivery fixture already materialised; this
	// keeps the test on the dependency seam instead of the chapter-edit seam.
	specRevision := strconv.FormatInt(project.SpecRevision, 10)
	for _, chapter := range chapters[:2] {
		if chapter.CurrentVersionID == nil {
			t.Fatalf("fixture chapter %s has no current version", chapter.ID)
		}
	}
	targetRef := `{"kind":"ChapterVersion","id":"` + chapters[0].ID + `","version":"` + *chapters[0].CurrentVersionID + `"}`
	sourceRef := `{"kind":"ProjectSpec","id":"project-1","version":"` + specRevision + `"}`
	var response *httptest.ResponseRecorder

	// Success: an AI candidate is created as suggested, not confirmed.
	edge := dependencyDataFrom(t, postDependencyJSON(t, engine, http.MethodPost, "/api/projects/project-1/dependencies", "reader", "change-http-edge-1",
		`{"expected_context_revision":1,"source":`+sourceRef+`,"target":`+targetRef+`,"relation":"supports","action":"REVALIDATE_EVIDENCE","origin":"ai","reason":"模型候选","confidence":0.2}`))
	if edge.Status != "suggested" || edge.Version != 1 || edge.Origin != "ai" || edge.ID == "" {
		t.Fatalf("ai edge over HTTP = %+v", edge)
	}

	// Failure: an unknown decision is rejected before any write.
	response = postDependencyJSON(t, engine, http.MethodPost, "/api/projects/project-1/dependencies/"+edge.ID+"/decision", "reader", "change-http-decide-bad",
		`{"expected_version":1,"decision":"approve","reason":"未知决定"}`)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown decision status = %d; body=%s", response.Code, response.Body.String())
	}

	// Version binding: a stale expected_version is a 409, not a silent overwrite.
	response = postDependencyJSON(t, engine, http.MethodPost, "/api/projects/project-1/dependencies/"+edge.ID+"/decision", "reader", "change-http-decide-stale",
		`{"expected_version":99,"decision":"confirm","reason":"版本不符"}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("stale decision status = %d; body=%s", response.Code, response.Body.String())
	}

	// Success: modify rebinds the candidate and keeps it confirmed.
	decisionPath := "/api/projects/project-1/dependencies/" + edge.ID + "/decision"
	modified := dependencyDataFrom(t, postDependencyJSON(t, engine, http.MethodPost, decisionPath, "reader", "change-http-decide-modify",
		`{"expected_version":1,"decision":"modify","reason":"人工修正","relation":"constrains","action":"REGENERATE","confidence":0.9}`))
	if modified.Status != "confirmed" || modified.Relation != "constrains" || modified.Action != "REGENERATE" ||
		modified.Confidence != 0.9 || modified.Version != 2 {
		t.Fatalf("modified edge over HTTP = %+v", modified)
	}

	// Idempotent replay of the same decision key returns the same confirmed edge.
	replay := dependencyDataFrom(t, postDependencyJSON(t, engine, http.MethodPost, decisionPath, "reader", "change-http-decide-modify",
		`{"expected_version":1,"decision":"modify","reason":"人工修正","relation":"constrains","action":"REGENERATE","confidence":0.9}`))
	if replay.ID != modified.ID || replay.Version != modified.Version || replay.Status != "confirmed" {
		t.Fatalf("decision replay diverged: %+v vs %+v", replay, modified)
	}

	// Conflict: the automated run may not resurrect the tuple the human replaced.
	response = postDependencyJSON(t, engine, http.MethodPost, "/api/projects/project-1/dependencies", "reader", "change-http-edge-resurrect",
		`{"expected_context_revision":1,"source":`+sourceRef+`,"target":`+targetRef+`,"relation":"supports","action":"REVALIDATE_EVIDENCE","origin":"ai","reason":"模型再次候选","confidence":0.3}`)
	if response.Code != http.StatusConflict {
		t.Fatalf("resurrected candidate status = %d; body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "dependency_decided") {
		t.Fatalf("resurrected candidate error body = %s", response.Body.String())
	}

	// The confirmed edge survives in the read projection with server-side status.
	response = postDependencyJSON(t, engine, http.MethodGet, "/api/projects/project-1/dependencies", "reader", "", "")
	if response.Code != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", response.Code, response.Body.String())
	}
	var listed struct {
		Data []DependencyEdge `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Data) != 1 || listed.Data[0].ID != modified.ID || listed.Data[0].Status != "confirmed" || listed.Data[0].Version != 2 {
		t.Fatalf("list after modify = %+v", listed.Data)
	}
}
