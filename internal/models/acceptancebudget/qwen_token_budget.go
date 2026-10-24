package acceptancebudget

// The Qwen controlled-execution profiles.
//
// What these profiles control is the TOKEN BUDGET and the CALL COUNT of
// external Qwen chat requests. They carry no price: the profile says so in
// the ledger instead of inventing a money amount, because a made-up unit
// price is a number nobody can check and a number that looks like a fact.
//
// The profiles are deliberately NOT derived from the DeepSeek profile. Their
// model allowlist, their provider rule and their limits are declared here,
// and a Qwen call is approved only when this file says so.
//
// Each profile is one entry of the configuration table in profile_config.go.
// The first profile's numbers are frozen; the second profile adds a
// cumulative token ceiling and changes nothing else.
//
// FOUR facts travel with every record these profiles write:
//
//   - the cost basis ("not_applicable" here): the money columns are NOT
//     computed, and 0 under this basis never means "the call was free",
//   - the usage status ("reported" / "unavailable"): a call whose provider
//     reported no usage is recorded as unavailable, never as an estimate,
//   - the token counters the provider actually reported,
//   - the token RESERVATION, which is an upper bound of what a call could
//     consume and never a claim about what it did consume.

const (
	// QwenProfile is the profile that arms the controlled Qwen execution path.
	//
	// It is selected with LINGDOC_T10_ACCEPTANCE_PROFILE, exactly like the
	// other profiles. The difference is what the profile controls: tokens and
	// calls, not money.
	//
	// Its meaning is FROZEN. The one call it already made lives in its own
	// ledger, and its review evidence was gathered against these exact
	// numbers. A new authorization is a NEW profile, never a new meaning
	// for this name.
	QwenProfile = "lingdoc-qwen-token-budget-20261023-v1"

	// QwenProfileV2 is the SECOND controlled Qwen profile.
	//
	// It is a new profile, not a new version OF the first one. It carries
	// its own call count and its own cumulative token ceiling, and it counts
	// only its own calls: the one call the first profile already made lives
	// in the first profile's ledger and is not counted here.
	//
	// The name is versioned on purpose. A profile name is the identity a
	// ledger is written under and the identity a reviewer reads; giving an
	// existing name a new meaning would silently change what an old ledger
	// says.
	QwenProfileV2 = "lingdoc-qwen-token-budget-20261024-v2"

	// QwenChatModel is the approved Qwen chat model name.
	QwenChatModel = "qwen3.8-flash"

	// QwenProvider is the provider field the approved model row must carry.
	//
	// The field is checked as it is stored. It is NOT derived from the base
	// URL: provider.DetectProvider does not recognise a regional Model
	// Studio workspace endpoint (it answers "generic" for it), so a
	// detection-based rule would approve the wrong provider.
	QwenProvider = "aliyun"

	// QwenInputMax is the input token ceiling of one controlled call.
	QwenInputMax = 16000
	// QwenOutputMax is the output token ceiling of one controlled call. It
	// is sent to the provider as the completion budget of the request, so the
	// ceiling is a request parameter and not only a post-hoc check.
	QwenOutputMax = 4096
	// QwenTotalMax is the ceiling of input plus output of one controlled call.
	QwenTotalMax = QwenInputMax + QwenOutputMax
	// QwenMaxCalls is the number of external requests the FIRST Qwen profile
	// allows.
	QwenMaxCalls = 1
	// QwenV2MaxCalls is the number of external requests the SECOND Qwen
	// profile allows.
	//
	// It counts the calls THIS profile makes. The call the first profile
	// already made belongs to the first profile's ledger, and the two
	// ledgers are not added together.
	QwenV2MaxCalls = 4
	// QwenV2CumulativeTokenMax is the ceiling of the CUMULATIVE token
	// reservation of one window under the second Qwen profile.
	//
	// The ceiling is on the RESERVATION, not on the usage. A request is
	// refused BEFORE it leaves the process when the cumulative reservation
	// of the window plus the reservation of that request would pass this
	// number. The provider's reported usage is recorded separately and is
	// never used to decide what may still be reserved.
	//
	// The worst case of the authorized window fits inside it: four calls of
	// at most 16,000 + 4,096 = 20,096 tokens reserve at most
	// 4 * 20,096 = 80,384 tokens, and 80,384 <= 90,000. The ceiling
	// therefore cannot be reached by the calls this profile authorizes; it
	// is the second line of defence behind the call count.
	QwenV2CumulativeTokenMax = 90000
)

