package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// This file closes four #197 acceptance gaps that share one fixture: the
// extended impact store from impact_g6_extended_test.go, which is the only
// setup that carries the real lingdoc_g3_records table that ImpactTasks land in.
//
// Each test drives the public Service methods, never the store directly, so a
// regression that hides behind an internal seam cannot stay green here.

// B2: two ChangeSets built on the same base revision are applied at the same
// time by two goroutines. Exactly one may win, and — the assertion that was
// missing entirely — the loser must not leave an ImpactTask behind.
func TestConcurrentApplyLetsOnlyOneChangeSetProduceImpactTasks(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	// Match the production SQLite pool: concurrent service calls share one
	// connection, so the losing request reaches the version check instead of
	// failing an unsupported concurrent read-to-write transaction upgrade.
	sqlDB, err := svc.testDB().DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	chapter := extendedChapter(t, svc, actor, p)
	ctx := context.Background()

	// Two independent proposals on the same base revision. They must not
	// interfere at creation time, otherwise this would stop being a race test.
	first := pr150DecodeChange(t, mustCreateChange(t, svc, actor, p, chapter.ID, "race-first", "并发变更一", p.Spec["research_goal"]))
	second := pr150DecodeChange(t, mustCreateChange(t, svc, actor, p, chapter.ID, "race-second", "并发变更二", p.Spec["research_goal"]))
	if err := svc.repository.Transaction(ctx, TransactionOptions{}, func(tx Transaction) error {
		if err := AssessChangeImpact(tx, p, first); err != nil {
			return err
		}
		return AssessChangeImpact(tx, p, second)
	}); err != nil {
		t.Fatal(err)
	}

	before := mustGetProject(t, svc, actor, p.ID)
	tasksBefore := countImpactTasks(t, svc, actor, p.ID)

	var wg sync.WaitGroup
	results := make([]struct {
		body   json.RawMessage
		status int
		replay bool
		err    error
	}, 2)
	for i, change := range []ChangeSet{first, second} {
		wg.Add(1)
		go func(index int, change ChangeSet) {
			defer wg.Done()
			body, status, replay, err := svc.ApplyChangeSet(ctx, actor, p.ID, change.ID, "race-apply-"+change.ID)
			results[index] = struct {
				body   json.RawMessage
				status int
				replay bool
				err    error
			}{body: body, status: status, replay: replay, err: err}
		}(i, change)
	}
	wg.Wait()

	winners, losers := 0, 0
	winningChangeID := ""
	changes := []ChangeSet{first, second}
	for i, result := range results {
		switch {
		case result.err == nil && result.status == 200 && !result.replay:
			winners++
			winningChangeID = changes[i].ID
		case result.err == nil && result.status == 409 && !result.replay:
			// A stale proposal is an answered conflict with no Go error. Verify
			// that this specific response describes the losing proposal.
			var stale ChangeSet
			if err := json.Unmarshal(result.body, &stale); err != nil || stale.ID != changes[i].ID || stale.Status != ChangeSetStatusStale {
				t.Fatalf("apply %d returned a non-stale conflict: body=%s err=%v", i, result.body, err)
			}
			losers++
		case !result.replay && (result.status == 0 || result.status == 409) && errors.Is(result.err, ErrVersionConflict):
			losers++
		default:
			t.Fatalf("apply %d returned an unaccounted outcome: status=%d replay=%v err=%v", i, result.status, result.replay, result.err)
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("concurrent apply must produce exactly one winner, got %d winners and %d losers: %+v", winners, losers, results)
	}

	after := mustGetProject(t, svc, actor, p.ID)
	if after.CurrentContextRevision != before.CurrentContextRevision+1 {
		t.Fatalf("concurrent apply advanced the context revision by %d, want exactly 1", after.CurrentContextRevision-before.CurrentContextRevision)
	}
	if after.ProjectVersion != before.ProjectVersion+1 {
		t.Fatalf("concurrent apply advanced the project version by %d, want exactly 1", after.ProjectVersion-before.ProjectVersion)
	}

	// The gap this test exists for: the losing proposal must not contribute
	// tasks. Comparing against a single-application baseline is what makes the
	// count meaningful — "more than zero" would pass even if the loser wrote.
	tasksAfter := countImpactTasks(t, svc, actor, p.ID)
	if tasksAfter == tasksBefore {
		t.Fatalf("the winning apply created no impact tasks at all: %d -> %d", tasksBefore, tasksAfter)
	}
	tasks, err := svc.ListImpactTasks(ctx, actor, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.ChangeSetID == first.ID || task.ChangeSetID == second.ID {
			if task.ChangeSetID != winningChangeID {
				t.Fatalf("losing proposal produced impact task %s: change=%s winner=%s", task.ID, task.ChangeSetID, winningChangeID)
			}
			continue
		}
		t.Fatalf("impact task %s belongs to neither concurrent proposal: change=%s", task.ID, task.ChangeSetID)
	}
	// Exactly one proposal may have produced tasks.
	fromFirst, fromSecond := 0, 0
	for _, task := range tasks {
		switch task.ChangeSetID {
		case first.ID:
			fromFirst++
		case second.ID:
			fromSecond++
		}
	}
	if fromFirst > 0 && fromSecond > 0 {
		t.Fatalf("both concurrent proposals produced impact tasks: %d and %d", fromFirst, fromSecond)
	}
	if fromFirst == 0 && fromSecond == 0 {
		t.Fatalf("neither proposal produced impact tasks: %+v", tasks)
	}

	// A loser can either commit a stale answer or be rejected by a concurrent
	// consistency check that rolls back its transaction. It must never apply.
	for _, change := range changes {
		stored, err := svc.GetChangeSet(ctx, actor, p.ID, change.ID)
		if err != nil {
			t.Fatal(err)
		}
		if change.ID == winningChangeID {
			if stored.Status != ChangeSetStatusApplied {
				t.Fatalf("winning proposal ended in %q, want applied", stored.Status)
			}
			continue
		}
		if stored.Status != "assessed" && stored.Status != "stale" {
			t.Fatalf("the losing proposal ended in %q, want assessed or stale", stored.Status)
		}
		if stored.TargetContextRevision != nil {
			t.Fatalf("the losing proposal recorded a target revision: %+v", stored.TargetContextRevision)
		}
	}
}

// C3: a lost response means the caller retries with the same idempotency key.
// The retry must replay the original result and must not repeat any side effect.
func TestApplyChangeSetRetryAfterLostResponseRepeatsNoSideEffect(t *testing.T) {
	svc, actor, p := extendedImpactStore(t)
	chapter := extendedChapter(t, svc, actor, p)
	change := extendedChange(t, svc, actor, p, chapter)
	ctx := context.Background()

	before := mustGetProject(t, svc, actor, p.ID)
	tasksBefore := countImpactTasks(t, svc, actor, p.ID)
	auditBefore := countApplyAudits(t, svc, actor, p.ID)

	raw, status, replay, err := svc.ApplyChangeSet(ctx, actor, p.ID, change.ID, "lost-response-apply")
	if err != nil || status != 200 || replay {
		t.Fatalf("first apply: status=%d replay=%v err=%v", status, replay, err)
	}
	first := pr150DecodeChange(t, raw)
	afterFirst := mustGetProject(t, svc, actor, p.ID)
	tasksAfterFirst := countImpactTasks(t, svc, actor, p.ID)
	if tasksAfterFirst <= tasksBefore {
		t.Fatalf("the first apply created no impact tasks: %d -> %d", tasksBefore, tasksAfterFirst)
	}
	if afterFirst.CurrentContextRevision != before.CurrentContextRevision+1 {
		t.Fatalf("the first apply advanced the context revision by %d, want 1", afterFirst.CurrentContextRevision-before.CurrentContextRevision)
	}

	// The retry. Same key, same payload — this is the caller that never saw the
	// first response.
	retriedRaw, retryStatus, replayed, err := svc.ApplyChangeSet(ctx, actor, p.ID, change.ID, "lost-response-apply")
	if err != nil || retryStatus != 200 || !replayed {
		t.Fatalf("retry after a lost response: status=%d replayed=%v err=%v", retryStatus, replayed, err)
	}

	retried := pr150DecodeChange(t, retriedRaw)
	if retried.ID != first.ID || retried.Status != first.Status {
		t.Fatalf("retry returned a different result: first=%+v retry=%+v", first, retried)
	}

	// Every side effect must be counted exactly once across both calls.
	afterRetry := mustGetProject(t, svc, actor, p.ID)
	if afterRetry.CurrentContextRevision != afterFirst.CurrentContextRevision {
		t.Fatalf("retry advanced the context revision again: %d -> %d", afterFirst.CurrentContextRevision, afterRetry.CurrentContextRevision)
	}
	if afterRetry.ProjectVersion != afterFirst.ProjectVersion {
		t.Fatalf("retry advanced the project version again: %d -> %d", afterFirst.ProjectVersion, afterRetry.ProjectVersion)
	}
	if got := countImpactTasks(t, svc, actor, p.ID); got != tasksAfterFirst {
		t.Fatalf("retry created duplicate impact tasks: %d -> %d", tasksAfterFirst, got)
	}
	if got := countApplyAudits(t, svc, actor, p.ID); got != auditBefore+1 {
		t.Fatalf("apply audits must be written once, got %d (before %d)", got, auditBefore)
	}
}

// C5: a satisfied reassessment is a no-op. Beyond "does not advance the
// revision" it must also create no impact tasks and must not be rejectable.
func TestSatisfiedReassessmentCreatesNoTasksAndCannotBeRejected(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "satisfied-tasks.db"))
	actor := Actor{TenantID: 9421, UserID: "owner"}
	project := seedActiveImpactProject(t, svc, actor)
	ctx := context.Background()

	chapters, err := svc.ListChapters(ctx, actor, project.ID)
	if err != nil || len(chapters) == 0 {
		t.Fatalf("chapters: %v", err)
	}
	create := func(key, goal string) ChangeSet {
		t.Helper()
		return pr150DecodeChange(t, mustCreateChange(t, svc, actor, project, chapters[0].ID, key, goal, project.Spec["research_goal"]))
	}

	// The same base-revision trick as change_set_test.go: the second proposal
	// goes stale once the first one is applied, and reassessing it then finds
	// every target already current.
	//
	// Both proposals must target the SAME new value. Reassess rejects a
	// proposal whose old value no longer matches the current spec
	// (service_facade.go:2488), so a second, different goal would surface as
	// ErrVersionConflict instead of the satisfied path under test.
	first := create("satisfied-apply-first", "共同目标")
	proposal := create("satisfied-stale-proposal", "共同目标")
	if _, status, _, err := svc.ApplyChangeSet(ctx, actor, project.ID, first.ID, "satisfied-apply-first"); err != nil || status != 200 {
		t.Fatalf("apply first: status=%d err=%v", status, err)
	}
	if _, status, _, err := svc.ApplyChangeSet(ctx, actor, project.ID, proposal.ID, "satisfied-stale-proposal"); err != nil || status != 409 {
		t.Fatalf("mark the second proposal stale: status=%d err=%v", status, err)
	}

	tasksBefore := countImpactTasks(t, svc, actor, project.ID)
	before := mustGetProject(t, svc, actor, project.ID)

	raw, status, replay, err := svc.ReassessChangeSet(ctx, actor, project.ID, proposal.ID, "satisfied-reassess")
	if err != nil || status != 201 || replay {
		t.Fatalf("reassess into satisfied: status=%d replay=%v err=%v", status, replay, err)
	}
	satisfied := pr150DecodeChange(t, raw)
	if satisfied.Status != "satisfied" {
		t.Fatalf("reassessment status = %q, want satisfied", satisfied.Status)
	}

	// The missing assertion: satisfied means no work, including no tasks.
	if got := countImpactTasks(t, svc, actor, project.ID); got != tasksBefore {
		t.Fatalf("a satisfied reassessment created impact tasks: %d -> %d", tasksBefore, got)
	}

	// The other missing assertion: satisfied is a terminal no-op, so it is not
	// a decision the Owner can still make.
	if _, _, _, err := svc.ApplyChangeSet(ctx, actor, project.ID, satisfied.ID, "satisfied-apply"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("applying a satisfied result: err = %v, want ErrInvalidState", err)
	}
	if _, rejectStatus, _, err := svc.RejectChangeSet(ctx, actor, project.ID, satisfied.ID, "satisfied-reject"); err == nil {
		t.Fatalf("rejecting a satisfied result succeeded with status %d; it must be refused", rejectStatus)
	}

	// None of the refusals may have moved anything.
	after := mustGetProject(t, svc, actor, project.ID)
	if after.CurrentContextRevision != before.CurrentContextRevision || after.ProjectVersion != before.ProjectVersion {
		t.Fatalf("operating on a satisfied result mutated the project: before=%+v after=%+v", before, after)
	}
	if got := countImpactTasks(t, svc, actor, project.ID); got != tasksBefore {
		t.Fatalf("operating on a satisfied result created impact tasks: %d -> %d", tasksBefore, got)
	}
	stored, err := svc.GetChangeSet(ctx, actor, project.ID, satisfied.ID)
	if err != nil || stored.Status != "satisfied" {
		t.Fatalf("satisfied result changed state: %+v err=%v", stored, err)
	}
}

