package ui

import (
	"fmt"
	"log/slog"

	"github.com/unaquatic/flow/config"
)

// SettingsStore saves the settings that /settings sets, as the app saves them
// in its config file: the terminal's, and debug.
type SettingsStore interface {
	// SaveTerminal saves the terminal's settings.
	SaveTerminal(t config.Terminal) error
	// SaveDebug saves the debug setting.
	SaveDebug(on bool) error
}

// The names the config file saves each setting's values under, as
// config.Terminal lists them. A value it leaves out, or does not name,
// is the setting's zero value, which is its default.
var (
	cacheModeKeys     = map[CacheMode]string{CacheOff: "off", CachePerTurn: "per_turn", CachePerMessage: "per_message"}
	expandedStyleKeys = map[ExpandedStyle]string{ExpandedBoxed: "boxed", ExpandedPlain: "plain"}
	toolNameColorKeys = map[ToolNameColor]string{ToolNameGray: "gray", ToolNameWhite: "white"}
	markerShapeKeys   = map[MarkerShape]string{
		MarkerSquare: "square", MarkerSquareOutline: "square_outline", MarkerRoundedOutline: "rounded_outline",
		MarkerCircle: "circle", MarkerCircleOutline: "circle_outline", MarkerDiamond: "diamond",
		MarkerDiamondOutline: "diamond_outline", MarkerBlock: "block",
	}
	keyHintsModeKeys   = map[KeyHintsMode]string{KeyHintsTitle: "title", KeyHintsSubtitle: "subtitle", KeyHintsFooter: "footer"}
	countPlaceKeys     = map[CountPlace]string{CountBelow: "below", CountInTitle: "title"}
	promptStyleKeys    = map[PromptStyle]string{PromptFilled: "filled", PromptBar: "bar"}
	inputStyleKeys     = map[InputStyle]string{InputBar: "bar", InputFilled: "filled"}
	selectionStyleKeys = map[SelectionStyle]string{SelectionFullWidth: "full_width", SelectionFill: "fill", SelectionArrow: "arrow"}
)

// SettingValue returns the value that keys saves under key, or the zero
// value of T, the setting's default, when it saves none under it.
func SettingValue[T comparable](keys map[T]string, key string) T {
	for v, k := range keys {
		if k == key {
			return v
		}
	}
	var zero T
	return zero
}

// ApplyConfig sets the settings of st as the config file saves them: t, the
// terminal's, and debug.
func (st *UIState) ApplyConfig(t config.Terminal, debug bool) {
	st.Inline = !t.Fullscreen
	st.CacheMode = SettingValue(cacheModeKeys, t.CacheUsage)
	st.ExpandStyle = SettingValue(expandedStyleKeys, t.ExpandedTools)
	st.ToolMarkers = t.ToolMarker
	st.MarkerShape = SettingValue(markerShapeKeys, t.ToolMarkerShape)
	st.ToolColor = SettingValue(toolNameColorKeys, t.ToolNameColor)
	st.KeyHints = SettingValue(keyHintsModeKeys, t.KeyHints)
	st.Count = SettingValue(countPlaceKeys, t.ListCount)
	st.Selection = SettingValue(selectionStyleKeys, t.Selection)
	st.PromptStyle = SettingValue(promptStyleKeys, t.PromptStyle)
	st.InputStyle = SettingValue(inputStyleKeys, t.InputStyle)
	st.Debug = debug
}

// TerminalConfig returns the terminal's settings in st as the config file
// saves them, each value named, so that the file shows them.
func (st *UIState) TerminalConfig() config.Terminal {
	return config.Terminal{
		Fullscreen:      !st.Inline,
		CacheUsage:      cacheModeKeys[st.CacheMode],
		ExpandedTools:   expandedStyleKeys[st.ExpandStyle],
		ToolMarker:      st.ToolMarkers,
		ToolMarkerShape: markerShapeKeys[st.MarkerShape],
		ToolNameColor:   toolNameColorKeys[st.ToolColor],
		KeyHints:        keyHintsModeKeys[st.KeyHints],
		ListCount:       countPlaceKeys[st.Count],
		Selection:       selectionStyleKeys[st.Selection],
		PromptStyle:     promptStyleKeys[st.PromptStyle],
		InputStyle:      inputStyleKeys[st.InputStyle],
	}
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
