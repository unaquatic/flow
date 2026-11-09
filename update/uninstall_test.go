package update

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestRemovePathBlock checks that the lines the install script added to a
// profile go, with the blank line before them, and that the rest stays.
func TestRemovePathBlock(t *testing.T) {
	for _, tt := range []struct {
		name, profile, want string
		found               bool
	}{
		{name: "block", found: true,
			profile: "export EDITOR=vi\n\n# >>> flow >>>\nexport PATH=\"/home/u/.local/bin:$PATH\"\n# <<< flow <<<\nalias ll='ls -l'\n",
			want:    "export EDITOR=vi\nalias ll='ls -l'\n"},
		{name: "block alone", found: true,
			profile: "\n# >>> flow >>>\nfish_add_path /home/u/.local/bin\n# <<< flow <<<\n", want: ""},
		{name: "no block", profile: "export EDITOR=vi\n", want: "export EDITOR=vi\n"},
		{name: "no end", profile: "# >>> flow >>>\nexport PATH=x\n", want: "# >>> flow >>>\nexport PATH=x\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".zprofile")
			if err := os.WriteFile(path, []byte(tt.profile), 0o640); err != nil {
				t.Fatal(err)
			}
			found, err := RemovePathBlock(path)
			got, _ := os.ReadFile(path)
			if err != nil || found != tt.found || string(got) != tt.want {
				t.Errorf("RemovePathBlock = %v, %v, profile %q; want %v, %q", found, err, got, tt.found, tt.want)
			}
			if info, err := os.Stat(path); err == nil && info.Mode().Perm() != 0o640 {
				t.Errorf("mode %v, want it kept", info.Mode().Perm())
			}
		})
	}
	if found, err := RemovePathBlock(filepath.Join(t.TempDir(), "missing")); found || err != nil {
		t.Errorf("RemovePathBlock of a missing profile = %v, %v", found, err)
	}
}

// TestUninstall checks that Uninstall removes what the install script made
// in home, and leaves a link that is not to this flow and the data
// directory.
func TestUninstall(t *testing.T) {
	home := t.TempDir()
	u := standalone(t, releases(t, false))
	exe := u.Executable
	if err := os.WriteFile(filepath.Join(filepath.Dir(exe), ".flow-new-7"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(LinkPath(home)), 0o755); err != nil {
		t.Fatal(err)
	}
	// The link names the folder as the install script does, which need not
	// be the path the executable resolves to, as on macOS under /var.
	if err := os.Symlink(filepath.Join(filepath.Dir(exe), ".", filepath.Base(exe)), LinkPath(home)); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(home, ".zprofile")
	os.WriteFile(profile, []byte("x=1\n\n# >>> flow >>>\nexport PATH=y\n# <<< flow <<<\n"), 0o644)

	removed, err := u.Uninstall(home, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{exe, LinkPath(home), profile}; len(removed) != 3 || removed[0] != want[0] || removed[1] != want[1] || removed[2] != want[2] {
		t.Errorf("removed %q, want %q", removed, want)
	}
	if _, err := os.Stat(filepath.Dir(exe)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("install folder left: %v", err)
	}
	if _, err := os.Lstat(LinkPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("link left: %v", err)
	}
	if got, _ := os.ReadFile(profile); string(got) != "x=1\n" {
		t.Errorf("profile = %q, want the block gone", got)
	}
	if _, err := os.Stat(u.Dir); err != nil {
		t.Errorf("data directory: %v, want it kept", err)
	}
}

// TestUninstallOtherLink checks that a link in ~/.local/bin to another flow
// stays, and that a flow the install script did not install is not removed.
func TestUninstallOtherLink(t *testing.T) {
	home := t.TempDir()
	u := standalone(t, releases(t, false))
	os.MkdirAll(filepath.Dir(LinkPath(home)), 0o755)
	os.Symlink("/usr/local/bin/flow", LinkPath(home))
	if _, err := u.Uninstall(home, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(LinkPath(home)); err != nil {
		t.Errorf("link to another flow removed: %v", err)
	}

	other := &Updater{Dir: t.TempDir(), Executable: filepath.Join(t.TempDir(), "flow")}
	os.WriteFile(other.Executable, nil, 0o755)
	if _, err := other.Uninstall(home, ""); !errors.Is(err, ErrNotStandalone) {
		t.Errorf("Uninstall of another flow = %v, want ErrNotStandalone", err)
	}
	if _, err := os.Stat(other.Executable); err != nil {
		t.Errorf("another flow removed: %v", err)
	}
}
