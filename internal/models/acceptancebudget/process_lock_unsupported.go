//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package acceptancebudget

import (
	"fmt"
	"os"
	"runtime"
)

func tryProcessGuard(_ *os.File) (bool, error) {
	return false, fmt.Errorf("%w: kernel file lock unsupported on %s", ErrUnavailable, runtime.GOOS)
}

func unlockProcessGuard(_ *os.File) error { return nil }
