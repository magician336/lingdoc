package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
)

// The dependency-edge recommender (#751): the server-side producer of
// dependency-candidate batches. The candidate queue itself, the suppression
// memory and the decision endpoints are covered by the candidate tests; these
// tests cover the recommender's own contract — the model call happens at most
// once per idempotency key, every proposal is reviewed before it is queued,
// and a failed or untrustworthy model answer leaves no business write.

type recommendationModelStub struct {
	calls     int
	requests  []DependencyRecommendationRequest
	proposals []ModelProposedDependency
	err       error
}

func (s *recommendationModelStub) Recommend(_ context.Context, _ Actor, request DependencyRecommendationRequest) (DependencyRecommendationResult, error) {
	s.calls++
	s.requests = append(s.requests, request)
	if s.err != nil {
		return DependencyRecommendationResult{}, s.err
	}
	return DependencyRecommendationResult{
		Proposals:  s.proposals,
		ModelID:    "stub-chat",
		RunMode:    "real",
		ModelRunID: "rec-run-1",
	}, nil
}

func decodeRecommendationOutcome(t *testing.T, raw json.RawMessage) DependencyRecommendationOutcome {
	t.Helper()
	var outcome DependencyRecommendationOutcome
	if err := json.Unmarshal(raw, &outcome); err != nil {
		t.Fatalf("decode recommendation outcome: %v (%s)", err, raw)
	}
	return outcome
}

