package candidateadoption

// Validity is the candidate's adoptability state.
//
// It is one enumeration, not a bare string, because the two reasons a
// candidate cannot be adopted call for different recovery steps: "the inputs
// changed" means run the action again, "the authorization is gone" means
// restore the authorization and run it again. A candidate never becomes fresh
// again on its own -- restoring an authorization does not revive a candidate
// that was produced under it.
type Validity string

const (
	// ValidityFresh means the candidate still matches the inputs it was
	// produced from and may be adopted.
	ValidityFresh Validity = "fresh"
	// ValidityStale means the inputs the candidate was produced from have
	// changed since, so the candidate no longer describes the chapter.
	ValidityStale Validity = "stale"
	// ValidityNotAuthorized means the authorization the candidate rests on is
	// gone, so the candidate cannot be used at all.
	ValidityNotAuthorized Validity = "not_authorized"
)

// RunMode says how the run that produced a candidate reached the model.
//
// The three values are the ones the rewrite side has always reported: a real
// model, no provider at all, and a real provider answering with a fake
// model.
type RunMode string

const (
	// RunModeReal means a real model produced the candidate.
	RunModeReal RunMode = "real"
	// RunModeMock means no provider was called.
	RunModeMock RunMode = "mock"
	// RunModeRealAPIFakeModel means a real provider answered with a fake
	// model.
	RunModeRealAPIFakeModel RunMode = "real_api_fake_model"
)

// ValidRunMode says whether this is one of the three run modes.
//
// The run-mode vocabulary has one owner: the candidate layer. An executor
// that needs to check a reported mode asks here instead of keeping a second
// word list of its own (development plan A27).
func ValidRunMode(mode RunMode) bool {
	switch mode {
	case RunModeReal, RunModeMock, RunModeRealAPIFakeModel:
		return true
	}
	return false
}
