package acceptancebudget

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// The embedding profile is a CONTROLLED profile with its own window: four
// calls, eight thousand tokens, and the two embedding models.
//
// It exists because the chat profiles approve exactly one model -- the Qwen
// chat model -- and therefore refuse every embedding model before the request
// leaves the process. A profile that may not reserve the embedding model
// cannot express "four embedding calls, eight thousand tokens" at all.
//
// The rules pinned here:
//
//   - the profile is a supported profile, and it is a Qwen-family profile;
//   - it reserves the embedding models and NOTHING else -- in particular not
//     the chat model, which is what makes a process armed with this profile
//     unable to spend chat budget;
//   - it allows four calls, and the fifth is refused;
//   - it keeps the cumulative token ceiling, and a window that would pass it
//     is refused;
//   - an embedding call carries no output tokens, because an embedding
//     endpoint produces vectors and not text;
//   - its ledger carries its own ceilings, its own cost basis and the
//     embedding price -- and it re-opens, because a profile that insisted on
//     numbers its own ledger does not carry would be a ledger that cannot be
//     read back;
//   - every OTHER profile keeps exactly the numbers it had.

func TestQwenEmbeddingProfileIsSupportedAndIsAQwenProfile(t *testing.T) {
	if !SupportedProfile(QwenEmbeddingProfile) {
		t.Fatalf("profile %q is not supported", QwenEmbeddingProfile)
	}
	if !IsQwenProfile(QwenEmbeddingProfile) {
		t.Fatal("the embedding profile is not a Qwen-family profile")
	}
	if ProfileApprovedModel(QwenEmbeddingProfile) != QwenEmbedding {
		t.Fatalf("approved model = %q, want %q", ProfileApprovedModel(QwenEmbeddingProfile), QwenEmbedding)
	}
	if ProfileCostBasis(QwenEmbeddingProfile) != CostBasisQwenEmbeddingNanoCNY {
		t.Fatalf("cost basis = %q, want %q", ProfileCostBasis(QwenEmbeddingProfile), CostBasisQwenEmbeddingNanoCNY)
	}
	if !ProfilePriced(QwenEmbeddingProfile) {
		t.Fatal("the embedding profile computes no money, so its ledger cannot say what an amount means")
	}
}

// The model allowlist is the gate. This profile reserves the two embedding
// models and nothing else -- and the chat model above all, because a process
// armed with this profile must not be able to spend chat budget.
func TestQwenEmbeddingProfileReservesTheEmbeddingModelsAndRefusesTheChatModel(t *testing.T) {
	budget := New(QwenEmbeddingProfile, filepath.Join(t.TempDir(), "calls.json"))
	for _, model := range []string{QwenEmbedding, QwenEmbeddingFlash} {
		reservation, err := budget.Reserve(context.Background(), model, 128, 0)
		if err != nil {
			t.Fatalf("reserve(%q) error = %v", model, err)
		}
		if err := reservation.Complete("response_received", 64, 0, "", false); err != nil {
			t.Fatalf("complete(%q) error = %v", model, err)
		}
	}
	for _, model := range []string{QwenChatModel, DeepSeekModel, "qwen3.8-flash-plus", ""} {
		if _, err := budget.Reserve(context.Background(), model, 128, 0); !errors.Is(err, ErrWrongModel) {
			t.Fatalf("reserve(%q) error = %v, want the model to be refused", model, err)
		}
	}
}

// Four calls, and the fifth is refused. The count is the profile's own, and it
// is not shared with the chat profile's ledger.
func TestQwenEmbeddingProfileAllowsFourCallsAndRefusesTheFifth(t *testing.T) {
	if ProfileMaxCalls(QwenEmbeddingProfile) != QwenEmbeddingMaxCalls {
		t.Fatalf("call ceiling = %d, want %d", ProfileMaxCalls(QwenEmbeddingProfile), QwenEmbeddingMaxCalls)
	}
	budget := New(QwenEmbeddingProfile, filepath.Join(t.TempDir(), "calls.json"))
	for index := 0; index < QwenEmbeddingMaxCalls; index++ {
		reservation, err := budget.Reserve(context.Background(), QwenEmbedding, 128, 0)
		if err != nil {
			t.Fatalf("call %d error = %v", index+1, err)
		}
		if err := reservation.Complete("response_received", 64, 0, "", false); err != nil {
			t.Fatalf("complete %d error = %v", index+1, err)
		}
	}
	if _, err := budget.Reserve(context.Background(), QwenEmbedding, 128, 0); !errors.Is(err, ErrCallsSpent) {
		t.Fatalf("fifth reserve error = %v, want the call budget to be exhausted", err)
	}
}