// recommendationFixture seeds the standard two-chapter project and wires a
// stub recommender that proposes one spec→chapter edge and one
// chapter→chapter edge.
func recommendationFixture(t *testing.T, proposals []ModelProposedDependency, modelErr error) (*Service, Actor, Project, []Chapter, *recommendationModelStub) {
	t.Helper()
	svc := testStore(t, filepath.Join(t.TempDir(), "change-set.db"))
	actor := Actor{TenantID: 311, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters := materializeChapterVersions(t, svc, actor, project, nil)
	fresh, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	stub := &recommendationModelStub{proposals: proposals, err: modelErr}
	svc.SetDependencyRecommendationModel(stub)
	return svc, actor, fresh, chapters, stub
}

func specProposal(project Project, chapter Chapter) ModelProposedDependency {
	return ModelProposedDependency{
		SourceKind: "ProjectSpec", SourceID: project.ID,
		TargetKind: "ChapterVersion", TargetID: chapter.ID,
		Relation: "uses", Action: "REGENERATE",
		Evidence:   "第 1 章正文引用了研究条件字段",
		Reason:     "研究条件变化需要重生成章节",
		Confidence: 0.7,
	}
}

func chapterProposal(from, to Chapter) ModelProposedDependency {
	return ModelProposedDependency{
		SourceKind: "ChapterVersion", SourceID: from.ID,
		TargetKind: "ChapterVersion", TargetID: to.ID,
		Relation: "constrains", Action: "REVIEW_REQUIRED",
		Evidence:   "第 1 章结论被第 2 章引用",
		Reason:     "上游章节变更需要人工复核下游",
		Confidence: 0.5,
	}
}

func TestDependencyRecommendationQueuesReviewedProposals(t *testing.T) {
	svc, actor, project, chapters, stub := recommendationFixture(t, nil, nil)
	stub.proposals = []ModelProposedDependency{
		specProposal(project, chapters[0]),
		chapterProposal(chapters[0], chapters[1]),
	}
	raw, status, replay, err := svc.RecommendDependencyCandidates(context.Background(), actor, project.ID, "rec-key-1", DependencyRecommendationInput{Reason: "冷启动建边"})
	if err != nil || status != 201 || replay {
		t.Fatalf("recommend: status=%d replay=%v err=%v", status, replay, err)
	}
	outcome := decodeRecommendationOutcome(t, raw)
	var batch DependencyCandidateBatch
	if err := json.Unmarshal(outcome.Batch, &batch); err != nil {
		t.Fatalf("decode queued batch: %v (%s)", err, outcome.Batch)
	}
	if len(batch.Candidates) != 2 || len(outcome.Dropped) != 0 {
		t.Fatalf("batch = %+v dropped = %+v", batch.Candidates, outcome.Dropped)
	}
	specRef := ObjectVersionRef{Kind: "ProjectSpec", ID: project.ID, Version: batch.Candidates[0].Source.Version}
	if batch.Candidates[0].Source.Kind != "ProjectSpec" || batch.Candidates[0].Source.ID != project.ID || batch.Candidates[0].Source.Version == "" {
		t.Fatalf("server did not stamp the version: %+v", specRef)
	}
	for _, candidate := range batch.Candidates {
		if candidate.ModelRunID != "rec-run-1" || candidate.Evidence == "" || candidate.ActorID != actor.UserID {
			t.Fatalf("candidate lost its provenance: %+v", candidate)
		}
	}
	if outcome.Model.ModelID != "stub-chat" || outcome.Model.RunMode != "real" || outcome.Model.ModelRunID != "rec-run-1" {
		t.Fatalf("model info = %+v", outcome.Model)
	}
	// The signal pack the model saw carries the server-owned current refs.
	if stub.calls != 1 || len(stub.requests[0].Signals.Objects) == 0 {
		t.Fatalf("model calls = %d signals = %+v", stub.calls, stub.requests[0].Signals)
	}
	// The batch is visible through the existing queue.
	batches, err := svc.ListDependencyCandidates(context.Background(), actor, project.ID, batch.ID)
	if err != nil || len(batches) != 1 || len(batches[0].Candidates) != 2 {
		t.Fatalf("queue read: %+v %v", batches, err)
	}
}

func TestDependencyRecommendationReplayNeverCallsTheModelTwice(t *testing.T) {
	svc, actor, project, chapters, stub := recommendationFixture(t, nil, nil)
	stub.proposals = []ModelProposedDependency{specProposal(project, chapters[0])}
	raw, status, _, err := svc.RecommendDependencyCandidates(context.Background(), actor, project.ID, "rec-key-replay", DependencyRecommendationInput{Reason: "冷启动建边"})
	if err != nil || status != 201 {
		t.Fatalf("first recommend: status=%d err=%v", status, err)
	}
	first := decodeRecommendationOutcome(t, raw)
	firstBatch := decodeCandidateBatch(t, first.Batch)
	again, status, replay, err := svc.RecommendDependencyCandidates(context.Background(), actor, project.ID, "rec-key-replay", DependencyRecommendationInput{Reason: "冷启动建边"})
	if err != nil || status != 200 || !replay {
		t.Fatalf("replay: status=%d replay=%v err=%v", status, replay, err)
	}
	replayedBatch := decodeCandidateBatch(t, again)
	if replayedBatch.ID != firstBatch.ID || len(replayedBatch.Candidates) != len(firstBatch.Candidates) {
		t.Fatalf("replay returned a different batch: %s vs %s", replayedBatch.ID, firstBatch.ID)
	}
	if stub.calls != 1 {
		t.Fatalf("a replayed key ran the model %d times", stub.calls)
	}
}

func TestDependencyRecommendationModelFailureWritesNoCandidates(t *testing.T) {
	svc, actor, project, chapters, _ := recommendationFixture(t, nil, errors.New("provider down"))
	_, _, _, err := svc.RecommendDependencyCandidates(context.Background(), actor, project.ID, "rec-key-fail", DependencyRecommendationInput{Reason: "冷启动建边"})
	if err == nil || errors.Is(err, ErrDependencyRecommendationNoValidCandidates) || errors.Is(err, ErrDependencyRecommendationInvalidOutput) {
		t.Fatalf("model failure was not surfaced: %v", err)
	}
	batches, listErr := svc.ListDependencyCandidates(context.Background(), actor, project.ID, "")
	if listErr != nil || len(batches) != 0 {
		t.Fatalf("a failed recommendation left batches: %+v %v", batches, listErr)
	}
	// The manual registration path stays usable after the failure.
	if _, _, _, err := svc.CreateDependencyEdge(context.Background(), actor, project.ID, "manual-after-fail", DependencyEdgeInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Source:                  ObjectVersionRef{Kind: "ProjectSpec", ID: project.ID, Version: strconv.FormatInt(project.SpecRevision, 10)},
		Target:                  chapterVersionRef(chapters[0]),
		Relation:                "uses", Action: "REGENERATE", Origin: "user", Reason: "人工补登记", Confidence: 1,
	}); err != nil {
		t.Fatalf("manual registration broke after a model failure: %v", err)
	}
}

