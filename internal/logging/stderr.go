package logging

import (
	"log/slog"
	"sync"
)

// CaptureStderr sends standard error to the named log file until restore is
// called, so that stray writes and crash reports do not draw over a terminal
// UI. Calling restore again does nothing. It captures nothing, and restore
// does nothing, when the log cannot be opened or standard error cannot be
// redirected.
func CaptureStderr(name string) (restore func()) {
	f, err := OpenFile(name)
	if err != nil {
		slog.Warn("capture stderr", "err", err)
		return func() {}
	}
	defer f.Close()

	restore, err = redirectStderr(f)
	if err != nil {
		slog.Warn("capture stderr", "err", err)
		return func() {}
	}
	return sync.OnceFunc(restore)
}
