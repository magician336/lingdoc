package acceptancebudget

// The Qwen controlled-execution profile.
//
// What this profile controls is the TOKEN BUDGET and the CALL COUNT of one
// external Qwen chat request. It carries no price: the profile says so in the
// ledger instead of inventing a money amount, because a made-up unit price is
// a number nobody can check and a number that looks like a fact.
//
// The profile is deliberately NOT derived from the DeepSeek profile. Its model
// allowlist, its provider rule and its limits are declared here, and a Qwen
// call is approved only when this file says so.
//
// Three facts travel with every record this profile writes:
//
//   - the cost basis ("not_applicable" here): the money columns are NOT
//     computed, and 0 under this basis never means "the call was free",
//   - the usage status ("reported" / "unavailable"): a call whose provider
//     reported no usage is recorded as unavailable, never as an estimate,
//   - the token counters the provider actually reported.

const (
	// QwenProfile is the profile that arms the controlled Qwen execution path.
	//
	// It is selected with LINGDOC_T10_ACCEPTANCE_PROFILE, exactly like the
	// other profiles. The difference is what the profile controls: tokens and
	// calls, not money.
	QwenProfile = "lingdoc-qwen-token-budget-20261023-v1"

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
	// QwenMaxCalls is the number of external requests this profile allows.
	QwenMaxCalls = 1
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
	// UsageStatusUnavailable means the provider reported no usage. The
	// recorded token counters are then empty, and they are never filled
	// with an estimate.
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
	if profile == QwenProfile {
		return QwenInputMax, QwenOutputMax, QwenTotalMax
	}
	return InputMax, OutputMax, InputMax + OutputMax
}

// ProfileCostBasis returns what the money columns of a profile's ledger mean.
func ProfileCostBasis(profile string) string {
	if profile == QwenProfile {
		return CostBasisNotApplicable
	}
	return CostBasisDeepSeekPeakUSD
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
	if profile == QwenProfile {
		return QwenChatModel
	}
	return DeepSeekModel
}

// ProfileAllowsModel reports whether one profile may reserve one model.
//
// The Qwen profile allows exactly one model: the approved Qwen chat model.
// Every other model -- the DeepSeek chat model, the embedding models -- is
// refused, which is what keeps the embedding, summary and background paths
// from reaching a provider while the controlled profile is armed.
func ProfileAllowsModel(profile, model string) bool {
	switch profile {
	case QwenProfile:
		return model == QwenChatModel
	case CostOnlyProfile:
		return model == DeepSeekModel || model == QwenEmbedding || model == QwenEmbeddingFlash
	default:
		return model == DeepSeekModel || model == QwenEmbedding
	}
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
