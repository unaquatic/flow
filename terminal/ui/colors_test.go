package ui

import (
	"testing"

	"github.com/unaquatic/fluid/style"
)

// TestColorChoices checks that every row of BackgroundChoices and of
// TextChoices names an opaque color of its own in its list, as a second row
// of a color would silently replace the first, and that every ANSI16 entry is
// one of the 16 ANSI colors, as 16-color mode ignores the others.
func TestColorChoices(t *testing.T) {
	for name, choices := range map[string][]ColorChoice{"background": BackgroundChoices, "text": TextChoices} {
		seen := make(map[style.Color]int)
		for i, c := range choices {
			if c.Color.A != 255 {
				t.Errorf("%s row %d: color %s is not opaque", name, i, c.Color.ToHex())
			}
			if j, ok := seen[c.Color]; ok {
				t.Errorf("%s rows %d and %d both name %s", name, j, i, c.Color.ToHex())
			}
			seen[c.Color] = i
			if c.ANSI16 > style.ANSIBrightWhite {
				t.Errorf("%s row %d: %s has ANSI16 entry %d, past bright white", name, i, c.Color.ToHex(), c.ANSI16)
			}
		}
	}
}

// TestColorIndexers checks the sequences that ColorIndexers give theme and
// UI colors, the normal and bright ANSI colors in 16-color mode and the
// xterm entries in 256-color mode, as backgrounds and as text apart, and that
// the colors the choices do not list fall back to the mode's own Indexer,
// and every color in 24-bit color keeps the sequence fluid picks.
func TestColorIndexers(t *testing.T) {
	theme := DefaultTheme()
	unlisted := style.ColorFromHex("#123456")
	for _, tt := range []struct {
		name  string
		mode  style.ColorMode
		color style.Color
		bg    bool
		want  string
	}{
		{name: "16 input card", mode: style.Color16, color: boxBg, bg: true, want: "\x1b[100m"},
		{name: "16 notice card", mode: style.Color16, color: theme.SurfaceRaised, bg: true, want: "\x1b[100m"},
		{name: "16 canvas", mode: style.Color16, color: theme.Bg, bg: true, want: "\x1b[40m"},
		{name: "16 selected bar", mode: style.Color16, color: productFg, bg: true, want: "\x1b[47m"},
		{name: "16 selected bar text", mode: style.Color16, color: boxBg, want: "\x1b[30m"},
		{name: "16 text", mode: style.Color16, color: theme.TextBase, want: "\x1b[97m"},
		{name: "16 dim text", mode: style.Color16, color: PanelDimFg, want: "\x1b[37m"},
		{name: "16 accent", mode: style.Color16, color: theme.Accent, want: "\x1b[33m"},
		{name: "16 success", mode: style.Color16, color: theme.Success, want: "\x1b[92m"},
		{name: "16 reasoning code", mode: style.Color16, color: ReasoningCodeFg(theme), want: "\x1b[32m"},
		{name: "256 reasoning code", mode: style.Color256, color: ReasoningCodeFg(theme), want: "\x1b[38;5;101m"},
		{name: "16 unlisted", mode: style.Color16, color: unlisted, want: style.FgColorSeq(unlisted, style.Color16)},
		{name: "16 text color as background", mode: style.Color16, color: theme.Success, bg: true,
			want: style.BgColorSeq(theme.Success, style.Color16)},
		{name: "256 text", mode: style.Color256, color: theme.TextBase, want: "\x1b[38;5;254m"},
		{name: "256 input card", mode: style.Color256, color: boxBg, bg: true, want: "\x1b[48;5;235m"},
		{name: "256 unlisted", mode: style.Color256, color: unlisted, want: style.FgColorSeq(unlisted, style.Color256)},
		{name: "256 unlisted background", mode: style.Color256, color: unlisted, bg: true,
			want: style.BgColorSeq(unlisted, style.Color256)},
		{name: "truecolor", mode: style.ColorTrueColor, color: theme.Success, want: style.FgColorSeq(theme.Success, style.ColorTrueColor)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			x := ColorIndexers(tt.mode)
			got := x.FgSeq(tt.color, tt.mode)
			if tt.bg {
				got = x.BgSeq(tt.color, tt.mode)
			}
			if got != tt.want {
				t.Errorf("sequence of %s = %q, want %q", tt.color.ToHex(), got, tt.want)
			}
		})
	}
	if x := ColorIndexers(style.ColorTrueColor); x.Fg != nil || x.Bg != nil {
		t.Errorf("24-bit color indexers = %v, want the zero Indexers", x)
	}
}
