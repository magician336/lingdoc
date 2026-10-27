package workspacecore

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

// 稳定化 acceptance: StabilizeRevision re-reads current domain truth, returns
// complete structured blockers instead of a bare error, never writes partial
// formal state, and keeps its result identical across the service, HTTP/contract
// and client type layers.
//
// The fixtures here reuse the #200 applied-change seam (appliedChangeFixture)
// so tasks and assessments are produced by the production commit path rather
// than hand-inserted.

// cleanRuleChecker is a deterministic ImpactRuleChecker that reports no rule
// blockers. Stabilization treats an absent checker as a blocker in its own
// right, so a test that wants to observe any other blocker must install one;
// otherwise every result is BLOCKED for an unrelated reason.
type cleanRuleChecker struct{}

func (cleanRuleChecker) Check(Transaction, Project) ([]ImpactBlocker, error) {
	return []ImpactBlocker{}, nil
}

// stabilizationFixture drives a real ChangeSet to applied and returns the current
// project, both seeded chapters, and the applied ChangeSet.
func stabilizationFixture(t *testing.T, svc *Service, actor Actor) (Project, []Chapter, ChangeSet) {
	t.Helper()
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	p, _, change := appliedChangeFixture(t, svc, actor)
	return p, mustListChapters(t, svc, actor, p.ID), change
}

// readPersistedAssessment reads the persisted assessment through the real store
// seam rather than the authorized read path, so the test observes exactly what
// stabilization would read.
func readPersistedAssessment(t *testing.T, svc *Service, projectID, changeID string) ImpactAssessment {
	t.Helper()
	var assessment ImpactAssessment
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var err error
		assessment, err = tx.(impactTransaction).ImpactAssessment(projectID, changeID)
		return err
	}))
	return assessment
}

// runStabilization calls the real stabilization seam and decodes the server's
// own result, so assertions never rely on a locally computed guess.
func runStabilization(t *testing.T, svc *Service, actor Actor, projectID, changeID, key string) (StabilizationResult, error) {
	t.Helper()
	raw, _, _, err := svc.StabilizeRevision(context.Background(), actor, projectID, changeID, key)
	if err != nil {
		return StabilizationResult{}, err
	}
	var result StabilizationResult
	require.NoError(t, json.Unmarshal(raw, &result))
	return result, nil
}

// blockerCodesOf indexes the blockers by code for readable assertions.
func blockerCodesOf(result StabilizationResult) map[string]int {
	codes := map[string]int{}
	for _, blocker := range result.Blockers {
		codes[blocker.Code]++
	}
	return codes
}

// closeBlockingTasks performs the genuine repair loop for every open non-BLOCK_RELEASE
// task at the current revision: re-save and re-confirm the target chapter at the
// current spec revision, then close the task against that current version. It
// returns once a full round closes nothing.
func closeBlockingTasks(t *testing.T, svc *Service, actor Actor, projectID string) {
	t.Helper()
	for round := 0; round < 6; round++ {
		p := reloadProject(t, svc, actor, projectID)
		pending := false
		for _, task := range tasksInProject(t, svc, actor, projectID) {
			if task.TargetRevision == p.CurrentContextRevision && task.Status != "DONE" && task.Status != "STALE" && task.Action != "BLOCK_RELEASE" {
				pending = true
			}
		}
		if !pending {
			return
		}
		for _, task := range tasksInProject(t, svc, actor, projectID) {
			// STALE is terminal and non-actionable (#200): the basis the task
			// was raised against is gone, so there is nothing to assign or close.
			if task.TargetRevision != p.CurrentContextRevision || task.Status != "OPEN" && task.Status != "IN_PROGRESS" && task.Status != "WAITING_USER" || task.Action == "BLOCK_RELEASE" {
				continue
			}
			evidence := confirmImpactTarget(t, svc, actor, projectID, task.Target.ID, round)
			// Match by the task's own ID: a change set raises one task per
			// impacted object, and matching by change ID alone made every
			// iteration after the first resolve to the first task and skip
			// the rest (#727's multi-object upgrade fixture).
			current := task
			for _, candidate := range tasksInProject(t, svc, actor, projectID) {
				if candidate.ID == task.ID {
					current = candidate
					break
				}
			}
			if current.AssigneeID != actor.UserID {
				raw, _, _, err := svc.ActOnImpactTask(context.Background(), actor, projectID, task.ID,
					"stabilize-assign-"+task.ID+"-"+strconv.Itoa(round), ImpactTaskActionInput{
						ExpectedVersion: current.Version, TargetRevision: current.TargetRevision, Action: "assign",
						AssigneeID: actor.UserID, Reason: "稳定化 指派给 Owner"})
				require.NoError(t, err, "assign %s", task.ID)
				current = decodeImpactTask(t, raw)
			}
			_, _, _, err := svc.ActOnImpactTask(context.Background(), actor, projectID, task.ID,
				"stabilize-close-"+task.ID+"-"+strconv.Itoa(round), ImpactTaskActionInput{
					ExpectedVersion: current.Version, TargetRevision: current.TargetRevision, Action: "complete",
					HumanDecision: "reviewed", Reason: "稳定化 复核当前版本",
					ClosureEvidence: []ObjectVersionRef{evidence}})
			require.NoError(t, err, "close %s (action=%s)", task.ID, task.Action)
		}
	}
}

