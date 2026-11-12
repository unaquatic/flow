package ui

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/unaquatic/catalog/gateway/gatewaytest"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/extension/protocol"
	"github.com/unaquatic/flow/extension/protocol/protocoltest"
	"github.com/unaquatic/flow/extension/skills"
	"github.com/unaquatic/flow/terminal/ui"
)

// review is a skill whose description runs over lines and paragraphs.
var review = skills.Skill{
	Name:        "review",
	Description: "Review code  for\nbugs.\n\nUse when asked to review.",
	Path:        "/home/u/.agents/skills/review/SKILL.md",
}

// TestSlashCommands checks that each skill has a command named as the prompt
// that invokes it, described by the first paragraph of its description on
// one line, which the UI finds by that name.
func TestSlashCommands(t *testing.T) {
	cmds := New(review).SlashCommands()
	if len(cmds) != 1 {
		t.Fatalf("commands = %+v, want one", cmds)
	}
	if c := cmds[0]; c.Name != "skill:review" || c.Name.String() != "/skill:review" || c.Desc != "Review code for bugs." {
		t.Errorf("command = %q %q, want /skill:review described on one line", c.Name, c.Desc)
	}
	if _, ok := ui.LookupCommand(ui.CommandsWith([]ui.Extension{New(review)}), "skill:review"); !ok {
		t.Error("the UI does not find /skill:review among its commands")
	}
}

// TestCommandRun checks that a skill's command sends the prompt that invokes
// the skill, through the queue as a typed prompt is sent.
func TestCommandRun(t *testing.T) {
	events := Command(review).Run(newTestService(t))
	want := ui.EventQueueChanged{Prompts: []string{"/skill:review"}}
	if !slices.ContainsFunc(events, func(e ui.UIEvent) bool {
		q, ok := e.(ui.EventQueueChanged)
		return ok && slices.Equal(q.Prompts, want.Prompts)
	}) {
		t.Errorf("events = %#v, want the prompt queued, %#v", events, want)
	}
}

// newTestService returns a UI service over an app with a new data
// directory, no catalog server and a catalog of one provider, provider-a, in
// a new working directory. The test ends by closing the app.
func newTestService(t *testing.T) *ui.Service {
	t.Helper()
	t.Chdir(t.TempDir())
	home := t.TempDir()
	settings := `{"default_model":"model","default_provider":"provider-a","catalog_source":"http://127.0.0.1:1"}`
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	gatewaytest.WriteCache(t, home, protocoltest.FromAPI(t, `{"provider-a":{"id":"provider-a","format":"responses","models":{}}}`))
	a, err := app.New(t.Context(), config.NewService(home), app.Options{Extensions: []extension.Load{protocol.Load}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return ui.NewService(t.Context(), a, &ui.UIState{})
}
