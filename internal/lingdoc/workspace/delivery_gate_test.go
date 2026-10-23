package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"github.com/stretchr/testify/require"
)

// Acceptance: STABILIZED, the frozen snapshot, READY/preflight and
// export stay independent, and the client consumes the server gate result
// instead of inferring it. These tests drive the real delivery guard seam.

// applyChangeForGate applies a real ChangeSet on the current revision so the
// gate has an applied-but-unstabilized revision to reject.
// newDeliveryGuardFixtureOverDB rebuilds the whole guard composition over an
// existing database, which is what a process restart actually does: no gate
// verdict may be carried in memory between the two compositions.
func newDeliveryGuardFixtureOverDB(t *testing.T, f deliveryGuardFixture) deliveryGuardFixture {
	t.Helper()
	ctx := f.requestContext(f.actor.UserID, f.actor.Role, "gate-restart")
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	t.Cleanup(cancel)
	runtime := NewGORMSourceRuntime(f.db, ProjectPorts{}, adapterSharesStub{}, nil, delivery.NewFixedTemplateReader())
	projects := core.NewServiceWithSources(core.NewGORMRepository(f.db), core.ContractDemoTemplate{}, runtime.WorkspaceSourcePolicy())
	runtime.ConnectProjects(ProjectPorts{Authorizer: projects, Resources: projects, Projects: projects, Context: projects})
	connectAggregateCapabilities(runtime, projects)
	runtime.EnableFullWorkflowPolicy()
	projects.ConfigureAgentRuntime(core.AgentRuntime{Sources: NewResearchAdapter(f.db, projects, runtime, adapterSharesStub{}, nil, nil)})
	governance, err := NewTemplateGovernanceRuntime(f.db, projects, runtime, nil)
	require.NoError(t, err)
	projects.SetTemplateCatalogResolver(governance)
	projects.SetImpactRuleChecker(governance.ImpactRuleChecker())
	projects.RequireActivationRead(true)
	projects.SetRuntimeMode("real")
	guard := NewFullWorkflowGuard(fullWorkflowGuardPortsFor(projects), runtime)
	inputs := &candidateadoption.DeliveryInputService{Reader: candidateadoption.NewSQLiteCandidateAdoptionStore(f.db), Authorizer: deliveryGuardProjectAuthorizer{projects}}
	builder := runtime.DeliveryInputBuilder()
	snapshots := delivery.NewSQLiteSnapshotStore(f.db)
	releases := GuardFullWorkflowRelease(NewDeliveryReleaseService(inputs, builder, snapshots, runtime.CandidateAdoptionSourcePolicy()), guard)
	exports := GuardFullWorkflowExport(NewDeliveryExportServiceWithCurrentness(snapshots, delivery.NewSQLiteExportStore(f.db), DeliveryDocument{}, DeliveryDocument{}, inputs, runtime.WorkspaceSourcePolicy(), builder.(DeliveryCurrentness)), guard)
	p, err := projects.GetProject(ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	chapters, err := projects.ListChapters(ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	return deliveryGuardFixture{f.db, projects, runtime, governance, releases, exports, f.actor, p, ctx, chapters}
}

// stabilizeThroughDeliveryGate runs the real repair loop: reopen every blocking
// impact task the Owner actually closed through the domain API, then retry
// stabilization. It returns the server's StabilizationResult verbatim so the
// caller asserts the gate against the server's own blockers rather than a
// locally computed guess.
func stabilizeThroughDeliveryGate(t *testing.T, f deliveryGuardFixture, changeID string) core.StabilizationResult {
	t.Helper()
	repairGateObligations(t, f, changeID)
	raw, _, _, err := f.projects.StabilizeRevision(f.ctx, f.actor, f.project.ID, changeID, "gate-stabilize-"+changeID)
	require.NoError(t, err)
	var result core.StabilizationResult
	require.NoError(t, json.Unmarshal(raw, &result))
	return result
}

// repairGateObligations discharges every blocking obligation the Owner can
// close through the real repair loop, leaving the revision ready to be
// stabilized. It is the part of the loop that does NOT decide the verdict, so
// a caller may stop here, change the world in between, and only then ask for
// the verdict (#255 needs exactly that: a closure whose target is replaced
// after the obligations were discharged).
func repairGateObligations(t *testing.T, f deliveryGuardFixture, changeID string) {
	t.Helper()
	// A real Chapter edit invalidates the affected chapters' confirmation;
	// re-confirming is the actual human repair the rules checker demands.
	store := candidateadoption.NewSQLiteCandidateAdoptionStore(f.db)
	confirm := candidateadoption.NewConfirmationService(store, f.runtime.CandidateAdoptionSourcePolicy().(candidateadoption.ConfirmationSourcePolicy), adapterChapterAuthorizerStub{f.projects})
	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	confirmAll := func(round int) {
		t.Helper()
		input, err := store.ReadDeliveryInput(f.ctx, f.project.ID)
		require.NoError(t, err)
		for _, chapter := range input.Chapters {
			if chapter.ChapterVersionID == nil {
				continue
			}
			// A chapter version only counts as Confirmed at the current
			// Spec revision, so applying a ChangeSet forces a real re-save
			// before the human confirmation can bind again.
			raw, _, _, err := f.projects.SaveChapter(f.ctx, f.actor, f.project.ID, chapter.ChapterID, fmt.Sprintf("gate-resave-%s-%d", chapter.ChapterID, round), core.SaveChapterInput{
				ExpectedChapterVersionID: chapter.ChapterVersionID, ExpectedSpecRevision: p.SpecRevision,
				BodyMarkdown: "交付门禁 合成复核后的当前章节正文。", SourceIDs: []string{},
			})
			require.NoError(t, err, "re-saving chapter %s", chapter.ChapterID)
			var saved core.Chapter
			require.NoError(t, json.Unmarshal(raw, &saved))
			require.NotNil(t, saved.CurrentVersionID)
			_, _, err = confirm.ConfirmChapter(f.ctx, candidateadoption.ConfirmChapterInput{
				ProjectID: f.project.ID, ChapterID: chapter.ChapterID, ActorID: f.actor.UserID,
				IdempotencyKey:           fmt.Sprintf("gate-confirm-%s-%d-%s", changeID, round, chapter.ChapterID),
				ExpectedChapterVersionID: *saved.CurrentVersionID, ExpectedSpecRevision: int(p.SpecRevision),
				Decisions: []candidateadoption.ReviewDecision{},
			})
			require.NoError(t, err)
		}
		p, err = f.projects.GetProject(f.ctx, f.actor, f.project.ID)
		require.NoError(t, err)
	}

	// Repair alternates because both halves invalidate the other: closing an
	// INVALIDATE_CONFIRMATION task requires its target to be Confirmed, while
	// the resulting re-confirmation may raise further work. The loop stops as
	// soon as a round closes no task, so the last write before stabilization
	// is a task closure and its closure evidence stays current.
	for round := 0; round < 5; round++ {
		tasks := f.deliveryGateTasks(t)
		pending := false
		for _, task := range tasks {
			if task.TargetRevision == p.CurrentContextRevision && task.Status != "DONE" && task.Severity == core.ImpactSeverityBlocking && task.Action != "BLOCK_RELEASE" {
				pending = true
			}
		}
		if !pending {
			break
		}
		confirmAll(round)
		tasks = f.deliveryGateTasks(t)
		chapters, err := f.projects.ListChapters(f.ctx, f.actor, f.project.ID)
		require.NoError(t, err)
		currentVersion := map[string]*string{}
		for _, chapter := range chapters {
			currentVersion[chapter.ID] = chapter.CurrentVersionID
		}
		for _, task := range tasks {
			if task.TargetRevision != p.CurrentContextRevision || task.Status == "DONE" || task.Severity != core.ImpactSeverityBlocking || task.Action == "BLOCK_RELEASE" {
				continue
			}
			// completion requires an explicit assignment to the acting Owner.
			if task.AssigneeID != f.actor.UserID {
				raw, _, _, err := f.projects.ActOnImpactTask(f.ctx, f.actor, f.project.ID, task.ID, fmt.Sprintf("gate-assign-%s-%d", task.ID, round), core.ImpactTaskActionInput{
					ExpectedVersion: task.Version, TargetRevision: p.CurrentContextRevision, Action: "assign",
					AssigneeID: f.actor.UserID, Reason: "交付门禁 合成指派给 Owner",
				})
				require.NoError(t, err, "assigning task %s", task.ID)
				task = decodeMutationResult[core.ImpactTask](t, raw)
			}
			// Closure evidence must name the *current* version of the target,
			// not the version the task was originally raised against.
			evidence := task.Target
			if task.Target.Kind == "ChapterVersion" {
				if version, ok := currentVersion[task.Target.ID]; ok && version != nil {
					evidence.Version = *version
				}
			}
			_, _, _, err := f.projects.ActOnImpactTask(f.ctx, f.actor, f.project.ID, task.ID, fmt.Sprintf("gate-close-%s-%d", task.ID, round), core.ImpactTaskActionInput{
				ExpectedVersion: task.Version, TargetRevision: p.CurrentContextRevision, Action: "complete",
				HumanDecision: "reviewed", Reason: "交付门禁 合成复核当前版本",
				ClosureEvidence: []core.ObjectVersionRef{evidence},
			})
			require.NoError(t, err, "closing task %s action=%s target=%s/%s@%s", task.ID, task.Action, task.Target.Kind, task.Target.ID, task.Target.Version)
		}
	}

	// Stabilization rechecks any BLOCK_RELEASE proof against the *current*
	// basis. A receipt captured before the repair is stale, so whenever such a
	// task is still open a fresh preflight receipt must be produced and
	// acknowledged for this revision before stabilization can succeed.
	open := 0
	for _, task := range f.deliveryGateTasks(t) {
		if task.Action == "BLOCK_RELEASE" && task.ChangeSetID == changeID && task.TargetRevision == p.CurrentContextRevision && task.Status != "DONE" {
			open++
		}
	}
	if open > 0 {
		fresh, checkErr := f.releases.Check(f.ctx, f.actor.UserID, f.project.ID, p.ProjectVersion)
		require.NoError(t, checkErr)
		require.NotEmpty(t, fresh.ReceiptID)
		_, _, _, err = f.projects.AcknowledgeImpactPreflight(f.ctx, f.actor, f.project.ID, changeID, fresh.ReceiptID, "gate-ack-"+changeID)
		require.NoError(t, err)
	}
}

// deliveryGateTasks reads the current impact tasks for the fixture project.
func (f deliveryGuardFixture) deliveryGateTasks(t *testing.T) []core.ImpactTask {
	t.Helper()
	tasks, err := f.projects.ListImpactTasks(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	return tasks
}

// g3202Decode reads a domain mutation payload without pulling in another
// package's test helper.
func decodeMutationResult[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func applyChangeForGate(t *testing.T, f deliveryGuardFixture, key string) core.ChangeSet {
	t.Helper()
	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	raw, _, _, err := f.projects.CreateChangeSet(f.ctx, f.actor, p.ID, key+"-create", core.CreateChangeSetInput{
		ExpectedContextRevision: p.CurrentContextRevision,
		Fields:                  map[string]core.ChangeFieldInput{"research_subject": {OldValue: p.Spec["research_subject"], NewValue: "交付门禁 合成修改后的研究对象"}},
		AffectedChapterIDs:      []string{f.chapters[0].ID},
		Reason:                  "交付门禁 合成正式变更",
	})
	require.NoError(t, err)
	var change core.ChangeSet
	require.NoError(t, json.Unmarshal(raw, &change))
	_, _, _, err = f.projects.ApplyChangeSet(f.ctx, f.actor, p.ID, change.ID, key+"-apply")
	require.NoError(t, err)
	changes, err := f.projects.ListChangeSets(f.ctx, f.actor, p.ID)
	require.NoError(t, err)
	for _, item := range changes {
		if item.ID == change.ID {
			return item
		}
	}
	t.Fatalf("applied ChangeSet %s is not listed", change.ID)
	return change
}

// An applied but unstabilized current revision blocks freeze, export and
// download, and each rejection names the stabilization step instead of a bare
// invalid-state.
func TestDeliveryGateFreezeAndExportRequireStabilization(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	change := applyChangeForGate(t, f, "gate-gate")

	// Preflight/check itself stays available: stabilization is a separate,
	// independent step and must not be inferred from a check result.
	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	check, err := f.releases.Check(f.ctx, f.actor.UserID, f.project.ID, p.ProjectVersion)
	require.NoError(t, err, "the delivery preflight is independent of Owner stabilization")
	require.NotEmpty(t, check.ReceiptID)

	frozen, replay, err := f.releases.Prepare(f.ctx, f.actor.UserID, f.project.ID, "gate-gate-freeze", p.ProjectVersion)
	require.ErrorIs(t, err, core.ErrRevisionNotStable)
	require.False(t, replay)
	require.Empty(t, frozen.ID)

	artifact, replay, err := f.exports.Start(f.ctx, f.actor.UserID, f.project.ID, frozen.ID, "gate-gate-export")
	require.ErrorIs(t, err, core.ErrRevisionNotStable)
	require.False(t, replay)
	require.Empty(t, artifact.ID)

	var count int64
	require.NoError(t, f.db.Table("lingdoc_release_snapshots").Where("project_id=?", f.project.ID).Count(&count).Error)
	require.EqualValues(t, 0, count, "a rejected freeze must not persist a snapshot")

	raw, _, _, err := f.projects.StabilizeRevision(f.ctx, f.actor, f.project.ID, change.ID, "gate-gate-stabilize")
	require.NoError(t, err)
	var blocked core.StabilizationResult
	require.NoError(t, json.Unmarshal(raw, &blocked))
	require.Equal(t, "BLOCKED", blocked.Status, "an unconfirmed revision must report structured blockers")
	require.NotEmpty(t, blocked.Blockers)
	require.ErrorIs(t, f.projects.AssertRevisionStabilized(f.ctx, f.actor, f.project.ID), core.ErrRevisionNotStable,
		"a BLOCKED server result must keep the release gate closed")

	result := stabilizeThroughDeliveryGate(t, f, change.ID)
	require.Len(t, result.Blockers, 0, "after the real repair loop: %+v", result.Blockers)
	require.Equal(t, "STABILIZED", result.Status)

	require.NoError(t, f.projects.AssertRevisionStabilized(f.ctx, f.actor, f.project.ID))
	p, err = f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	snapshot, replay, err := f.releases.Prepare(f.ctx, f.actor.UserID, f.project.ID, "gate-gate-freeze", p.ProjectVersion)
	require.NoError(t, err)
	require.False(t, replay)
	require.Equal(t, delivery.CheckPassed, snapshot.Check.Status)

	// The frozen snapshot and the export remain separate facts.
	exported, replay, err := f.exports.Start(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID, "gate-gate-export")
	require.NoError(t, err)
	require.False(t, replay)
	require.Equal(t, delivery.ExportVerified, exported.Status)
	_, bytes, err := f.exports.Download(f.ctx, f.actor.UserID, f.project.ID, exported.ID)
	require.NoError(t, err)
	require.NotEmpty(t, bytes)
}

// A historical snapshot frozen before the current revision is not stabilized
// must not be re-exportable once the gate is closed again.
func TestDeliveryGateRejectsHistoricalSnapshot(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	applyChangeForGate(t, f, "gate-history")
	// There is no stabilized revision yet, so no current snapshot exists to
	// reuse; prove the gate rejects a fabricated/historical snapshot ID.
	artifact, _, err := f.exports.Start(f.ctx, f.actor.UserID, f.project.ID, "gate-no-such-snapshot", "gate-history-export")
	require.Error(t, err)
	require.Empty(t, artifact.ID)
}

// Repeating the delivery request after the gate is satisfied is idempotent:
// the same key returns the same verified artifact rather than a second render.
func TestDeliveryGateRepeatedExportIsIdempotent(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	change := applyChangeForGate(t, f, "gate-repeat")
	result := stabilizeThroughDeliveryGate(t, f, change.ID)
	require.Len(t, result.Blockers, 0, "unexpected blockers: %+v", result.Blockers)
	require.Equal(t, "STABILIZED", result.Status)

	p, err := f.projects.GetProject(f.ctx, f.actor, f.project.ID)
	require.NoError(t, err)
	snapshot, _, err := f.releases.Prepare(f.ctx, f.actor.UserID, f.project.ID, "gate-repeat-freeze", p.ProjectVersion)
	require.NoError(t, err)
	first, replay, err := f.exports.Start(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID, "gate-repeat-export")
	require.NoError(t, err)
	require.False(t, replay)
	second, replay, err := f.exports.Start(f.ctx, f.actor.UserID, f.project.ID, snapshot.ID, "gate-repeat-export")
	require.NoError(t, err)
	require.True(t, replay)
	require.Equal(t, first.ID, second.ID)

	var count int64
	require.NoError(t, f.db.Table("lingdoc_export_artifacts").Where("project_id=?", f.project.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

// The gate is reconstructed from persisted state after a restart: a fresh
// guard over the same database re-derives the same verdict without any
// in-memory carry-over.
func TestDeliveryGateRebuiltFromPersistedState(t *testing.T) {
	f := newDeliveryGuardFixture(t, false)
	change := applyChangeForGate(t, f, "gate-restart")
	// Restart: rebuild the guard and services over the same database.
	restarted := newDeliveryGuardFixtureOverDB(t, f)
	require.ErrorIs(t, restarted.projects.AssertRevisionStabilized(restarted.ctx, restarted.actor, f.project.ID), core.ErrRevisionNotStable)

	result := stabilizeThroughDeliveryGate(t, restarted, change.ID)
	require.Len(t, result.Blockers, 0, "unexpected blockers: %+v", result.Blockers)
	require.Equal(t, "STABILIZED", result.Status)
	require.NoError(t, restarted.projects.AssertRevisionStabilized(restarted.ctx, restarted.actor, f.project.ID))
}
