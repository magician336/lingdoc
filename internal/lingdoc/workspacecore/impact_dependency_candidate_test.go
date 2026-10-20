package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

// The AI-proposed dependency candidates (G3 gap closure #252, AC-G3-15).

func decodeCandidateBatch(t *testing.T, raw json.RawMessage) DependencyCandidateBatch {
	t.Helper()
	var batch DependencyCandidateBatch
	if err := json.Unmarshal(raw, &batch); err != nil {
		t.Fatalf("decode dependency candidate batch: %v (%s)", err, raw)
	}
	return batch
}

func decodeCandidateDecision(t *testing.T, raw json.RawMessage) DependencyCandidateDecisionResult {
	t.Helper()
	var result DependencyCandidateDecisionResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode dependency candidate decision: %v (%s)", err, raw)
	}
	return result
}

// candidateFixture submits one batch of two proposals and returns the batch.
func candidateFixture(t *testing.T, svc *Service, actor Actor, project Project, source, target ObjectVersionRef, key string) DependencyCandidateBatch {
	t.Helper()
	raw, status, replay, err := svc.SubmitDependencyCandidateBatch(context.Background(), actor, project.ID, key, DependencyCandidateBatchInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Reason:                  "研究条件、证据卡和第 2-4 章",
		Scope: DependencyCandidateScope{
			Objects: []ObjectVersionRef{source, target},
			Reason:  "用户选择的范围",
		},
		Candidates: []DependencyCandidateInput{
			{Source: source, Target: target, Relation: "supports", Action: "REVALIDATE_EVIDENCE",
				Evidence: "第 2 章结论引用了证据卡 E12", Reason: "章节结论依赖该证据", Confidence: 0.6, ModelRunID: "run-1"},
			{Source: target, Target: source, Relation: "constrains", Action: "REGENERATE",
				Evidence: "证据卡 E12 的版本已经落后", Reason: "证据变化需要重算章节", Confidence: 0.4, ModelRunID: "run-1"},
		},
	})
	if err != nil || status != 201 || replay {
		t.Fatalf("submit candidate batch: status=%d replay=%v err=%v", status, replay, err)
	}
	return decodeCandidateBatch(t, raw)
}

