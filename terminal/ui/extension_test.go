package ui

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/widget"
)

// notePart is a part an extension defines, which testExtension shows.
type notePart struct {
	ID   message.PartID
	Text string
}

func (notePart) PartType() message.ContentType { return "test_note" }
func (p notePart) PartID() message.PartID      { return p.ID }

// metaPart is metadata that no extension shows.
type metaPart struct{ ID message.PartID }

func (metaPart) PartType() message.ContentType { return "test_meta" }
func (p metaPart) PartID() message.PartID      { return p.ID }
func (metaPart) Metadata()                     {}

// noteView shows a notePart, and records the parts and styles it was given.
type noteView struct {
	*widget.Text
	Parts  []notePart
	Styles []ViewStyle
}

func (v *noteView) SetPart(part message.ContentPart) {
	if n, ok := part.(notePart); ok {
		v.Parts = append(v.Parts, n)
		v.SetContent(n.Text)
	}
}

func (v *noteView) SetViewStyle(st ViewStyle) { v.Styles = append(v.Styles, st) }

// blockView is a hidden view under each message, in a PartBox that Ctrl+O
// expands, as the changes of a step are, and records the messages it was
// given.
type blockView struct {
	*PartBox
	Msgs []ChatMessage
}

func (v *blockView) SetMessage(msg ChatMessage) { v.Msgs = append(v.Msgs, msg) }

// testExtension shows notePart with a noteView, and a blockView under every
// message.
type testExtension struct{}

func (testExtension) NewPartView(part message.ContentPart, host ViewHost) PartView {
	n, ok := part.(notePart)
	if !ok {
		return nil
	}
	v := &noteView{Text: widget.NewText("")}
	v.SetPart(n)
	v.SetViewStyle(host.Style)
	return v
}

func (testExtension) NewSegmentView(msg ChatMessage, host ViewHost) SegmentView {
	v := &blockView{PartBox: NewPartBox(host.Theme, host.Fg, themeOrDefault(host.Theme).ResolveSurfaceRaised())}
	v.Shape, v.Marked = ShapeBlock, false
	v.SetGone(true)
	v.SetMessage(msg)
	return v
}

// newExtensionTestUI returns an inline test UI that shows the widgets of
// testExtension.
func newExtensionTestUI(t *testing.T) *UI {
	t.Helper()
	ui := newInlineTestUI(t)
	ui.extensions = []Extension{testExtension{}}
	return ui
}

// TestExtension_PartView checks that a part an extension shows gets the
// extension's view, in the transcript's style, which follows the parts and
// the style /settings sets.
func TestExtension_PartView(t *testing.T) {
	ui := newExtensionTestUI(t)
	ui.AddMessage(agentParts("a1", message.TextPart{ID: "a1-text", Text: "hi"}, notePart{ID: "a1-note", Text: "one"}))
	view, ok := ui.messageWidgets["a1"].SegmentFor("a1").Parts["a1-note"].(*noteView)
	if !ok {
		t.Fatalf("the note shows as %T, want its noteView", ui.messageWidgets["a1"].SegmentFor("a1").Parts["a1-note"])
	}
	ui.UpdateMessage(agentParts("a1", message.TextPart{ID: "a1-text", Text: "hi"}, notePart{ID: "a1-note", Text: "two"}))
	if got := view.Parts; len(got) != 2 || got[1].Text != "two" {
		t.Errorf("view was given %+v, want the note and then its update", got)
	}
	ui.Apply(ui.service.SetDebug(true))
	ui.Apply(ui.service.SetToolMarkers(true))
	last := view.Styles[len(view.Styles)-1]
	if !last.Debug || !last.ToolMarkers || last.NameFg != ToolNameGray.Fg() {
		t.Errorf("view style = %+v, want debug and markers on, in the tool name color", last)
	}
}

// TestExtension_MetadataHidden checks that metadata no extension shows keeps
// its place among the parts, unseen, while a part of an unknown type that is
// not metadata shows as Markdown.
func TestExtension_MetadataHidden(t *testing.T) {
	ui := newExtensionTestUI(t)
	ui.AddMessage(agentParts("a1", message.TextPart{ID: "a1-text", Text: "hi"}, metaPart{ID: "a1-meta"}))
	seg := ui.messageWidgets["a1"].SegmentFor("a1")
	meta, ok := seg.Parts["a1-meta"]
	if !ok || meta.Visible() {
		t.Errorf("metadata widget = %v (found %v), want it hidden", meta, ok)
	}
	if got := len(seg.ContentBox.ChildrenRaw()); got != 2 {
		t.Errorf("segment holds %d part widgets, want one per part", got)
	}
}

