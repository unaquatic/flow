package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/extension/snapshot"
	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/input"
	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/render"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/terminal"
	"github.com/unaquatic/fluid/widget"
)

// The trees streamToolTurn's reply starts from and ends with.
var (
	toolTurnBase = mustParseTree("7a36b04ce429eb3326713c5689665b792179b206")
	toolTurnTree = mustParseTree("c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0")
)

// mustParseTree parses s, a valid tree hash.
func mustParseTree(s string) snapshot.Hash {
	h, err := snapshot.ParseHash(s)
	if err != nil {
		panic(err)
	}
	return h
}

// streamToolTurn applies the updates of agent step a1 as the agent sends
// them: the step follows a prompt that ended with toolTurnBase, streams text
// and the arguments of a bash call, runs the call, which prints as it goes,
// and ends with a snapshot part holding toolTurnTree.
func streamToolTurn(ui *UI) {
	const id = message.ID("a1")
	input := `{"command":"go test ./..."}`
	prev := []message.ContentPart{snapshot.SnapshotPart{ID: "u1-step", Tree: toolTurnBase}}
	step := snapshot.SnapshotPart{ID: "a1-step", Tree: toolTurnTree}
	text := func(s string) message.ContentPart { return message.TextPart{ID: "a1-text", Text: s} }
	tool := func(state message.ToolState) message.ContentPart {
		return message.ToolPart{ID: "a1-tool", CallID: "c1", Name: "bash", State: state}
	}
	running := func(output string) message.ToolState {
		return message.ToolStateRunning{Input: json.RawMessage(input), Output: output}
	}

	ui.AddMessage(ChatMessage{ID: id, ParentID: id, Role: message.RoleAgent, Prev: prev})
	for _, parts := range [][]message.ContentPart{
		{text("Running")},
		{text("Running the tests.")},
		{text("Running the tests."), tool(message.ToolStatePending{Input: json.RawMessage(input[:12])})},
		{text("Running the tests."), tool(message.ToolStatePending{Input: json.RawMessage(input)})},
		{text("Running the tests."), tool(running(""))},
		{text("Running the tests."), tool(running("ok  \tcalc"))},
		{text("Running the tests."), tool(message.ToolStateCompleted{Input: json.RawMessage(input), Output: "ok  \tcalc\t0.003s"})},
		{text("Running the tests."), tool(message.ToolStateCompleted{Input: json.RawMessage(input), Output: "ok  \tcalc\t0.003s"}), step},
	} {
		ui.UpdateMessage(ChatMessage{ID: id, Role: message.RoleAgent, Parts: parts, Prev: prev})
	}
}

func TestMessageUpdatesKeepOneWidgetPerPart(t *testing.T) {
	ui := newInlineTestUI(t)
	streamToolTurn(ui)
	seg := ui.messageWidgets["a1"].SegmentFor("a1")

	ids := make(map[render.Renderable]string, len(seg.Parts))
	for id, view := range seg.Parts {
		ids[view] = id
	}
	var order, texts []string
	var tools []message.ToolPart
	for _, child := range seg.ContentBox.Children() {
		order = append(order, ids[child])
		switch view := child.(type) {
		case *widget.StreamMarkdown:
			texts = append(texts, view.Content())
		case *ToolBox:
			tools = append(tools, view.Tool)
		}
	}
	if want := []string{"a1-text", "a1-tool", "a1-step"}; !slices.Equal(order, want) {
		t.Errorf("widgets are in part order %q, want %q", order, want)
	}
	if want := []string{"Running the tests."}; !slices.Equal(texts, want) {
		t.Errorf("text widgets show %q, want %q", texts, want)
	}
	if len(tools) != 1 {
		t.Fatalf("got %d tool widgets, want 1", len(tools))
	}
	if done, ok := tools[0].State.(message.ToolStateCompleted); !ok || done.Output != "ok  \tcalc\t0.003s" {
		t.Errorf("tool widget state = %+v, want completed with its output", tools[0].State)
	}
}

