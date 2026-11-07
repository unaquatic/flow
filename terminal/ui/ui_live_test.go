package ui

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/extension/demo"
	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/terminal"
	"github.com/unaquatic/fluid/testutil"
	"github.com/unaquatic/fluid/widget"
)

// inlineLayoutRows is the height a test lays the inline root out at, as the
// inline renderer lays it out taller than any transcript.
const inlineLayoutRows = 4096

// Scrollback models the terminal under inline output, as fluid's inline
// renderer drives it: the screen shows the last rows of the frame, and the
// rows that scroll off its top go to the scrollback, where the renderer can
// no longer rewrite them. A frame that changes one makes the renderer erase
// the screen and the scrollback and write every row again.
type Scrollback struct {
	// Top is the first row of the frame on screen.
	Top int
	// Last is the frame drawn last, and Rows the rows its content takes.
	Last *buffer.Buffer
	Rows int
}

// Frame draws the inline root of ui, as DrawInline does, and fails the test
// when the frame changes a row that an earlier frame scrolled off the
// screen. Step names the change drawn, for the failure.
func (s *Scrollback) Frame(t *testing.T, ui *UI, step string) {
	t.Helper()
	buf, rows := DrawInline(ui)
	if s.Last != nil {
		for y := range s.Top {
			if !RowsEqual(s.Last, buf, y) {
				t.Fatalf("%s rewrote row %d, which had scrolled into the scrollback:\nwas %q\nnow %q",
					step, y, testutil.RowText(s.Last, y), testutil.RowText(buf, y))
			}
		}
	}
	s.Top = max(s.Top, rows-ui.renderer.Height())
	s.Last, s.Rows = buf, rows
}

// Shows reports whether a row of the frame drawn last holds text.
func (s *Scrollback) Shows(text string) bool {
	for y := range s.Last.Height() {
		if strings.Contains(testutil.RowText(s.Last, y), text) {
			return true
		}
	}
	return false
}

// DrawInline lays the root of ui out as the inline renderer does, as wide as
// the terminal less a column and taller than its content, and draws it. It
// returns the frame and the rows the content takes.
func DrawInline(ui *UI) (*buffer.Buffer, int) {
	root := ui.renderer.Root()
	w := max(ui.renderer.Width()-1, 1)
	root.Resize(w, inlineLayoutRows)
	buf := buffer.New(w, inlineLayoutRows)
	root.Render(buf, 0)
	return buf, ContentRows(root)
}

// ContentRows returns the bottom edge of the children of root, their bottom
// margins included, as the inline renderer measures the content.
func ContentRows(root *terminal.RootRenderable) int {
	bottom := 0
	for _, child := range root.LayoutNode().Children {
		margin, _ := child.MarginBottom.Resolve(0)
		bottom = max(bottom, child.Computed.Y+child.Computed.Height+margin)
	}
	return bottom
}

// RowsEqual reports whether row y of a and b holds the same cells.
func RowsEqual(a, b *buffer.Buffer, y int) bool {
	for x := range a.Width() {
		if !a.Cell(x, y).Equals(b.Cell(x, y)) {
			return false
		}
	}
	return true
}

// liveReply is the agent message the stream tests stream.
const liveReply = message.ID("a1")

// partEvent returns the event that starts, updates, or ends part of
// liveReply.
func partEvent(kind string, part message.ContentPart) app.Event {
	change := app.PartChange{MessageID: liveReply, Part: part}
	switch kind {
	case "start":
		return app.EventPartStarted{PartChange: change}
	case "end":
		return app.EventPartEnded{PartChange: change}
	}
	return app.EventPartUpdated{PartChange: change}
}

// streamText returns the events that stream a text part of liveReply, a
// word of chunks an event, as a model streams text, and end it.
func streamText(id message.PartID, chunks []string) []app.Event {
	var events []app.Event
	var text strings.Builder
	for _, word := range strings.SplitAfter(strings.Join(chunks, ""), " ") {
		text.WriteString(word)
		kind := "update"
		if len(events) == 0 {
			kind = "start"
		}
		events = append(events, partEvent(kind, message.TextPart{ID: id, Text: text.String()}))
	}
	return append(events, partEvent("end", message.TextPart{ID: id, Text: text.String()}))
}

