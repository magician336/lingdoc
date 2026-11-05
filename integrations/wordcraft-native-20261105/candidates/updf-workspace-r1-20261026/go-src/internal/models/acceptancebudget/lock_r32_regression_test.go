package acceptancebudget

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestR32LiveOwnerCannotBeReclaimedAfterLeaseAge(t *testing.T) {
	old := lockLeaseHeartbeatInterval
	lockLeaseHeartbeatInterval = time.Hour
	defer func() { lockLeaseHeartbeatInterval = old }()
	path := filepath.Join(t.TempDir(), "calls.json.lock")
	owner, err := AcquireLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ReleaseLock(owner, path) }()
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-2 * LockLeaseTTL)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 160*time.Millisecond)
	defer cancel()
	contender, err := AcquireLock(ctx, path)
	if contender != nil {
		_ = ReleaseLock(contender, path)
		t.Fatalf("live reservation was replaced after its diagnostic mtime aged: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contender error = %v, want deadline", err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("active marker changed: %v", err)
	}
}

func TestR32LateHeartbeatCannotCreateTwoOwners(t *testing.T) {
	old := lockLeaseHeartbeatInterval
	lockLeaseHeartbeatInterval = time.Hour
	defer func() { lockLeaseHeartbeatInterval = old }()
	path := filepath.Join(t.TempDir(), "calls.json.lock")
	owner, err := AcquireLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ReleaseLock(owner, path) }()
	// Reproduce the old handoff state directly: the original descriptor remains
	// open after rename, and a late heartbeat lands on that very inode.
	moved := path + ".reclaim-test"
	if err := os.Rename(path, moved); err != nil {
		if runtime.GOOS == "windows" {
			t.Skip("Windows forbids moving this open marker; active-owner exclusion is independently tested")
		}
		t.Fatal(err)
	}
	if _, err := fmt.Fprintln(owner, "heartbeat=renewed-before-delete"); err != nil {
		t.Fatal(err)
	}
	if err := owner.Sync(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 160*time.Millisecond)
	defer cancel()
	contender, err := AcquireLock(ctx, path)
	if contender != nil {
		_ = ReleaseLock(contender, path)
		t.Fatal("late-renewed original descriptor and replacement both own the ledger")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contender error = %v, want deadline", err)
	}
	if _, err := os.Stat(moved); err != nil {
		t.Fatalf("late-renewed marker deleted: %v", err)
	}
}
