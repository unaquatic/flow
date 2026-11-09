package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunDemoUpdate checks that flow demo update shows each case of flow
// update as the command prints it, an error as the command line reports
// it, and only the case it is given one.
func TestRunDemoUpdate(t *testing.T) {
	var w bytes.Buffer
	if err := runDemoUpdate(t.Context(), &w, false, nil); err != nil {
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
	if n := strings.Count(w.String(), "\n# "); n != len(updateDemos)-1 {
		t.Errorf("output has %d cases after the first, want %d", n, len(updateDemos)-1)
	}

	w.Reset()
	if err := runDemoUpdate(t.Context(), &w, false, []string{"latest"}); err != nil {
		t.Fatal(err)
	}
	if got, want := w.String(), "# Up to date\n$ flow update\nflow v1.2.3 is up to date.\n"; got != want {
		t.Errorf("latest = %q, want %q", got, want)
	}
}

// TestUpdateDemoArgs checks that flow demo update takes no case or one it
// knows.
func TestUpdateDemoArgs(t *testing.T) {
	for _, args := range [][]string{nil, {"install"}} {
		if err := updateDemoArgs(args); err != nil {
			t.Errorf("updateDemoArgs(%q) = %v", args, err)
		}
	}
	for _, args := range [][]string{{"nope"}, {"latest", "check"}} {
		if err := updateDemoArgs(args); err == nil {
			t.Errorf("updateDemoArgs(%q) accepted", args)
		}
	}
}
