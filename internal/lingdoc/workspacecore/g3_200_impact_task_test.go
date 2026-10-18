package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// g3_200_impactTaskStore reuses the real production-migration SQLite store, so
// these tests exercise the same persistence seam as the HTTP service.
func g3_200_impactTaskStore(t *testing.T, path string) *Service {
	t.Helper()
	return testStore(t, path)
}

// g3_200_appliedFixture drives a real ChangeSet from creation through apply, so
// the tasks under test are produced by the production commit path rather than
// hand-inserted.
func g3_200_appliedFixture(t *testing.T, svc *Service, actor Actor) (Project, Chapter, ChangeSet) {
	t.Helper()
	p := seedActiveG3Project(t, svc, actor)
	chapters := g3MaterializeChapters(t, svc, actor, p, map[string]string{})
	chapter := chapters[0]
	p = g3_200_current(t, svc, actor, p.ID)
	input := CreateChangeSetInput{ExpectedContextRevision: p.CurrentContextRevision,
		Fields:             map[string]ChangeFieldInput{"research_subject": {OldValue: p.Spec["research_subject"], NewValue: "G3-200 新主题"}},
		AffectedChapterIDs: []string{chapter.ID}, Reason: "G3-200 研究对象变更"}
	raw, _, _, err := svc.CreateChangeSet(context.Background(), actor, p.ID, "g3-200-change", input)
	if err != nil {
		t.Fatalf("create changeset: %v", err)
	}
	var change ChangeSet
	if err := json.Unmarshal(raw, &change); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.ApplyChangeSet(context.Background(), actor, p.ID, change.ID, "g3-200-apply"); err != nil {
		t.Fatalf("apply changeset: %v", err)
	}
	p = g3_200_current(t, svc, actor, p.ID)
	return p, chapter, change
}

// g3_200_markChangeStabilized moves the ChangeSet past the lifecycle
// precondition that AssertRevisionStabilized checks before it inspects tasks.
func g3_200_markChangeStabilized(t *testing.T, svc *Service, actor Actor, projectID, changeID string) {
	t.Helper()
	if err := svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		changes, ok := tx.(changeSetTransaction)
		if !ok {
			return ErrInvalidState
		}
		current, err := changes.ChangeSet(projectID, changeID)
		if err != nil {
			return err
		}
		next := current
		next.Status = "stabilized"
		return changes.UpdateChangeSet(current, next)
	}); err != nil {
		t.Fatalf("mark change stabilized: %v", err)
	}
}

// g3_200_addCollaborator seeds tenant membership plus a project membership that
// carries owner governance, because assigning an impact task requires the
// assignee to hold write capability on the project.
func g3_200_addCollaborator(t *testing.T, svc *Service, actor Actor, projectID string) {
	t.Helper()
	seedTenantMember(t, svc, actor)
	if err := svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		members, ok := tx.(ownerTransferStore)
		if !ok {
			return ErrInvalidState
		}
		return members.EnsureMember(projectID, actor.UserID, "owner")
	}); err != nil {
		t.Fatalf("seed project member: %v", err)
	}
}

// g3_200_replaceTargetChapterBody writes a new chapter version so the exact
// version an impact task names stops being current.
func g3_200_replaceTargetChapterBody(t *testing.T, svc *Service, actor Actor, p Project, chapterID, key, body string) {
	t.Helper()
	fresh := g3_200_current(t, svc, actor, p.ID)
	chapters := mustListChapters(t, svc, actor, p.ID)
	for _, chapter := range chapters {
		if chapter.ID != chapterID {
			continue
		}
		if _, _, _, err := svc.SaveChapter(context.Background(), actor, p.ID, chapter.ID, key, SaveChapterInput{
			ExpectedChapterVersionID: chapter.CurrentVersionID, ExpectedSpecRevision: fresh.SpecRevision,
			BodyMarkdown: body, SourceIDs: []string{}}); err != nil {
			t.Fatalf("replace chapter %s version: %v", chapter.ID, err)
		}
		return
	}
	t.Fatalf("chapter %s not found", chapterID)
}

