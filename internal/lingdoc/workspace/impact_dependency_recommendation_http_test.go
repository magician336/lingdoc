package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"

	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
)

// The recommendation trigger route (#751): a human fires it, the response
// carries the queued batch plus the refused proposals, and the human gate
// holds against machine principals. The happy-path response schema is checked
// by the contract test; these tests pin the route's own semantics.

// recommendationRouteFixture wires the contract fixture with a stub
// recommender over the fixture's own spec and first chapter.
func recommendationRouteFixture(t *testing.T, proposals []core.ModelProposedDependency, modelErr error) routeContractFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	fixture := newRouteContractFixture(t)
	fixture.service.SetDependencyRecommendationModel(&httpRecommendationStub{proposals: proposals, err: modelErr})
	return fixture
}

type httpRecommendationStub struct {
	proposals []core.ModelProposedDependency
	err       error
	calls     int
}

func (s *httpRecommendationStub) Recommend(context.Context, core.Actor, core.DependencyRecommendationRequest) (core.DependencyRecommendationResult, error) {
	s.calls++
	if s.err != nil {
		return core.DependencyRecommendationResult{}, s.err
	}
	return core.DependencyRecommendationResult{
		Proposals: s.proposals, ModelID: "http-stub", RunMode: "real", ModelRunID: "http-rec-run",
	}, nil
}

func TestDependencyRecommendationRouteQueuesAndReplays(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := newRouteContractFixture(t)
	if len(fixture.chapters) == 0 {
		t.Fatal("fixture produced no chapters")
	}
	stub := &httpRecommendationStub{proposals: []core.ModelProposedDependency{{
		SourceKind: "ProjectSpec", SourceID: fixture.project.ID,
		TargetKind: "ChapterVersion", TargetID: fixture.chapters[0].ID,
		Relation: "uses", Action: "REGENERATE",
		Evidence: "第 1 章正文引用研究条件字段", Reason: "研究条件变化需要重生成",
		Confidence: 0.7,
	}}}
	fixture.service.SetDependencyRecommendationModel(stub)
	engine := fixture.engine
	trigger := func(key string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/projects/"+fixture.project.ID+"/dependency-recommendations",
			strings.NewReader(`{"reason":"冷启动建边"}`)).WithContext(actorContext(deliveryActorID))
		request.Header.Set("Idempotency-Key", key)
		recorded := httptest.NewRecorder()
		engine.ServeHTTP(recorded, request)
		return recorded
	}
	first := trigger("recommend-http-1")
	if first.Code != http.StatusCreated {
		t.Fatalf("recommend status = %d; body=%s", first.Code, first.Body.String())
	}
	var outcome struct {
		Data struct {
			Batch core.DependencyCandidateBatch `json:"batch"`
			Model struct {
				ModelID    string `json:"model_id"`
				ModelRunID string `json:"model_run_id"`
			} `json:"model"`
		} `json:"data"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &outcome); err != nil {
		t.Fatalf("decode recommendation: %v (%s)", err, first.Body.String())
	}
	if len(outcome.Data.Batch.Candidates) == 0 || outcome.Data.Model.ModelRunID != "http-rec-run" {
		t.Fatalf("outcome = %+v", outcome.Data)
	}
	replay := trigger("recommend-http-1")
	if replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), `"replayed":true`) {
		t.Fatalf("replay status = %d; body=%s", replay.Code, replay.Body.String())
	}
	var replayedBatch struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(replay.Body.Bytes(), &replayedBatch); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(replayedBatch.Data), outcome.Data.Batch.ID) {
		t.Fatalf("replay did not return the original batch: %s", string(replayedBatch.Data))
	}
	if stub.calls != 1 {
		t.Fatalf("a replayed key ran the model %d times over HTTP", stub.calls)
	}
}

func TestDependencyRecommendationRouteRefusesAMachinePrincipal(t *testing.T) {
	fixture := recommendationRouteFixture(t, nil, nil)
	machine := types.WithTenantAPIKeyScope(actorContext(deliveryActorID), types.TenantAPIKeyScope{KeyID: 13, FullAccess: true})
	request := httptest.NewRequest(http.MethodPost, "/api/projects/"+fixture.project.ID+"/dependency-recommendations",
		strings.NewReader(`{"reason":"机器触发"}`)).WithContext(machine)
	request.Header.Set("Idempotency-Key", "recommend-machine-1")
	recorded := httptest.NewRecorder()
	fixture.engine.ServeHTTP(recorded, request)
	if recorded.Code != http.StatusForbidden || !strings.Contains(recorded.Body.String(), "human_decision_required") {
		t.Fatalf("machine trigger status = %d; body=%s", recorded.Code, recorded.Body.String())
	}
}

func TestDependencyRecommendationRouteSurfacesModelFailureWithoutWrites(t *testing.T) {
	fixture := recommendationRouteFixture(t, nil, errors.New("provider down"))
	request := httptest.NewRequest(http.MethodPost, "/api/projects/"+fixture.project.ID+"/dependency-recommendations",
		strings.NewReader(`{"reason":"冷启动建边"}`)).WithContext(actorContext(deliveryActorID))
	request.Header.Set("Idempotency-Key", "recommend-http-fail")
	recorded := httptest.NewRecorder()
	fixture.engine.ServeHTTP(recorded, request)
	if recorded.Code != http.StatusServiceUnavailable || !strings.Contains(recorded.Body.String(), "dependency_recommendation_unavailable") {
		t.Fatalf("model failure status = %d; body=%s", recorded.Code, recorded.Body.String())
	}
	list := httptest.NewRequest(http.MethodGet, "/api/projects/"+fixture.project.ID+"/dependency-candidates", nil).WithContext(actorContext(deliveryActorID))
	listed := httptest.NewRecorder()
	fixture.engine.ServeHTTP(listed, list)
	if !strings.Contains(listed.Body.String(), "[]") {
		t.Fatalf("a failed recommendation left batches: %s", listed.Body.String())
	}
}