func TestDependencyRecommendationDropsUntrustworthyProposals(t *testing.T) {
	svc, actor, project, chapters, _ := recommendationFixture(t, nil, nil)
	fabricated := specProposal(project, chapters[0])
	fabricated.SourceID = "no-such-object"
	selfLoop := specProposal(project, chapters[0])
	selfLoop.TargetKind, selfLoop.TargetID = "ProjectSpec", project.ID
	badAction := chapterProposal(chapters[0], chapters[1])
	badAction.Action = "DESTROY_EVERYTHING"
	duplicate := specProposal(project, chapters[0])
	stub := &recommendationModelStub{proposals: []ModelProposedDependency{
		specProposal(project, chapters[0]),
		fabricated, selfLoop, badAction, duplicate,
	}}
	svc.SetDependencyRecommendationModel(stub)
	raw, status, _, err := svc.RecommendDependencyCandidates(context.Background(), actor, project.ID, "rec-key-review", DependencyRecommendationInput{Reason: "冷启动建边"})
	if err != nil || status != 201 {
		t.Fatalf("recommend: status=%d err=%v", status, err)
	}
	outcome := decodeRecommendationOutcome(t, raw)
	var batch DependencyCandidateBatch
	if err := json.Unmarshal(outcome.Batch, &batch); err != nil {
		t.Fatal(err)
	}
	if len(batch.Candidates) != 1 {
		t.Fatalf("only the trustworthy proposal may be queued: %+v", batch.Candidates)
	}
	reasons := map[string]bool{}
	for _, drop := range outcome.Dropped {
		reasons[drop.Reason] = true
	}
	for _, want := range []string{DependencyRecommendationDropFabricatedObject, DependencyRecommendationDropInvalidField, DependencyRecommendationDropInvalidAction, DependencyRecommendationDropDuplicate} {
		if !reasons[want] {
			t.Fatalf("drop reason %s missing from %+v", want, outcome.Dropped)
		}
	}
}

func TestDependencyRecommendationRefusesAnEntirelyInvalidBatch(t *testing.T) {
	svc, actor, project, chapters, _ := recommendationFixture(t, nil, nil)
	fabricated := specProposal(project, chapters[0])
	fabricated.TargetID = "ghost-chapter"
	svc.SetDependencyRecommendationModel(&recommendationModelStub{proposals: []ModelProposedDependency{fabricated}})
	_, _, _, err := svc.RecommendDependencyCandidates(context.Background(), actor, project.ID, "rec-key-all-invalid", DependencyRecommendationInput{Reason: "冷启动建边"})
	if !errors.Is(err, ErrDependencyRecommendationNoValidCandidates) {
		t.Fatalf("an entirely invalid answer must be refused, got %v", err)
	}
	batches, listErr := svc.ListDependencyCandidates(context.Background(), actor, project.ID, "")
	if listErr != nil || len(batches) != 0 {
		t.Fatalf("a refused answer still left batches: %+v %v", batches, listErr)
	}
}

