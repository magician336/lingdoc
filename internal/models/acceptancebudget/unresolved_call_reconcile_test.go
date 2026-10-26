package acceptancebudget

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The controlled budget window must be able to RECOVER from an outcome nobody
// can confirm.
//
// Before #675, only the provider's HTTP error had a recovery channel. Every
// other unconfirmable outcome -- a failed transport, a missing usage, a crash
// between the reservation and the completion, a cancelled reservation -- left
// hasUnresolved true and ledger.Blocked true for ever, so Reserve answered
// ErrBlocked for the rest of the window's life: the authorized budget (four
// calls, eight thousand tokens) was retired by the FIRST transport hiccup,
// with no legitimate way back.
//
// These tests pin the recovery and the conservative invariants it has to
// keep: nothing is refunded, no history is rewritten, the settlement is bound
// to one exact snapshot, it needs an explicit confirmation, it settles
// exactly one row, and it never loosens a budget limit.

// unresolvedOutputLimit is the output limit of one call of the given model.
func unresolvedOutputLimit(model string) int {
	if model == QwenEmbedding || model == QwenEmbeddingFlash {
		return 0
	}
	return 64
}

// unresolvedWindowFixture builds a window whose LAST call ended in the given
// unconfirmable outcome, together with the operator evidence for that exact
// ledger snapshot.
func unresolvedWindowFixture(t *testing.T, profile, model, status string, priorCalls int) (*Budget, ReconcileUnresolvedCallInput) {
	t.Helper()
	b := New(profile, filepath.Join(t.TempDir(), "synthetic.calls.json"))
	for index := 0; index < priorCalls; index++ {
		done, err := b.Reserve(context.Background(), model, 128, unresolvedOutputLimit(model))
		if err != nil {
			t.Fatal(err)
		}
		if err := done.Complete("response_received", 100, 0, "", false); err != nil {
			t.Fatal(err)
		}
	}
	failed, err := b.Reserve(context.Background(), model, 256, unresolvedOutputLimit(model))
	if err != nil {
		t.Fatal(err)
	}
	if err := failed.Unknown(status); !errors.Is(err, ErrCallsSpent) {
		t.Fatalf("fixture Unknown: %v", err)
	}
	return b, unresolvedWindowEvidence(t, b, status)
}

// unresolvedWindowEvidence builds the operator evidence for one exact ledger
// snapshot, the way the CLI's evidence file does.
func unresolvedWindowEvidence(t *testing.T, b *Budget, status string) ReconcileUnresolvedCallInput {
	t.Helper()
	data := mustLedgerBytes(t, b)
	var ledger Ledger
	if err := json.Unmarshal(data, &ledger); err != nil {
		t.Fatal(err)
	}
	number := len(ledger.Requests)
	blockedReason := status
	if status == unresolvedStatusInFlight {
		// The crash shape never froze the window, so it has no block reason
		// to quote.
		blockedReason = ""
	}
	e := UnresolvedCallEvidence{
		LedgerSHA256: digestSHA256(data), RequestNumber: number,
		OriginalStatus: status, OriginalBlockedReason: blockedReason,
		EvidenceSHA256: digestSHA256([]byte("synthetic operator attestation artifact; no network")),
		Operator:       "synthetic-local-operator",
		Reason:         "Reviewed the synthetic operator attestation; accept the full reserve with provider usage unknown",
	}
	return ReconcileUnresolvedCallInput{
		ExpectedLedgerSHA256: e.LedgerSHA256, RequestNumber: number,
		ExpectedStatus: status, ExpectedBlockedReason: blockedReason,
		ExpectedReason: e.Reason, ConfirmFullReserveCharge: true, Evidence: e,
	}
}

// assertSettlementKeptTheRowIntact checks that the settlement only APPENDED
// an annotation: the row's own fields are the ones the failure produced.
func assertSettlementKeptTheRowIntact(t *testing.T, before, after Ledger, number int) *UnresolvedCallReconciliation {
	t.Helper()
	if len(after.Requests) != len(before.Requests) {
		t.Fatalf("the settlement changed the number of recorded calls: before %d after %d",
			len(before.Requests), len(after.Requests))
	}
	original := before.Requests[number-1]
	settled := after.Requests[number-1]
	proof := settled.UnresolvedReconciliation
	settled.UnresolvedReconciliation = nil
	if !reflect.DeepEqual(original, settled) {
		t.Fatalf("the settlement rewrote the failed request:\nbefore %+v\nafter  %+v", original, settled)
	}
	if proof == nil {
		t.Fatal("the settlement left no record of itself")
	}
	return proof
}

