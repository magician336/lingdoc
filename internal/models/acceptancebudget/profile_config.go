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
