package workspacecore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
		// The severity is derived from the action, exactly as the production
		// path derives it. The fixture used to write "advisory", which is not
		// even inside the closed severity vocabulary and only looked like a
		// blocker because impactSeverityBlocks fails closed on it.
		task.Severity = DeriveImpactSeverity(task.Action)
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
	return receiptWithSources(t, svc, actor, p, []string{})
}

// receiptWithSources is genuineReceipt with a named source set. The source set
// is hashed the same way the verifier hashes it, so a receipt that names REAL
// sources verifies, and one that names an id nobody can resolve cannot.
func receiptWithSources(t *testing.T, svc *Service, actor Actor, p Project, sourceIDs []string) ImpactComputationReceipt {
	t.Helper()
	var receipt ImpactComputationReceipt
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		basis, err := ImpactComputationBasisHash(tx, p)
		if err != nil {
			return err
		}
		sourceHash, err := tx.(impactComputationSourceTransaction).ImpactSourceDigest(p.ID, sourceIDs)
		if err != nil {
			return err
		}
		receipt = ImpactComputationReceipt{ID: "guard-receipt", Version: 1, ProjectID: p.ID, Status: "blocked",
			ProjectVersion: p.ProjectVersion, ContextRevision: p.CurrentContextRevision,
			TemplateCopyVersion: p.TemplateCopyVersion, RulesetHash: p.TemplateCopy.RulesetHash,
			InputHash: "guard-evaluator-input", ComputationBasisHash: basis, SourceBasisHash: sourceHash,
			SourceIDs: sourceIDs, CheckedAt: time.Now().UTC(),
			Evaluations: []template.Evaluation{{RuleID: "required-fields", TargetRef: p.ID, TargetVersion: "1",
				RulesetHash: p.TemplateCopy.RulesetHash, EvaluatorVersion: template.EvaluatorVersion, Status: template.EvaluationIssue}}}
		return tx.(runtimeTransaction).InsertRuntimeRecord(actor.TenantID, p.ID, "preflight_check", receipt.ID, 1, receipt)
	}))
	return receipt
}

// unresolvableReceipt is the receipt the verifier MUST reject: it names a
// source id nobody can resolve. Its source hash was never computed, so the
// verification has to fail on the lookup itself, not on a hash mismatch.
func unresolvableReceipt(t *testing.T, svc *Service, actor Actor, p Project, sourceIDs []string) ImpactComputationReceipt {
	t.Helper()
	var receipt ImpactComputationReceipt
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		basis, err := ImpactComputationBasisHash(tx, p)
		if err != nil {
			return err
		}
		receipt = ImpactComputationReceipt{ID: "guard-receipt", Version: 1, ProjectID: p.ID, Status: "blocked",
			ProjectVersion: p.ProjectVersion, ContextRevision: p.CurrentContextRevision,
			TemplateCopyVersion: p.TemplateCopyVersion, RulesetHash: p.TemplateCopy.RulesetHash,
			InputHash: "guard-evaluator-input", ComputationBasisHash: basis, SourceBasisHash: "guard-unresolvable-hash",
			SourceIDs: sourceIDs, CheckedAt: time.Now().UTC(),
			Evaluations: []template.Evaluation{{RuleID: "required-fields", TargetRef: p.ID, TargetVersion: "1",
				RulesetHash: p.TemplateCopy.RulesetHash, EvaluatorVersion: template.EvaluatorVersion, Status: template.EvaluationIssue}}}
		return tx.(runtimeTransaction).InsertRuntimeRecord(actor.TenantID, p.ID, "preflight_check", receipt.ID, 1, receipt)
	}))
	return receipt
}

// guardSourceSchema creates the two application-side tables the source digest
// reads (knowledges and chunks) with the PRODUCTION DDL, taken from the
// migration that defines them.
//
// The LingDoc test fixture applies only the *_lingdoc_* migrations, so those
// application tables are absent from it. Hand-writing a copy of the DDL would
// let the fixture drift away from the schema the digest actually runs against,
// so the DDL is read from the migration file itself.
func guardSourceSchema(t *testing.T, svc *Service) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "sqlite", "000000_init.up.sql"))
	require.NoError(t, err, "read the production migration that defines knowledges and chunks")
	applied := 0
	for _, statement := range strings.Split(stripSQLLineComments(string(raw)), ";") {
		trimmed := strings.TrimSpace(statement)
		if !strings.HasPrefix(trimmed, "CREATE TABLE IF NOT EXISTS knowledges (") &&
			!strings.HasPrefix(trimmed, "CREATE TABLE IF NOT EXISTS chunks (") {
			continue
		}
		require.NoError(t, svc.testDB().Exec(trimmed).Error, "apply the production DDL")
		applied++
	}
	require.Equal(t, 2, applied, "both production tables must have been applied")
}