// confirmImpactTarget re-saves and re-confirms the chapter's current version at
// the current spec revision, returning the ref a completion may name as
// closure evidence.
func confirmImpactTarget(t *testing.T, svc *Service, actor Actor, projectID, chapterID string, round int) ObjectVersionRef {
	t.Helper()
	for _, chapter := range mustListChapters(t, svc, actor, projectID) {
		if chapter.ID != chapterID {
			continue
		}
		fresh := reloadProject(t, svc, actor, projectID)
		if _, _, _, err := svc.SaveChapter(context.Background(), actor, projectID, chapter.ID,
			"stabilize-resave-"+chapter.ID+"-"+strconv.Itoa(round), SaveChapterInput{
				ExpectedChapterVersionID: chapter.CurrentVersionID, ExpectedSpecRevision: fresh.SpecRevision,
				BodyMarkdown: "稳定化 合成复核后的当前章节正文。", SourceIDs: []string{}}); err != nil {
			t.Fatalf("re-save chapter %s: %v", chapter.ID, err)
		}
		reloaded := mustListChapters(t, svc, actor, projectID)
		for _, re := range reloaded {
			if re.ID != chapterID {
				continue
			}
			require.NotNil(t, re.CurrentVersionID, "chapter %s has no current version after re-save", chapterID)
			confirmChapterAtSpecRevision(t, svc, actor, projectID, reloadProject(t, svc, actor, projectID), re)
			return ObjectVersionRef{Kind: "ChapterVersion", ID: chapterID, Version: *re.CurrentVersionID}
		}
	}
	t.Fatalf("chapter %s not found", chapterID)
	return ObjectVersionRef{}
}

func decodeImpactTask(t *testing.T, raw json.RawMessage) ImpactTask {
	t.Helper()
	var task ImpactTask
	require.NoError(t, json.Unmarshal(raw, &task))
	return task
}

