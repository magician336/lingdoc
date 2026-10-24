package acceptancebudget

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// The second Qwen profile.
//
// It exists because a NEW authorization is a NEW profile. The first profile
// keeps its meaning, its call count and its ledger; this one carries its own
// call count and its own cumulative token ceiling.
//
// The tests below are offline. They never reach a provider: they exercise the
// reservation, the ledger and the refusal, which is where the whole budget
// gate lives.

// qwenV2LedgerWithCeiling writes a v2 ledger whose cumulative token ceiling
// is LOWERED to the given number.
//
// Lowering the ceiling of one window is what an operator may do; the
// profile's number stays the hard maximum. It is also how the gate is
// exercised without editing the source: a window that only fits one call
// makes the refusal observable, which a 90,000 ceiling and four calls of at
// most 20,096 tokens cannot do (4 * 20,096 = 80,384 <= 90,000).
func qwenV2LedgerWithCeiling(t *testing.T, path string, ceiling int) {
	t.Helper()
	ledger := freshLedger(QwenProfileV2)
	ledger.CumulativeTokenMax = ceiling
	if err := writeLedger(path, ledger); err != nil {
		t.Fatalf("writeLedger() error = %v", err)
	}
}

// The second profile carries its own numbers and shares the first profile's
// single-call limits.
func TestQwenV2ProfileCarriesItsOwnCallCountAndCumulativeCeiling(t *testing.T) {
	if ProfileMaxCalls(QwenProfileV2) != 4 {
		t.Fatalf("ProfileMaxCalls(v2) = %d, want 4", ProfileMaxCalls(QwenProfileV2))
	}
	if ProfileCumulativeTokenMax(QwenProfileV2) != 90000 {
		t.Fatalf("ProfileCumulativeTokenMax(v2) = %d, want 90000", ProfileCumulativeTokenMax(QwenProfileV2))
	}
	inputMax, outputMax, totalMax := ProfileTokenLimits(QwenProfileV2)
	if inputMax != QwenInputMax || outputMax != QwenOutputMax || totalMax != QwenTotalMax {
		t.Fatalf("v2 token limits = %d/%d/%d, want the first profile's %d/%d/%d",
			inputMax, outputMax, totalMax, QwenInputMax, QwenOutputMax, QwenTotalMax)
	}
	if ProfileCostBasis(QwenProfileV2) != CostBasisNotApplicable || ProfilePriced(QwenProfileV2) {
		t.Fatalf("v2 cost basis = %q, priced = %v; want %q and unpriced",
			ProfileCostBasis(QwenProfileV2), ProfilePriced(QwenProfileV2), CostBasisNotApplicable)
	}
	if ProfileApprovedModel(QwenProfileV2) != QwenChatModel {
		t.Fatalf("ProfileApprovedModel(v2) = %q, want %q", ProfileApprovedModel(QwenProfileV2), QwenChatModel)
	}
	for _, model := range []string{DeepSeekModel, QwenEmbedding, QwenEmbeddingFlash, "qwen3.8-flash-plus"} {
		if ProfileAllowsModel(QwenProfileV2, model) {
			t.Fatalf("the v2 profile approved %q", model)
		}
	}
	if !ProfileAllowsModel(QwenProfileV2, QwenChatModel) {
		t.Fatal("the v2 profile refused its own approved model")
	}
}