func g3_200_current(t *testing.T, svc *Service, actor Actor, projectID string) Project {
	t.Helper()
	p, err := svc.GetProject(context.Background(), actor, projectID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func g3_200_tasks(t *testing.T, svc *Service, actor Actor, projectID string) []ImpactTask {
	t.Helper()
	tasks, err := svc.ListImpactTasks(context.Background(), actor, projectID)
	if err != nil {
		t.Fatalf("list impact tasks: %v", err)
	}
	return tasks
}

func g3_200_taskFor(t *testing.T, tasks []ImpactTask, changeID string) ImpactTask {
	t.Helper()
	for _, task := range tasks {
		if task.ChangeSetID == changeID {
			return task
		}
	}
	t.Fatalf("no impact task raised for change %s in %+v", changeID, tasks)
	return ImpactTask{}
}

// AC1: task identity is change_set_id + target_ref + target_revision +
// action_type. A replayed commit must not create a second task, distinct
// actions stay independent, and repeated causes merge without loss.
func TestG3200TaskIdentityIsIdempotentAndMergesCauses(t *testing.T) {
	svc := g3_200_impactTaskStore(t, filepath.Join(t.TempDir(), "g3-200-identity.db"))
	actor := Actor{TenantID: 3201, UserID: "owner"}
	p, chapter, change := g3_200_appliedFixture(t, svc, actor)
	tasks := g3_200_tasks(t, svc, actor, p.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected exactly one definite impact task, got %d: %+v", len(tasks), tasks)
	}
	task := tasks[0]

	// The persisted ID must equal the identity-derived ID, otherwise a
	// redelivered event could not address the same task after a restart.
	wantID := ImpactTaskID(p.ID, change.ID, chapterRefVersion(chapter), p.CurrentContextRevision, task.Action)
	if task.ID != wantID {
		t.Fatalf("task ID is not derived from its identity: got %s want %s", task.ID, wantID)
	}
	identity := ImpactTaskIdentity(task)
	if identity == "" || task.StaleReason != "" {
		t.Fatalf("unexpected fresh task: %+v", task)
	}

	// Replay the exact same commit out of band (as a duplicated or delayed
	// event would). It must merge, not duplicate.
	replay := task
	replay.Causes = append([]ImpactCause{}, task.Causes...)
	if err := svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		return CommitChangeImpactTasks(tx, p, change)
	}); err != nil {
		t.Fatalf("replayed commit: %v", err)
	}
	after := g3_200_tasks(t, svc, actor, p.ID)
	if len(after) != 1 {
		t.Fatalf("replayed commit duplicated the task: %d -> %d", len(tasks), len(after))
	}
	if after[0].ID != task.ID || after[0].Version != task.Version {
		t.Fatalf("idempotent redelivery mutated the task: %+v -> %+v", task, after[0])
	}
	if len(after[0].Causes) != len(task.Causes) {
		t.Fatalf("redelivery lost causes: %d -> %d", len(task.Causes), len(after[0].Causes))
	}

	// A new cause for the same identity merges in without losing the old ones.
	store := gormTransaction{svc.testDB()}
	merged := after[0]
	extra := ImpactCause{EdgeID: "edge-late", Source: ObjectVersionRef{Kind: "ProjectSpec", ID: p.ID, Version: "1"}, Reason: "延迟到达的依赖"}
	merged.Causes = append(merged.Causes, extra)
	if err := store.InsertImpactTask(merged); err != nil {
		t.Fatalf("merge late cause: %v", err)
	}
	got := g3_200_tasks(t, svc, actor, p.ID)
	if len(got) != 1 {
		t.Fatalf("cause merge created a second task: %d", len(got))
	}
	found := false
	for _, cause := range got[0].Causes {
		if cause == extra {
			found = true
		}
	}
	if !found || len(got[0].Causes) != len(task.Causes)+1 {
		t.Fatalf("late cause not merged: %+v", got[0].Causes)
	}

	// Different action types on the same target stay independent tasks.
	other := got[0]
	other.Action = "RECOMPUTE"
	other.ID = ImpactTaskID(p.ID, change.ID, other.Target, other.TargetRevision, other.Action)
	if err := store.InsertImpactTask(other); err != nil {
		t.Fatalf("insert distinct action: %v", err)
	}
	final := g3_200_tasks(t, svc, actor, p.ID)
	if len(final) != 2 {
		t.Fatalf("distinct action types collapsed into one task: %d %+v", len(final), final)
	}
	if final[0].ID == final[1].ID {
		t.Fatalf("distinct action types share an identity: %+v", final)
	}
}

