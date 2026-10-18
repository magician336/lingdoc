package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

// AC1 (ticket #200) + integration regression: naming a responsible person must
// not invalidate a task whose target version has moved on. The owner's ordinary
// flow is assign -> complete-against-the-current-version; if "assign" stale-marked
// the task, the follow-up "complete" could never close it and a change that
// invalidates chapter confirmations could never reach STABILIZED.
func TestG3200AssignDoesNotStaleAReplacedTarget(t *testing.T) {
	svc := g3_200_impactTaskStore(t, filepath.Join(t.TempDir(), "g3-200-assign.db"))
	actor := Actor{TenantID: 3210, UserID: "owner"}
	p, _, change := g3_200_appliedFixture(t, svc, actor)
	task := g3_200_taskFor(t, g3_200_tasks(t, svc, actor, p.ID), change.ID)

	g3_200_replaceTargetChapterBody(t, svc, actor, p, task.Target.ID, "g3-200-assign-replace", "G3-200 指派前被替换的正文")

	raw, _, _, err := svc.ActOnImpactTask(context.Background(), actor, p.ID, task.ID, "g3-200-assign", ImpactTaskActionInput{
		ExpectedVersion: task.Version, TargetRevision: p.CurrentContextRevision, Action: "assign",
		AssigneeID: actor.UserID, Reason: "指派给 Owner",
	})
	if err != nil {
		t.Fatalf("assigning a task with a replaced target must succeed, got %v", err)
	}
	var assigned ImpactTask
	if err := json.Unmarshal(raw, &assigned); err != nil {
		t.Fatal(err)
	}
	if assigned.Status == "STALE" {
		t.Fatalf("assign must not stale the task: %+v", assigned)
	}
	if assigned.AssigneeID != actor.UserID || assigned.Status != "OPEN" {
		t.Fatalf("assign did not record responsibility: %+v", assigned)
	}

	// "start" deliberately still stales on a replaced version: starting work
	// against a version that no longer exists is meaningless. The two actions
	// must not be collapsed into one rule.
	started, _, _, err := svc.ActOnImpactTask(context.Background(), actor, p.ID, task.ID, "g3-200-start", ImpactTaskActionInput{
		ExpectedVersion: assigned.Version, TargetRevision: p.CurrentContextRevision, Action: "start", Reason: "推进",
	})
	if err != nil {
		t.Fatalf("start on a replaced target must succeed as STALE, got %v", err)
	}
	var stale ImpactTask
	if err := json.Unmarshal(started, &stale); err != nil {
		t.Fatal(err)
	}
	if stale.Status != "STALE" || stale.StaleReason != ImpactTaskStaleTargetReplaced {
		t.Fatalf("start must still stale a replaced target: %+v", stale)
	}
	if slicesContains(ImpactTaskClosedStatuses, stale.Status) {
		t.Fatalf("STALE must never count as closure: %+v", stale)
	}

	// A task already poisoned as STALE must reject further work outright.
	if _, _, _, err := svc.ActOnImpactTask(context.Background(), actor, p.ID, task.ID, "g3-200-assign-after-stale", ImpactTaskActionInput{
		ExpectedVersion: stale.Version, TargetRevision: p.CurrentContextRevision, Action: "assign",
		AssigneeID: actor.UserID, Reason: "再次指派",
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("assign after STALE error = %v, want ErrInvalidState", err)
	}
}
