package workspacecore

import (
	"fmt"
	"slices"
)

// Single ownership of "what currently makes a revision not stabilized".
//
// Before #210 the Owner stabilization path (StabilizeRevision) and the read-only
// delivery gate (AssertRevisionStabilized) each carried their own loop over the
// impact tasks. Ten conditions were evaluated twice, so the two entries could
// drift apart silently. This file is the one implementation: a side-effect-free
// evaluation that reads current domain truth and returns every unsatisfied
// condition, plus the inputs the two entries need for their own post-checks.
//
// Side-effect freedom is a hard property here, because one entry writes (Owner
// audit, ChangeSet status advance, STALE task transitions) and the other must
// never write at all. The evaluation therefore only reads. The single write the
// Owner path performs *as part of evaluating a condition* - recording a STALE
// transition for a task whose closure evidence has been replaced - is returned
// as data (StaleTransitions) and executed by the Owner entry after the
// evaluation, inside the same transaction.
//
// Every condition carries BOTH renderings of itself:
//
//   - the Owner rendering (blocker code / target / reason, or a STALE report),
//     reproduced verbatim; and
//   - the read-only gate rendering (the error the gate returns verbatim).
//
// A rendering an entry does not use stays empty. That is what lets the two
// entries share the knowledge while keeping their different, adjudicated
// external shapes byte-for-byte identical to before.

// stabilizationScope is the only thing that differs between the two entries.
// Each field below is an adjudicated asymmetry, not an accident; see the
// comment on the field and in the report accompanying #210.
type stabilizationScope struct {
	// ChangeID scopes the task scan to one change set (Owner stabilization).
	// An empty ChangeID scans every task of the current context revision, which
	// is what the read-only gate asks: "is this revision as a whole released?".
	ChangeID string

	// RecheckImpactedObjects enables the impacted-object confirmation re-read
	// (impactConfirmationBlockers).
	//
	// ADJUDICATION #3 (deliberate difference, not merged). The Owner path runs
	// this re-read because a task judged STALE is neither a blocker nor closure
	// (#200), which would otherwise leave its impacted object with no obligation
	// at all. The gate does NOT run it. Merging it would change gate behaviour:
	// projects that are exportable today could start being rejected. The gate
	// also has no change-set scope to read a persisted assessment against. This
	// is a real semantic gap between the two entries, it needs a product
	// decision, and it is NOT closed by this refactor.
	RecheckImpactedObjects bool

	// GateFailFast makes the evaluation stop at the first condition the gate
	// would reject on, instead of evaluating the remaining conditions and
	// letting the consumer pick the first one afterwards.
	//
	// ADJUDICATION (fail-fast fidelity, not one of the four single-sided
	// conditions). The gate used to return from inside its loop, so conditions
	// after the failing one were never evaluated. Collecting them all and
	// selecting afterwards would be observationally different in two ways: the
	// gate would read objects it used not to read, and a later infrastructure
	// error (RouteStabilizationBlockers, the rules checker) could displace the
	// task error the gate actually returns today. The Owner path needs the whole
	// list and leaves this false.
	GateFailFast bool

	// ClosureSourcesOfResultTasks makes a verified RECOMPUTE/RETRIEVE_CANDIDATES
	// task fall through into the closure-evidence loop instead of stopping after
	// the receipt check.
	//
	// ADJUDICATION (pre-existing asymmetry, preserved). The Owner loop has always
	// stopped after the receipt check; the gate loop has always fallen through.
	// The fall-through is not dead code: currentImpactObject resolves a
	// ComputationReceipt to the receipt's own SourceIDs, and the gate adds exactly
	// those to the set it re-authorizes in checkSources. A single shared condition
	// would have to pick one of the two, and picking the Owner's would silently
	// shrink the gate's authorization set - a weakening disguised as a refactor.
	ClosureSourcesOfResultTasks bool
}

// record appends one evaluated condition and reports whether a fail-fast
// consumer must stop evaluating now.
func (c *stabilizationConditions) record(f stabilizationFailure, scope stabilizationScope) bool {
	c.Failures = append(c.Failures, f)
	return scope.GateFailFast && f.GateError != nil
}

// stabilizationFailure is one evaluated condition that is currently unsatisfied.
type stabilizationFailure struct {
	// Owner rendering. An empty Code means the Owner path does not express this
	// condition as a blocker at all (the gate-only conditions, and the STALE
	// reports, which go to StaleTaskIDs instead).
	Code     string
	TargetID string
	Reason   string

	// Gate rendering: the error the read-only gate returns for this condition,
	// verbatim. Nil means the gate does not evaluate this condition.
	GateError error

	// StaleTransition marks a condition the Owner path discharges by writing a
	// STALE transition on the task instead of reporting a blocker. The
	// evaluation itself never writes.
	StaleTransition bool

	// StaleReport marks a condition the Owner path reports in StaleTaskIDs.
	StaleReport bool

	// task carries the ImpactTask for the STALE renderings, so the Owner entry
	// does not have to re-derive which record the transition belongs to.
	task ImpactTask
}

