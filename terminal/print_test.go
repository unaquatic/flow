package terminal

import (
	"strings"
	"testing"

	"github.com/unaquatic/flow/product"
	"github.com/unaquatic/fluid/style"
)

// colorModes are the color modes the printed text is checked in, with the
// sequences that draw the primary orange in each: itself in 24-bit color, its
// palette color, xterm color 202, on 256-color terminals, and white, ANSI
// color 7, on 16-color terminals. The icon draws its "f" in near-black,
// xterm color 233 or black, on a square of the primary orange, xterm color
// 202 or white.
var colorModes = []struct {
	name         string
	mode         style.ColorMode
	orange, icon string
}{
	{name: "truecolor", mode: style.ColorTrueColor, orange: "\x1b[38;2;255;95;0m", icon: "\x1b[38;2;17;17;17m\x1b[48;2;255;95;0m"},
	{name: "256", mode: style.Color256, orange: "\x1b[38;5;202m", icon: "\x1b[38;5;233m\x1b[48;5;202m"},
	{name: "16", mode: style.Color16, orange: "\x1b[37m", icon: "\x1b[30m\x1b[47m"},
}

// TestANSI checks that ANSI marks up a chunk in its attributes and its
// colors, as ColorIndexers converts them in each mode, and ends it with a
// reset, and that it writes an unstyled chunk, or one in transparent colors,
// as it is, and an empty one not at all.
func TestANSI(t *testing.T) {
	orange := style.ColorFromHex(product.PrimaryColor)
	st := style.NewStyledText(
		style.Plain("a "),
		style.TextChunk{Text: "flow", Fg: orange, Attributes: style.AttrBold},
		style.TextChunk{Fg: orange},
		style.TextChunk{Text: " b", Fg: style.ColorTransparent, Bg: style.ColorTransparent},
	)
	for _, tt := range colorModes {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := ANSI(st, tt.mode), "a \x1b[1m"+tt.orange+"flow\x1b[0m b"; got != want {
				t.Errorf("ANSI = %q, want %q", got, want)
			}
		})
	}
}

// TestIconBanner checks that the banner of help and the resume notice is the
// icon after a blank line, its "f" near-black on a square of the primary
// orange, as the UI draws it.
func TestIconBanner(t *testing.T) {
	if got, want := IconBanner().PlainText(), "\n"+product.Icon; got != want {
		t.Errorf("plain icon banner =\n%q\nwant\n%q", got, want)
	}
	for _, tt := range colorModes {
		t.Run(tt.name, func(t *testing.T) {
			var lines []string
			for line := range strings.SplitSeq(product.Icon, "\n") {
				lines = append(lines, tt.icon+line+sgrReset)
			}
			if got, want := ANSI(IconBanner(), tt.mode), "\n"+strings.Join(lines, "\n"); got != want {
				t.Errorf("icon banner =\n%q\nwant\n%q", got, want)
			}
		})
	}
}

// TestResumeText checks that the printed resume notice is the icon banner, then
// after a blank line a bold line describing the command right before the
// command, which is indented by two spaces, its product name bold and
// orange. Text other than the icon and the product name keeps the terminal's
// color.
func TestResumeText(t *testing.T) {
	const id = "01a12e38-2689-73a1-a799-5db6b6a8bc44"
	const intro = "Resume this session with:"
	text := ResumeText(id)
	if got, want := text.PlainText(), "\n"+product.Icon+"\n\n"+intro+"\n  "+product.Name+" --session "+id; got != want {
		t.Errorf("plain notice =\n%q\nwant\n%q", got, want)
	}
	for _, tt := range colorModes {
		t.Run(tt.name, func(t *testing.T) {
			want := ANSI(IconBanner(), tt.mode) + "\n\n\x1b[1m" + intro + sgrReset +
				"\n  \x1b[1m" + tt.orange + product.Name + sgrReset + " --session " + id
			if got := ANSI(text, tt.mode); got != want {
				t.Errorf("notice =\n%q\nwant\n%q", got, want)
			}
		})
	}
}
