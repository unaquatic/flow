//go:build windows

package logging

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// fileID identifies the standard error handle and the file os.Stderr writes
// to, which CaptureStderr both change.
type fileID struct {
	handle windows.Handle
	file   *os.File
}

// stderrFile returns the process's standard error handle and os.Stderr.
func stderrFile(t *testing.T) fileID {
	t.Helper()
	h, err := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
	if err != nil {
		t.Fatal(err)
	}
	return fileID{handle: h, file: os.Stderr}
}
