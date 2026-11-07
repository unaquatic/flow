//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package flock

import (
	"errors"
	"os"
	"syscall"
)

// TryLock takes a flock(2) lock on f without waiting. It reports false when
// another open file holds a conflicting lock.
func TryLock(f *os.File, exclusive bool) (bool, error) {
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	err := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EINTR) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Unlock releases the flock(2) lock on f.
func Unlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