// AC1 (the gap this ticket closes): stabilization must re-read the CURRENT
// confirmation of every impacted object the persisted assessment recorded.
//
// Today only DONE tasks' closure evidence is re-read. A task that went STALE is
// deliberately neither a blocker nor closure (#200), so its impacted object ends
// up with no obligation attached at all: nothing blocks on it and nothing checks
// it. An impacted chapter can therefore be left unconfirmed while stabilization
// still reports STABILIZED. The re-read is scoped to the impacted objects the
// assessment recorded, so it cannot fire on unrelated project state.
func TestStabilizationRechecksImpactedObjectsWithoutCoveringTask(t *testing.T) {
	svc := impactTaskStore(t, filepath.Join(t.TempDir(), "stabilize-gap.db"))
	actor := Actor{TenantID: 3207, UserID: "owner"}
	p, chapters, change := stabilizationFixture(t, svc, actor)
	target := chapters[0]
	_ = chapters[1]

	// The chapter is a genuinely recorded impacted object of this assessment.
	assessment := readPersistedAssessment(t, svc, p.ID, change.ID)
	recorded := false
	for _, object := range assessment.Objects {
		if object.Ref.Kind == "ChapterVersion" && object.Ref.ID == target.ID {
			recorded = true
		}
	}
	require.True(t, recorded, "chapter must be a recorded impacted object: %+v", assessment.Objects)

	// Replace the chapter body so the task's recorded target version stops being
	// current, then act on it: it transitions to STALE.
	task := taskFor(t, tasksInProject(t, svc, actor, p.ID), change.ID)
	replaceTargetChapterBody(t, svc, actor, p, target.ID, "stabilize-gap-replace", "稳定化 替换正文")
	raw, _, _, err := svc.ActOnImpactTask(context.Background(), actor, p.ID, task.ID, "stabilize-gap-stale", ImpactTaskActionInput{
		ExpectedVersion: task.Version, TargetRevision: p.CurrentContextRevision, Action: "start", Reason: "推进"})
	require.NoError(t, err)
	require.Equal(t, "STALE", decodeImpactTask(t, raw).Status)

	// Close every remaining real task, so the STALE task's object is the only
	// outstanding question.
	closeBlockingTasks(t, svc, actor, p.ID)
	revokeChapterConfirmation(t, svc, actor, p.ID, target.ID)

	result, err := runStabilization(t, svc, actor, p.ID, change.ID, "stabilize-gap-stabilize")
	require.NoError(t, err)
	require.Equal(t, "BLOCKED", result.Status,
		"an impacted object left unconfirmed must still block: %+v", result.Blockers)
	require.NotEmpty(t, result.Blockers)
	// The STALE task is still reported as invalidated, not silently dropped.
	require.Contains(t, result.StaleTaskIDs, task.ID)

	// Re-confirming the chapter is the real repair; only then may it stabilize.
	confirmImpactTarget(t, svc, actor, p.ID, target.ID, 99)
	result, err = runStabilization(t, svc, actor, p.ID, change.ID, "stabilize-gap-retry")
	require.NoError(t, err)
	require.Equal(t, "STABILIZED", result.Status, "after re-confirming the object: %+v", result.Blockers)
}

// AC2: a blocking condition must be a structured 200/BLOCKED answer carrying
// every reason, not an error, and it must not advance the ChangeSet. The
// boundary is explicit: "blocked" describes domain truth the Owner can repair,
// while an error describes a call the service refuses to interpret at all
// (wrong revision, wrong state machine position, not the Owner).
func TestStabilizationBlockedReturnsCompleteReasons(t *testing.T) {
	svc := impactTaskStore(t, filepath.Join(t.TempDir(), "stabilize-blocked.db"))
	actor := Actor{TenantID: 3208, UserID: "owner"}
	p, _, change := stabilizationFixture(t, svc, actor)

	before := changeSetStatus(t, svc, actor, p.ID, change.ID)
	revisionsBefore := reloadProject(t, svc, actor, p.ID).CurrentContextRevision

	result, err := runStabilization(t, svc, actor, p.ID, change.ID, "stabilize-blocked-stabilize")
	require.NoError(t, err, "a repairable blocking condition must not be an error")

	// 200 + BLOCKED, with a machine-readable code AND a human-readable reason.
	require.Equal(t, "BLOCKED", result.Status)
	require.NotEmpty(t, result.Blockers)
	for _, blocker := range result.Blockers {
		require.NotEmpty(t, blocker.Code, "every blocker must carry a code: %+v", blocker)
		require.NotEmpty(t, blocker.Reason, "every blocker must be explainable: %+v", blocker)
	}
	// The open blocking task is named, so the Owner knows what to repair.
	task := taskFor(t, tasksInProject(t, svc, actor, p.ID), change.ID)
	require.Contains(t, blockerCodesOf(result), "impact_task_open")
	found := false
	for _, blocker := range result.Blockers {
		if blocker.Code == "impact_task_open" && blocker.TargetID == task.ID {
			found = true
		}
	}
	require.True(t, found, "the open task must be named in the blockers: %+v", result.Blockers)
	require.NotZero(t, result.CheckedAt.Unix(), "a stabilization verdict must record when it was taken")

	// No new revision and no STABILIZED marking on a blocked verdict.
	require.Equal(t, before, changeSetStatus(t, svc, actor, p.ID, change.ID),
		"a blocked stabilization must not change the ChangeSet status")
	require.Equal(t, revisionsBefore, reloadProject(t, svc, actor, p.ID).CurrentContextRevision,
		"a blocked stabilization must not create a revision")

	// Repair, then the same request succeeds and only then advances state.
	closeBlockingTasks(t, svc, actor, p.ID)
	repaired, err := runStabilization(t, svc, actor, p.ID, change.ID, "stabilize-blocked-retry")
	require.NoError(t, err)
	require.Equal(t, "STABILIZED", repaired.Status, "unexpected blockers: %+v", repaired.Blockers)
	require.Equal(t, "stabilized", changeSetStatus(t, svc, actor, p.ID, change.ID))
}

