package logging

import (
	"log/slog"
	"sync"

	"golang.org/x/sys/unix"
)

// CaptureStderr sends standard error to the named log file until restore is
// called, so that stray writes and crash reports do not draw over a terminal
// UI. Calling restore again does nothing. It captures nothing, and restore
// does nothing, when the log cannot be opened or the descriptors cannot be
// duplicated.
func CaptureStderr(name string) (restore func()) {
	f, err := OpenFile(name)
	if err != nil {
		slog.Warn("capture stderr", "err", err)
		return func() {}
	}
	defer f.Close()

	// The saved descriptor is closed on exec, so that the processes the
	// agent's tools start do not inherit it.
	saved, err := unix.FcntlInt(uintptr(unix.Stderr), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		slog.Warn("capture stderr", "err", err)
		return func() {}
	}
	if err := unix.Dup2(int(f.Fd()), unix.Stderr); err != nil {
		slog.Warn("capture stderr", "err", err)
		unix.Close(saved)
		return func() {}
	}
	return sync.OnceFunc(func() {
		if err := unix.Dup2(saved, unix.Stderr); err != nil {
			slog.Warn("restore stderr", "err", err)
		}
		unix.Close(saved)
	})
}