// bashCall returns the bash call id of liveReply in state, whose input runs
// command.
func bashCall(id message.PartID, command string, state func(json.RawMessage) message.ToolState) message.ToolPart {
	input := json.RawMessage(fmt.Sprintf(`{"command":%q}`, command))
	return message.ToolPart{ID: id, CallID: string(id), Name: "bash", State: state(input)}
}

// runBash returns the events that start the bash call id, run it while its
// output grows by lines, a line an event, and end it with that output.
func runBash(id message.PartID, command string, lines []string) []app.Event {
	running := func(out string) func(json.RawMessage) message.ToolState {
		return func(in json.RawMessage) message.ToolState { return message.ToolStateRunning{Input: in, Output: out} }
	}
	events := []app.Event{partEvent("start", bashCall(id, command, func(in json.RawMessage) message.ToolState {
		return message.ToolStatePending{Input: in}
	}))}
	for i := range lines {
		events = append(events, partEvent("update", bashCall(id, command, running(strings.Join(lines[:i+1], "\n")))))
	}
	return append(events, partEvent("end", bashCall(id, command, func(in json.RawMessage) message.ToolState {
		return message.ToolStateCompleted{Input: in, Output: strings.Join(lines, "\n")}
	})))
}

// numbered returns n lines made by line from their index, but for the line
// at wide, which wide makes.
func numbered(n, at int, line func(int) string, wide string) []string {
	lines := make([]string, n)
	for i := range n {
		lines[i] = line(i)
	}
	lines[at] = wide
	return lines
}

// TestStreamKeepsTheScrollback streams replies whose early rows would change
// as later content arrives, in a terminal too short to show them whole, and
// checks that no frame rewrites a row that has scrolled into the
// scrollback, which inline output cannot rewrite but by drawing the whole
// transcript again. It checks that the reply shows whole once it ends.
func TestStreamKeepsTheScrollback(t *testing.T) {
	// The table's rows and the code's lines outnumber the terminal's, and one
	// near their end widens a column or the code's background.
	tableRows := numbered(40, 35, func(i int) string { return fmt.Sprintf("| f%d.go | %d |\n", i, i) },
		"| internal/terminal/ui/a_much_longer_name.go | 123456 |\n")
	codeLines := numbered(40, 35, func(i int) string { return fmt.Sprintf("x%d := %d\n", i, i) },
		"result := computeSomethingElaborate(withArguments, andMoreOfThem)\n")
	output := numbered(40, 35, func(i int) string { return fmt.Sprintf("ok  \tpkg/%d", i) }, "ok  \tpkg/last")

	tests := []struct {
		name string
		// expanded expands tool calls, as Ctrl+O does.
		expanded bool
		events   []app.Event
		// shows is text the reply shows once it ends.
		shows []string
	}{
		{
			name:   "table",
			events: streamText("a1-text", append([]string{"Line counts:\n\n", "| file | lines |\n|---|---|\n"}, tableRows...)),
			shows:  []string{"a_much_longer_name.go", "f39.go"},
		},
		{
			name:   "code block",
			events: streamText("a1-text", append([]string{"Here it is:\n\n", "```go\n"}, codeLines...)),
			shows:  []string{"computeSomethingElaborate", "x39 := 39"},
		},
		{
			name:     "expanded tool call",
			expanded: true,
			events:   runBash("a1-bash", "go test ./...", output),
			shows:    []string{"pkg/last", "pkg/39"},
		},
		{
			// The second call ends first, while the first, above it, still
			// runs.
			name:     "expanded parallel calls",
			expanded: true,
			events: func() []app.Event {
				first, second := runBash("a1-first", "sleep 1", []string{"done"}), runBash("a1-second", "go test ./...", output)
				// The first starts, then the second runs and ends, and then
				// the first runs and ends.
				return append(append(first[:1:1], second...), first[1:]...)
			}(),
			shows: []string{"done", "pkg/last", "pkg/39"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := newBuiltTestUI(t, true)
			if tt.expanded {
				ui.Apply(ui.service.SetToolsExpanded(true))
			}
			var s Scrollback
			apply := func(ev app.Event, step string) {
				ui.Apply(ui.service.ApplyAgentEvent(ev))
				s.Frame(t, ui, step)
			}
			apply(app.EventMessageStarted{Message: app.Message{ID: liveReply, Role: message.RoleAgent}}, "the reply's start")
			for i, ev := range tt.events {
				apply(ev, fmt.Sprintf("event %d, %T", i, ev))
			}
			apply(app.EventMessageEnded{Message: app.Message{ID: liveReply, Role: message.RoleAgent,
				Parts: partsOf(ui, liveReply)}}, "the reply's end")
			for _, text := range tt.shows {
				if !s.Shows(text) {
					t.Errorf("the reply's end shows no %q", text)
				}
			}
		})
	}
}