// AC2/AC6: a refused call (not a repairable blocker) must stay an error and
// must not mutate anything. This is the other half of the boundary.
func TestStabilizationRefusalWritesNothing(t *testing.T) {
	svc := impactTaskStore(t, filepath.Join(t.TempDir(), "stabilize-refused.db"))
	actor := Actor{TenantID: 3209, UserID: "owner"}
	p, _, change := stabilizationFixture(t, svc, actor)
	closeBlockingTasks(t, svc, actor, p.ID)

	before := changeSetStatus(t, svc, actor, p.ID, change.ID)

	// A change set that was never applied is not a repairable condition: the
	// caller addressed a revision that does not exist in the applied lifecycle.
	_, err := runStabilization(t, svc, actor, p.ID, "stabilize-no-such-change", "stabilize-refused-unknown")
	require.Error(t, err, "an unknown ChangeSet must be refused, not reported as BLOCKED")
	require.Equal(t, before, changeSetStatus(t, svc, actor, p.ID, change.ID))

	// A non-Owner cannot stabilize at all.
	outsider := Actor{TenantID: 3209, UserID: "outsider"}
	_, err = runStabilization(t, svc, outsider, p.ID, change.ID, "stabilize-refused-outsider")
	require.Error(t, err, "a non-Owner must be refused")
	require.Equal(t, before, changeSetStatus(t, svc, actor, p.ID, change.ID))
}

// changeSetStatus reads the persisted ChangeSet status through the real store.
func changeSetStatus(t *testing.T, svc *Service, actor Actor, projectID, changeID string) string {
	t.Helper()
	var status string
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		change, err := tx.(changeSetTransaction).ChangeSet(projectID, changeID)
		if err != nil {
			return err
		}
		status = change.Status
		return nil
	}))
	return status
}

// AC6: an audit write failure must roll the whole stabilization back. The
// ChangeSet must not be marked STABILIZED, no stabilization audit entry may be
// left behind, and no operation record may be persisted, so a retry after the
// fault clears is still able to perform the transition for real.
func TestStabilizationAuditFailureRollsBack(t *testing.T) {
	svc := impactTaskStore(t, filepath.Join(t.TempDir(), "stabilize-audit.db"))
	actor := Actor{TenantID: 3210, UserID: "owner"}
	p, _, change := stabilizationFixture(t, svc, actor)
	closeBlockingTasks(t, svc, actor, p.ID)

	before := changeSetStatus(t, svc, actor, p.ID, change.ID)
	require.Equal(t, "applied", before, "fixture precondition: the change is applied but not stabilized")
	auditBefore := countAuditEvents(t, svc, "revision.stabilize")
	recordsBefore := countRecordRows(t, svc, &versionedRecordRow{})

	require.NoError(t, svc.testDB().Exec(
		"CREATE TRIGGER stabilization_audit_failure BEFORE INSERT ON lingdoc_project_audits "+
			"WHEN NEW.action = 'revision.stabilize' BEGIN SELECT RAISE(ABORT, 'stabilize audit unavailable'); END").Error)
	_, err := runStabilization(t, svc, actor, p.ID, change.ID, "stabilize-audit-stabilize")
	require.Error(t, err, "an audit write failure must fail the stabilization")
	require.NoError(t, svc.testDB().Exec("DROP TRIGGER stabilization_audit_failure").Error)

	// No partial formal write: the status must be exactly as it was, and the
	// failed attempt must not have persisted its operation receipt.
	require.Equal(t, before, changeSetStatus(t, svc, actor, p.ID, change.ID),
		"a failed stabilization must not mark the ChangeSet STABILIZED")
	require.Equal(t, auditBefore, countAuditEvents(t, svc, "revision.stabilize"),
		"a failed stabilization must not leave a stabilization audit entry")
	require.Equal(t, recordsBefore, countRecordRows(t, svc, &versionedRecordRow{}),
		"a failed stabilization must not persist a partial record")

	// Recovery: once the fault clears the same intent succeeds, which proves the
	// failure left nothing half-written that would block a retry.
	result, err := runStabilization(t, svc, actor, p.ID, change.ID, "stabilize-audit-retry")
	require.NoError(t, err, "a retry after the audit fault cleared must succeed")
	require.Equal(t, "STABILIZED", result.Status, "unexpected blockers: %+v", result.Blockers)
	require.Equal(t, "stabilized", changeSetStatus(t, svc, actor, p.ID, change.ID))
}