// guardSource seeds one REAL resolvable source for the project: the chunk, the
// knowledge it belongs to, and the project's own binding of that knowledge as
// an asset. ImpactSourceDigest resolves exactly these three rows per id, so a
// receipt that names this chunk carries a non-empty AND verifiable source set.
//
// A faked id cannot stand in for this: the digest lookup fails first, so the
// receipt check itself fails and the fall-through never becomes observable.
func guardSource(t *testing.T, svc *Service, actor Actor, p Project, chunkID, knowledgeID string) {
	t.Helper()
	guardSourceSchema(t, svc)
	db := svc.testDB()
	require.NoError(t, db.Exec("INSERT INTO knowledges(id,tenant_id,knowledge_base_id,type,title,source,parse_status,enable_status,file_hash,file_size,processed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)",
		knowledgeID, actor.TenantID, "guard-kb", "file", "守护 合成来源", "guard://source", "completed", "enabled",
		"guard-file-hash", 42, time.Now().UTC()).Error, "seed knowledge %s", knowledgeID)
	require.NoError(t, db.Exec("INSERT INTO chunks(id,tenant_id,knowledge_base_id,knowledge_id,content,source_content,content_revision,index_status,chunk_index,is_enabled,start_at,end_at,seq_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)",
		chunkID, actor.TenantID, "guard-kb", knowledgeID, "守护 合成来源正文", "守护 合成来源正文", 1, "ready",
		0, true, 0, 12, 990001).Error, "seed chunk %s", chunkID)
	require.NoError(t, db.Exec("INSERT INTO lingdoc_project_assets(id,tenant_id,project_id,knowledge_id,asset_revision,processing_state,created_by) VALUES(?,?,?,?,?,?,?)",
		"guard-asset-"+chunkID, actor.TenantID, p.ID, knowledgeID, 1, "ready", actor.UserID).Error,
		"seed project asset for knowledge %s", knowledgeID)
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
		TargetRevision: p.CurrentContextRevision, Action: "BLOCK_RELEASE", Severity: DeriveImpactSeverity("BLOCK_RELEASE"), Status: "DONE",
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

// guardConditions runs the shared stabilization evaluation directly, so a guard
// can pin the evaluation itself (what it stops at, what it collects) instead
// of only what the two public entries return. The public entries return the
// first gate error either way, so several differences are invisible there.
func guardConditions(t *testing.T, svc *Service, p Project, scope stabilizationScope) stabilizationConditions {
	t.Helper()
	var out stabilizationConditions
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		var err error
		out, err = svc.stabilizationConditions(tx, p, tx.(impactTransaction), scope)
		return err
	}))
	return out
}

// An empty-closure-evidence DONE task must not make the Owner path go quiet.
//
// The gate rejects such a task outright, but the Owner path has never had that
// condition: for a RECOMPUTE task it runs the receipt check, which fails on
// len(refs) != 1, and reports result_receipt_stale. A shared evaluation that
// short-circuits on the gate-only condition silently drops that blocker - the
// Owner would report a task that discharges nothing as if it were fine.
//
// Only reachable through the store seam (the production complete path forbids
// empty closure evidence), which is exactly why it needs a guard.
func TestStabilizationStillReportsReceiptTaskWithNoClosureEvidence(t *testing.T) {
	svc, actor, p, _, change := guardFixture(t, "guard-empty-closure")
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	closeBlockingTasks(t, svc, actor, p.ID)
	task := guardTask(t, svc, ImpactTask{ID: "guard-empty-closure-task", ProjectID: p.ID, ChangeSetID: change.ID,
		Target:         ObjectVersionRef{Kind: "ProjectSpec", ID: p.ID, Version: strconv.FormatInt(p.SpecRevision, 10)},
		TargetRevision: p.CurrentContextRevision, Action: "RECOMPUTE", Status: "DONE", Disposition: "completed"})

	result, err := runStabilization(t, svc, actor, p.ID, change.ID, "guard-empty-closure-stabilize")
	require.NoError(t, err)
	require.Equal(t, "BLOCKED", result.Status, "a DONE receipt task with no evidence must still block: %+v", result.Blockers)
	blocker := blockerFor(t, result, "result_receipt_stale")
	require.Equal(t, task.ID, blocker.TargetID, "the receipt check, not silence, must explain it: %+v", blocker)
}