func chapterRefVersion(chapter Chapter) ObjectVersionRef {
	version := ""
	if chapter.CurrentVersionID != nil {
		version = *chapter.CurrentVersionID
	}
	return ObjectVersionRef{Kind: "ChapterVersion", ID: chapter.ID, Version: version}
}

// AC2: every one of the seven states is reachable, and each task records its
// target revision, severity, responsibility domain, causes and closure evidence.
func TestG3200AllSevenTaskStatesAreReachable(t *testing.T) {
	svc := g3_200_impactTaskStore(t, filepath.Join(t.TempDir(), "g3-200-states.db"))
	actor := Actor{TenantID: 3202, UserID: "owner"}
	worker := Actor{TenantID: 3202, UserID: "worker"}
	p, _, change := g3_200_appliedFixture(t, svc, actor)
	g3_200_addCollaborator(t, svc, worker, p.ID)
	task := g3_200_taskFor(t, g3_200_tasks(t, svc, actor, p.ID), change.ID)

	// Every state a task passes through is recorded, because a task only holds
	// one status at a time.
	reached := map[string]bool{task.Status: true}

	// OPEN is the creation state and must already carry the full record.
	if task.Status != "OPEN" || task.TargetRevision != p.CurrentContextRevision || task.Severity != "blocking" || task.ResponsibilityDomain == "" || len(task.Causes) == 0 || task.ClosureEvidence == nil {
		t.Fatalf("OPEN task did not record its full basis: %+v", task)
	}

	actOn := func(key string, subject ImpactTask, action string, assignee Actor, evidence []ObjectVersionRef) ImpactTask {
		t.Helper()
		input := ImpactTaskActionInput{
			ExpectedVersion: subject.Version, TargetRevision: p.CurrentContextRevision, Action: action,
			Reason: "G3-200 " + action, ClosureEvidence: evidence}
		if action == "assign" {
			input.AssigneeID = assignee.UserID
		}
		raw, _, _, err := svc.ActOnImpactTask(context.Background(), assignee, p.ID, subject.ID, key, input)
		if err != nil {
			t.Fatalf("act %s: %v", action, err)
		}
		var got ImpactTask
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		reached[got.Status] = true
		return got
	}

	act := func(key string, action string, assignee Actor, expected int64, evidence []ObjectVersionRef) ImpactTask {
		t.Helper()
		return actOn(key, ImpactTask{ID: task.ID, Version: expected}, action, assignee, evidence)
	}

	// IN_PROGRESS and WAITING_USER are assignee-scoped.
	assigned := act("g3-200-assign", "assign", worker, task.Version, nil)
	if assigned.AssigneeID != worker.UserID || assigned.Status != "OPEN" || assigned.Version != task.Version+1 {
		t.Fatalf("assign did not record responsibility: %+v", assigned)
	}
	started := act("g3-200-start", "start", worker, assigned.Version, nil)
	if started.Status != "IN_PROGRESS" {
		t.Fatalf("start: %+v", started)
	}
	waiting := act("g3-200-wait", "wait", worker, started.Version, nil)
	if waiting.Status != "WAITING_USER" {
		t.Fatalf("wait: %+v", waiting)
	}

	// Produce real closure evidence: a chapter version written at the current
	// spec revision and genuinely confirmed, which is what Complete requires.
	// Completing also legitimately re-targets the task onto that newer version.
	g3_200_replaceTargetChapterBody(t, svc, actor, p, task.Target.ID, "g3-200-evidence", "G3-200 闭环正文")
	fresh := g3_200_current(t, svc, actor, p.ID)
	g3_200_confirmChapter(t, svc, actor, p.ID, fresh, mustListChapters(t, svc, actor, p.ID)[0])
	evidence := g3_200_currentChapterRef(t, svc, actor, p.ID)

	done := act("g3-200-complete", "complete", worker, waiting.Version, []ObjectVersionRef{evidence})
	if done.Status != "DONE" || len(done.ClosureEvidence) != 1 || done.ClosureEvidence[0] != evidence || done.Disposition == "" {
		t.Fatalf("complete did not record closure evidence: %+v", done)
	}

	// A second impact task on the same ChangeSet reaches DISMISSED and CANCELLED.
	dismissedSource := g3_200_secondTaskFixture(t, svc, actor, p, change, "RECOMPUTE")
	dismissed := actOn("g3-200-dismiss", dismissedSource, "dismiss", actor, nil)
	if dismissed.Status != "DISMISSED" {
		t.Fatalf("dismiss: %+v", dismissed)
	}
	cancelledSource := g3_200_secondTaskFixture(t, svc, actor, p, change, "REGENERATE")
	cancelled := actOn("g3-200-cancel", cancelledSource, "cancel", actor, nil)
	if cancelled.Status != "CANCELLED" {
		t.Fatalf("cancel: %+v", cancelled)
	}

	// STALE is reached on this ChangeSet too: replacing the version this task
	// names invalidates the recorded basis.
	staleSource := g3_200_secondTaskFixture(t, svc, actor, p, change, "REVALIDATE_EVIDENCE")
	g3_200_replaceTargetChapterBody(t, svc, actor, p, staleSource.Target.ID, "g3-200-stale-here", "G3-200 触发失效的正文")
	stale := actOn("g3-200-stale-here-act", staleSource, "start", actor, nil)
	if stale.Status != "STALE" || stale.StaleReason == "" {
		t.Fatalf("replaced target did not reach an explainable STALE: %+v", stale)
	}

	// Every declared state was reached over the real service seam.
	for _, status := range ImpactTaskStatuses {
		if !reached[status] {
			t.Fatalf("task status %q is unreachable; reached %v", status, reached)
		}
	}
	_ = fresh
}