// TestMessageErrorKeepsOneView checks that the error a message failed with
// shows in one ErrorView after its parts, whose line and body read as the
// indicator's do, however often the message updates, and that the latest
// error replaces the one before it.
func TestMessageErrorKeepsOneView(t *testing.T) {
	ui := newInlineTestUI(t)
	msg := agentText("a1", "Reading the file.")
	ui.AddMessage(msg)
	msg.Error = errors.New("connection reset by peer")
	ui.UpdateMessage(msg)
	msg.Error = &agent.ResponseError{Provider: "gw", StatusCode: 401, ResponseBody: `{"error":"bad key"}`}
	ui.UpdateMessage(msg)

	var views []*ErrorView
	for _, child := range ui.messageWidgets["a1"].SegmentFor("a1").ContentBox.Children() {
		if v, ok := child.(*ErrorView); ok {
			views = append(views, v)
		}
	}
	if len(views) != 1 {
		t.Fatalf("got %d error views, want 1", len(views))
	}
	if got, want := views[0].Label.Content().PlainText(), IndicatorErrorTitle+" gw response error (401)"; got != want {
		t.Errorf("line = %q, want %q", got, want)
	}
	if got, want := views[0].Detail.Content().PlainText(), "{\n  \"error\": \"bad key\"\n}"; !views[0].Detail.Visible() || got != want {
		t.Errorf("body = %q (shown %v), want %q", got, views[0].Detail.Visible(), want)
	}
}

// TestMessagesHoldThePartsBefore checks that each message holds the parts of
// the message before it, which the views of extensions show it against,
// whether the transcript streams in or is loaded, and that the first holds
// none.
func TestMessagesHoldThePartsBefore(t *testing.T) {
	prompt := ChatMessage{ID: "u1", Role: message.RoleUser, Parts: []message.ContentPart{
		message.TextPart{ID: "u1-text", Text: "Run the tests"}, snapshot.SnapshotPart{ID: "u1-step", Tree: toolTurnBase},
	}}
	first := ChatMessage{ID: "a1", Role: message.RoleAgent, Parts: []message.ContentPart{snapshot.SnapshotPart{ID: "a1-step", Tree: toolTurnTree}}}
	second := ChatMessage{ID: "a2", Role: message.RoleAgent}
	want := [][]message.ContentPart{nil, prompt.Parts, first.Parts}

	ui := newInlineTestUI(t)
	var streamed [][]message.ContentPart
	for _, msg := range []ChatMessage{prompt, first, second} {
		for _, ev := range ui.service.AddMessage(msg) {
			streamed = append(streamed, ev.(EventMessageAdded).Msg.Prev)
		}
	}
	if !reflect.DeepEqual(streamed, want) {
		t.Errorf("streamed Prev = %v, want %v", streamed, want)
	}

	loaded := []ChatMessage{prompt, first, second}
	AssignPrev(loaded)
	var prevs [][]message.ContentPart
	for _, msg := range loaded {
		prevs = append(prevs, msg.Prev)
	}
	if !reflect.DeepEqual(prevs, want) {
		t.Errorf("loaded Prev = %v, want %v", prevs, want)
	}
}

func TestPromptStartedAndEnded(t *testing.T) {
	ui := newInlineTestUI(t)
	prompt := app.Message{
		ID:    "u1",
		Role:  message.RoleUser,
		Parts: []message.ContentPart{message.TextPart{ID: "u1-text", Text: "Run the tests"}},
	}
	ui.Apply(ui.service.ApplyAgentEvent(app.EventMessageStarted{Message: prompt}))
	ui.Apply(ui.service.ApplyAgentEvent(app.EventMessageEnded{Message: prompt}))

	var texts []string
	for _, child := range ui.messageWidgets["u1"].SegmentFor("u1").ContentBox.Children() {
		if view, ok := child.(*widget.StreamMarkdown); ok {
			texts = append(texts, view.Content())
		}
	}
	if want := []string{"Run the tests"}; !slices.Equal(texts, want) {
		t.Errorf("prompt shows %q, want %q", texts, want)
	}
}

