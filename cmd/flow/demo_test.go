package main

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/product"
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
		"# Up to date\n$ flow update\nAlready up to date: 1.2.3\n",
		"$ flow update --check\nNew update available: 1.2.2 → 1.2.3\n\nUpdate with:\n  flow update\n",
		"$ flow update\nUpdating 1.2.2 → 1.2.3\n  npm install -g @unaquatic/flow@1.2.3\n\nInstalled successfully\n",
		"for a flow the install script installed\n$ flow update\nUpdating 1.2.2 → 1.2.3\n\nInstalled successfully\n",
		"# A newer release, whose download breaks off, from the mirror too\n$ flow update\nUpdating 1.2.2 → 1.2.3\n\nerror: flow update: download " + demoReleaseURL + ": unexpected EOF; mirror: download " + demoMirrorURL + ": unexpected EOF\n",
		"error: flow update: not writable: " + root + "; run npm install -g @unaquatic/flow@1.2.3\n",
		"error: flow update: not installed by npm, pnpm or the install script: /usr/local/bin/flow; run curl -fsSL https://aquatic.dev/flow/install | sh\n",
		"# A development build\n$ flow update\nerror: flow (devel) is a development build, run flow update --force to install\n",
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
	if got, want := w.String(), "# Up to date\n$ flow update\nAlready up to date: 1.2.3\n"; got != want {
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

// TestDemos checks what each command of flow demo shows, case by case: the
// output the command prints, on demo apps and listeners, and an
// error as the command line reports it.
func TestDemos(t *testing.T) {
	// The serve demos warn as the environment's password has it.
	t.Setenv(config.EnvServerPassword, "")
	a, k := string(demoAccount), string(demoKeyAccount)
	for _, tt := range []struct {
		command string
		cases   []demoCase
		// want holds, for each case by name, the pattern its output, after
		// its comment and command line, matches in whole.
		want map[string]string
	}{
		{command: "version", cases: versionDemos, want: map[string]string{
			"version": regexp.QuoteMeta("flow " + product.Version() + "\n"),
			"json":    regexp.QuoteMeta("{\n  \"name\": \"flow\",\n  \"version\": \"" + product.Version() + "\"\n}\n"),
		}},
		{command: "login", cases: loginDemos, want: map[string]string{
			"browser":     regexp.QuoteMeta("Open this URL in your browser:\n  https://login.example/authorize\n\nWaiting for authorization...\n\nLogged in successfully\n"),
			"device":      regexp.QuoteMeta("Open this URL in your browser:\n  https://login.example/device\n\nEnter this code in your browser:\n  WDJB-MJHT\n\nWaiting for authorization...\n\nLogged in successfully\n"),
			"key":         regexp.QuoteMeta("Enter the API key: " + demoKey + "\n\nAPI key saved\n"),
			"no-login":    regexp.QuoteMeta("error: login to provider-b: unsupported operation\n"),
			"no-provider": regexp.QuoteMeta("error: " + errProviderRequired.Error() + "\n"),
			"key-json": regexp.QuoteMeta("Enter the API key: "+demoKey+"\n{\n  \"provider\": \"provider-b\",\n  \"account\": {\n    \"id\": \"") +
				`[0-9a-f-]{36}` + regexp.QuoteMeta("\",\n    \"kind\": \"api_key\",\n    \"source\": \"store\"\n  }\n}\n"),
		}},
		{command: "logout", cases: logoutDemos, want: map[string]string{
			"remove":      regexp.QuoteMeta("Removed:\n  provider-a  " + a + "  oauth  store\n"),
			"no-account":  regexp.QuoteMeta("error: --account is required; list account IDs with flow account\n"),
			"remove-json": regexp.QuoteMeta("{\n  \"provider\": \"provider-a\",\n  \"account\": {\n    \"id\": \"" + a + "\",\n    \"kind\": \"oauth\",\n    \"source\": \"store\"\n  }\n}\n"),
		}},
		{command: "account", cases: accountDemos, want: map[string]string{
			"list":         regexp.QuoteMeta("provider-a\n* " + a + "  oauth    store\n  " + k + "  api_key  store\nprovider-b\n  [no accounts]\n"),
			"current":      regexp.QuoteMeta("Current account:\n  provider-a  " + a + "  oauth  store\n"),
			"current-none": regexp.QuoteMeta("error: no active account: provider-b\n"),
			"use":          regexp.QuoteMeta("Switched to:\n  provider-a  " + k + "  api_key  store\n"),
			"use-invalid":  regexp.QuoteMeta("error: invalid account: 123\n"),
			"list-json": regexp.QuoteMeta("[\n  {\n    \"provider\": \"provider-a\",\n    \"active\": \"" + a + "\",\n    \"accounts\": [\n" +
				"      {\n        \"id\": \"" + a + "\",\n        \"kind\": \"oauth\",\n        \"source\": \"store\"\n      },\n" +
				"      {\n        \"id\": \"" + k + "\",\n        \"kind\": \"api_key\",\n        \"source\": \"store\"\n      }\n    ],\n" +
				"    \"login_methods\": [\n      \"browser\",\n      \"device\"\n    ],\n    \"built_in\": false\n  },\n" +
				"  {\n    \"provider\": \"provider-b\",\n    \"accounts\": [],\n    \"login_methods\": [],\n    \"built_in\": false\n  }\n]\n"),
			"current-json": regexp.QuoteMeta("[\n  {\n    \"provider\": \"provider-a\",\n    \"account\": {\n      \"id\": \"" + a +
				"\",\n      \"kind\": \"oauth\",\n      \"source\": \"store\"\n    }\n  }\n]\n"),
			"use-json":          regexp.QuoteMeta("{\n  \"provider\": \"provider-a\",\n  \"account\": {\n    \"id\": \"" + k + "\",\n    \"kind\": \"api_key\",\n    \"source\": \"store\"\n  }\n}\n"),
			"check":             regexp.QuoteMeta("ready\n"),
			"check-not-ready":   regexp.QuoteMeta("not_ready\nprovider-b has no active account; log in with flow login --provider provider-b\n"),
			"check-json":        regexp.QuoteMeta("{\n  \"provider\": \"provider-a\",\n  \"status\": \"ready\",\n  \"account\": \"" + a + "\"\n}\n"),
			"check-no-provider": regexp.QuoteMeta("error: " + errNoCheckProvider.Error() + "\n"),
		}},
		{command: "serve", cases: serveDemos, want: map[string]string{
			"local":   regexp.QuoteMeta("Server listening on:\n  http://127.0.0.1:4096\n"),
			"exposed": regexp.QuoteMeta("warning: FLOW_SERVER_PASSWORD is not set, so anyone who reaches 0.0.0.0 can run commands on this machine\nServer listening on:\n  http://0.0.0.0:4096\n"),
			"json":    regexp.QuoteMeta(`{"url":"http://127.0.0.1:4096"}` + "\n"),
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