// The cost basis values a ledger can carry.
//
// A cost basis names what the money columns of a ledger MEAN. Without it, a 0
// in a money column is ambiguous: it reads as "this call was free", and for
// the Qwen profile that is a false statement -- the amount was never
// computed.
const (
	// CostBasisNotApplicable marks a ledger whose money columns carry no
	// computed amount. The money columns are 0 because nothing was priced,
	// not because the call cost nothing.
	CostBasisNotApplicable = "not_applicable"
	// CostBasisDeepSeekPeakUSD marks the DeepSeek money basis: the peak
	// published rates captured for 2026-09-29, cache treated as a miss.
	CostBasisDeepSeekPeakUSD = "deepseek_peak_usd_2026_09_29"
)

// The usage status values a request can carry.
const (
	// UsageStatusReported means the provider reported the token counters and
	// the recorded counters are the provider's own numbers.
	UsageStatusReported = "reported"
	// UsageStatusUnavailable means the provider reported no usage: the
	// amount is 不可核实 (unverifiable). The recorded token counters are then
	// empty, and they are never filled with an estimate.
	//
	// It is a statement about what is KNOWN, not about what happened: the
	// provider may well have consumed tokens, but nobody can prove how many
	// from this side, so the record says so instead of guessing.
	UsageStatusUnavailable = "unavailable"
)

// QwenApprovedBaseURLs is the allowlist of Bailian (Aliyun Model Studio)
// workspace endpoints the controlled Qwen call may be sent to.
//
// The list is an allowlist, not a pattern: a workspace endpoint is a
// deployment decision, and an endpoint nobody approved must not be reachable
// just because it looks like a Model Studio address.
var QwenApprovedBaseURLs = []string{
	"https://llm-k57eqvdqacn4nov3.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
}

// ProfileTokenLimits returns the input, output and total token ceilings of one
// profile.
//
// The ceilings are the profile's control core. They are the same numbers the
// request carries (output) and the same numbers the ledger checks after the
// provider answered (input, output, total).
func ProfileTokenLimits(profile string) (inputMax, outputMax, totalMax int) {
	config := profileConfigOf(profile)
	return config.inputMax, config.outputMax, config.totalMax
}

// ProfileCostBasis returns what the money columns of a profile's ledger mean.
func ProfileCostBasis(profile string) string {
	return profileConfigOf(profile).costBasis
}

// ProfilePriced reports whether a profile computes money at all.
//
// A profile without a price basis must not be given one: the money gate of
// such a profile is not a money gate, and the ledger says "not applicable"
// rather than 0.
func ProfilePriced(profile string) bool {
	return ProfileCostBasis(profile) != CostBasisNotApplicable
}

// ProfileApprovedModel returns the model name one profile may call.
//
// It is the single source of truth for "which model is approved": the
// reservation, the ledger and the model selection all read it, so a call can
// never be recorded under a model the profile did not approve.
func ProfileApprovedModel(profile string) string {
	return profileConfigOf(profile).approvedModel
}

// ProfileAllowsModel reports whether one profile may reserve one model.
//
// The Qwen profiles allow exactly one model: the approved Qwen chat model.
// Every other model -- the DeepSeek chat model, the embedding models -- is
// refused, which is what keeps the embedding, summary and background paths
// from reaching a provider while the controlled profile is armed.
func ProfileAllowsModel(profile, model string) bool {
	for _, allowed := range profileConfigOf(profile).allowedModels {
		if model == allowed {
			return true
		}
	}
	return false
}

// IsApprovedQwenBaseURL reports whether a base URL is on the approved list.
//
// The comparison ignores a trailing slash, because a base URL written with
// one names the same endpoint.
func IsApprovedQwenBaseURL(baseURL string) bool {
	for _, approved := range QwenApprovedBaseURLs {
		if trimTrailingSlash(baseURL) == trimTrailingSlash(approved) {
			return true
		}
	}
	return false
}

func trimTrailingSlash(value string) string {
	for len(value) > 0 && value[len(value)-1] == '/' {
		value = value[:len(value)-1]
	}
	return value
}