// Fail-fast fidelity. AssertRevisionStabilized used to return from INSIDE its
// task loop, so conditions after the failing task were never evaluated at all.
// The shared evaluation collects conditions and lets the consumer pick, which
// is observationally identical for the gate's own return value but not for the
// evaluation itself: without fail-fast a later infrastructure error could
// displace the task error the gate returns, and the gate would read objects it
// used not to read.
//
// No assertion on AssertRevisionStabilized can separate the two - it returns
// the first gate error either way - so this pins the evaluation contract
// directly: GateFailFast stops at the failing task, the Owner-shaped scope
// keeps evaluating and sees the later route condition too.
func TestStabilizationConditionsGateFailFastStopsAtFirstFailingTask(t *testing.T) {
	svc, actor, p, chapter, change := guardFixture(t, "guard-failfast")
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	closeBlockingTasks(t, svc, actor, p.ID)
	// One open blocking task: the very first condition of the loop rejects.
	// REVIEW_REQUIRED derives as blocking, so this is a state the production
	// path can actually produce. (RECOMPUTE derives as info, and pinning
	// "RECOMPUTE + blocking" in a fixture would keep asserting a scenario the
	// production path can no longer produce.)
	guardTask(t, svc, ImpactTask{ID: "guard-failfast-task", ProjectID: p.ID, ChangeSetID: change.ID,
		Target: ObjectVersionRef{Kind: "Chapter", ID: chapter.ID, Version: "1"}, TargetRevision: p.CurrentContextRevision,
		Action: "REVIEW_REQUIRED", Status: "open", Severity: DeriveImpactSeverity("REVIEW_REQUIRED")})
	// One required route that is not current: the condition evaluated right after.
	node := appendRouteNode(t, svc, p, "guard-failfast-route", true, "planned")

	evaluate := func(t *testing.T, scope stabilizationScope) stabilizationConditions {
		t.Helper()
		return guardConditions(t, svc, p, scope)
	}

	fast := evaluate(t, stabilizationScope{GateFailFast: true})
	require.Len(t, fast.Failures, 1, "a fail-fast gate must not evaluate past the failing task: %+v", fast.Failures)
	require.Equal(t, "impact_task_open", fast.Failures[0].Code)
	requireGateError(t, fast.GateError(), ErrInvalidState, "guard-failfast-task")

	full := evaluate(t, stabilizationScope{})
	codes := []string{}
	for _, blocker := range full.OwnerBlockers() {
		codes = append(codes, blocker.Code)
	}
	require.Equal(t, []string{"impact_task_open", "required_route_not_current"}, codes,
		"without fail-fast the later route condition must also be evaluated")
	require.Equal(t, node.ID, blockerFor(t, StabilizationResult{Blockers: full.OwnerBlockers()}, "required_route_not_current").TargetID)
}