// The first profile's meaning did not move.
//
// This is the "the old profile is unchanged" proof at the configuration
// level: the same numbers, the same cost basis, the same approved model, the
// same allowlist, the same single call -- and NO cumulative token ceiling,
// because a cumulative ceiling is the second profile's new thing and adding
// one to the first profile would change what an old ledger means.
func TestQwenV1ProfileKeepsItsOwnNumbers(t *testing.T) {
	if ProfileMaxCalls(QwenProfile) != 1 {
		t.Fatalf("ProfileMaxCalls(v1) = %d, want 1", ProfileMaxCalls(QwenProfile))
	}
	if ProfileCumulativeTokenMax(QwenProfile) != 0 {
		t.Fatalf("ProfileCumulativeTokenMax(v1) = %d, want 0: the first profile has no cumulative ceiling",
			ProfileCumulativeTokenMax(QwenProfile))
	}
	inputMax, outputMax, totalMax := ProfileTokenLimits(QwenProfile)
	if inputMax != 16000 || outputMax != 4096 || totalMax != 20096 {
		t.Fatalf("v1 token limits = %d/%d/%d, want 16000/4096/20096", inputMax, outputMax, totalMax)
	}
	if ProfileCostBasis(QwenProfile) != CostBasisNotApplicable || ProfilePriced(QwenProfile) {
		t.Fatalf("v1 cost basis = %q, priced = %v; want %q and unpriced",
			ProfileCostBasis(QwenProfile), ProfilePriced(QwenProfile), CostBasisNotApplicable)
	}
	if ProfileApprovedModel(QwenProfile) != QwenChatModel {
		t.Fatalf("ProfileApprovedModel(v1) = %q, want %q", ProfileApprovedModel(QwenProfile), QwenChatModel)
	}
	if !ProfileAllowsModel(QwenProfile, QwenChatModel) {
		t.Fatal("the v1 profile refused its own approved model")
	}
	for _, model := range []string{DeepSeekModel, QwenEmbedding, QwenEmbeddingFlash} {
		if ProfileAllowsModel(QwenProfile, model) {
			t.Fatalf("the v1 profile approved %q", model)
		}
	}
}

// The two profiles differ in exactly two numbers: the call count and the
// cumulative token ceiling. Everything else -- the single-call limits, the
// cost basis, the approved model, the allowlist -- is the same, because the
// second authorization loosens nothing about one call.
func TestQwenV2DiffersFromV1OnlyInTheCallCountAndTheCumulativeCeiling(t *testing.T) {
	v1Input, v1Output, v1Total := ProfileTokenLimits(QwenProfile)
	v2Input, v2Output, v2Total := ProfileTokenLimits(QwenProfileV2)
	if v1Input != v2Input || v1Output != v2Output || v1Total != v2Total {
		t.Fatalf("the single-call limits differ: v1 %d/%d/%d, v2 %d/%d/%d",
			v1Input, v1Output, v1Total, v2Input, v2Output, v2Total)
	}
	if ProfileCostBasis(QwenProfile) != ProfileCostBasis(QwenProfileV2) {
		t.Fatal("the cost basis differs")
	}
	if ProfileApprovedModel(QwenProfile) != ProfileApprovedModel(QwenProfileV2) {
		t.Fatal("the approved model differs")
	}
	if ProfileMaxCalls(QwenProfile) == ProfileMaxCalls(QwenProfileV2) {
		t.Fatal("the call counts must differ: that is the new authorization")
	}
	if ProfileCumulativeTokenMax(QwenProfile) != 0 {
		t.Fatal("the first profile must not carry a cumulative ceiling")
	}
}

// The whole authorized window fits inside the ceiling.
//
// Four calls of at most 16,000 + 4,096 = 20,096 tokens reserve at most
// 4 * 20,096 = 80,384 tokens, and 80,384 <= 90,000. The implementation can
// therefore execute the four calls it is authorized to make, and the ceiling
// is a second line of defence behind the call count rather than a limit that
// stops the window early.
//
// The numbers are literals: the arithmetic is the claim, and an arithmetic
// written against the constants it proves proves nothing.
func TestQwenV2FourCallsFitInsideTheCumulativeCeiling(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.json")
	budget := New(QwenProfileV2, path)

	for call := 1; call <= 4; call++ {
		reservation, err := budget.Reserve(context.Background(), QwenChatModel, QwenInputMax, QwenOutputMax)
		if err != nil {
			t.Fatalf("reserve %d error = %v, want the authorized call to be allowed", call, err)
		}
		if err := reservation.Complete("response_received", 1024, 512, "stop", false); err != nil {
			t.Fatalf("complete %d error = %v", call, err)
		}
	}

	ledger, err := readLedger(path, QwenProfileV2)
	if err != nil {
		t.Fatal(err)
	}
	const perCall = 16000 + 4096
	const window = 4 * perCall
	if ledger.ReservedTokens != window {
		t.Fatalf("cumulative reservation = %d, want %d", ledger.ReservedTokens, window)
	}
	if window != 80384 {
		t.Fatalf("the arithmetic changed: 4 * %d = %d, want 80384", perCall, window)
	}
	if window > 90000 {
		t.Fatalf("the authorized window reserves %d tokens, which does not fit inside the ceiling 90000", window)
	}
	if !validLedger(ledger, QwenProfileV2) {
		t.Fatalf("the v2 ledger is not valid: %+v", ledger)
	}

	// The fifth call is refused: the call count is exhausted.
	if _, err := budget.Reserve(context.Background(), QwenChatModel, QwenInputMax, QwenOutputMax); !errors.Is(err, ErrCallsSpent) {
		t.Fatalf("fifth reserve error = %v, want the call budget to be exhausted", err)
	}
}