// The cumulative token ceiling is the second line of defence behind the call
// count: a window that would pass it is refused BEFORE the request leaves the
// process.
func TestQwenEmbeddingProfileKeepsItsCumulativeTokenCeiling(t *testing.T) {
	if ProfileCumulativeTokenMax(QwenEmbeddingProfile) != QwenEmbeddingCumulativeTokenMax {
		t.Fatalf("cumulative ceiling = %d, want %d",
			ProfileCumulativeTokenMax(QwenEmbeddingProfile), QwenEmbeddingCumulativeTokenMax)
	}
	budget := New(QwenEmbeddingProfile, filepath.Join(t.TempDir(), "calls.json"))
	reservation, err := budget.Reserve(context.Background(), QwenEmbedding, QwenEmbeddingCumulativeTokenMax-1, 0)
	if err != nil {
		t.Fatalf("first reserve error = %v", err)
	}
	if err := reservation.Complete("response_received", 64, 0, "", false); err != nil {
		t.Fatalf("complete error = %v", err)
	}
	if _, err := budget.Reserve(context.Background(), QwenEmbedding, 2, 0); !errors.Is(err, ErrTokenBudgetExhausted) {
		t.Fatalf("over-ceiling reserve error = %v, want the token budget to be exhausted", err)
	}
}

// An embedding call carries no output tokens: the provider returns vectors,
// not text, and a request that carried an output limit would be a request for
// something an embedding endpoint does not do.
func TestQwenEmbeddingProfileRefusesAnOutputLimitItCannotHave(t *testing.T) {
	budget := New(QwenEmbeddingProfile, filepath.Join(t.TempDir(), "calls.json"))
	if _, err := budget.Reserve(context.Background(), QwenEmbedding, 128, 1); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("reserve with an output limit error = %v, want the output ceiling", err)
	}
	if _, err := budget.Reserve(context.Background(), QwenEmbedding, QwenEmbeddingInputMax+1, 0); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("oversized input error = %v, want the input ceiling", err)
	}
}

// The record says what it is: the embedding price, the embedding cost basis,
// and the profile's own ceilings.
func TestQwenEmbeddingProfileLedgerCarriesItsOwnCeilingsAndBasis(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.json")
	budget := New(QwenEmbeddingProfile, path)
	reservation, err := budget.Reserve(context.Background(), QwenEmbedding, 2048, 0)
	if err != nil {
		t.Fatalf("reserve error = %v", err)
	}
	if err := reservation.Complete("response_received", 900, 0, "", false); err != nil {
		t.Fatalf("complete error = %v", err)
	}
	ledger, err := readLedger(path, QwenEmbeddingProfile)
	if err != nil {
		t.Fatalf("read ledger error = %v", err)
	}
	if ledger.MaxCalls != QwenEmbeddingMaxCalls {
		t.Fatalf("ledger call ceiling = %d, want %d", ledger.MaxCalls, QwenEmbeddingMaxCalls)
	}
	if ledger.CumulativeTokenMax != QwenEmbeddingCumulativeTokenMax {
		t.Fatalf("ledger cumulative ceiling = %d, want %d", ledger.CumulativeTokenMax, QwenEmbeddingCumulativeTokenMax)
	}
	if ledger.TotalMaxTokens != QwenEmbeddingTotalMax {
		t.Fatalf("ledger single-call ceiling = %d, want %d", ledger.TotalMaxTokens, QwenEmbeddingTotalMax)
	}
	if ledger.MaxReserveNanoCNY != QwenEmbeddingMaxReserveNanoCNY {
		t.Fatalf("ledger money ceiling = %d, want %d", ledger.MaxReserveNanoCNY, QwenEmbeddingMaxReserveNanoCNY)
	}
	if len(ledger.Requests) != 1 {
		t.Fatalf("ledger rows = %d, want 1", len(ledger.Requests))
	}
	row := ledger.Requests[0]
	if row.CostBasis != CostBasisQwenEmbeddingNanoCNY {
		t.Fatalf("row cost basis = %q, want %q", row.CostBasis, CostBasisQwenEmbeddingNanoCNY)
	}
	if row.ReservedNanoCNY != int64(2048)*QwenNanoCNYPerToken {
		t.Fatalf("row reserved = %d, want %d", row.ReservedNanoCNY, int64(2048)*QwenNanoCNYPerToken)
	}
	if row.ActualNanoCNY != int64(900)*QwenNanoCNYPerToken {
		t.Fatalf("row actual = %d, want %d", row.ActualNanoCNY, int64(900)*QwenNanoCNYPerToken)
	}
	// A ledger that cannot be read back is not evidence of anything.
	if !validLedger(ledger, QwenEmbeddingProfile) {
		t.Fatal("the ledger of the embedding profile is not accepted by its own validator")
	}
	reopened := New(QwenEmbeddingProfile, path)
	reopenedReservation, err := reopened.Reserve(context.Background(), QwenEmbedding, 128, 0)
	if err != nil {
		t.Fatalf("re-open reserve error = %v", err)
	}
	if err := reopenedReservation.Complete("response_received", 64, 0, "", false); err != nil {
		t.Fatalf("re-open complete error = %v", err)
	}
}

