package workspacecore

// This file is the single owner of the change-set status vocabulary.
//
// A change set is the record of one proposed change to a project: what the
// proposal changes, what it was assessed against, and where it ended up.
// Its status says which of those things it currently is. The vocabulary used
// to be spelled as bare string literals in nine production files, so adding a
// state meant finding every place that had to learn about it, and a place
// that was missed would silently keep accepting the old set of states.
//
// The status is the change set's own. Several other aggregates in this
// package have a status of their own — projects, rule sets, member records,
// template copies, spec fields, review anchors, impact tasks, runtime
// events — and some of them use the same words. Those vocabularies are not
// declared here and must not be folded in.

// The reachable change-set states.
const (
	// ChangeSetStatusDraft is a proposal that has not been assessed yet. It
	// is the state a change set returns to when its creator edits it, because
	// an edit invalidates the assessment that was made against the old text.
	ChangeSetStatusDraft = "draft"
	// ChangeSetStatusAssessed is a proposal whose impact has been assessed
	// against the current project state. It is the state an application or
	// rejection is allowed to act on.
	ChangeSetStatusAssessed = "assessed"
	// ChangeSetStatusApplied is a proposal that was applied to the project:
	// the formal basis and the affected chapters now carry the new values.
	ChangeSetStatusApplied = "applied"
	// ChangeSetStatusStabilized is an applied proposal whose impact tasks
	// have been closed and whose context revision has been frozen.
	ChangeSetStatusStabilized = "stabilized"
	// ChangeSetStatusRejected is a proposal a human refused. A rejection is a
	// decision, not a failure, and it is never reopened.
	ChangeSetStatusRejected = "rejected"
	// ChangeSetStatusStale is a proposal whose basis no longer holds: the
	// project moved on, the template copy changed, or a chapter version was
	// replaced. The proposal is not closed — it has to be reassessed.
	ChangeSetStatusStale = "stale"
	// ChangeSetStatusSatisfied is a proposal that turned out to need no work:
	// a reassessment found the project already carries the proposed values.
	// It is terminal and assessment-free.
	ChangeSetStatusSatisfied = "satisfied"
)

// ChangeSetStatuses lists every reachable change-set state.
//
// The list is the vocabulary: a state that is not in it does not exist, and a
// test pins it against the contract enum, the database constraint and the
// frontend type so the four cannot drift apart.
var ChangeSetStatuses = []string{
	ChangeSetStatusDraft,
	ChangeSetStatusAssessed,
	ChangeSetStatusApplied,
	ChangeSetStatusStabilized,
	ChangeSetStatusRejected,
	ChangeSetStatusStale,
	ChangeSetStatusSatisfied,
}

// ChangeSetClosedStatuses are the states that count as a closed loop for the
// change set: the change reached the project and the project was stabilized
// around it.
//
// STALE is intentionally absent: a stale proposal records that the basis is
// gone, not that the work was handled. SATISFIED is absent because it means
// there was never any work to do. REJECTED is absent because a rejection is a
// decision about the proposal, not a closed loop on the project.
var ChangeSetClosedStatuses = []string{
	ChangeSetStatusApplied,
	ChangeSetStatusStabilized,
}

// ChangeSetProposalStatuses are the states in which the change set is still a
// proposal: the formal basis has not been changed by it yet.
//
// A proposal in one of these states can be edited (which sends it back to
// draft) and can be assessed or applied.
var ChangeSetProposalStatuses = []string{
	ChangeSetStatusDraft,
	ChangeSetStatusAssessed,
}

// ChangeSetStatusMove is one change of a change set's status: the statuses
// the change set may be in when the change happens, and the status it ends in.
type ChangeSetStatusMove struct {
	From []string
	To   string
}

// ChangeSetStatusMoves lists the legal changes of a change-set status.
//
// The rule lives here so that a caller does not spell out the precondition at
// its own call site. The precondition for "apply" and the precondition for
// "reassess" differ even though both end at "assessed", so a single from/to
// table could not express them. A change that is not named here is not one the
// domain supports.
//
// A change set that is created starts in "draft" or "assessed"; creation is
// not a change of an existing change set and has no predecessor.
var ChangeSetStatusMoves = map[string]ChangeSetStatusMove{
	// A human applies the proposal: the formal basis and the affected
	// chapters change.
	"apply": {From: []string{ChangeSetStatusAssessed}, To: ChangeSetStatusApplied},
	// A human refuses the proposal.
	"reject": {From: []string{ChangeSetStatusAssessed}, To: ChangeSetStatusRejected},
	// The basis moved before the proposal could be applied, so it has to be
	// reassessed.
	"markStale": {From: []string{ChangeSetStatusAssessed}, To: ChangeSetStatusStale},
	// The proposal is assessed against the current project state.
	"assess": {From: []string{ChangeSetStatusDraft, ChangeSetStatusAssessed}, To: ChangeSetStatusAssessed},
	// A reassessment produces a fresh assessment of a proposal whose basis
	// has been restored.
	"reassess": {From: []string{ChangeSetStatusStale}, To: ChangeSetStatusAssessed},
	// A reassessment finds the project already carries the proposed values.
	"satisfy": {From: []string{ChangeSetStatusStale}, To: ChangeSetStatusSatisfied},
	// Every impact task of the proposal is closed and the context revision
	// is frozen.
	"stabilize": {From: []string{ChangeSetStatusApplied}, To: ChangeSetStatusStabilized},
	// The creator edits the proposal: the old assessment no longer describes
	// the new text, so the proposal returns to draft.
	"editDraft": {From: []string{ChangeSetStatusDraft, ChangeSetStatusAssessed}, To: ChangeSetStatusDraft},
}

// ChangeSetStatusCanMove reports whether the named change is legal from the
// given status.
//
// A change that is not named in ChangeSetStatusMoves is never legal: an
// unrecognised name must not be treated as a free pass.
func ChangeSetStatusCanMove(move, from string) bool {
	declared, ok := ChangeSetStatusMoves[move]
	if !ok {
		return false
	}
	for _, candidate := range declared.From {
		if candidate == from {
			return true
		}
	}
	return false
}

// ChangeSetStatusNeeds reports whether the change set is not yet in the state
// the named change produces.
//
// It answers the idempotence question rather than the legality question: a
// change set that is already in the target state does not need the change
// again. A move that is not named is never needed, because it has no target.
func ChangeSetStatusNeeds(move, status string) bool {
	declared, ok := ChangeSetStatusMoves[move]
	if !ok {
		return false
	}
	return status != declared.To
}

// IsChangeSetStatus reports whether the value is a reachable change-set state.
func IsChangeSetStatus(status string) bool {
	for _, candidate := range ChangeSetStatuses {
		if candidate == status {
			return true
		}
	}
	return false
}

// IsClosedChangeSetStatus reports whether the state counts as a closed loop
// for the change set.
func IsClosedChangeSetStatus(status string) bool {
	for _, candidate := range ChangeSetClosedStatuses {
		if candidate == status {
			return true
		}
	}
	return false
}
