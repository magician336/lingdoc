package acceptancebudget

// The configuration table of every profile.
//
// The profiles are read from this table instead of from a chain of
// single-value comparisons. A second Qwen profile -- one with its own call
// count and its own cumulative token ceiling -- CANNOT be expressed as
// "if profile == X", and a comparison chain that grows one branch per
// profile is exactly how two profiles end up sharing one branch by
// accident.
//
// The table is the single source of truth for:
//
//   - how many calls a profile may make,
//   - the token ceilings of ONE call,
//   - what the money columns of the ledger MEAN,
//   - which model the profile may call,
//   - the ceiling of the CUMULATIVE token reservation of one window.
//
// One entry exists per declared profile, and one fallback entry answers for
// a profile name nobody declared. The fallback is the DeepSeek
// configuration, because that is what the comparison chain it replaces
// answered for an unknown name.

// The rule families a profile can belong to.
//
// The family is what the CONTROLLED PATH asks about: "is this profile one
// of the Qwen profiles?" is a question about a family, and asking it with a
// single name stops being true the moment a second version of that profile
// exists.
const (
	// profileFamilyDeepSeek is the family of the priced DeepSeek profile.
	profileFamilyDeepSeek = "deepseek"
	// profileFamilyCostOnly is the family of the separately authorized
	// cost-only profile.
	profileFamilyCostOnly = "cost_only"
	// profileFamilyQwen is the family of the controlled Qwen profiles.
	profileFamilyQwen = "qwen"
)

// profileConfig is the configuration of ONE profile.
type profileConfig struct {
	// family names the rule family of the profile.
	family string
	// maxCalls is the number of external requests the profile allows.
	// 0 means "the profile does not limit the call count".
	maxCalls int
	// nanoCNY is the money ceiling of the profile.
	// 0 means "this profile computes no amount at all": the zero is "no
	// price basis", not "no budget".
	nanoCNY int64
	// backstopCNY is the human backstop of the profile.
	backstopCNY int
	// inputMax, outputMax and totalMax are the token ceilings of ONE call.
	inputMax  int
	outputMax int
	totalMax  int
	// costBasis names what the money columns of the ledger MEAN.
	costBasis string
	// approvedModel is the one model the profile may call.
	approvedModel string
	// allowedModels lists every model the profile may RESERVE.
	allowedModels []string
	// cumulativeTokenMax is the ceiling of the CUMULATIVE token reservation
	// of one window. 0 means the profile has no cumulative token ceiling.
	cumulativeTokenMax int
	// embeddingHTTPErrorReconcilable says whether an operator may reconcile an
	// embedding HTTP error on this profile's ledger.
	//
	// A provider 4xx leaves the window FROZEN: the failed call's usage
	// cannot be confirmed, so the budget layer refuses every later
	// reservation. Only an explicit operator reconciliation reopens it.
	// A profile that can make embedding calls must therefore be able to
	// RECOVER from one of them failing -- otherwise a single 4xx retires the
	// whole window (ticket #671).
	//
	// The reconciliation keeps every conservative invariant: the failed
	// request is preserved exactly, the FULL reserved upper bound is charged,
	// and the provider's usage stays "unknown". It is a local ledger
	// operation: it never calls a provider, never changes the profile, and
	// never refunds anything.
	embeddingHTTPErrorReconcilable bool
}

