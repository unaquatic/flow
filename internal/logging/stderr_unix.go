//go:build unix

package logging

import (
	"log/slog"
	"os"

	"golang.org/x/sys/unix"
)

// redirectStderr makes descriptor 2 write to f, which the caller may close
// once it returns, and returns a func that points it back.
func redirectStderr(f *os.File) (restore func(), err error) {
	// The saved descriptor is closed on exec, so that the processes the
	// agent's tools start do not inherit it.
	saved, err := unix.FcntlInt(uintptr(unix.Stderr), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.Dup2(int(f.Fd()), unix.Stderr); err != nil {
		unix.Close(saved)
		return nil, err
	}
	return func() {
		if err := unix.Dup2(saved, unix.Stderr); err != nil {
			slog.Warn("restore stderr", "err", err)
		}
		unix.Close(saved)
	}, nil
}
