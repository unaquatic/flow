//go:build windows

package logging

import (
	"log/slog"
	"os"

	"golang.org/x/sys/windows"
)

// redirectStderr makes the process's standard error handle, which crash
// reports are written to, and os.Stderr write to f, which the caller may
// close once it returns, and returns a func that points them back. os.Stderr
// is replaced too, as it keeps the handle it was opened with.
func redirectStderr(f *os.File) (restore func(), err error) {
	// The duplicate is not inheritable, so that the processes the agent's
	// tools start do not inherit it.
	p := windows.CurrentProcess()
	var h windows.Handle
	if err := windows.DuplicateHandle(p, windows.Handle(f.Fd()), p, &h, 0, false, windows.DUPLICATE_SAME_ACCESS); err != nil {
		return nil, err
	}
	saved, err := windows.GetStdHandle(windows.STD_ERROR_HANDLE)
	if err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	if err := windows.SetStdHandle(windows.STD_ERROR_HANDLE, h); err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	savedFile := os.Stderr
	os.Stderr = os.NewFile(uintptr(h), f.Name())
	return func() {
		if err := windows.SetStdHandle(windows.STD_ERROR_HANDLE, saved); err != nil {
			slog.Warn("restore stderr", "err", err)
		}
		captured := os.Stderr
		os.Stderr = savedFile
		captured.Close()
	}, nil
}
