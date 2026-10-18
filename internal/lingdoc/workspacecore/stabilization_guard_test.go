package workspacecore

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/template"
	"github.com/stretchr/testify/require"
)

// 稳定化守护: #209 proved that 16 of the 20 stabilization conditions can be
// deleted without turning any test red, so a behaviour-preserving refactor
// could not be proven safe. Each case here pins ONE condition to a literal
// blocker code (or a literal error identity) on a real production seam.
//
// Reused fixtures: extendedImpactStore (real migrated SQLite store with the G3
// and G6 record tables), extendedChapter/extendedChange (real ChangeSet
// lifecycle), closeBlockingTasks (the genuine repair loop), revokeChapterConfirmation
// and markChangeSetStabilized. Nothing here weakens an existing assertion.

// guardFixture drives one real ChangeSet to applied and returns the project as
// it stands after the apply, so tasks and the change set live at the CURRENT
// context revision exactly as stabilization will see them.
func guardFixture(t *testing.T, key string) (*Service, Actor, Project, Chapter, ChangeSet) {
	t.Helper()
	svc, actor, p := extendedImpactStore(t)
	chapter := extendedChapter(t, svc, actor, p)
	change := extendedChange(t, svc, actor, p, chapter)
	if _, _, _, err := svc.ApplyChangeSet(context.Background(), actor, p.ID, change.ID, key+"-apply"); err != nil {
		t.Fatalf("apply change set: %v", err)
	}
	return svc, actor, reloadProject(t, svc, actor, p.ID), chapter, change
}

// guardTask inserts one impact task through the real store seam, so a guard can
// exercise a state the ordinary commit path cannot reach at this point in time.
func guardTask(t *testing.T, svc *Service, task ImpactTask) ImpactTask {
	t.Helper()
	if task.ID == "" {
		t.Fatal("guard task needs an id")
	}
	if task.Severity == "" {
		task.Severity = "advisory"
	}
	if task.Causes == nil {
		task.Causes = []ImpactCause{}
	}
	if task.ClosureEvidence == nil {
		task.ClosureEvidence = []ObjectVersionRef{}
	}
	if task.Version == 0 {
		task.Version = 1
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now().UTC()
	}
	task.UpdatedAt = time.Now().UTC()
	store := gormTransaction{svc.testDB()}
	if err := store.InsertImpactTask(task); err != nil {
		t.Fatalf("seed task %s: %v", task.ID, err)
	}
	return task
}

// blockerFor returns the blocker carrying the given literal code, so the
// assertion names WHICH reason fired instead of only "some blocker exists".
func blockerFor(t *testing.T, result StabilizationResult, code string) ImpactBlocker {
	t.Helper()
	for _, blocker := range result.Blockers {
		if blocker.Code == code {
			return blocker
		}
	}
	t.Fatalf("no %q blocker in %+v", code, result.Blockers)
	return ImpactBlocker{}
}

// requireGateError asserts the gate refused with the literal domain identity.
func requireGateError(t *testing.T, err error, want error, contains string) {
	t.Helper()
	require.Error(t, err, "the gate must refuse")
	require.True(t, errors.Is(err, want), "gate error %v is not %v", err, want)
	if contains != "" {
		require.True(t, strings.Contains(err.Error(), contains), "gate error %q does not contain %q", err, contains)
	}
}

// genuineReceipt stores one computation receipt through the production record
// seam whose basis matches the CURRENT project, i.e. a receipt that genuinely
// satisfied its RECOMPUTE task at the moment it was written.
func genuineReceipt(t *testing.T, svc *Service, actor Actor, p Project) ImpactComputationReceipt {
	t.Helper()
	var receipt ImpactComputationReceipt
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		basis, err := ImpactComputationBasisHash(tx, p)
		if err != nil {
			return err
		}
		sourceHash, err := tx.(impactComputationSourceTransaction).ImpactSourceDigest(p.ID, nil)
		if err != nil {
			return err
		}
		receipt = ImpactComputationReceipt{ID: "guard-receipt", Version: 1, ProjectID: p.ID, Status: "blocked",
			ProjectVersion: p.ProjectVersion, ContextRevision: p.CurrentContextRevision,
			TemplateCopyVersion: p.TemplateCopyVersion, RulesetHash: p.TemplateCopy.RulesetHash,
			InputHash: "guard-evaluator-input", ComputationBasisHash: basis, SourceBasisHash: sourceHash,
			SourceIDs: []string{}, CheckedAt: time.Now().UTC(),
			Evaluations: []template.Evaluation{{RuleID: "required-fields", TargetRef: p.ID, TargetVersion: "1",
				RulesetHash: p.TemplateCopy.RulesetHash, EvaluatorVersion: template.EvaluatorVersion, Status: template.EvaluationIssue}}}
		return tx.(runtimeTransaction).G6Insert(actor.TenantID, p.ID, "preflight_check", receipt.ID, 1, receipt)
	}))
	return receipt
}

