package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/stretchr/testify/require"
)

// G3-198.5 acceptance: Spec #198's last slice. The four earlier tickets each
// proved one layer in isolation. This file drives the whole delivery chain over
// the real composition - one SQLite database, the production GORM source
// runtime, real chapter confirmations, the real delivery guard - and asserts
// only externally observable results at every stage:
//
//	ChangeSet apply -> ImpactTask -> task disposition/confirmation
//	-> StabilizeRevision -> frozen Preflight -> READY -> export/download
//
// Each blocking scenario is paired with the recovery that follows it, so a
// refusal is never asserted without also proving the path reopens.

// g3203StabilizeResult reads the server's own stabilization verdict.
func g3203StabilizeResult(t *testing.T, f deliveryGuardFixture, changeID, key string) core.StabilizationResult {
	t.Helper()
	raw, _, _, err := f.projects.StabilizeRevision(f.ctx, f.actor, f.project.ID, changeID, key)
	require.NoError(t, err, "stabilize %s", changeID)
	var result core.StabilizationResult
	require.NoError(t, json.Unmarshal(raw, &result))
	return result
}

// g3203TaskByChange reads the visible impact tasks for one ChangeSet.
func g3203TaskByChange(t *testing.T, f deliveryGuardFixture, changeID string) core.ImpactTask {
	t.Helper()
	for _, task := range f.g3202Tasks(t) {
		if task.ChangeSetID == changeID {
			return task
		}
	}
	t.Fatalf("no visible impact task for change %s", changeID)
	return core.ImpactTask{}
}

// The whole chain, end to end, over one database and one composition. Each
// stage asserts the server's published result rather than an internal row.
func TestG3203DeliveryChainRunsFromApplyToVerifiedExport(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)

	// Stage 1: applying a real ChangeSet produces real impact obligations.
	change := applyG3202Change(t, f, "g3203-chain")
	tasks := f.g3202Tasks(t)
	require.NotEmpty(t, tasks, "applying a ChangeSet must raise impact obligations")
	task := g3203TaskByChange(t, f, change.ID)
	require.Equal(t, "OPEN", task.Status)
	require.Equal(t, "blocking", task.Severity)
	require.Equal(t, change.ID, task.ChangeSetID)

	// Stage 2: the gate is closed while the revision is unstabilized, and the
	// server names the stabilization step instead of a bare invalid state.
	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	blocked := g3203StabilizeResult(t, f, change.ID, "g3203-chain-blocked")
	require.Equal(t, "BLOCKED", blocked.Status)
	require.NotEmpty(t, blocked.Blockers)
	require.ErrorIs(t, f.projects.AssertRevisionStabilized(f.ctx, f.actor, f.project.ID), core.ErrRevisionNotStable)

	// Preflight is an independent fact: it evaluates and reports its own
	// blocked verdict while the stabilization gate is still shut, rather than
	// staying silent or reporting a false READY.
	check, err := f.releases.Check(f.ctx, f.actor.UserID, f.project.ID, p.ProjectVersion)
	require.NoError(t, err, "preflight must stay available while the gate is closed")
	require.Equal(t, delivery.CheckBlocked, check.Status, "an unstabilized revision must not preflight as READY")
	require.NotEmpty(t, check.ReceiptID, "even a blocked preflight publishes an auditable receipt")

	// Stage 3: the human repairs the obligations and the revision stabilizes.
	result := stabilizeG3202Revision(t, f, change.ID)
	require.Empty(t, result.Blockers, "unexpected blockers: %+v", result.Blockers)
	require.Equal(t, "STABILIZED", result.Status)
	require.NoError(t, f.projects.AssertRevisionStabilized(f.ctx, f.actor, f.project.ID))

	// Stage 4: the stabilized revision can be frozen.
	p, err = f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	snapshot, replay, err := f.releases.Prepare(f.ctx, f.actor.UserID, f.project.ID, "g3203-chain-freeze", p.ProjectVersion)
	require.NoError(t, err)
	require.False(t, replay)
	require.Equal(t, delivery.CheckPassed, snapshot.Check.Status)

	// Stage 5: READY and the export are separate facts, both reachable now.
	artifact, replay, err := f.exports.Start(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID, "g3203-chain-export")
	require.NoError(t, err)
	require.False(t, replay)
	require.Equal(t, delivery.ExportVerified, artifact.Status)
	_, payload, err := f.exports.Download(f.ctx, f.actor.UserID, f.project.ID, artifact.ID)
	require.NoError(t, err)
	require.NotEmpty(t, payload, "a verified export must be downloadable")
}