func TestDependencyRecommendationRespectsTheRejectionMemory(t *testing.T) {
	svc, actor, project, chapters, _ := recommendationFixture(t, nil, nil)
	svc.SetDependencyRecommendationModel(&recommendationModelStub{proposals: []ModelProposedDependency{specProposal(project, chapters[0])}})
	first := decodeRecommendationOutcome(t, mustRecommend(t, svc, actor, project.ID, "rec-key-mem-1"))
	var batch DependencyCandidateBatch
	if err := json.Unmarshal(first.Batch, &batch); err != nil || len(batch.Candidates) != 1 {
		t.Fatalf("first recommendation: %v %+v", err, batch)
	}
	if _, _, _, err := svc.DecideDependencyCandidate(context.Background(), actor, project.ID, batch.Candidates[0].ID, "rec-decide-reject", DependencyDecisionInput{
		ExpectedVersion: batch.Candidates[0].Version, Decision: "reject", Reason: "人工否决",
	}); err != nil {
		t.Fatalf("reject candidate: %v", err)
	}
	// A second run (new key) re-proposes the same tuple; the existing memory
	// suppresses it inside the same project version.
	second := decodeRecommendationOutcome(t, mustRecommend(t, svc, actor, project.ID, "rec-key-mem-2"))
	var replayedBatch DependencyCandidateBatch
	if err := json.Unmarshal(second.Batch, &replayedBatch); err != nil {
		t.Fatal(err)
	}
	if len(replayedBatch.Candidates) != 0 || len(replayedBatch.Suppressed) != 1 ||
		replayedBatch.Suppressed[0].Reason != DependencyCandidateSuppressedRejected {
		t.Fatalf("the rejected tuple came back: candidates=%+v suppressed=%+v", replayedBatch.Candidates, replayedBatch.Suppressed)
	}
}

func TestDependencyRecommendationSkipsExistingEdges(t *testing.T) {
	svc, actor, project, chapters, _ := recommendationFixture(t, nil, nil)
	specRef := ObjectVersionRef{Kind: "ProjectSpec", ID: project.ID, Version: strconv.FormatInt(project.SpecRevision, 10)}
	if _, _, _, err := svc.CreateDependencyEdge(context.Background(), actor, project.ID, "rec-seed-edge", DependencyEdgeInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Source:                  specRef,
		Target:                  chapterVersionRef(chapters[0]),
		Relation:                "uses", Action: "REGENERATE", Origin: "user", Reason: "人工先登记", Confidence: 1,
	}); err != nil {
		t.Fatalf("seed edge: %v", err)
	}
	svc.SetDependencyRecommendationModel(&recommendationModelStub{proposals: []ModelProposedDependency{
		specProposal(project, chapters[0]), // same tuple as the manual edge
		chapterProposal(chapters[0], chapters[1]),
	}})
	outcome := decodeRecommendationOutcome(t, mustRecommend(t, svc, actor, project.ID, "rec-key-existing"))
	var batch DependencyCandidateBatch
	if err := json.Unmarshal(outcome.Batch, &batch); err != nil {
		t.Fatal(err)
	}
	if len(batch.Candidates) != 1 || batch.Candidates[0].Target.ID != chapters[1].ID {
		t.Fatalf("only the new tuple may be queued: %+v", batch.Candidates)
	}
	if len(outcome.Dropped) != 1 || outcome.Dropped[0].Reason != DependencyRecommendationDropEdgePresent {
		t.Fatalf("existing-edge drop missing: %+v", outcome.Dropped)
	}
}

func TestDependencyRecommendationRequiresAWiredModel(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-set.db"))
	actor := Actor{TenantID: 311, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	_, _, _, err := svc.RecommendDependencyCandidates(context.Background(), actor, project.ID, "rec-key-unwired", DependencyRecommendationInput{Reason: "冷启动建边"})
	if !errors.Is(err, ErrDependencyRecommendationUnavailable) {
		t.Fatalf("an unwired recommender must fail closed, got %v", err)
	}
}

func mustRecommend(t *testing.T, svc *Service, actor Actor, projectID, key string) json.RawMessage {
	t.Helper()
	raw, status, _, err := svc.RecommendDependencyCandidates(context.Background(), actor, projectID, key, DependencyRecommendationInput{Reason: "冷启动建边"})
	if err != nil || status != 201 {
		t.Fatalf("recommend %s: status=%d err=%v", key, status, err)
	}
	return raw
}
