package acceptancebudget

import (
	"context"
	"errors"
	"os"
	"time"
)

// The guard inode is permanent: unlinking it would let waiters lock different
// files. The OS lock, not the diagnostic marker's age, determines ownership.
const guardedLockProtocol = "lingdoc-budget-lock-v2\n"

var ErrLegacyLock = errors.New("legacy or incomplete acceptance lock requires offline owner verification")

func acquireProcessGuard(ctx context.Context, path string) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	guard, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = guard.Close()
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		locked, err := tryProcessGuard(guard)
		if err != nil {
			return nil, err
		}
		if locked {
			if err := ctx.Err(); err != nil {
				_ = unlockProcessGuard(guard)
				return nil, err
			}
			keep = true
			return guard, nil
		}
		if err := waitForLock(ctx); err != nil {
			return nil, err
		}
	}
}

func releaseProcessGuard(guard *os.File) error {
	if guard == nil {
		return nil
	}
	return errors.Join(unlockProcessGuard(guard), guard.Close())
}

func waitForLock(ctx context.Context) error {
	timer := time.NewTimer(25 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