// TestLivePartsFollowPartEvents checks that a part is live from the event
// that starts it to the one that ends it, that a usage part, final as it
// starts, never is, and that no part of a message is once the message ends.
func TestLivePartsFollowPartEvents(t *testing.T) {
	s := &Service{state: &UIState{}}
	s.ApplyAgentEvent(openReply(liveReply, 0))
	text := message.TextPart{ID: "t", Text: "Hi"}
	call := bashCall("c", "ls", func(in json.RawMessage) message.ToolState { return message.ToolStatePending{Input: in} })
	usage := message.UsagePart{ID: "u", Usage: message.Usage{OutputTokens: 1}}
	for i, step := range []struct {
		ev   app.Event
		want []message.PartID
	}{
		{partEvent("start", text), []message.PartID{"t"}},
		{partEvent("update", text), []message.PartID{"t"}},
		{partEvent("start", call), []message.PartID{"t", "c"}},
		{partEvent("end", text), []message.PartID{"c"}},
		{partEvent("end", call), nil},
		{partEvent("update", call), []message.PartID{"c"}},
		{partEvent("start", usage), []message.PartID{"c"}},
	} {
		out := s.ApplyAgentEvent(step.ev)
		if got := out[0].(EventMessageUpdated).Msg.Live; !slices.Equal(got, step.want) {
			t.Errorf("after event %d, %T, live parts %q, want %q", i, step.ev, got, step.want)
		}
	}

	out := s.ApplyAgentEvent(app.EventMessageEnded{Message: app.Message{ID: liveReply, Role: message.RoleAgent}})
	if got := out[0].(EventMessageUpdated).Msg.Live; len(got) > 0 {
		t.Errorf("the message's end shows live parts %q, want none", got)
	}
	if got := s.state.Messages[0].Live; len(got) > 0 {
		t.Errorf("the ended message keeps live parts %q, want none", got)
	}
}

// startReply opens liveReply in ui, and applies events to it.
func startReply(ui *UI, events ...app.Event) {
	ui.Apply(ui.service.ApplyAgentEvent(openReply(liveReply, 0)))
	applyEvents(ui, events...)
}

// applyEvents applies events to ui as the app's events reach it.
func applyEvents(ui *UI, events ...app.Event) {
	for _, ev := range events {
		ui.Apply(ui.service.ApplyAgentEvent(ev))
	}
}

// openBoxes reports whether each tool call of liveReply in ui shows its
// details, in order.
func openBoxes(ui *UI) []bool {
	var open []bool
	for box := range ui.messageWidgets[liveReply].ToolBoxes() {
		open = append(open, box.Open())
	}
	return open
}

// TestHeldCallsOpenOnceFinal checks that, inline, an expanded call shows its
// details only once it has ended and every call above it has too, and that
// the fullscreen transcript shows them as soon as they come.
func TestHeldCallsOpenOnceFinal(t *testing.T) {
	for _, inline := range []bool{true, false} {
		t.Run(fmt.Sprintf("inline=%v", inline), func(t *testing.T) {
			ui := newBuiltTestUI(t, inline)
			ui.Apply(ui.service.SetToolsExpanded(true))
			first, second := runBash("a1-first", "sleep 1", []string{"done"}), runBash("a1-second", "go vet ./...", []string{"ok"})

			// The first call starts, and the second one, under it, runs and
			// ends.
			startReply(ui, append(first[:1:1], second...)...)
			if got, want := openBoxes(ui), []bool{!inline, !inline}; !slices.Equal(got, want) {
				t.Errorf("while the first call runs, calls open %v, want %v", got, want)
			}

			applyEvents(ui, first[1:]...)
			if got, want := openBoxes(ui), []bool{true, true}; !slices.Equal(got, want) {
				t.Errorf("once both calls end, calls open %v, want %v", got, want)
			}
		})
	}
}

