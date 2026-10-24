package acceptancebudget

// The three token numbers of one window, kept apart.
//
// The three numbers answer three different questions, and reading one of
// them as another is how a budget turns into a guess:
//
//   - ReservedTokens: how many tokens the window RESERVED. A reservation is
//     an upper bound of what the calls could consume, not a claim about what
//     they consumed.
//   - ActualTokens: how many tokens the provider REPORTED. It is the truth
//     about the calls whose usage is known, and it excludes every call whose
//     usage was unavailable.
//   - the two remainders: how much of the ceiling is left on each count.
//
// A reader who wants to know "how much budget is left" must say which count
// they mean. The reservation remainder is what the GATE uses; the usage
// remainder is what the provider's own numbers say. They are different
// numbers, and the gate never uses the second one to decide the first.

// TokenBudgetState is the state of the token budget of one window.
type TokenBudgetState struct {
	// Ceiling is the cumulative token ceiling of the window.
	Ceiling int
	// ReservedTokens is the cumulative token reservation of the window.
	ReservedTokens int
	// ActualTokens is the cumulative token usage the provider reported.
	ActualTokens int
	// RemainingReserved is Ceiling - ReservedTokens: the tokens the window
	// may still reserve.
	RemainingReserved int
	// RemainingActual is Ceiling - ActualTokens: the tokens the window may
	// still CONSUME, measured against what the provider reported.
	//
	// It is nil when the usage is not fully verifiable: the remainder
	// against the usage is then UNKNOWN, and nil says so. It is never 0 in
	// that case, because 0 would read as "nothing left" or "nothing used".
	RemainingActual *int
	// UsageUnverifiable says that at least one recorded call has a usage
	// that could not be verified.
	UsageUnverifiable bool
}

// NewTokenBudgetState builds the state of a window from its counters.
//
// It is the one place that decides what the two remainders mean, so every
// reader of a ledger gets the same numbers.
func NewTokenBudgetState(ceiling, reserved, actual int, usageUnverifiable bool) TokenBudgetState {
	state := TokenBudgetState{
		Ceiling:           ceiling,
		ReservedTokens:    reserved,
		ActualTokens:      actual,
		RemainingReserved: ceiling - reserved,
		UsageUnverifiable: usageUnverifiable,
	}
	if !usageUnverifiable {
		remaining := ceiling - actual
		state.RemainingActual = &remaining
	}
	return state
}

// TokenBudgetState returns the state of the token budget of this ledger.
func (l Ledger) TokenBudgetState() TokenBudgetState {
	return NewTokenBudgetState(l.CumulativeTokenMax, l.ReservedTokens, l.ActualTokens, ledgerUsageUnverifiable(l))
}