// The gate's fall-through for result tasks is observable only through the
// source set it re-authorizes. A computation receipt can only carry a
// NON-EMPTY source set if those sources are real chunks (ImpactSourceDigest
// resolves every id), so the guard needs a source-backed receipt.
//
// The two tests below build exactly that, and they are the pair that makes the
// fall-through observable at all:
//
//   - with a REAL source, the receipt verifies, the gate falls through into the
//     closure-evidence loop and the receipt's own SourceIDs join the set the
//     gate re-authorizes, while the Owner-shaped scope stops at the receipt
//     check and contributes nothing;
//   - with an UNRESOLVABLE source, the receipt check fails first, so the same
//     fall-through contributes nothing. That is why a faked id could never
//     stand in for a real source here.
//
// The RETRIEVE_CANDIDATES half of the same fall-through is still NOT BUILT: it
// needs a source-backed research run, which #209 already recorded the cost of.
func TestStabilizationGateFallThroughAddsResultTaskSources(t *testing.T) {
	svc, actor, p, _, change := guardFixture(t, "guard-fallthrough")
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	closeBlockingTasks(t, svc, actor, p.ID)

	const chunkID = "guard-fallthrough-chunk"
	const knowledgeID = "guard-fallthrough-knowledge"
	guardSource(t, svc, actor, p, chunkID, knowledgeID)

	receipt := receiptWithSources(t, svc, actor, p, []string{chunkID})
	guardTask(t, svc, ImpactTask{ID: "guard-fallthrough-task", ProjectID: p.ID, ChangeSetID: change.ID,
		Target:         ObjectVersionRef{Kind: "ProjectSpec", ID: p.ID, Version: strconv.FormatInt(p.SpecRevision, 10)},
		TargetRevision: p.CurrentContextRevision, Action: "RECOMPUTE", Status: "DONE", Disposition: "completed",
		ClosureEvidence: []ObjectVersionRef{recomputeReceiptRef(receipt)}})

	gate := guardConditions(t, svc, p, stabilizationScope{GateFailFast: true, ClosureSourcesOfResultTasks: true})
	require.Empty(t, gate.Failures, "the gate scope must reach the result task: %+v", gate.Failures)
	require.Contains(t, gate.ClosureSourceIDs, chunkID,
		"a verified result task must add its own sources to the set the gate re-authorizes: %+v", gate.ClosureSourceIDs)

	owner := guardConditions(t, svc, p, stabilizationScope{})
	require.NotContains(t, owner.ClosureSourceIDs, chunkID,
		"the Owner-shaped scope stops at the receipt check and must not collect the sources: %+v", owner.ClosureSourceIDs)
}

// The counterpart: a result task whose receipt names a source nobody can
// resolve is stopped by the receipt check itself, so it contributes no source
// to the set the gate re-authorizes. Without this case the fall-through guard
// above could be satisfied by a receipt that never verified.
func TestStabilizationGateFallThroughNeedsResolvableResultSources(t *testing.T) {
	svc, actor, p, _, change := guardFixture(t, "guard-fallthrough-unresolvable")
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	closeBlockingTasks(t, svc, actor, p.ID)

	const fakeChunkID = "guard-fallthrough-fake-chunk"
	receipt := unresolvableReceipt(t, svc, actor, p, []string{fakeChunkID})
	guardTask(t, svc, ImpactTask{ID: "guard-fallthrough-unresolvable-task", ProjectID: p.ID, ChangeSetID: change.ID,
		Target:         ObjectVersionRef{Kind: "ProjectSpec", ID: p.ID, Version: strconv.FormatInt(p.SpecRevision, 10)},
		TargetRevision: p.CurrentContextRevision, Action: "RECOMPUTE", Status: "DONE", Disposition: "completed",
		ClosureEvidence: []ObjectVersionRef{recomputeReceiptRef(receipt)}})

	gate := guardConditions(t, svc, p, stabilizationScope{GateFailFast: true, ClosureSourcesOfResultTasks: true})
	require.NotContains(t, gate.ClosureSourceIDs, fakeChunkID,
		"an unverifiable receipt must contribute no source: %+v", gate.ClosureSourceIDs)
	require.Len(t, gate.Failures, 1, "the receipt check must be the one thing that failed: %+v", gate.Failures)
	require.Equal(t, "result_receipt_stale", gate.Failures[0].Code)
	require.Equal(t, "guard-fallthrough-unresolvable-task", gate.Failures[0].TargetID)
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
	second := decodeAgentJSON[ChangeSet](t, raw, nil)
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
	return guardClaimOnBasis(t, svc, p, id, p.SpecRevision)
}