// TestSetInlineModeHoldsLiveParts checks that switching to inline mode
// holds a running call that the fullscreen transcript shows whole, and that
// switching back shows it whole again.
func TestSetInlineModeHoldsLiveParts(t *testing.T) {
	ui := newBuiltTestUI(t, false)
	ui.Apply(ui.service.SetToolsExpanded(true))
	startReply(ui, runBash("a1-bash", "go test ./...", []string{"ok"})[:2]...)

	for _, inline := range []bool{true, false} {
		ui.SetInlineMode(EventInlineModeSet{Inline: inline})
		if got, want := openBoxes(ui), []bool{!inline}; !slices.Equal(got, want) {
			t.Errorf("inline %v: running call open %v, want %v", inline, got, want)
		}
	}
}

// TestLiveTextIsLastPart checks that, inline, text that streams holds back
// its last block until a part after it starts, which ends the text.
func TestLiveTextIsLastPart(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	startReply(ui, partEvent("start", message.TextPart{ID: "a1-text", Text: "Listing the files."}))
	text := ui.messageWidgets[liveReply].SegmentFor(liveReply).Parts["a1-text"].(*widget.StreamMarkdown)
	if !text.Streaming() {
		t.Fatal("text that streams shows its last block")
	}

	applyEvents(ui, runBash("a1-bash", "ls", nil)[0])
	if text.Streaming() {
		t.Error("text holds back its last block after a part after it started")
	}
}

// TestLiveTop checks the first row of the live area of inline output: the
// row that stands for the last block of text that streams, the row of the
// first call that has not ended, and otherwise the indicator's.
func TestLiveTop(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	ui.ShowIndicator(WorkingIndicator(time.Second, false))
	text := message.TextPart{ID: "a1-text", Text: "Listing the files.\n\nThen"}
	startReply(ui, partEvent("start", text))
	DrawInline(ui)
	sm := ui.messageWidgets[liveReply].SegmentFor(liveReply).Parts["a1-text"].(*widget.StreamMarkdown)
	if got, want := ui.LiveTop(), sm.Y()+sm.Height()-1; got != want {
		t.Errorf("while text streams, live area from row %d, want the Writing… row, %d", got, want)
	}

	applyEvents(ui, runBash("a1-bash", "ls", nil)[0])
	DrawInline(ui)
	call := slices.Collect(ui.messageWidgets[liveReply].ToolBoxes())[0]
	if got, want := ui.LiveTop(), call.Y(); got != want {
		t.Errorf("while a call has not ended, live area from row %d, want the call's, %d", got, want)
	}

	ui.Apply(ui.service.ApplyAgentEvent(app.EventMessageEnded{Message: app.Message{ID: liveReply, Role: message.RoleAgent,
		Parts: partsOf(ui, liveReply)}}))
	DrawInline(ui)
	if got, want := ui.LiveTop(), ui.indicatorBox.Y(); got != want {
		t.Errorf("once the reply ends, live area from row %d, want the indicator's, %d", got, want)
	}
}

// TestTallMenuKeepsTheLiveRowsOnScreen opens a banner taller than the
// terminal while a call runs, which the input box bounds so that the call's
// row stays on screen with a row free above it. It sweeps the shimmer across
// the call's row, ends the call, and closes the banner, and checks that no
// change rewrites a row in the scrollback, and that the call shows whole,
// expanded, once it ends.
func TestTallMenuKeepsTheLiveRowsOnScreen(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	ui.Apply(ui.service.SetToolsExpanded(true))
	ui.ShowIndicator(WorkingIndicator(time.Second, false))
	var s Scrollback
	call := runBash("a1-bash", "go test ./...", []string{"ok  \tcalc"})
	text := strings.Repeat("A finished paragraph.\n\n", 30)
	startReply(ui, partEvent("start", message.TextPart{ID: "a1-text", Text: text}))
	applyEvents(ui, call[:2]...)
	s.Frame(t, ui, "the call runs")

	var items []widget.SelectItem
	for i := range 30 {
		items = append(items, widget.SelectItem{Name: fmt.Sprintf("item %d", i)})
	}
	ui.ShowBanner(EventBannerShow{Title: "Menu", Text: strings.Repeat("A line of the banner.\n", 20), Items: items})
	s.Frame(t, ui, "the banner opens")
	if live, room := s.Rows-ui.LiveTop(), ui.renderer.Height()-1; live != room {
		t.Fatalf("live area takes %d rows, want all but a row of the terminal, %d", live, room)
	}

	ui.shimmer.On.Store(true)
	for _, phase := range []float64{0.2, 0.5, 0.8} {
		ui.shimmer.Phase = phase
		ui.shimmer.SyncRows()
		s.Frame(t, ui, fmt.Sprintf("the shimmer at %.1f", phase))
	}
	applyEvents(ui, call[2:]...)
	s.Frame(t, ui, "the call ends")
	ui.HideBanner()
	s.Frame(t, ui, "the banner closes")

	if !s.Shows("ok  \tcalc") && !s.Shows("ok      calc") {
		t.Error("the ended call does not show its output once there is room")
	}
}