func TestToolBoxSkipsEqualTool(t *testing.T) {
	input := json.RawMessage(`{"command":"ls"}`)
	shown := message.ToolPart{ID: "t1", Name: "bash", State: message.ToolStateRunning{Input: input, Output: "a.go"}}
	tests := []struct {
		name  string
		tool  message.ToolPart
		dirty bool
	}{
		{name: "equal copy", dirty: false, tool: message.ToolPart{ID: "t1", Name: "bash",
			State: message.ToolStateRunning{Input: slices.Clone(input), Output: "a.go"}}},
		{name: "more output", dirty: true, tool: message.ToolPart{ID: "t1", Name: "bash",
			State: message.ToolStateRunning{Input: input, Output: "a.go\nb.go"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			theme := DefaultTheme()
			box := newTestToolBox(t, shown, theme, theme.ResolveTextBase())
			renderFrames(40, 6, box)
			if box.LayoutNode().IsDirty() {
				t.Fatal("box needs layout after being drawn")
			}
			box.SetTool(tt.tool)
			if got := box.LayoutNode().IsDirty(); got != tt.dirty {
				t.Errorf("box needs layout = %v after SetTool, want %v", got, tt.dirty)
			}
		})
	}
}

// TestCtrlOLeavesKeptToolsExpanded checks that a call whose view keeps it
// expanded, as a todo call's does, starts expanded among collapsed calls,
// that Ctrl+O, expanding and collapsing the others, leaves it so, and that
// TogglesTools does not count it.
func TestCtrlOLeavesKeptToolsExpanded(t *testing.T) {
	ui := newInputTestUI(t)
	ui.AddMessage(agentParts("a1",
		message.ToolPart{ID: "t1", CallID: "t1", Name: "todo", State: message.ToolStateCompleted{
			Input: json.RawMessage(`{"todos":[{"content":"Add Divide","status":"in_progress"}]}`), Output: "[–] Add Divide"}},
		message.ToolPart{ID: "t2", CallID: "t2", Name: "bash", State: message.ToolStateCompleted{
			Input: json.RawMessage(`{"command":"go test ./..."}`), Output: "ok"}},
	))
	boxes := slices.Collect(ui.messageWidgets["a1"].ToolBoxes())
	check := func(when string, todo, bash bool) {
		t.Helper()
		if boxes[0].Expanded != todo || boxes[1].Expanded != bash {
			t.Errorf("%s: todo expanded %v, bash %v; want %v, %v", when, boxes[0].Expanded, boxes[1].Expanded, todo, bash)
		}
	}
	check("new", true, false)
	if ui.TogglesTools(false) {
		t.Error("TogglesTools(false) = true with only the todo call expanded")
	}
	press := func() {
		t.Helper()
		ui.HandleKey(input.NewParser().Parse([]byte("\x0f"))[0].Key) // Ctrl+O
	}
	press()
	check("Ctrl+O", true, true)
	press()
	check("Ctrl+O again", true, false)
}

// TestCtrlOTogglesTools checks that tool calls and step changes start
// collapsed, that Ctrl+O expands every one and those added after it, and
// that it collapses them again. A bare "o" still goes to the input. While the
// session tree is open, Ctrl+O cycles its filter instead.
func TestCtrlOTogglesTools(t *testing.T) {
	ui := newInputTestUI(t)
	// The extension shows a block under each reply, as a step's changes.
	ui.extensions = []Extension{testExtension{}}
	bash := func(id message.PartID) message.ContentPart {
		return message.ToolPart{ID: id, CallID: string(id), Name: "bash", State: message.ToolStateCompleted{
			Input: json.RawMessage(`{"command":"go test ./...","description":"Run the tests"}`), Output: "ok",
		}}
	}
	// expanded lists whether each block shows its details, in order: each
	// reply's tool calls, and then its changes. A box with nothing to
	// expand, such as the cache row, is no block.
	expanded := func() []bool {
		var got []bool
		for _, id := range []message.ID{"a1", "a2"} {
			if cm, ok := ui.messageWidgets[id]; ok {
				for c := range cm.Boxes() {
					if c.Expandable {
						got = append(got, c.Expanded)
					}
				}
			}
		}
		return got
	}
	check := func(when string, want ...bool) {
		t.Helper()
		if got := expanded(); !slices.Equal(got, want) {
			t.Errorf("%s: blocks expanded %v, want %v", when, got, want)
		}
	}
	press := func(seq string, consumed bool) {
		t.Helper()
		events := input.NewParser().Parse([]byte(seq))
		if len(events) != 1 || events[0].Key == nil {
			t.Fatalf("Parse(%q) = %+v, want one key", seq, events)
		}
		k := events[0].Key
		ui.HandleKey(k)
		if k.DefaultPrevented() != consumed {
			t.Errorf("%q: key consumed %v, want %v", seq, k.DefaultPrevented(), consumed)
		}
	}

	ui.AddMessage(agentParts("a1", bash("t1")))
	check("new", false, false)

	press("\x0f", true) // Ctrl+O
	check("Ctrl+O", true, true)
	ui.AddMessage(agentParts("a2", bash("t2")))
	ui.UpdateMessage(agentParts("a1", bash("t1"), bash("t3")))
	check("added after Ctrl+O", true, true, true, true, true)

	press("o", false)
	check("bare o", true, true, true, true, true)

	press("\x0f", true)
	check("Ctrl+O again", false, false, false, false, false)

	ui.Apply(ui.service.OpenTree(forkedTree("a2")))
	press("\x0f", true)
	check("Ctrl+O in the tree", false, false, false, false, false)
	if got := ui.service.state.Tree.Filter; got != TreeFilterNoTools {
		t.Errorf("Ctrl+O in the tree: filter %v, want %v", got, TreeFilterNoTools)
	}
}

// TestClickKeepsHeaderInPlace checks that in fullscreen, a click on a tool
// call's header while the transcript follows its bottom edge expands the call
// below the header rather than pushing the header up: a boxed call's header
// stays on the row that was clicked, and a compact call's moves down by the
// row of padding its block gains above it. The expanded header holds the tool
// name alone, as the command shows under it, or would below the viewport. A
// second click collapses the call where it was, and the transcript follows
// its bottom edge again.
func TestClickKeepsHeaderInPlace(t *testing.T) {
	const w, h = 60, 12
	for _, tt := range []struct {
		style ExpandedStyle
		// shift is the number of rows the expanded header sits below the
		// row that was clicked.
		shift int
	}{
		{style: ExpandedPlain},
		{style: ExpandedBoxed, shift: 1},
	} {
		t.Run(tt.style.String(), func(t *testing.T) {
			ui := newInputTestUI(t)
			ui.Apply(ui.service.SetExpandStyle(tt.style))
			for i := range 4 {
				ui.AddMessage(userText(message.ID(fmt.Sprintf("u%d", i)), "Run the tests"))
			}
			ui.AddMessage(agentParts("a1", message.ToolPart{ID: "a1-tool", CallID: "c1", Name: "bash",
				State: message.ToolStateCompleted{Input: json.RawMessage(`{"command":"go test ./..."}`), Output: "ok"}}))

			ui.renderer.Root().Remove(ui.messageBox.ID())
			root := terminal.NewRootRenderable(w, h)
			root.Add(ui.messageBox)
			// rowOf draws a frame and returns the row that shows text, or -1.
			rowOf := func(text string) int {
				buf := buffer.New(w, h)
				root.Render(buf, 0)
				for y, line := range buf.Lines() {
					if strings.Contains(line.Text(), text) {
						return y
					}
				}
				return -1
			}

			header := rowOf("Bash go test ./...")
			if header < 0 || ui.messageBox.UserScrolled() || !ui.messageBox.IsAtBottom() {
				t.Fatalf("setup: header on row %d, scrolled %v; want it shown with the transcript at its bottom",
					header, ui.messageBox.UserScrolled())
			}

			clickTool(ui, "a1", 0)
			if got, want := rowOf("Bash"), header+tt.shift; got != want {
				t.Errorf("expanded: header on row %d, want row %d, %d below where it was clicked", got, want, tt.shift)
			}
			if got := rowOf("Bash go test ./..."); got >= 0 {
				t.Errorf("expanded: header on row %d still holds the command", got)
			}
			// The details open downward from the header, past the bottom of
			// the viewport where they do not fit.
			if got := rowOf("$ go test ./..."); got >= 0 && got <= header+tt.shift {
				t.Errorf("expanded: command on row %d, want it below the header on row %d", got, header+tt.shift)
			}
			if !ui.messageBox.UserScrolled() {
				t.Error("expanded: the transcript still follows its bottom edge")
			}

			clickTool(ui, "a1", 0)
			if got := rowOf("Bash go test ./..."); got != header {
				t.Errorf("collapsed: header on row %d, want row %d", got, header)
			}
			if ui.messageBox.UserScrolled() || !ui.messageBox.IsAtBottom() {
				t.Error("collapsed: the transcript stopped following its bottom edge")
			}
		})
	}
}

func TestUpdateMessageKeepsUsage(t *testing.T) {
	ui := newInlineTestUI(t)
	ui.AddMessage(ChatMessage{ID: "a1", ParentID: "a1", Role: message.RoleAgent})
	usage := message.Usage{InputTokens: 1200, OutputTokens: 300}
	ui.UpdateMessage(ChatMessage{ID: "a1", Usage: usage,
		Parts: []message.ContentPart{message.TextPart{ID: "a1-text", Text: "Done."}}})
	ui.UpdateMessage(ChatMessage{ID: "a1",
		Parts: []message.ContentPart{message.TextPart{ID: "a1-text", Text: "Done."}}})

	if got := ui.messageWidgets["a1"].SegmentFor("a1").Usage; got != usage {
		t.Errorf("usage after an update without usage = %+v, want %+v", got, usage)
	}
}

// TestCacheRowUnderReply checks that, per message, a reply's cache use shows
// in a row of its own under its parts, whichever part comes last, and leaves
// the parts as they are: no trailer after its text and nothing added to a
// tool call's status. The row waits for the reply's usage, and a user's
// message shows none.
func TestCacheRowUnderReply(t *testing.T) {
	ui := newInlineTestUI(t)
	ui.Apply(ui.service.SetCacheMode(CachePerMessage))
	usage := message.Usage{InputTokens: 20_000, CachedInputTokens: 18_000, CacheCreationInputTokens: 1_500}
	const row = "Cache read 18K write 1.5K"
	text := message.TextPart{ID: "a1-text", Text: "Running the tests."}
	ui.AddMessage(agentParts("a1", text))
	seg := ui.messageWidgets["a1"].SegmentFor("a1")
	if seg.Cache.Visible() {
		t.Errorf("cache row shows %q before the reply reports usage; want none", seg.Cache.Header.PlainText())
	}

	ui.UpdateMessage(ChatMessage{ID: "a1", Usage: usage, Parts: []message.ContentPart{text}})
	if got := seg.Cache.Header.PlainText(); !seg.Cache.Visible() || got != row {
		t.Errorf("cache row = %q, shown %v; want %q, shown", got, seg.Cache.Visible(), row)
	}
	body := seg.Parts["a1-text"].(*widget.StreamMarkdown)
	if got := body.Trailer().PlainText(); got != "" {
		t.Errorf("text trailer = %q; want none", got)
	}

	// A call that follows the text, and its later updates, change neither
	// the row nor the call's status.
	input := json.RawMessage(`{"command":"go test ./..."}`)
	bash := message.ToolPart{ID: "a1-tool", CallID: "c1", Name: "bash", State: message.ToolStatePending{Input: input}}
	ui.UpdateMessage(ChatMessage{ID: "a1", Parts: []message.ContentPart{text, bash}})
	box := seg.Parts["a1-tool"].(*ToolBox)
	if got := box.Status.PlainText(); got != "[queued]" {
		t.Errorf("status = %q; want %q", got, "[queued]")
	}
	bash.State = message.ToolStateError{Input: input, Error: "FAIL\n\n[exit 1]"}
	ui.UpdateMessage(ChatMessage{ID: "a1", Parts: []message.ContentPart{text, bash}})
	if got := box.Status.PlainText(); got != "[exit 1]" {
		t.Errorf("status after the call failed = %q; want %q", got, "[exit 1]")
	}
	if got := seg.Cache.Header.PlainText(); !seg.Cache.Visible() || got != row {
		t.Errorf("cache row after the call = %q, shown %v; want %q, shown", got, seg.Cache.Visible(), row)
	}

	prompt := userText("u1", "Run the tests")
	prompt.Usage = usage
	ui.AddMessage(prompt)
	if ui.messageWidgets["u1"].SegmentFor("u1").Cache.Visible() {
		t.Errorf("prompt shows a cache row; want none")
	}
}

// TestCacheModeSet checks that the cache use of every reply shows where the
// mode says, and moves when the mode changes: in a row under each reply; only
// in the footer of the turn's last reply, after its time; or nowhere. Replies
// added later follow the mode in use.
func TestCacheModeSet(t *testing.T) {
	ui := newInlineTestUI(t)
	usage := message.Usage{InputTokens: 20_000, CachedInputTokens: 18_000}
	call := agentParts("a1", message.ToolPart{ID: "a1-tool", CallID: "c1", Name: "bash",
		State: message.ToolStateError{Input: json.RawMessage(`{"command":"go test ./..."}`), Error: "FAIL\n\n[exit 1]"}})
	call.Usage = usage
	ui.AddMessage(call)
	seg := ui.messageWidgets["a1"].SegmentFor
	status := seg("a1").Parts["a1-tool"].(*ToolBox).Status

	ui.Apply(ui.service.SetCacheMode(CacheOff))
	reply := agentText("a2", "Fixed.")
	reply.ParentID, reply.Usage, reply.Worked = "a1", usage, 39*time.Second
	ui.AddMessage(reply)
	trailer := seg("a2").Parts["a2-text"].(*widget.StreamMarkdown).Trailer
	footer := seg("a2").Footer.Text

	const row = "Cache read 18K write 0K"
	tests := []struct {
		mode   CacheMode
		rows   bool
		footer string
	}{
		{mode: CacheOff, footer: "Done [39s]"},
		{mode: CachePerTurn, footer: "Done [39s] [cache 18K write 0K]"},
		{mode: CachePerMessage, rows: true, footer: "Done [39s]"},
		{mode: CacheOff, footer: "Done [39s]"},
	}
	for _, tt := range tests {
		ui.Apply(ui.service.SetCacheMode(tt.mode))
		for _, id := range []message.ID{"a1", "a2"} {
			cache := seg(id).Cache
			if cache.Visible() != tt.rows || tt.rows && cache.Header.PlainText() != row {
				t.Errorf("%s: %s cache row %q, shown %v; want %q, shown %v", tt.mode, id, cache.Header.PlainText(), cache.Visible(), row, tt.rows)
			}
		}
		// The parts never carry the cache use.
		if got := status.PlainText(); got != "[exit 1]" {
			t.Errorf("%s: status = %q, want %q", tt.mode, got, "[exit 1]")
		}
		if got := trailer().PlainText(); got != "" {
			t.Errorf("%s: text trailer = %q, want none", tt.mode, got)
		}
		if got := footer.PlainText(); got != tt.footer {
			t.Errorf("%s: footer = %q, want %q", tt.mode, got, tt.footer)
		}
	}
}

// TestFooterSpacedFromRowAbove checks that the footer of a turn's last reply
// is parted from what its reply shows above it by a blank row, whatever that
// is: text, a collapsed tool call, or the cache row, which stacks under a call.
// A footer that is the first row of its reply has no row above it to part from.
func TestFooterSpacedFromRowAbove(t *testing.T) {
	usage := message.Usage{InputTokens: 20_000, CachedInputTokens: 18_000}
	text := message.TextPart{ID: "a1-text", Text: "Done."}
	call := message.ToolPart{ID: "a1-tool", CallID: "c1", Name: "bash", State: message.ToolStateCompleted{
		Input: json.RawMessage(`{"command":"go test ./..."}`), Output: "ok"}}
	tests := []struct {
		name  string
		mode  CacheMode
		parts []message.ContentPart
		gap   int
	}{
		{name: "text", mode: CacheOff, parts: []message.ContentPart{text}, gap: 1},
		{name: "text and cache row", mode: CachePerMessage, parts: []message.ContentPart{text}, gap: 1},
		{name: "call", mode: CacheOff, parts: []message.ContentPart{call}, gap: 1},
		{name: "call and cache row", mode: CachePerMessage, parts: []message.ContentPart{call}, gap: 1},
		{name: "cache row alone", mode: CachePerMessage, gap: 1},
		{name: "nothing above", mode: CacheOff, gap: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInlineTestUI(t)
			ui.Apply(ui.service.SetCacheMode(tt.mode))
			msg := agentParts("a1", tt.parts...)
			msg.Usage, msg.Worked = usage, 3*time.Second
			ui.AddMessage(msg)
			seg := ui.messageWidgets["a1"].SegmentFor("a1")
			if !seg.Footer.Visible() {
				t.Fatalf("footer is not shown")
			}
			if got := seg.Footer.LayoutNode().MarginTop; got != layout.Points(tt.gap) {
				t.Errorf("footer margin above = %v, want %d blank rows", got, tt.gap)
			}
		})
	}
}

