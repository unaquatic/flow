package ui

import (
	"slices"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/terminal"
	"github.com/unaquatic/fluid/testutil"
	"github.com/unaquatic/fluid/widget"
)

// This file exports the test helpers of package ui to the tests of package
// ui_test, which show the widgets of real extensions in the transcript. The
// tests of package ui cannot import those widgets, since they import ui.

// BuiltinExtensions are the widgets of the extensions flow is built with that
// the tests of package ui show the transcript and the menus with, as flow
// shows them: the formats of the built-in tools' calls, and the demo's
// /demo. The tests of package ui cannot import them, since they import ui:
// builtin_test.go, in package ui_test, sets them as the test binary starts,
// before any test runs.
var BuiltinExtensions []Extension

// builtinExtensions returns BuiltinExtensions, failing the test when
// builtin_test.go has not set them.
func builtinExtensions(t *testing.T) []Extension {
	t.Helper()
	if len(BuiltinExtensions) == 0 {
		t.Fatal("BuiltinExtensions is empty: builtin_test.go sets it as the test binary starts")
	}
	return slices.Clone(BuiltinExtensions)
}

// builtinCommands returns the slash commands of a UI with
// BuiltinExtensions, as CommandsWith gives them.
func builtinCommands(t *testing.T) []SlashCommand {
	t.Helper()
	return CommandsWith(builtinExtensions(t))
}

// runCommand returns what the slash command name, among those of a UI with
// BuiltinExtensions, does to s, failing the test when there is none.
func runCommand(t *testing.T, s *Service, name Command) []UIEvent {
	t.Helper()
	cmd, ok := LookupCommand(builtinCommands(t), name)
	if !ok {
		t.Fatalf("no /%s among the commands of BuiltinExtensions", name)
	}
	return cmd.Run(s)
}

// toolFormats returns the ToolFormatters among BuiltinExtensions, for a
// ToolBox a test makes outside a transcript.
func toolFormats(t *testing.T) []ToolFormatter {
	t.Helper()
	var out []ToolFormatter
	for _, e := range builtinExtensions(t) {
		if f, ok := e.(ToolFormatter); ok {
			out = append(out, f)
		}
	}
	return out
}

// newTestToolBox returns a ToolBox, as NewToolBox makes it, that shows tool
// as flow does, in the formats of BuiltinExtensions.
func newTestToolBox(t *testing.T, tool message.ToolPart, theme *style.Theme, fg style.Color) *ToolBox {
	t.Helper()
	return NewToolBox(tool, theme, fg, toolFormats(t)...)
}

// NewInlineTestUI returns an inline UI without an input box, as
// newInlineTestUI does, that shows the widgets of exts after those of
// BuiltinExtensions.
func NewInlineTestUI(t *testing.T, exts ...Extension) *UI {
	t.Helper()
	ui := newInlineTestUI(t)
	ui.extensions = append(ui.extensions, exts...)
	ui.service.Commands = CommandsWith(ui.extensions)
	return ui
}

// NewInputTestUI returns an inline UI with an input box, as newInputTestUI
// does, that shows the widgets of exts after those of BuiltinExtensions.
func NewInputTestUI(t *testing.T, exts ...Extension) *UI {
	t.Helper()
	ui := newInputTestUI(t)
	ui.extensions = append(ui.extensions, exts...)
	ui.service.Commands = CommandsWith(ui.extensions)
	return ui
}

// AssertTranscriptFrames draws the transcript of ui w cells wide and h rows
// tall, and checks it against the golden frames name, as
// TestTranscriptFrames does.
func AssertTranscriptFrames(t *testing.T, ui *UI, name string, w, h int) {
	t.Helper()
	testutil.AssertGoldenFrames(t, name, renderFrames(w, h, detachTranscript(ui)...), surfaceNames(ui))
}

// DrawFullscreen draws the scrolling transcript of ui, built with an input
// box, w cells wide and h rows tall, scrolled scrollY rows down from its top.
// The rows keep the sticky offsets that scrolling gives them.
func DrawFullscreen(ui *UI, w, h, scrollY int) *buffer.Buffer {
	ui.renderer.Root().Remove(ui.messageBox.ID())
	root := terminal.NewRootRenderable(w, h)
	root.Add(ui.messageBox)
	// The first frame lays the transcript out, which scrolling needs to know
	// how far it can go and where each row lies.
	root.Render(buffer.New(w, h), 0)
	ui.messageBox.SetScrollY(scrollY)
	buf := buffer.New(w, h)
	root.Render(buf, 0)
	return buf
}

// AssertFullscreenFrames draws the scrolling transcript of ui as
// DrawFullscreen does, and checks it against the golden frames name, as
// TestFullscreenFrames does.
func AssertFullscreenFrames(t *testing.T, ui *UI, name string, w, h, scrollY int) {
	t.Helper()
	testutil.AssertGoldenFrames(t, name, DrawFullscreen(ui, w, h, scrollY), surfaceNames(ui))
}

// AssertIndicatorFrames draws the transcript of ui, built with an input box,
// and under it the indicator of a run in progress, w cells wide and h rows
// tall, and checks them against the golden frames name, as
// TestIndicatorFrames does.
func AssertIndicatorFrames(t *testing.T, ui *UI, name string, w, h int) {
	t.Helper()
	ui.service.state.Working = true
	ui.ShowIndicator(WorkingIndicator(2*time.Second, false))
	rows := slices.Concat(detachTranscript(ui), []*widget.Box{detachFromRoot(ui, ui.indicatorBox)})
	testutil.AssertGoldenFrames(t, name, renderFrames(w, h, rows...), surfaceNames(ui))
}

// Service returns the controller of ui.
func (ui *UI) Service() *Service { return ui.service }

// Bubble returns the transcript's bubble that shows message id, or nil.
func (ui *UI) Bubble(id message.ID) *ChatMessageBox { return ui.messageWidgets[id] }

// The helpers that build the transcripts of the tests.
var (
	UserText       = userText
	AgentParts     = agentParts
	AgentText      = agentText
	StreamToolTurn = streamToolTurn
	ExpandTools    = expandTools
	ToolTurnBase   = toolTurnBase
	ToolTurnTree   = toolTurnTree
)
