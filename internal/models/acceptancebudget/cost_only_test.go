package acceptancebudget

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCostOnlyBudgetMigrationPreservesHistoryAndAllowsSeventh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.json")
	old := New(Profile, path)
	for i := 0; i < 4; i++ {
		r, e := old.Reserve(context.Background(), QwenEmbedding, 100, 0)
		if e != nil {
			t.Fatal(e)
		}
		if e = r.Complete("response_received", 50, 0, "", false); e != nil {
			t.Fatal(e)
		}
	}
	before, e := readLedger(path)
	if e != nil {
		t.Fatal(e)
	}
	migrated := before
	migrated.Profile = CostOnlyProfile
	migrated.MaxCalls = 0
	migrated.MaxReserveNanoCNY = CostOnlyMaxBudgetNanoCNY
	if !validLedger(migrated, CostOnlyProfile) {
		t.Fatal("invalid migration")
	}
	if e = writeLedger(path, migrated); e != nil {
		t.Fatal(e)
	}
	budget := New(CostOnlyProfile, path)
	for i := 4; i < 8; i++ {
		r, e := budget.Reserve(context.Background(), DeepSeekModel, 100, 100)
		if e != nil {
			t.Fatal(e)
		}
		if e = r.Complete("response_received", 50, 50, "stop", false); e != nil {
			t.Fatal(e)
		}
	}
	after, e := readLedger(path)
	if e != nil {
		t.Fatal(e)
	}
	if len(after.Requests) != 8 || !reflect.DeepEqual(before.Requests, after.Requests[:4]) {
		t.Fatal("history lost or seventh call rejected")
	}
	if _, e = old.Reserve(context.Background(), QwenEmbedding, 1, 0); !errors.Is(e, ErrUnavailable) {
		t.Fatalf("profile mismatch: %v", e)
	}
}

func TestCostOnlyBudgetStopsBelowTwentyYuan(t *testing.T) {
	budget := New(CostOnlyProfile, filepath.Join(t.TempDir(), "calls.json"))
	count := 0
	for {
		r, e := budget.Reserve(context.Background(), DeepSeekModel, InputMax, OutputMax)
		if errors.Is(e, ErrCallsSpent) {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		if e = r.Complete("response_received", InputMax, OutputMax, "stop", false); e != nil {
			t.Fatal(e)
		}
		count++
		if count > 200 {
			t.Fatal("cost cap missing")
		}
	}
	ledger, e := readLedger(budget.ledgerPath)
	if e != nil {
		t.Fatal(e)
	}
	if count <= 6 || ledger.ActualNanoCNY >= 20_000_000_000 || ledger.ReservedNanoCNY >= 20_000_000_000 {
		t.Fatalf("invalid cost cap: %+v", ledger)
	}
	if !validLedger(ledger, CostOnlyProfile) {
		t.Fatal("settled cost-only ledger invalid")
	}
}

func TestCostOnlyBudgetUnknownAndTamperingFailClosed(t *testing.T) {
	for _, tamper := range []bool{false, true} {
		budget := New(CostOnlyProfile, filepath.Join(t.TempDir(), "calls.json"))
		r, e := budget.Reserve(context.Background(), DeepSeekModel, 100, 100)
		if e != nil {
			t.Fatal(e)
		}
		if tamper {
			if e = r.Complete("response_received", 50, 50, "stop", false); e != nil {
				t.Fatal(e)
			}
			ledger, e := readLedger(budget.ledgerPath)
			if e != nil {
				t.Fatal(e)
			}
			ledger.MaxReserveNanoCNY++
			if e = writeLedger(budget.ledgerPath, ledger); e != nil {
				t.Fatal(e)
			}
		} else {
			if e = r.Unknown("outcome_unknown"); e == nil {
				t.Fatal("unknown accepted")
			}
		}
		if _, e = budget.Reserve(context.Background(), QwenEmbedding, 1, 0); e == nil {
			t.Fatal("blocked/tampered ledger accepted")
		}
	}
}
