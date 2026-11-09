package update

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unaquatic/flow/internal/flock"
)

// TestIsRelease checks that only versions of tagged, clean builds count as
// releases that updates apply to.
func TestIsRelease(t *testing.T) {
	for v, want := range map[string]bool{
		"v0.1.0":                               true,
		"v1.2.3-beta.1":                        true,
		"(devel)":                              false,
		"":                                     false,
		"0.1.0":                                false,
		"v0.1.0+dirty":                         false,
		"v0.1.1-0.20261010091914-54eabbb75a2d": false,
		"v0.1.1-0.20261010091914-54eabbb75a2d+dirty": false,
	} {
		if got := IsRelease(v); got != want {
			t.Errorf("IsRelease(%q) = %v, want %v", v, got, want)
		}
	}
}

// TestNewer checks that versions compare as semantic versions, with or
// without the leading "v" that npm leaves out.
func TestNewer(t *testing.T) {
	for _, tt := range []struct {
		latest, current string
		want            bool
	}{
		{"0.2.0", "v0.1.0", true},
		{"v0.10.0", "v0.9.0", true},
		{"0.1.0", "v0.1.0", false},
		{"0.1.0", "v0.2.0", false},
		{"1.0.0", "v1.0.0-rc.1", true},
		{"1.0.0-rc.1", "v1.0.0", false},
		{"bogus", "v0.1.0", false},
		{"0.2.0", "(devel)", false},
	} {
		if got := Newer(tt.latest, tt.current); got != tt.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tt.latest, tt.current, got, tt.want)
		}
	}
}

// TestDetectMethod checks that the folders an executable is in tell which
// package manager installed it.
func TestDetectMethod(t *testing.T) {
	for path, want := range map[string]Method{
		"/usr/local/lib/node_modules/@unaquatic/flow/bin/flow":                                          MethodNPM,
		"/home/u/.nvm/versions/node/v24.0.0/lib/node_modules/@unaquatic/flow-linux-x64/bin/flow":        MethodNPM,
		`C:\Users\u\AppData\Roaming\npm\node_modules\@unaquatic\flow\bin\flow.exe`:                      MethodNPM,
		"/home/u/.local/share/pnpm/global/5/node_modules/.pnpm/@unaquatic+flow@0.1.0/node_modules/flow": MethodPNPM,
		// Other package managers' installs read as npm's, which Plan finds
		// outside npm's global folder.
		"/home/u/.config/yarn/global/node_modules/@unaquatic/flow/bin/flow": MethodNPM,
		"/home/u/.bun/install/global/node_modules/@unaquatic/flow/bin/flow": MethodNPM,
		"/home/u/go/bin/flow":      MethodNone,
		"/usr/local/bin/flow":      MethodNone,
		"/work/flow/cmd/flow/flow": MethodNone,
	} {
		if got := DetectMethod(path); got != want {
			t.Errorf("DetectMethod(%q) = %q, want %q", path, got, want)
		}
	}
}

// TestInstallArgs checks the command line each package manager installs a
// release with, the version without its leading "v".
func TestInstallArgs(t *testing.T) {
	for m, want := range map[Method]string{
		MethodNPM:  "npm install -g @unaquatic/flow@0.2.0",
		MethodPNPM: "pnpm add -g @unaquatic/flow@0.2.0",
	} {
		if got := strings.Join(m.InstallArgs("v0.2.0"), " "); got != want {
			t.Errorf("%s.InstallArgs = %q, want %q", m, got, want)
		}
	}
	if args := MethodNone.InstallArgs("v0.2.0"); args != nil {
		t.Errorf("MethodNone.InstallArgs = %q, want nil", args)
	}
}

