package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/unaquatic/flow/terminal"
	"github.com/unaquatic/flow/update"
)

// TestRunUninstallManaged checks that flow uninstall names the command that
// removes a flow npm or pnpm installed, and removes nothing.
func TestRunUninstallManaged(t *testing.T) {
	for m, want := range map[update.Method]string{
		update.MethodNPM:  "flow uninstall: installed by npm, run npm uninstall -g @unaquatic/flow",
		update.MethodPNPM: "flow uninstall: installed by pnpm, run pnpm remove -g @unaquatic/flow",
	} {
		u := &update.Updater{Dir: t.TempDir(), Executable: filepath.Join(t.TempDir(), "node_modules", "@unaquatic", "flow", "bin", "flow-linux-x64"), Method: m}
		var w bytes.Buffer
		err := runUninstall(&w, false, u, t.TempDir(), "", uninstallOptions{})
		if err == nil || err.Error() != want || w.Len() != 0 {
			t.Errorf("%s: runUninstall = %v, wrote %q; want %q and nothing written", m, err, w.String(), want)
		}
	}
}

// TestRunUninstall checks what flow uninstall writes once it has removed a
// flow that the install script installed: the banner, a line for each step
// and that it is done.
func TestRunUninstall(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(t.TempDir(), "bin")
	exe := filepath.Join(dir, "flow")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(exe, nil, 0o755)
	os.WriteFile(filepath.Join(dir, update.MarkerName), nil, 0o644)
	os.MkdirAll(filepath.Dir(update.LinkPath(home)), 0o755)
	os.Symlink(exe, update.LinkPath(home))
	os.WriteFile(filepath.Join(home, ".zprofile"), []byte("\n# >>> flow >>>\nexport PATH=x\n# <<< flow <<<\n"), 0o644)

	var w bytes.Buffer
	u := &update.Updater{Dir: t.TempDir(), Executable: exe}
	if err := runUninstall(&w, false, u, home, "", uninstallOptions{data: true}); err != nil {
		t.Fatal(err)
	}
	want := terminal.LogoBanner().PlainText() + "\n\nFlow Installer\n\n" +
		"■ Binary file removed\n■ Symlink removed\n■ Shell setup removed\n■ Config and sessions removed\n\n" +
		"Uninstall complete\n"
	if w.String() != want {
		t.Errorf("output =\n%s\nwant\n%s", w.String(), want)
	}
}
