package ui

import (
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/widget"
)

// ReasoningBox displays model reasoning as plain streamed text, without a
// header.
type ReasoningBox struct {
	*widget.Box
	// Part is the reasoning content being rendered.
	Part message.ReasoningPart
	// Body renders the reasoning text as it streams.
	Body *widget.StreamMarkdown
}

// ReasoningCodeFg returns the color of inline code in reasoning: the hue of
// the theme's inline code, as bright as ToolFg, which reasoning is drawn
// in, and with chroma scaled by the ratio of that text's lightness to
// regular text's, so that the code dims as the prose around it does.
// Regular text keeps the theme's own color.
func ReasoningCodeFg(theme *style.Theme) style.Color {
	code := theme.ResolveMarkdownCode().Fg.ToOKLCH()
	text, muted := theme.ResolveTextBase().ToOKLCH().L, ToolFg.ToOKLCH().L
	dim := 1.0
	if brighter := max(text, muted); brighter > 0 {
		dim = min(text, muted) / brighter
	}
	code.L, code.C = muted, code.C*dim
	return code.ToColor()
}

// NewReasoningBox creates a reasoning block, padded by PaddingX at the sides,
// that is transparent and sits flush against the surrounding rows, without
// vertical padding. Its text is in ToolFg, as tool rows are, and its
// inline code in the color ReasoningCodeFg gives, on the theme's code
// background; fenced code keeps the theme's colors.
func NewReasoningBox(part message.ReasoningPart, theme *style.Theme) *ReasoningBox {
	theme = themeOrDefault(theme)
	b := &ReasoningBox{
		Box: widget.NewBox(
			widget.BoxWithLayout(
				layout.WithFlexDirection(layout.DirectionColumn),
				layout.WithAlignItems(layout.AlignStretch),
				layout.WithPaddingLeft(layout.Points(PaddingX)),
				layout.WithPaddingRight(layout.Points(PaddingX)),
			),
			widget.BoxWithBg(style.ColorTransparent),
		),
		Body: widget.NewStreamMarkdown(
			widget.StreamMarkdownWithTheme(theme),
			widget.StreamMarkdownWithCode(theme.ResolveMarkdownCode().WithFg(ReasoningCodeFg(theme))),
			widget.StreamMarkdownWithFg(ToolFg),
			widget.StreamMarkdownWithBg(style.ColorTransparent),
			widget.StreamMarkdownWithPending(WritingRow()),
			widget.StreamMarkdownWithLayout(layout.WithWidth(layout.Percent(100))),
		),
	}
	b.Add(b.Body)
	b.SetGone(true)
	b.SetPart(part)
	return b
}

// SetPart replaces the reasoning content, unless part equals the part
// already shown. Reasoning without text, such as a signed block that
// streamed none, takes no rows.
func (b *ReasoningBox) SetPart(part message.ReasoningPart) {
	if part == b.Part {
		return
	}
	b.Part = part
	b.Body.SetContent(part.Text)
	b.SetGone(part.Text == "")
}
