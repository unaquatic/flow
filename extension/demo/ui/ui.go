// Package ui is the terminal side of the demo extension: the /demo command,
// which offers the demos in a menu, and the label of webfetch, the tool the
// demo calls to show a call of a tool that is not found.
package ui

import (
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension/demo"
	"github.com/unaquatic/flow/terminal/ui"
)

// Command is the slash command that offers the demos.
const Command ui.Command = "demo"

// StoppedToast is the toast /demo shows when it stops a run in progress.
const StoppedToast = "Stream stopped"

// Extension adds the demos to the terminal UI. It implements
// ui.CommandExtension and ui.ToolFormatter, and shows no parts or views of
// its own.
type Extension struct{}

var (
	_ ui.CommandExtension = (*Extension)(nil)
	_ ui.ToolFormatter    = (*Extension)(nil)
)

// New returns the demos.
func New() *Extension { return &Extension{} }

// NewPartView returns nil: the extension adds no parts.
func (*Extension) NewPartView(message.ContentPart, ui.ViewHost) ui.PartView { return nil }

// NewSegmentView returns nil: the extension shows nothing under a message.
func (*Extension) NewSegmentView(ui.ChatMessage, ui.ViewHost) ui.SegmentView { return nil }

// SlashCommands returns /demo, which runs as Run does.
func (*Extension) SlashCommands() []ui.SlashCommand {
	return []ui.SlashCommand{{Name: Command, Desc: "Run a demo", Run: Run}}
}

// FormatTool labels webfetch "Web Fetch", which its name does not read as
// once split, and leaves every other tool's call to the box.
func (*Extension) FormatTool(call message.ToolPart) (ui.ToolView, bool) {
	if call.Name == "webfetch" {
		return ui.ToolView{Label: "Web Fetch"}, true
	}
	return ui.ToolView{}, false
}

// Run stops the run in progress, as a second /demo stops the demo it
// started, or else offers the demos, as ShowMenu does.
func Run(s *ui.Service) []ui.UIEvent {
	if s.Streaming() {
		s.Stop()
		return s.ShowToast(StoppedToast)
	}
	return ShowMenu(s)
}

// ShowMenu offers the demos, searched from the input: a streamed demo
// session, a simulated request failure, the grapheme width report, and the
// 256-color palette.
func ShowMenu(s *ui.Service) []ui.UIEvent {
	item := func(name, desc string, run func() []ui.UIEvent) ui.MenuItem {
		return ui.MenuItem{Name: name, Desc: desc, Choose: s.Closing(run)}
	}
	items := []ui.MenuItem{
		item("Stream", "Run a streamed demo reply", func() []ui.UIEvent { return StartStream(s) }),
		item("Error", "Run a simulated network error", s.SimulateError),
		item("Grapheme", "Run a grapheme width check on this terminal", s.ShowGraphemeReport),
		item("Palette", "Run a demo of the xterm 256-color palette", s.ShowPalette),
	}
	return s.ShowSearchMenu(&ui.SearchMenu{Title: "Demos", Items: func() []ui.MenuItem { return items }})
}

// StartStream plays the demo session, as demo.Script gives it, stored and
// shown like any other run.
func StartStream(s *ui.Service) []ui.UIEvent {
	return s.StartScript("Demo", demo.Script())
}