// profileConfigs is the configuration of every declared profile.
//
// The first Qwen profile is listed with exactly the numbers it always had:
// its call count, its token ceilings, its cost basis, its approved model
// and its allowlist. A second entry does not change the first one.
var profileConfigs = map[string]profileConfig{
	Profile: {
		family:        profileFamilyDeepSeek,
		maxCalls:      MaxCalls,
		nanoCNY:       MaxBudgetNanoCNY,
		backstopCNY:   HumanBackstopCNY,
		inputMax:      InputMax,
		outputMax:     OutputMax,
		totalMax:      InputMax + OutputMax,
		costBasis:     CostBasisDeepSeekPeakUSD,
		approvedModel: DeepSeekModel,
		allowedModels: []string{DeepSeekModel, QwenEmbedding},
	},
	CostOnlyProfile: {
		family:        profileFamilyCostOnly,
		maxCalls:      0,
		nanoCNY:       CostOnlyMaxBudgetNanoCNY,
		backstopCNY:   20,
		inputMax:      InputMax,
		outputMax:     OutputMax,
		totalMax:      InputMax + OutputMax,
		costBasis:     CostBasisDeepSeekPeakUSD,
		approvedModel: DeepSeekModel,
		allowedModels: []string{DeepSeekModel, QwenEmbedding, QwenEmbeddingFlash},
		// This profile has always been the one an operator could reconcile an
		// embedding HTTP error on. The capability is now stated in the table
		// instead of being hard-wired to this profile's name.
		embeddingHTTPErrorReconcilable: true,
	},
	QwenProfile: {
		family:        profileFamilyQwen,
		maxCalls:      QwenMaxCalls,
		nanoCNY:       0,
		backstopCNY:   0,
		inputMax:      QwenInputMax,
		outputMax:     QwenOutputMax,
		totalMax:      QwenTotalMax,
		costBasis:     CostBasisNotApplicable,
		approvedModel: QwenChatModel,
		allowedModels: []string{QwenChatModel},
	},
	QwenProfileV2: {
		family:        profileFamilyQwen,
		maxCalls:      QwenV2MaxCalls,
		nanoCNY:       0,
		backstopCNY:   0,
		inputMax:      QwenInputMax,
		outputMax:     QwenOutputMax,
		totalMax:      QwenTotalMax,
		costBasis:     CostBasisNotApplicable,
		approvedModel: QwenChatModel,
		allowedModels: []string{QwenChatModel},
		// The cumulative token ceiling is the only thing that is new here.
		// The single-call ceilings are the first profile's: nothing about
		// one call is loosened by allowing more calls.
		cumulativeTokenMax: QwenV2CumulativeTokenMax,
	},
	QwenEmbeddingProfile: {
		family:        profileFamilyQwen,
		maxCalls:      QwenEmbeddingMaxCalls,
		nanoCNY:       QwenEmbeddingMaxReserveNanoCNY,
		backstopCNY:   QwenEmbeddingBackstopCNY,
		inputMax:      QwenEmbeddingInputMax,
		outputMax:     QwenEmbeddingOutputMax,
		totalMax:      QwenEmbeddingTotalMax,
		costBasis:     CostBasisQwenEmbeddingNanoCNY,
		approvedModel: QwenEmbedding,
		allowedModels: []string{QwenEmbedding, QwenEmbeddingFlash},
		// The cumulative token ceiling is the window the authorization
		// names: four calls, eight thousand tokens.
		cumulativeTokenMax: QwenEmbeddingCumulativeTokenMax,
		// A provider 4xx must not retire this window for good: the
		// authorized budget (four calls, eight thousand tokens) would be
		// unusable after the FIRST failure, with no way to recover it.
		embeddingHTTPErrorReconcilable: true,
	},
}

// defaultProfileConfig answers for a profile name nobody declared.
//
// It is the DeepSeek configuration, which is what the comparison chain this
// table replaces answered for an unknown name.
var defaultProfileConfig = profileConfig{
	family:        profileFamilyDeepSeek,
	maxCalls:      MaxCalls,
	nanoCNY:       MaxBudgetNanoCNY,
	backstopCNY:   HumanBackstopCNY,
	inputMax:      InputMax,
	outputMax:     OutputMax,
	totalMax:      InputMax + OutputMax,
	costBasis:     CostBasisDeepSeekPeakUSD,
	approvedModel: DeepSeekModel,
	allowedModels: []string{DeepSeekModel, QwenEmbedding},
}

// profileConfigOf returns the configuration of one profile.
func profileConfigOf(profile string) profileConfig {
	if config, ok := profileConfigs[profile]; ok {
		return config
	}
	return defaultProfileConfig
}

