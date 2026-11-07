package ui

import (
	"slices"
	"testing"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/terminal/ui"
)

// TestSlashCommands checks that the demo adds /demo to the menu, after the
// UI's own commands and before /quit, which still ends it.
func TestSlashCommands(t *testing.T) {
	cmds := ui.CommandsWith([]ui.Extension{New()})
	names := make([]ui.Command, 0, len(cmds))
	for _, c := range cmds {
		names = append(names, c.Name)
	}
	if len(cmds) != len(ui.SlashCommands)+1 || names[len(names)-1] != ui.CommandQuit || names[len(names)-2] != Command {
		t.Errorf("commands = %v, want the UI's own with /demo before /quit", names)
	}
	if got, ok := ui.LookupCommand(cmds, Command); !ok || got.Desc == "" {
		t.Errorf("LookupCommand(demo) = %+v, %v; want /demo", got, ok)
	}
	if slices.Contains(names[:len(ui.SlashCommands)-1], Command) {
		t.Error("/demo is among the UI's own commands")
	}
}

// TestFormatTool checks that the demo labels the call of webfetch, the tool
// it calls to show one that is not found, and leaves other calls alone.
func TestFormatTool(t *testing.T) {
	if view, ok := New().FormatTool(message.ToolPart{Name: "webfetch"}); !ok || view.Label != "Web Fetch" {
		t.Errorf("FormatTool(webfetch) = %+v, %v; want the label Web Fetch", view, ok)
	}
	if view, ok := New().FormatTool(message.ToolPart{Name: "bash"}); ok {
		t.Errorf("FormatTool(bash) = %+v, want it left to the box", view)
	}
}
