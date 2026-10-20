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

// ChangeSetStatusTransition is one legal move between two change-set states.
type ChangeSetStatusTransition struct {
	From string
	To   string
}

// ChangeSetStatusTransitions lists the legal moves between change-set states.
//
// Every move here is one the production code performs. A move that is not
// listed is not a transition the domain supports, and a caller that attempts
// one is refused rather than silently producing a state nothing else
// understands.
var ChangeSetStatusTransitions = []ChangeSetStatusTransition{
	// The creator edits the proposal: the old assessment no longer describes
	// the new text, so the proposal returns to draft.
	{From: ChangeSetStatusAssessed, To: ChangeSetStatusDraft},
	// The proposal is assessed against the current project state.
	{From: ChangeSetStatusDraft, To: ChangeSetStatusAssessed},
	// A human applies the proposal: the formal basis and the affected
	// chapters change.
	{From: ChangeSetStatusAssessed, To: ChangeSetStatusApplied},
	// A human refuses the proposal.
	{From: ChangeSetStatusAssessed, To: ChangeSetStatusRejected},
	// The basis moved before the proposal could be applied, so it has to be
	// reassessed.
	{From: ChangeSetStatusAssessed, To: ChangeSetStatusStale},
	// A reassessment produces a fresh assessment of a proposal whose basis
	// has been restored.
	{From: ChangeSetStatusStale, To: ChangeSetStatusAssessed},
	// A reassessment finds the project already carries the proposed values.
	{From: ChangeSetStatusStale, To: ChangeSetStatusSatisfied},
	// Every impact task of the proposal is closed and the context revision
	// is frozen.
	{From: ChangeSetStatusApplied, To: ChangeSetStatusStabilized},
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

// ChangeSetStatusTransitionAllowed reports whether a change set may move from
// one status to another.
//
// Writing the same status again is allowed and is a no-op: several code paths
// re-write the current status while adding a reason or a timestamp, and
// refusing those would change behaviour without changing meaning.
func ChangeSetStatusTransitionAllowed(from, to string) bool {
	if from == to {
		return IsChangeSetStatus(from)
	}
	for _, transition := range ChangeSetStatusTransitions {
		if transition.From == from && transition.To == to {
			return true
		}
	}
	return false
}
