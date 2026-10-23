package acceptancebudget

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The Qwen profile controls tokens and call count. It computes no money, and
// every record it writes says so.

func TestQwenProfileReservesOneCallAndRefusesTheSecond(t *testing.T) {
	budget := New(QwenProfile, filepath.Join(t.TempDir(), "calls.json"))
	reservation, err := budget.Reserve(context.Background(), QwenChatModel, 2048, 4096)
	if err != nil {
		t.Fatalf("first reserve error = %v", err)
	}
	if err := reservation.Complete("response_received", 1024, 512, "stop", false); err != nil {
		t.Fatalf("complete error = %v", err)
	}
	if _, err := budget.Reserve(context.Background(), QwenChatModel, 2048, 4096); !errors.Is(err, ErrCallsSpent) {
		t.Fatalf("second reserve error = %v, want the call budget to be exhausted", err)
	}
}

// The profile's model allowlist is the gate: nothing else may be reserved,
// so the embedding, summary and background paths cannot reach a provider
// while the controlled profile is armed.
func TestQwenProfileRefusesEveryModelItDidNotApprove(t *testing.T) {
	budget := New(QwenProfile, filepath.Join(t.TempDir(), "calls.json"))
	for _, model := range []string{DeepSeekModel, QwenEmbedding, QwenEmbeddingFlash, "qwen3.8-flash-plus"} {
		if _, err := budget.Reserve(context.Background(), model, 128, 0); !errors.Is(err, ErrWrongModel) {
			t.Fatalf("reserve(%q) error = %v, want the model to be refused", model, err)
		}
	}
}

// The limits are the profile's: the input ceiling, the output ceiling and
// the sum of the two.
func TestQwenProfileEnforcesTheTokenCeilings(t *testing.T) {
	budget := New(QwenProfile, filepath.Join(t.TempDir(), "calls.json"))
	if _, err := budget.Reserve(context.Background(), QwenChatModel, QwenInputMax+1, 0); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("oversized input error = %v, want the input ceiling", err)
	}
	if _, err := budget.Reserve(context.Background(), QwenChatModel, 0, QwenOutputMax+1); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("oversized output error = %v, want the output ceiling", err)
	}
	if _, err := budget.Reserve(context.Background(), QwenChatModel, QwenInputMax, QwenOutputMax+1); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("oversized sum error = %v, want the sum ceiling", err)
	}
}

// The record says what it is: the tokens the provider reported, the usage
// status, the cost basis, and no money.
func TestQwenProfileRecordCarriesTokensAndNoMoney(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.json")
	budget := New(QwenProfile, path)
	reservation, err := budget.Reserve(context.Background(), QwenChatModel, 2048, QwenOutputMax)
	if err != nil {
		t.Fatalf("reserve error = %v", err)
	}
	if err := reservation.CompleteUsage("response_received", Usage{
		InputTokens: 900, OutputTokens: 400, TotalTokens: 1300, Reported: true,
	}, "stop", false); err != nil {
		t.Fatalf("complete error = %v", err)
	}
	ledger, err := readLedger(path, QwenProfile)
	if err != nil {
		t.Fatal(err)
	}
	if !validLedger(ledger, QwenProfile) {
		t.Fatalf("the Qwen ledger is not valid: %+v", ledger)
	}
	if ledger.CostBasis != CostBasisNotApplicable {
		t.Fatalf("ledger cost basis = %q, want %q", ledger.CostBasis, CostBasisNotApplicable)
	}
	if len(ledger.Requests) != 1 {
		t.Fatalf("recorded requests = %d, want 1", len(ledger.Requests))
	}
	request := ledger.Requests[0]
	if request.InputTokens != 900 || request.OutputTokens != 400 || request.TotalTokens != 1300 {
		t.Fatalf("recorded tokens = %d/%d/%d, want 900/400/1300",
			request.InputTokens, request.OutputTokens, request.TotalTokens)
	}
	if request.UsageStatus != UsageStatusReported {
		t.Fatalf("recorded usage status = %q, want %q", request.UsageStatus, UsageStatusReported)
	}
	if request.ActualNanoCNY != 0 || request.ActualNanoUSD != 0 || request.ReservedNanoCNY != 0 || request.ReservedNanoUSD != 0 {
		t.Fatalf("a profile without a price basis must record no money: %+v", request)
	}
	if request.CostBasis != CostBasisNotApplicable {
		t.Fatalf("request cost basis = %q, want %q", request.CostBasis, CostBasisNotApplicable)
	}
}