// stabilizationConditions is the complete result of the evaluation.
type stabilizationConditions struct {
	Project  Project
	Failures []stabilizationFailure

	// PreflightIDs are the BLOCK_RELEASE closure evidence ids that must be
	// re-verified against a live preflight basis.
	PreflightIDs []string

	// ClosureSourceIDs are the source ids carried by the CURRENT closure
	// evidence objects, which is the source set each entry re-authorizes.
	ClosureSourceIDs []string

	// StaleTransitions lists, in evaluation order, the tasks the Owner entry must
	// record as STALE. Empty for the read-only gate, which writes nothing.
	StaleTransitions []ImpactTask
}

// OwnerBlockers renders the Owner blocker list. Blocker codes are reproduced
// verbatim; a condition without a code is not a blocker on this path.
func (c stabilizationConditions) OwnerBlockers() []ImpactBlocker {
	out := []ImpactBlocker{}
	for _, failure := range c.Failures {
		if failure.Code == "" {
			continue
		}
		out = append(out, ImpactBlocker{Code: failure.Code, TargetID: failure.TargetID, Reason: failure.Reason})
	}
	return out
}

// OwnerStaleTaskIDs renders the STALE report. Entries are kept in evaluation
// order and are NOT de-duplicated: one unreadable closure ref yields one report,
// exactly as the Owner loop did.
func (c stabilizationConditions) OwnerStaleTaskIDs() []string {
	out := []string{}
	for _, failure := range c.Failures {
		if !failure.StaleReport {
			continue
		}
		out = append(out, failure.task.ID)
	}
	return out
}

// GateError returns the first condition the read-only gate rejects on, or nil
// when the revision is releasable. The gate is fail-fast, so evaluation order is
// part of its observable behaviour.
func (c stabilizationConditions) GateError() error {
	for _, failure := range c.Failures {
		if failure.GateError != nil {
			return failure.GateError
		}
	}
	return nil
}

