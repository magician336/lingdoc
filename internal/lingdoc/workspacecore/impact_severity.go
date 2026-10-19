package workspacecore

import "slices"

// Impact severity vocabulary.
//
// Severity describes the CONSEQUENCE of leaving an impact unhandled. It does
// not describe urgency, ownership, or how much work the impact represents, and
// it is deliberately independent of ResponsibilityDomain: severity answers
// "how bad", the domain answers "whose problem".
//
// The vocabulary is shared with ValidationIssue severity. ImpactTask.Severity
// already stored the literal "blocking" before this module existed; that
// literal was borrowed from the template rule severity constants rather than
// invented locally, so unifying on one word set is the smaller change.
//
// Why this lives behind functions instead of three string comparisons
// ---------------------------------------------------------------------
// The rule "a blocking impact can never be waived" used to be expressed as
// `task.Severity == "blocking"` in two unrelated modules (the action handler
// and the stabilization evaluator) plus one hardcoded literal at task
// creation. Three places, no shared constant, and no shared reason.
//
// That coupling carried a live hazard. BLOCK_RELEASE derives as blocking
// precisely so it cannot be waved away, but nothing about the old code tied
// BLOCK_RELEASE to blocking - it just happened that every task was blocking.
// Had BLOCK_RELEASE ever become waivable, a DISMISSED BLOCK_RELEASE task would
// have stopped blocking the release gate entirely and silently, because the
// BLOCK_RELEASE preflight recheck only inspects DONE tasks. The test that
// pins that guarantee would still have passed: its fixture hardcodes
// severity, so it would have kept asserting against a scenario the production
// path could no longer produce.
//
// Writing BLOCK_RELEASE's severity as a constant inside DeriveImpactSeverity,
// and making waiver permission the negation of "blocks stabilization", turns
// that hazard into an invariant instead of a coincidence. There is no call
// site left that could drift.
const (
	// ImpactSeverityBlocking marks an impact whose unresolved state must
	// prevent stabilization. Blocking impacts can only be discharged by
	// producing real closure evidence; they can never be waived.
	ImpactSeverityBlocking = "blocking"
	// ImpactSeverityWarning marks an impact that weakens the evidence or
	// currency behind a claim without invalidating a confirmation. It does not
	// block stabilization on its own, but it still requires disposition: either
	// closure evidence or an explicit, reasoned acceptance.
	ImpactSeverityWarning = "warning"
	// ImpactSeverityInfo marks an impact whose resolution is mechanical - it
	// asks for a recomputed derivative, not for a new assertion about content
	// truth. It does not block stabilization.
	ImpactSeverityInfo = "info"
)

// impactSeverities is the closed severity domain. A task whose severity falls
// outside it is treated as blocking: an unrecognised severity must not be
// read as permission to skip work.
var impactSeverities = []string{ImpactSeverityBlocking, ImpactSeverityWarning, ImpactSeverityInfo}

// impactBlockingActions derive ImpactSeverityBlocking.
//
// A block-release impact is blocking by definition - its whole meaning is
// "this must not reach export". The other two are blocking because they
// invalidate something a human asserted: REVIEW_REQUIRED covers content that
// has not been looked at, INVALIDATE_CONFIRMATION covers a confirmation that
// no longer applies to the current version. Both can let unverified content
// reach delivery, which is the exact failure stabilization exists to prevent.
var impactBlockingActions = []string{"BLOCK_RELEASE", "REVIEW_REQUIRED", "INVALIDATE_CONFIRMATION"}

// DeriveImpactSeverity maps an impact action to the severity of leaving it
// unhandled.
//
// The mapping is deliberately closed and stated here rather than at the call
// site: task creation is the only producer, so this is the single place the
// product's "how bad does an unhandled impact get" judgement is expressed.
//
// An action outside the known vocabulary derives as blocking. A future action
// invented by a caller must be handled rather than silently waved away, and
// the cost of that choice lands on whoever adds the action instead of on
// every downstream consumer.
func DeriveImpactSeverity(action string) string {
	switch {
	case slices.Contains(impactBlockingActions, action):
		return ImpactSeverityBlocking
	case action == "RECOMPUTE":
		// Recomputing a derivative asks for a calculation, not for a new claim
		// about content truth. Its result is a receipt the gate can re-check,
		// not an assertion a human has to stand behind.
		return ImpactSeverityInfo
	default:
		// REVALIDATE_EVIDENCE, RETRIEVE_CANDIDATES, REGENERATE: these weaken
		// the basis behind a claim or refresh a candidate, and each can be
		// discharged by a responsible person's ordinary work.
		return ImpactSeverityWarning
	}
}

// impactSeverityBlocks reports whether an impact of this severity must keep
// the project from reaching STABILIZED while it stays unhandled.
//
// This is the only place the "unhandled blocking impact blocks" rule is
// written. Waiver permission is its negation rather than a second rule,
// because the product treats them as two faces of one fact: an impact you
// may waive is an impact that never blocked. See the Waiver entry in the
// glossary, which makes the same statement for ValidationIssue.
//
// An unrecognised severity blocks. Fail closed.
func impactSeverityBlocks(severity string) bool {
	if !slices.Contains(impactSeverities, severity) {
		return true
	}
	return severity == ImpactSeverityBlocking
}