// Blocking side: a stale version expectation and a duplicated delivery event
// are both refused, and neither corrupts state. Recovery side: after the
// client re-reads and retries, both succeed against the same identities.
func TestG3203VersionConflictAndDuplicateEventsBlockThenRecover(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	change := applyG3202Change(t, f, "g3203-conflict")
	task := g3203TaskByChange(t, f, change.ID)

	// Block: acting with a stale expected_version is refused, not applied.
	_, _, _, err := f.projects.ActOnImpactTask(f.ctx, f.actor, f.project.ID, task.ID, "g3203-conflict-stale", core.ImpactTaskActionInput{
		ExpectedVersion: task.Version + 99, TargetRevision: task.TargetRevision, Action: "assign",
		AssigneeID: f.actor.UserID, Reason: "G3-203 故意使用过期版本",
	})
	require.ErrorIs(t, err, core.ErrVersionConflict)
	unchanged := g3203TaskByChange(t, f, change.ID)
	require.Equal(t, task.Version, unchanged.Version, "a refused action must not bump the task version")
	require.Equal(t, "OPEN", unchanged.Status)
	require.Empty(t, unchanged.AssigneeID, "a refused assignment must not record responsibility")

	// Recovery: the client re-reads the current version and the same action
	// now succeeds against the very same task identity.
	assigned := g2DecodeG3202[core.ImpactTask](t, mustActG3203(t, f, unchanged, "g3203-conflict-retry", "assign"))
	require.Equal(t, f.actor.UserID, assigned.AssigneeID)
	require.Equal(t, unchanged.Version+1, assigned.Version)

	// Block: a duplicated delivery event replays the same idempotency key.
	// The replay must return the first result, not perform the action twice.
	first := g2DecodeG3202[core.ImpactTask](t, mustActG3203(t, f, assigned, "g3203-conflict-dup", "start"))
	second := g2DecodeG3202[core.ImpactTask](t, mustActG3203(t, f, assigned, "g3203-conflict-dup", "start"))
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, first.Version, second.Version, "a replayed event advanced the task twice")
	require.Equal(t, first.Status, second.Status)

	// Recovery: the chain still completes from the recovered state.
	result := stabilizeG3202Revision(t, f, change.ID)
	require.Empty(t, result.Blockers, "unexpected blockers: %+v", result.Blockers)
	require.Equal(t, "STABILIZED", result.Status)
}

// mustActG3203 performs one impact-task action and requires it to succeed.
func mustActG3203(t *testing.T, f deliveryGuardFixture, task core.ImpactTask, key, action string) json.RawMessage {
	t.Helper()
	raw, _, _, err := f.projects.ActOnImpactTask(f.ctx, f.actor, f.project.ID, task.ID, key, core.ImpactTaskActionInput{
		ExpectedVersion: task.Version, TargetRevision: task.TargetRevision, Action: action,
		Reason: "G3-203 " + action, HumanDecision: "reviewed", AssigneeID: f.actor.UserID,
	})
	require.NoError(t, err, "act %s on task %s", action, task.ID)
	return raw
}

// Blocking side: when the audit write fails, the whole task transition rolls
// back with zero partial state. Recovery side: once auditing works again the
// identical action commits, so the failure was transactional and not a
// permanent loss of the obligation.
func TestG3203AuditFailureRollsBackTheWholeTransitionAndRecovers(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	change := applyG3202Change(t, f, "g3203-audit")
	task := g3203TaskByChange(t, f, change.ID)
	assigned := g2DecodeG3202[core.ImpactTask](t, mustActG3203(t, f, task, "g3203-audit-assign", "assign"))

	// Block: the audit sink for this action is unavailable.
	require.NoError(t, f.db.Exec(
		"CREATE TRIGGER g3203_audit_failure BEFORE INSERT ON lingdoc_project_audits "+
			"WHEN NEW.action = 'impact_task.start' BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END").Error)
	_, _, _, err := f.projects.ActOnImpactTask(f.ctx, f.actor, f.project.ID, assigned.ID, "g3203-audit-start", core.ImpactTaskActionInput{
		ExpectedVersion: assigned.Version, TargetRevision: assigned.TargetRevision, Action: "start",
		Reason: "G3-203 审计故障", HumanDecision: "reviewed",
	})
	require.Error(t, err, "a failed audit must fail the whole operation")
	require.NoError(t, f.db.Exec("DROP TRIGGER g3203_audit_failure").Error)

	// Nothing was written: the transition and its version bump both rolled back.
	rolledBack := g3203TaskByChange(t, f, change.ID)
	require.Equal(t, assigned.Version, rolledBack.Version, "a failed audit still advanced the task")
	require.Equal(t, assigned.Status, rolledBack.Status)
	require.Empty(t, rolledBack.StaleReason)

	// Recovery: the identical action now commits.
	started := g2DecodeG3202[core.ImpactTask](t, mustActG3203(t, f, rolledBack, "g3203-audit-retry", "start"))
	require.Equal(t, "IN_PROGRESS", started.Status)
	require.Equal(t, rolledBack.Version+1, started.Version)

	result := stabilizeG3202Revision(t, f, change.ID)
	require.Empty(t, result.Blockers, "unexpected blockers: %+v", result.Blockers)
	require.Equal(t, "STABILIZED", result.Status)
}

