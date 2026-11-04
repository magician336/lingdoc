package workspacecore

import "testing"

func TestAcademicCandidatePolicyEligibilityPreservesHistoricalValues(t *testing.T) {
	tests := []struct {
		name, policy, retraction, duplicate string
		want                                bool
	}{
		{"approved unknown", "approved", "unknown", "unique", true},
		{"legacy empty optional statuses", "approved", "", "", true},
		{"possible duplicate", "approved", "not_retracted", "possible_duplicate", true},
		{"missing approval", "", "unknown", "unique", false},
		{"out of policy", "out_of_policy", "unknown", "unique", false},
		{"retracted", "approved", "retracted", "unique", false},
		{"retraction notice", "approved", "retraction_notice", "unique", false},
		{"duplicate", "approved", "unknown", "duplicate", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := academicCandidatePolicyEligible(tc.policy, tc.retraction, tc.duplicate); got != tc.want {
				t.Fatalf("eligibility = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAcademicCrossrefProviderWireValues(t *testing.T) {
	if AcademicCrossrefProviderID != "academic:crossref" || AcademicCrossrefResultProvider != "crossref" {
		t.Fatal("task routing and result-source wire values must remain compatible")
	}
}
