package workspace

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/acceptancebudget"
)

// The embedding profile approves no chat model, so the chat path must refuse
// before anything leaves the process.
//
// The refusal is not a formality. The reservation uses the profile's APPROVED
// model, so without this check a chat request under an embedding profile would
// be reserved -- and therefore counted -- as an embedding call, and the
// provider would answer a chat request that no chat budget was written for.
//
// The existing profiles are unaffected: they all approve a chat model, and the
// chat path keeps working under them.
func TestTheChatPathRefusesAProfileThatApprovesNoChatModel(t *testing.T) {
	t.Setenv(acceptancebudget.ProfileEnv, acceptancebudget.QwenEmbeddingProfile)
	budget := &generationAcceptanceBudget{profile: acceptancebudget.QwenEmbeddingProfile}

	if _, err := budget.approvedModel(generationAcceptanceModel); !errors.Is(err, errAcceptanceWrongModel) {
		t.Fatalf("chat call under the embedding profile: err = %v, want a refusal", err)
	}
	// The refusal must not have written anything: the budget is not the
	// ledger's business, and the ledger must stay untouched.
	if budget.ledgerPath != "" {
		t.Fatalf("the refusal wrote a ledger path: %q", budget.ledgerPath)
	}
}

func TestTheChatPathStillWorksUnderTheChatProfiles(t *testing.T) {
	for _, profile := range []string{
		acceptancebudget.Profile,
		acceptancebudget.CostOnlyProfile,
		acceptancebudget.QwenProfile,
		acceptancebudget.QwenProfileV2,
	} {
		t.Run(profile, func(t *testing.T) {
			t.Setenv(acceptancebudget.ProfileEnv, profile)
			budget := &generationAcceptanceBudget{profile: profile, ledgerPath: filepath.Join(t.TempDir(), "calls.json")}
			approved, err := budget.approvedModel(generationAcceptanceModel)
			if err != nil {
				t.Fatalf("chat call under %q: err = %v", profile, err)
			}
			if approved != acceptancebudget.ProfileApprovedModel(profile) {
				t.Fatalf("approved model = %q, want %q", approved, acceptancebudget.ProfileApprovedModel(profile))
			}
		})
	}
}
