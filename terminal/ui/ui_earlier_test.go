package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/testutil"
)

// newFullscreenTestUI returns a UI built by BuildUI in fullscreen mode,
// without its input box, so that the transcript fills the screen.
func newFullscreenTestUI(t *testing.T) *UI {
	t.Helper()
	ui := newBuiltTestUI(t, false)
	ui.inputWrapper.SetGone(true)
	return ui
}

// loadFullscreen returns a UI as newFullscreenTestUI does that loaded msgs
// as the active session's transcript, as a session that opens loads them,
// and drew its first frame.
func loadFullscreen(t *testing.T, msgs []ChatMessage) *UI {
	t.Helper()
	ui := newFullscreenTestUI(t)
	ui.service.state.Messages = msgs
	ui.Apply([]UIEvent{EventSessionLoad{Messages: msgs}})
	drawScreen(ui)
	return ui
}

// drawScreen draws a frame of ui's screen, as large as its root, as the
// renderer does, its lifecycle passes first, and returns it.
func drawScreen(ui *UI) *buffer.Buffer {
	root := ui.renderer.Root()
	buf := buffer.New(root.Width(), root.Height())
	root.Render(buf, 0)
	return buf
}

// framesOf returns buf as golden frames show it, its text and its
// backgrounds.
func framesOf(t *testing.T, ui *UI, buf *buffer.Buffer) string {
	t.Helper()
	frames, err := testutil.GoldenFrames(buf, surfaceNames(ui))
	if err != nil {
		t.Fatal(err)
	}
	return frames
}

// builtCount returns how many of msgs the transcript of ui shows.
func builtCount(ui *UI, msgs []ChatMessage) int {
	n := 0
	for _, m := range msgs {
		if _, ok := ui.messageWidgets[m.ID]; ok {
			n++
		}
	}
	return n
}

// TestBuildTranscript_FullscreenBuildsTheLastScreens checks that a long
// session loaded in fullscreen mode builds the rows of its last messages
// alone, BuiltScreens screens of them at least, and shows its bottom.
func TestBuildTranscript_FullscreenBuildsTheLastScreens(t *testing.T) {
	msgs := longSession(200, 3)
	ui := loadFullscreen(t, msgs)

	want := BuiltScreens * ui.ScreenRows()
	if height := ui.TranscriptHeight(); height < want || height >= 2*want {
		t.Errorf("the transcript takes %d rows, want %d at least and fewer than %d", height, want, 2*want)
	}
	if _, ok := ui.messageWidgets["u0"]; ok {
		t.Error("the transcript shows the first prompt, want it left for a scroll up")
	}
	if _, ok := ui.messageWidgets[msgs[len(msgs)-1].ID]; !ok {
		t.Error("the transcript does not show the last reply")
	}
	if built := builtCount(ui, msgs); built+len(ui.earlier) != len(msgs) || built > len(msgs)/4 {
		t.Errorf("built %d messages and left %d of %d, want the last few built and the rest left", built, len(ui.earlier), len(msgs))
	}
	if !ui.trimmed {
		t.Error("trimmed = false with earlier messages left out")
	}
	if !ui.messageBox.IsAtBottom() {
		t.Error("the transcript does not show its bottom")
	}
}

// TestTurnStart checks where a transcript of the last messages from an index
// on starts: where BubbleStart starts it, or at the prompt of the reply that
// starts there, past the notices between them.
func TestTurnStart(t *testing.T) {
	notice := func(id message.ID) ChatMessage { return ChatMessage{ID: id, Notice: &Notice{Text: "a notice"}} }
	msgs := []ChatMessage{systemText("s1", "rules"), agentText("a0", "v"), userText("u1", "p"), notice("n1"),
		agentText("a1", "x"), agentText("a2", "y"), notice("n2"), agentText("a3", "z"), userText("u2", "q"), agentText("a4", "w")}
	AssignParents(msgs)
	// The notice keeps the reply together, as the service records it.
	msgs[7].ParentID = "a1"
	for _, tt := range []struct {
		i, want int
	}{
		{i: -1, want: 0},
		// A reply after a system message has no prompt to take.
		{i: 1, want: 1},
		{i: 2, want: 2},
		// The notice between a prompt and its reply goes with them.
		{i: 3, want: 2},
		{i: 4, want: 2},
		{i: 5, want: 2},
		{i: 6, want: 2},
		{i: 7, want: 2},
		{i: 8, want: 8},
		{i: 9, want: 8},
		{i: 10, want: 10},
	} {
		if got := TurnStart(msgs, tt.i); got != tt.want {
			t.Errorf("TurnStart(%d) = %d, want %d", tt.i, got, tt.want)
		}
	}
}