// recomputeReceiptRef is the closure evidence a DONE RECOMPUTE task carries for
// the receipt above.
func recomputeReceiptRef(receipt ImpactComputationReceipt) ObjectVersionRef {
	return ObjectVersionRef{Kind: "ComputationReceipt", ID: receipt.ID, Version: strconv.FormatInt(receipt.Version, 10)}
}

// appendRouteNode writes one route node through the real store seam. Routes are
// part of the computation basis a receipt was hashed over, so adding one drifts
// that basis without invalidating any chapter closure evidence.
func appendRouteNode(t *testing.T, svc *Service, p Project, id string, required bool, status string) RouteNode {
	t.Helper()
	node := RouteNode{ID: id, ProjectID: p.ID, Name: "合成路线 " + id, Required: required, Status: status,
		Version: 1, ContextRevision: p.CurrentContextRevision, ActorID: "route-author", Inputs: []ObjectVersionRef{},
		Reason: "守护 合成路线", UpdatedAt: time.Now().UTC()}
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		return tx.(semanticTransaction).AppendRouteNode(RouteNode{}, node)
	}), "seed route %s", id)
	return node
}

// blockingRuleChecker is a checker that reports one blocker, so the gate side of
// the rule condition can be observed distinctly from an absent checker.
type blockingRuleChecker struct{}

func (blockingRuleChecker) Check(Transaction, Project) ([]ImpactBlocker, error) {
	return []ImpactBlocker{{Code: "guard_rule", Reason: "合成规则阻断"}}, nil
}

// fixturePreflightChecker answers a BLOCK_RELEASE preflight recheck from the
// project's own current metadata. With current=false the receipt it returns no
// longer describes current truth, which is exactly the state a preflight
// receipt drifts into between the task being closed and stabilization.
type fixturePreflightChecker struct {
	project Project
	current bool
}

func (c fixturePreflightChecker) Recheck(_ context.Context, _ Actor, _, snapshotID string) (ImpactPreflightEvidence, error) {
	return ImpactPreflightEvidence{ProofID: snapshotID, SnapshotDigest: "guard-preflight-digest", Status: "passed",
		IsCurrent: c.current, ProjectVersion: c.project.ProjectVersion, ContextRevision: c.project.CurrentContextRevision,
		TemplateCopyVersion: c.project.TemplateCopyVersion, RulesetHash: c.project.TemplateCopy.RulesetHash,
		SourceIDs: []string{}}, nil
}

// blockReleaseTask closes the BLOCK_RELEASE obligation against one preflight
// snapshot, which is the only closure a BLOCK_RELEASE task may carry.
func blockReleaseTask(t *testing.T, svc *Service, p Project, changeID, id, snapshotID string) ImpactTask {
	t.Helper()
	return guardTask(t, svc, ImpactTask{ID: id, ProjectID: p.ID, ChangeSetID: changeID,
		Target:         ObjectVersionRef{Kind: "ProjectSpec", ID: p.ID, Version: strconv.FormatInt(p.SpecRevision, 10)},
		TargetRevision: p.CurrentContextRevision, Action: "BLOCK_RELEASE", Severity: "blocking", Status: "DONE",
		Disposition: "completed", ClosureEvidence: []ObjectVersionRef{{Kind: "ProjectSpec", ID: p.ID, Version: snapshotID}}})
}

