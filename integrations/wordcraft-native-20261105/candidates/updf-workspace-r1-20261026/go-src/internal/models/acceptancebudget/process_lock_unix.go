//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package acceptancebudget

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryProcessGuard(file *os.File) (bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
		return false, nil
	}
	return err == nil, err
}

func unlockProcessGuard(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}