// TestBuildTranscript_PinsTheLastPrompt checks that a session whose last
// reply alone takes more than BuiltScreens screens, loaded in fullscreen
// mode, builds that reply's prompt too, which pins to the top over it.
func TestBuildTranscript_PinsTheLastPrompt(t *testing.T) {
	msgs := longSession(5, 200)
	ui := loadFullscreen(t, msgs)
	buf := drawScreen(ui)

	prompt, ok := ui.messageWidgets["u4"]
	if !ok {
		t.Fatal("the transcript does not show the last reply's prompt")
	}
	if prompt.Box.StickyOffsetY() <= 0 {
		t.Error("the last reply's prompt does not pin to the top")
	}
	if top := testutil.RowText(buf, 0) + testutil.RowText(buf, 1); !strings.Contains(top, "prompt 4") {
		t.Errorf("the top rows read %q, want the pinned prompt 4", top)
	}
}

// TestBuildEarlierInView_FollowingBuildsNothing checks that a transcript
// that follows its bottom builds no earlier messages, frame after frame, the
// first frame after it was built included, whose scroll box still has the
// offset of the transcript before it.
func TestBuildEarlierInView_FollowingBuildsNothing(t *testing.T) {
	msgs := longSession(200, 3)
	ui := newFullscreenTestUI(t)
	ui.service.state.Messages = msgs
	ui.Apply([]UIEvent{EventSessionLoad{Messages: msgs}})
	left := len(ui.earlier)
	for range 3 {
		drawScreen(ui)
	}
	if len(ui.earlier) != left {
		t.Errorf("following the bottom built %d earlier messages, want none", left-len(ui.earlier))
	}
}

// TestBuildEarlier_LeavesTheLayoutDirty checks that the rows of earlier
// messages built before a frame leave the root's layout dirty, though
// measuring them laid it out, so that the frame fits the input box again.
func TestBuildEarlier_LeavesTheLayoutDirty(t *testing.T) {
	ui := loadFullscreen(t, longSession(200, 3))
	if ui.renderer.Root().LayoutNode().IsDirty() {
		t.Fatal("the root's layout is dirty after a frame")
	}
	if ui.BuildEarlier(ui.ScreenRows()) == 0 {
		t.Fatal("built no rows")
	}
	if !ui.renderer.Root().LayoutNode().IsDirty() {
		t.Error("the root's layout is clean after rows were built")
	}
}

// TestBuildEarlierInView_TallerTerminal checks that a transcript that follows
// its bottom builds the rows of earlier messages once the terminal grows
// taller than the rows built leave room for, and still follows its bottom.
func TestBuildEarlierInView_TallerTerminal(t *testing.T) {
	ui := loadFullscreen(t, longSession(200, 3))
	left := len(ui.earlier)
	root := ui.renderer.Root()
	root.Resize(root.Width(), 8*root.Height())
	for range 5 {
		drawScreen(ui)
	}

	if len(ui.earlier) >= left {
		t.Fatal("the taller terminal built no earlier messages")
	}
	if y, want := ui.messageBox.ScrollY(), EarlierScreens*ui.ScreenRows(); y < want {
		t.Errorf("the viewport is %d rows under the first row, want %d at least", y, want)
	}
	if ui.messageBox.UserScrolled() || !ui.messageBox.IsAtBottom() {
		t.Error("the transcript stopped following its bottom")
	}
}

// TestBuildEarlierInView_KeepsTheView checks that once the user scrolls up
// to fewer than EarlierScreens screens under the first row built, the next
// frame builds the rows of earlier messages above it and shows what the
// frame would have shown without them.
func TestBuildEarlierInView_KeepsTheView(t *testing.T) {
	ui := loadFullscreen(t, longSession(200, 3))
	screen := ui.ScreenRows()
	ui.messageBox.SetScrollY(screen)

	// The frame drawn without earlier messages to build is the view the
	// user scrolled to.
	earlier := ui.earlier
	ui.earlier = nil
	want := drawScreen(ui)
	ui.earlier = earlier

	got := drawScreen(ui)
	if len(ui.earlier) >= len(earlier) {
		t.Fatal("scrolling up built no earlier messages")
	}
	if y := ui.messageBox.ScrollY(); y < EarlierScreens*screen {
		t.Errorf("the viewport is %d rows under the first row, want %d at least", y, EarlierScreens*screen)
	}
	if g, w := framesOf(t, ui, got), framesOf(t, ui, want); g != w {
		t.Errorf("the frame that built earlier messages moved the view:\n%s\nwant:\n%s", g, w)
	}
	testutil.AssertGoldenFrames(t, "fullscreen_earlier_built", got, surfaceNames(ui))
}

// toolSession returns turns turns, each a prompt and a reply that ran a bash
// call of 100 lines of output, which an expanded call shows taller than the
// screen.
func toolSession(turns int) []ChatMessage {
	output := numberedLines(100, func(i int) string { return fmt.Sprint(i) })
	var msgs []ChatMessage
	for i := range turns {
		n := fmt.Sprint(i)
		msgs = append(msgs, userText(message.ID("u"+n), "prompt "+n),
			agentParts(message.ID("a"+n), completed("c"+n, "bash", `{"command":"ls"}`, output)))
	}
	AssignParents(msgs)
	AssignPrev(msgs)
	return msgs
}