// Owner path: a DONE RECOMPUTE task whose result receipt went stale before
// stabilization must report the literal code result_receipt_stale.
func TestStabilizationBlocksStaleResultReceipt(t *testing.T) {
	svc, actor, p, _, change := guardFixture(t, "guard-receipt-owner")
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	receipt := genuineReceipt(t, svc, actor, p)
	task := guardTask(t, svc, ImpactTask{ID: "guard-recompute-owner", ProjectID: p.ID, ChangeSetID: change.ID,
		Target:         ObjectVersionRef{Kind: "ProjectSpec", ID: p.ID, Version: strconv.FormatInt(p.SpecRevision, 10)},
		TargetRevision: p.CurrentContextRevision, Action: "RECOMPUTE", Status: "DONE", Disposition: "completed",
		ClosureEvidence: []ObjectVersionRef{recomputeReceiptRef(receipt)}})
	closeBlockingTasks(t, svc, actor, p.ID)

	// A new route node drifts the computation basis the receipt was hashed over,
	// so the receipt still resolves and still matches its own version, but no
	// longer describes current truth.
	appendRouteNode(t, svc, p, "guard-receipt-drift", false, "ready")

	result, err := runStabilization(t, svc, actor, p.ID, change.ID, "guard-receipt-stabilize")
	require.NoError(t, err)
	require.Equal(t, "BLOCKED", result.Status, "unexpected blockers: %+v", result.Blockers)
	blocker := blockerFor(t, result, "result_receipt_stale")
	require.Equal(t, task.ID, blocker.TargetID, "the stale receipt must name its task: %+v", blocker)
	require.NotEmpty(t, blocker.Reason)
}

// Gate path: the same stale receipt must fail the read-only gate with the
// receipt's own version-conflict identity, not with a bare gate verdict.
func TestReleaseGateRejectsStaleResultReceipt(t *testing.T) {
	svc, actor, p, _, change := guardFixture(t, "guard-receipt-gate")
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	closeBlockingTasks(t, svc, actor, p.ID)
	markChangeSetStabilized(t, svc, actor, p.ID, change.ID)
	receipt := genuineReceipt(t, svc, actor, p)
	guardTask(t, svc, ImpactTask{ID: "guard-recompute-gate", ProjectID: p.ID, ChangeSetID: change.ID,
		Target:         ObjectVersionRef{Kind: "ProjectSpec", ID: p.ID, Version: strconv.FormatInt(p.SpecRevision, 10)},
		TargetRevision: p.CurrentContextRevision, Action: "RECOMPUTE", Status: "DONE", Disposition: "completed",
		ClosureEvidence: []ObjectVersionRef{recomputeReceiptRef(receipt)}})
	appendRouteNode(t, svc, p, "guard-receipt-gate-drift", false, "ready")

	requireGateError(t, svc.AssertRevisionStabilized(context.Background(), actor, p.ID), ErrVersionConflict, "")
}

// Owner path: a DONE task whose closure evidence is no longer confirmed must
// report the literal code closure_stale, not merely go STALE.
func TestStabilizationBlocksStaleClosureEvidence(t *testing.T) {
	svc, actor, p, _, change := guardFixture(t, "guard-closure-owner")
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	closeBlockingTasks(t, svc, actor, p.ID)
	task := taskFor(t, tasksInProject(t, svc, actor, p.ID), change.ID)
	require.Equal(t, "DONE", task.Status, "fixture precondition: the task is closed")
	require.Contains(t, []string{"REVIEW_REQUIRED", "INVALIDATE_CONFIRMATION", "REVALIDATE_EVIDENCE"}, task.Action)
	require.Len(t, task.ClosureEvidence, 1)
	require.Equal(t, "ChapterVersion", task.ClosureEvidence[0].Kind)

	// The version the closure named still exists, but its confirmation lapsed:
	// the result holds while the confirmation it depended on does not.
	revokeChapterConfirmation(t, svc, actor, p.ID, task.ClosureEvidence[0].ID)

	result, err := runStabilization(t, svc, actor, p.ID, change.ID, "guard-closure-stabilize")
	require.NoError(t, err)
	require.Equal(t, "BLOCKED", result.Status, "unexpected blockers: %+v", result.Blockers)
	blocker := blockerFor(t, result, "closure_stale")
	require.Equal(t, task.ID, blocker.TargetID, "the stale closure must name its task: %+v", blocker)
	require.NotContains(t, result.StaleTaskIDs, task.ID, "an unconfirmed closure is a blocker, not a STALE transition")

	// Re-confirming the very version the closure named is the real repair, and
	// only then may the revision stabilize.
	confirmChapterAtSpecRevision(t, svc, actor, p.ID, reloadProject(t, svc, actor, p.ID),
		chapterByID(t, svc, actor, p.ID, task.ClosureEvidence[0].ID))
	repaired, err := runStabilization(t, svc, actor, p.ID, change.ID, "guard-closure-retry")
	require.NoError(t, err)
	require.Equal(t, "STABILIZED", repaired.Status, "unexpected blockers: %+v", repaired.Blockers)
}

