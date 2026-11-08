package ui

import (
	"testing"

	"github.com/unaquatic/flow/agent/message"
)

// TestLineCount checks that the count leaves out the line breaks that end
// the text, names one line in the singular, and is empty for blank text.
func TestLineCount(t *testing.T) {
	for text, want := range map[string]string{
		"":                 "",
		" \n\n":            "",
		"one":              "[1 line]",
		"one\n":            "[1 line]",
		"one\n\nthree":     "[3 lines]",
		"one\ntwo\n\n\n\n": "[2 lines]",
	} {
		if got := LineCount(text); got != want {
			t.Errorf("LineCount(%q) = %q, want %q", text, got, want)
		}
	}
}

// TestSystemBox_ShowsItsLines checks that the box's header is ContextLabel
// alone, its status the number of lines of its text, and that a new form of
// the part counts its lines again.
func TestSystemBox_ShowsItsLines(t *testing.T) {
	b := NewSystemBox(message.TextPart{ID: "s1-text", Text: "You are flow.\n\nBe concise."}, nil, ToolFg)
	if got := b.Header.PlainText(); got != ContextLabel {
		t.Errorf("header = %q, want %q", got, ContextLabel)
	}
	if got := b.Status.PlainText(); got != "[3 lines]" || !b.Status.Visible() {
		t.Errorf("status = %q, shown %v; want [3 lines], shown", got, b.Status.Visible())
	}
	UpdateContentPart(b, message.TextPart{ID: "s1-text", Text: "You are flow."})
	if got := b.Status.PlainText(); got != "[1 line]" {
		t.Errorf("status after update = %q, want [1 line]", got)
	}
}
