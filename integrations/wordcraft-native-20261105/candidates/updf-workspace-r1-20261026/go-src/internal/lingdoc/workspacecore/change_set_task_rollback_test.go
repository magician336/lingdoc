package workspacecore

import (
	"context"
	"strings"
	"testing"
)

// The audit and the chapter confirmations each have their own injected-failure
// coverage. ImpactTask persistence is the third write in the same apply
// transaction, and it had none: the only existing case drives
// CommitChangeImpactTasks inside a hand-built transaction rather than through
// ApplyChangeSet, so it never proved that a failed task insert rolls back the
// revision and the business writes that preceded it.
func TestApplyChangeSetRollsBackWhenImpactTaskWriteFails(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	chapter := extendedChapter(t, svc, actor, p)
	change := extendedChange(t, svc, actor, p, chapter)
	ctx := context.Background()

	before, err := svc.GetProject(ctx, actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	confirmedBefore := func() []bool {
		chapters, err := svc.ListChapters(ctx, actor, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		states := make([]bool, 0, len(chapters))
		for _, chapter := range chapters {
			states = append(states, chapter.ConfirmationValid)
		}
		return states
	}()
	confirmationsBefore := countRows(t, svc, "lingdoc_chapter_confirmations")
	tasksBefore := countImpactTasks(t, svc, actor, p.ID)

	// Fail only the task rows, so the audit and the confirmation updates keep
	// working and the rollback cannot be attributed to an earlier write.
	if err := svc.testDB().Exec("CREATE TRIGGER apply_task_write_failure BEFORE INSERT ON lingdoc_g3_records WHEN NEW.kind = 'task' BEGIN SELECT RAISE(ABORT, 'impact task write unavailable'); END").Error; err != nil {
		t.Fatal(err)
	}
	_, status, replay, err := svc.ApplyChangeSet(ctx, actor, p.ID, change.ID, "apply-task-write-failure")
	if err == nil || !strings.Contains(err.Error(), "impact task write unavailable") || replay {
		t.Fatalf("injected task write failure did not abort the apply: status=%d replay=%v err=%v", status, replay, err)
	}
	if err := svc.testDB().Exec("DROP TRIGGER apply_task_write_failure").Error; err != nil {
		t.Fatal(err)
	}

	// Nothing the apply had already written may survive its own failure.
	after, err := svc.GetProject(ctx, actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.CurrentContextRevision != before.CurrentContextRevision || after.ProjectVersion != before.ProjectVersion || after.SpecRevision != before.SpecRevision {
		t.Fatalf("failed task write left a partial revision\nbefore=%+v\nafter=%+v", before, after)
	}
	if after.Spec["research_goal"] != before.Spec["research_goal"] {
		t.Fatalf("failed task write committed the business value: %q -> %q", before.Spec["research_goal"], after.Spec["research_goal"])
	}
	if got := confirmedBefore; len(got) > 0 {
		chapters, err := svc.ListChapters(ctx, actor, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		for i, chapter := range chapters {
			if chapter.ConfirmationValid != got[i] {
				t.Fatalf("failed task write changed confirmation validity of %s: %v -> %v", chapter.ID, got[i], chapter.ConfirmationValid)
			}
		}
	}
	if got := countRows(t, svc, "lingdoc_chapter_confirmations"); got != confirmationsBefore {
		t.Fatalf("failed task write changed the confirmation count: %d -> %d", confirmationsBefore, got)
	}
	if got := countImpactTasks(t, svc, actor, p.ID); got != tasksBefore {
		t.Fatalf("failed task write left tasks behind: %d -> %d", tasksBefore, got)
	}

	// The proposal itself stays applied-able: a failed apply is retryable, not
	// a permanent poisoning of the record.
	if _, status, replay, err := svc.ApplyChangeSet(ctx, actor, p.ID, change.ID, "apply-task-write-failure"); err != nil || status != 200 || replay {
		t.Fatalf("retry after rollback: status=%d replay=%v err=%v", status, replay, err)
	}
	current, err := svc.GetProject(ctx, actor, p.ID)
	if err != nil || current.CurrentContextRevision != before.CurrentContextRevision+1 || current.Spec["research_goal"] != "新的合成目标" {
		t.Fatalf("retry did not apply exactly one revision: %+v err=%v", current, err)
	}
	if got := countImpactTasks(t, svc, actor, p.ID); got <= tasksBefore {
		t.Fatalf("successful retry created no impact tasks: %d -> %d", tasksBefore, got)
	}
}

func countImpactTasks(t *testing.T, svc *Service, actor Actor, projectID string) int {
	t.Helper()
	tasks, err := svc.ListImpactTasks(context.Background(), actor, projectID)
	if err != nil {
		t.Fatal(err)
	}
	return len(tasks)
}

func countRows(t *testing.T, svc *Service, table string) int {
	t.Helper()
	var n int64
	if err := svc.testDB().Table(table).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return int(n)
}