// Restart read-back over the real persistent driver (SQLite through GORM, not
// an in-memory store): a brand-new service and guard composition over the same
// database re-derives task history, responsibility and the delivery gate
// verdict without carrying anything in memory.
func TestG3203TaskHistoryAndDeliveryGateSurviveRestartOnRealStorage(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	change := applyG3202Change(t, f, "g3203-restart")
	task := g3203TaskByChange(t, f, change.ID)
	assigned := g2DecodeG3202[core.ImpactTask](t, mustActG3203(t, f, task, "g3203-restart-assign", "assign"))
	started := g2DecodeG3202[core.ImpactTask](t, mustActG3203(t, f, assigned, "g3203-restart-start", "start"))

	// Restart: a brand-new runtime, service, guard, release and export service
	// over the same SQLite file.
	restarted := newDeliveryGuardFixtureOverDB(t, f)

	readBack := g3203TaskByChange(t, restarted, change.ID)
	require.Equal(t, started.ID, readBack.ID, "task identity lost across restart")
	require.Equal(t, started.Version, readBack.Version, "task version lost across restart")
	require.Equal(t, started.Status, readBack.Status, "task status lost across restart")
	require.Equal(t, f.actor.UserID, readBack.AssigneeID, "responsibility lost across restart")
	require.Equal(t, started.Target, readBack.Target)
	require.Equal(t, started.TargetRevision, readBack.TargetRevision)
	require.Equal(t, len(started.Causes), len(readBack.Causes), "causes lost across restart")
	require.Equal(t, started.HumanDecision, readBack.HumanDecision)

	// The gate verdict is re-derived from storage, not remembered.
	require.ErrorIs(t, restarted.projects.AssertRevisionStabilized(restarted.ctx, restarted.actor, f.project.ID), core.ErrRevisionNotStable,
		"a restart must not remember a gate verdict that was never reached")

	// And the whole chain still completes from the restarted composition.
	result := stabilizeG3202Revision(t, restarted, change.ID)
	require.Empty(t, result.Blockers, "unexpected blockers: %+v", result.Blockers)
	require.Equal(t, "STABILIZED", result.Status)
	require.NoError(t, restarted.projects.AssertRevisionStabilized(restarted.ctx, restarted.actor, f.project.ID))

	p, err := restarted.projects.GetProject(restarted.ctx, restarted.actor, f.project.ID)
	require.NoError(t, err)
	snapshot, _, err := restarted.releases.Prepare(restarted.ctx, restarted.actor.UserID, f.project.ID, "g3203-restart-freeze", p.ProjectVersion)
	require.NoError(t, err)
	artifact, _, err := restarted.exports.Start(restarted.ctx, restarted.actor.UserID, f.project.ID, snapshot.ID, "g3203-restart-export")
	require.NoError(t, err)
	require.Equal(t, delivery.ExportVerified, artifact.Status)
}

// The gate refusal must name the change that has to be stabilized, so an
// operator can act on it. A bare invalid-state error would not be actionable.
func TestG3203GateRefusalNamesTheChangeSetToStabilize(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	change := applyG3202Change(t, f, "g3203-named")

	err := f.projects.AssertRevisionStabilized(f.ctx, f.actor, f.project.ID)
	require.ErrorIs(t, err, core.ErrRevisionNotStable)
	require.Contains(t, err.Error(), change.ID,
		"the release gate must name the ChangeSet the owner has to stabilize")
	require.True(t, errors.Is(err, core.ErrRevisionNotStable))
	require.NotEmpty(t, fmt.Sprint(change.ID))
}