// g3_200_secondTaskFixture raises one extra task for the same ChangeSet with
// the given action, so dismiss/cancel can be exercised on distinct identities.
func g3_200_secondTaskFixture(t *testing.T, svc *Service, actor Actor, p Project, change ChangeSet, action string) ImpactTask {
	t.Helper()
	task := ImpactTask{ID: ImpactTaskID(p.ID, change.ID, g3_200_currentChapterRef(t, svc, actor, p.ID), p.CurrentContextRevision, action),
		ProjectID: p.ID, ChangeSetID: change.ID, Target: g3_200_currentChapterRef(t, svc, actor, p.ID),
		TargetRevision: p.CurrentContextRevision, Action: action, Severity: "advisory",
		ResponsibilityDomain: "B", Status: "OPEN", Causes: []ImpactCause{}, Version: 1,
		ClosureEvidence: []ObjectVersionRef{}, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	store := gormTransaction{svc.testDB()}
	if err := store.InsertImpactTask(task); err != nil {
		t.Fatalf("seed %s task: %v", action, err)
	}
	return task
}

func g3_200_currentChapterRef(t *testing.T, svc *Service, actor Actor, projectID string) ObjectVersionRef {
	t.Helper()
	chapters := mustListChapters(t, svc, actor, projectID)
	if len(chapters) == 0 || chapters[0].CurrentVersionID == nil {
		t.Fatalf("no current chapter version: %+v", chapters)
	}
	return ObjectVersionRef{Kind: "ChapterVersion", ID: chapters[0].ID, Version: *chapters[0].CurrentVersionID}
}

// g3_200_confirmChapter writes a real confirmation row for the chapter's
// current version at the project's current spec revision, so the target is
// genuinely confirmed and completion evidence is accepted by the service.
func g3_200_confirmChapter(t *testing.T, svc *Service, actor Actor, projectID string, p Project, chapter Chapter) {
	t.Helper()
	if chapter.CurrentVersionID == nil {
		t.Fatal("chapter has no current version")
	}
	id := "g3-200-confirmation"
	details, err := json.Marshal(chapterConfirmationDetails{ID: id, ChapterVersionID: *chapter.CurrentVersionID,
		SpecRevision: p.SpecRevision, TemplateVersion: p.TemplateVersion, Valid: true})
	if err != nil {
		t.Fatal(err)
	}
	db := svc.testDB()
	if err := db.Model(&chapterVersionRow{}).Where("id = ?", *chapter.CurrentVersionID).Update("confirmation_valid", true).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&chapterConfirmationRow{ID: id, ChapterID: chapter.ID, ChapterVersionID: *chapter.CurrentVersionID, Valid: true, DetailsJSON: string(details), CreatedAt: time.Now().UTC()}).Error; err != nil {
		t.Fatal(err)
	}
}