// registry serves version as the latest release of PackageName and counts
// the requests it answers.
func registry(t *testing.T, version string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/@unaquatic%2Fflow/latest" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		fmt.Fprintf(w, `{"name":%q,"version":%q}`, PackageName, version)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// TestLatest checks that the release the registry's latest dist-tag names
// comes back in Canonical form, and that a response without a version
// fails.
func TestLatest(t *testing.T) {
	srv, _ := registry(t, "0.2.0")
	u := &Updater{Registry: srv.URL + "/", Current: "v0.1.0"}
	if got, err := u.Latest(t.Context()); err != nil || got != "v0.2.0" {
		t.Errorf("Latest = %q, %v; want v0.2.0", got, err)
	}

	bad, _ := registry(t, "not-a-version")
	u.Registry = bad.URL
	if _, err := u.Latest(t.Context()); err == nil {
		t.Error("Latest of a response without a valid version succeeded")
	}
}

// TestCheckCaches checks that Check asks the registry once per Interval and
// keeps what it found in the state file.
func TestCheckCaches(t *testing.T) {
	srv, hits := registry(t, "0.2.0")
	u := &Updater{Dir: t.TempDir(), Registry: srv.URL, Current: "v0.1.0", Interval: time.Hour}
	for range 3 {
		if got, err := u.Check(t.Context()); err != nil || got != "v0.2.0" {
			t.Fatalf("Check = %q, %v; want v0.2.0", got, err)
		}
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("registry asked %d times, want once", n)
	}
	s, err := u.State()
	if err != nil || s.Latest != "v0.2.0" || s.CheckedAt.IsZero() {
		t.Errorf("State = %+v, %v; want the release and the time of the check", s, err)
	}

	// Once the interval has passed, the registry is asked again.
	if err := u.SaveState(func(s *State) { s.CheckedAt = time.Now().Add(-2 * time.Hour) }); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("registry asked %d times after the interval, want twice", n)
	}
}

// fakeGlobal makes a global folder of npm in a temporary directory, with an
// executable of flow in it, and returns an Updater of that executable whose
// npm reports the folder.
func fakeGlobal(t *testing.T, registryURL string) *Updater {
	t.Helper()
	root := filepath.Join(t.TempDir(), "lib", "node_modules")
	exe := filepath.Join(root, "@unaquatic", "flow", "bin", "flow")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	root, _ = filepath.EvalSymlinks(root)
	exe, _ = filepath.EvalSymlinks(exe)
	return &Updater{
		Dir:        t.TempDir(),
		Current:    "v0.1.0",
		Executable: exe,
		Registry:   registryURL,
		Output: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "npm" && slices.Equal(args, []string{"root", "-g"}) {
				return []byte(root + "\n"), nil
			}
			return nil, fmt.Errorf("unexpected command %s %q", name, args)
		},
	}
}

// TestPlan checks that only an executable in the global folder of the
// package manager that installed it updates, and only where that folder is
// writable.
func TestPlan(t *testing.T) {
	u := fakeGlobal(t, "")
	inst, err := u.Plan(t.Context(), "v0.2.0")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if got, want := inst.String(), "npm install -g @unaquatic/flow@0.2.0"; got != want || inst.Version != "v0.2.0" {
		t.Errorf("Plan = %q at %s, want %q at v0.2.0", got, inst.Version, want)
	}

	// A project's node_modules is no global install.
	local := *u
	local.Executable = filepath.Join(t.TempDir(), "node_modules", "@unaquatic", "flow", "bin", "flow")
	if _, err := local.Plan(t.Context(), "v0.2.0"); !errors.Is(err, ErrUnmanaged) {
		t.Errorf("Plan of a project's install = %v, want ErrUnmanaged", err)
	}

	// Nor is a binary outside node_modules, such as one built from source.
	source := *u
	source.Executable = filepath.Join(t.TempDir(), "flow")
	if _, err := source.Plan(t.Context(), "v0.2.0"); !errors.Is(err, ErrUnmanaged) {
		t.Errorf("Plan of a build from source = %v, want ErrUnmanaged", err)
	}

	if runtime.GOOS == "windows" || os.Getuid() == 0 {
		t.Skip("cannot make a folder read-only to this user")
	}
	root, _ := u.GlobalRoot(t.Context(), MethodNPM)
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o755) })
	_, err = u.Plan(t.Context(), "v0.2.0")
	if nw, ok := errors.AsType[*NotWritableError](err); !ok || nw.Dir != root {
		t.Errorf("Plan into a read-only folder = %v, want a NotWritableError of %s", err, root)
	}
}