// chapterByID returns the chapter a closure evidence ref names.
func chapterByID(t *testing.T, svc *Service, actor Actor, projectID, chapterID string) Chapter {
	t.Helper()
	for _, chapter := range mustListChapters(t, svc, actor, projectID) {
		if chapter.ID == chapterID {
			return chapter
		}
	}
	t.Fatalf("chapter %s not found", chapterID)
	return Chapter{}
}

// Gate path: the gate must independently reject the same unconfirmed closure.
func TestReleaseGateRejectsUnconfirmedClosureEvidence(t *testing.T) {
	svc, actor, p, _, change := guardFixture(t, "guard-closure-gate")
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	closeBlockingTasks(t, svc, actor, p.ID)
	markChangeSetStabilized(t, svc, actor, p.ID, change.ID)
	task := taskFor(t, tasksInProject(t, svc, actor, p.ID), change.ID)
	revokeChapterConfirmation(t, svc, actor, p.ID, task.ClosureEvidence[0].ID)

	requireGateError(t, svc.AssertRevisionStabilized(context.Background(), actor, p.ID), ErrInvalidState, "")
}

// Owner path: a required route that is not current must produce the literal
// code required_route_not_current.
func TestStabilizationBlocksNotCurrentRequiredRoute(t *testing.T) {
	svc, actor, p, _, change := guardFixture(t, "guard-route-owner")
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	closeBlockingTasks(t, svc, actor, p.ID)
	node := appendRouteNode(t, svc, p, "guard-route-owner", true, "planned")

	result, err := runStabilization(t, svc, actor, p.ID, change.ID, "guard-route-stabilize")
	require.NoError(t, err)
	require.Equal(t, "BLOCKED", result.Status, "unexpected blockers: %+v", result.Blockers)
	blocker := blockerFor(t, result, "required_route_not_current")
	require.Equal(t, node.ID, blocker.TargetID, "the unready route must be named: %+v", blocker)
}

// Gate path: the same unready required route must fail the gate.
func TestReleaseGateRejectsNotCurrentRequiredRoute(t *testing.T) {
	svc, actor, p, _, change := guardFixture(t, "guard-route-gate")
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	closeBlockingTasks(t, svc, actor, p.ID)
	markChangeSetStabilized(t, svc, actor, p.ID, change.ID)
	appendRouteNode(t, svc, p, "guard-route-gate", true, "planned")

	err := svc.AssertRevisionStabilized(context.Background(), actor, p.ID)
	requireGateError(t, err, ErrInvalidState, "")
}

// Owner path: a ChangeSet whose target revision is no longer the current one is
// refused as a version conflict, before any blocker is computed.
func TestStabilizationRefusesChangeSetOfAnotherRevision(t *testing.T) {
	svc, actor, p, chapter, change := guardFixture(t, "guard-revision-owner")
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	closeBlockingTasks(t, svc, actor, p.ID)

	// A second applied ChangeSet advances the current context revision, so the
	// first ChangeSet now names a revision that no longer exists.
	fresh := reloadProject(t, svc, actor, p.ID)
	raw, _, _, err := svc.CreateChangeSet(context.Background(), actor, p.ID, "guard-revision-second", CreateChangeSetInput{
		ExpectedContextRevision: fresh.CurrentContextRevision,
		Fields:                  map[string]ChangeFieldInput{"research_subject": {OldValue: fresh.Spec["research_subject"], NewValue: "守护 第二个主题"}},
		AffectedChapterIDs:      []string{chapter.ID}, Reason: "守护 第二次正式变更"})
	require.NoError(t, err)
	second := g2Decode[ChangeSet](t, raw, nil)
	if _, _, _, err := svc.ApplyChangeSet(context.Background(), actor, p.ID, second.ID, "guard-revision-second-apply"); err != nil {
		t.Fatalf("apply second change set: %v", err)
	}
	require.Greater(t, reloadProject(t, svc, actor, p.ID).CurrentContextRevision, p.CurrentContextRevision)

	_, err = runStabilization(t, svc, actor, p.ID, change.ID, "guard-revision-stabilize")
	require.Error(t, err, "a ChangeSet from another revision must be refused")
	require.True(t, errors.Is(err, ErrVersionConflict), "revision mismatch error %v is not a version conflict", err)
}