// AC3: assignment, disposition and completion obey capability and scope, and an
// unauthorized user cannot change task state at all.
func TestG3200TaskStateChangesRequireCapabilityAndScope(t *testing.T) {
	svc := g3_200_impactTaskStore(t, filepath.Join(t.TempDir(), "g3-200-permission.db"))
	actor := Actor{TenantID: 3203, UserID: "owner"}
	outsider := Actor{TenantID: 3203, UserID: "outsider"}
	p, _, change := g3_200_appliedFixture(t, svc, actor)
	// The outsider is a tenant member but holds no project membership, so the
	// project-scoped capability checks must refuse them.
	seedTenantMember(t, svc, outsider)
	task := g3_200_taskFor(t, g3_200_tasks(t, svc, actor, p.ID), change.ID)

	// A tenant member without manage capability may not assign or dismiss.
	for _, action := range []string{"assign", "dismiss"} {
		input := ImpactTaskActionInput{ExpectedVersion: task.Version, TargetRevision: p.CurrentContextRevision, Action: action, Reason: "越权尝试", AssigneeID: outsider.UserID}
		if _, _, _, err := svc.ActOnImpactTask(context.Background(), outsider, p.ID, task.ID, "g3-200-denied-"+action, input); err == nil {
			t.Fatalf("unprivileged actor performed %s", action)
		}
	}
	if got := g3_200_taskFor(t, g3_200_tasks(t, svc, actor, p.ID), change.ID); got.Status != "OPEN" || got.Version != task.Version {
		t.Fatalf("denied action changed task state: %+v", got)
	}

	// A non-assignee may not start a task that is assigned to somebody else.
	assignee := Actor{TenantID: 3203, UserID: "assignee"}
	g3_200_addCollaborator(t, svc, assignee, p.ID)
	raw, _, _, err := svc.ActOnImpactTask(context.Background(), actor, p.ID, task.ID, "g3-200-owner-assign", ImpactTaskActionInput{
		ExpectedVersion: task.Version, TargetRevision: p.CurrentContextRevision, Action: "assign", Reason: "指派", AssigneeID: assignee.UserID})
	if err != nil {
		t.Fatalf("owner assign: %v", err)
	}
	var assigned ImpactTask
	if err := json.Unmarshal(raw, &assigned); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := svc.ActOnImpactTask(context.Background(), actor, p.ID, task.ID, "g3-200-owner-start", ImpactTaskActionInput{
		ExpectedVersion: assigned.Version, TargetRevision: p.CurrentContextRevision, Action: "start", Reason: "非责任人启动"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-assignee start error = %v, want ErrNotFound", err)
	}

	// A machine principal cannot drive the human disposition surface at all.
	machine := types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{KeyID: 99, FullAccess: true})
	if _, _, _, err := svc.ActOnImpactTask(machine, actor, p.ID, task.ID, "g3-200-machine", ImpactTaskActionInput{
		ExpectedVersion: assigned.Version, TargetRevision: p.CurrentContextRevision, Action: "start", Reason: "机器代劳"}); !errors.Is(err, ErrHumanDecisionRequired) {
		t.Fatalf("machine principal bypassed the human decision gate: %v", err)
	}
}

