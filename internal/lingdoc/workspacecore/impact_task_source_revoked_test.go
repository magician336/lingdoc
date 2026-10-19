package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

// Acceptance: source revocation is one of the three
// invalidated-basis triggers #200 introduced, but it had no test at all.
//
// What this file records is the behaviour the production seam actually
// produces, verified by running it:
//
//   - Read side: `visibleImpactTasks` drops any task whose target citations the
//     caller can no longer revalidate, so a revoked source removes the task
//     from that actor's projection (impact_visibility.go:23).
//   - Write side: `ActOnImpactTask` fails CLOSED with the source error and
//     performs no state change - the obligation is neither discharged nor
//     silently mutated into STALE/DONE.
//   - Gate side: `StabilizeRevision` reads tasks from the raw store, not the
//     visibility-filtered projection, so a hidden task still blocks the gate.
//
// KNOWN DEFECT (reported by #203, deliberately NOT fixed here and NOT encoded
// as expected behaviour): the `source_revoked` reason constant is unreachable
// on this seam. `impactTaskStaleReason` returns `ImpactTaskStaleSourceRevoked`
// at impact_service.go:911, but its only call site (impact_service.go:
// 780) sits behind `checkedImpactWrite`, whose operation-level source recheck
// resolves `OperationSourceIDs("actOnImpactTask")` -- a strict superset of
// the same `impactSourceIDsForRefs([task.Target])` call -- for the same actor
// (impact_source_operation.go:189). So the operation recheck always fails first
// and the STALE branch is dead code. Fixing it would mean weakening a
// fail-closed revalidation, which is a product decision, not a test change.

// sourceRevokedFixture seeds a project whose chapters are written with a
// real citation, so impactSourceIDsForRefs actually resolves a source for the
// task's target and the revocation path is reachable at all.
func sourceRevokedFixture(t *testing.T, svc *Service, actor Actor) (Project, Chapter, ChangeSet) {
	t.Helper()
	p := seedActiveImpactProject(t, svc, actor)
	chapters := materializeChapterVersions(t, svc, actor, p, nil)
	chapter := chapters[0]
	if _, _, _, err := svc.SaveChapter(context.Background(), actor, p.ID, chapter.ID, "accept-sourced-body", SaveChapterInput{
		ExpectedChapterVersionID: chapter.CurrentVersionID, ExpectedSpecRevision: p.SpecRevision,
		BodyMarkdown: "验收 受限来源章节 [[source:source-secret]]", SourceIDs: []string{"source-secret"},
	}); err != nil {
		t.Fatalf("write source-backed chapter body: %v", err)
	}
	p = reloadProject(t, svc, actor, p.ID)
	chapter = mustListChapters(t, svc, actor, p.ID)[0]

	raw, _, _, err := svc.CreateChangeSet(context.Background(), actor, p.ID, "accept-revoked-change", CreateChangeSetInput{
		ExpectedContextRevision: p.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: p.Spec["research_subject"], NewValue: "验收 来源撤销主题"}},
		AffectedChapterIDs:      []string{chapter.ID}, Reason: "验收 来源撤销场景",
	})
	if err != nil {
		t.Fatalf("create changeset: %v", err)
	}
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.ApplyChangeSet(context.Background(), actor, p.ID, change.ID, "accept-revoked-apply"); err != nil {
		t.Fatalf("apply changeset: %v", err)
	}
	return reloadProject(t, svc, actor, p.ID), chapter, change
}

// fakeSourcePolicy returns the fake source revalidator so a test can flip
// its verdict the way a real revocation would, without swapping the store.
func fakeSourcePolicy(t *testing.T, svc *Service) *fakeSources {
	t.Helper()
	policy, ok := svc.sources.(*fakeSources)
	if !ok {
		t.Fatalf("service source policy = %T, want *fakeSources", svc.sources)
	}
	return policy
}