// The cumulative gate refuses BEFORE the request leaves the process.
//
// The window is given a ceiling of one call, so the second reservation would
// pass it. The refusal happens inside Reserve, so the caller never receives
// a reservation and the request cannot reach a provider.
func TestQwenV2CumulativeGateRefusesTheRequestThatWouldPassTheCeiling(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.json")
	qwenV2LedgerWithCeiling(t, path, QwenInputMax+QwenOutputMax)
	budget := New(QwenProfileV2, path)

	first, err := budget.Reserve(context.Background(), QwenChatModel, QwenInputMax, QwenOutputMax)
	if err != nil {
		t.Fatalf("first reserve error = %v", err)
	}
	if err := first.Complete("response_received", 1024, 512, "stop", false); err != nil {
		t.Fatalf("complete error = %v", err)
	}

	before := len(readLedgerRequests(t, path))
	if _, err := budget.Reserve(context.Background(), QwenChatModel, QwenInputMax, QwenOutputMax); !errors.Is(err, ErrTokenBudgetExhausted) {
		t.Fatalf("second reserve error = %v, want the cumulative token budget to be exhausted", err)
	}
	if after := len(readLedgerRequests(t, path)); after != before {
		t.Fatalf("recorded requests = %d, want %d: a refused reservation must not be recorded", after, before)
	}
}

// readLedgerRequests returns the recorded requests of one ledger file.
func readLedgerRequests(t *testing.T, path string) []Request {
	t.Helper()
	ledger, err := readLedger(path, QwenProfileV2)
	if err != nil {
		t.Fatal(err)
	}
	return ledger.Requests
}

// A ledger may TIGHTEN the profile's ceiling but never RAISE it.
//
// The profile's number is the hard maximum. A ledger that claims a bigger
// one is refused rather than believed: a window that could raise its own
// budget would not have a budget.
func TestQwenV2LedgerWithARaisedCeilingIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.json")
	ledger := freshLedger(QwenProfileV2)
	ledger.CumulativeTokenMax = QwenV2CumulativeTokenMax + 1
	if err := writeLedger(path, ledger); err != nil {
		t.Fatal(err)
	}
	if _, err := New(QwenProfileV2, path).Reserve(context.Background(), QwenChatModel, 1024, 512); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("reserve error = %v, want a ledger that raises its own ceiling to be refused", err)
	}
}

// A ledger without a ceiling is refused for a profile that has one: such a
// window would run without the gate.
func TestQwenV2LedgerWithoutACeilingIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.json")
	ledger := freshLedger(QwenProfileV2)
	ledger.CumulativeTokenMax = 0
	if err := writeLedger(path, ledger); err != nil {
		t.Fatal(err)
	}
	if _, err := New(QwenProfileV2, path).Reserve(context.Background(), QwenChatModel, 1024, 512); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("reserve error = %v, want a ledger without a ceiling to be refused", err)
	}
}