// TestExtension_SegmentView checks that a view an extension shows under a
// message is given the message as it streams, and that Ctrl+O expands it
// with the tool calls, from the start when they are expanded.
func TestExtension_SegmentView(t *testing.T) {
	ui := newExtensionTestUI(t)
	ui.AddMessage(agentText("a1", "hi"))
	seg := ui.messageWidgets["a1"].SegmentFor("a1")
	if len(seg.Views) != 1 {
		t.Fatalf("segment has %d views, want the extension's", len(seg.Views))
	}
	block := seg.Views[0].(*blockView)
	ui.UpdateMessage(agentText("a1", "hi there"))
	if got := len(block.Msgs); got != 2 || block.Msgs[1].Parts[0].(message.TextPart).Text != "hi there" {
		t.Errorf("view was given %d messages, want the message and then its update", got)
	}
	cm := ui.messageWidgets["a1"]
	if !slices.Contains(slices.Collect(cm.Boxes()), block.PartBox) {
		t.Error("Ctrl+O does not expand the view")
	}
	cm.SetToolsExpanded(true)
	ui.AddMessage(ChatMessage{ID: "a2", ParentID: "a1", Role: message.RoleAgent})
	if added := cm.SegmentFor("a2").Views[0].(*blockView); !added.Expanded {
		t.Error("a view added while the tool calls are expanded starts collapsed")
	}
}

// runFormatter is an extension that describes the calls of one tool, as
// view says.
type runFormatter struct {
	tool string
	view ToolView
}

func (runFormatter) NewPartView(message.ContentPart, ViewHost) PartView { return nil }

func (runFormatter) NewSegmentView(ChatMessage, ViewHost) SegmentView { return nil }

func (f runFormatter) FormatTool(call message.ToolPart) (ToolView, bool) {
	return f.view, call.Name == f.tool
}

// TestToolBox_ShowsTheFormattersView checks that a call shows as the first
// formatter that describes its tool says, in each part of its box, and that
// the call of a tool no formatter describes shows as any tool's.
func TestToolBox_ShowsTheFormattersView(t *testing.T) {
	run := runFormatter{tool: "run", view: ToolView{
		Label: "Run It", HeaderNote: "[fast]", ParamKey: "cmd", Param: "make test", Shell: true,
		Notes: []string{"[in ci]"}, Unnoted: []string{"secret"},
		BodyLang: "go", OutputInBody: true, Status: "[2 passed]",
	}}
	formats := []ToolFormatter{runFormatter{tool: "other"}, run, runFormatter{tool: "run", view: ToolView{Label: "Shadowed"}}}
	input := json.RawMessage(`{"path":"pkg","cmd":"make test","secret":"x","env":"ci"}`)
	box := NewToolBox(message.ToolPart{Name: "run", State: message.ToolStateCompleted{Input: input, Output: "package main\n"}},
		DefaultTheme(), style.ColorWhite, formats...)
	for part, tt := range map[string]struct{ got, want string }{
		"header": {box.Header.PlainText(), "Run It pkg [fast]"},
		"status": {box.Status.PlainText(), "[2 passed]"},
		"params": {box.Params.PlainText(), "$ make test [in ci] [path pkg] [env ci]"},
		"body":   {box.Body.Content(), "```go\npackage main\n```"},
		"output": {box.Output.Content(), ""},
	} {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", part, tt.got, tt.want)
		}
	}
	box.SetExpanded(true)
	if got, want := box.Header.PlainText(), "Run It"; got != want {
		t.Errorf("expanded header = %q, want the formatter's label %q", got, want)
	}

	other := NewToolBox(message.ToolPart{Name: "go_test", State: message.ToolStateCompleted{Input: input, Output: "ok\n"}},
		DefaultTheme(), style.ColorWhite, formats...)
	for part, tt := range map[string]struct{ got, want string }{
		"header": {other.Header.PlainText(), "Go Test pkg"},
		"status": {other.Status.PlainText(), ""},
		"params": {other.Params.PlainText(), "pkg [cmd make test] [secret x] [env ci]"},
		"body":   {other.Body.Content(), ""},
		"output": {other.Output.Content(), "```text\nok\n```"},
	} {
		if tt.got != tt.want {
			t.Errorf("unformatted %s = %q, want %q", part, tt.got, tt.want)
		}
	}
}

// TestExtension_FormatsToolCalls checks that the tool calls of the
// transcript show as the formatters among the UI's extensions describe them.
func TestExtension_FormatsToolCalls(t *testing.T) {
	ui := newInlineTestUI(t)
	ui.extensions = append(ui.extensions, runFormatter{tool: "run", view: ToolView{Label: "Run It"}})
	ui.AddMessage(agentParts("a1", message.ToolPart{ID: "a1-tool", CallID: "c1", Name: "run",
		State: message.ToolStatePending{Input: json.RawMessage(`{"path":"pkg"}`)}}))
	var headers []string
	for box := range ui.messageWidgets["a1"].ToolBoxes() {
		headers = append(headers, box.Header.PlainText())
	}
	if want := []string{"Run It pkg"}; !slices.Equal(headers, want) {
		t.Errorf("tool call headers = %q, want %q", headers, want)
	}
}