// countAuditEvents counts persisted audit rows for one action.
func countAuditEvents(t *testing.T, svc *Service, action string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, svc.testDB().Table("lingdoc_project_audits").Where("action = ?", action).Count(&count).Error)
	return count
}

// countRecordRows counts rows of a persisted model, used to prove that a failed
// stabilization wrote nothing at all.
func countRecordRows(t *testing.T, svc *Service, model any) int64 {
	t.Helper()
	var count int64
	require.NoError(t, svc.testDB().Model(model).Count(&count).Error)
	return count
}

// AC4/AC6: stabilizing advances only the stabilization state. It must not
// implicitly authorize READY or an export, and repeating the request is
// idempotent rather than a second transition.
func TestStabilizationAdvancesOnlyItsOwnState(t *testing.T) {
	svc := impactTaskStore(t, filepath.Join(t.TempDir(), "stabilize-idem.db"))
	actor := Actor{TenantID: 3211, UserID: "owner"}
	p, _, change := stabilizationFixture(t, svc, actor)
	closeBlockingTasks(t, svc, actor, p.ID)

	revisionsBefore := reloadProject(t, svc, actor, p.ID).CurrentContextRevision
	first, err := runStabilization(t, svc, actor, p.ID, change.ID, "stabilize-idem-first")
	require.NoError(t, err)
	require.Equal(t, "STABILIZED", first.Status, "unexpected blockers: %+v", first.Blockers)

	// Only the stabilization state advanced: no new revision, and the project
	// basis the delivery gate reads is untouched by stabilization itself.
	require.Equal(t, revisionsBefore, reloadProject(t, svc, actor, p.ID).CurrentContextRevision,
		"stabilization must not create a revision")
	require.Equal(t, "stabilized", changeSetStatus(t, svc, actor, p.ID, change.ID))

	// Repeating the same intent is a replay of an identical decision, not a
	// second transition: same verdict, no new audit entry.
	auditsAfterFirst := countAuditEvents(t, svc, "revision.stabilize")
	second, err := runStabilization(t, svc, actor, p.ID, change.ID, "stabilize-idem-first")
	require.NoError(t, err)
	require.Equal(t, first.Status, second.Status)
	require.Equal(t, first.Blockers, second.Blockers, "a repeated request must reach the same verdict")
	require.Equal(t, auditsAfterFirst, countAuditEvents(t, svc, "revision.stabilize"),
		"a repeated identical stabilization must not audit a second transition")
}

// AC5: the service, the contract and the client type expose the same
// stabilization status and the same complete blocking reasons. The wire payload
// is what the client consumes, so a field the server sends but the client type
// omits is a real contract defect.
func TestStabilizationServiceAndClientAgree(t *testing.T) {
	svc := impactTaskStore(t, filepath.Join(t.TempDir(), "stabilize-contract.db"))
	actor := Actor{TenantID: 3212, UserID: "owner"}
	p, _, change := stabilizationFixture(t, svc, actor)

	raw, _, _, err := svc.StabilizeRevision(context.Background(), actor, p.ID, change.ID, "stabilize-contract-stabilize")
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(raw, &wire))

	// Every field a client needs to render a verdict must be on the wire. In
	// particular checked_at is how a caller knows when the verdict was taken, so
	// omitting it from the client type makes the server and client disagree.
	for _, field := range []string{"change_set_id", "context_revision", "status", "blockers", "stale_task_ids", "checked_at"} {
		require.Contains(t, wire, field, "stabilization contract missing %q: %s", field, raw)
	}
	require.Equal(t, "BLOCKED", wire["status"])
	blockers, ok := wire["blockers"].([]any)
	require.True(t, ok, "blockers must be an array: %s", raw)
	require.NotEmpty(t, blockers)
	for _, entry := range blockers {
		blocker := entry.(map[string]any)
		require.Contains(t, blocker, "code")
		require.Contains(t, blocker, "reason")
	}

	// The published contract must accept every field the server actually sends.
	// StabilizationResult is additionalProperties:false, so a field present on
	// the wire but absent from the schema is a contract that rejects its own
	// service's real response.
	doc := publishedContract(t)
	schemas, ok := doc["components"].(map[string]any)["schemas"].(map[string]any)
	require.True(t, ok, "contract has no components.schemas")
	schema, ok := schemas["StabilizationResult"].(map[string]any)
	require.True(t, ok, "contract has no StabilizationResult schema")
	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok, "StabilizationResult has no properties")
	for field := range wire {
		require.Contains(t, properties, field,
			"the server sends %q but the contract does not describe it", field)
	}
}

