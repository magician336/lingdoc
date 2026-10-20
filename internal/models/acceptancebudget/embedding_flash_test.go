package acceptancebudget

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestR31EmbeddingPriceMapAndLegacyProfileStayExact(t *testing.T) {
	for _, tc := range []struct {
		model string
		price int64
	}{
		{QwenEmbedding, 500}, {QwenEmbeddingFlash, 125},
	} {
		price, ok := EmbeddingInputNanoCNYPerToken(tc.model)
		if !ok || price != tc.price {
			t.Fatalf("price for %q: %d/%v", tc.model, price, ok)
		}
	}
	for _, model := range []string{"other-model", "QWEN3.7-TEXT-EMBEDDING-FLASH", QwenEmbeddingFlash + "-extra", " " + QwenEmbeddingFlash, DeepSeekModel} {
		if _, ok := EmbeddingInputNanoCNYPerToken(model); ok {
			t.Fatalf("unknown embedding accepted: %q", model)
		}
		if model == DeepSeekModel {
			continue
		} // Its existing separate chat budget remains supported.
		b := New(CostOnlyProfile, filepath.Join(t.TempDir(), "synthetic.json"))
		if _, err := b.Reserve(context.Background(), model, 4, 0); !errors.Is(err, ErrWrongModel) {
			t.Fatalf("unknown model: %v", err)
		}
		if _, err := os.Stat(b.ledgerPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("unknown model created ledger")
		}
	}
	legacy := New(Profile, filepath.Join(t.TempDir(), "legacy.json"))
	if _, err := legacy.Reserve(context.Background(), QwenEmbeddingFlash, 4, 0); !errors.Is(err, ErrWrongModel) {
		t.Fatalf("Flash entered six-call profile: %v", err)
	}
	if _, err := os.Stat(legacy.ledgerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("denied Flash changed legacy ledger")
	}
	r, err := legacy.Reserve(context.Background(), QwenEmbedding, 4, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Complete("response_received", 4, 0, "", false); err != nil {
		t.Fatal(err)
	}
	l, err := readLedger(legacy.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if l.MaxCalls != 6 || l.ReservedNanoCNY != 2000 || l.ActualNanoCNY != 2000 {
		t.Fatal("legacy price/profile changed")
	}
}

func TestR31MixedEmbeddingHistoryRetainsThirtyRecordsAndReopens(t *testing.T) {
	b := New(CostOnlyProfile, filepath.Join(t.TempDir(), "synthetic.calls.json"))
	for index := 0; index < 28; index++ {
		r, err := b.Reserve(context.Background(), QwenEmbedding, 8, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Complete("response_received", 4, 0, "", false); err != nil {
			t.Fatal(err)
		}
	}
	r, err := b.Reserve(context.Background(), QwenEmbedding, 256, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Unknown("http_error"); !errors.Is(err, ErrCallsSpent) {
		t.Fatal(err)
	}
	if err := b.ReconcileEmbeddingHTTPError(context.Background(), operatorInput(t, b)); err != nil {
		t.Fatal(err)
	}
	r, err = b.Reserve(context.Background(), QwenEmbedding, 64, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Unknown("http_error"); !errors.Is(err, ErrCallsSpent) {
		t.Fatal(err)
	}
	before, err := readLedger(b.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeBytes := mustLedgerBytes(t, b)
	// New model authorization does not erase request30's unresolved HTTP error.
	if _, err := New(CostOnlyProfile, b.ledgerPath).Reserve(context.Background(), QwenEmbeddingFlash, 4, 0); !errors.Is(err, ErrBlocked) {
		t.Fatalf("model switch bypassed reconciliation: %v", err)
	}
	assertLedgerUnchanged(t, b, beforeBytes)
	if err := b.ReconcileEmbeddingHTTPError(context.Background(), operatorInput(t, b)); err != nil {
		t.Fatal(err)
	}
	reopened := New(CostOnlyProfile, b.ledgerPath)
	r, err = reopened.Reserve(context.Background(), QwenEmbeddingFlash, 4, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Complete("response_received", 2, 0, "", false); err != nil {
		t.Fatal(err)
	}
	r, err = reopened.Reserve(context.Background(), QwenEmbedding, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Complete("response_received", 1, 0, "", false); err != nil {
		t.Fatal(err)
	}
	after, err := readLedger(New(CostOnlyProfile, b.ledgerPath).ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if !validLedger(after, CostOnlyProfile) || len(after.Requests) != 32 || !reflect.DeepEqual(before.Requests[:29], after.Requests[:29]) {
		t.Fatal("history or request29 reconciliation changed")
	}
	old30 := after.Requests[29]
	old30.Reconciliation = nil
	if !reflect.DeepEqual(before.Requests[29], old30) || after.Requests[29].Reconciliation == nil {
		t.Fatal("request30 failure/money rewritten")
	}
	if after.Requests[28].Status != "http_error" || after.Requests[28].ActualNanoCNY != 128000 || after.Requests[29].ActualNanoCNY != 32000 ||
		after.Requests[30].ReservedNanoCNY != 500 || after.Requests[30].ActualNanoCNY != 250 ||
		after.Requests[31].ReservedNanoCNY != 1000 || after.Requests[31].ActualNanoCNY != 500 ||
		after.ReservedNanoCNY != 273500 || after.ActualNanoCNY != 216750 {
		t.Fatal("old/new model prices were not preserved")
	}
}

func TestR31EmbeddingWrongHistoricalFeesFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name, model      string
		reserved, actual int64
	}{
		{"old-repriced-as-flash", QwenEmbedding, 12500, 12500},
		{"old-actual-discount-only", QwenEmbedding, 50000, 12500},
		{"flash-charged-as-old", QwenEmbeddingFlash, 50000, 50000},
		{"flash-actual-wrong-only", QwenEmbeddingFlash, 12500, 12000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := New(CostOnlyProfile, filepath.Join(t.TempDir(), "synthetic.json"))
			r, err := b.Reserve(context.Background(), tc.model, 100, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Complete("response_received", 100, 0, "", false); err != nil {
				t.Fatal(err)
			}
			l, err := readLedger(b.ledgerPath)
			if err != nil {
				t.Fatal(err)
			}
			l.Requests[0].ReservedNanoCNY, l.Requests[0].ActualNanoCNY = tc.reserved, tc.actual
			l.ReservedNanoCNY, l.ActualNanoCNY = tc.reserved, tc.actual
			if err := writeLedger(b.ledgerPath, l); err != nil {
				t.Fatal(err)
			}
			before := mustLedgerBytes(t, b)
			if _, err := New(CostOnlyProfile, b.ledgerPath).Reserve(context.Background(), QwenEmbeddingFlash, 1, 0); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("wrong fees accepted: %v", err)
			}
			assertLedgerUnchanged(t, b, before)
		})
	}
	// A Flash record cannot be laundered through migration from the old profile.
	l := freshLedger(Profile)
	l.Requests = []Request{{Number: 1, Model: QwenEmbeddingFlash, Status: "response_received", InputUpperBound: 4, InputTokens: 4, ReservedNanoCNY: 500, ActualNanoCNY: 500, CreatedAt: time.Now().UTC()}}
	l.ReservedNanoCNY, l.ActualNanoCNY = 500, 500
	b := New(CostOnlyProfile, filepath.Join(t.TempDir(), "bad-legacy.json"))
	if err := writeLedger(b.ledgerPath, l); err != nil {
		t.Fatal(err)
	}
	before := mustLedgerBytes(t, b)
	if validLedger(l, Profile) {
		t.Fatal("legacy ledger accepted Flash history")
	}
	if _, err := b.Reserve(context.Background(), QwenEmbedding, 1, 0); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("legacy migration accepted Flash: %v", err)
	}
	assertLedgerUnchanged(t, b, before)
}

func TestR31FlashFourTokensRejects499NanoRemainingWithoutThousandsOfCalls(t *testing.T) {
	l := freshLedger(CostOnlyProfile)
	// Construct consistent settled history in memory and write once: no provider
	// calls and no loop of thousands of Reserve/Complete file transactions.
	remainingTokens := int64(39999999) // Original-model price500 => reserved19,999,999,500.
	for remainingTokens > 0 {
		n := InputMax
		if remainingTokens < int64(n) {
			n = int(remainingTokens)
		}
		cost := int64(n) * 500
		l.Requests = append(l.Requests, Request{Number: len(l.Requests) + 1, Model: QwenEmbedding, Status: "response_received", InputUpperBound: n, InputTokens: n, ReservedNanoCNY: cost, ActualNanoCNY: cost, CreatedAt: time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)})
		l.ReservedNanoCNY += cost
		l.ActualNanoCNY += cost
		remainingTokens -= int64(n)
	}
	if !validLedger(l, CostOnlyProfile) || l.MaxReserveNanoCNY-l.ReservedNanoCNY != 499 {
		t.Fatal("boundary fixture must be valid and leave exactly499 nano")
	}
	b := New(CostOnlyProfile, filepath.Join(t.TempDir(), "boundary.json"))
	if err := writeLedger(b.ledgerPath, l); err != nil {
		t.Fatal(err)
	}
	before := mustLedgerBytes(t, b)
	if _, err := b.Reserve(context.Background(), QwenEmbeddingFlash, 4, 0); !errors.Is(err, ErrCallsSpent) {
		t.Fatalf("Flash4tokens500 exceeded499 remaining: %v", err)
	}
	assertLedgerUnchanged(t, b, before)
	r, err := b.Reserve(context.Background(), QwenEmbeddingFlash, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Complete("response_received", 3, 0, "", false); err != nil {
		t.Fatal(err)
	}
	after, err := readLedger(b.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if after.ReservedNanoCNY != 19999999875 || after.ActualNanoCNY != 19999999875 || after.ReservedNanoCNY >= 20000000000 {
		t.Fatal("strict20CNY cap changed")
	}
}

func TestR31FlashHTTPErrorRecoveryKeeps125NanoAndUnknownUsage(t *testing.T) {
	for _, status := range []string{"http_error", "outcome_unknown", "usage_unknown"} {
		t.Run(status, func(t *testing.T) {
			b := New(CostOnlyProfile, filepath.Join(t.TempDir(), "synthetic.json"))
			r, err := b.Reserve(context.Background(), QwenEmbeddingFlash, 256, 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Unknown(status); !errors.Is(err, ErrCallsSpent) {
				t.Fatal(err)
			}
			before, err := readLedger(b.ledgerPath)
			if err != nil {
				t.Fatal(err)
			}
			if before.ActualNanoCNY != 32000 || before.ReservedNanoCNY != 32000 {
				t.Fatal("unknown Flash usage was not fully reserved at125")
			}
			beforeBytes := mustLedgerBytes(t, b)
			if _, err := b.Reserve(context.Background(), QwenEmbeddingFlash, 1, 0); !errors.Is(err, ErrBlocked) {
				t.Fatalf("unknown usage did not block: %v", err)
			}
			assertLedgerUnchanged(t, b, beforeBytes)
			err = New(CostOnlyProfile, b.ledgerPath).ReconcileEmbeddingHTTPError(context.Background(), operatorInput(t, b))
			if status != "http_error" {
				if err == nil {
					t.Fatal("unknown outcome/usage was reconciled")
				}
				assertLedgerUnchanged(t, b, beforeBytes)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			after, err := readLedger(b.ledgerPath)
			if err != nil {
				t.Fatal(err)
			}
			if !validLedger(after, CostOnlyProfile) || hasUnresolved(after) || after.Blocked || after.Requests[0].Status != "http_error" ||
				after.ActualNanoCNY != 32000 || after.ReservedNanoCNY != 32000 || after.Requests[0].InputTokens != 256 ||
				after.Requests[0].Reconciliation.ProviderTokenUsage != "unknown" || after.Requests[0].Reconciliation.ChargedNanoCNY != 32000 {
				t.Fatal("Flash recovery changed original failure/unknown usage/money")
			}
			if err := New(CostOnlyProfile, b.ledgerPath).ReconcileEmbeddingHTTPError(context.Background(), operatorInput(t, b)); err == nil {
				t.Fatal("duplicate Flash reconciliation accepted")
			}
		})
	}
}

func TestR31FlashRetainsInputAndOutputLimits(t *testing.T) {
	for _, tc := range []struct {
		in, out int
		want    error
	}{{InputMax + 1, 0, ErrInputTooLarge}, {-1, 0, ErrInputTooLarge}, {1, 1, ErrWrongModel}} {
		b := New(CostOnlyProfile, filepath.Join(t.TempDir(), "synthetic.json"))
		if _, err := b.Reserve(context.Background(), QwenEmbeddingFlash, tc.in, tc.out); !errors.Is(err, tc.want) {
			t.Fatalf("limits %d/%d: %v", tc.in, tc.out, err)
		}
	}
	if _, _, err := actualAmounts(QwenEmbeddingFlash, 1, 1); !errors.Is(err, ErrWrongModel) {
		t.Fatal("Flash output tokens accepted")
	}
	if _, _, err := actualAmounts(strings.ToUpper(QwenEmbeddingFlash), 1, 0); !errors.Is(err, ErrWrongModel) {
		t.Fatal("model case alias accepted")
	}
}