// IsQwenProfile reports whether the profile is one of the controlled Qwen
// profiles.
//
// It answers a FAMILY question, not "is it the first one". A rule that
// compares against a single name stops applying the moment a second version
// of that profile exists, and the controlled path would then fall back to
// the DeepSeek rule -- which approves a different provider, a different
// endpoint and a different model.
func IsQwenProfile(profile string) bool {
	return profileConfigOf(profile).family == profileFamilyQwen
}

// ProfileCumulativeTokenMax returns the ceiling of the CUMULATIVE token
// reservation of one window under one profile.
//
// It is 0 for a profile that has no cumulative token ceiling. A profile with
// a ceiling refuses a request whose reservation would push the cumulative
// reservation past it, BEFORE the request leaves the process.
func ProfileCumulativeTokenMax(profile string) int {
	return profileConfigOf(profile).cumulativeTokenMax
}

// ProfileMaxCalls returns the number of external requests one profile
// allows. It is 0 for a profile that does not limit the call count.
func ProfileMaxCalls(profile string) int {
	return profileConfigOf(profile).maxCalls
}

// ProfileSupportsEmbeddingHTTPErrorReconciliation reports whether an operator
// may reconcile an embedding HTTP error on one profile's ledger.
//
// It answers a question about the PROFILE, not about a call: a profile that
// can make embedding calls must be able to recover from one of them failing.
// Without this, a single provider 4xx freezes the window and the authorized
// budget (the call count and the token ceiling) is retired with no way back
// (ticket #671).
//
// The reconciliation it authorises is a LOCAL LEDGER OPERATION with
// conservative invariants: the failed request is preserved exactly, the full
// reserved upper bound is charged, the provider's usage stays "unknown", and
// nothing is refunded. It never calls a provider, never switches a profile,
// and never loosens a budget limit.
//
// It is deliberately NOT a general "unblock anything" switch: a profile whose
// money columns mean something else (the chat profiles carry no price basis)
// is not covered by it, because the conservative charge has no defined
// meaning there.
func ProfileSupportsEmbeddingHTTPErrorReconciliation(profile string) bool {
	return profileConfigOf(profile).embeddingHTTPErrorReconcilable
}

// The authoritative checklist of the question "did this call exceed a limit?".
//
// TWO ledgers answer that question about ONE and the SAME provider call:
//
//   - the shared budget ledger: the file every profile's calls are counted in
//     (ledger.go), and
//   - the round ledger: the file one window of one run is counted in
//     (internal/lingdoc/workspace/generation_acceptance_budget.go).
//
// When each of them reads its criteria from its own place, they can answer two
// different ways about one and the same answer: one ledger writes down "limit
// violation", the other writes down "response received", and a reader of both
// files cannot tell which one is true. That is not a reporting detail -- it is
// the budget gate itself, because the ledger that does not see the violation
// keeps spending.
//
// This file is the ONE place the criteria live. Both ledgers read it, so the
// two answers cannot drift apart again.
//
// # The four classes and the quantity each one measures
//
//	| class            | criterion                                      | quantity                            | dimension                              |
//	| ---------------- | ---------------------------------------------- | ----------------------------------- | -------------------------------------- |
//	| output ceiling   | outputTokens > the request's output limit      | the request's own output limit      | tokens, EXACT                          |
//	| input ceiling    | inputTokens > the request's input upper bound  | the request's own input upper bound | UTF-8 BYTE LENGTH + a fixed overhead    |
//	| total ceiling    | inputTokens + outputTokens > the profile's total ceiling | the profile's single-call total ceiling | tokens                         |
//	| negative counter | inputTokens < 0 or outputTokens < 0            | --                                   | --                                     |
//
// # The two dimensions, and why they are not interchangeable
//
// The input upper bound is a UTF-8 BYTE LENGTH plus a fixed overhead. It is an
// UPPER BOUND of the token count and never a token count: a token is at least
// one byte, so the byte length of a prompt can never be smaller than its token
// count. Comparing the provider's reported token count against it is
// therefore CONSERVATIVE -- it can fire only when the provider counted MORE
// tokens than the request's own byte bound, which is an anomaly -- and it can
// never let a call through that the request had not reserved for.
//
// The byte bound is NOT a token ceiling, and it is not treated as one:
// LimitQuantityRequestInputUpperBound says so in its dimension, and every
// ledger field that carries it says so next to the field.
//
// The profile's own input ceiling (inputMax) is the ceiling of the REQUEST'S
// INPUT BOUND, so it is a byte ceiling too. The reservation refuses a request
// whose bound passes it, which means every recorded request satisfies
// bound <= inputMax. A reported token count is at most the byte bound, so it
// can never pass inputMax on its own either: the post-hoc comparison against
// the profile's input ceiling is SUBSUMED by the input-ceiling criterion
// above, and the criterion that decides a verdict is the one that names the
// request's own bound.
//
// # What each ledger records
//
// A ledger records BOTH quantities -- the request's own bound (the quantity
// the verdict is measured against) and the profile's ceiling (the quantity the
// pre-flight gate is measured against) -- so a reader can always see which
// number is which and can explain a verdict afterwards.