// The cumulative counter cannot drift away from the ledger's own history.
//
// The aggregate is recomputed from the request rows, so a ledger whose
// counter disagrees with its rows is refused. Without that check, editing
// the counter would edit the budget.
func TestQwenV2LedgerWithADisagreeingCounterIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.json")
	budget := New(QwenProfileV2, path)
	reservation, err := budget.Reserve(context.Background(), QwenChatModel, 2048, QwenOutputMax)
	if err != nil {
		t.Fatalf("reserve error = %v", err)
	}
	if err := reservation.Complete("response_received", 900, 400, "stop", false); err != nil {
		t.Fatalf("complete error = %v", err)
	}

	ledger, err := readLedger(path, QwenProfileV2)
	if err != nil {
		t.Fatal(err)
	}
	ledger.ReservedTokens = 1
	if err := writeLedger(path, ledger); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.Reserve(context.Background(), QwenChatModel, 2048, QwenOutputMax); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("reserve error = %v, want a ledger whose counter disagrees with its rows to be refused", err)
	}
}

// The three token numbers are kept apart.
//
// The reservation says how many tokens the window could still consume. The
// usage says how many the provider reported. The remainder is a number
// against one of those two counts, and a reader has to say which one they
// mean.
func TestQwenV2KeepsReservationUsageAndRemainingApart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.json")
	budget := New(QwenProfileV2, path)
	reservation, err := budget.Reserve(context.Background(), QwenChatModel, 2048, QwenOutputMax)
	if err != nil {
		t.Fatalf("reserve error = %v", err)
	}
	if err := reservation.CompleteUsage("response_received", Usage{
		InputTokens: 900, OutputTokens: 400, TotalTokens: 1300, Reported: true,
	}, "stop", false); err != nil {
		t.Fatalf("complete error = %v", err)
	}

	ledger, err := readLedger(path, QwenProfileV2)
	if err != nil {
		t.Fatal(err)
	}
	reserved := 2048 + QwenOutputMax
	if ledger.ReservedTokens != reserved {
		t.Fatalf("cumulative reservation = %d, want %d", ledger.ReservedTokens, reserved)
	}
	if ledger.ActualTokens != 1300 {
		t.Fatalf("cumulative reported usage = %d, want the provider's 1300", ledger.ActualTokens)
	}
	if ledger.ReservedTokens == ledger.ActualTokens {
		t.Fatal("the reservation and the reported usage must not be the same number")
	}

	state := ledger.TokenBudgetState()
	if state.Ceiling != QwenV2CumulativeTokenMax {
		t.Fatalf("ceiling = %d, want %d", state.Ceiling, QwenV2CumulativeTokenMax)
	}
	if state.RemainingReserved != QwenV2CumulativeTokenMax-reserved {
		t.Fatalf("remaining against the reservation = %d, want %d",
			state.RemainingReserved, QwenV2CumulativeTokenMax-reserved)
	}
	if state.RemainingActual == nil {
		t.Fatal("the remainder against the reported usage must be known when the usage is reported")
	}
	if *state.RemainingActual != QwenV2CumulativeTokenMax-1300 {
		t.Fatalf("remaining against the reported usage = %d, want %d",
			*state.RemainingActual, QwenV2CumulativeTokenMax-1300)
	}
	if state.UsageUnverifiable {
		t.Fatal("a fully reported usage must not be flagged as unverifiable")
	}
}

