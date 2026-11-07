package logging

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// stderrFile returns the device and inode of the file standard error writes
// to.
func stderrFile(t *testing.T) (dev uint64, ino uint64) {
	t.Helper()
	var st unix.Stat_t
	if err := unix.Fstat(unix.Stderr, &st); err != nil {
		t.Fatal(err)
	}
	return uint64(st.Dev), st.Ino
}

func TestCaptureStderr(t *testing.T) {
	name := filepath.Join(t.TempDir(), "test.log")
	devBefore, inoBefore := stderrFile(t)

	restore := CaptureStderr(name)
	if dev, ino := stderrFile(t); dev == devBefore && ino == inoBefore {
		restore()
		t.Fatal("standard error was not captured")
	}
	fmt.Fprintln(os.Stderr, "captured line")
	restore()
	restore() // A second call does nothing.

	if dev, ino := stderrFile(t); dev != devBefore || ino != inoBefore {
		t.Error("standard error was not restored")
	}
	log, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(log, []byte("captured line\n")) {
		t.Errorf("log = %q, want the line written while captured", log)
	}
}