// guardClaimOnBasis stores a ResearchClaim recorded against a SPECIFIC spec
// revision, so a guard can separate "the decision was a review" from "the claim
// the review was recorded against is still the current one". Passing anything
// other than the current spec revision models a claim whose basis has been
// overtaken by a later spec revision.
func guardClaimOnBasis(t *testing.T, svc *Service, p Project, id string, specRevision int64) ObjectVersionRef {
	t.Helper()
	claim := ResearchClaim{ID: id, ProjectID: p.ID, Version: 1, Text: "守护 合成研究主张", Scope: "守护范围",
		Status: "active", Basis: VersionBasis{ProjectVersion: p.ProjectVersion, SpecRevision: specRevision,
			ContextRevision: p.CurrentContextRevision}, Reason: "守护 合成主张", CreatedBy: "owner",
		CreatedAt: time.Now().UTC()}
	require.NoError(t, svc.repository.Transaction(context.Background(), TransactionOptions{}, func(tx Transaction) error {
		return storeStoredObject(tx, StoredObject{}, p.ID, ResearchClaimKind, id, 1, claim)
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

// The two guards above cannot tell the human-review escape apart from a mere
// "the decision was not a review": both reach the same closure_stale branch
// through the HumanDecision term, so deleting the review-currentness check
// entirely would leave them green. These two isolate the review itself: the
// decision IS a review, and only the claim it was recorded against is no longer
// current. Nothing else in the condition can fire, so the check is the only
// thing standing between this state and stabilization.

// Owner path: a recorded human review of a claim whose basis has been overtaken
// is not a review of anything current, and must still block.
func TestStabilizationRejectsReviewedClaimOnStaleBasis(t *testing.T) {
	svc, actor, p, _, change := guardFixture(t, "guard-claim-stale-owner")
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	closeBlockingTasks(t, svc, actor, p.ID)

	staleBasis := p.SpecRevision - 1
	if staleBasis < 0 {
		staleBasis = 0
	}
	ref := guardClaimOnBasis(t, svc, p, "guard-claim-stale-owner-claim", staleBasis)
	guardTask(t, svc, ImpactTask{ID: "guard-claim-stale-owner", ProjectID: p.ID, ChangeSetID: change.ID, Target: ref,
		TargetRevision: p.CurrentContextRevision, Action: "REVIEW_REQUIRED", Status: "DONE", Disposition: "completed",
		HumanDecision: "reviewed", ClosureEvidence: []ObjectVersionRef{ref}})

	blocked, err := runStabilization(t, svc, actor, p.ID, change.ID, "guard-claim-stale-owner")
	require.NoError(t, err)
	require.Equal(t, "BLOCKED", blocked.Status, "a review of an overtaken claim must block: %+v", blocked.Blockers)
	blocker := blockerFor(t, blocked, "closure_stale")
	require.Equal(t, "guard-claim-stale-owner", blocker.TargetID)
}

// Gate path: the same state must refuse the gate.
func TestReleaseGateRejectsReviewedClaimOnStaleBasis(t *testing.T) {
	svc, actor, p, _, change := guardFixture(t, "guard-claim-stale-gate")
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	closeBlockingTasks(t, svc, actor, p.ID)
	markChangeSetStabilized(t, svc, actor, p.ID, change.ID)

	staleBasis := p.SpecRevision - 1
	if staleBasis < 0 {
		staleBasis = 0
	}
	ref := guardClaimOnBasis(t, svc, p, "guard-claim-stale-gate-claim", staleBasis)
	guardTask(t, svc, ImpactTask{ID: "guard-claim-stale-gate", ProjectID: p.ID, ChangeSetID: change.ID, Target: ref,
		TargetRevision: p.CurrentContextRevision, Action: "REVIEW_REQUIRED", Status: "DONE", Disposition: "completed",
		HumanDecision: "reviewed", ClosureEvidence: []ObjectVersionRef{ref}})

	requireGateError(t, svc.AssertRevisionStabilized(context.Background(), actor, p.ID), ErrInvalidState, "")
}

// Gate path, accepted direction: a recorded human review of a CURRENT claim is
// the one state in which the review escape must open the gate. Without this case
// the gate-side guard only pins the rejection, so deleting or inverting the
// review-currentness check would leave the gate side green.
func TestReleaseGateAcceptsReviewedCurrentClaim(t *testing.T) {
	svc, actor, p, _, change := guardFixture(t, "guard-claim-gate-current")
	svc.SetImpactRuleChecker(cleanRuleChecker{})
	closeBlockingTasks(t, svc, actor, p.ID)
	markChangeSetStabilized(t, svc, actor, p.ID, change.ID)
	ref := guardClaim(t, svc, p, "guard-claim-gate-current-claim")
	guardTask(t, svc, ImpactTask{ID: "guard-claim-gate-current", ProjectID: p.ID, ChangeSetID: change.ID, Target: ref,
		TargetRevision: p.CurrentContextRevision, Action: "REVIEW_REQUIRED", Status: "DONE", Disposition: "completed",
		HumanDecision: "reviewed", ClosureEvidence: []ObjectVersionRef{ref}})

	require.NoError(t, svc.AssertRevisionStabilized(context.Background(), actor, p.ID),
		"a recorded human review of a current claim must open the gate")
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