// AC4 + AC2: replacing the target version, revoking a source, or revoking the
// assignee turns the task STALE with an explicit, explainable reason; STALE is
// terminal and is never mistaken for closure.
func TestG3200InvalidatedBasisBecomesStaleWithReason(t *testing.T) {
	t.Run("target version replaced", func(t *testing.T) {
		svc := g3_200_impactTaskStore(t, filepath.Join(t.TempDir(), "g3-200-stale-target.db"))
		actor := Actor{TenantID: 3204, UserID: "owner"}
		p, _, change := g3_200_appliedFixture(t, svc, actor)
		task := g3_200_taskFor(t, g3_200_tasks(t, svc, actor, p.ID), change.ID)

		// Replace the target version so the exact version the task names is no
		// longer current.
		g3_200_replaceTargetChapterBody(t, svc, actor, p, task.Target.ID, "g3-200-replace", "G3-200 被替换的正文")

		raw, _, _, err := svc.ActOnImpactTask(context.Background(), actor, p.ID, task.ID, "g3-200-stale", ImpactTaskActionInput{
			ExpectedVersion: task.Version, TargetRevision: p.CurrentContextRevision, Action: "start", Reason: "尝试推进"})
		if err != nil {
			t.Fatalf("acting on a replaced target must succeed as STALE, got %v", err)
		}
		var stale ImpactTask
		if err := json.Unmarshal(raw, &stale); err != nil {
			t.Fatal(err)
		}
		if stale.Status != "STALE" || stale.StaleReason != ImpactTaskStaleTargetReplaced || stale.StaleAt.IsZero() || stale.Version != task.Version+1 {
			t.Fatalf("replaced target did not produce an explainable STALE: %+v", stale)
		}
		if slicesContains(ImpactTaskClosedStatuses, stale.Status) {
			t.Fatalf("STALE must never count as closure: %+v", stale)
		}

		// Idempotent: acting again on the STALE task changes nothing and
		// produces no second transition.
		if _, _, _, err := svc.ActOnImpactTask(context.Background(), actor, p.ID, task.ID, "g3-200-stale-again", ImpactTaskActionInput{
			ExpectedVersion: stale.Version, TargetRevision: p.CurrentContextRevision, Action: "start", Reason: "再次尝试"}); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("second act on STALE task error = %v, want ErrInvalidState", err)
		}
		after := g3_200_taskFor(t, g3_200_tasks(t, svc, actor, p.ID), change.ID)
		if after.Status != "STALE" || after.Version != stale.Version {
			t.Fatalf("repeated STALE trigger mutated the task: %+v -> %+v", stale, after)
		}
	})

	t.Run("assignee revoked", func(t *testing.T) {
		svc := g3_200_impactTaskStore(t, filepath.Join(t.TempDir(), "g3-200-stale-assignee.db"))
		actor := Actor{TenantID: 3205, UserID: "owner"}
		worker := Actor{TenantID: 3205, UserID: "worker"}
		p, _, change := g3_200_appliedFixture(t, svc, actor)
		g3_200_addCollaborator(t, svc, worker, p.ID)
		task := g3_200_taskFor(t, g3_200_tasks(t, svc, actor, p.ID), change.ID)
		raw, _, _, err := svc.ActOnImpactTask(context.Background(), actor, p.ID, task.ID, "g3-200-assign-worker", ImpactTaskActionInput{
			ExpectedVersion: task.Version, TargetRevision: p.CurrentContextRevision, Action: "assign", Reason: "指派", AssigneeID: worker.UserID})
		if err != nil {
			t.Fatalf("assign: %v", err)
		}
		var assigned ImpactTask
		if err := json.Unmarshal(raw, &assigned); err != nil {
			t.Fatal(err)
		}

		// Revoke the assignee's tenant membership, i.e. they left the scope.
		if err := svc.testDB().Exec("UPDATE tenant_members SET status = 'revoked' WHERE tenant_id = ? AND user_id = ?", worker.TenantID, worker.UserID).Error; err != nil {
			t.Fatalf("revoke assignee: %v", err)
		}
		raw, _, _, err = svc.ActOnImpactTask(context.Background(), actor, p.ID, task.ID, "g3-200-stale-assignee", ImpactTaskActionInput{
			ExpectedVersion: assigned.Version, TargetRevision: p.CurrentContextRevision, Action: "start", Reason: "责任人已撤权"})
		if err != nil {
			t.Fatalf("acting after assignee revocation: %v", err)
		}
		var stale ImpactTask
		if err := json.Unmarshal(raw, &stale); err != nil {
			t.Fatal(err)
		}
		if stale.Status != "STALE" || stale.StaleReason != ImpactTaskStaleAssigneeRevoked {
			t.Fatalf("revoked assignee did not produce STALE/assignee_revoked: %+v", stale)
		}
	})
}

