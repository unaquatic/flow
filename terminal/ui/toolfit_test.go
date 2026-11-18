package ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/testutil"
)

// fitCall adds a reply of calls, expands them inline as Ctrl+O does, fits
// them to a terminal height rows tall, and returns their boxes and the limit
// they were fitted to.
func fitCall(t *testing.T, height int, calls ...message.ContentPart) (*UI, []*ToolBox, int) {
	t.Helper()
	ui := newInputTestUI(t)
	ui.inline = true
	showSteps(ui)
	ui.AddMessage(agentParts("a1", calls...))
	expandTools(ui)
	ui.renderer.Root().CalculateLayout()
	ui.FitToolBoxes(height)
	ui.renderer.Root().CalculateLayout()
	return ui, partBoxesOf(ui, "a1"), ui.ToolRowLimit(height)
}

// completed returns a completed call of tool with input and output.
func completed(id, tool, input, output string) message.ToolPart {
	return message.ToolPart{ID: message.PartID(id), CallID: id, Name: tool, State: message.ToolStateCompleted{
		Input: json.RawMessage(input), Output: output}}
}

// numbered returns n lines made by line.
func numberedLines(n int, line func(int) string) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = line(i)
	}
	return strings.Join(lines, "\n")
}

// TestFitToolBoxes checks that, inline, an expanded call taller than the
// screen ends at the limit, or within a row of it, with TruncatedNote,
// whatever its tallest block holds: short lines, lines that wrap, one line
// that wraps past the screen, or an edit's diff, and that a call that fits
// is left whole.
func TestFitToolBoxes(t *testing.T) {
	const height = 30
	bash := `{"command":"go test ./..."}`
	long := strings.Repeat("word ", 30)
	tests := []struct {
		name string
		call message.ToolPart
	}{
		{"short lines", completed("c1", "bash", bash, numberedLines(200, func(i int) string { return fmt.Sprintf("ok  \tpkg/%d", i) }))},
		{"wrapping lines", completed("c1", "bash", bash, numberedLines(60, func(i int) string { return fmt.Sprint(i, " ", long) }))},
		{"one huge line", completed("c1", "bash", bash, strings.Repeat("x", 8000))},
		{"edit diff", completed("c1", "edit", fmt.Sprintf(`{"path":"a.go","old_string":%q,"new_string":%q}`,
			numberedLines(80, func(i int) string { return fmt.Sprint("old ", i) }),
			numberedLines(80, func(i int) string { return fmt.Sprint("new ", i) })), "ok")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, boxes, limit := fitCall(t, height, tt.call)
			b := boxes[0]
			got := b.LayoutNode().Computed.Height
			if got > limit || got < limit-1 || !b.Truncated.Visible() {
				t.Errorf("call takes %d rows, truncated %v; want %d or one fewer, truncated", got, b.Truncated.Visible(), limit)
			}
		})
	}
	t.Run("fits", func(t *testing.T) {
		_, boxes, _ := fitCall(t, height, completed("c1", "bash", bash, "ok"))
		if b := boxes[0]; b.KeepRows != 0 || b.Truncated.Visible() {
			t.Errorf("short call keeps %d rows, truncated %v; want it whole", b.KeepRows, b.Truncated.Visible())
		}
	})
}

// TestFitToolBoxesTaller checks that a call cut short shows whole again once
// a taller terminal leaves it room.
func TestFitToolBoxesTaller(t *testing.T) {
	ui, boxes, _ := fitCall(t, 30, completed("c1", "bash", `{"command":"ls"}`, numberedLines(100, func(i int) string { return fmt.Sprint(i) })))
	ui.FitToolBoxes(400)
	ui.renderer.Root().CalculateLayout()
	if b := boxes[0]; b.KeepRows != 0 || b.Truncated.Visible() {
		t.Errorf("taller terminal: keeps %d rows, truncated %v; want it whole", b.KeepRows, b.Truncated.Visible())
	}
}

// TestRestInputRows checks that the input box counts at its fewest rows on
// a terminal of one height, so that a menu open for a while does not shrink
// the limit, and that a new height starts over.
func TestRestInputRows(t *testing.T) {
	ui := newInputTestUI(t)
	ui.renderer.Root().CalculateLayout()
	rest := ui.RestInputRows(30)
	ui.restInputRows = rest + 10
	ui.restHeight = 30
	if got := ui.RestInputRows(30); got != rest {
		t.Errorf("rows = %d, want the fewest, %d", got, rest)
	}
	ui.restInputRows = rest - 1
	if got := ui.RestInputRows(30); got != rest-1 {
		t.Errorf("rows = %d, want the fewest seen, %d", got, rest-1)
	}
	if got := ui.RestInputRows(40); got != rest {
		t.Errorf("new height: rows = %d, want %d", got, rest)
	}
}

// TestKeepRows checks that text is cut to its first rows, a line that wraps
// counting a row for each width columns and cut within, and reports a cut
// only when it leaves text out.
func TestKeepRows(t *testing.T) {
	for _, tt := range []struct {
		text string
		n, w int
		want string
		cut  bool
	}{
		{"a\nb\nc", 2, 10, "a\nb", true},
		{"a\nb\nc", 3, 10, "a\nb\nc", false},
		{"a\nb\n", 2, 10, "a\nb", false},
		{"abcdefghij\nk", 2, 4, "abcdefgh", true},
		{"abcd\nefgh", 2, 4, "abcd\nefgh", false},
	} {
		if got, cut := KeepRows(tt.text, tt.n, tt.w); got != tt.want || cut != tt.cut {
			t.Errorf("KeepRows(%q, %d, %d) = %q, %v; want %q, %v", tt.text, tt.n, tt.w, got, cut, tt.want, tt.cut)
		}
	}
}

// partBoxesOf returns the tool boxes of message id of ui, in order.
func partBoxesOf(ui *UI, id message.ID) []*ToolBox {
	var out []*ToolBox
	for b := range ui.messageWidgets[id].ToolBoxes() {
		out = append(out, b)
	}
	return out
}

// TestToolFitFrames draws an expanded call cut short inline on a short
// terminal: its header on top, its output's first lines, and TruncatedNote.
func TestToolFitFrames(t *testing.T) {
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = fmt.Sprintf("ok  \tpkg/%d", i)
	}
	ui := newInputTestUI(t)
	ui.inline = true
	showSteps(ui)
	ui.AddMessage(agentParts("a1", message.ToolPart{ID: "c1", CallID: "c1", Name: "bash", State: message.ToolStateCompleted{
		Input: json.RawMessage(`{"command":"go test ./...","description":"Run all tests"}`), Output: strings.Join(lines, "\n")}}))
	expandTools(ui)
	ui.renderer.Root().CalculateLayout()
	ui.FitToolBoxes(16)
	buf := renderFrames(60, 16, detachTranscript(ui)...)
	testutil.AssertGoldenFrames(t, "toolbox_inline_truncated", buf, surfaceNames(ui))
}
