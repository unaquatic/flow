package ui

import "github.com/unaquatic/fluid/style"

// textFg is the text of the transcript, the input, titles, and the keys the
// UI names, which the UI also draws with outside of the theme: the default
// theme's TextBase.
var textFg = DefaultTheme().TextBase

// mutedFg is the dim text the UI draws outside of the theme: the count and
// the keys after a panel's title, menu descriptions and values, the labels
// of the Usage box and the diff toast, the indicator, the input's
// placeholder, and the words between keys in the hints: the default theme's
// TextMuted.
var mutedFg = DefaultTheme().TextMuted

// DefaultTheme returns a new default palette for the chat UI: Dark Flow.
func DefaultTheme() *style.Theme {
	return style.DarkFlow()
}

// themeOrDefault returns t, or a new default theme when t is nil.
func themeOrDefault(t *style.Theme) *style.Theme {
	if t == nil {
		return DefaultTheme()
	}
	return t
}