// setTemplateCopyStatus drives the project's template copy through the real
// project store, which is how a copy stops being the bound copy.
func setTemplateCopyStatus(t *testing.T, svc *Service, actor Actor, projectID, status string) {
	t.Helper()
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		p, err := svc.GetProject(context.Background(), actor, projectID)
		if err != nil {
			return err
		}
		next := p
		copy := *p.TemplateCopy
		copy.Status = status
		next.TemplateCopy = &copy
		next.ProjectVersion++
		return tx.UpdateProject(actor.TenantID, p, next)
	}))
}

// Owner path: a template copy that is not the bound copy must produce the
// literal code template_unbound.
func TestStabilizationBlocksUnboundTemplateCopy(t *testing.T) {
	svc, actor, p, _, change := guardFixture(t, "guard-template-owner")
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	closeBlockingTasks(t, svc, actor, p.ID)
	setTemplateCopyStatus(t, svc, actor, p.ID, TemplateCopySuperseded)

	result, err := runStabilization(t, svc, actor, p.ID, change.ID, "guard-template-stabilize")
	require.NoError(t, err)
	require.Equal(t, "BLOCKED", result.Status, "unexpected blockers: %+v", result.Blockers)
	blocker := blockerFor(t, result, "template_unbound")
	require.NotEmpty(t, blocker.Reason)
}

// Owner path: with no rule checker installed the verdict must name the literal
// code rules_checker_unavailable, and installing one must lift exactly that
// blocker.
func TestStabilizationBlocksWithoutRuleChecker(t *testing.T) {
	svc, actor, p, _, change := guardFixture(t, "guard-rules-owner")
	svc.SetImpactRuleChecker(nil)
	closeBlockingTasks(t, svc, actor, p.ID)

	result, err := runStabilization(t, svc, actor, p.ID, change.ID, "guard-rules-stabilize")
	require.NoError(t, err)
	require.Equal(t, "BLOCKED", result.Status, "unexpected blockers: %+v", result.Blockers)
	blockerFor(t, result, "rules_checker_unavailable")

	svc.SetImpactRuleChecker(cleanRuleChecker{})
	repaired, err := runStabilization(t, svc, actor, p.ID, change.ID, "guard-rules-retry")
	require.NoError(t, err)
	require.Equal(t, "STABILIZED", repaired.Status, "unexpected blockers: %+v", repaired.Blockers)

	// The checker's own blockers must reach the verdict verbatim, so a rule
	// blocker cannot be swallowed by the surrounding condition.
	svc.SetImpactRuleChecker(blockingRuleChecker{})
	ruled, err := runStabilization(t, svc, actor, p.ID, change.ID, "guard-rules-blocking")
	require.NoError(t, err)
	require.Equal(t, "BLOCKED", ruled.Status, "a rule blocker must block: %+v", ruled.Blockers)
	blockerFor(t, ruled, "guard_rule")
}

// Gate path: an absent rule checker is its own identity on the gate side, and a
// checker that reports a blocker must refuse the gate as well.
func TestReleaseGateFailsOnUnavailableAndBlockingRuleChecker(t *testing.T) {
	svc, actor, p, _, change := guardFixture(t, "guard-rules-gate")
	svc.SetImpactRuleChecker(nil)
	closeBlockingTasks(t, svc, actor, p.ID)
	markChangeSetStabilized(t, svc, actor, p.ID, change.ID)

	requireGateError(t, svc.AssertRevisionStabilized(context.Background(), actor, p.ID), ErrImpactObjectUnavailable, "")

	svc.SetImpactRuleChecker(blockingRuleChecker{})
	requireGateError(t, svc.AssertRevisionStabilized(context.Background(), actor, p.ID), ErrInvalidState, "current rules block release")
}