// AC-G3-15: a batch shows the object versions, the evidence and the proposed
// rationale; only the confirmed candidates reach the formal dependency graph.
func TestDependencyCandidateBatchCarriesEvidenceAndOnlyDecidedOnesEnterTheGraph(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-set.db"))
	actor := Actor{TenantID: 301, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters := materializeChapterVersions(t, svc, actor, project, nil)
	fresh, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	project = fresh
	source, target := chapterVersionRef(chapters[0]), chapterVersionRef(chapters[1])

	batch := candidateFixture(t, svc, actor, project, source, target, "change-candidate-batch-1")
	if len(batch.Candidates) != 2 || batch.ID == "" || batch.IdempotencyKey != "change-candidate-batch-1" {
		t.Fatalf("candidate batch = %+v", batch)
	}
	for _, candidate := range batch.Candidates {
		if candidate.Source.Version == "" || candidate.Target.Version == "" {
			t.Fatalf("candidate lost the object version: %+v", candidate)
		}
		if candidate.Evidence == "" || candidate.Reason == "" {
			t.Fatalf("candidate lost the evidence or the proposed rationale: %+v", candidate)
		}
		if candidate.Status != DependencyCandidateStatusProposed || candidate.ProjectVersion != project.ProjectVersion {
			t.Fatalf("candidate state = %+v", candidate)
		}
	}

	// A candidate is not an edge: nothing entered the formal dependency graph.
	edges, err := svc.ListDependencyEdges(context.Background(), actor, project.ID)
	if err != nil || len(edges) != 0 {
		t.Fatalf("candidates entered the dependency graph: %+v %v", edges, err)
	}

	// Confirm the first candidate.
	confirmedRaw, status, replay, err := svc.DecideDependencyCandidate(context.Background(), actor, project.ID, batch.Candidates[0].ID, "change-candidate-decide-confirm", DependencyDecisionInput{
		ExpectedVersion: batch.Candidates[0].Version, Decision: "confirm", Reason: "人工确认这条依赖"})
	if err != nil || status != 200 || replay {
		t.Fatalf("confirm candidate: status=%d replay=%v err=%v", status, replay, err)
	}
	confirmed := decodeCandidateDecision(t, confirmedRaw)
	if confirmed.Candidate.Status != DependencyCandidateStatusConfirmed || confirmed.Edge == nil {
		t.Fatalf("confirmed candidate = %+v", confirmed)
	}
	if confirmed.Edge.Status != "confirmed" || confirmed.Edge.Origin != "ai" || confirmed.Edge.Reason != "人工确认这条依赖" {
		t.Fatalf("confirmed edge = %+v", confirmed.Edge)
	}
	if confirmed.Edge.Source != source || confirmed.Edge.Target != target {
		t.Fatalf("confirmed edge rebound the endpoints: %+v", confirmed.Edge)
	}

	// Modify the second candidate: the edge carries the corrected reason, the
	// corrected relation and remembers the replaced tuple.
	modifiedRaw, _, _, err := svc.DecideDependencyCandidate(context.Background(), actor, project.ID, batch.Candidates[1].ID, "change-candidate-decide-modify", DependencyDecisionInput{
		ExpectedVersion: batch.Candidates[1].Version, Decision: "modify", Reason: "人工修正为强制复核",
		Relation: pointerTo("governs"), Action: pointerTo("REVIEW_REQUIRED"), Confidence: pointerTo(0.9)})
	if err != nil {
		t.Fatalf("modify candidate: %v", err)
	}
	modified := decodeCandidateDecision(t, modifiedRaw)
	if modified.Candidate.Status != DependencyCandidateStatusModified || modified.Edge == nil {
		t.Fatalf("modified candidate = %+v", modified)
	}
	if modified.Edge.Relation != "governs" || modified.Edge.Action != "REVIEW_REQUIRED" || modified.Edge.Confidence != 0.9 {
		t.Fatalf("modified edge lost the correction: %+v", modified.Edge)
	}
	if modified.Edge.Reason != "人工修正为强制复核" {
		t.Fatalf("modified edge lost the human reason: %+v", modified.Edge)
	}
	if modified.Edge.SupersededRelation != "constrains" || modified.Edge.SupersededAction != "REGENERATE" {
		t.Fatalf("modified edge forgot the replaced tuple: %+v", modified.Edge)
	}

	// Both decided candidates are in the graph, and nothing else is.
	edges, err = svc.ListDependencyEdges(context.Background(), actor, project.ID)
	if err != nil || len(edges) != 2 {
		t.Fatalf("edges after the decisions: %+v %v", edges, err)
	}
	for _, edge := range edges {
		if edge.Status != "confirmed" {
			t.Fatalf("an undecided candidate reached the graph: %+v", edge)
		}
	}
}

// The third state: a rejected candidate never becomes an edge, and the
// rejection is remembered.
func TestDependencyCandidateRejectProducesNoEdge(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-set.db"))
	actor := Actor{TenantID: 301, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters := materializeChapterVersions(t, svc, actor, project, nil)
	fresh, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	project = fresh
	batch := candidateFixture(t, svc, actor, project, chapterVersionRef(chapters[0]), chapterVersionRef(chapters[1]), "change-candidate-batch-reject")

	raw, _, _, err := svc.DecideDependencyCandidate(context.Background(), actor, project.ID, batch.Candidates[0].ID, "change-candidate-decide-reject", DependencyDecisionInput{
		ExpectedVersion: batch.Candidates[0].Version, Decision: "reject", Reason: "人工否决"})
	if err != nil {
		t.Fatalf("reject candidate: %v", err)
	}
	rejected := decodeCandidateDecision(t, raw)
	if rejected.Candidate.Status != DependencyCandidateStatusRejected {
		t.Fatalf("rejected candidate = %+v", rejected.Candidate)
	}
	if rejected.Edge != nil {
		t.Fatalf("a rejected candidate produced an edge: %+v", rejected.Edge)
	}
	edges, err := svc.ListDependencyEdges(context.Background(), actor, project.ID)
	if err != nil || len(edges) != 0 {
		t.Fatalf("a rejected candidate reached the dependency graph: %+v %v", edges, err)
	}
}

// AC-G3-15: a refused or corrected candidate is not recommended again inside
// the same project version. After the project version moved on it may appear
// again.
func TestDependencyCandidateRejectedOrModifiedIsNotReproposedInSameProjectVersion(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-set.db"))
	actor := Actor{TenantID: 301, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters := materializeChapterVersions(t, svc, actor, project, nil)
	fresh, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	project = fresh
	source, target := chapterVersionRef(chapters[0]), chapterVersionRef(chapters[1])
	batch := candidateFixture(t, svc, actor, project, source, target, "change-candidate-batch-1")

	if _, _, _, err := svc.DecideDependencyCandidate(context.Background(), actor, project.ID, batch.Candidates[0].ID, "change-candidate-decide-reject", DependencyDecisionInput{
		ExpectedVersion: batch.Candidates[0].Version, Decision: "reject", Reason: "人工否决"}); err != nil {
		t.Fatalf("reject candidate: %v", err)
	}
	if _, _, _, err := svc.DecideDependencyCandidate(context.Background(), actor, project.ID, batch.Candidates[1].ID, "change-candidate-decide-modify", DependencyDecisionInput{
		ExpectedVersion: batch.Candidates[1].Version, Decision: "modify", Reason: "人工修正",
		Relation: pointerTo("governs"), Action: pointerTo("REVIEW_REQUIRED")}); err != nil {
		t.Fatalf("modify candidate: %v", err)
	}

	// The next run in the SAME project version does not re-propose either tuple.
	again := candidateFixtureExpecting(t, svc, actor, project, source, target, "change-candidate-batch-2")
	if len(again.Candidates) != 0 {
		t.Fatalf("the same version re-proposed decided candidates: %+v", again.Candidates)
	}
	if len(again.Suppressed) != 2 {
		t.Fatalf("suppressed candidates = %+v", again.Suppressed)
	}
	reasons := map[string]bool{}
	for _, suppression := range again.Suppressed {
		reasons[suppression.Reason] = true
	}
	if !reasons[DependencyCandidateSuppressedRejected] || !reasons[DependencyCandidateSuppressedModified] {
		t.Fatalf("suppression reasons = %+v", again.Suppressed)
	}

	// The project version moves on: the tuples may be proposed again. The
	// version moves through the real application path (an applied ChangeSet),
	// not through a fixture write.
	changeRaw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-candidate-change", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: "新主题"}},
		AffectedChapterIDs:      []string{chapters[0].ID, chapters[1].ID},
		Reason:                  "研究对象发生变化"})
	if err != nil {
		t.Fatalf("create changeset: %v", err)
	}
	var change ChangeSet
	if err := json.Unmarshal(changeRaw, &change); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.ApplyChangeSet(context.Background(), actor, project.ID, change.ID, "change-candidate-apply"); err != nil {
		t.Fatalf("apply changeset: %v", err)
	}
	moved, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if moved.ProjectVersion == project.ProjectVersion {
		t.Fatalf("the project version did not move: %+v", moved)
	}
	third := candidateFixtureExpecting(t, svc, actor, moved, source, target, "change-candidate-batch-3")
	if len(third.Candidates) != 2 {
		t.Fatalf("the candidates did not reappear after the version moved: %+v", third)
	}
}

// candidateFixtureExpecting submits the same batch and returns it even when
// every candidate was suppressed.
func candidateFixtureExpecting(t *testing.T, svc *Service, actor Actor, project Project, source, target ObjectVersionRef, key string) DependencyCandidateBatch {
	t.Helper()
	raw, status, replay, err := svc.SubmitDependencyCandidateBatch(context.Background(), actor, project.ID, key, DependencyCandidateBatchInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Reason:                  "再次运行 AI 依赖导入",
		Scope:                   DependencyCandidateScope{Objects: []ObjectVersionRef{source, target}},
		Candidates: []DependencyCandidateInput{
			{Source: source, Target: target, Relation: "supports", Action: "REVALIDATE_EVIDENCE",
				Evidence: "第 2 章结论引用了证据卡 E12", Reason: "章节结论依赖该证据", Confidence: 0.6},
			{Source: target, Target: source, Relation: "constrains", Action: "REGENERATE",
				Evidence: "证据卡 E12 的版本已经落后", Reason: "证据变化需要重算章节", Confidence: 0.4},
		},
	})
	if err != nil || status != 201 || replay {
		t.Fatalf("submit candidate batch: status=%d replay=%v err=%v", status, replay, err)
	}
	return decodeCandidateBatch(t, raw)
}

