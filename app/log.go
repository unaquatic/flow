package app

import (
	"log/slog"
	"path/filepath"

	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/internal/logging"
)

// logFile returns the path of the application log, logs/app.log in data
// directory dir.
func logFile(dir string) string {
	return filepath.Join(dir, "logs", "app.log")
}

// InitLogging writes structured logs to the application log in data
// directory dir, at level logLevel and above, as [logging.Init] does.
// Standard error is left to the terminal; see [App.CaptureStderr].
func InitLogging(dir string, logLevel config.LogLevel) {
	if err := logging.Init(logFile(dir), logLevel.Level()); err != nil {
		return
	}
	slog.Info("start")
}

// CaptureStderr sends standard error to the application log until restore is
// called, as [logging.CaptureStderr] does.
func (a *App) CaptureStderr() (restore func()) {
	return logging.CaptureStderr(logFile(a.config.Dir))
}
