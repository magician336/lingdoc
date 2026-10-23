package workspace

import (
	"os"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
	"github.com/Tencent/WeKnora/internal/types"
)

// The Qwen controlled-execution path.
//
// What it decides: which model row may serve the controlled Qwen call, and
// which limits that call runs under.
//
// What it deliberately does NOT do: reuse the DeepSeek rule. The DeepSeek
// rule names a different provider, a different endpoint and a different
// model, and it asks for a DeepSeek wire format the Qwen model does not
// declare. A Qwen call approved by a DeepSeek rule would be approved by
// facts that say nothing about Qwen.
//
// The profile is the single source of truth for the limits. The two adapters
// that call a provider (the generation adapter and the agent adapter) belong
// to another face and are not changed by this work, so they read the limits
// through the profile rather than through their own constants.

// activeAcceptanceProfile returns the profile this process runs under.
//
// It is the profile named by LINGDOC_T10_ACCEPTANCE_PROFILE, which is also
// what loadGenerationAcceptanceBudget reads. One variable decides which
// provider may be called, so there is no second place to configure.
func activeAcceptanceProfile() string {
	return strings.TrimSpace(os.Getenv(generationAcceptanceProfileEnv))
}

// selectQwenAcceptanceModel returns the one approved Qwen chat model.
//
// It fails closed. No model, more than one model, a model of the wrong type,
// a model of another provider, a model of another endpoint and a model whose
// name is not approved all end the same way: no model is selected, and the
// call never leaves the process.
func selectQwenAcceptanceModel(models []*types.Model) (*types.Model, bool) {
	var selected *types.Model
	for _, model := range models {
		if model == nil || !qwenModelApproved(model) {
			continue
		}
		if selected != nil {
			// Two approved rows are ambiguous: the ledger could not say
			// which one answered.
			return nil, false
		}
		selected = model
	}
	if selected == nil {
		return nil, false
	}
	return selected, true
}

// qwenModelApproved reports whether one model row may serve the controlled
// Qwen call.
//
// The facts it checks:
//
//   - the model TYPE is KnowledgeQA (a chat model), not an embedding model,
//   - the row is a remote, active model,
//   - the model NAME is on the approved list,
//   - the provider FIELD is aliyun,
//   - the base URL is one of the approved Bailian workspace endpoints.
//
// The provider field is checked as it is stored. It is not derived from the
// base URL: provider.DetectProvider does not recognise a regional Model
// Studio workspace endpoint and answers "generic" for it, so a
// detection-based rule would approve the wrong provider.
//
// The DeepSeek wire-format requirement (thinking_control = thinking_type) is
// NOT part of this rule. It describes a DeepSeek request shape, not a Qwen
// one, and requiring it would reject an approved Qwen model.
func qwenModelApproved(model *types.Model) bool {
	if model == nil {
		return false
	}
	if model.Type != types.ModelTypeKnowledgeQA {
		return false
	}
	if model.Source != types.ModelSourceRemote || model.Status != types.ModelStatusActive {
		return false
	}
	if model.Name != acceptancebudget.QwenChatModel {
		return false
	}
	if model.Parameters.Provider != acceptancebudget.QwenProvider {
		return false
	}
	return acceptancebudget.IsApprovedQwenBaseURL(model.Parameters.BaseURL)
}

// acceptanceOutputMax returns the output token ceiling of one profile.
//
// The ceiling is sent to the provider as the completion budget of the
// request, so it is a request parameter and not only a check after the
// answer arrived.
func acceptanceOutputMax(profile string) int {
	_, outputMax, _ := acceptancebudget.ProfileTokenLimits(profile)
	return outputMax
}

// acceptanceInputMax returns the input token ceiling of one profile.
func acceptanceInputMax(profile string) int {
	inputMax, _, _ := acceptancebudget.ProfileTokenLimits(profile)
	return inputMax
}

// acceptanceTotalMax returns the input-plus-output ceiling of one profile.
func acceptanceTotalMax(profile string) int {
	_, _, totalMax := acceptancebudget.ProfileTokenLimits(profile)
	return totalMax
}

// acceptanceReservedPerCall returns the money one call reserves.
//
// A profile without a price basis reserves no money at all: the zero means
// "nothing was priced", and the ledger says so through its cost basis.
func acceptanceReservedPerCall(profile string) int64 {
	if !acceptancebudget.ProfilePriced(profile) {
		return 0
	}
	return generationAcceptancePerCall
}

// acceptancePricingBasis returns the sentence that says what the money
// columns of one profile's ledger mean.
//
// A profile without a price basis says so in words. It does not carry a
// price description of another profile: a reader who finds "DeepSeek peak
// rates" under a Qwen ledger would read a price into a ledger that has none.
func acceptancePricingBasis(profile string) string {
	if !acceptancebudget.ProfilePriced(profile) {
		return "No price basis: this profile controls tokens and call count, and no money amount is computed."
	}
	return "DeepSeek official peak rates captured for 2026-09-29: input $0.30/M, output $1.20/M; cache treated as miss."
}
