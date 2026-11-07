package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/textbuf"
)

// Report columns use fixed widths so every sample aligns.
const (
	graphemeLabelWidth  = 18
	graphemeSampleWidth = 8
)

// GraphemeSample describes one row in the width diagnostic. An empty Text
// marks a section heading.
type GraphemeSample struct {
	// Label names the sample or section.
	Label string
	// Text contains the grapheme sequence to measure.
	Text string
	// Note explains the expected width behavior.
	Note string
}

// GraphemeSamples contains representative sequences with nontrivial widths.
var GraphemeSamples = []GraphemeSample{
	{Label: "baseline"},
	{Label: "latin", Text: "A"},
	{Label: "cjk", Text: "\u4e16", Note: "east asian wide"},

	{Label: "east asian ambiguous"},
	{Label: "box drawing", Text: "\u2500", Note: "1 or 2 — terminal decides"},
	{Label: "em dash", Text: "\u2014", Note: "1 or 2 — terminal decides"},
	{Label: "arrow", Text: "\u2192", Note: "1 or 2 — terminal decides"},
	{Label: "bullet", Text: "\u25cf", Note: "1 or 2 — terminal decides"},

	{Label: "combining marks"},
	{Label: "latin accent", Text: "e\u0301", Note: "nonspacing"},
	{Label: "thai tone", Text: "\u0E17\u0E35\u0E48", Note: "nonspacing"},
	{Label: "thai sara am", Text: "\u0E01\u0E33", Note: "spacing mark, keeps its column"},
	{Label: "devanagari matra", Text: "\u0915\u093E", Note: "spacing mark, keeps its column"},

	{Label: "zero width"},
	{Label: "zero width space", Text: "A\u200bA"},
	{Label: "byte order mark", Text: "A\ufeffA"},
	{Label: "hangul jamo", Text: "\u1100\u1161\u11a8", Note: "L+V+T compose to one syllable"},

	{Label: "emoji"},
	{Label: "plain", Text: "\U0001F600"},
	{Label: "text presentation", Text: "\u2764", Note: "no variation selector"},
	{Label: "emoji + VS16", Text: "\u2764\uFE0F", Note: "promoted to 2 columns"},
	{Label: "emoji + VS15", Text: "\u263A\uFE0E", Note: "forced back to 1 column"},
	{Label: "skin tone", Text: "\U0001F44D\U0001F3FD", Note: "modifier merges into base"},
	{Label: "zwj sequence", Text: "\U0001F468\u200D\U0001F469\u200D\U0001F467", Note: "3 emoji, 1 glyph"},
	{Label: "flag", Text: "\U0001F1FA\U0001F1F8", Note: "regional indicator pair"},
	{Label: "keycap", Text: "1\uFE0F\u20E3"},
}

// GraphemeProbeSamples returns the text emitted for each GraphemeSamples entry.
// Empty section entries preserve index alignment. Renderer output is used
// because cell drawing can omit zero-width code points from the emitted text.
func GraphemeProbeSamples() []string {
	out := make([]string, len(GraphemeSamples))
	for i, s := range GraphemeSamples {
		out[i] = RenderedText(s.Text)
	}
	return out
}

// RenderedText returns the text emitted after drawing text into buffer cells.
func RenderedText(text string) string {
	if text == "" {
		return ""
	}
	b := buffer.New(graphemeSampleWidth, 1)
	b.Clear(style.ColorTransparent)
	used := b.DrawClusters(text, 0, 0, graphemeSampleWidth,
		style.ColorWhite, style.ColorTransparent, 0, nil)

	var sb strings.Builder
	for x := range used {
		c := b.Cell(x, 0)
		if c.IsContinuation() || c.Char == 0 {
			continue
		}
		sb.WriteRune(c.Char)
		sb.WriteString(c.Combining())
	}
	return sb.String()
}

// GraphemeReport compares fluid widths with measured terminal widths. The
// renderer re-anchors uncertain cells, so mismatches can clip but cannot shift
// subsequent report columns.
func GraphemeReport(probed []int, probeErr error) string {
	var b strings.Builder

	b.WriteString(GraphemeSummary(probed, probeErr))
	b.WriteString("\n\n")
	fmt.Fprintf(&b, "  %-*s%-*s%7s%10s\n",
		graphemeLabelWidth, "sample", graphemeSampleWidth, "", "fluid", "terminal")

	for i, s := range GraphemeSamples {
		if s.Text == "" {
			fmt.Fprintf(&b, "\n  %s\n", s.Label)
			continue
		}
		b.WriteString(GraphemeReportRow(i, s, probed))
	}
	return b.String()
}

// GraphemeReportRow formats one sample row. A dash denotes no measurement.
func GraphemeReportRow(i int, s GraphemeSample, probed []int) string {
	fluid := textbuf.StringWidth(s.Text)
	pad := max(graphemeSampleWidth-fluid, 0)

	measured, note := "-", s.Note
	if probed != nil {
		measured = strconv.Itoa(probed[i])
		if probed[i] != fluid {
			note = "MISMATCH — glyph clips, row stays intact"
		}
	}
	if note != "" {
		note = "   " + note
	}

	return fmt.Sprintf("  %-*s%s%s%7d%10s%s\n",
		graphemeLabelWidth, s.Label,
		s.Text, strings.Repeat(" ", pad),
		fluid, measured, note)
}

// GraphemeSummary summarizes probe availability and width mismatches. A nil
// result without an error means the probe was not requested.
func GraphemeSummary(probed []int, probeErr error) string {
	if probed == nil {
		if probeErr != nil {
			return fmt.Sprintf("grapheme width — not measured (%v)", probeErr)
		}
		return fmt.Sprintf("grapheme width — fluid's widths only\nrun with %s=1 to measure what this terminal actually does",
			config.EnvWidthProbe)
	}

	rows, disagree := 0, 0
	for i, s := range GraphemeSamples {
		if s.Text == "" {
			continue
		}
		rows++
		if probed[i] != textbuf.StringWidth(s.Text) {
			disagree++
		}
	}
	if disagree == 0 {
		return fmt.Sprintf("grapheme width — this terminal agrees with fluid on all %d samples", rows)
	}
	return fmt.Sprintf("grapheme width — %d of %d samples disagree; the flush re-anchors after each,\n"+
		"so they clip the glyph rather than corrupt the row", disagree, rows)
}