// A provider that reported no usage is recorded as unavailable, and the
// counters stay empty. The estimate of the request is not written as an
// actual usage.
func TestQwenProfileUnknownUsageIsRecordedAsUnavailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.json")
	budget := New(QwenProfile, path)
	reservation, err := budget.Reserve(context.Background(), QwenChatModel, 2048, QwenOutputMax)
	if err != nil {
		t.Fatalf("reserve error = %v", err)
	}
	if err := reservation.Unknown("usage_unknown"); !errors.Is(err, ErrCallsSpent) {
		t.Fatalf("unknown error = %v, want the ledger to be blocked", err)
	}
	ledger, err := readLedger(path, QwenProfile)
	if err != nil {
		t.Fatal(err)
	}
	request := ledger.Requests[0]
	if request.UsageStatus != UsageStatusUnavailable {
		t.Fatalf("recorded usage status = %q, want %q", request.UsageStatus, UsageStatusUnavailable)
	}
	if request.InputTokens != 0 || request.OutputTokens != 0 || request.TotalTokens != 0 {
		t.Fatalf("a missing usage must leave the counters empty: %+v", request)
	}
}

// A counter that cannot be true does not become an amount.
//
// The Qwen profile has no price basis, so there is nothing to compute -- not
// even for an impossible (negative) counter. A priced profile keeps its own
// behaviour: an impossible counter there is refused, and the ledger's
// reserved+1 marker says "over the reservation".
func TestQwenProfileNegativeCounterStaysUnpriced(t *testing.T) {
	cny, usd, err := actualAmounts(QwenChatModel, -1, -1)
	if err != nil || cny != 0 || usd != 0 {
		t.Fatalf("actualAmounts(QwenChatModel, -1, -1) = (%d, %d, %v), want (0, 0, nil)", cny, usd, err)
	}
	if _, _, err := actualAmounts(DeepSeekModel, -1, -1); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("actualAmounts(DeepSeekModel, -1, -1) error = %v, want the impossible counter to be refused", err)
	}

	path := filepath.Join(t.TempDir(), "calls.json")
	budget := New(QwenProfile, path)
	reservation, err := budget.Reserve(context.Background(), QwenChatModel, 2048, QwenOutputMax)
	if err != nil {
		t.Fatalf("reserve error = %v", err)
	}
	if err := reservation.CompleteUsage("response_received",
		Usage{InputTokens: -1, OutputTokens: -1, Reported: true}, "stop", false); err == nil {
		t.Fatal("an impossible counter must not settle as a normal response")
	}
	ledger, err := readLedger(path, QwenProfile)
	if err != nil {
		t.Fatal(err)
	}
	if ledger.ActualNanoCNY != 0 || ledger.ActualNanoUSD != 0 {
		t.Fatalf("a profile without a price basis must record no amount: %d/%d",
			ledger.ActualNanoCNY, ledger.ActualNanoUSD)
	}
	if len(ledger.Requests) != 1 || ledger.Requests[0].Status != "limit_violation" {
		t.Fatalf("the impossible counter must be recorded as a violation: %+v", ledger.Requests)
	}
}

// A ledger written before the new fields existed must still read. The
// fields it does not carry are read as the priced basis and as "reported",
// which is what those ledgers meant.
func TestLegacyLedgerWithoutTheNewFieldsStillReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.json")
	legacy := `{
  "schema_version": 1,
  "profile": "lingdoc-t10-deepseek-flash-20260929-v1",
  "max_calls": 6,
  "input_max_tokens": 16000,
  "output_max_tokens": 6000,
  "max_concurrency": 1,
  "usd_to_cny_reserve_rate": 10,
  "max_reserve_nano_cny": 5000000000,
  "reserved_nano_cny": 78144000,
  "actual_nano_cny": 900000,
  "reserved_nano_usd": 7814400,
  "actual_nano_usd": 90000,
  "blocked": false,
  "requests": [
    {
      "number": 1,
      "model": "deepseek-flash",
      "status": "response_received",
      "input_upper_bound_tokens": 2048,
      "output_limit_tokens": 6000,
      "input_tokens": 100,
      "output_tokens": 50,
      "reserved_nano_cny": 78144000,
      "actual_nano_cny": 900000,
      "reserved_nano_usd": 7814400,
      "actual_nano_usd": 90000,
      "created_at": "2026-10-01T00:00:00Z"
    }
  ]
}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	ledger, err := readLedger(path, Profile)
	if err != nil {
		t.Fatalf("a ledger written before the new fields must still read: %v", err)
	}
	if !validLedger(ledger, Profile) {
		t.Fatalf("a legacy ledger must stay valid: %+v", ledger)
	}
	if len(ledger.Requests) != 1 || ledger.Requests[0].UsageStatus != "" {
		t.Fatalf("legacy request changed: %+v", ledger.Requests)
	}
	// The file on disk is unchanged: reading a ledger never rewrites it.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var reread map[string]any
	if err := json.Unmarshal(after, &reread); err != nil {
		t.Fatal(err)
	}
	if _, ok := reread["cost_basis"]; ok {
		t.Fatal("reading a legacy ledger must not add fields to it")
	}
}