func slicesContains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// AC5: identity, history and responsibility survive a service restart, and the
// derived task ID still addresses the same task after reopening the database.
func TestG3200TaskHistoryAndResponsibilitySurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g3-200-restart.db")
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("restart fixture must start from a fresh database")
	}
	svc := g3_200_impactTaskStore(t, path)
	actor := Actor{TenantID: 3206, UserID: "owner"}
	worker := Actor{TenantID: 3206, UserID: "worker"}
	p, _, change := g3_200_appliedFixture(t, svc, actor)
	g3_200_addCollaborator(t, svc, worker, p.ID)
	task := g3_200_taskFor(t, g3_200_tasks(t, svc, actor, p.ID), change.ID)
	raw, _, _, err := svc.ActOnImpactTask(context.Background(), actor, p.ID, task.ID, "g3-200-restart-assign", ImpactTaskActionInput{
		ExpectedVersion: task.Version, TargetRevision: p.CurrentContextRevision, Action: "assign", Reason: "指派责任人", AssigneeID: worker.UserID})
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	var assigned ImpactTask
	if err := json.Unmarshal(raw, &assigned); err != nil {
		t.Fatal(err)
	}

	// A real restart: brand-new Service, brand-new connection pool, same file.
	restarted := g3_200_impactTaskStore(t, path)
	readBack := g3_200_taskFor(t, g3_200_tasks(t, restarted, actor, p.ID), change.ID)
	if readBack.ID != assigned.ID || readBack.AssigneeID != worker.UserID || readBack.Version != assigned.Version || readBack.Status != assigned.Status {
		t.Fatalf("task history or responsibility lost across restart: %+v -> %+v", assigned, readBack)
	}
	if len(readBack.Causes) != len(task.Causes) || readBack.ChangeSetID != change.ID || readBack.Target != task.Target || readBack.TargetRevision != task.TargetRevision {
		t.Fatalf("task basis lost across restart: %+v", readBack)
	}

	// The identity-derived ID still addresses the same row after restart, so a
	// redelivered event cannot fork the task.
	recomputed := ImpactTaskID(p.ID, change.ID, readBack.Target, readBack.TargetRevision, readBack.Action)
	if recomputed != readBack.ID {
		t.Fatalf("derived ID is not stable across restart: %s vs %s", recomputed, readBack.ID)
	}
	if err := restarted.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		return CommitChangeImpactTasks(tx, p, change)
	}); err != nil {
		t.Fatalf("post-restart redelivery: %v", err)
	}
	final := g3_200_tasks(t, restarted, actor, p.ID)
	if len(final) != 1 || final[0].ID != readBack.ID {
		t.Fatalf("post-restart redelivery forked the task: %+v", final)
	}
}