// LimitCriterion names one class of the checklist.
type LimitCriterion string

const (
	// LimitCriterionOutputCeiling is the class "the call produced more output
	// tokens than the request asked for".
	LimitCriterionOutputCeiling LimitCriterion = "output_ceiling"
	// LimitCriterionInputCeiling is the class "the call consumed more input
	// tokens than the request reserved against".
	LimitCriterionInputCeiling LimitCriterion = "input_ceiling"
	// LimitCriterionTotalCeiling is the class "the call exceeded the
	// profile's single-call total ceiling".
	LimitCriterionTotalCeiling LimitCriterion = "total_ceiling"
	// LimitCriterionNegativeCounter is the class "the provider reported a
	// counter that cannot be true".
	LimitCriterionNegativeCounter LimitCriterion = "negative_counter"
)

// LimitQuantity names the number one criterion measures against.
//
// The quantity says WHICH number, and LimitDimension says what that number
// counts. The dimension is never left implicit: a byte bound and a token
// ceiling are different numbers, and reading one as the other is how two
// ledgers end up disagreeing about one and the same answer.
type LimitQuantity string

// LimitDimension says what a number counts.
type LimitDimension string

const (
	// LimitQuantityRequestOutputLimit is the output limit the request
	// carried.
	LimitQuantityRequestOutputLimit LimitQuantity = "request_output_limit_tokens"
	// LimitQuantityRequestInputUpperBound is the input bound the request
	// reserved against.
	LimitQuantityRequestInputUpperBound LimitQuantity = "request_input_upper_bound_bytes"
	// LimitQuantityProfileTotalCeiling is the profile's single-call total
	// ceiling.
	LimitQuantityProfileTotalCeiling LimitQuantity = "profile_total_ceiling_tokens"
	// LimitQuantityProviderCounters is the pair of counters the provider
	// reported.
	LimitQuantityProviderCounters LimitQuantity = "provider_reported_token_counters"
)

const (
	// LimitDimensionOutputTokensExact is the dimension of an output limit:
	// tokens, and exact -- it is the max_completion_tokens that was sent to
	// the provider.
	LimitDimensionOutputTokensExact LimitDimension = "tokens (exact: the max_completion_tokens sent to the provider)"
	// LimitDimensionInputUpperBoundBytes is the dimension of the request's
	// input bound: a UTF-8 byte length plus a fixed overhead. It is an UPPER
	// BOUND of the token count and NEVER a token count.
	LimitDimensionInputUpperBoundBytes LimitDimension = "UTF-8 byte length plus a fixed overhead (an upper bound of the token count, never a token count)"
	// LimitDimensionTotalTokens is the dimension of the profile's single-call
	// total ceiling: tokens.
	LimitDimensionTotalTokens LimitDimension = "tokens"
	// LimitDimensionProviderTokens is the dimension of the provider's own
	// counters: tokens, and a negative one cannot be true.
	LimitDimensionProviderTokens LimitDimension = "tokens (the provider's own numbers; a negative one cannot be true)"
)