func TestTheControlledEmbeddingWindowRecoversFromAnUnconfirmableOutcome(t *testing.T) {
	for _, status := range []string{unresolvedStatusOutcomeUnknown, unresolvedStatusUsageUnknown,
		unresolvedStatusCancelReconciliationRequired} {
		t.Run(status, func(t *testing.T) {
			b, input := unresolvedWindowFixture(t, QwenEmbeddingProfile, QwenEmbedding, status, 1)

			// Before the settlement the window is frozen: every later
			// reservation is refused.
			if _, err := b.Reserve(context.Background(), QwenEmbedding, 64, 0); !errors.Is(err, ErrBlocked) {
				t.Fatalf("the frozen window reserved anyway: %v", err)
			}
			before, err := readLedger(b.ledgerPath, QwenEmbeddingProfile)
			if err != nil {
				t.Fatal(err)
			}

			if err := b.ReconcileUnresolvedCall(context.Background(), input); err != nil {
				t.Fatalf("the settlement was refused: %v", err)
			}

			after, err := readLedger(b.ledgerPath, QwenEmbeddingProfile)
			if err != nil {
				t.Fatal(err)
			}
			if after.Blocked || after.BlockedReason != "" {
				t.Fatalf("the window is still frozen after the settlement: %q", after.BlockedReason)
			}
			if hasUnresolved(after) || !validLedger(after, QwenEmbeddingProfile) {
				t.Fatal("the settled ledger is not a valid, settled ledger")
			}
			// The failed request is preserved exactly: status, counters,
			// money and usage status are the ones the failure produced.
			proof := assertSettlementKeptTheRowIntact(t, before, after, input.RequestNumber)
			// The provider's usage stays unknown and the charge is the full
			// reserved upper bound -- the conservative reading.
			if proof.Kind != unresolvedCallReconciliationKind ||
				proof.ProviderTokenUsage != unknownProviderUsage ||
				proof.ChargeBasis != fullReserveChargeBasis ||
				proof.ChargedNanoCNY != before.Requests[input.RequestNumber-1].ReservedNanoCNY ||
				proof.ChargedNanoUSD != before.Requests[input.RequestNumber-1].ReservedNanoUSD {
				t.Fatalf("the settlement proof does not carry the conservative charge: %+v", proof)
			}
			if proof.Evidence.Operator != input.Evidence.Operator ||
				proof.Evidence.RequestNumber != input.RequestNumber ||
				proof.Evidence.EvidenceSHA256 != input.Evidence.EvidenceSHA256 {
				t.Fatalf("the settlement proof does not carry the operator evidence: %+v", proof.Evidence)
			}
			// Nothing is refunded: the failed call and its money stay spent,
			// and the call keeps its place in the call count.
			if len(after.Requests) != len(before.Requests) ||
				after.ReservedNanoCNY != before.ReservedNanoCNY ||
				after.ActualNanoCNY != before.ActualNanoCNY ||
				after.ReservedNanoUSD != before.ReservedNanoUSD ||
				after.ActualNanoUSD != before.ActualNanoUSD ||
				after.ReservedTokens != before.ReservedTokens {
				t.Fatalf("the settlement refunded something: before %+v after %+v", before, after)
			}
			// And the window can be used again.
			next, err := b.Reserve(context.Background(), QwenEmbedding, 64, 0)
			if err != nil {
				t.Fatalf("the settled window still refuses reservations: %v", err)
			}
			if err := next.CancelNoCall(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestTheSettledWindowStillHonoursTheCumulativeTokenCeiling pins that the
// settlement reopens the window WITHOUT loosening a single ceiling.
func TestTheSettledWindowStillHonoursTheCumulativeTokenCeiling(t *testing.T) {
	b, input := unresolvedWindowFixture(t, QwenEmbeddingProfile, QwenEmbedding, unresolvedStatusOutcomeUnknown, 1)
	if err := b.ReconcileUnresolvedCall(context.Background(), input); err != nil {
		t.Fatalf("the settlement was refused: %v", err)
	}
	// The window used 128 + 256 = 384 tokens of the 8000 the authorization
	// names, and the failed call still counts: its reservation was never
	// given back. A reservation that would push the cumulative reservation
	// past the ceiling is still refused after the settlement.
	if _, err := b.Reserve(context.Background(), QwenEmbedding, 8000, 0); !errors.Is(err, ErrTokenBudgetExhausted) {
		t.Fatalf("the settled window reserved past the cumulative token ceiling: %v", err)
	}
	ledger, err := readLedger(b.ledgerPath, QwenEmbeddingProfile)
	if err != nil {
		t.Fatal(err)
	}
	if ledger.ReservedTokens != 384 {
		t.Fatalf("the settled window's token reservation changed: %d", ledger.ReservedTokens)
	}
}

// TestTheSettledWindowStillHonoursTheCallCount pins that the failed call
// keeps its place in the call count: settling it does not hand a call back.
func TestTheSettledWindowStillHonoursTheCallCount(t *testing.T) {
	b := New(QwenEmbeddingProfile, filepath.Join(t.TempDir(), "synthetic.calls.json"))
	for index := 0; index < 3; index++ {
		done, err := b.Reserve(context.Background(), QwenEmbedding, 64, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := done.Complete("response_received", 50, 0, "", false); err != nil {
			t.Fatal(err)
		}
	}
	failed, err := b.Reserve(context.Background(), QwenEmbedding, 64, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := failed.Unknown(unresolvedStatusOutcomeUnknown); !errors.Is(err, ErrCallsSpent) {
		t.Fatalf("fixture Unknown: %v", err)
	}
	input := unresolvedWindowEvidence(t, b, unresolvedStatusOutcomeUnknown)
	if err := b.ReconcileUnresolvedCall(context.Background(), input); err != nil {
		t.Fatalf("the settlement was refused: %v", err)
	}
	if _, err := b.Reserve(context.Background(), QwenEmbedding, 64, 0); !errors.Is(err, ErrCallsSpent) {
		t.Fatalf("the settled window reserved past its call count: %v", err)
	}
}

// TestTheWindowRecoversFromACrashBetweenTheReservationAndTheCompletion
// covers the in_flight shape: the process died between the reservation and
// the completion, so the window was never frozen -- and the ledger is not
// even a valid window until the crash is settled.
func TestTheWindowRecoversFromACrashBetweenTheReservationAndTheCompletion(t *testing.T) {
	b := New(QwenEmbeddingProfile, filepath.Join(t.TempDir(), "synthetic.calls.json"))
	reservation, err := b.Reserve(context.Background(), QwenEmbedding, 256, 0)
	if err != nil {
		t.Fatal(err)
	}
	// The process dies here: the call is neither completed nor cancelled, so
	// its row stays in_flight and the window was never frozen.
	reservation.release()

	before, err := readLedger(b.ledgerPath, QwenEmbeddingProfile)
	if err != nil {
		t.Fatal(err)
	}
	if before.Requests[0].Status != unresolvedStatusInFlight || before.Blocked {
		t.Fatalf("the crash fixture is not a crashed window: %+v", before)
	}
	// The crashed ledger is not a usable window: the row is unresolved and
	// nobody settled it.
	if _, err := b.Reserve(context.Background(), QwenEmbedding, 64, 0); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("the crashed window reserved anyway: %v", err)
	}

	input := unresolvedWindowEvidence(t, b, unresolvedStatusInFlight)
	if err := b.ReconcileUnresolvedCall(context.Background(), input); err != nil {
		t.Fatalf("the settlement was refused: %v", err)
	}
	after, err := readLedger(b.ledgerPath, QwenEmbeddingProfile)
	if err != nil {
		t.Fatal(err)
	}
	if after.Blocked || hasUnresolved(after) || !validLedger(after, QwenEmbeddingProfile) {
		t.Fatalf("the settled crashed ledger is not a settled window: %+v", after)
	}
	proof := assertSettlementKeptTheRowIntact(t, before, after, 1)
	if proof.Kind != unresolvedCallReconciliationKind || proof.ProviderTokenUsage != unknownProviderUsage {
		t.Fatalf("the settlement proof does not carry the conservative charge: %+v", proof)
	}
	// Nothing was refunded: the crashed call keeps its reservation.
	if after.ReservedNanoCNY != before.ReservedNanoCNY || after.ReservedTokens != before.ReservedTokens {
		t.Fatalf("the settlement refunded the crashed call: before %+v after %+v", before, after)
	}
	next, err := b.Reserve(context.Background(), QwenEmbedding, 64, 0)
	if err != nil {
		t.Fatalf("the settled window still refuses reservations: %v", err)
	}
	if err := next.CancelNoCall(); err != nil {
		t.Fatal(err)
	}
}

// TestTheChatProfileRecoversWithoutAnyMoneyColumn covers the chat profiles:
// they carry no price basis, so there is no amount to charge -- but the
// window is governed by TOKENS, and the failed call's token reservation is
// never given back.
func TestTheChatProfileRecoversWithoutAnyMoneyColumn(t *testing.T) {
	t.Run("v2", func(t *testing.T) {
		b, input := unresolvedWindowFixture(t, QwenProfileV2, QwenChatModel, unresolvedStatusOutcomeUnknown, 1)
		if _, err := b.Reserve(context.Background(), QwenChatModel, 64, 64); !errors.Is(err, ErrBlocked) {
			t.Fatalf("the frozen window reserved anyway: %v", err)
		}
		before, err := readLedger(b.ledgerPath, QwenProfileV2)
		if err != nil {
			t.Fatal(err)
		}
		if err := b.ReconcileUnresolvedCall(context.Background(), input); err != nil {
			t.Fatalf("the settlement was refused: %v", err)
		}
		after, err := readLedger(b.ledgerPath, QwenProfileV2)
		if err != nil {
			t.Fatal(err)
		}
		if after.Blocked || hasUnresolved(after) || !validLedger(after, QwenProfileV2) {
			t.Fatalf("the settled chat ledger is not a settled window: %+v", after)
		}
		proof := assertSettlementKeptTheRowIntact(t, before, after, input.RequestNumber)
		if proof.ChargeBasis != fullReserveChargeBasis || proof.ProviderTokenUsage != unknownProviderUsage {
			t.Fatalf("the settlement proof does not carry the conservative charge: %+v", proof)
		}
		// The chat profile computes no amount, so the charged amounts are 0
		// under a basis that computes nothing -- and the token reservation
		// of the failed call is NOT given back.
		if proof.ChargedNanoCNY != 0 || proof.ChargedNanoUSD != 0 {
			t.Fatalf("the chat settlement invented an amount: %+v", proof)
		}
		if after.ReservedTokens != before.ReservedTokens || len(after.Requests) != len(before.Requests) {
			t.Fatalf("the chat settlement refunded something: before %+v after %+v", before, after)
		}
		// The window can be used again: the settled call's usage is
		// unverifiable, but its full token reservation stays counted, so the
		// remaining cumulative budget is provable.
		next, err := b.Reserve(context.Background(), QwenChatModel, 64, 64)
		if err != nil {
			t.Fatalf("the settled chat window still refuses reservations: %v", err)
		}
		if err := next.CancelNoCall(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("v1 keeps its single call spent", func(t *testing.T) {
		b, input := unresolvedWindowFixture(t, QwenProfile, QwenChatModel, unresolvedStatusOutcomeUnknown, 0)
		if err := b.ReconcileUnresolvedCall(context.Background(), input); err != nil {
			t.Fatalf("the settlement was refused: %v", err)
		}
		after, err := readLedger(b.ledgerPath, QwenProfile)
		if err != nil {
			t.Fatal(err)
		}
		if after.Blocked || hasUnresolved(after) {
			t.Fatalf("the settled chat ledger is not a settled window: %+v", after)
		}
		// The first chat profile authorizes exactly ONE call, and the
		// settled call is that one: settling it does not authorize another.
		if _, err := b.Reserve(context.Background(), QwenChatModel, 64, 64); !errors.Is(err, ErrCallsSpent) {
			t.Fatalf("the settled window reserved past its call count: %v", err)
		}
	})
}

// TestThePricedProfilesRecoverFromAnUnconfirmableOutcome covers the profiles
// that price their calls.
func TestThePricedProfilesRecoverFromAnUnconfirmableOutcome(t *testing.T) {
	for _, profile := range []string{Profile, CostOnlyProfile} {
		t.Run(profile, func(t *testing.T) {
			b, input := unresolvedWindowFixture(t, profile, DeepSeekModel, unresolvedStatusOutcomeUnknown, 1)
			if _, err := b.Reserve(context.Background(), DeepSeekModel, 64, 64); !errors.Is(err, ErrBlocked) {
				t.Fatalf("the frozen window reserved anyway: %v", err)
			}
			before, err := readLedger(b.ledgerPath, profile)
			if err != nil {
				t.Fatal(err)
			}
			if err := b.ReconcileUnresolvedCall(context.Background(), input); err != nil {
				t.Fatalf("the settlement was refused: %v", err)
			}
			after, err := readLedger(b.ledgerPath, profile)
			if err != nil {
				t.Fatal(err)
			}
			if after.Blocked || hasUnresolved(after) || !validLedger(after, profile) {
				t.Fatalf("the settled ledger is not a settled window: %+v", after)
			}
			proof := assertSettlementKeptTheRowIntact(t, before, after, input.RequestNumber)
			// The charge is the FULL reserved upper bound, which for the
			// priced profiles is a real amount.
			if proof.ChargedNanoCNY != before.Requests[input.RequestNumber-1].ReservedNanoCNY {
				t.Fatalf("the settlement did not charge the full reserve: %+v", proof)
			}
			if after.ReservedNanoCNY != before.ReservedNanoCNY || after.ActualNanoCNY != before.ActualNanoCNY {
				t.Fatalf("the settlement refunded something: before %+v after %+v", before, after)
			}
			next, err := b.Reserve(context.Background(), DeepSeekModel, 64, 64)
			if err != nil {
				t.Fatalf("the settled window still refuses reservations: %v", err)
			}
			if err := next.CancelNoCall(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// TestTheUnresolvedSettlementRefusesEverythingItMustRefuse pins the negative
// paths: every one of them leaves the ledger byte for byte as it was.
func TestTheUnresolvedSettlementRefusesEverythingItMustRefuse(t *testing.T) {
	cases := map[string]func(*ReconcileUnresolvedCallInput){
		"wrong whole-ledger SHA": func(i *ReconcileUnresolvedCallInput) {
			i.ExpectedLedgerSHA256 = strings.Repeat("0", 64)
			i.Evidence.LedgerSHA256 = i.ExpectedLedgerSHA256
		},
		"wrong evidence snapshot":     func(i *ReconcileUnresolvedCallInput) { i.Evidence.LedgerSHA256 = strings.Repeat("1", 64) },
		"no full charge confirmation": func(i *ReconcileUnresolvedCallInput) { i.ConfirmFullReserveCharge = false },
		"wrong status":                func(i *ReconcileUnresolvedCallInput) { i.ExpectedStatus = unresolvedStatusUsageUnknown },
		"wrong evidence status":       func(i *ReconcileUnresolvedCallInput) { i.Evidence.OriginalStatus = unresolvedStatusUsageUnknown },
		"wrong block reason":          func(i *ReconcileUnresolvedCallInput) { i.ExpectedBlockedReason = unresolvedStatusUsageUnknown },
		"wrong evidence block reason": func(i *ReconcileUnresolvedCallInput) {
			i.Evidence.OriginalBlockedReason = unresolvedStatusUsageUnknown
		},
		"wrong operator reason":   func(i *ReconcileUnresolvedCallInput) { i.ExpectedReason = "different reason" },
		"missing operator":        func(i *ReconcileUnresolvedCallInput) { i.Evidence.Operator = "" },
		"missing reason":          func(i *ReconcileUnresolvedCallInput) { i.ExpectedReason = ""; i.Evidence.Reason = "" },
		"missing evidence digest": func(i *ReconcileUnresolvedCallInput) { i.Evidence.EvidenceSHA256 = "" },
		"wrong request":           func(i *ReconcileUnresolvedCallInput) { i.RequestNumber = 1; i.Evidence.RequestNumber = 1 },
		"cross-request evidence":  func(i *ReconcileUnresolvedCallInput) { i.Evidence.RequestNumber = 1 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			b, input := unresolvedWindowFixture(t, QwenEmbeddingProfile, QwenEmbedding, unresolvedStatusOutcomeUnknown, 1)
			before := mustLedgerBytes(t, b)
			mutate(&input)
			if err := b.ReconcileUnresolvedCall(context.Background(), input); err == nil {
				t.Fatal("the invalid settlement was accepted")
			}
			assertLedgerUnchanged(t, b, before)
		})
	}
}

// TestTheUnresolvedSettlementRefusesAResponseReceivedRow pins that a call
// the provider DID answer is not an unconfirmable outcome: its accounting is
// final, and settling it is a different decision than this one.
func TestTheUnresolvedSettlementRefusesAResponseReceivedRow(t *testing.T) {
	b := New(QwenEmbeddingProfile, filepath.Join(t.TempDir(), "synthetic.calls.json"))
	done, err := b.Reserve(context.Background(), QwenEmbedding, 128, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := done.Complete("response_received", 100, 0, "", false); err != nil {
		t.Fatal(err)
	}
	input := unresolvedWindowEvidence(t, b, "response_received")
	before := mustLedgerBytes(t, b)
	if err := b.ReconcileUnresolvedCall(context.Background(), input); err == nil {
		t.Fatal("a settled call was settled a second time")
	}
	assertLedgerUnchanged(t, b, before)
}

// TestTheUnresolvedSettlementRefusesASecondUnresolvedCall pins that only one
// unresolved call may be settled at a time: settling the wrong one of two is
// the failure this refuses.
func TestTheUnresolvedSettlementRefusesASecondUnresolvedCall(t *testing.T) {
	b, input := unresolvedTwoUnresolvedRowsFixture(t)
	before := mustLedgerBytes(t, b)
	if err := b.ReconcileUnresolvedCall(context.Background(), input); !errors.Is(err, ErrBlocked) {
		t.Fatalf("the settlement error = %v, want a refusal while another call is unresolved", err)
	}
	assertLedgerUnchanged(t, b, before)
}

// unresolvedTwoUnresolvedRowsFixture builds a ledger that carries TWO
// unresolved rows: one crashed in flight and one whose outcome nobody can
// confirm.
func unresolvedTwoUnresolvedRowsFixture(t *testing.T) (*Budget, ReconcileUnresolvedCallInput) {
	t.Helper()
	b := New(QwenEmbeddingProfile, filepath.Join(t.TempDir(), "synthetic.calls.json"))
	reservation, err := b.Reserve(context.Background(), QwenEmbedding, 256, 0)
	if err != nil {
		t.Fatal(err)
	}
	reservation.release()
	ledger, err := readLedger(b.ledgerPath, QwenEmbeddingProfile)
	if err != nil {
		t.Fatal(err)
	}
	ledger.Requests = append(ledger.Requests, Request{
		Number: 2, Model: QwenEmbedding, Status: unresolvedStatusOutcomeUnknown,
		InputUpperBound: 128, OutputLimit: 0, UsageStatus: UsageStatusUnavailable,
		CostBasis:       ProfileCostBasis(QwenEmbeddingProfile),
		ReservedNanoCNY: int64(128) * QwenNanoCNYPerToken,
		// The row's creation time is the moment the first call was
		// reserved: a timestamp from the future would make any settlement
		// of this row invalid for a reason of its own.
		CreatedAt: ledger.Requests[0].CreatedAt,
	})
	ledger.ReservedNanoCNY += int64(128) * QwenNanoCNYPerToken
	ledger.ReservedTokens += 128
	ledger.Blocked = true
	ledger.BlockedReason = unresolvedStatusOutcomeUnknown
	if err := writeLedger(b.ledgerPath, ledger); err != nil {
		t.Fatal(err)
	}
	return b, unresolvedWindowEvidence(t, b, unresolvedStatusOutcomeUnknown)
}

// TestTheUnresolvedSettlementRefusesAStaleSnapshot pins the snapshot
// binding: a digest that does not match the file on disk is refused, and the
// file is not touched.
func TestTheUnresolvedSettlementRefusesAStaleSnapshot(t *testing.T) {
	b, input := unresolvedWindowFixture(t, QwenEmbeddingProfile, QwenEmbedding, unresolvedStatusOutcomeUnknown, 1)
	// Somebody else changed the ledger after the operator took the digest.
	ledger, err := readLedger(b.ledgerPath, QwenEmbeddingProfile)
	if err != nil {
		t.Fatal(err)
	}
	ledger.BlockedReason = "manually annotated"
	if err := writeLedger(b.ledgerPath, ledger); err != nil {
		t.Fatal(err)
	}
	before := mustLedgerBytes(t, b)
	if err := b.ReconcileUnresolvedCall(context.Background(), input); err == nil {
		t.Fatal("a stale snapshot was accepted")
	}
	assertLedgerUnchanged(t, b, before)
}

// TestTheUnresolvedSettlementRefusesARepeatedSettlement pins that one call
// is settled once: a refreshed digest does not open the door either.
func TestTheUnresolvedSettlementRefusesARepeatedSettlement(t *testing.T) {
	b, input := unresolvedWindowFixture(t, QwenEmbeddingProfile, QwenEmbedding, unresolvedStatusOutcomeUnknown, 1)
	if err := b.ReconcileUnresolvedCall(context.Background(), input); err != nil {
		t.Fatalf("the settlement was refused: %v", err)
	}
	after := mustLedgerBytes(t, b)
	if err := b.ReconcileUnresolvedCall(context.Background(), input); err == nil {
		t.Fatal("the same call was settled twice")
	}
	assertLedgerUnchanged(t, b, after)
	refreshed := unresolvedWindowEvidence(t, b, unresolvedStatusOutcomeUnknown)
	if err := b.ReconcileUnresolvedCall(context.Background(), refreshed); err == nil {
		t.Fatal("a refreshed digest let the same call be settled twice")
	}
	assertLedgerUnchanged(t, b, after)
}

// TestTheUnresolvedSettlementNeedsAnAbsoluteLedgerPath pins that the
// settlement never touches a relative path, and never creates a file.
func TestTheUnresolvedSettlementNeedsAnAbsoluteLedgerPath(t *testing.T) {
	_, input := unresolvedWindowFixture(t, QwenEmbeddingProfile, QwenEmbedding, unresolvedStatusOutcomeUnknown, 1)
	relative := New(QwenEmbeddingProfile, filepath.Join("relative", "synthetic.calls.json"))
	if err := relative.ReconcileUnresolvedCall(context.Background(), input); err == nil {
		t.Fatal("a relative ledger path was accepted")
	}
	if _, err := os.Stat(filepath.Join("relative", "synthetic.calls.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the refused settlement created a ledger file")
	}
}

// TestTheUnresolvedSettlementNeedsTheProfileCapability pins that a profile
// without the capability is refused before anything is read.
func TestTheUnresolvedSettlementNeedsTheProfileCapability(t *testing.T) {
	b, input := unresolvedWindowFixture(t, QwenEmbeddingProfile, QwenEmbedding, unresolvedStatusOutcomeUnknown, 1)
	before := mustLedgerBytes(t, b)
	if err := New("a-profile-nobody-declared", b.ledgerPath).ReconcileUnresolvedCall(context.Background(), input); err == nil {
		t.Fatal("a profile without the capability settled an unresolved call")
	}
	assertLedgerUnchanged(t, b, before)
}

// TestTheLimitViolationRowIsSettledOnlyWhileItsUsageIsUnverifiable pins the
// limit_violation shape.
//
// A limit violation is a CONFIRMED outcome: the provider reported usage that
// passed a ceiling, so the row's accounting is final and this channel has
// nothing to say about it. Such a row is refused.
//
// The shape this channel does settle is the one whose usage was never
// reported: there, the full reserved upper bound is charged, exactly as for
// every other unconfirmable outcome.
func TestTheLimitViolationRowIsSettledOnlyWhileItsUsageIsUnverifiable(t *testing.T) {
	t.Run("reported usage is refused", func(t *testing.T) {
		b := New(QwenEmbeddingProfile, filepath.Join(t.TempDir(), "synthetic.calls.json"))
		reservation, err := b.Reserve(context.Background(), QwenEmbedding, 128, 0)
		if err != nil {
			t.Fatal(err)
		}
		// The provider reported MORE tokens than the request reserved
		// against: the checklist calls that a limit violation.
		if err := reservation.Complete("response_received", 200, 0, "", false); err == nil {
			t.Fatal("the limit violation was not recorded")
		}
		before := mustLedgerBytes(t, b)
		input := unresolvedWindowEvidence(t, b, unresolvedStatusLimitViolation)
		if err := b.ReconcileUnresolvedCall(context.Background(), input); err == nil {
			t.Fatal("a confirmed limit violation was settled by this channel")
		}
		assertLedgerUnchanged(t, b, before)
	})
	t.Run("unverifiable usage is settled", func(t *testing.T) {
		b := New(QwenEmbeddingProfile, filepath.Join(t.TempDir(), "synthetic.calls.json"))
		reservation, err := b.Reserve(context.Background(), QwenEmbedding, 128, 0)
		if err != nil {
			t.Fatal(err)
		}
		reservation.release()
		ledger, err := readLedger(b.ledgerPath, QwenEmbeddingProfile)
		if err != nil {
			t.Fatal(err)
		}
		ledger.Requests[0].Status = unresolvedStatusLimitViolation
		ledger.Requests[0].UsageStatus = UsageStatusUnavailable
		ledger.Blocked = true
		ledger.BlockedReason = unresolvedStatusLimitViolation
		if err := writeLedger(b.ledgerPath, ledger); err != nil {
			t.Fatal(err)
		}
		input := unresolvedWindowEvidence(t, b, unresolvedStatusLimitViolation)
		if err := b.ReconcileUnresolvedCall(context.Background(), input); err != nil {
			t.Fatalf("the settlement was refused: %v", err)
		}
		after, err := readLedger(b.ledgerPath, QwenEmbeddingProfile)
		if err != nil {
			t.Fatal(err)
		}
		if after.Blocked || hasUnresolved(after) {
			t.Fatalf("the settled ledger is not a settled window: %+v", after)
		}
	})
}

// TestTheHTTPErrorChannelKeepsItsOwnAnnotation pins that the new channel did
// not touch the HTTP error one: an http_error row still carries the
// ORIGINAL annotation type, and the new channel refuses it.
func TestTheHTTPErrorChannelKeepsItsOwnAnnotation(t *testing.T) {
	b, input := embeddingWindowFixture(t, 403)
	if err := b.ReconcileEmbeddingHTTPError(context.Background(), input); err != nil {
		t.Fatalf("the HTTP error settlement was refused: %v", err)
	}
	ledger, err := readLedger(b.ledgerPath, QwenEmbeddingProfile)
	if err != nil {
		t.Fatal(err)
	}
	row := ledger.Requests[input.RequestNumber-1]
	if row.Reconciliation == nil || row.Reconciliation.Kind != embeddingHTTPErrorReconciliationKind {
		t.Fatalf("the http_error row lost its own annotation: %+v", row.Reconciliation)
	}
	if row.UnresolvedReconciliation != nil {
		t.Fatal("the http_error row carries the new channel's annotation")
	}
	// And the new channel refuses an http_error row: that call is settled by
	// the HTTP error channel, not by this one.
	before := mustLedgerBytes(t, b)
	unresolved := unresolvedWindowEvidence(t, b, "http_error")
	if err := b.ReconcileUnresolvedCall(context.Background(), unresolved); err == nil {
		t.Fatal("the new channel settled an http_error row")
	}
	assertLedgerUnchanged(t, b, before)
}

// TestATamperedSettlementLeavesTheCallUnresolved pins that the settlement
// annotation is only a proof while it still matches the row it sits on.
//
// The annotation is the only thing that says "this call was settled", so a
// tampered or incomplete one must NOT open the window: the call stays
// unresolved, the window stays frozen, and the ledger file is refused
// outright rather than reopened on a proof nobody can check.
//
// The check is structural: it does not look at one field, it re-derives the
// whole settlement. A discount, a claimed provider usage, another kind or
// another row's digest all leave the call unresolved.
func TestATamperedSettlementLeavesTheCallUnresolved(t *testing.T) {
	b, input := unresolvedWindowFixture(t, QwenEmbeddingProfile, QwenEmbedding, unresolvedStatusOutcomeUnknown, 1)
	if err := b.ReconcileUnresolvedCall(context.Background(), input); err != nil {
		t.Fatalf("the settlement was refused: %v", err)
	}
	settled, err := readLedger(b.ledgerPath, QwenEmbeddingProfile)
	if err != nil {
		t.Fatal(err)
	}
	number := input.RequestNumber
	tamperings := map[string]func(*UnresolvedCallReconciliation){
		"a discounted charge":      func(r *UnresolvedCallReconciliation) { r.ChargedNanoCNY-- },
		"a claimed provider usage": func(r *UnresolvedCallReconciliation) { r.ProviderTokenUsage = "1024" },
		"another kind":             func(r *UnresolvedCallReconciliation) { r.Kind = "another_kind_v9" },
		"another row's digest":     func(r *UnresolvedCallReconciliation) { r.OriginalRequestSHA256 = digestSHA256([]byte("another row")) },
		"an empty evidence digest": func(r *UnresolvedCallReconciliation) { r.Evidence.EvidenceSHA256 = "" },
	}
	for name, tamper := range tamperings {
		t.Run(name, func(t *testing.T) {
			row := settled.Requests[number-1]
			if row.UnresolvedReconciliation == nil {
				t.Fatal("the settled row carries no settlement")
			}
			proof := *row.UnresolvedReconciliation
			tamper(&proof)
			row.UnresolvedReconciliation = &proof
			if !requestUnresolved(row) {
				t.Fatal("a tampered settlement was accepted as a settlement")
			}
			tampered := settled
			tampered.Requests = append([]Request(nil), settled.Requests...)
			tampered.Requests[number-1] = row
			if !hasUnresolved(tampered) {
				t.Fatal("a tampered settlement opened the window")
			}
			// And the file itself is refused: a window is never reopened on
			// a proof nobody can check.
			path := filepath.Join(t.TempDir(), "tampered.calls.json")
			if err := writeLedger(path, tampered); err != nil {
				t.Fatal(err)
			}
			if _, err := readLedger(path, QwenEmbeddingProfile); err == nil {
				t.Fatal("a tampered settlement was read back as a settled window")
			}
		})
	}
}

// TestTheUnresolvedSettlementCapabilityIsStatedInTheProfileTable pins that
// the capability lives in the profile table, not in a hard-wired profile
// name.
func TestTheUnresolvedSettlementCapabilityIsStatedInTheProfileTable(t *testing.T) {
	for _, profile := range []string{QwenEmbeddingProfile, CostOnlyProfile, QwenProfile, QwenProfileV2, Profile} {
		if !ProfileSupportsUnresolvedCallReconciliation(profile) {
			t.Fatalf("profile %q lost the unresolved settlement capability", profile)
		}
	}
	if ProfileSupportsUnresolvedCallReconciliation("a-profile-nobody-declared") {
		t.Fatal("an undeclared profile claims the unresolved settlement capability")
	}
}

// TestTheUnresolvedSettlementIsNeverCalledByAnApplicationSeam pins that the
// settlement is a LOCAL operator operation: no provider, HTTP or application
// path may call it.
//
// The check is structural. It reads EVERY .go file of the repository -- the
// test files and the OS-specific ones included -- and refuses any mention of
// the settlement outside the two places that are allowed to mention it: the
// budget package itself and the operator command.
func TestTheUnresolvedSettlementIsNeverCalledByAnApplicationSeam(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	allowed := []string{
		filepath.Join("internal", "models", "acceptancebudget"),
		filepath.Join("cmd", "acceptance-budget-reconcile"),
	}
	skipped := map[string]bool{".git": true, "node_modules": true, "vendor": true, "dist": true, "build": true}
	var problems []string
	inspected := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if skipped[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			relative = path
		}
		for _, allowedPath := range allowed {
			if relative == allowedPath || strings.HasPrefix(relative, allowedPath+string(filepath.Separator)) {
				return nil
			}
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		inspected++
		if strings.Contains(string(data), "ReconcileUnresolvedCall") {
			problems = append(problems, relative)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan the repository: %v", err)
	}
	if inspected == 0 {
		t.Fatal("no source file was inspected; the check would pass by looking at nothing")
	}
	for _, problem := range problems {
		t.Errorf("%s mentions ReconcileUnresolvedCall; the settlement is an operator operation and no application seam may call it", problem)
	}
}
