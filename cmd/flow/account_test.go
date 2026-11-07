package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/extension/protocol"
	"github.com/unaquatic/flow/extension/protocol/login/chatgpt"
	"github.com/unaquatic/flow/internal/cmdline"
)

// newTestApp returns an app over a new data directory, with snapshots off,
// no catalog server and no API keys in the environment. The test ends by
// closing it.
func newTestApp(t *testing.T) *app.App {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	home := t.TempDir()
	settings := `{"snapshot":false,"catalog_source":"http://127.0.0.1:1"}`
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	conf := config.NewService(home)
	a, err := app.New(t.Context(), conf, app.Options{Extensions: []extension.Load{protocol.Load}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}

// TestAccountCommands checks what the account commands write: the account
// list marks the active account, account use names the account it makes
// active, by the provider's alias too, and logout the account it removes.
func TestAccountCommands(t *testing.T) {
	a := newTestApp(t)
	id, err := a.Accounts().SetKey(t.Context(), chatgpt.ProviderID, "", agent.APIKey("sk-test"))
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := listAccounts(t.Context(), &out, a); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "openai\n * "+string(id)) {
		t.Errorf("account list =\n%s\nwant the active openai account %s", out.String(), id)
	}

	out.Reset()
	if err := runAccountUse(t.Context(), &out, a, cmdline.NoOptions{}, []string{"openai", string(id)}); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "Active openai account: "+string(id)+".\n"; got != want {
		t.Errorf("account use = %q, want %q", got, want)
	}

	out.Reset()
	if err := runLogout(t.Context(), &out, a, logoutOptions{provider: chatgpt.ProviderID, account: id}, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "Removed openai account "+string(id)+".\n"; got != want {
		t.Errorf("logout = %q, want %q", got, want)
	}
}
