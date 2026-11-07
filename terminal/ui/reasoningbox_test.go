package ui

import (
	"math"
	"testing"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/style"
)

// TestReasoningFg checks that reasoning is drawn in ToolFg, as tool rows
// are.
func TestReasoningFg(t *testing.T) {
	box := NewReasoningBox(message.ReasoningPart{Text: "thinking"}, DefaultTheme())
	if got := box.Body.Fg(); !got.Equals(ToolFg) {
		t.Errorf("reasoning fg = %v, want ToolFg %v", got, ToolFg)
	}
}

// TestReasoningCodeFg checks that inline code in reasoning keeps the hue of
// the theme's inline code, is as bright as the text of reasoning, and is
// less saturated than the code in a reply, as the muted text is less bright
// than the reply's.
func TestReasoningCodeFg(t *testing.T) {
	theme := DefaultTheme()
	code, got := theme.ResolveMarkdownCode().Fg.ToOKLCH(), ReasoningCodeFg(theme).ToOKLCH()
	if want := ToolFg.ToOKLCH().L; math.Abs(got.L-want) > 0.01 {
		t.Errorf("lightness = %.3f, want that of the reasoning text, %.3f", got.L, want)
	}
	if math.Abs(got.H-code.H) > 1 {
		t.Errorf("hue = %.1f, want that of the inline code, %.1f", got.H, code.H)
	}
	if got.C <= 0 || got.C >= code.C {
		t.Errorf("chroma = %.3f, want some, but less than the inline code's %.3f", got.C, code.C)
	}
}

// TestReasoningCodeFgThemes checks that the color is as bright as ToolFg,
// which reasoning is drawn in, whatever a theme's text, dark or light, and
// never more saturated than the theme's inline code, including when a text
// color is black or the two are the same.
func TestReasoningCodeFgThemes(t *testing.T) {
	gray := func(v uint8) style.Color { return style.ColorFromInts(v, v, v, 255) }
	for name, tt := range map[string]struct{ base, muted style.Color }{
		"dark":        {base: gray(230), muted: gray(138)},
		"light":       {base: gray(40), muted: gray(120)},
		"black base":  {base: gray(0), muted: gray(120)},
		"black muted": {base: gray(230), muted: gray(0)},
		"equal":       {base: gray(138), muted: gray(138)},
	} {
		t.Run(name, func(t *testing.T) {
			theme := DefaultTheme()
			theme.TextBase, theme.TextMuted = tt.base, tt.muted
			code, got := theme.ResolveMarkdownCode().Fg.ToOKLCH(), ReasoningCodeFg(theme).ToOKLCH()
			if want := ToolFg.ToOKLCH().L; math.Abs(got.L-want) > 0.02 {
				t.Errorf("lightness = %.3f, want %.3f", got.L, want)
			}
			// The color is rounded to 8 bits a channel, which can move its
			// chroma a little either way.
			if got.C > code.C+0.005 {
				t.Errorf("chroma = %.3f, want at most the inline code's %.3f", got.C, code.C)
			}
		})
	}
}
