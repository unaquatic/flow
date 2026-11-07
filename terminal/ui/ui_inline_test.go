package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/input"
	"github.com/unaquatic/fluid/render"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/terminal"
	"github.com/unaquatic/fluid/widget"
)

func newInlineTestUI(t *testing.T) *UI {
	t.Helper()
	// Tearing the renderer down restores terminal modes, which would otherwise
	// reach the test's output.
	r, err := terminal.NewRenderer(terminal.Config{InlineMode: true, Stdout: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	ui := &UI{
		renderer:       r,
		theme:          DefaultTheme(),
		service:        &Service{state: &UIState{}, Commands: builtinCommands(t)},
		extensions:     builtinExtensions(t),
		messageBox:     widget.NewScrollBox(widget.ScrollBoxWithID("messages")),
		indicatorBox:   widget.NewBox(widget.BoxWithID("indicator-box")),
		indicatorLabel: widget.NewText("", widget.TextWithID("indicator-label")),
		inputWrapper:   widget.NewBox(widget.BoxWithID("input-wrapper")),
		messageWidgets: make(map[message.ID]*ChatMessageBox),
	}
	ui.indicatorBox.Add(ui.indicatorLabel)
	ui.indicatorBox.SetGone(true)
	r.Root().Add(ui.messageBox)
	r.Root().Add(ui.indicatorBox)
	r.Root().Add(ui.inputWrapper)
	return ui
}

// promptRows is the number of rows a prompt takes in the transcript: the row
// that pins to the top of the fullscreen transcript, and the row under it
// that scrolls.
const promptRows = 2

// rootRowIDs returns the root's children in order, which is where inline
// transcript rows live.
func rootRowIDs(ui *UI) []string {
	var ids []string
	for _, child := range ui.renderer.Root().Children() {
		ids = append(ids, child.ID())
	}
	return ids
}

func TestAddTranscriptRowTargetsTheActiveParent(t *testing.T) {
	ui := newInlineTestUI(t)

	ui.AddMessage(ChatMessage{ID: "m1", ParentID: "m1", Role: message.RoleUser})

	if got := len(ui.messageBox.Children()); got != promptRows {
		t.Errorf("fullscreen transcript holds %d rows, want the %d of a prompt", got, promptRows)
	}

	ui.inline = true
	ui.AddMessage(ChatMessage{ID: "m2", ParentID: "m2", Role: message.RoleUser})

	// Inline rows are parented to the root, before the indicator.
	ids := rootRowIDs(ui)
	row := ui.transcriptRows[len(ui.transcriptRows)-1]
	if slices.Index(ids, row.ID()) >= slices.Index(ids, "indicator-box") {
		t.Errorf("root children are %v, want the new row before the indicator", ids)
	}
}

// TestInlineTranscriptStaysOffTheRootTail keeps transcript rows above the fixed
// chrome, which the root also owns.
func TestInlineTranscriptStaysOffTheRootTail(t *testing.T) {
	ui := newInlineTestUI(t)
	ui.inline = true

	for _, id := range []message.ID{"m1", "m2", "m3"} {
		ui.AddMessage(ChatMessage{ID: id, ParentID: id, Role: message.RoleUser})
	}

	ids := rootRowIDs(ui)
	indicator := slices.Index(ids, "indicator-box")
	input := slices.Index(ids, "input-wrapper")
	if indicator < 0 || input < 0 || input < indicator {
		t.Fatalf("root children are %v, want the indicator then the input last", ids)
	}
	for _, row := range ui.transcriptRows {
		if slices.Index(ids, row.ID()) > indicator {
			t.Errorf("row %q sits below the indicator in %v", row.ID(), ids)
		}
	}
}

func TestResetTranscriptDetachesEveryRow(t *testing.T) {
	ui := newInlineTestUI(t)
	ui.inline = true
	for _, id := range []message.ID{"m1", "m2"} {
		ui.AddMessage(ChatMessage{ID: id, ParentID: id, Role: message.RoleUser})
	}
	rows := slices.Clone(ui.transcriptRows)

	ui.ResetTranscript()

	if len(ui.transcriptRows) != 0 {
		t.Errorf("transcript still tracks %d rows", len(ui.transcriptRows))
	}
	for _, row := range rows {
		if row.Parent() != nil {
			t.Errorf("row %q is still attached", row.ID())
		}
	}
	// The fixed chrome survives.
	if got := rootRowIDs(ui); len(got) != 3 {
		t.Errorf("root children are %v, want only the chrome", got)
	}
}

// TestSetInlineModeIsSynchronous guards against reintroducing a call onto the
// renderer's event loop. UI events already run there, so waiting for the loop
// would deadlock the key that requested the switch.
func TestSetInlineModeIsSynchronous(t *testing.T) {
	ui := newInlineTestUI(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		ui.SetInlineMode(EventInlineModeSet{Inline: true})
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SetInlineMode blocked; it must not wait on the event loop")
	}

	if !ui.inline {
		t.Error("expected inline mode to be active")
	}
}

// TestSetInlineModeMovesRows checks that a mode switch reparents the transcript
// instead of rebuilding it, so streaming content and widget state survive.
func TestSetInlineModeMovesRows(t *testing.T) {
	ui := newInlineTestUI(t)
	ids := []message.ID{"m1", "m2", "m3"}
	for _, id := range ids {
		ui.AddMessage(ChatMessage{ID: id, ParentID: id, Role: message.RoleUser})
	}
	before := ui.messageWidgets[ids[0]]

	ui.SetInlineMode(EventInlineModeSet{Inline: true})

	if got := len(ui.messageBox.Children()); got != 0 {
		t.Errorf("scroll transcript holds %d rows, want 0", got)
	}
	// Every bubble, and no notice of the switch.
	if got, want := len(ui.transcriptRows), promptRows*len(ids); got != want {
		t.Errorf("transcript tracks %d rows, want %d", got, want)
	}
	if ui.messageWidgets[ids[0]] != before {
		t.Error("expected the bubble widgets to be reused")
	}
	if parent := before.Box.Parent(); parent == nil || parent.ID() != ui.renderer.Root().ID() {
		t.Error("expected the bubble to be reparented to the root")
	}
}

func TestSetInlineModeRoundTrip(t *testing.T) {
	ui := newInlineTestUI(t)
	ui.AddMessage(ChatMessage{ID: "m1", ParentID: "m1", Role: message.RoleUser})

	ui.SetInlineMode(EventInlineModeSet{Inline: true})
	inlineRows := len(ui.transcriptRows)

	ui.SetInlineMode(EventInlineModeSet{Inline: false})

	// The rows carried back, and no notice of the switch.
	if got := len(ui.messageBox.Children()); got != inlineRows {
		t.Errorf("scroll transcript holds %d rows, want %d", got, inlineRows)
	}
	for _, row := range ui.transcriptRows {
		if parent := row.Parent(); parent == nil || parent.ID() != ui.messageBox.ID() {
			t.Errorf("row %q was not returned to the scroll container", row.ID())
		}
	}
}

func TestSetInlineModeIgnoresUnchangedMode(t *testing.T) {
	ui := newInlineTestUI(t)
	ui.AddMessage(ChatMessage{ID: "m1", ParentID: "m1", Role: message.RoleUser})

	ui.SetInlineMode(EventInlineModeSet{Inline: false})

	if got := len(ui.messageBox.Children()); got != promptRows {
		t.Errorf("transcript holds %d rows, want the existing %d", got, promptRows)
	}
}

// TestQuitDropsTheInputBox checks that quitting inline leaves the input box
// and the indicator out of the last frame, so that the output left in the
// terminal ends with the transcript, and that the live area then starts under
// the transcript, where the box would. It hides the input's cursor before the
// renderer is torn down. The alternate screen leaves nothing behind, so a
// fullscreen quit keeps the box and the cursor.
func TestQuitDropsTheInputBox(t *testing.T) {
	for _, tt := range []struct {
		name   string
		inline bool
	}{
		{name: "inline", inline: true},
		{name: "fullscreen"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ui := newBuiltTestUI(t, tt.inline)
			ui.AddMessage(userText("u1", "Run the tests"))
			ui.ShowIndicator(EventIndicatorShow{Text: "Request failed"})
			ui.renderer.FocusRenderable(ui.input)

			ui.ProcessEvent(EventQuit{})

			if ui.inputWrapper.Visible() == tt.inline {
				t.Errorf("input box shown %v, want %v", ui.inputWrapper.Visible(), !tt.inline)
			}
			if tt.inline && ui.indicatorBox.Visible() {
				t.Error("indicator shown in the last frame, want it left out")
			}
			if tt.inline && ui.input.Focused() {
				t.Error("input kept the focus, which would draw its cursor in the last frame")
			}
			if !ui.renderer.IsDestroyed() {
				t.Error("expected the renderer to be destroyed")
			}
		})
	}
}

// TestQuitLiveTop checks that once an inline quit drops the input box, the
// live area starts under the transcript and the blank row that ends it,
// where the box would have started.
func TestQuitLiveTop(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	ui.AddMessage(userText("u1", "Run the tests"))
	ui.renderer.Root().CalculateLayout()
	want := ui.InputBoxTop()
	ui.PrepareInlineQuit()
	ui.renderer.Root().CalculateLayout()
	if got := ui.InputBoxTop(); got != want || got == 0 {
		t.Errorf("live area starts at row %d, want %d, where the input box started", got, want)
	}
}

// TestResumeNoticeComesLast checks that the resume notice goes under every
// other row, the indicator included, so that it ends the final frame.
func TestResumeNoticeComesLast(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	ui.AddMessage(userText("u1", "Run the tests"))
	ui.ShowIndicator(EventIndicatorShow{Text: "Request failed"})

	ui.AppendResumeNotice("01a12e38-2689-73a1-a799-5db6b6a8bc44")

	rows := rootRowIDs(ui)
	if rows[len(rows)-1] != "resume-notice" {
		t.Errorf("rows %q, want the resume notice last", rows)
	}
}

// TestQuitShowsTheResumeNotice checks that quitting inline shows the resume
// notice, and reports it shown, only for a session that is stored.
func TestQuitShowsTheResumeNotice(t *testing.T) {
	for _, tt := range []struct {
		name   string
		stored bool
	}{
		{name: "stored", stored: true},
		{name: "not stored"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, a := newTestService(t)
			s.NewSession()
			if tt.stored {
				err := a.Sessions().AppendMessage(t.Context(), s.SessionID(), &message.Content{
					ID: message.NewID(), Role: message.RoleUser,
					Parts: []message.ContentPart{message.TextPart{Text: "hi"}},
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			r, err := terminal.NewRenderer(terminal.Config{InlineMode: true, Stdout: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			ui := &UI{renderer: r, theme: DefaultTheme(), service: s, inline: true,
				messageWidgets: make(map[message.ID]*ChatMessageBox)}
			ui.BuildUI()

			ui.ProcessEvent(EventQuit{})

			if got := ui.resumeShown.Load(); got != tt.stored {
				t.Errorf("resume notice shown = %v, want %v", got, tt.stored)
			}
		})
	}
}

func TestScrollTranscriptSkippedInline(t *testing.T) {
	ui := newInlineTestUI(t)
	key := &input.KeyEvent{Name: input.KeyPageUp}

	if !ui.ScrollTranscript(key) {
		t.Error("fullscreen mode should scroll the transcript")
	}

	ui.inline = true

	if ui.ScrollTranscript(key) {
		t.Error("inline mode should leave scrolling to the terminal")
	}
}

// TestTranscriptWheelAccelerates sends five wheel events, gap apart on
// synctest's clock, to the fullscreen transcript scrolled to its top, and
// checks how far they scroll it.
func TestTranscriptWheelAccelerates(t *testing.T) {
	tests := []struct {
		name string
		gap  time.Duration
		want int
	}{
		{name: "slow events move one row each", gap: 100 * time.Millisecond, want: 5},
		// The events move 1, 1, 2, 3, and 3 rows.
		{name: "fast events accelerate", gap: 10 * time.Millisecond, want: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInputTestUI(t)
			for i := range 30 {
				ui.AddNotice(Notice{Text: fmt.Sprintf("Notice %d", i)})
			}
			DrawFullscreen(ui, 40, 10, 0)
			synctest.Test(t, func(t *testing.T) {
				for range 5 {
					time.Sleep(tt.gap)
					ui.messageBox.OnMouseScrollHandler(&input.MouseEvent{
						Type:   input.MouseScroll,
						Button: input.ButtonNone,
						Scroll: &input.ScrollInfo{DeltaY: 1},
					})
				}
			})
			if got := ui.messageBox.ScrollY(); got != tt.want {
				t.Errorf("scrollY = %d after 5 wheel events %v apart, want %d", got, tt.gap, tt.want)
			}
		})
	}
}

// TestShowIndicatorWhile checks that the indicator shows the text while fn
// runs, and then shows what it showed before, or nothing.
func TestShowIndicatorWhile(t *testing.T) {
	for _, tt := range []struct {
		name   string
		before EventIndicatorShow // the indicator before; no text means hidden
		// shown is the indicator's text before, and after fn runs.
		shown string
	}{
		{name: "hidden"},
		{name: "working", before: WorkingIndicator(12*time.Second, false), shown: "Working [12s] [esc cancel] [ctrl+o expand]"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ui := newBuiltTestUI(t, true)
			if tt.before.Text != "" {
				ui.ShowIndicator(tt.before)
			}
			ran := false
			ui.ShowIndicatorWhile(ExpandingToolsText, func() {
				ran = true
				if got := ui.indicatorLabel.PlainText(); got != ExpandingToolsText || !ui.indicatorBox.Visible() {
					t.Errorf("indicator while fn runs = %q, visible %v; want %q shown", got, ui.indicatorBox.Visible(), ExpandingToolsText)
				}
				if chunk := ui.indicatorLabel.Content().Chunks[0]; chunk.Attributes&style.AttrBold != 0 {
					t.Errorf("indicator while fn runs = %+v; want it not bold", chunk)
				}
			})
			if !ran {
				t.Fatal("fn did not run")
			}
			if got := ui.indicatorBox.Visible(); got != (tt.shown != "") {
				t.Errorf("indicator visible after = %v, want %v", got, tt.shown != "")
			}
			if tt.shown == "" {
				return
			}
			if got := ui.indicatorLabel.PlainText(); got != tt.shown {
				t.Errorf("indicator after = %q, want %q as before", got, tt.shown)
			}
			if chunk := ui.indicatorLabel.Content().Chunks[0]; chunk.Text != tt.before.Text || chunk.Attributes&style.AttrBold != 0 {
				t.Errorf("indicator after starts with %+v, want %q, not bold, as before", chunk, tt.before.Text)
			}
		})
	}
}

// TestToolsExpandedSet checks that Ctrl+O shows or hides the details of every
// tool call, leaving a bash call's header to its name, and the indicator as it
// found it, and that TogglesTools tells whether it changes any call, which is
// when it shows a hint.
func TestToolsExpandedSet(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	ui.AddMessage(agentParts("a1", message.ToolPart{CallID: "c1", Name: "bash",
		State: message.ToolStateCompleted{Input: json.RawMessage(`{"command":"go test ./..."}`), Output: "ok"}}))
	ui.ShowIndicator(WorkingIndicator(12*time.Second, false))
	const working = "Working [12s] [esc cancel] [ctrl+o expand]"
	if !ui.TogglesTools(true) || ui.TogglesTools(false) {
		t.Fatalf("TogglesTools(true), TogglesTools(false) = %v, %v; want true, false", ui.TogglesTools(true), ui.TogglesTools(false))
	}

	ui.ProcessEvent(EventToolsExpandedSet{Expanded: true})

	for box := range ui.messageWidgets["a1"].ToolBoxes() {
		if !box.Expanded {
			t.Error("tool call stayed collapsed")
		}
		if got := box.Header.PlainText(); got != "Bash" {
			t.Errorf("expanded header = %q; want the command left to the details", got)
		}
	}
	if ui.TogglesTools(true) {
		t.Error("TogglesTools(true) = true once every call is expanded")
	}
	if got := ui.indicatorLabel.PlainText(); got != working || !ui.indicatorBox.Visible() {
		t.Errorf("indicator = %q, visible %v; want %q shown as before", got, ui.indicatorBox.Visible(), working)
	}
}

var _ render.Renderable = (*widget.Box)(nil)

// TestReplyStepsTakeRowsUnderTheirReply checks that each step of a reply is
// a row of the transcript of its own, so that the step that streams repaints
// alone, and that a step added once a notice follows the reply goes under
// the reply's rows, above the notice, in either mode and across a switch.
func TestReplyStepsTakeRowsUnderTheirReply(t *testing.T) {
	for _, inline := range []bool{false, true} {
		t.Run(fmt.Sprintf("inline=%v", inline), func(t *testing.T) {
			ui := newBuiltTestUI(t, inline)
			ui.AddMessage(agentText("a1", "first"))
			ui.AddNotice(Notice{Text: "notice"})
			notice := ui.transcriptRows[len(ui.transcriptRows)-1]
			second := agentText("a2", "second")
			second.ParentID = "a1"
			ui.AddMessage(second)

			steps := slices.Collect(ui.messageWidgets["a1"].Rows())
			if len(steps) != 2 {
				t.Fatalf("the reply takes %d rows, want one for each of its 2 steps", len(steps))
			}
			want := append(steps, notice)
			assertTranscriptRows(t, ui, want)

			ui.SetInlineMode(EventInlineModeSet{Inline: !inline})
			assertTranscriptRows(t, ui, want)
		})
	}
}

// assertTranscriptRows fails the test unless the transcript of ui is rows,
// in order, and their container holds them in that order, one after another.
func assertTranscriptRows(t *testing.T, ui *UI, rows []*widget.Box) {
	t.Helper()
	if !slices.Equal(ui.transcriptRows, rows) {
		t.Fatalf("transcript rows are %v, want %v", boxIDs(ui.transcriptRows), boxIDs(rows))
	}
	children := rows[0].Parent().ChildrenRaw()
	first := slices.Index(children, render.Renderable(rows[0]))
	for i, row := range rows {
		if got := slices.Index(children, render.Renderable(row)); got != first+i {
			t.Errorf("row %q is child %d of its container, want %d", row.ID(), got, first+i)
		}
	}
}

// boxIDs returns the IDs of boxes, in order.
func boxIDs(boxes []*widget.Box) []string {
	ids := make([]string, len(boxes))
	for i, b := range boxes {
		ids[i] = b.ID()
	}
	return ids
}

// TestPreviewReplyStepsTakeRowsUnderTheirReply checks that the session
// preview shows each step of a reply in a row of its own, under the reply's
// rows above it, as the transcript does, though another reply follows it.
func TestPreviewReplyStepsTakeRowsUnderTheirReply(t *testing.T) {
	ui := newBuiltTestUI(t, false)
	second := agentText("a2", "second")
	second.ParentID = "a1"
	ui.LoadPreview([]ChatMessage{agentText("a1", "first"), agentText("a3", "third"), second})

	want := slices.Concat(slices.Collect(ui.previewWidgets["a1"].Rows()), slices.Collect(ui.previewWidgets["a3"].Rows()))
	if len(want) != 3 {
		t.Fatalf("the replies take %d rows, want 3: one for each step", len(want))
	}
	got := make([]*widget.Box, 0, len(want))
	for _, c := range ui.previewMessageBox.ChildrenRaw() {
		if b, ok := c.(*widget.Box); ok {
			got = append(got, b)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("preview rows are %v, want %v", boxIDs(got), boxIDs(want))
	}
}
