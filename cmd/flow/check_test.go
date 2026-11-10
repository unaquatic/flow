package main

import (
	"errors"
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
	"github.com/unaquatic/flow/internal/cmdline"
)

// checkAccount is the account of testProvider that newCheckTestApp's
// auth.json may hold.
const checkAccount agent.AccountID = "0192f3c4-5a6b-7c8d-9e0f-1a2b3c4d5e20"

// newCheckTestApp returns an app as newTestApp does, whose config file sets
// settings and whose auth.json holds auth, unless it is empty.
func newCheckTestApp(t *testing.T, settings, auth string) *app.App {
	t.Helper()
	t.Chdir(t.TempDir())
	t.Setenv(keyEnv, "")
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, config.FileName), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	if auth != "" {
		if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(auth), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	gatewaytest.WriteCache(t, home, protocoltest.FromAPI(t,
		`{"provider-a":{"id":"provider-a","format":"responses","env":["`+keyEnv+`"],"models":{}}}`))
	a, err := app.New(t.Context(), config.NewService(home), app.Options{Extensions: []extension.Load{protocol.Load}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}

// checkSettings is a config file with no catalog server, and testProvider's
// model as the default.
const checkSettings = `{"snapshot":false,"catalog_source":"http://127.0.0.1:1","default_provider":"provider-a","default_model":"model-a"}`

// TestAccountCheck checks what flow account check writes, and how it ends:
// not ready with exit status 1 and a hint on stderr for a provider with no
// account, ready for one with an API key, the default model's provider
// when it names none, and its finding as JSON.
func TestAccountCheck(t *testing.T) {
	a := newCheckTestApp(t, checkSettings, "")
	steps := captureStderr(t)
	text := AccountOptions{Format: OutputText}

	var out strings.Builder
	err := runAccountCheck(t.Context(), &out, a, text, nil)
	if ee, ok := errors.AsType[*cmdline.ExitError](err); !ok || ee.Code != 1 {
		t.Errorf("check with no account: err = %v, want exit status 1", err)
	}
	if out.String() != "not_ready\n" || !strings.Contains(steps.String(), "flow login --provider provider-a") {
		t.Errorf("check with no account = %q, stderr %q; want not_ready and how to log in", out.String(), steps.String())
	}

	id, err := a.Accounts().SetKey(t.Context(), testProvider, "", agent.APIKey("sk-test"))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {string(testProvider)}} {
		out.Reset()
		if err := runAccountCheck(t.Context(), &out, a, text, args); err != nil || out.String() != "ready\n" {
			t.Errorf("check %q = %q, %v; want ready", args, out.String(), err)
		}
	}

	out.Reset()
	if err := runAccountCheck(t.Context(), &out, a, AccountOptions{Format: OutputJSON}, nil); err != nil {
		t.Fatal(err)
	}
	want := app.AuthCheck{Provider: testProvider, Status: app.CheckReady, Account: id}
	if got := decodeJSON[app.AuthCheck](t, out.String()); got != want {
		t.Errorf("check as JSON = %+v, want %+v", got, want)
	}
}

// TestAccountCheckInvalid checks that an active account whose OAuth token
// has expired, and cannot be refreshed, is invalid, with exit status 1, and
// why on stderr.
func TestAccountCheckInvalid(t *testing.T) {
	auth := `{"provider-a":{"active":"` + string(checkAccount) + `","accounts":[{"id":"` + string(checkAccount) +
		`","type":"oauth","access_token":"old","refresh_token":"r","expires_at":1}]}}`
	a := newCheckTestApp(t, checkSettings, auth)
	steps := captureStderr(t)
	var out strings.Builder
	err := runAccountCheck(t.Context(), &out, a, AccountOptions{Format: OutputText}, nil)
	if ee, ok := errors.AsType[*cmdline.ExitError](err); !ok || ee.Code != 1 {
		t.Errorf("check of an expired token: err = %v, want exit status 1", err)
	}
	if out.String() != "invalid\n" || steps.Len() == 0 {
		t.Errorf("check of an expired token = %q, stderr %q; want invalid and why", out.String(), steps.String())
	}
}

// TestAccountCheckNoProvider checks that flow account check fails when it
// names no provider and the config file sets no default model.
func TestAccountCheckNoProvider(t *testing.T) {
	a := newCheckTestApp(t, `{"snapshot":false,"catalog_source":"http://127.0.0.1:1"}`, "")
	if err := runAccountCheck(t.Context(), new(strings.Builder), a, AccountOptions{Format: OutputText}, nil); !errors.Is(err, errNoCheckProvider) {
		t.Errorf("check with no default model: err = %v, want %v", err, errNoCheckProvider)
	}
}
