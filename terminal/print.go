package terminal

import (
	"slices"
	"strings"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/terminal/ui"
	"github.com/unaquatic/fluid/style"
)

// sgrReset turns off every attribute and color that ANSI turns on.
const sgrReset = "\x1b[0m"

// ANSI returns st as text to print to a terminal in mode outside the UI,
// marked up with SGR escape sequences: each chunk in its attributes and its
// colors, which it converts to the palette entries of mode that the UI draws
// them in. A transparent color keeps the terminal's own, and each styled
// chunk ends with a reset, so that no style reaches past it.
func ANSI(st style.StyledText, mode style.ColorMode) string {
	x := ui.ColorIndexers(mode)
	var b strings.Builder
	for _, c := range st.Chunks {
		if c.Text == "" {
			continue
		}
		sgr := c.Attributes.ANSI()
		if c.Fg.A != 0 {
			sgr += x.FgSeq(c.Fg, mode)
		}
		if c.Bg.A != 0 {
			sgr += x.BgSeq(c.Bg, mode)
		}
		if sgr == "" {
			b.WriteString(c.Text)
			continue
		}
		b.WriteString(sgr + c.Text + sgrReset)
	}
	return b.String()
}

// LogoBanner returns the logo that help, the version and the resume notice
// print, its "f" in the product's orange and the rest light grey, as the UI
// draws it, after a blank line that sets it apart from the command line
// above it.
func LogoBanner() style.StyledText {
	return ui.JoinStyledLines(append([]style.StyledText{{}}, slices.Collect(ui.LogoLines())...))
}

// ResumeText returns the notice that tells how to resume session id as text
// to print, once fullscreen mode has taken its screen and the notice on it:
// LogoBanner, a blank line, a bold line that introduces the command, and
// the command, indented, with the product name bold and orange. The other
// text keeps the terminal's own color.
func ResumeText(id message.SessionID) style.StyledText {
	indent := style.NewStyledText(style.Plain(strings.Repeat(" ", ui.ResumeIndent)))
	return ui.JoinStyledLines([]style.StyledText{
		LogoBanner(),
		{},
		ui.ResumeIntro(style.ColorTransparent),
		indent.Append(ui.ResumeCommand(id, style.ColorTransparent)),
	})
}