// Every other profile keeps exactly the numbers it had. A new profile may not
// change what an old profile means: the old ledgers say what they say.
func TestTheEmbeddingProfileLeavesEveryOtherProfileAlone(t *testing.T) {
	cases := []struct {
		profile        string
		maxCalls       int
		cumulative     int
		inputMax       int
		outputMax      int
		approved       string
		allowsEmbed    bool
		allowsApproved bool
		priced         bool
		moneyCeiling   int64
	}{
		{Profile, MaxCalls, 0, InputMax, OutputMax, DeepSeekModel, true, true, true, MaxBudgetNanoCNY},
		{CostOnlyProfile, 0, 0, InputMax, OutputMax, DeepSeekModel, true, true, true, CostOnlyMaxBudgetNanoCNY},
		{QwenProfile, QwenMaxCalls, 0, QwenInputMax, QwenOutputMax, QwenChatModel, false, true, false, 0},
		{QwenProfileV2, QwenV2MaxCalls, QwenV2CumulativeTokenMax, QwenInputMax, QwenOutputMax, QwenChatModel, false, true, false, 0},
	}
	for _, test := range cases {
		t.Run(test.profile, func(t *testing.T) {
			if got := ProfileMaxCalls(test.profile); got != test.maxCalls {
				t.Fatalf("call ceiling = %d, want %d", got, test.maxCalls)
			}
			if got := ProfileCumulativeTokenMax(test.profile); got != test.cumulative {
				t.Fatalf("cumulative ceiling = %d, want %d", got, test.cumulative)
			}
			inputMax, outputMax, _ := ProfileTokenLimits(test.profile)
			if inputMax != test.inputMax || outputMax != test.outputMax {
				t.Fatalf("token ceilings = (%d, %d), want (%d, %d)", inputMax, outputMax, test.inputMax, test.outputMax)
			}
			if got := ProfileApprovedModel(test.profile); got != test.approved {
				t.Fatalf("approved model = %q, want %q", got, test.approved)
			}
			if got := ProfileAllowsModel(test.profile, QwenEmbedding); got != test.allowsEmbed {
				t.Fatalf("allows the embedding model = %v, want %v", got, test.allowsEmbed)
			}
			if got := ProfileAllowsModel(test.profile, test.approved); got != test.allowsApproved {
				t.Fatalf("allows its own approved model = %v, want %v", got, test.allowsApproved)
			}
			if got := ProfilePriced(test.profile); got != test.priced {
				t.Fatalf("priced = %v, want %v", got, test.priced)
			}
			_, money, _ := ProfileLimits(test.profile)
			if money != test.moneyCeiling {
				t.Fatalf("money ceiling = %d, want %d", money, test.moneyCeiling)
			}
		})
	}
}

// The chat profiles still refuse the embedding model. That refusal is WHY
// this profile exists, and a new profile must not quietly take it away.
func TestTheChatProfilesStillRefuseTheEmbeddingModel(t *testing.T) {
	for _, profile := range []string{QwenProfile, QwenProfileV2} {
		budget := New(profile, filepath.Join(t.TempDir(), "calls.json"))
		if _, err := budget.Reserve(context.Background(), QwenEmbedding, 128, 0); !errors.Is(err, ErrWrongModel) {
			t.Fatalf("profile %q reserved the embedding model: %v", profile, err)
		}
	}
}
