package ui

import (
	"fmt"
	"log/slog"

	"github.com/unaquatic/flow/config"
)

// SettingsStore saves the settings that /config sets, as the app saves them
// in its config file: the terminal's, and debug.
type SettingsStore interface {
	// SaveTerminal saves the terminal's settings.
	SaveTerminal(t config.Terminal) error
	// SaveDebug saves the debug setting.
	SaveDebug(on bool) error
}

// ApplyConfig sets the settings of st as the config file saves them: t, the
// terminal's, and debug. The style settings /config shows while debug is
// on keep their defaults, as the config file saves none of them.
func (st *UIState) ApplyConfig(t config.Terminal, debug bool) {
	st.Inline = !t.Fullscreen
	st.ShowSnapshots = t.Snapshots
	st.Debug = debug
}

// TerminalConfig returns the terminal's settings in st as the config file
// saves them.
func (st *UIState) TerminalConfig() config.Terminal {
	return config.Terminal{Fullscreen: !st.Inline, Snapshots: st.ShowSnapshots}
}

// PersistTerminal saves the terminal's settings as they are now in Settings,
// and returns a toast that reports a failure, or nothing. It saves nothing
// without Settings.
func (s *Service) PersistTerminal() []UIEvent {
	if s.Settings == nil {
		return nil
	}
	return s.persisted(s.Settings.SaveTerminal(s.state.TerminalConfig()))
}

// PersistDebug saves the debug setting as it is now in Settings, as
// PersistTerminal saves the terminal's.
func (s *Service) PersistDebug() []UIEvent {
	if s.Settings == nil {
		return nil
	}
	return s.persisted(s.Settings.SaveDebug(s.state.Debug))
}

// persisted returns a toast that reports err, the failure to save a setting,
// or nothing when err is nil. The setting applies all the same.
func (s *Service) persisted(err error) []UIEvent {
	if err == nil {
		return nil
	}
	slog.Warn("save settings", "err", err)
	return s.ShowToast(fmt.Sprintf("Saving settings failed: %v", err))
}
