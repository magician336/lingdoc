package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	// This is a total budget for recovery after the first SQLite lock error,
	// not a fresh timeout per retry. The caller's earlier deadline always wins.
	specLockRetryBudget  = 5 * time.Second
	specLockMaxAttempts  = 64
	specLockInitialDelay = 5 * time.Millisecond
	specLockMaximumDelay = 100 * time.Millisecond
)

// retrySQLiteSpecOperation retries a complete authorized, idempotent transaction.
// A read-to-write upgrade can report BUSY immediately while a competing writer
// is still committing; a handful of 5/10/15ms sleeps does not wait for that
// writer. Back off within one bounded recovery deadline instead. Never infer a
// version conflict from an independent read: the transaction must check replay
// before checking a new write's expected revision.
func retrySQLiteSpecOperation(
	ctx context.Context, budget time.Duration,
	operation func(context.Context) (json.RawMessage, int, bool, error),
) (json.RawMessage, int, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, false, err
	}
	raw, status, replayed, err := operation(ctx)
	if !isSQLiteLockError(err) {
		return raw, status, replayed, err
	}

	retryCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	stopped := func() (json.RawMessage, int, bool, error) {
		if err := ctx.Err(); err != nil {
			return nil, 0, false, err
		}
		return nil, 0, false, ErrRequestInProgress
	}
	delay := specLockInitialDelay
	for attempt := 1; attempt < specLockMaxAttempts; attempt++ {
		if retryCtx.Err() != nil {
			return stopped()
		}
		timer := time.NewTimer(delay)
		select {
		case <-retryCtx.Done():
			timer.Stop()
			return stopped()
		case <-timer.C:
		}
		if retryCtx.Err() != nil {
			return stopped()
		}
		// The deadline is also passed to database calls, not only to the timer.
		raw, status, replayed, err = operation(retryCtx)
		if err == nil {
			return raw, status, replayed, nil // Do not hide a confirmed commit.
		}
		if ctx.Err() != nil {
			return stopped()
		}
		if retryCtx.Err() != nil && (isSQLiteLockError(err) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)) {
			return stopped()
		}
		if !isSQLiteLockError(err) {
			return raw, status, replayed, err
		}
		delay = min(delay*2, specLockMaximumDelay)
	}
	return stopped()
}