// CallFacts are the recorded facts ONE finished call is measured against.
//
// They are the numbers a ledger already holds: what the request reserved
// against, and what the provider reported.
type CallFacts struct {
	// InputUpperBound is the request's own pre-flight input bound.
	//
	// Its dimension is a UTF-8 BYTE LENGTH plus a fixed overhead: an upper
	// bound of the token count, never a token count.
	InputUpperBound int
	// OutputLimit is the request's own output limit. Its dimension is tokens,
	// and it is exact.
	OutputLimit int
	// InputTokens is the input token count the provider reported.
	InputTokens int
	// OutputTokens is the output token count the provider reported.
	OutputTokens int
}

// LimitBreach is one criterion of the checklist that fired.
type LimitBreach struct {
	// Criterion names the class that fired.
	Criterion LimitCriterion
	// Quantity names the number the class measured against.
	Quantity LimitQuantity
	// Dimension says what that number counts.
	Dimension LimitDimension
	// Limit is that number.
	Limit int
	// Actual is the reported number it was measured against.
	Actual int
}

// LimitVerdict is the answer of the checklist for ONE call.
type LimitVerdict struct {
	// Breaches lists every criterion that fired.
	Breaches []LimitBreach
	// Violated says whether any criterion fired.
	Violated bool
}

// EvaluateCallLimits answers the question "did this call exceed a limit?".
//
// It is the ONE definition of the criteria, and both ledgers call it. The
// profile decides the total ceiling; the request's own recorded bounds decide
// the output and the input criteria.
//
// The verdict never guesses: a criterion fires only when a reported number
// passes the number it is measured against.
func EvaluateCallLimits(profile string, facts CallFacts) LimitVerdict {
	_, _, totalMax := ProfileTokenLimits(profile)
	verdict := LimitVerdict{}
	if facts.OutputTokens > facts.OutputLimit {
		verdict.Breaches = append(verdict.Breaches, LimitBreach{
			Criterion: LimitCriterionOutputCeiling,
			Quantity:  LimitQuantityRequestOutputLimit,
			Dimension: LimitDimensionOutputTokensExact,
			Limit:     facts.OutputLimit,
			Actual:    facts.OutputTokens,
		})
	}
	if facts.InputTokens > facts.InputUpperBound {
		verdict.Breaches = append(verdict.Breaches, LimitBreach{
			Criterion: LimitCriterionInputCeiling,
			Quantity:  LimitQuantityRequestInputUpperBound,
			Dimension: LimitDimensionInputUpperBoundBytes,
			Limit:     facts.InputUpperBound,
			Actual:    facts.InputTokens,
		})
	}
	if facts.InputTokens+facts.OutputTokens > totalMax {
		verdict.Breaches = append(verdict.Breaches, LimitBreach{
			Criterion: LimitCriterionTotalCeiling,
			Quantity:  LimitQuantityProfileTotalCeiling,
			Dimension: LimitDimensionTotalTokens,
			Limit:     totalMax,
			Actual:    facts.InputTokens + facts.OutputTokens,
		})
	}
	if facts.InputTokens < 0 || facts.OutputTokens < 0 {
		// The offending counter is the one that cannot be true. A token count
		// is never negative, so a negative one is an impossible reading and
		// not a small number.
		impossible := facts.InputTokens
		if impossible >= 0 {
			impossible = facts.OutputTokens
		}
		verdict.Breaches = append(verdict.Breaches, LimitBreach{
			Criterion: LimitCriterionNegativeCounter,
			Quantity:  LimitQuantityProviderCounters,
			Dimension: LimitDimensionProviderTokens,
			Limit:     0,
			Actual:    impossible,
		})
	}
	verdict.Violated = len(verdict.Breaches) > 0
	return verdict
}
