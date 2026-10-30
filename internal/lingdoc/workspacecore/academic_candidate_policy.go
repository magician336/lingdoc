package workspacecore

// academicCandidatePolicyEligible contains only checks shared by creation,
// confirmation, replay and history. Each caller retains its own stage-specific
// identity, version, acquisition and state checks.
func academicCandidatePolicyEligible(policyStatus, retractionStatus, duplicateStatus string) bool {
	return policyStatus == "approved" && retractionStatus != "retracted" &&
		retractionStatus != "retraction_notice" && duplicateStatus != "duplicate"
}