// guardClaim stores one ResearchClaim whose basis is the current spec
// revision, so the claim itself is current and only the HUMAN REVIEW behind it
// is in question.
func guardClaim(t *testing.T, svc *Service, p Project, id string) ObjectVersionRef {
	t.Helper()
	claim := ResearchClaim{ID: id, ProjectID: p.ID, Version: 1, Text: "守护 合成研究主张", Scope: "守护范围",
		Status: "active", Basis: G2Basis{ProjectVersion: p.ProjectVersion, SpecRevision: p.SpecRevision,
			ContextRevision: p.CurrentContextRevision}, Reason: "守护 合成主张", CreatedBy: "owner",
		CreatedAt: time.Now().UTC()}
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		return g2Store(tx, G2StoredObject{}, p.ID, G2ClaimKind, id, 1, claim)
	}))
	return ObjectVersionRef{Kind: "ResearchClaim", ID: id, Version: "1"}
}

// Owner path: a REVIEW_REQUIRED task on a ResearchClaim is only satisfied by a
// recorded human review. A decision that is not a review must still be blocked
// with the literal closure_stale code, rather than riding the research-claim
// review escape.
func TestStabilizationRequiresHumanReviewOfResearchClaim(t *testing.T) {
	svc, actor, p, _, change := guardFixture(t, "guard-claim-owner")
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	closeBlockingTasks(t, svc, actor, p.ID)

	// A recorded human review of a current claim is the accepted path.
	reviewedRef := guardClaim(t, svc, p, "guard-claim-reviewed-claim")
	guardTask(t, svc, ImpactTask{ID: "guard-claim-reviewed", ProjectID: p.ID, ChangeSetID: change.ID, Target: reviewedRef,
		TargetRevision: p.CurrentContextRevision, Action: "REVIEW_REQUIRED", Status: "DONE", Disposition: "completed",
		HumanDecision: "reviewed", ClosureEvidence: []ObjectVersionRef{reviewedRef}})
	accepted, err := runStabilization(t, svc, actor, p.ID, change.ID, "guard-claim-reviewed")
	require.NoError(t, err)
	require.Equal(t, "STABILIZED", accepted.Status, "a reviewed current claim must not block: %+v", accepted.Blockers)

	// The same kind of claim closed with a decision that is not a human review
	// must not inherit that escape.
	deferredRef := guardClaim(t, svc, p, "guard-claim-deferred-claim")
	guardTask(t, svc, ImpactTask{ID: "guard-claim-deferred", ProjectID: p.ID, ChangeSetID: change.ID, Target: deferredRef,
		TargetRevision: p.CurrentContextRevision, Action: "REVIEW_REQUIRED", Status: "DONE", Disposition: "completed",
		HumanDecision: "deferred", ClosureEvidence: []ObjectVersionRef{deferredRef}})
	blocked, err := runStabilization(t, svc, actor, p.ID, change.ID, "guard-claim-deferred")
	require.NoError(t, err)
	require.Equal(t, "BLOCKED", blocked.Status, "an unreviewed claim must block: %+v", blocked.Blockers)
	blocker := blockerFor(t, blocked, "closure_stale")
	require.Equal(t, "guard-claim-deferred", blocker.TargetID)
}

// Gate path: the gate must independently require that human review.
func TestReleaseGateRequiresHumanReviewOfResearchClaim(t *testing.T) {
	svc, actor, p, _, change := guardFixture(t, "guard-claim-gate")
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	closeBlockingTasks(t, svc, actor, p.ID)
	markChangeSetStabilized(t, svc, actor, p.ID, change.ID)
	ref := guardClaim(t, svc, p, "guard-claim-gate")
	guardTask(t, svc, ImpactTask{ID: "guard-claim-gate-deferred", ProjectID: p.ID, ChangeSetID: change.ID, Target: ref,
		TargetRevision: p.CurrentContextRevision, Action: "REVIEW_REQUIRED", Status: "DONE", Disposition: "completed",
		HumanDecision: "deferred", ClosureEvidence: []ObjectVersionRef{ref}})

	requireGateError(t, svc.AssertRevisionStabilized(context.Background(), actor, p.ID), ErrInvalidState, "")
}