// TestTallInputBoxStaysStill streams a reply above an input box whose text
// would fill the screen, which it bounds so that the live area takes all of
// the screen but a row. It checks that the reply gains its rows above the
// live area, which keeps its rows, as the renderer needs to scroll them in
// through the free row rather than rewrite every row of the input box a row
// lower for each of them, and that no row in the scrollback is rewritten.
func TestTallInputBoxStaysStill(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	ui.ShowIndicator(WorkingIndicator(time.Second, false))
	ui.SetInputText(strings.Repeat("a line of a long prompt\n", 60))
	var s Scrollback
	events := streamText("a1-text", []string{strings.Repeat("A paragraph of the reply.\n\n", 20)})
	startReply(ui, events[0])
	s.Frame(t, ui, "the reply starts")
	if live, room := s.Rows-ui.LiveTop(), ui.renderer.Height()-1; live != room {
		t.Fatalf("live area takes %d rows, want all of the screen but a row, %d", live, room)
	}

	held := s.LiveArea(ui)
	for i, ev := range events[1 : len(events)-1] {
		ui.Apply(ui.service.ApplyAgentEvent(ev))
		s.Frame(t, ui, fmt.Sprintf("event %d", i+1))
		if got := s.LiveArea(ui); !slices.Equal(got, held) {
			t.Fatalf("event %d changed the live area:\n%q\nwant\n%q", i+1, got, held)
		}
	}
	if !s.Shows("A paragraph of the reply.") {
		t.Error("the reply gained no rows above the live area")
	}
}

// LiveArea returns the text of the rows of the frame drawn last from
// UI.LiveTop down.
func (s *Scrollback) LiveArea(ui *UI) []string {
	var rows []string
	for y := ui.LiveTop(); y < s.Rows; y++ {
		rows = append(rows, testutil.RowText(s.Last, y))
	}
	return rows
}

// replayDemo applies to ui the events of a run of the demo's turn, as the
// app publishes them: each step's message starts, its text streams a word
// at a time, its call is queued, runs, and ends, and the message ends. It
// calls each, naming the event, after applying each one.
func replayDemo(ui *UI, each func(step string)) {
	apply := func(ev app.Event, step string) {
		ui.Apply(ui.service.ApplyAgentEvent(ev))
		each(step)
	}
	for i, seg := range demo.Segments {
		id := message.ID(fmt.Sprintf("a%d", i))
		part := func(kind string, p message.ContentPart) app.Event {
			change := app.PartChange{MessageID: id, Part: p}
			switch kind {
			case "start":
				return app.EventPartStarted{PartChange: change}
			case "end":
				return app.EventPartEnded{PartChange: change}
			}
			return app.EventPartUpdated{PartChange: change}
		}
		apply(app.EventMessageStarted{Message: app.Message{ID: id, Role: message.RoleAgent}}, fmt.Sprintf("step %d starts", i))
		var parts []message.ContentPart
		text := message.TextPart{ID: message.PartID(id + "-text"), Text: seg.Text}
		if seg.Text != "" {
			var sb strings.Builder
			for j, word := range strings.SplitAfter(seg.Text, " ") {
				sb.WriteString(word)
				kind := "update"
				if j == 0 {
					kind = "start"
				}
				apply(part(kind, message.TextPart{ID: text.ID, Text: sb.String()}), fmt.Sprintf("step %d, word %d", i, j))
			}
			parts = append(parts, text)
		}
		if tool := seg.Tool; tool != nil {
			call := func(st message.ToolState) message.ToolPart {
				return message.ToolPart{ID: message.PartID(id + "-call"), CallID: string(id), Name: tool.Name, State: st}
			}
			apply(part("start", call(message.ToolStatePending{Input: tool.Input})), fmt.Sprintf("step %d, call queued", i))
			apply(part("update", call(message.ToolStateRunning{Input: tool.Input})), fmt.Sprintf("step %d, call runs", i))
			ended := call(tool.State(time.Time{}, time.Time{}))
			apply(part("end", ended), fmt.Sprintf("step %d, call ends", i))
			parts = append(parts, ended)
		}
		if seg.Text != "" {
			apply(part("end", text), fmt.Sprintf("step %d, text ends", i))
		}
		apply(app.EventMessageEnded{Message: app.Message{ID: id, Role: message.RoleAgent, Parts: parts}}, fmt.Sprintf("step %d ends", i))
	}
}