// TestAuto checks what Auto reports of the release the registry names:
// nothing for a build that is no release or is up to date, and a release
// available, without installing it, for a flow no package manager installed.
func TestAuto(t *testing.T) {
	// No package manager runs, whatever the case.
	t.Setenv("PATH", t.TempDir())
	srv, _ := registry(t, "0.2.0")
	for _, tt := range []struct {
		name      string
		current   string
		unmanaged bool
		want      Result
		ok        bool
	}{
		{name: "unmanaged", current: "v0.1.0", unmanaged: true, want: Result{Status: StatusAvailable, Version: "v0.2.0"}, ok: true},
		{name: "current", current: "v0.2.0"},
		{name: "newer", current: "v0.3.0"},
		{name: "devel", current: "(devel)"},
		{name: "pseudo", current: "v0.1.1-0.20261010091914-54eabbb75a2d"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			u := fakeGlobal(t, srv.URL)
			u.Current = tt.current
			if tt.unmanaged {
				u.Executable = filepath.Join(t.TempDir(), "flow")
			}
			got, ok := u.Auto(t.Context())
			if got != tt.want || ok != tt.ok {
				t.Errorf("Auto = %+v, %v; want %+v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

// TestAutoInstall checks that Auto installs a newer release with the
// package manager, writing its output to the update log, and that a
// release that failed to install is not installed again.
func TestAutoInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Auto does not install on Windows")
	}
	srv, _ := registry(t, "0.2.0")
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	npm := func(script string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, "npm"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	u := fakeGlobal(t, srv.URL)
	npm(`echo "installed $*"`)
	if got, ok := u.Auto(t.Context()); !ok || got.Status != StatusInstalled || got.Version != "v0.2.0" {
		t.Fatalf("Auto = %+v, %v; want v0.2.0 installed", got, ok)
	}
	log, err := os.ReadFile(LogPath(u.Dir))
	if err != nil || !strings.Contains(string(log), "installed install -g @unaquatic/flow@0.2.0") {
		t.Errorf("update log = %q, %v; want the output of npm", log, err)
	}
	if s, err := u.State(); err != nil || s.Installed != "v0.2.0" {
		t.Errorf("State = %+v, %v; want v0.2.0 installed", s, err)
	}

	u = fakeGlobal(t, srv.URL)
	npm("echo no access >&2; exit 1")
	if got, ok := u.Auto(t.Context()); !ok || got.Status != StatusFailed || got.Err == nil {
		t.Fatalf("Auto with a failing npm = %+v, %v; want a failure", got, ok)
	}
	if s, err := u.State(); err != nil || s.Failed != "v0.2.0" {
		t.Errorf("State = %+v, %v; want v0.2.0 failed", s, err)
	}
	npm("echo must not run; exit 1")
	if got, ok := u.Auto(t.Context()); !ok || got.Status != StatusAvailable {
		t.Errorf("Auto after a failure = %+v, %v; want the release only available", got, ok)
	}
}

// TestBackgroundBusy checks that an install waits for none while another
// flow holds the install lock, and reports ErrBusy instead.
func TestBackgroundBusy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script")
	}
	dir := t.TempDir()
	holder, err := os.OpenFile(filepath.Join(dir, InstallLockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	// Hold the lock as another flow's install would.
	if ok, err := flock.TryLock(holder, true); err != nil || !ok {
		t.Fatalf("TryLock = %v, %v", ok, err)
	}
	u := &Updater{Dir: dir}
	if err := u.Background(Install{Args: []string{"true"}}); !errors.Is(err, ErrBusy) {
		t.Errorf("Background while another installs = %v, want ErrBusy", err)
	}
	if err := flock.Unlock(holder); err != nil {
		t.Fatal(err)
	}
	if err := u.Background(Install{Args: []string{"true"}}); err != nil {
		t.Errorf("Background once the lock is free: %v", err)
	}
}

// TestTakeInstalled checks that the release an update installed is reported
// once, by its own first start, and by no flow of another version.
func TestTakeInstalled(t *testing.T) {
	u := &Updater{Dir: t.TempDir(), Current: "v0.1.0"}
	if got := u.TakeInstalled(); got != "" {
		t.Errorf("TakeInstalled without a state file = %q, want none", got)
	}
	if err := u.RecordInstalled("0.2.0"); err != nil {
		t.Fatal(err)
	}
	// The flow that installed it still runs the version before.
	if got := u.TakeInstalled(); got != "" {
		t.Errorf("TakeInstalled of v0.1.0 = %q, want none", got)
	}
	u.Current = "v0.2.0"
	if got := u.TakeInstalled(); got != "v0.2.0" {
		t.Errorf("first TakeInstalled of v0.2.0 = %q, want v0.2.0", got)
	}
	if got := u.TakeInstalled(); got != "" {
		t.Errorf("second TakeInstalled of v0.2.0 = %q, want none", got)
	}
}
