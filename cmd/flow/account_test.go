package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unaquatic/catalog/gateway/gatewaytest"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/extension/protocol"
	"github.com/unaquatic/flow/extension/protocol/protocoltest"
)

// testProvider is the provider of the test app's catalog, and keyEnv the
// environment variable of its API key.
const (
	testProvider agent.ProviderID = "provider-a"
	keyEnv                        = "PROVIDER_A_API_KEY"
)

// TestAccountCurrentNone checks that account current reports no active
// account: a hint for every provider, and an error for one it names.
func TestAccountCurrentNone(t *testing.T) {
	a := newTestApp(t)
	var out strings.Builder
	if err := runAccountCurrent(t.Context(), &out, a, AccountOptions{Format: OutputText}, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "Current account:\n  [no active accounts]\n"; got != want {
		t.Errorf("account current = %q, want %q", got, want)
	}
	err := runAccountCurrent(t.Context(), &out, a, AccountOptions{Format: OutputText}, []string{"provider-a"})
	if err == nil || !strings.Contains(err.Error(), "no active account: provider-a") {
		t.Errorf("account current provider-a: err = %v, want no active account", err)
	}
}

// newTestApp returns an app over a new data directory, with snapshots off,
// no catalog server, a catalog of testProvider alone and no API key in the
// environment. The test ends by closing it.
func newTestApp(t *testing.T) *app.App {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv(keyEnv, "")
	home := t.TempDir()
	settings := `{"snapshot":false,"catalog_source":"http://127.0.0.1:1"}`
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	gatewaytest.WriteCache(t, home, protocoltest.FromAPI(t,
		`{"provider-a":{"id":"provider-a","format":"responses","env":["`+keyEnv+`"],"models":{}}}`))
	conf := config.NewService(home)
	a, err := app.New(t.Context(), conf, app.Options{Extensions: []extension.Load{protocol.Load}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}

// TestAccountCommands checks what the account commands write: the account
// list marks the active account, account use shows the row of the account
// it switches to, account current shows the active account alone, and
// logout the account it removes.
func TestAccountCommands(t *testing.T) {
	a := newTestApp(t)
	id, err := a.Accounts().SetKey(t.Context(), testProvider, "", agent.APIKey("sk-test"))
	if err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := listAccounts(t.Context(), &out, a, AccountOptions{Format: OutputText}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "provider-a\n* "+string(id)) {
		t.Errorf("account list =\n%s\nwant the active provider-a account %s", out.String(), id)
	}

	out.Reset()
	if err := runAccountUse(t.Context(), &out, a, AccountOptions{Format: OutputText}, []string{"provider-a", string(id)}); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "Switched to:\n  provider-a  "+string(id)+"  api_key  store\n"; got != want {
		t.Errorf("account use = %q, want %q", got, want)
	}

	// A second account leaves the first active; current shows that one alone.
	other, err := a.Accounts().SetKey(t.Context(), testProvider, "", agent.APIKey("sk-other"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Accounts().Use(t.Context(), testProvider, id); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {"provider-a"}} {
		out.Reset()
		if err := runAccountCurrent(t.Context(), &out, a, AccountOptions{Format: OutputText}, args); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); !strings.HasPrefix(got, "Current account:\n  provider-a  "+string(id)+"  ") || strings.Count(got, "\n") != 2 || strings.Contains(got, string(other)) {
			t.Errorf("account current %q =\n%s\nwant the active account %s alone", args, got, id)
		}
	}
	if _, err := a.Accounts().Logout(t.Context(), testProvider, other); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := runLogout(t.Context(), &out, a, logoutOptions{provider: testProvider, account: id}, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "Removed:\n  provider-a  "+string(id)+"  api_key  store\n"; got != want {
		t.Errorf("logout = %q, want %q", got, want)
	}
}

// TestAccountCurrentPlural checks that account current heads the active
// accounts of several providers as accounts, a row each in provider order.
func TestAccountCurrentPlural(t *testing.T) {
	a, closeApp, err := newDemoApp(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer closeApp()
	id, err := a.Accounts().SetKey(t.Context(), demoKeyProvider, "", demoKey)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := runAccountCurrent(t.Context(), &out, a, AccountOptions{Format: OutputText}, nil); err != nil {
		t.Fatal(err)
	}
	want := "Current accounts:\n" +
		"  provider-a  " + string(demoAccount) + "  oauth    store\n" +
		"  provider-b  " + string(id) + "  api_key  store\n"
	if got := out.String(); got != want {
		t.Errorf("account current =\n%s\nwant\n%s", got, want)
	}
}