// TestDemoTurnUnderATallInputBox replays the demo's turn while the input
// holds a prompt taller than the screen, and checks that no event rewrites
// a row in the scrollback, and that the input box keeps its height once the
// run's first live rows show, rather than growing and shrinking as each
// step's live rows come and go, which moves every row of it.
func TestDemoTurnUnderATallInputBox(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	ui.service.state.Running = true
	ui.ShowIndicator(WorkingIndicator(time.Second, false))
	ui.SetInputText(strings.Repeat("a line of a long prompt\n", 60))
	var s Scrollback
	s.Frame(t, ui, "the run starts")
	height, changes := ui.InputBoxHeight(), 0
	replayDemo(ui, func(step string) {
		s.Frame(t, ui, step)
		if h := ui.InputBoxHeight(); h != height {
			changes++
			t.Logf("%s: input box %d rows, was %d", step, h, height)
			height = h
		}
	})
	if changes > 1 {
		t.Errorf("input box changed height %d times during the run, want once at most", changes)
	}
}

// TestUsageLeavesTheInputBoxRoom streams a reply taller than the terminal
// whose usage then starts and ends, as every reply's does, and checks that a
// prompt pasted into the input box while the run goes on shows whole. The
// usage once counted as live from its start to its end, and stays gone, laid
// out at the transcript's top, so the live area took the whole transcript,
// which the run then kept free, leaving the input box at its least.
func TestUsageLeavesTheInputBoxRoom(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	ui.service.state.Running = true
	ui.ShowIndicator(WorkingIndicator(time.Second, false))
	startReply(ui, streamText("a1-text", []string{strings.Repeat("A finished paragraph.\n\n", 30)})...)
	usage := message.UsagePart{ID: "a1-usage", Usage: message.Usage{InputTokens: 100, OutputTokens: 10}}
	for _, ev := range []app.Event{partEvent("start", usage), partEvent("end", usage)} {
		applyEvents(ui, ev)
		DrawInline(ui)
	}

	ui.SetInputText("one\ntwo\nthree\nfour\nfive")
	DrawInline(ui)
	if got, want := ui.input.Height(), 5; got != want {
		t.Errorf("input box shows %d rows of the pasted prompt, want all %d", got, want)
	}
}

// TestGonePartIsNotLive checks that a gone part, as a reply's usage is, is
// not the live part of its bubble even while its message lists it as live:
// it takes no rows to change, and lays out at the transcript's top.
func TestGonePartIsNotLive(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	usage := message.UsagePart{ID: "a1-usage", Usage: message.Usage{OutputTokens: 10}}
	startReply(ui, append(streamText("a1-text", []string{"Done."}), partEvent("end", usage))...)
	cm := ui.messageWidgets[liveReply]
	cm.SetSegmentLive(liveReply, []message.PartID{usage.ID})
	DrawInline(ui)
	if row, ok := cm.LiveRow(); ok {
		t.Errorf("the gone usage is live at row %d, want no live row", row)
	}
}

// partsOf returns the parts of message id in the transcript of ui, as
// the end of the message carries them.
func partsOf(ui *UI, id message.ID) []message.ContentPart {
	for _, m := range ui.service.state.Messages {
		if m.ID == id {
			return m.Parts
		}
	}
	return nil
}
