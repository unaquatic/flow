package builtin

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// MaxLines is the most lines a tool returns by default.
	MaxLines = 2000
	// MaxBytes is the most bytes a tool returns by default.
	MaxBytes = 64 * 1024
	// MaxGrepLine is the most characters of one line a search match shows.
	MaxGrepLine = 500
)

// Truncation describes the part of a text that TruncateHead or TruncateTail
// kept. Lines are counted without their newlines, and a final newline does not
// start another line.
type Truncation struct {
	// Content is the kept text, without a final newline.
	Content string
	// Truncated reports whether anything was cut.
	Truncated bool
	// ByBytes reports that the byte limit, rather than the line limit, cut
	// the text.
	ByBytes bool
	// TotalLines is the number of lines in the original text.
	TotalLines int
	// TotalBytes is the size of the original text.
	TotalBytes int
	// OutputLines is the number of lines in Content.
	OutputLines int
	// OutputBytes is the size of Content.
	OutputBytes int
	// FirstLineTooLong reports, for TruncateHead, that the first line alone
	// exceeds the byte limit, so Content is empty.
	FirstLineTooLong bool
	// LastLinePartial reports, for TruncateTail, that the last line alone
	// exceeds the byte limit, so Content holds only its end.
	LastLinePartial bool
}

// TruncateHead keeps the start of s: at most maxLines whole lines and
// maxBytes bytes, whichever limit comes first. It never keeps part of a line.
func TruncateHead(s string, maxLines, maxBytes int) Truncation {
	lines := splitLines(s)
	t := Truncation{TotalLines: len(lines), TotalBytes: len(s)}
	if len(lines) <= maxLines && len(s) <= maxBytes {
		t.Content, t.OutputLines, t.OutputBytes = s, len(lines), len(s)
		return t
	}
	t.Truncated = true
	if len(lines[0]) > maxBytes {
		t.ByBytes, t.FirstLineTooLong = true, true
		return t
	}
	size := 0
	kept := 0
	for i, line := range lines[:min(len(lines), maxLines)] {
		n := len(line)
		if i > 0 {
			n++
		}
		if size+n > maxBytes {
			t.ByBytes = true
			break
		}
		size += n
		kept++
	}
	t.Content = strings.Join(lines[:kept], "\n")
	t.OutputLines, t.OutputBytes = kept, len(t.Content)
	return t
}

// TruncateTail keeps the end of s: at most maxLines whole lines and maxBytes
// bytes, whichever limit comes first. When the last line alone exceeds
// maxBytes, it keeps the end of that line.
func TruncateTail(s string, maxLines, maxBytes int) Truncation {
	lines := splitLines(s)
	t := Truncation{TotalLines: len(lines), TotalBytes: len(s)}
	if len(lines) <= maxLines && len(s) <= maxBytes {
		t.Content, t.OutputLines, t.OutputBytes = s, len(lines), len(s)
		return t
	}
	t.Truncated = true
	size := 0
	first := len(lines)
	for i := len(lines) - 1; i >= 0 && len(lines)-i <= maxLines; i-- {
		n := len(lines[i])
		if i < len(lines)-1 {
			n++
		}
		if size+n > maxBytes {
			t.ByBytes = true
			if first == len(lines) {
				t.Content = bytesFromEnd(lines[i], maxBytes)
				t.OutputLines, t.OutputBytes = 1, len(t.Content)
				t.LastLinePartial = true
				return t
			}
			break
		}
		size += n
		first = i
	}
	t.Content = strings.Join(lines[first:], "\n")
	t.OutputLines, t.OutputBytes = len(lines)-first, len(t.Content)
	return t
}

// TruncateLine shortens line to maxChars characters, marking the cut with
// "... [truncated]", and reports whether it did.
func TruncateLine(line string, maxChars int) (string, bool) {
	if utf8.RuneCountInString(line) <= maxChars {
		return line, false
	}
	i := 0
	for range maxChars {
		_, size := utf8.DecodeRuneInString(line[i:])
		i += size
	}
	return line[:i] + "... [truncated]", true
}

// FormatSize writes a byte count for a notice, such as 512B, 64.0KB or 1.2MB.
func FormatSize(bytes int) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%dB", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
	}
}

// splitLines splits s into lines without their newlines. A final newline
// does not start another line, and an empty s has no lines.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// bytesFromEnd returns the last maxBytes bytes of s, or fewer so that the
// result starts at the beginning of a UTF-8 character.
func bytesFromEnd(s string, maxBytes int) string {
	start := len(s) - maxBytes
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[start:]
}
