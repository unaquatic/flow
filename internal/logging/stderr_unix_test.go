//go:build unix

package logging

import (
	"testing"

	"golang.org/x/sys/unix"
)

// fileID identifies an open file.
type fileID struct {
	dev, ino uint64
}

// stderrFile returns the device and inode of the file standard error writes
// to.
func stderrFile(t *testing.T) fileID {
	t.Helper()
	var st unix.Stat_t
	if err := unix.Fstat(unix.Stderr, &st); err != nil {
		t.Fatal(err)
	}
	return fileID{dev: uint64(st.Dev), ino: st.Ino}
}
