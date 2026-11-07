package ui

import "github.com/unaquatic/fluid/style"

// textFg is the text of the transcript, the input, titles, and the keys the
// UI names, which the UI also draws with outside of the theme: the default
// theme's TextBase.
var textFg = DefaultTheme().TextBase

// DefaultTheme returns a new default palette for the chat UI: Darcula Solid.
func DefaultTheme() *style.Theme {
	return style.DarculaSolid()
}

// themeOrDefault returns t, or a new default theme when t is nil.
func themeOrDefault(t *style.Theme) *style.Theme {
	if t == nil {
		return DefaultTheme()
	}
	return t
}
