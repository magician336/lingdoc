package workspace

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/gin-gonic/gin"
)

// A1: the reject and reassess routes must be exercised through the real
// workspace Service and the real database, not through a stub application.
//
// The existing coverage for these two routes is handler-level only:
// focused_application_http_test.go:42 and
// agent_change_draft_reassess_receipt_test.go both drive a fake application,
// so they prove parameter passthrough and status mapping but never reach a
// write. change_set_http_test.go reaches the real service but only for
// create/apply/list/get. This file closes that gap by reusing the same fixture.
//
// The fixture is deliberately the same one: seedBoundSource +
// testRuntime(handler).projects, so a divergence between this file and
// change_set_http_test.go is a real finding rather than a setup difference.

func changeSetHTTPFixture(t *testing.T) (*gin.Engine, *core.Service, ChangeSet) {
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
	seedDeliveryBoundTemplateCopy(t, db, "change-http-decision-copy")
	service, ok := testRuntime(handler).projects.(*core.Service)
	if !ok {
		t.Fatalf("workspace handler service = %T, want *core.Service", testRuntime(handler).projects)
	}

	engine := gin.New()
	handler.Register(RouteGroups{Read: engine.Group("/api"), Write: engine.Group("/api")})

	body := `{"expected_context_revision":1,"fields":{"research_subject":{"old_value":"公开的合成调查样本","new_value":"待否决的研究对象"}},"affected_chapter_ids":["chapter-1","chapter-2"],"reason":"研究对象发生变化"}`
	request := httptest.NewRequest(http.MethodPost, "/api/projects/project-1/change-sets", strings.NewReader(body)).WithContext(actorContext("reader"))
	request.Header.Set("Idempotency-Key", "change-http-decision-create")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body=%s", response.Code, response.Body.String())
	}
	var created struct {
		Data ChangeSet `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.Data.Status != "assessed" {
		t.Fatalf("created ChangeSet = %+v, want assessed", created.Data)
	}
	return engine, service, created.Data
}

func postChangeSetRoute(t *testing.T, engine *gin.Engine, path, key string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, nil).WithContext(actorContext("reader"))
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

// A1a: reject reaches the real Service, persists the decision, and replays.
func TestRejectRouteReachesRealWorkspaceService(t *testing.T) {
	engine, service, change := changeSetHTTPFixture(t)

	response := postChangeSetRoute(t, engine, "/api/projects/project-1/change-sets/"+change.ID+"/reject", "change-http-reject-1")
	if response.Code != http.StatusOK {
		t.Fatalf("reject status = %d; body=%s", response.Code, response.Body.String())
	}
	var rejected struct {
		Data ChangeSet `json:"data"`
		Meta struct {
			Replayed bool `json:"replayed"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &rejected); err != nil {
		t.Fatalf("decode reject response: %v", err)
	}
	if rejected.Data.Status != "rejected" || rejected.Data.ID != change.ID || rejected.Data.DecisionBy == "" || rejected.Data.DecidedAt == nil {
		t.Fatalf("reject response = %+v", rejected.Data)
	}
	if rejected.Meta.Replayed {
		t.Fatalf("the first reject must not report a replay: %s", response.Body.String())
	}

	// The decision must be in the database, not merely in the response body.
	stored, err := service.GetChangeSet(policyContext(), Actor{TenantID: 7, UserID: deliveryActorID}, "project-1", change.ID)
	if err != nil {
		t.Fatalf("read the rejected ChangeSet back: %v", err)
	}
	if stored.Status != "rejected" || stored.DecisionBy != rejected.Data.DecisionBy {
		t.Fatalf("persisted ChangeSet = %+v, response = %+v", stored, rejected.Data)
	}

	// Same key again: replayed, same record, and no second decision.
	replay := postChangeSetRoute(t, engine, "/api/projects/project-1/change-sets/"+change.ID+"/reject", "change-http-reject-1")
	if replay.Code != http.StatusOK {
		t.Fatalf("reject replay status = %d; body=%s", replay.Code, replay.Body.String())
	}
	var replayed struct {
		Data ChangeSet `json:"data"`
		Meta struct {
			Replayed bool `json:"replayed"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(replay.Body.Bytes(), &replayed); err != nil {
		t.Fatalf("decode reject replay: %v", err)
	}
	if !replayed.Meta.Replayed || replayed.Data.ID != change.ID {
		t.Fatalf("reject replay = %+v, first = %+v", replayed.Data, rejected.Data)
	}

	events, err := service.ListAuditEvents(policyContext(), Actor{TenantID: 7, UserID: deliveryActorID}, "project-1")
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	rejects := 0
	for _, event := range events {
		if event.Action == "project.change_set.reject" && event.Target == change.ID {
			rejects++
		}
	}
	if rejects != 1 {
		t.Fatalf("reject audit events = %d, want exactly 1", rejects)
	}
}

// A1b: reassess only works on a stale proposal, and the old one stays immutable.
func TestReassessRouteReachesRealWorkspaceService(t *testing.T) {
	engine, service, first := changeSetHTTPFixture(t)
	ctx := policyContext()
	actor := Actor{TenantID: 7, UserID: deliveryActorID}

	// A second proposal on the same base revision goes stale once the first is
	// applied, which is the only state reassess accepts.
	//
	// It must target the SAME new value as the first proposal. Reassess refuses
	// a proposal whose old value no longer matches the current spec
	// (service_facade.go:2488), so a different goal would surface as
	// version_conflict instead of the reassessment path under test.
	body := `{"expected_context_revision":1,"fields":{"research_subject":{"old_value":"公开的合成调查样本","new_value":"待否决的研究对象"}},"affected_chapter_ids":["chapter-1"],"reason":"第二个提案"}`
	request := httptest.NewRequest(http.MethodPost, "/api/projects/project-1/change-sets", strings.NewReader(body)).WithContext(actorContext("reader"))
	request.Header.Set("Idempotency-Key", "change-http-reassess-proposal")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create second proposal: status=%d body=%s", response.Code, response.Body.String())
	}
	var proposal struct {
		Data ChangeSet `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &proposal); err != nil {
		t.Fatalf("decode second proposal: %v", err)
	}

	applied := postChangeSetRoute(t, engine, "/api/projects/project-1/change-sets/"+first.ID+"/apply", "change-http-reassess-apply")
	if applied.Code != http.StatusOK {
		t.Fatalf("apply first: status=%d body=%s", applied.Code, applied.Body.String())
	}

	// Reassessing a proposal that is not stale must be refused by the real
	// service, not merely by the handler.
	notStale := postChangeSetRoute(t, engine, "/api/projects/project-1/change-sets/"+first.ID+"/reassess", "change-http-reassess-not-stale")
	if notStale.Code != http.StatusConflict && notStale.Code != http.StatusUnprocessableEntity && notStale.Code != http.StatusBadRequest {
		t.Fatalf("reassessing an applied ChangeSet returned %d, want a refusal; body=%s", notStale.Code, notStale.Body.String())
	}

	// Force the second proposal stale the same way the sequential path does.
	// 409 is the *success* signal here: the apply is refused because the base
	// revision moved, and the refusal is what marks the proposal stale.
	if _, status, _, err := service.ApplyChangeSet(ctx, actor, "project-1", proposal.Data.ID, "change-http-reassess-stale"); err != nil || status != http.StatusConflict {
		t.Fatalf("the second proposal should have gone stale with 409, got status=%d err=%v", status, err)
	}

	staleBefore, err := service.GetChangeSet(ctx, actor, "project-1", proposal.Data.ID)
	if err != nil || staleBefore.Status != "stale" {
		t.Fatalf("second proposal = %+v err=%v, want stale", staleBefore, err)
	}

	reassessed := postChangeSetRoute(t, engine, "/api/projects/project-1/change-sets/"+proposal.Data.ID+"/reassess", "change-http-reassess-1")
	if reassessed.Code != http.StatusCreated {
		t.Fatalf("reassess status = %d; body=%s", reassessed.Code, reassessed.Body.String())
	}
	var next struct {
		Data ChangeSet `json:"data"`
		Meta struct {
			Replayed bool `json:"replayed"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(reassessed.Body.Bytes(), &next); err != nil {
		t.Fatalf("decode reassess response: %v", err)
	}
	if next.Data.ID == "" || next.Data.ID == staleBefore.ID {
		t.Fatalf("reassess must create a new record, got %+v (stale was %s)", next.Data, staleBefore.ID)
	}
	if next.Data.ReassessedFromID != staleBefore.ID {
		t.Fatalf("reassess response does not link its source: %+v", next.Data)
	}
	if next.Data.Status != "assessed" && next.Data.Status != "satisfied" {
		t.Fatalf("reassessed status = %q, want assessed or satisfied", next.Data.Status)
	}
	if next.Meta.Replayed {
		t.Fatalf("the first reassess must not report a replay: %s", reassessed.Body.String())
	}

	// The new record must exist in the database.
	created, err := service.GetChangeSet(ctx, actor, "project-1", next.Data.ID)
	if err != nil {
		t.Fatalf("read the reassessed ChangeSet back: %v", err)
	}
	if created.ReassessedFromID != staleBefore.ID {
		t.Fatalf("persisted reassessment = %+v, want reassessed_from_id=%s", created, staleBefore.ID)
	}

	// The stale proposal is immutable history.
	staleAfter, err := service.GetChangeSet(ctx, actor, "project-1", staleBefore.ID)
	if err != nil {
		t.Fatal(err)
	}
	if staleAfter.Status != staleBefore.Status || staleAfter.StaleReason != staleBefore.StaleReason {
		t.Fatalf("reassessment rewrote the stale proposal: before=%+v after=%+v", staleBefore, staleAfter)
	}

	replay := postChangeSetRoute(t, engine, "/api/projects/project-1/change-sets/"+proposal.Data.ID+"/reassess", "change-http-reassess-1")
	if replay.Code != http.StatusCreated {
		t.Fatalf("reassess replay status = %d; body=%s", replay.Code, replay.Body.String())
	}
	var replayed struct {
		Data ChangeSet `json:"data"`
		Meta struct {
			Replayed bool `json:"replayed"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(replay.Body.Bytes(), &replayed); err != nil {
		t.Fatalf("decode reassess replay: %v", err)
	}
	if !replayed.Meta.Replayed || replayed.Data.ID != next.Data.ID {
		t.Fatalf("reassess replay = %+v, first = %+v", replayed.Data, next.Data)
	}
}
