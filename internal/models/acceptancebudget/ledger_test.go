package acceptancebudget

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSharedBudgetCapsMixedModelRequestsAtSix(t *testing.T) {
	budget := New(Profile, filepath.Join(t.TempDir(), "calls.json"))
	for index := 0; index < MaxCalls; index++ {
		model := DeepSeekModel
		outputLimit := 100
		if index%2 == 1 {
			model = QwenEmbedding
			outputLimit = 0
		}
		reservation, err := budget.Reserve(context.Background(), model, 200, outputLimit)
		if err != nil {
			t.Fatalf("reserve call %d: %v", index+1, err)
		}
		if err := reservation.Complete("response_received", 150, outputLimit, "stop", false); err != nil {
			t.Fatalf("complete call %d: %v", index+1, err)
		}
	}
	if _, err := budget.Reserve(context.Background(), QwenEmbedding, 200, 0); !errors.Is(err, ErrCallsSpent) {
		t.Fatalf("seventh call error = %v, want request budget exhausted", err)
	}
	ledger, err := readLedger(budget.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Requests) != MaxCalls || ledger.ReservedNanoCNY > maxBudgetNanoCNY || ledger.ActualNanoCNY > ledger.ReservedNanoCNY {
		t.Fatalf("unexpected mixed request ledger: %+v", ledger)
	}
}

func TestUnknownProviderOutcomeBlocksSharedBudget(t *testing.T) {
	budget := New(Profile, filepath.Join(t.TempDir(), "calls.json"))
	reservation, err := budget.Reserve(context.Background(), QwenEmbedding, 256, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := reservation.Unknown("outcome_unknown"); !errors.Is(err, ErrCallsSpent) {
		t.Fatalf("unknown completion error = %v, want blocked budget", err)
	}
	if _, err := budget.Reserve(context.Background(), DeepSeekModel, 256, 256); !errors.Is(err, ErrBlocked) {
		t.Fatalf("next call error = %v, want reconciliation block", err)
	}
}

func TestInputLimitIsCheckedBeforeReservation(t *testing.T) {
	budget := New(Profile, filepath.Join(t.TempDir(), "calls.json"))
	if _, err := budget.Reserve(context.Background(), QwenEmbedding, InputMax+1, 0); !errors.Is(err, ErrInputTooLarge) {
		t.Fatalf("oversize input error = %v, want input limit error", err)
	}
	ledger, err := readLedger(budget.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Requests) != 0 {
		t.Fatalf("oversize request was recorded: %+v", ledger.Requests)
	}
}

func TestCancelNoCallRestoresReservation(t *testing.T) {
	budget := New(Profile, filepath.Join(t.TempDir(), "calls.json"))
	reservation, err := budget.Reserve(context.Background(), DeepSeekModel, 100, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := reservation.CancelNoCall(); err != nil {
		t.Fatal(err)
	}
	ledger, err := readLedger(budget.ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Requests) != 0 || ledger.ReservedNanoCNY != 0 || ledger.ReservedNanoUSD != 0 {
		t.Fatalf("cancelled reservation remains: %+v", ledger)
	}
}

func TestAcquireLockRenewsWhileHeld(t *testing.T) {
	oldInterval := lockLeaseHeartbeatInterval
	lockLeaseHeartbeatInterval = 10 * time.Millisecond
	defer func() { lockLeaseHeartbeatInterval = oldInterval }()

	path := filepath.Join(t.TempDir(), "calls.json.lock")
	lock, err := AcquireLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ReleaseLock(lock, path) }()
	initial, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(35 * time.Millisecond)
	renewed, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !renewed.ModTime().After(initial.ModTime()) {
		t.Fatalf("lock mtime = %s, want renewal after %s", renewed.ModTime(), initial.ModTime())
	}
}

func TestAcquireLockReclaimsAnExpiredLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.json.lock")
	if err := os.WriteFile(path, []byte("crashed owner\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-2 * LockLeaseTTL)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatal(err)
	}
	lock, err := AcquireLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReleaseLock(lock, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock path after release = %v, want it removed", err)
	}
}

func TestAcquireLockHonorsCancellationBeforeReclaiming(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.json.lock")
	if err := os.WriteFile(path, []byte("crashed owner\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-2 * LockLeaseTTL)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	lock, err := AcquireLock(ctx, path)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("AcquireLock error = %v, want context canceled", err)
	}
	if lock != nil {
		t.Fatal("canceled acquisition returned a lock")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("canceled acquisition changed stale lock: %v", err)
	}
}
