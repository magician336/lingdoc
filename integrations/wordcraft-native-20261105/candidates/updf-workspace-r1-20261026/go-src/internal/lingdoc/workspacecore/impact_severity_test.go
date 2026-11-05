package workspacecore

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 严重度推导: the action-to-severity mapping is a product rule, so it is
// pinned here as a table instead of being implied by whichever fixture happens
// to need a task. The names in this file are the ones the rest of the suite
// refers to, e.g. impact_task_test.go points at
// TestImpactTaskSeverityFollowsTheProductRules for the mapping itself.

// TestImpactTaskSeverityFollowsTheProductRules pins the closed action-to-
// severity mapping, including the fail-closed handling of an action outside
// the known vocabulary.
//
// The last two cases are the ones that used to drift: the function comment
// promised that an unknown action derives as blocking while the default branch
// returned warning, and no test covered the mapping at all.
func TestImpactTaskSeverityFollowsTheProductRules(t *testing.T) {
	cases := []struct {
		action   string
		severity string
	}{
		// Blocking: leaving these unhandled can put unverified content into
		// delivery, so they can never be waived.
		{action: "BLOCK_RELEASE", severity: ImpactSeverityBlocking},
		{action: "REVIEW_REQUIRED", severity: ImpactSeverityBlocking},
		{action: "INVALIDATE_CONFIRMATION", severity: ImpactSeverityBlocking},
		// Info: mechanical work, no new assertion about content truth.
		{action: "RECOMPUTE", severity: ImpactSeverityInfo},
		{action: ImpactActionNoAction, severity: ImpactSeverityInfo},
		// Warning: weakens the basis behind a claim, dischargeable by ordinary
		// work.
		{action: "REVALIDATE_EVIDENCE", severity: ImpactSeverityWarning},
		{action: "RETRIEVE_CANDIDATES", severity: ImpactSeverityWarning},
		{action: "REGENERATE", severity: ImpactSeverityWarning},
		// Unknown actions fail closed: an undefined action must be handled,
		// not waved away.
		{action: "SOMETHING_NEW", severity: ImpactSeverityBlocking},
		{action: "", severity: ImpactSeverityBlocking},
	}
	for _, c := range cases {
		t.Run(c.action, func(t *testing.T) {
			require.Equal(t, c.severity, DeriveImpactSeverity(c.action),
				"action %q must derive as %q", c.action, c.severity)
		})
	}
}

// The mapping must stay in step with the action vocabulary the service
// accepts. A new action added to validImpactAction without a severity branch
// would silently derive as blocking, so this pins the two sets as equal
// rather than letting the drift show up as a mysterious blocker later.
func TestImpactSeverityMappingCoversTheWholeActionVocabulary(t *testing.T) {
	mapped := map[string]bool{}
	for _, action := range impactBlockingActions {
		mapped[action] = true
	}
	for _, action := range impactInfoActions {
		mapped[action] = true
	}
	for _, action := range impactWarningActions {
		mapped[action] = true
	}

	accepted := []string{"REVALIDATE_EVIDENCE", "RETRIEVE_CANDIDATES", "REVIEW_REQUIRED",
		"REGENERATE", "RECOMPUTE", "INVALIDATE_CONFIRMATION", "BLOCK_RELEASE", ImpactActionNoAction}
	for _, action := range accepted {
		require.True(t, mapped[action], "action %q has no severity branch", action)
		require.True(t, validImpactAction(action), "action %q must stay inside the accepted vocabulary", action)
	}
	require.Len(t, mapped, len(accepted), "the severity mapping knows actions outside the vocabulary")
}

// Every derived severity must land inside the closed severity domain, so no
// caller can store a value the rest of the module cannot interpret.
func TestDerivedImpactSeverityStaysInsideTheClosedVocabulary(t *testing.T) {
	actions := []string{"BLOCK_RELEASE", "REVIEW_REQUIRED", "INVALIDATE_CONFIRMATION",
		"RECOMPUTE", ImpactActionNoAction, "REVALIDATE_EVIDENCE", "RETRIEVE_CANDIDATES",
		"REGENERATE", "SOMETHING_NEW", ""}
	for _, action := range actions {
		severity := DeriveImpactSeverity(action)
		require.Contains(t, impactSeverities, severity,
			"action %q derived severity %q outside the closed vocabulary", action, severity)
	}
}

// Only a blocking impact blocks stabilization, and blocking is exactly the
// severity that cannot be dismissed: waiving permission is the negation of
// "blocks stabilization", so the two are one rule rather than two. An
// unrecognised severity blocks too, so a stored value the module cannot
// interpret is never read as permission to skip work.
func TestOnlyBlockingImpactBlocksAndCannotBeWaived(t *testing.T) {
	require.True(t, impactSeverityBlocks(DeriveImpactSeverity("BLOCK_RELEASE")))
	require.True(t, impactSeverityBlocks(DeriveImpactSeverity("REVIEW_REQUIRED")))
	require.True(t, impactSeverityBlocks(DeriveImpactSeverity("INVALIDATE_CONFIRMATION")))
	require.False(t, impactSeverityBlocks(DeriveImpactSeverity("RECOMPUTE")))
	require.False(t, impactSeverityBlocks(DeriveImpactSeverity(ImpactActionNoAction)))
	require.False(t, impactSeverityBlocks(DeriveImpactSeverity("REVALIDATE_EVIDENCE")))
	require.False(t, impactSeverityBlocks(DeriveImpactSeverity("RETRIEVE_CANDIDATES")))
	require.False(t, impactSeverityBlocks(DeriveImpactSeverity("REGENERATE")))

	// An unknown action derives as blocking, so it blocks and cannot be waived.
	require.Equal(t, ImpactSeverityBlocking, DeriveImpactSeverity("SOMETHING_NEW"))
	require.True(t, impactSeverityBlocks(DeriveImpactSeverity("SOMETHING_NEW")))

	// An unknown stored severity blocks as well, from the other direction.
	require.True(t, impactSeverityBlocks("advisory"))
	require.True(t, impactSeverityBlocks(""))
}