// The version the candidate was raised against is re-checked when a human
// confirms: a candidate whose basis moved on requires re-evaluation.
func TestDependencyCandidateConfirmRechecksTheObjectVersion(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-set.db"))
	actor := Actor{TenantID: 301, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters := materializeChapterVersions(t, svc, actor, project, nil)
	fresh, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	project = fresh
	source, target := chapterVersionRef(chapters[0]), chapterVersionRef(chapters[1])
	batch := candidateFixture(t, svc, actor, project, source, target, "change-candidate-batch-stale")

	// The target chapter moves on: the candidate's basis is behind.
	if _, _, _, err := svc.SaveChapter(context.Background(), actor, project.ID, chapters[1].ID, "change-candidate-chapter", SaveChapterInput{
		ExpectedChapterVersionID: chapters[1].CurrentVersionID,
		ExpectedSpecRevision:     project.SpecRevision,
		BodyMarkdown:             "新的章节正文",
		SourceIDs:                []string{}}); err != nil {
		t.Fatalf("save chapter: %v", err)
	}

	if _, _, _, err := svc.DecideDependencyCandidate(context.Background(), actor, project.ID, batch.Candidates[0].ID, "change-candidate-decide-stale", DependencyDecisionInput{
		ExpectedVersion: batch.Candidates[0].Version, Decision: "confirm", Reason: "确认这条依赖"}); !errors.Is(err, ErrDependencyCandidateStale) {
		t.Fatalf("confirming a stale candidate: err=%v, want ErrDependencyCandidateStale", err)
	}
	edges, err := svc.ListDependencyEdges(context.Background(), actor, project.ID)
	if err != nil || len(edges) != 0 {
		t.Fatalf("a stale candidate was confirmed anyway: %+v %v", edges, err)
	}
	batches, err := svc.ListDependencyCandidates(context.Background(), actor, project.ID, "")
	if err != nil || len(batches) != 1 {
		t.Fatalf("read candidates: %+v %v", batches, err)
	}
	found := false
	for _, candidate := range batches[0].Candidates {
		if candidate.ID == batch.Candidates[0].ID {
			found = true
			if candidate.Status != DependencyCandidateStatusStale {
				t.Fatalf("stale candidate status = %q, want stale", candidate.Status)
			}
		}
	}
	if !found {
		t.Fatalf("the stale candidate disappeared: %+v", batches[0].Candidates)
	}
}

// The idempotency key of the batch: a replay never produces a second batch.
func TestDependencyCandidateBatchIsIdempotent(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-set.db"))
	actor := Actor{TenantID: 301, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters := materializeChapterVersions(t, svc, actor, project, nil)
	fresh, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	project = fresh
	source, target := chapterVersionRef(chapters[0]), chapterVersionRef(chapters[1])
	input := DependencyCandidateBatchInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		IdempotencyKey:          "candidate-batch-key-1",
		Reason:                  "研究条件、证据卡和第 2-4 章",
		Scope:                   DependencyCandidateScope{Objects: []ObjectVersionRef{source, target}},
		Candidates: []DependencyCandidateInput{
			{Source: source, Target: target, Relation: "supports", Action: "REVALIDATE_EVIDENCE",
				Evidence: "第 2 章结论引用了证据卡 E12", Reason: "章节结论依赖该证据", Confidence: 0.6},
		},
	}
	raw, status, replay, err := svc.SubmitDependencyCandidateBatch(context.Background(), actor, project.ID, "change-candidate-transport-key", input)
	if err != nil || status != 201 || replay {
		t.Fatalf("submit candidate batch: status=%d replay=%v err=%v", status, replay, err)
	}
	first := decodeCandidateBatch(t, raw)

	// The same transport key replays the same batch.
	replayedRaw, _, replayed, err := svc.SubmitDependencyCandidateBatch(context.Background(), actor, project.ID, "change-candidate-transport-key", input)
	if err != nil || !replayed {
		t.Fatalf("replay candidate batch: replay=%v err=%v", replayed, err)
	}
	if decodeCandidateBatch(t, replayedRaw).ID != first.ID {
		t.Fatalf("the replay produced another batch: %s vs %s", decodeCandidateBatch(t, replayedRaw).ID, first.ID)
	}

	// Another transport key with the same batch key returns the same batch and
	// writes no second candidate.
	otherRaw, otherStatus, _, err := svc.SubmitDependencyCandidateBatch(context.Background(), actor, project.ID, "change-candidate-other-transport", input)
	if err != nil || otherStatus != 200 {
		t.Fatalf("second submit with the same batch key: status=%d err=%v", otherStatus, err)
	}
	if decodeCandidateBatch(t, otherRaw).ID != first.ID {
		t.Fatalf("the batch key produced a second batch: %+v", decodeCandidateBatch(t, otherRaw))
	}
	batches, err := svc.ListDependencyCandidates(context.Background(), actor, project.ID, "")
	if err != nil || len(batches) != 1 || len(batches[0].Candidates) != 1 {
		t.Fatalf("candidate batches after the replay: %+v %v", batches, err)
	}

	// The same batch key with different content is a conflict, not an
	// overwrite.
	changed := input
	changed.Candidates = []DependencyCandidateInput{{
		Source: source, Target: target, Relation: "supports", Action: "REGENERATE",
		Evidence: "另一段依据", Reason: "另一个理由", Confidence: 0.7}}
	if _, _, _, err := svc.SubmitDependencyCandidateBatch(context.Background(), actor, project.ID, "change-candidate-other-transport", changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("the same batch key with other content: err=%v, want ErrIdempotencyConflict", err)
	}
}

// A candidate without its evidence is refused: a proposal the user cannot
// check is not a proposal.
func TestDependencyCandidateWithoutEvidenceIsRefused(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-set.db"))
	actor := Actor{TenantID: 301, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters := materializeChapterVersions(t, svc, actor, project, nil)
	fresh, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	project = fresh
	if _, _, _, err := svc.SubmitDependencyCandidateBatch(context.Background(), actor, project.ID, "change-candidate-no-evidence", DependencyCandidateBatchInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Reason:                  "研究条件、证据卡和第 2-4 章",
		Candidates: []DependencyCandidateInput{{
			Source: chapterVersionRef(chapters[0]), Target: chapterVersionRef(chapters[1]),
			Relation: "supports", Action: "REVALIDATE_EVIDENCE", Reason: "没有依据片段", Confidence: 0.6}}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("candidate without evidence: err=%v, want ErrInvalidRequest", err)
	}
	batches, err := svc.ListDependencyCandidates(context.Background(), actor, project.ID, "")
	if err != nil || len(batches) != 0 {
		t.Fatalf("a refused batch was written: %+v %v", batches, err)
	}
}

// AC-G3-13: an undecided candidate is not part of the dependency graph, so it
// cannot invalidate a chapter and cannot gate a delivery on its own.
func TestUnconfirmedDependencyCandidateNeverGatesTheImpactGraph(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-set.db"))
	actor := Actor{TenantID: 301, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	chapters := materializeChapterVersions(t, svc, actor, project, nil)
	fresh, err := svc.GetProject(context.Background(), actor, project.ID)
	if err != nil {
		t.Fatal(err)
	}
	project = fresh
	declared, suggested := chapterVersionRef(chapters[0]), chapterVersionRef(chapters[1])
	candidateFixture(t, svc, actor, project, declared, suggested, "change-candidate-graph")

	raw, _, _, err := svc.CreateChangeSet(context.Background(), actor, project.ID, "change-candidate-graph-change", CreateChangeSetInput{
		ExpectedContextRevision: project.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: "旧主题", NewValue: "新主题"}},
		AffectedChapterIDs:      []string{declared.ID},
		Reason:                  "研究对象发生变化"})
	if err != nil {
		t.Fatalf("create changeset: %v", err)
	}
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil {
		t.Fatal(err)
	}
	assessment, err := svc.GetImpactAssessment(context.Background(), actor, project.ID, change.ID)
	if err != nil {
		t.Fatalf("read assessment: %v", err)
	}
	for _, impact := range assessment.Impacts {
		if impact.Target == suggested {
			t.Fatalf("an undecided candidate produced an impact: %+v", impact)
		}
	}
	for _, object := range assessment.Objects {
		if object.Ref == suggested {
			t.Fatalf("an undecided candidate made a required object: %+v", object)
		}
	}
}