// A5: creating a ChangeSet requires an active, non-discarded project.
//
// One correction to the original acceptance wording, established by reading the
// schema rather than guessed: `discarded` is not a project status. The
// migration's CHECK constraint admits only ('draft', 'active'), so "非 active、
// discarded" is really "status is draft" plus the separate `discarded_at`
// column — two axes, each with its own case below.
//
// The discarded case is enforced at **two** independent layers, verified by
// mutation testing rather than by reading alone:
//
//   - authorizeCurrentProject (g5_policy.go:127) refuses any non-read action on
//     a discarded project;
//   - createAssessedChangeMutation (change_creation_mutation.go:23) repeats the
//     check at the mutation boundary.
//
// Removing either one alone leaves the test GREEN — the other still refuses.
// Only removing both turns it red (create returns 201). So this is deliberate
// defence in depth, and the coverage below pins the *behaviour* rather than any
// single line: a regression that deletes both guards, or swaps one for the
// other, is caught.
func TestCreateChangeSetRejectsInactiveAndDiscardedProjects(t *testing.T) {
	svc := testStore(t, filepath.Join(t.TempDir(), "change-guard.db"))
	ctx := context.Background()

	for _, tc := range []struct {
		name   string
		tenant uint64
		mutate func(t *testing.T, svc *Service, p Project)
	}{
		{
			name:   "status is draft rather than active",
			tenant: 5101,
			mutate: func(t *testing.T, svc *Service, p Project) {
				t.Helper()
				if err := svc.testDB().Table("lingdoc_projects").Where("id = ?", p.ID).Update("status", "draft").Error; err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name:   "discarded_at is set while status is still active",
			tenant: 5102,
			mutate: func(t *testing.T, svc *Service, p Project) {
				t.Helper()
				result := svc.testDB().Table("lingdoc_projects").Where("id = ?", p.ID).
					Update("discarded_at", time.Now().UTC())
				if result.Error != nil {
					t.Fatal(result.Error)
				}
				if result.RowsAffected != 1 {
					t.Fatalf("discarded_at mutation touched %d rows, want 1", result.RowsAffected)
				}
				// Read the column back through the service, not through gorm:
				// the guard under test reads the hydrated Project, so writing
				// the row is not evidence that the Project carries it.
				reloaded, err := svc.GetProject(context.Background(), Actor{TenantID: 5102, UserID: "owner"}, p.ID)
				if err != nil {
					t.Fatal(err)
				}
				if reloaded.DiscardedAt == nil {
					t.Fatal("discarded_at was written but the reloaded Project still reports nil")
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			actor := Actor{TenantID: tc.tenant, UserID: "owner"}
			project := seedActiveImpactProject(t, svc, actor)
			chapters, err := svc.ListChapters(ctx, actor, project.ID)
			if err != nil || len(chapters) == 0 {
				t.Fatalf("chapters: %v", err)
			}

			// The project must be writable before the mutation, otherwise the
			// case would pass for the wrong reason.
			if _, status, _, err := svc.CreateChangeSet(ctx, actor, project.ID, tc.name+"-control",
				CreateChangeSetInput{ExpectedContextRevision: project.CurrentContextRevision,
					Fields:             map[string]ChangeFieldInput{"research_goal": {OldValue: project.Spec["research_goal"], NewValue: "控制组目标"}},
					AffectedChapterIDs: []string{chapters[0].ID}, Reason: "控制组"}); err != nil || status != 201 {
				t.Fatalf("the control create must succeed: status=%d err=%v", status, err)
			}

			tc.mutate(t, svc, project)

			_, status, _, err := svc.CreateChangeSet(ctx, actor, project.ID, tc.name+"-after-mutation",
				CreateChangeSetInput{ExpectedContextRevision: project.CurrentContextRevision,
					Fields:             map[string]ChangeFieldInput{"research_goal": {OldValue: project.Spec["research_goal"], NewValue: "越权目标"}},
					AffectedChapterIDs: []string{chapters[0].ID}, Reason: "守卫用例"})
			if !errors.Is(err, ErrInvalidState) {
				t.Fatalf("create on a guarded project: status=%d err=%v, want ErrInvalidState", status, err)
			}
		})
	}
}

func mustCreateChange(t *testing.T, svc *Service, actor Actor, p Project, chapterID, key, reason, oldGoal string) json.RawMessage {
	t.Helper()
	raw, status, replay, err := svc.CreateChangeSet(context.Background(), actor, p.ID, key, CreateChangeSetInput{
		ExpectedContextRevision: p.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_goal": {OldValue: oldGoal, NewValue: reason}},
		AffectedChapterIDs:      []string{chapterID}, Reason: reason,
	})
	if err != nil || status != 201 || replay {
		t.Fatalf("create change set %s: status=%d replay=%v err=%v", key, status, replay, err)
	}
	return raw
}

func mustGetProject(t *testing.T, svc *Service, actor Actor, projectID string) Project {
	t.Helper()
	p, err := svc.GetProject(context.Background(), actor, projectID)
	if err != nil {
		t.Fatalf("get project: %v", err)
	}
	return p
}

func countApplyAudits(t *testing.T, svc *Service, actor Actor, projectID string) int {
	t.Helper()
	events, err := svc.ListAuditEvents(context.Background(), actor, projectID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Action == "project.change_set.apply" {
			count++
		}
	}
	return count
}
