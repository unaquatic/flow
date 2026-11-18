package ui

import (
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/widget"
)

// ThinkingLabel heads reasoning while it streams, and ThoughtLabel once it
// has ended.
const (
	ThinkingLabel = "Thinking"
	ThoughtLabel  = "Thought"
)

// ReasoningBox is the PartBox of model reasoning: a box like a tool call's,
// collapsed and expanded as Ctrl+O and /config show tool calls, with
// ToolMarker as they have it. Its header is ThinkingLabel while it streams
// and ThoughtLabel once it ends, with how long
// the reasoning took after it as its Status, such as "[12s]", and its
// details show the reasoning text as it streams.
type ReasoningBox struct {
	*PartBox
	// Part is the reasoning content being rendered.
	Part message.ReasoningPart
	// Body renders the reasoning text as it streams.
	Body *widget.StreamMarkdown
}

var _ BoxContent = (*ReasoningBox)(nil)

// ReasoningCodeFg returns the color of inline code in reasoning: the hue of
// the theme's inline code, as bright as the theme's TextMuted, which
// reasoning is drawn in, and with chroma scaled by the ratio of that text's
// lightness to regular text's, so that the code dims as the prose around it
// does. Regular text keeps the theme's own color.
func ReasoningCodeFg(theme *style.Theme) style.Color {
	code := theme.ResolveMarkdownCode().Fg.ToOKLCH()
	text, muted := theme.ResolveTextBase().ToOKLCH().L, theme.TextMuted.ToOKLCH().L
	dim := 1.0
	if brighter := max(text, muted); brighter > 0 {
		dim = min(text, muted) / brighter
	}
	code.L, code.C = muted, code.C*dim
	return code.ToColor()
}

// NewReasoningBox creates a collapsed reasoning box, its header in fg and
// its block on the theme's raised surface. Its text is in the theme's
// TextMuted, as tool rows are, and its inline code in the color
// ReasoningCodeFg gives, on the theme's code background; fenced code keeps
// the theme's colors.
func NewReasoningBox(part message.ReasoningPart, theme *style.Theme, fg style.Color) *ReasoningBox {
	theme = themeOrDefault(theme)
	b := &ReasoningBox{
		PartBox: NewPartBox(theme, fg, theme.ResolveSurfaceRaised()),
		Body: widget.NewStreamMarkdown(
			widget.StreamMarkdownWithTheme(theme),
			widget.StreamMarkdownWithCode(theme.ResolveMarkdownCode().WithFg(ReasoningCodeFg(theme))),
			widget.StreamMarkdownWithFg(theme.TextMuted),
			widget.StreamMarkdownWithBg(style.ColorTransparent),
			widget.StreamMarkdownWithLayout(layout.WithWidth(layout.Percent(100))),
		),
	}
	b.Content = b
	b.Details.Add(b.Body)
	b.SetGone(true)
	b.ShowHeader()
	b.SetPart(part)
	b.ShowShape()
	return b
}

// SetPart replaces the reasoning part, shows how long it took in Status as
// ReasoningTime gives it, and replaces the text unless the part already
// shown has the same, as a change of its metadata alone leaves it.
// Reasoning without text, such as a signed block that streamed none, takes
// no rows.
func (b *ReasoningBox) SetPart(part message.ReasoningPart) {
	same := part.Text == b.Part.Text
	b.Part = part
	b.SetStatus(ReasoningTime(part))
	if same {
		return
	}
	b.Body.SetContent(part.Text)
	b.SetGone(part.Text == "")
}

// ShowHeader writes ThinkingLabel while the reasoning streams, as Busy says,
// and ThoughtLabel once it ends, in NameFg, as a tool call's header starts
// with its name, and hides the header row while the box is open, so that
// expanded reasoning shows its text alone.
func (b *ReasoningBox) ShowHeader() {
	b.HeaderRow.SetGone(b.Open())
	label := ThoughtLabel
	if b.Busy {
		label = ThinkingLabel
	}
	b.Header.SetStyledContent(style.NewStyledText(ToolNameChunk(label, b.NameFg)))
}

// SetBusy records whether the reasoning streams, which its marker shows by
// blinking, as PartBox.SetBusy does, and its header by its label.
func (b *ReasoningBox) SetBusy(busy bool) {
	if busy == b.Busy {
		return
	}
	b.PartBox.SetBusy(busy)
	b.ShowHeader()
}

// Span returns when the reasoning started and last streamed, either zero
// when not known.
func (b *ReasoningBox) Span() (start, end time.Time) { return b.Part.StartedAt, b.Part.EndedAt }

// ReasoningTime returns how long part took to stream, as FormatDuration
// gives it in brackets, such as "[12s]", or "" when that is not known, as
// for reasoning stored before flow recorded its times.
func ReasoningTime(part message.ReasoningPart) string {
	if part.StartedAt.IsZero() || part.EndedAt.IsZero() {
		return ""
	}
	return "[" + FormatDuration(part.Duration()) + "]"
}