// Owner path: a BLOCK_RELEASE task closed against a preflight receipt that is
// no longer current must be refused as a version conflict.
func TestStabilizationRechecksBlockReleasePreflight(t *testing.T) {
	svc, actor, p, _, change := guardFixture(t, "guard-preflight-owner")
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	closeBlockingTasks(t, svc, actor, p.ID)
	blockReleaseTask(t, svc, p, change.ID, "guard-block-release-owner", "guard-preflight-owner")

	// A current receipt leaves the verdict to the ordinary blocker rules.
	svc.SetImpactPreflightChecker(fixturePreflightChecker{project: reloadProject(t, svc, actor, p.ID), current: true})
	current, err := runStabilization(t, svc, actor, p.ID, change.ID, "guard-preflight-current")
	require.NoError(t, err, "a current preflight receipt must not refuse the call")
	require.NotNil(t, current)

	// The same receipt after it stopped being current must refuse.
	svc.SetImpactPreflightChecker(fixturePreflightChecker{project: reloadProject(t, svc, actor, p.ID), current: false})
	_, err = runStabilization(t, svc, actor, p.ID, change.ID, "guard-preflight-stale")
	require.Error(t, err, "a stale BLOCK_RELEASE preflight receipt must refuse the stabilization")
	require.True(t, errors.Is(err, ErrVersionConflict), "stale preflight error %v is not a version conflict", err)
}

// Gate path: the gate must independently recheck the same preflight receipt.
func TestReleaseGateRechecksBlockReleasePreflight(t *testing.T) {
	svc, actor, p, _, change := guardFixture(t, "guard-preflight-gate")
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	closeBlockingTasks(t, svc, actor, p.ID)
	markChangeSetStabilized(t, svc, actor, p.ID, change.ID)
	blockReleaseTask(t, svc, p, change.ID, "guard-block-release-gate", "guard-preflight-gate")

	svc.SetImpactPreflightChecker(fixturePreflightChecker{project: reloadProject(t, svc, actor, p.ID), current: true})
	require.NoError(t, svc.AssertRevisionStabilized(context.Background(), actor, p.ID),
		"a current preflight receipt must open the gate")

	svc.SetImpactPreflightChecker(fixturePreflightChecker{project: reloadProject(t, svc, actor, p.ID), current: false})
	requireGateError(t, svc.AssertRevisionStabilized(context.Background(), actor, p.ID), ErrVersionConflict, "")
}

// Gate path: a revision that advanced past every applied ChangeSet has no
// current stabilized ChangeSet at all, which is a different failure from an
// applied-but-unstabilized one.
func TestReleaseGateRejectsMissingStabilizedChangeSet(t *testing.T) {
	svc, actor, p, _, change := guardFixture(t, "guard-missing-change")
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	closeBlockingTasks(t, svc, actor, p.ID)
	markChangeSetStabilized(t, svc, actor, p.ID, change.ID)

	// Drift the revision forward without any ChangeSet targeting it: the
	// revision exists but nothing stabilized it.
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		current, err := svc.GetProject(context.Background(), actor, p.ID)
		if err != nil {
			return err
		}
		next := current
		next.CurrentContextRevision++
		next.ProjectVersion++
		return tx.UpdateProject(actor.TenantID, current, next)
	}))
	current := reloadProject(t, svc, actor, p.ID)
	require.Greater(t, current.CurrentContextRevision, p.CurrentContextRevision)

	err := svc.AssertRevisionStabilized(context.Background(), actor, p.ID)
	requireGateError(t, err, ErrRevisionNotStable, "no current stabilized ChangeSet")
}

// Gate path: a STALE task is reported as its own distinct failure and must never
// be read as a satisfied task nor folded into the generic "not DONE" branch.
func TestReleaseGateFailsStaleTaskIndependently(t *testing.T) {
	svc, actor, p, chapter, change := guardFixture(t, "guard-stale-task")
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	closeBlockingTasks(t, svc, actor, p.ID)
	markChangeSetStabilized(t, svc, actor, p.ID, change.ID)

	// A STALE task records an invalidated obligation, produced here through the
	// real store seam so the gate meets exactly that terminal, non-closure state.
	guardTask(t, svc, ImpactTask{ID: "guard-stale-task", ProjectID: p.ID, ChangeSetID: change.ID,
		Target:         ObjectVersionRef{Kind: "ChapterVersion", ID: chapter.ID, Version: "1"},
		TargetRevision: p.CurrentContextRevision, Action: "REVALIDATE_EVIDENCE", Status: "STALE",
		StaleReason: ImpactTaskStaleTargetReplaced})

	err := svc.AssertRevisionStabilized(context.Background(), actor, p.ID)
	requireGateError(t, err, ErrInvalidState, "guard-stale-task")
	require.True(t, strings.Contains(err.Error(), "STALE"), "gate error %q does not name the STALE state", err)
}
