package ui

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/unaquatic/catalog/gateway/gatewaytest"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/extension/demo"
	"github.com/unaquatic/flow/extension/protocol"
	"github.com/unaquatic/flow/extension/protocol/protocoltest"
	"github.com/unaquatic/flow/terminal/ui"
)

// newTestService returns a UI service over an app with a new data
// directory, snapshots off, no catalog server and a catalog of one provider,
// provider-a, in a new working directory. The test ends by closing the app.
func newTestService(t *testing.T) *ui.Service {
	t.Helper()
	t.Chdir(t.TempDir())
	home := t.TempDir()
	settings := `{"default_model":"model","default_provider":"provider-a","snapshot":false,"catalog_source":"http://127.0.0.1:1"}`
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

// TestErrorItems checks that each row of the error demo shows its error
// where flow shows one of its kind, all as the indicator reads: a provider's
// response headed by its provider and status over the body, JSON or not; an
// error without a status or a body as its message; a failed login headed by
// the login and the status over the body; and an error under a reply, which
// ui.ErrorView shows as the indicator does.
func TestErrorItems(t *testing.T) {
	indented := func(body string) string {
		var buf bytes.Buffer
		if err := json.Indent(&buf, []byte(body), "", "  "); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	indicator := func(want ui.EventIndicatorShow) func(*testing.T, []ui.UIEvent) {
		return func(t *testing.T, events []ui.UIEvent) {
			if !slices.Contains(events, ui.UIEvent(want)) {
				t.Errorf("events = %#v, want %#v", events, want)
			}
		}
	}
	tests := []struct {
		name  string
		check func(*testing.T, []ui.UIEvent)
	}{
		{name: "Response JSON", check: indicator(ui.EventIndicatorShow{
			Text: "provider-a response error (401)", Error: true, Detail: indented(demo.KeyExpiredBody), JSON: true,
		})},
		{name: "Response HTML", check: indicator(ui.EventIndicatorShow{
			Text: "provider-a response error (502)", Error: true, Detail: strings.TrimSpace(demo.BadGatewayBody),
		})},
		{name: "Stream error", check: indicator(ui.EventIndicatorShow{
			Text: "provider-a response error: Overloaded", Error: true,
		})},
		{name: "Request failed", check: indicator(ui.EventIndicatorShow{
			Text: `request failed: Post "` + demo.APIURL + `": EOF`, Error: true,
		})},
		{name: "Login failed", check: indicator(ui.EventIndicatorShow{
			Text:  "login to provider-a account demo failed: token endpoint error (401)",
			Error: true, Detail: indented(demo.TokenErrorBody), JSON: true,
		})},
		{name: "Message error", check: func(t *testing.T, events []ui.UIEvent) {
			for _, ev := range events {
				updated, ok := ev.(ui.EventMessageUpdated)
				if !ok || updated.Msg.Error == nil {
					continue
				}
				want := ui.EventIndicatorShow{Text: "provider-a response error (401)", Error: true, Detail: indented(demo.KeyExpiredBody), JSON: true}
				if got := ui.ErrorIndicator(app.ErrorOf(updated.Msg.Error)); got != want {
					t.Errorf("ErrorIndicator = %#v, want %#v", got, want)
				}
				if !slices.Contains(updated.Msg.Parts, message.ContentPart(message.TextPart{ID: updated.Msg.Parts[0].PartID(), Text: MessageErrorText})) {
					t.Errorf("parts = %#v, want the text %q", updated.Msg.Parts, MessageErrorText)
				}
				return
			}
			t.Errorf("events = %#v, want a reply updated with its error", events)
		}},
	}
	items := ErrorItems(newTestService(t))
	names := make([]string, 0, len(items))
	for _, it := range items {
		names = append(names, it.Name)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			i := slices.Index(names, tt.name)
			if i < 0 {
				t.Fatalf("items = %v, want %q", names, tt.name)
			}
			tt.check(t, items[i].Choose())
		})
	}
	if len(items) != len(tests) {
		t.Errorf("items = %v, want %d", names, len(tests))
	}
}

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

// TestShowUpdated checks that the update demo adds to the transcript the
// notice of the first start of UpdateVersion.
func TestShowUpdated(t *testing.T) {
	var got []string
	for _, ev := range ShowUpdated(newTestService(t)) {
		if added, ok := ev.(ui.EventMessageAdded); ok && added.Msg.Notice != nil {
			for _, line := range added.Msg.Notice.Lines {
				got = append(got, line.PlainText())
			}
		}
	}
	if want := []string{"Updated to v1.2.3"}; !slices.Equal(got, want) {
		t.Errorf("notice = %q, want %q", got, want)
	}
}
