package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/unaquatic/flow/update"
)

// testUpdater returns an Updater of a flow at version current whose
// registry names 0.2.0 the latest release. With global set, the flow is in
// npm's global folder, which a fake npm reports; otherwise it is a build
// from source.
func testUpdater(t *testing.T, current string, global bool) *update.Updater {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"version":"0.2.0"}`)
	}))
	t.Cleanup(srv.Close)
	u := &update.Updater{Dir: t.TempDir(), Current: current, Registry: srv.URL,
		Executable: filepath.Join(t.TempDir(), "flow")}
	if !global {
		return u
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root = filepath.Join(root, "node_modules")
	u.Executable = filepath.Join(root, "@unaquatic", "flow", "bin", "flow")
	if err := os.MkdirAll(filepath.Dir(u.Executable), 0o755); err != nil {
		t.Fatal(err)
	}
	u.Output = func(_ context.Context, name string, args ...string) ([]byte, error) {
		return []byte(root), nil
	}
	return u
}

// TestRunUpdate checks what flow update reports, and that it installs a
// newer release only into a global install of a package manager.
func TestRunUpdate(t *testing.T) {
	for _, tt := range []struct {
		name    string
		current string
		opts    updateOptions
		want    string
	}{
		{name: "up to date", current: "v0.2.0", want: "flow v0.2.0 is up to date.\n"},
		{name: "check", current: "v0.1.0", opts: updateOptions{check: true},
			want: "flow v0.2.0 is available, and this is v0.1.0. Run flow update to install it.\n"},
		{name: "development build", current: "(devel)",
			want: "flow (devel) is a development build, run flow update --force to install v0.2.0.\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var w bytes.Buffer
			u := testUpdater(t, tt.current, false)
			if err := runUpdate(t.Context(), &w, u, tt.opts, (*exec.Cmd).Run); err != nil {
				t.Fatal(err)
			}
			if w.String() != tt.want {
				t.Errorf("output = %q, want %q", w.String(), tt.want)
			}
			if s, err := u.State(); err != nil || s.Latest != "v0.2.0" {
				t.Errorf("State = %+v, %v; want the release saved", s, err)
			}
		})
	}

	t.Run("build from source", func(t *testing.T) {
		err := runUpdate(t.Context(), new(bytes.Buffer), testUpdater(t, "v0.1.0", false), updateOptions{}, (*exec.Cmd).Run)
		if !errors.Is(err, update.ErrUnmanaged) || !strings.Contains(err.Error(), "npm install -g @unaquatic/flow@latest") {
			t.Errorf("runUpdate = %v, want ErrUnmanaged naming the npm install", err)
		}
	})

	t.Run("install", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("uses a shell script")
		}
		bin := t.TempDir()
		t.Setenv("PATH", bin)
		if err := os.WriteFile(filepath.Join(bin, "npm"), []byte("#!/bin/sh\necho \"npm $*\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		var w bytes.Buffer
		if err := runUpdate(t.Context(), &w, testUpdater(t, "v0.1.0", true), updateOptions{}, (*exec.Cmd).Run); err != nil {
			t.Fatal(err)
		}
		want := "flow v0.1.0 is updating to v0.2.0 with npm install -g @unaquatic/flow@0.2.0\n" +
			"npm install -g @unaquatic/flow@0.2.0\n" +
			"flow updated to v0.2.0.\n"
		if w.String() != want {
			t.Errorf("output = %q, want %q", w.String(), want)
		}
	})
}

// TestRunUpdateBusy checks that flow update installs nothing while another
// flow, such as one installing in the background, holds the install lock.
func TestRunUpdateBusy(t *testing.T) {
	u := testUpdater(t, "v0.1.0", true)
	held, err := u.LockInstall()
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	install := func(*exec.Cmd) error {
		t.Error("installed while another flow holds the install lock")
		return nil
	}
	err = runUpdate(t.Context(), new(bytes.Buffer), u, updateOptions{}, install)
	if !errors.Is(err, update.ErrBusy) || err.Error() != "cannot update flow: another flow is installing an update" {
		t.Errorf("runUpdate = %v, want ErrBusy", err)
	}

	// Once the other is done, it installs.
	held.Close()
	installed := false
	install = func(*exec.Cmd) error { installed = true; return nil }
	if err := runUpdate(t.Context(), new(bytes.Buffer), u, updateOptions{}, install); err != nil || !installed {
		t.Errorf("runUpdate once the lock is free = %v, installed %v; want it installed", err, installed)
	}
}