// AC5: the impact task contract must describe what the service actually serves.
// The /impact-tasks route existed and returned ImpactTask while the contract
// described neither the route nor the schema, so a client generated from the
// contract could not represent the task state it needs to render.
func TestContractDescribesImpactTaskSurface(t *testing.T) {
	doc := publishedContract(t)
	paths, ok := doc["paths"].(map[string]any)
	require.True(t, ok, "contract has no paths")
	for _, name := range []string{"/projects/{projectId}/impact-tasks", "/projects/{projectId}/impact-tasks/{taskId}/actions"} {
		require.Contains(t, paths, name, "contract is missing the %s operation", name)
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	task, ok := schemas["ImpactTask"].(map[string]any)
	require.True(t, ok, "contract has no ImpactTask schema")

	// STALE must be a declared status with its own reason codes, otherwise a
	// consumer cannot explain an invalidated task.
	status := task["properties"].(map[string]any)["status"].(map[string]any)
	values, ok := status["enum"].([]any)
	require.True(t, ok, "ImpactTask.status has no enum")
	seen := map[string]bool{}
	for _, value := range values {
		seen[value.(string)] = true
	}
	for _, want := range ImpactTaskStatuses {
		require.True(t, seen[want], "ImpactTask.status enum is missing %q", want)
	}
	reasons, ok := task["properties"].(map[string]any)["stale_reason"].(map[string]any)
	require.True(t, ok, "ImpactTask has no stale_reason schema")
	require.Equal(t, "string", reasons["type"])
	require.Contains(t, reasons, "enum", "stale_reason must publish its reason codes")
}

// AC6: the verdict must be re-derived from persisted state after a restart, not
// carried in memory between two service compositions.
func TestStabilizationVerdictSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stabilize-restart.db")
	svc := impactTaskStore(t, path)
	actor := Actor{TenantID: 3213, UserID: "owner"}
	p, _, change := stabilizationFixture(t, svc, actor)
	closeBlockingTasks(t, svc, actor, p.ID)

	// Restart over the same database file.
	restarted := impactTaskStore(t, path)
	restarted.SetImpactRuleChecker(cleanRuleChecker{})
	result, err := runStabilization(t, restarted, actor, p.ID, change.ID, "stabilize-restart-stabilize")
	require.NoError(t, err)
	require.Equal(t, "STABILIZED", result.Status, "unexpected blockers: %+v", result.Blockers)
	require.Equal(t, "stabilized", changeSetStatus(t, restarted, actor, p.ID, change.ID))
}

// revokeChapterConfirmation revokes the chapter's current confirmation through the
// real persistence seam.
func revokeChapterConfirmation(t *testing.T, svc *Service, actor Actor, projectID, chapterID string) {
	t.Helper()
	for _, chapter := range mustListChapters(t, svc, actor, projectID) {
		if chapter.ID != chapterID || chapter.CurrentVersionID == nil {
			continue
		}
		require.NoError(t, svc.testDB().Model(&chapterVersionRow{}).Where("id = ?", *chapter.CurrentVersionID).
			Update("confirmation_valid", false).Error)
		require.NoError(t, svc.testDB().Where("chapter_id = ?", chapterID).Delete(&chapterConfirmationRow{}).Error)
		return
	}
	t.Fatalf("chapter %s not found", chapterID)
}
