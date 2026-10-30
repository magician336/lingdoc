package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Only a callback failure followed by a successful rollback receives this
// marker. In particular an unknown COMMIT outcome is never safe to retry.
type verifiedSQLiteRollback struct{ cause error }

func (e verifiedSQLiteRollback) Error() string { return e.cause.Error() }
func (e verifiedSQLiteRollback) Unwrap() error { return e.cause }

func retryVerifiedSQLiteOperation(ctx context.Context, operation func(context.Context) (json.RawMessage, int, bool, error)) (json.RawMessage, int, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, false, err
	}
	raw, status, replay, err := operation(ctx)
	var rolledBack verifiedSQLiteRollback
	if err == nil || !errors.As(err, &rolledBack) || !isSQLiteLockError(err) {
		return raw, status, replay, err
	}
	deadline, cancel := context.WithTimeout(ctx, specLockRetryBudget)
	defer cancel()
	delay := specLockInitialDelay
	for attempt := 1; attempt < specLockMaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return nil, 0, false, ctx.Err()
		}
		timer := time.NewTimer(delay)
		select {
		case <-deadline.Done():
			timer.Stop()
			if ctx.Err() != nil {
				return nil, 0, false, ctx.Err()
			}
			return nil, 0, false, ErrRequestInProgress
		case <-timer.C:
		}
		raw, status, replay, err = operation(deadline)
		if err == nil || !errors.As(err, &rolledBack) || !isSQLiteLockError(err) {
			return raw, status, replay, err
		}
		delay = min(delay*2, specLockMaximumDelay)
	}
	return nil, 0, false, ErrRequestInProgress
}
