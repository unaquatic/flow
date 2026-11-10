package main

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/terminal"
)

// TestRunDemoUpdate checks that flow demo update shows each case of flow
// update as the command prints it, an error as the command line reports
// it, and only the case it is given one.
func TestRunDemoUpdate(t *testing.T) {
	var w bytes.Buffer
	if err := runDemo(t.Context(), &w, false, updateDemos(), nil); err != nil {
		t.Fatal(err)
	}
	root := demoGlobalRoot
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	for _, want := range []string{
		"# Up to date\n$ flow update\nflow v1.2.3 is up to date.\n",
		"$ flow update --check\nflow v1.2.3 is available, and this is v1.2.2. Run flow update to install it.\n",
		"flow v1.2.2 is updating to v1.2.3 with npm install -g @unaquatic/flow@1.2.3\n\nchanged 1 package in 2s\nflow updated to v1.2.3.\n",
		"error: cannot update flow: " + root + " is not writable, run npm install -g @unaquatic/flow@1.2.3\n",
		"error: cannot update flow: /usr/local/bin/flow is not a global npm or pnpm install, run npm install -g @unaquatic/flow@latest\n",
		"# A development build\n$ flow update\nflow (devel) is a development build, run flow update --force to install v1.2.3.\n",
	} {
		if !strings.Contains(w.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, w.String())
		}
	}
	if n := strings.Count(w.String(), "\n# "); n != len(updateDemoCases)-1 {
		t.Errorf("output has %d cases after the first, want %d", n, len(updateDemoCases)-1)
	}

	w.Reset()
	if err := runDemo(t.Context(), &w, false, updateDemos(), []string{"latest"}); err != nil {
		t.Fatal(err)
	}
	if got, want := w.String(), "# Up to date\n$ flow update\nflow v1.2.3 is up to date.\n"; got != want {
		t.Errorf("latest = %q, want %q", got, want)
	}
}

// TestDemoArgs checks that a command of flow demo takes no case or one it
// knows.
func TestDemoArgs(t *testing.T) {
	cases := updateDemos()
	for _, args := range [][]string{nil, {"install"}} {
		if err := demoArgs(cases, args); err != nil {
			t.Errorf("demoArgs(%q) = %v", args, err)
		}
	}
	for _, args := range [][]string{{"nope"}, {"latest", "check"}} {
		if err := demoArgs(cases, args); err == nil {
			t.Errorf("demoArgs(%q) accepted", args)
		}
	}
}

// demoID matches an account ID that a demo login makes anew.
var demoID = `[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[0-9a-f]{4}-[0-9a-f]{12}`

// TestDemos checks what each command of flow demo shows, case by case: the
// output the command prints, on demo apps and listeners, and an
// error as the command line reports it.
func TestDemos(t *testing.T) {
	// The serve demos warn as the environment's password has it.
	t.Setenv(config.EnvServerPassword, "")
	banner := terminal.LogoBanner().PlainText()
	a, k := string(demoAccount), string(demoKeyAccount)
	for _, tt := range []struct {
		command string
		cases   []demoCase
		// want holds, for each case by name, the pattern its output, after
		// its comment and command line, matches in whole.
		want map[string]string
	}{
		{command: "version", cases: versionDemos, want: map[string]string{
			"version": regexp.QuoteMeta(banner+"\n\nflow ") + `.+\n`,
		}},
		{command: "login", cases: loginDemos, want: map[string]string{
			"browser":     regexp.QuoteMeta("open https://login.example/authorize in your browser\nLogged in to provider-a account ") + demoID + regexp.QuoteMeta("; it is now active.\n"),
			"device":      regexp.QuoteMeta("open https://login.example/device and enter code WDJB-MJHT\nLogged in to provider-a account " + a + "; it is now active.\n"),
			"key":         regexp.QuoteMeta("Enter the provider-b API key: "+demoKey+"\nSaved the API key as provider-b account ") + demoID + regexp.QuoteMeta("; it is now active.\n"),
			"no-login":    regexp.QuoteMeta("error: login to provider-b: unsupported operation\n"),
			"no-provider": regexp.QuoteMeta("error: " + errProviderRequired.Error() + "\n"),
		}},
		{command: "logout", cases: logoutDemos, want: map[string]string{
			"active":     regexp.QuoteMeta("Removed provider-a account " + a + ".\nActive provider-a account: " + k + ".\n"),
			"last":       regexp.QuoteMeta("Removed provider-a account " + a + ".\n"),
			"no-account": regexp.QuoteMeta("error: --account is required; list account IDs with flow account\n"),
		}},
		{command: "account", cases: accountDemos, want: map[string]string{
			"list":         regexp.QuoteMeta("provider-a\n * " + a + "  oauth    store\n   " + k + "  api_key  store\nprovider-b\n   (no accounts; run flow login)\n"),
			"current":      regexp.QuoteMeta("provider-a  " + a + "  oauth  store\n"),
			"current-none": regexp.QuoteMeta("error: provider-b has no active account; run flow login\n"),
			"use":          regexp.QuoteMeta("Active provider-a account: " + k + ".\n"),
			"use-invalid":  regexp.QuoteMeta(`error: invalid account id "123"` + "\n"),
		}},
		{command: "serve", cases: serveDemos, want: map[string]string{
			"local":   regexp.QuoteMeta("server listening on http://127.0.0.1:4096\n"),
			"exposed": regexp.QuoteMeta("warning: FLOW_SERVER_PASSWORD is not set, so anyone who reaches 0.0.0.0 can run commands on this machine\nserver listening on http://0.0.0.0:4096\n"),
		}},
	} {
		for _, c := range tt.cases {
			t.Run(tt.command+"/"+c.name, func(t *testing.T) {
				want, ok := tt.want[c.name]
				if !ok {
					t.Fatalf("no output given for case %q", c.name)
				}
				var w bytes.Buffer
				if err := runDemo(t.Context(), &w, false, tt.cases, []string{c.name}); err != nil {
					t.Fatal(err)
				}
				head := "# " + c.desc + "\n$ flow " + c.line + "\n"
				re := regexp.MustCompile(`\A` + regexp.QuoteMeta(head) + want + `\z`)
				if !re.MatchString(w.String()) {
					t.Errorf("output =\n%s\nwant it to match\n%s", w.String(), re)
				}
			})
		}
		if len(tt.want) != len(tt.cases) {
			t.Errorf("%s: %d outputs given for %d cases", tt.command, len(tt.want), len(tt.cases))
		}
	}
}
