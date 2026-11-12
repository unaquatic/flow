package ui

import (
	"strconv"
	"strings"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/widget"
)

// ContextLabel names the system message a session opens with, at the start
// of its box's header.
const ContextLabel = "Context"

// SystemBox is the PartBox of a text part of a system message, the
// instructions a session opens with and every request of it sends first: a
// box like a tool call's, shown as /config and Ctrl+O show tool calls,
// with ToolMarker as they have it. Its header is ContextLabel, with the
// number of lines of the text after it as its Status, such as "[12 lines]",
// and its details show the whole text, as the model is sent it.
type SystemBox struct {
	*PartBox
	// Part is the text part being rendered.
	Part message.TextPart
	// Body shows the text as it is, in a block of its own lines.
	Body *widget.Markdown
}

var _ BoxContent = (*SystemBox)(nil)

// NewSystemBox creates a collapsed system box, its header and text in fg and
// its block on the theme's raised surface.
func NewSystemBox(part message.TextPart, theme *style.Theme, fg style.Color) *SystemBox {
	theme = themeOrDefault(theme)
	b := &SystemBox{
		PartBox: NewPartBox(theme, fg, theme.ResolveSurfaceRaised()),
		Body:    NewToolBody(theme, fg, style.ColorTransparent),
	}
	b.Content = b
	b.Details.Add(b.Body)
	b.SetPart(part)
	b.ShowShape()
	return b
}

// SetPart shows part, and its length in Status as LineCount gives it, unless
// it equals the part already shown.
func (b *SystemBox) SetPart(part message.TextPart) {
	if b.Header.PlainText() != "" && part == b.Part {
		return
	}
	b.Part = part
	b.ShowHeader()
	b.SetStatus(LineCount(part.Text))
	b.Body.SetContent(ToolFence("text", part.Text))
}

// ShowHeader writes ContextLabel in NameFg, as a tool call's header starts
// with its name.
func (b *SystemBox) ShowHeader() {
	b.Header.SetStyledContent(style.NewStyledText(ToolNameChunk(ContextLabel, b.NameFg)))
}

// LineCount returns the number of lines of text, as "[12 lines]" or
// "[1 line]", leaving out the line breaks that end it. Blank text has none,
// and reads "".
func LineCount(text string) string {
	text = strings.TrimRight(text, "\n")
	if strings.TrimSpace(text) == "" {
		return ""
	}
	n := strings.Count(text, "\n") + 1
	if n == 1 {
		return "[1 line]"
	}
	return "[" + strconv.Itoa(n) + " lines]"
}

// ToolCount returns the number of tools a system message holds, as
// "[12 tools]" or "[1 tool]".
func ToolCount(n int) string {
	if n == 1 {
		return "[1 tool]"
	}
	return "[" + strconv.Itoa(n) + " tools]"
}
