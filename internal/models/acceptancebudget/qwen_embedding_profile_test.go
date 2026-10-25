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
		totalMax       int
		backstopCNY    int
		approved       string
		allowedModels  []string
		allowsEmbed    bool
		allowsApproved bool
		priced         bool
		moneyCeiling   int64
	}{
		{Profile, MaxCalls, 0, InputMax, OutputMax, InputMax + OutputMax, HumanBackstopCNY,
			DeepSeekModel, []string{DeepSeekModel, QwenEmbedding}, true, true, true, MaxBudgetNanoCNY},
		{CostOnlyProfile, 0, 0, InputMax, OutputMax, InputMax + OutputMax, 20,
			DeepSeekModel, []string{DeepSeekModel, QwenEmbedding, QwenEmbeddingFlash}, true, true, true, CostOnlyMaxBudgetNanoCNY},
		{QwenProfile, QwenMaxCalls, 0, QwenInputMax, QwenOutputMax, QwenTotalMax, 0,
			QwenChatModel, []string{QwenChatModel}, false, true, false, 0},
		{QwenProfileV2, QwenV2MaxCalls, QwenV2CumulativeTokenMax, QwenInputMax, QwenOutputMax, QwenTotalMax, 0,
			QwenChatModel, []string{QwenChatModel}, false, true, false, 0},
	}
	for _, test := range cases {
		t.Run(test.profile, func(t *testing.T) {
			if got := ProfileMaxCalls(test.profile); got != test.maxCalls {
				t.Fatalf("call ceiling = %d, want %d", got, test.maxCalls)
			}
			if got := ProfileCumulativeTokenMax(test.profile); got != test.cumulative {
				t.Fatalf("cumulative ceiling = %d, want %d", got, test.cumulative)
			}
			inputMax, outputMax, totalMax := ProfileTokenLimits(test.profile)
			if inputMax != test.inputMax || outputMax != test.outputMax || totalMax != test.totalMax {
				t.Fatalf("token ceilings = (%d, %d, %d), want (%d, %d, %d)",
					inputMax, outputMax, totalMax,
					test.inputMax, test.outputMax, test.totalMax)
			}
			// The human backstop is part of the profile's numbers too: it is
			// the amount a person must be willing to stand behind.
			_, _, backstop := ProfileLimits(test.profile)
			if backstop != test.backstopCNY {
				t.Fatalf("human backstop = %d CNY, want %d", backstop, test.backstopCNY)
			}
			// The whole allowlist, not one member of it.
			for _, model := range []string{
				DeepSeekModel, QwenChatModel, QwenEmbedding, QwenEmbeddingFlash,
				"qwen3.8-flash-plus", "",
			} {
				want := false
				for _, allowed := range test.allowedModels {
					if model == allowed {
						want = true
						break
					}
				}
				if got := ProfileAllowsModel(test.profile, model); got != want {
					t.Fatalf("allows %q = %v, want %v", model, got, want)
				}
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
	// The legacy human backstop is a CONSTANT, and the table reads it: a
	// change to the constant would move every profile's backstop together,
	// and the table assertion above would not notice. Its literal value is
	// pinned here so that it cannot drift unnoticed.
	if HumanBackstopCNY != 5 {
		t.Fatalf("the legacy human backstop = %d CNY, want 5", HumanBackstopCNY)
	}
}

// The authorization names four calls, eight thousand tokens and a sixteen
// thousand token input ceiling.
//
// Those numbers are the CONTRACT, and a test that compared the profile
// against the constants would keep passing after somebody changed a
// constant: the profile and the test would move together. So the literals
// are pinned here, against the constants, and the constants are pinned
// against the profile by the ledger test above.
func TestTheEmbeddingProfileCarriesTheAuthorizedNumbers(t *testing.T) {
	if QwenEmbeddingProfile != "lingdoc-qwen-embedding-budget-20261025" {
		t.Fatalf("profile name = %q, want the authorized name", QwenEmbeddingProfile)
	}
	if QwenEmbeddingMaxCalls != 4 {
		t.Fatalf("call ceiling = %d, want the authorized 4", QwenEmbeddingMaxCalls)
	}
	if QwenEmbeddingCumulativeTokenMax != 8000 {
		t.Fatalf("cumulative token ceiling = %d, want the authorized 8000", QwenEmbeddingCumulativeTokenMax)
	}
	if QwenEmbeddingInputMax != 16000 {
		t.Fatalf("input ceiling = %d, want the authorized 16000", QwenEmbeddingInputMax)
	}
	if QwenEmbeddingOutputMax != 0 {
		t.Fatalf("output ceiling = %d, want 0 (an embedding call produces no output tokens)", QwenEmbeddingOutputMax)
	}
	if QwenEmbeddingTotalMax != 16000 {
		t.Fatalf("total ceiling = %d, want 16000", QwenEmbeddingTotalMax)
	}
	// The money ceiling is the money value of ONE maximum-size call at the
	// embedding input price: 16,000 x 500 nano-CNY.
	if QwenEmbeddingMaxReserveNanoCNY != 8_000_000 {
		t.Fatalf("money ceiling = %d, want 8000000", QwenEmbeddingMaxReserveNanoCNY)
	}
	if QwenEmbeddingBackstopCNY != 1 {
		t.Fatalf("human backstop = %d CNY, want 1", QwenEmbeddingBackstopCNY)
	}
	// The table must carry the constants, not a copy of them that can drift.
	inputMax, outputMax, totalMax := ProfileTokenLimits(QwenEmbeddingProfile)
	if inputMax != QwenEmbeddingInputMax || outputMax != QwenEmbeddingOutputMax ||
		totalMax != QwenEmbeddingTotalMax {
		t.Fatalf("table token ceilings = (%d, %d, %d), want (%d, %d, %d)",
			inputMax, outputMax, totalMax,
			QwenEmbeddingInputMax, QwenEmbeddingOutputMax, QwenEmbeddingTotalMax)
	}
	if ProfileMaxCalls(QwenEmbeddingProfile) != QwenEmbeddingMaxCalls {
		t.Fatalf("table call ceiling = %d, want %d",
			ProfileMaxCalls(QwenEmbeddingProfile), QwenEmbeddingMaxCalls)
	}
	if ProfileCumulativeTokenMax(QwenEmbeddingProfile) != QwenEmbeddingCumulativeTokenMax {
		t.Fatalf("table cumulative ceiling = %d, want %d",
			ProfileCumulativeTokenMax(QwenEmbeddingProfile), QwenEmbeddingCumulativeTokenMax)
	}
	_, money, _ := ProfileLimits(QwenEmbeddingProfile)
	if money != QwenEmbeddingMaxReserveNanoCNY {
		t.Fatalf("table money ceiling = %d, want %d", money, QwenEmbeddingMaxReserveNanoCNY)
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

// A profile that approves an embedding model approves NO chat model.
//
// This is the answer the chat path asks before it reserves anything: the
// reservation uses the profile's APPROVED model, so a chat request under an
// embedding profile would otherwise be reserved -- and counted -- as an
// embedding call, and the provider would answer a chat request that no chat
// budget was written for.
//
// The existing profiles are unaffected: they all approve a chat model.
func TestOnlyTheEmbeddingProfileApprovesNoChatModel(t *testing.T) {
	if model, ok := ProfileApprovedChatModel(QwenEmbeddingProfile); ok {
		t.Fatalf("the embedding profile approves the chat model %q", model)
	}
	for _, profile := range []string{Profile, CostOnlyProfile, QwenProfile, QwenProfileV2} {
		model, ok := ProfileApprovedChatModel(profile)
		if !ok {
			t.Fatalf("profile %q approves no chat model", profile)
		}
		if model != ProfileApprovedModel(profile) {
			t.Fatalf("profile %q approves the chat model %q, want %q",
				profile, model, ProfileApprovedModel(profile))
		}
	}
}