// TestBuildEarlierInView_KeepsTheViewOverCutCalls checks that the rows of
// earlier messages built as the user scrolls up keep the view where it was,
// though the frame that builds them cuts their expanded calls short to fit
// the screen, as FitToolBoxes cuts them.
func TestBuildEarlierInView_KeepsTheViewOverCutCalls(t *testing.T) {
	msgs := toolSession(40)
	ui := newFullscreenTestUI(t)
	ui.service.state.ToolsExpanded = true
	ui.service.state.Messages = msgs
	ui.Apply([]UIEvent{EventSessionLoad{Messages: msgs}})
	drawScreen(ui)
	ui.messageBox.SetScrollY(ui.ScreenRows())

	earlier := ui.earlier
	ui.earlier = nil
	want := drawScreen(ui)
	ui.earlier = earlier

	got := drawScreen(ui)
	if len(ui.earlier) >= len(earlier) {
		t.Fatal("scrolling up built no earlier messages")
	}
	if b := partBoxesOf(ui, msgs[len(ui.earlier)+1].ID)[0]; !b.Truncated.Visible() {
		t.Error("the call of the first reply built is not cut short")
	}
	if g, w := framesOf(t, ui, got), framesOf(t, ui, want); g != w {
		t.Errorf("the frame that built earlier messages moved the view:\n%s\nwant:\n%s", g, w)
	}
}

// TestBuildEarlierInView_BuildsToTheTop checks that scrolling up to the top
// again and again builds every earlier message, in its place: the transcript
// then shows what one built whole shows, at the top, the middle and the
// bottom.
func TestBuildEarlierInView_BuildsToTheTop(t *testing.T) {
	msgs := longSession(40, 3)
	ui := loadFullscreen(t, msgs)
	if len(ui.earlier) == 0 {
		t.Fatal("the session is too short to leave earlier messages out")
	}
	for i := 0; len(ui.earlier) > 0; i++ {
		if i == len(msgs) {
			t.Fatalf("%d earlier messages left after %d scrolls to the top", len(ui.earlier), i)
		}
		ui.messageBox.SetScrollY(0)
		drawScreen(ui)
	}
	if ui.trimmed {
		t.Error("trimmed = true with every message built")
	}

	whole := newFullscreenTestUI(t)
	whole.service.state.Messages = msgs
	for _, m := range msgs {
		whole.AddMessage(m)
	}
	drawScreen(whole)
	if got, want := ui.messageBox.ContentHeight(), whole.messageBox.ContentHeight(); got != want {
		t.Fatalf("the transcript takes %d rows, want %d, as one built whole", got, want)
	}
	for _, y := range []int{0, ui.messageBox.ContentHeight() / 2, ui.messageBox.ContentHeight()} {
		ui.messageBox.SetScrollY(y)
		whole.messageBox.SetScrollY(y)
		if got, want := framesOf(t, ui, drawScreen(ui)), framesOf(t, whole, drawScreen(whole)); got != want {
			t.Errorf("scrolled %d rows down, the transcript shows:\n%s\nwant, as one built whole:\n%s", y, got, want)
		}
	}
}

// TestBuildEarlier_SwitchesToFullscreen checks that an inline transcript
// switched to fullscreen mode builds the last BuiltScreens screens of rows of
// the terminal, not of the root as inline mode laid it out, far taller.
func TestBuildEarlier_SwitchesToFullscreen(t *testing.T) {
	msgs := longSession(400, 3)
	ui := loadSession(t, true, msgs)
	root := ui.renderer.Root()
	root.Resize(root.Width(), inlineLayoutRows)

	ui.SetInlineMode(EventInlineModeSet{Inline: false})

	if len(ui.earlier) == 0 || !ui.trimmed {
		t.Errorf("earlier holds %d messages, trimmed = %v, want the first ones left out", len(ui.earlier), ui.trimmed)
	}
	if built := builtCount(ui, msgs); built > len(msgs)/4 {
		t.Errorf("built %d messages of %d, want the last few", built, len(msgs))
	}
	if got, want := root.Height(), ui.renderer.Height(); got != want {
		t.Errorf("the root is %d rows tall, want the terminal's %d", got, want)
	}
}

// TestBuildEarlier_SwitchesToInline checks that a fullscreen transcript that
// lacks its earlier rows, switched to inline mode, shows the last TailRows
// rows, as an inline load builds them.
func TestBuildEarlier_SwitchesToInline(t *testing.T) {
	ui := loadFullscreen(t, longSession(400, 3))
	ui.SetInlineMode(EventInlineModeSet{Inline: true})
	if len(ui.earlier) > 0 || !ui.trimmed {
		t.Errorf("earlier holds %d messages, trimmed = %v, want none and true", len(ui.earlier), ui.trimmed)
	}
	if height := ui.TranscriptHeight(); height < TailRows {
		t.Errorf("the inline transcript takes %d rows, want %d at least", height, TailRows)
	}
}
