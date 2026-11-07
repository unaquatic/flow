package ui

import (
	"fmt"
	"strings"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/widget"
)

// UsageBox is the PartBox of the token counts of a reply's request, which
// its usage part records: a block whose header sums them up, and whose
// details list them all. It is gone until shown: the counts already show as
// the reply's cache use and in the Usage box.
type UsageBox struct {
	*PartBox
	// Part is the usage part being rendered.
	Part message.UsagePart
	// Body lists every count the provider reported.
	Body *widget.Markdown
}

var _ BoxContent = (*UsageBox)(nil)

// NewUsageBox creates a collapsed usage block on the theme's raised surface,
// its header in the theme's muted text color, gone until SetGone(false) shows
// it.
func NewUsageBox(part message.UsagePart, theme *style.Theme, fg style.Color) *UsageBox {
	theme = themeOrDefault(theme)
	surface := theme.ResolveSurfaceRaised()
	b := &UsageBox{
		PartBox: NewPartBox(theme, theme.TextMuted, surface),
		Body:    NewToolBody(theme, fg, surface),
	}
	b.Content = b
	b.Shape = ShapeBlock
	b.Marked = false
	b.Details.Add(b.Body)
	b.SetGone(true)
	b.SetPart(part)
	b.ShowShape()
	return b
}

// SetPart updates the header and details, unless part equals the part
// already shown. It leaves the box gone or shown as it was.
func (b *UsageBox) SetPart(part message.UsagePart) {
	if b.Header.PlainText() != "" && part == b.Part {
		return
	}
	b.Part = part
	b.ShowHeader()
	b.Body.SetContent(ToolFence("text", UsageDetails(part.Usage)))
}

// ShowHeader writes the counts the Usage box shows, as UsageHeader gives
// them.
func (b *UsageBox) ShowHeader() {
	b.Header.SetContent(UsageHeader(b.Part.Usage))
}

// UsageHeader summarizes u on one line, with the counts the Usage box shows,
// such as "[usage] in 0.2K out 0.3K cache 1.0K write 0.2K": the prompt
// tokens processed anew, the output tokens, and the prompt tokens read from
// and written to a prompt cache.
func UsageHeader(u message.Usage) string {
	r := UsageOf(u)
	return fmt.Sprintf("%s in %s out %s cache %s write %s", Tag("usage"),
		FormatTokens(r.Input), FormatTokens(r.Output), FormatTokens(r.CacheRead), FormatTokens(r.CacheWrite))
}

// UsageDetails lists every count of u, one to a line, as the provider
// reported it, labels left and counts right-aligned.
func UsageDetails(u message.Usage) string {
	rows := []struct {
		label string
		n     int
	}{
		{"input", u.InputTokens},
		{"cached input", u.CachedInputTokens},
		{"cache creation", u.CacheCreationInputTokens},
		{"output", u.OutputTokens},
		{"reasoning output", u.ReasoningOutputTokens},
		{"total", u.TotalTokens},
	}
	labels, counts := 0, 0
	for _, r := range rows {
		labels, counts = max(labels, len(r.label)), max(counts, len(fmt.Sprint(r.n)))
	}
	width := labels + 1 + counts
	var sb strings.Builder
	for i, r := range rows {
		if i > 0 {
			sb.WriteByte('\n')
		}
		n := fmt.Sprint(r.n)
		sb.WriteString(r.label + strings.Repeat(" ", width-len(r.label)-len(n)) + n)
	}
	return sb.String()
}