func actOnImpactTask(t *testing.T, svc *Service, actor Actor, projectID string, task ImpactTask, key, action string) (ImpactTask, error) {
	t.Helper()
	input := ImpactTaskActionInput{
		ExpectedVersion: task.Version, TargetRevision: task.TargetRevision, Action: action,
		Reason: "验收 " + action, HumanDecision: "reviewed",
	}
	if action == "assign" {
		input.AssigneeID = actor.UserID
	}
	raw, _, _, err := svc.ActOnImpactTask(context.Background(), actor, projectID, task.ID, key, input)
	if err != nil {
		return ImpactTask{}, err
	}
	var out ImpactTask
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

// Blocking side: a revoked citation fails the whole operation closed and
// changes nothing. The task must not be advanced, closed, or silently marked
// STALE behind the caller's back - and the delivery gate must still see it.
func TestSourceRevocationFailsClosedWithoutMutatingTask(t *testing.T) {
	svc := impactTaskStore(t, filepath.Join(t.TempDir(), "accept-source-revoked.db"))
	actor := Actor{TenantID: 3301, UserID: "owner"}
	p, _, change := sourceRevokedFixture(t, svc, actor)
	policy := fakeSourcePolicy(t, svc)

	task := taskFor(t, tasksInProject(t, svc, actor, p.ID), change.ID)
	if len(task.Causes) == 0 {
		t.Fatalf("fixture precondition: task carries no basis: %+v", task)
	}

	// Block: the citation behind the target can no longer be revalidated.
	policy.verdict = errors.New("citation revoked")
	if _, err := actOnImpactTask(t, svc, actor, p.ID, task, "accept-revoke", "start"); err == nil {
		t.Fatal("acting on a revoked-source target must fail closed")
	}

	// The revalidator really was consulted for the task's target citations.
	sawTargetSources := false
	for _, call := range policy.calls {
		for _, id := range call.sourceIDs {
			if id == "source-secret" {
				sawTargetSources = true
			}
		}
	}
	if !sawTargetSources {
		t.Fatalf("source revocation was never evaluated against the target's citations: %+v", policy.calls)
	}

	// Nothing was written: the task keeps its identity, version and status.
	// Read through the raw store, because the visibility projection is exactly
	// what the revocation is meant to hide.
	store := gormTransaction{svc.testDB()}
	persisted, err := store.ImpactTask(p.ID, task.ID)
	if err != nil {
		t.Fatalf("read back persisted task: %v", err)
	}
	if persisted.Version != task.Version || persisted.Status != "OPEN" || persisted.AssigneeID != "" ||
		persisted.StaleReason != "" || !persisted.StaleAt.IsZero() {
		t.Fatalf("a refused source revalidation mutated the task: %+v -> %+v", task, persisted)
	}

	// The read projection hides it from the caller who lost access...
	if visible := tasksInProject(t, svc, actor, p.ID); len(visible) != 0 {
		t.Fatalf("a task with revoked target citations stayed visible: %+v", visible)
	}

	// ...and the delivery gate stays closed rather than reporting a false
	// STABILIZED. Stabilization itself is a source-sensitive operation, so it
	// also fails closed on the same revalidation, and the read-only release
	// assertion independently refuses the revision.
	if raw, _, _, err := svc.StabilizeRevision(context.Background(), actor, p.ID, change.ID, "accept-revoked-stabilize"); err == nil {
		var result StabilizationResult
		if json.Unmarshal(raw, &result) == nil && result.Status == "STABILIZED" {
			t.Fatalf("revoked source produced a STABILIZED revision: %+v", result)
		}
	}
	if err := svc.AssertRevisionStabilized(context.Background(), actor, p.ID); err == nil {
		t.Fatal("the release assertion accepted a revision whose citations are revoked")
	}
}

// Recovery side: once a human actually restores access to the citation, the
// very same task is visible and workable again - no re-creation, no data loss.
func TestRestoredSourceAccessMakesTaskWorkableAgain(t *testing.T) {
	svc := impactTaskStore(t, filepath.Join(t.TempDir(), "accept-source-restored.db"))
	actor := Actor{TenantID: 3302, UserID: "owner"}
	p, _, change := sourceRevokedFixture(t, svc, actor)
	policy := fakeSourcePolicy(t, svc)
	task := taskFor(t, tasksInProject(t, svc, actor, p.ID), change.ID)

	policy.verdict = errors.New("citation revoked")
	if _, err := actOnImpactTask(t, svc, actor, p.ID, task, "accept-restore-revoke", "start"); err == nil {
		t.Fatal("blocking precondition: revoked source must fail closed")
	}
	if visible := tasksInProject(t, svc, actor, p.ID); len(visible) != 0 {
		t.Fatalf("blocking precondition: task still visible under revocation: %+v", visible)
	}

	// Recover: the human restores access to the citation.
	policy.verdict = nil

	readBack := taskFor(t, tasksInProject(t, svc, actor, p.ID), change.ID)
	if readBack.ID != task.ID || readBack.Version != task.Version || readBack.Status != "OPEN" {
		t.Fatalf("recovery lost or rewrote the task: %+v -> %+v", task, readBack)
	}

	assigned, err := actOnImpactTask(t, svc, actor, p.ID, readBack, "accept-recovered-assign", "assign")
	if err != nil {
		t.Fatalf("a task whose access was restored must be assignable again, got %v", err)
	}
	if assigned.AssigneeID != actor.UserID {
		t.Fatalf("restored task did not record responsibility: %+v", assigned)
	}
	advanced, err := actOnImpactTask(t, svc, actor, p.ID, assigned, "accept-recovered-start", "start")
	if err != nil {
		t.Fatalf("a task whose access was restored must be workable, got %v", err)
	}
	if advanced.Status == "STALE" {
		t.Fatalf("restored access still produced STALE: %+v", advanced)
	}
	if advanced.Status != "IN_PROGRESS" || advanced.Version != assigned.Version+1 {
		t.Fatalf("recovered task did not advance: %+v", advanced)
	}
}