// A usage that could not be verified is not a smaller usage.
//
// It is an UNKNOWN usage, so the window cannot prove that the next call
// stays inside the ceiling, and the next reservation is refused. The
// counters stay empty: nobody fills an unknown usage with a 0 and calls it
// a measurement.
func TestQwenV2UnverifiableUsageStopsTheWindow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.json")
	budget := New(QwenProfileV2, path)
	reservation, err := budget.Reserve(context.Background(), QwenChatModel, 2048, QwenOutputMax)
	if err != nil {
		t.Fatalf("reserve error = %v", err)
	}
	if err := reservation.CompleteUsage("response_received", Usage{Reported: false}, "stop", false); err != nil {
		t.Fatalf("complete error = %v", err)
	}

	ledger, err := readLedger(path, QwenProfileV2)
	if err != nil {
		t.Fatal(err)
	}
	request := ledger.Requests[0]
	if request.UsageStatus != UsageStatusUnavailable {
		t.Fatalf("recorded usage status = %q, want %q", request.UsageStatus, UsageStatusUnavailable)
	}
	if request.InputTokens != 0 || request.OutputTokens != 0 || request.TotalTokens != 0 {
		t.Fatalf("an unverifiable usage must leave the counters empty: %+v", request)
	}
	if ledger.ActualTokens != 0 {
		t.Fatalf("cumulative reported usage = %d, want 0: an unverifiable usage is not a usage", ledger.ActualTokens)
	}
	if !ledger.TokenBudgetState().UsageUnverifiable {
		t.Fatal("the window must report that its usage is unverifiable")
	}
	if ledger.TokenBudgetState().RemainingActual != nil {
		t.Fatal("the remainder against the usage is UNKNOWN while a usage is unverifiable")
	}

	if _, err := budget.Reserve(context.Background(), QwenChatModel, 2048, QwenOutputMax); !errors.Is(err, ErrTokenBudgetExhausted) {
		t.Fatalf("reserve after an unverifiable usage error = %v, want the window to be stopped", err)
	}
}

// Two reservations that arrive at the same time cannot both pass the gate.
//
// The window has room for ONE call, so exactly one of the two may be
// reserved. The ledger lock serializes the read, the decision and the
// write, so the second one reads the counter the first one wrote and is
// refused.
func TestQwenV2ConcurrentReservesCannotBothPassTheCumulativeGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.json")
	qwenV2LedgerWithCeiling(t, path, QwenInputMax+QwenOutputMax)
	budget := New(QwenProfileV2, path)

	type outcome struct {
		err error
	}
	results := make(chan outcome, 2)
	var wait sync.WaitGroup
	for attempt := 0; attempt < 2; attempt++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			reservation, err := budget.Reserve(context.Background(), QwenChatModel, QwenInputMax, QwenOutputMax)
			if err == nil {
				// The holder releases the ledger lock.
				_ = reservation.Complete("response_received", 1024, 512, "stop", false)
			}
			results <- outcome{err: err}
		}()
	}
	wait.Wait()
	close(results)

	succeeded, refused := 0, 0
	for result := range results {
		switch {
		case result.err == nil:
			succeeded++
		case errors.Is(result.err, ErrTokenBudgetExhausted):
			refused++
		default:
			t.Fatalf("unexpected reserve error = %v", result.err)
		}
	}
	if succeeded != 1 || refused != 1 {
		t.Fatalf("succeeded = %d, refused = %d; want exactly one of each", succeeded, refused)
	}

	ledger, err := readLedger(path, QwenProfileV2)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Requests) != 1 {
		t.Fatalf("recorded requests = %d, want exactly 1: the refused reservation must not be recorded", len(ledger.Requests))
	}
}

// The first profile's ledger file does not carry the second profile's
// fields.
//
// The cumulative counters exist for the profiles that HAVE a cumulative
// ceiling. The first profile does not, so its ledger stays byte for byte
// what it was before the counters existed: a reader of an old ledger sees
// exactly the old keys.
func TestQwenV1LedgerCarriesNoCumulativeTokenFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.json")
	budget := New(QwenProfile, path)
	reservation, err := budget.Reserve(context.Background(), QwenChatModel, 2048, QwenOutputMax)
	if err != nil {
		t.Fatalf("reserve error = %v", err)
	}
	if err := reservation.Complete("response_received", 900, 400, "stop", false); err != nil {
		t.Fatalf("complete error = %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"cumulative_token_max", "reserved_tokens", "actual_tokens"} {
		if _, ok := raw[key]; ok {
			t.Fatalf("the v1 ledger must not carry %q: %s", key, data)
		}
	}
	if !validLedger(mustReadLedger(t, path), QwenProfile) {
		t.Fatal("the v1 ledger must stay valid")
	}
}

func mustReadLedger(t *testing.T, path string) Ledger {
	t.Helper()
	ledger, err := readLedger(path, QwenProfile)
	if err != nil {
		t.Fatal(err)
	}
	return ledger
}
