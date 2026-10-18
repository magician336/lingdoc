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
func TestImpactTaskAssignSurvivesReplacedTarget(t *testing.T) {
	svc := impactTaskStore(t, filepath.Join(t.TempDir(), "impact-assign.db"))
	actor := Actor{TenantID: 3210, UserID: "owner"}
	p, _, change := appliedChangeFixture(t, svc, actor)
	task := taskFor(t, tasksInProject(t, svc, actor, p.ID), change.ID)

	replaceTargetChapterBody(t, svc, actor, p, task.Target.ID, "impact-assign-replace", "影响任务 指派前被替换的正文")

	raw, _, _, err := svc.ActOnImpactTask(context.Background(), actor, p.ID, task.ID, "impact-assign", ImpactTaskActionInput{
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
	started, _, _, err := svc.ActOnImpactTask(context.Background(), actor, p.ID, task.ID, "impact-start", ImpactTaskActionInput{
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
	if _, _, _, err := svc.ActOnImpactTask(context.Background(), actor, p.ID, task.ID, "impact-assign-after-stale", ImpactTaskActionInput{
		ExpectedVersion: stale.Version, TargetRevision: p.CurrentContextRevision, Action: "assign",
		AssigneeID: actor.UserID, Reason: "再次指派",
	}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("assign after STALE error = %v, want ErrInvalidState", err)
	}
}

// The same carve-out must cover "wait": parking a task while a human decision is
// outstanding does not work the task against its recorded version either, so a
// replaced version says nothing about whether the task may sit in WAITING_USER.
// Without this, the ordinary "suspend -> someone re-confirms -> resume -> complete"
// flow ends in a terminal STALE task nobody can close.
func TestImpactTaskWaitSurvivesReplacedTarget(t *testing.T) {
	svc := impactTaskStore(t, filepath.Join(t.TempDir(), "impact-wait.db"))
	actor := Actor{TenantID: 3211, UserID: "owner"}
	p, _, change := appliedChangeFixture(t, svc, actor)
	task := taskFor(t, tasksInProject(t, svc, actor, p.ID), change.ID)

	replaceTargetChapterBody(t, svc, actor, p, task.Target.ID, "impact-wait-replace", "影响任务 挂起前被替换的正文")

	// "wait" is only meaningful for the responsible person, so take ownership
	// first. Assigning must itself survive the replaced version.
	assignedRaw, _, _, err := svc.ActOnImpactTask(context.Background(), actor, p.ID, task.ID, "impact-wait-assign", ImpactTaskActionInput{
		ExpectedVersion: task.Version, TargetRevision: p.CurrentContextRevision, Action: "assign",
		AssigneeID: actor.UserID, Reason: "指派给 Owner",
	})
	if err != nil {
		t.Fatalf("assign before wait failed: %v", err)
	}
	var assigned ImpactTask
	if err := json.Unmarshal(assignedRaw, &assigned); err != nil {
		t.Fatal(err)
	}

	raw, _, _, err := svc.ActOnImpactTask(context.Background(), actor, p.ID, task.ID, "impact-wait", ImpactTaskActionInput{
		ExpectedVersion: assigned.Version, TargetRevision: p.CurrentContextRevision, Action: "wait", Reason: "等待人工判断",
	})
	if err != nil {
		t.Fatalf("parking a task with a replaced target must succeed, got %v", err)
	}
	var waiting ImpactTask
	if err := json.Unmarshal(raw, &waiting); err != nil {
		t.Fatal(err)
	}
	if waiting.Status == "STALE" {
		t.Fatalf("wait must not stale the task: %+v", waiting)
	}
	if waiting.Status != "WAITING_USER" {
		t.Fatalf("wait did not park the task: %+v", waiting)
	}
}
