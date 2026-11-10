package ui

import (
	"fmt"
	"math"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/style"
)

// FormatContentPart formats one content part as Markdown. Tool parts use their
// plain-text header and output representation, as any tool's call shows.
func FormatContentPart(part message.ContentPart) string {
	switch p := part.(type) {
	case message.TextPart:
		if p.Text == "" {
			return Tag("text") + " (empty)"
		}
		return p.Text
	case message.ReasoningPart:
		return Tag("reasoning") + "\n> " + p.Text
	case message.ToolPart:
		header, status, output := ToolHeaderOutput(p, ToolView{}, style.Color{}, style.Color{})
		text := header.PlainText()
		if status != "" {
			text += " " + status
		}
		if output != "" {
			text += "\n" + output
		}
		return text
	case message.ToolUsePart:
		return Tag("tool_use") + " `" + p.Name + "` " + Truncate(string(p.Input), 80)
	case message.ToolResultPart:
		header := Tag("tool_result")
		if p.IsError {
			header = Tag("tool_result error")
		}
		body := Truncate(p.Text, 200)
		if p.Name != "" {
			return header + " `" + p.Name + "` " + body
		}
		return header + " " + body
	case message.ImagePart:
		mt := "image"
		if p.Source != nil && p.Source.MediaType != "" {
			mt = p.Source.MediaType
		}
		return Tag("image") + " `" + mt + "`"
	case message.FilePart:
		name := p.Filename
		if name == "" {
			name = p.MediaType
		}
		return Tag("file") + " `" + name + "`"
	case message.UsagePart:
		return UsageHeader(p.Usage)
	default:
		return Tag(string(part.PartType()))
	}
}

// Tag formats name as a bracketed content label.
func Tag(name string) string {
	return "[" + name + "]"
}

// FormatTokens formats a token count in thousands, or in millions from a
// million, with a K or M suffix: to one decimal place below ten, and whole
// from ten. A count below a hundred shows as 0.1K, so that no count but zero
// reads as none.
func FormatTokens(n int) string {
	switch {
	case n <= 0:
		return "0K"
	case n < 100:
		return "0.1K"
	case n < 9_950:
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	case n < 999_500:
		return fmt.Sprintf("%.0fK", math.Round(float64(n)/1e3))
	case n < 9_950_000:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	default:
		return fmt.Sprintf("%.0fM", math.Round(float64(n)/1e6))
	}
}

// FormatCost formats a cost in US dollars to the cent, such as "$0.42".
func FormatCost(usd float64) string {
	return fmt.Sprintf("$%.2f", usd)
}

// Truncate returns s unchanged when it fits within n bytes. Otherwise, it
// returns a valid UTF-8 prefix of at most n bytes followed by an ellipsis.
func Truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Move the cut before any partial UTF-8 encoding.
	cut := n
	for cut > 0 && s[cut]&0xC0 == 0x80 {
		cut--
	}
	return s[:cut] + "…"
}

// JoinStyledLines joins styled text with newline chunks.
func JoinStyledLines(lines []style.StyledText) style.StyledText {
	var chunks []style.TextChunk
	for i, line := range lines {
		if i > 0 {
			chunks = append(chunks, style.Plain("\n"))
		}
		chunks = append(chunks, line.Chunks...)
	}
	return style.NewStyledText(chunks...)
}
