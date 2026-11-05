package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestSpecRetryBudgetWaitsBeyondThreeRetries(t *testing.T) {
	calls := 0
	want := json.RawMessage(`{"spec_revision":1}`)
	raw, status, replayed, err := retrySQLiteSpecOperation(context.Background(), time.Second,
		func(ctx context.Context) (json.RawMessage, int, bool, error) {
			calls++
			if calls > 1 {
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("retry database operation has no deadline")
				}
			}
			if calls <= 4 {
				return nil, 0, false, errors.New("database is locked")
			}
			return want, 200, true, nil
		})
	if err != nil || calls != 5 || status != 200 || !replayed || string(raw) != string(want) {
		t.Fatalf("retry = %s/%d/%v/%v, calls=%d", raw, status, replayed, err, calls)
	}
}

func TestSpecRetryBudgetBoundsDatabaseAttempt(t *testing.T) {
	calls := 0
	_, _, _, err := retrySQLiteSpecOperation(context.Background(), 25*time.Millisecond,
		func(ctx context.Context) (json.RawMessage, int, bool, error) {
			calls++
			if calls == 1 {
				return nil, 0, false, errors.New("database is locked")
			}
			<-ctx.Done() // Simulate a driver waiting within the recovery deadline.
			return nil, 0, false, ctx.Err()
		})
	if !errors.Is(err, ErrRequestInProgress) || calls < 1 || calls > 2 {
		t.Fatalf("retry = %v, calls=%d; want bounded request_in_progress", err, calls)
	}
}

func TestSpecRetryBudgetPreservesCallerAndDomainErrors(t *testing.T) {
	t.Run("caller cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		calls := 0
		_, _, _, err := retrySQLiteSpecOperation(ctx, time.Second, func(context.Context) (json.RawMessage, int, bool, error) {
			calls++
			cancel()
			return nil, 0, false, errors.New("database is locked")
		})
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("retry = %v, calls=%d", err, calls)
		}
	})
	t.Run("caller deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()
		_, _, _, err := retrySQLiteSpecOperation(ctx, time.Second, func(context.Context) (json.RawMessage, int, bool, error) {
			return nil, 0, false, errors.New("database is locked")
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("retry = %v; caller deadline must not become request_in_progress", err)
		}
	})
	for _, domainErr := range []error{ErrVersionConflict, ErrIdempotencyConflict, ErrNotFound} {
		t.Run(domainErr.Error(), func(t *testing.T) {
			calls := 0
			_, _, _, err := retrySQLiteSpecOperation(context.Background(), time.Second, func(context.Context) (json.RawMessage, int, bool, error) {
				calls++
				if calls == 1 {
					return nil, 0, false, errors.New("database is locked")
				}
				return nil, 0, false, domainErr
			})
			if !errors.Is(err, domainErr) || calls != 2 {
				t.Fatalf("retry = %v, calls=%d; want %v once discovered", err, calls, domainErr)
			}
		})
	}
}