// stabilizationConditions evaluates every stabilization condition against
// current domain truth and writes nothing.
//
// Evaluation order is part of the contract: it reproduces the Owner path's
// historical order (tasks, impacted objects, routes, template, rules), which is
// also the order in which the read-only gate used to fail fast.
func (s *Service) stabilizationConditions(tx Transaction, p Project, store impactTransaction, scope stabilizationScope) (stabilizationConditions, error) {
	result := stabilizationConditions{Project: p, Failures: []stabilizationFailure{},
		PreflightIDs: []string{}, ClosureSourceIDs: []string{}, StaleTransitions: []ImpactTask{}}
	tasks, err := store.ImpactTasks(p.ID)
	if err != nil {
		return stabilizationConditions{}, err
	}
	// Tasks the Owner loop has already turned into STALE transitions. The
	// impacted-object re-read below must not count them as closure, because by
	// the time it runs their record no longer says DONE. Passing the set keeps
	// the evaluation pure while preserving that ordering dependency.
	invalidated := map[string]bool{}
	for _, task := range tasks {
		if task.TargetRevision != p.CurrentContextRevision || (scope.ChangeID != "" && task.ChangeSetID != scope.ChangeID) {
			continue
		}
		// A STALE task records an invalidated obligation. It must not block (the
		// basis it was raised against no longer exists, so no actor can ever
		// discharge it) and it must not count as closure. The Owner path reports
		// it explicitly instead of dropping it; the gate has no structured
		// result to report into and rejects on it as its own distinct failure, so
		// a caller can never read it as a satisfied task.
		if task.Status == "STALE" {
			if result.record(stabilizationFailure{task: task, StaleReport: true,
				GateError: fmt.Errorf("%w: current impact task %s is STALE (%s)", ErrInvalidState, task.ID, task.StaleReason)}, scope) {
				return result, nil
			}
			continue
		}
		if task.Severity == "blocking" && task.Status != "DONE" {
			// ADJUDICATION (not one of the four single-sided conditions, but the
			// same shape): the Owner path reports this as a blocker in a 200
			// response, the gate rejects with an error naming the task. Both
			// renderings are kept verbatim rather than unified.
			if result.record(stabilizationFailure{Code: "impact_task_open", TargetID: task.ID, Reason: task.Action,
				GateError: fmt.Errorf("%w: current impact task %s remains %s", ErrInvalidState, task.ID, task.Status)}, scope) {
				return result, nil
			}
			continue
		}
		if task.Status != "DONE" {
			continue
		}
		// Gate-only condition: a DONE task with no closure evidence discharges
		// nothing. The Owner path has never expressed this and gains nothing from
		// it, so it stays gate-only.
		if len(task.ClosureEvidence) == 0 {
			if result.record(stabilizationFailure{GateError: ErrInvalidState}, scope) {
				return result, nil
			}
			continue
		}
		if task.Action == "BLOCK_RELEASE" {
			for _, ref := range task.ClosureEvidence {
				result.PreflightIDs = append(result.PreflightIDs, ref.ID)
			}
			continue
		}
		if task.Action == "RECOMPUTE" || task.Action == "RETRIEVE_CANDIDATES" {
			if e := verifyImpactActionResult(tx, p, task, task.ClosureEvidence); e != nil {
				if result.record(stabilizationFailure{Code: "result_receipt_stale", TargetID: task.ID,
					Reason: "服务端结果回执未完成或已经过期", GateError: e}, scope) {
					return result, nil
				}
				continue
			}
			// Pre-existing asymmetry, preserved deliberately (see
			// stabilizationScope.ClosureSourcesOfResultTasks): the Owner path has
			// always stopped here, the gate has always fallen through into the
			// closure-evidence loop below. Falling through is what puts a
			// computation receipt's own SourceIDs into the set the gate
			// re-authorizes, so collapsing it here would silently shrink the
			// gate's authorization set.
			if !scope.ClosureSourcesOfResultTasks {
				continue
			}
		}
		for _, ref := range task.ClosureEvidence {
			state, e := currentImpactObject(tx, p, ref)
			if e != nil {
				// Re-checked before closure: the version this result was produced
				// against is no longer readable, so the result no longer holds.
				// The Owner path records the invalidation as an explicit STALE
				// transition (a write, therefore returned as data); the gate just
				// propagates the read failure.
				result.StaleTransitions = append(result.StaleTransitions, task)
				invalidated[task.ID] = true
				if result.record(stabilizationFailure{task: task, StaleTransition: true, StaleReport: true, GateError: e}, scope) {
					return result, nil
				}
				continue
			}
			result.ClosureSourceIDs = append(result.ClosureSourceIDs, state.SourceIDs...)
			if slices.Contains([]string{"REVIEW_REQUIRED", "INVALIDATE_CONFIRMATION", "REVALIDATE_EVIDENCE"}, task.Action) && !state.Confirmed && (task.Action != "REVIEW_REQUIRED" || task.Target.Kind != "ResearchClaim" || task.HumanDecision != "reviewed" || !researchClaimReviewCurrent(tx, p, ref)) {
				if result.record(stabilizationFailure{Code: "closure_stale", TargetID: task.ID,
					Reason: "闭环证据已变化，需重新处理", GateError: ErrInvalidState}, scope) {
					return result, nil
				}
			}
		}
	}
	if scope.RecheckImpactedObjects {
		for _, blocker := range impactConfirmationBlockers(tx, p, store, scope.ChangeID, invalidated) {
			result.Failures = append(result.Failures, stabilizationFailure{Code: blocker.Code, TargetID: blocker.TargetID, Reason: blocker.Reason})
		}
	}
	routeBlockers, err := RouteStabilizationBlockers(tx, p)
	if err != nil {
		return stabilizationConditions{}, err
	}
	for _, blocker := range routeBlockers {
		if result.record(stabilizationFailure{Code: blocker.Code, TargetID: blocker.TargetID, Reason: blocker.Reason, GateError: ErrInvalidState}, scope) {
			return result, nil
		}
	}
	// ADJUDICATION #1 (merged, presentation preserved). The template binding
	// condition is evaluated once, here. The Owner path reports it as the
	// machine-readable `template_unbound` blocker; the gate has always rejected
	// an unbound copy with a bare ErrInvalidState and keeps doing so, because
	// its preflight basis cannot even be computed without a bound copy. Giving
	// the gate the machine-readable code instead would change its error identity,
	// which #210 explicitly forbids, so the collapsed rendering stays.
	if p.TemplateCopy == nil || p.TemplateCopy.Status != TemplateCopyBound {
		if result.record(stabilizationFailure{Code: "template_unbound", Reason: "当前模板未绑定", GateError: ErrInvalidState}, scope) {
			return result, nil
		}
	}
	// ADJUDICATION #2 (merged, presentation preserved). One availability check,
	// two return types: the Owner path reports a blocker inside a 200 response,
	// the gate rejects with ErrImpactObjectUnavailable. Unifying the return type
	// would either make the gate accept a governance gap or make stabilization
	// fail instead of reporting, so both renderings are kept.
	if s.impactRules == nil {
		if result.record(stabilizationFailure{Code: "rules_checker_unavailable", Reason: "缺少当前规则检查器", GateError: ErrImpactObjectUnavailable}, scope) {
			return result, nil
		}
	} else {
		blockers, err := s.impactRules.Check(tx, p)
		if err != nil {
			return stabilizationConditions{}, err
		}
		for _, blocker := range blockers {
			if result.record(stabilizationFailure{Code: blocker.Code, TargetID: blocker.TargetID, Reason: blocker.Reason,
				GateError: fmt.Errorf("%w: current rules block release", ErrInvalidState)}, scope) {
				return result, nil
			}
		}
	}
	return result, nil
}