// TestToolMarkersSet checks that the tool marker setting applies to the calls
// already shown and to those added later, and that the header's text starts
// in the same column with the marker and without it.
func TestToolMarkersSet(t *testing.T) {
	ui := newInlineTestUI(t)
	bash := func(id message.PartID) message.ContentPart {
		return message.ToolPart{ID: id, CallID: string(id), Name: "bash", State: message.ToolStateCompleted{
			Input: json.RawMessage(`{"command":"go test ./..."}`), Output: "ok"}}
	}
	ui.AddMessage(agentParts("a1", bash("t1")))
	check := func(when string, want ...bool) {
		t.Helper()
		var got []bool
		for _, id := range []message.ID{"a1", "a2"} {
			if cm, ok := ui.messageWidgets[id]; ok {
				for box := range cm.ToolBoxes() {
					got = append(got, box.Marker.Visible())
				}
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: markers shown %v, want %v", when, got, want)
		}
	}
	// headerX returns the column of the first call's header, as laid out.
	headerX := func() int {
		renderFrames(40, 6, detachTranscript(ui)...)
		for box := range ui.messageWidgets["a1"].ToolBoxes() {
			return box.Header.X()
		}
		return -1
	}

	check("new", false)
	shown := headerX()
	if shown != PaddingX {
		t.Fatalf("header at column %d, want %d, where prose starts", shown, PaddingX)
	}
	ui.Apply(ui.service.SetToolMarkers(true))
	check("shown", true)
	if x := headerX(); x != shown {
		t.Errorf("header at column %d with the marker, want %d, as without it", x, shown)
	}
	ui.AddMessage(agentParts("a2", bash("t2")))
	check("added shown", true, true)
	// An expanded call shows no marker, and its name stays in its column.
	expandTools(ui)
	check("expanded", false, false)
	if x := headerX(); x != shown {
		t.Errorf("expanded header at column %d, want %d, as collapsed", x, shown)
	}
	ui.service.SetToolsExpanded(false)
	ui.SetToolsExpanded(false)
	check("collapsed again", true, true)
	ui.Apply(ui.service.SetToolMarkers(false))
	check("hidden", false, false)
}

// TestToolColorSet checks that the tool name color applies to the marker and
// the name of the calls already shown and of those added later, and leaves
// the input after the name gray.
func TestToolColorSet(t *testing.T) {
	ui := newInlineTestUI(t)
	bash := func(id message.PartID) message.ContentPart {
		return message.ToolPart{ID: id, CallID: string(id), Name: "bash", State: message.ToolStateCompleted{
			Input: json.RawMessage(`{"command":"go test ./..."}`), Output: "ok"}}
	}
	ui.AddMessage(agentParts("a1", bash("t1")))
	check := func(when string, want style.Color, calls int) {
		t.Helper()
		n := 0
		for _, id := range []message.ID{"a1", "a2"} {
			cm, ok := ui.messageWidgets[id]
			if !ok {
				continue
			}
			for box := range cm.ToolBoxes() {
				n++
				chunks := box.Header.Content().Chunks
				if box.Marker.Fg() != want || chunks[0].Fg != want || chunks[len(chunks)-1].Fg != ToolFg {
					t.Errorf("%s: marker %v, name %v, input %v; want the marker and the name %v, the input %v",
						when, box.Marker.Fg(), chunks[0].Fg, chunks[len(chunks)-1].Fg, want, ToolFg)
				}
			}
		}
		if n != calls {
			t.Errorf("%s: %d calls, want %d", when, n, calls)
		}
	}

	check("new", ToolFg, 1)
	ui.Apply(ui.service.SetToolColor(ToolNameWhite))
	check("white", titleFg, 1)
	ui.AddMessage(agentParts("a2", bash("t2")))
	check("added white", titleFg, 2)
	ui.Apply(ui.service.SetToolColor(ToolNameGray))
	check("gray", ToolFg, 2)
}

// TestExpandStyleSet checks that the expanded style applies to the calls
// already shown and to those added later: a collapsed call is a row in
// either style, and an expanded one a block, on the surface when boxed and
// on the canvas when plain.
func TestExpandStyleSet(t *testing.T) {
	ui := newInlineTestUI(t)
	bash := func(id message.PartID) message.ContentPart {
		return message.ToolPart{ID: id, CallID: string(id), Name: "bash", State: message.ToolStateCompleted{
			Input: json.RawMessage(`{"command":"go test ./..."}`), Output: "ok"}}
	}
	ui.AddMessage(agentParts("a1", bash("t1")))
	// shapes returns "row", "boxed", or "plain" for each call shown.
	shapes := func() []string {
		var got []string
		for _, id := range []message.ID{"a1", "a2"} {
			if cm, ok := ui.messageWidgets[id]; ok {
				for box := range cm.ToolBoxes() {
					switch {
					case box.Row():
						got = append(got, "row")
					case box.Bg().IsTransparent():
						got = append(got, "plain")
					default:
						got = append(got, "boxed")
					}
				}
			}
		}
		return got
	}
	check := func(when string, want ...string) {
		t.Helper()
		if got := shapes(); !slices.Equal(got, want) {
			t.Errorf("%s: calls shown as %q, want %q", when, got, want)
		}
	}

	check("new", "row")
	ui.Apply(ui.service.SetExpandStyle(ExpandedPlain))
	check("plain collapsed", "row")
	expandTools(ui)
	check("plain expanded", "plain")
	ui.AddMessage(agentParts("a2", bash("t2")))
	check("added plain expanded", "plain", "plain")
	ui.Apply(ui.service.SetExpandStyle(ExpandedBoxed))
	check("boxed expanded", "boxed", "boxed")
}
