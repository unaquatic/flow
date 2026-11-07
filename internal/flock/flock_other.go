//go:build !(darwin || dragonfly || freebsd || linux || netbsd || openbsd || windows)

package flock

import (
	"errors"
	"os"
)

// TryLock reports that file locking is not implemented on this platform.
func TryLock(*os.File, bool) (bool, error) {
	return false, errors.ErrUnsupported
}

// Unlock reports that file locking is not implemented on this platform.
func Unlock(*os.File) error {
	return errors.ErrUnsupported
}
