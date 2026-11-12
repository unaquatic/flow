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

// TestSlashCommands checks that the extension adds /skills alone, and no
// command of each skill, which the UI finds by that name.
func TestSlashCommands(t *testing.T) {
	cmds := New(review).SlashCommands()
	if len(cmds) != 1 || cmds[0].Name.String() != "/skills" {
		t.Fatalf("commands = %+v, want /skills alone", cmds)
	}
	all := ui.CommandsWith([]ui.Extension{New(review)})
	if _, ok := ui.LookupCommand(all, Command); !ok {
		t.Error("the UI does not find /skills among its commands")
	}
	if _, ok := ui.LookupCommand(all, "skill:review"); ok {
		t.Error("the UI has a command of the skill review, want /skills alone")
	}
}

// TestMenuItems checks that /skills lists each skill by its name and the
// first paragraph of its description on one line; that choosing one closes
// the list and fills the input with its instructions, without the
// frontmatter, and a blank line for what to ask, sending nothing; and that
// choosing one whose file is gone says so, and leaves the input as it is.
func TestMenuItems(t *testing.T) {
	s := newTestService(t)
	path := filepath.Join(t.TempDir(), "review", skills.File)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: review\ndescription: Review code.\n---\n\n# Review\n\nRead scripts/check.sh.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	found := review
	found.Path = path
	gone := skills.Skill{Name: "gone", Description: "Gone.", Path: filepath.Join(t.TempDir(), "gone", skills.File)}
	items := New(found, gone).MenuItems(s)
	if len(items) != 2 || items[0].Name != "review" || items[0].Desc != "Review code for bugs." || items[1].Name != "gone" {
		t.Fatalf("rows = %+v, want review, described on one line, then gone", items)
	}

	events := items[0].Choose()
	want := ui.EventInputSet{Text: "# Review\n\nRead scripts/check.sh.\n\n"}
	if !slices.Contains(events, ui.UIEvent(want)) {
		t.Errorf("choosing review = %#v, want the input set to %q", events, want.Text)
	}
	if slices.ContainsFunc(events, func(e ui.UIEvent) bool { _, ok := e.(ui.EventQueueChanged); return ok }) {
		t.Errorf("choosing review = %#v, want nothing sent", events)
	}

	if events := items[1].Choose(); slices.ContainsFunc(events, func(e ui.UIEvent) bool { _, ok := e.(ui.EventInputSet); return ok }) {
		t.Errorf("choosing a skill whose file is gone = %#v, want the input left as it is", events)
	}
}

// TestShowMenuWithoutSkills checks that /skills with no skill found says so,
// and opens no list.
func TestShowMenuWithoutSkills(t *testing.T) {
	events := New().ShowMenu(newTestService(t))
	if len(events) == 0 || slices.ContainsFunc(events, func(e ui.UIEvent) bool { _, ok := e.(ui.EventBannerShow); return ok }) {
		t.Errorf("/skills without skills = %#v, want a toast and no list", events)
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
