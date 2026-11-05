package acceptancebudget

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestR32LockProcessHelper(t *testing.T) {
	mode := os.Getenv("LINGDOC_R32_TEST_MODE")
	if mode == "" {
		return
	}
	path := os.Getenv("LINGDOC_R32_TEST_PATH")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if mode == "reserve-many" {
		n, err := strconv.Atoi(os.Getenv("LINGDOC_R32_TEST_COUNT"))
		if err != nil {
			t.Fatal(err)
		}
		b := New(CostOnlyProfile, path)
		for i := 0; i < n; i++ {
			r, err := b.Reserve(ctx, DeepSeekModel, 10, 10)
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(2 * time.Millisecond)
			if err := r.Complete("response_received", 5, 5, "stop", false); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	if mode == "budget-crash" {
		b := New(CostOnlyProfile, path)
		if _, err := b.Reserve(ctx, DeepSeekModel, 10, 10); err != nil {
			t.Fatal(err)
		}
		os.Exit(0) // no defer: OS must release the guard; in_flight must survive
	}
	lockLeaseHeartbeatInterval = time.Hour
	lock, err := AcquireLock(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "marker-crash" {
		os.Exit(0)
	}
	defer func() {
		if err := ReleaseLock(lock, path); err != nil {
			t.Error(err)
		}
	}()
	ready := os.Getenv("LINGDOC_R32_TEST_READY")
	if err := os.WriteFile(ready, []byte("ready"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := make([]byte, 1)
	for {
		if _, err := io.ReadFull(os.Stdin, command); err != nil {
			return
		}
		if command[0] == 'q' {
			return
		}
		if command[0] == 'r' {
			if _, err := fmt.Fprintln(lock, "heartbeat=child-renewed"); err != nil {
				t.Fatal(err)
			}
			if err := lock.Sync(); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(ready+".renewed", []byte("renewed"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func r32Command(t *testing.T, mode, path string, extra ...string) *exec.Cmd {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestR32LockProcessHelper$", "-test.timeout=18s")
	cmd.Env = append(os.Environ(), "LINGDOC_R32_TEST_MODE="+mode, "LINGDOC_R32_TEST_PATH="+path)
	cmd.Env = append(cmd.Env, extra...)
	return cmd
}

func r32AwaitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("owned helper readiness not observed: %s", filepath.Base(path))
}

func TestR32CrossProcessOwnerExcludesStaleAndLateRenewedContender(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "calls.json.lock")
	ready := filepath.Join(dir, "ready")
	cmd := r32Command(t, "hold", path, "LINGDOC_R32_TEST_READY="+ready)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	r32AwaitFile(t, ready)
	initial, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stale := time.Now().Add(-2 * LockLeaseTTL)
	if err := os.Chtimes(path, stale, stale); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		other, e := AcquireLock(ctx, path)
		if other != nil {
			_ = ReleaseLock(other, path)
			done <- errors.New("second owner obtained live child's lock")
			return
		}
		done <- e
	}()
	if _, err := input.Write([]byte("r")); err != nil {
		t.Fatal(err)
	}
	r32AwaitFile(t, ready+".renewed")
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cross-process acquisition = %v", err)
	}
	after, err := os.Stat(path)
	if err != nil || !os.SameFile(initial, after) {
		t.Fatalf("child marker replaced: %v", err)
	}
	if _, err := input.Write([]byte("q")); err != nil {
		t.Fatal(err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("owned helper failed: %v: %s", err, output.String())
	}
}

func TestR32CrashReleasesKernelGuardWithoutWaitingForLeaseAge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.json.lock")
	if out, err := r32Command(t, "marker-crash", path).CombinedOutput(); err != nil {
		t.Fatalf("crash helper: %v %s", err, out)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	owner, err := AcquireLock(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ReleaseLock(owner, path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".guard"); err != nil {
		t.Fatalf("stable guard removed: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("owner marker remains: %v", err)
	}
}

func TestR32CrashRecoveryDoesNotClearInflightBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.json")
	if out, err := r32Command(t, "budget-crash", path).CombinedOutput(); err != nil {
		t.Fatalf("crash helper: %v %s", err, out)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := New(CostOnlyProfile, path).Reserve(ctx, DeepSeekModel, 10, 10); !errors.Is(err, ErrUnavailable) && !errors.Is(err, ErrBlocked) {
		t.Fatalf("after crash reserve = %v, want incomplete/unresolved history refused", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("crash recovery rewrote/refunded historical reservation")
	}
	ledger, err := readLedger(path, CostOnlyProfile)
	if err != nil || len(ledger.Requests) != 1 || ledger.Requests[0].Status != "in_flight" || ledger.ReservedNanoCNY == 0 {
		t.Fatalf("lost crash history: %v", err)
	}
}

func TestR32ParallelProcessesPreserveCompleteBudgetSequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.json")
	const workers = 3
	const each = 4
	commands := make([]*exec.Cmd, 0, workers)
	outputs := make([]*bytes.Buffer, 0, workers)
	for i := 0; i < workers; i++ {
		cmd := r32Command(t, "reserve-many", path, "LINGDOC_R32_TEST_COUNT="+strconv.Itoa(each))
		output := new(bytes.Buffer)
		cmd.Stdout = output
		cmd.Stderr = output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, cmd)
		outputs = append(outputs, output)
		t.Cleanup(func() { _ = cmd.Process.Kill() })
	}
	for i, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("worker %d: %v %s", i, err, outputs[i].String())
		}
	}
	ledger, err := readLedger(path, CostOnlyProfile)
	if err != nil || !validLedger(ledger, CostOnlyProfile) || ledger.Blocked || len(ledger.Requests) != workers*each {
		t.Fatalf("invalid final sequence: %v", err)
	}
	for i, r := range ledger.Requests {
		if r.Number != i+1 || r.Status != "response_received" || r.InputTokens != 5 || r.OutputTokens != 5 {
			t.Fatalf("lost or duplicate request: %+v", r)
		}
	}
}

func TestR32NilOrWrongPathReleaseCannotDeleteAnotherMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "calls.json.lock")
	owner, err := AcquireLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ReleaseLock(owner, path) }()
	if err := ReleaseLock(nil, path); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseLock(owner, path+".other"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("wrong-path release = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("active marker removed: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	other, err := AcquireLock(ctx, path)
	if other != nil {
		_ = ReleaseLock(other, path)
		t.Fatal("wrong release unlocked guard")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