// AC6: the service response and the client contract expose the same task
// semantics, including the STALE state and its reason.
func TestG3200ServiceAndClientContractShareTaskSemantics(t *testing.T) {
	svc := g3_200_impactTaskStore(t, filepath.Join(t.TempDir(), "g3-200-contract.db"))
	actor := Actor{TenantID: 3207, UserID: "owner"}
	p, _, change := g3_200_appliedFixture(t, svc, actor)
	task := g3_200_taskFor(t, g3_200_tasks(t, svc, actor, p.ID), change.ID)

	raw, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"id", "project_id", "change_set_id", "target", "target_revision", "action", "severity", "responsibility_domain", "status", "causes", "version", "closure_evidence"} {
		if _, ok := wire[field]; !ok {
			t.Fatalf("task contract missing %q: %s", field, raw)
		}
	}
	// STALE and its reason must be part of the same wire contract, otherwise a
	// consumer cannot explain an invalidated task.
	g3_200_replaceTargetChapterBody(t, svc, actor, p, task.Target.ID, "g3-200-contract-replace", "G3-200 合同替换")
	raw, _, _, err = svc.ActOnImpactTask(context.Background(), actor, p.ID, task.ID, "g3-200-contract-stale", ImpactTaskActionInput{
		ExpectedVersion: task.Version, TargetRevision: p.CurrentContextRevision, Action: "start", Reason: "推进"})
	if err != nil {
		t.Fatalf("stale transition: %v", err)
	}
	var stale ImpactTask
	if err := json.Unmarshal(raw, &stale); err != nil {
		t.Fatal(err)
	}
	staleWire, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	var staleFields map[string]any
	if err := json.Unmarshal(staleWire, &staleFields); err != nil {
		t.Fatal(err)
	}
	if staleFields["status"] != "STALE" || staleFields["stale_reason"] != ImpactTaskStaleTargetReplaced {
		t.Fatalf("STALE semantics are not on the wire: %s", staleWire)
	}
	if !slicesContains(ImpactTaskStatuses, staleFields["status"].(string)) {
		t.Fatalf("STALE is not a declared task status: %v", ImpactTaskStatuses)
	}
}

// AC2 gate semantics: STALE is non-blocking but is not closure. Stabilization
// must not report it as an open blocker, must not count it as closed, and must
// report it explicitly instead of dropping it. The read-only release assertion
// must refuse it as well.
func TestG3200StaleIsNonBlockingButNeverClosure(t *testing.T) {
	svc := g3_200_impactTaskStore(t, filepath.Join(t.TempDir(), "g3-200-gate.db"))
	actor := Actor{TenantID: 3208, UserID: "owner"}
	p, _, change := g3_200_appliedFixture(t, svc, actor)
	task := g3_200_taskFor(t, g3_200_tasks(t, svc, actor, p.ID), change.ID)

	g3_200_replaceTargetChapterBody(t, svc, actor, p, task.Target.ID, "g3-200-gate-replace", "G3-200 门禁替换")
	raw, _, _, err := svc.ActOnImpactTask(context.Background(), actor, p.ID, task.ID, "g3-200-gate-stale", ImpactTaskActionInput{
		ExpectedVersion: task.Version, TargetRevision: p.CurrentContextRevision, Action: "start", Reason: "推进"})
	if err != nil {
		t.Fatalf("stale transition: %v", err)
	}
	var stale ImpactTask
	if err := json.Unmarshal(raw, &stale); err != nil {
		t.Fatal(err)
	}
	if stale.Status != "STALE" {
		t.Fatalf("expected a STALE task, got %+v", stale)
	}

	raw, _, _, err = svc.StabilizeRevision(context.Background(), actor, p.ID, change.ID, "g3-200-gate-stabilize")
	if err != nil {
		t.Fatalf("stabilize: %v", err)
	}
	var result StabilizationResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	// Non-blocking: the invalidated task must not appear as an open blocker,
	// because its basis is gone and no actor could ever discharge it.
	for _, blocker := range result.Blockers {
		if blocker.TargetID == stale.ID {
			t.Fatalf("STALE task blocked stabilization: %+v", result.Blockers)
		}
	}
	// Non-silent: it must still be reported.
	if !slicesContains(result.StaleTaskIDs, stale.ID) {
		t.Fatalf("STALE task was silently dropped from stabilization: %+v", result)
	}

	// Not closure: the read-only release assertion must refuse the revision and
	// must name the STALE task, rather than quietly accepting it as closed.
	// Mark the ChangeSet stabilized first, so the task gate is what fails rather
	// than the unrelated lifecycle precondition in front of it.
	g3_200_markChangeStabilized(t, svc, actor, p.ID, change.ID)
	if err := svc.AssertRevisionStabilized(context.Background(), actor, p.ID); err == nil {
		t.Fatal("a STALE task satisfied the release gate")
	} else if !strings.Contains(err.Error(), "STALE") {
		t.Fatalf("release gate must name the STALE task explicitly, got %v", err)
	}
}
