package main

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/app/server"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/product"
)

// decodeJSON decodes out, what a command wrote in OutputJSON, into a value of
// type T, or fails t.
func decodeJSON[T any](t *testing.T, out string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("JSON = %q: %v", out, err)
	}
	return v
}

// captureStderr makes stderr a builder for the rest of the test, and
// returns it.
func captureStderr(t *testing.T) *strings.Builder {
	t.Helper()
	prev := stderr
	steps := new(strings.Builder)
	stderr = steps
	t.Cleanup(func() { stderr = prev })
	return steps
}

// TestAccountCommandsJSON checks what the account commands write in
// OutputJSON: no active account as an empty list, the providers' accounts
// as GET /auth lists them, and the account that account current, account
// use and logout name, each with its provider.
func TestAccountCommandsJSON(t *testing.T) {
	a := newTestApp(t)
	jsonOpts := AccountOptions{Format: OutputJSON}
	var out strings.Builder
	if err := runAccountCurrent(t.Context(), &out, a, jsonOpts, nil); err != nil || out.String() != "[]\n" {
		t.Errorf("account current with none active = %q, %v; want []", out.String(), err)
	}

	id, err := a.Accounts().SetKey(t.Context(), testProvider, "", agent.APIKey("sk-test"))
	if err != nil {
		t.Fatal(err)
	}
	want := AccountRef{Provider: testProvider, Account: server.AccountStatus{ID: id, Kind: agent.CredentialAPIKey, Source: agent.CredentialSourceStore}}

	out.Reset()
	if err := listAccounts(t.Context(), &out, a, jsonOpts); err != nil {
		t.Fatal(err)
	}
	list := decodeJSON[[]server.AuthStatus](t, out.String())
	if len(list) != 1 || list[0].Provider != testProvider || list[0].Active != id ||
		len(list[0].Accounts) != 1 || list[0].Accounts[0] != want.Account {
		t.Errorf("account list = %+v, want %s active at %s", list, id, testProvider)
	}

	for name, run := range map[string]func() error{
		"account current": func() error { return runAccountCurrent(t.Context(), &out, a, jsonOpts, nil) },
		"account use": func() error {
			return runAccountUse(t.Context(), &out, a, jsonOpts, []string{string(testProvider), string(id)})
		},
	} {
		out.Reset()
		if err := run(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := out.String()
		if name == "account current" {
			if refs := decodeJSON[[]AccountRef](t, got); len(refs) != 1 || refs[0] != want {
				t.Errorf("%s = %+v, want [%+v]", name, refs, want)
			}
			continue
		}
		if ref := decodeJSON[AccountRef](t, got); ref != want {
			t.Errorf("%s = %+v, want %+v", name, ref, want)
		}
	}

	out.Reset()
	if err := runLogout(t.Context(), &out, a, logoutOptions{provider: testProvider, account: id, format: OutputJSON}, nil); err != nil {
		t.Fatal(err)
	}
	if ref := decodeJSON[AccountRef](t, out.String()); ref != want {
		t.Errorf("logout = %+v, want %+v", ref, want)
	}
}

// TestLoginKeyJSON checks that a login with an API key in OutputJSON asks
// for the key on stderr, and writes the account it saved, alone, to its
// output.
func TestLoginKeyJSON(t *testing.T) {
	a := newTestApp(t)
	steps := captureStderr(t)

	var out strings.Builder
	o := loginOptions{method: keyMethod, provider: testProvider, format: OutputJSON}
	if err := login(t.Context(), &out, strings.NewReader("sk-test\n"), a, o); err != nil {
		t.Fatal(err)
	}
	if got := steps.String(); got != "Enter the API key: " {
		t.Errorf("stderr = %q, want the question alone", got)
	}
	ref := decodeJSON[AccountRef](t, out.String())
	st, err := a.Accounts().Status(t.Context(), testProvider)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Provider != testProvider || ref.Account.ID != st.Active || ref.Account.Kind != agent.CredentialAPIKey {
		t.Errorf("login = %+v, want the API key account %s", ref, st.Active)
	}
}

// TestVersionJSON checks that flow version writes its name and version in
// OutputJSON.
func TestVersionJSON(t *testing.T) {
	var out strings.Builder
	if err := writeVersionJSON(&out); err != nil {
		t.Fatal(err)
	}
	if got := decodeJSON[VersionInfo](t, out.String()); got != (VersionInfo{Name: product.Name, Version: product.Version()}) {
		t.Errorf("version = %+v", got)
	}
}

// TestServeJSON checks that flow serve writes where it listens in
// OutputJSON, as one line, and its warning about a server that other
// machines reach with no password to stderr, not to its output.
func TestServeJSON(t *testing.T) {
	t.Setenv(config.EnvServerPassword, "")
	a := newTestApp(t)
	steps := captureStderr(t)

	for _, tt := range []struct {
		hostname, url string
		warns         bool
	}{
		{hostname: "", url: "http://127.0.0.1:4096"},
		{hostname: "0.0.0.0", url: "http://0.0.0.0:4096", warns: true},
	} {
		steps.Reset()
		var out strings.Builder
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		listen := func(_, address string) (net.Listener, error) {
			addr, err := net.ResolveTCPAddr("tcp", address)
			if err != nil {
				return nil, err
			}
			return &demoListener{addr: addr, closed: make(chan struct{})}, nil
		}
		o := serveOptions{hostname: tt.hostname, port: 4096, format: OutputJSON}
		if err := serve(ctx, &out, a, o, listen); err != nil {
			t.Fatal(err)
		}
		if got, want := out.String(), `{"url":"`+tt.url+`"}`+"\n"; got != want {
			t.Errorf("serve on %q = %q, want %q", tt.hostname, got, want)
		}
		if warned := strings.HasPrefix(steps.String(), "warning: "); warned != tt.warns {
			t.Errorf("serve on %q: stderr = %q, want a warning %v", tt.hostname, steps.String(), tt.warns)
		}
	}
}
