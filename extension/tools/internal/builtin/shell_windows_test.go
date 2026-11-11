//go:build windows

package builtin

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent"
)

// TestShellName checks that a shell is named by its file name, lowercased
// and without .exe.
func TestShellName(t *testing.T) {
	for path, want := range map[string]string{
		`C:\Program Files\Git\bin\bash.exe`: "bash",
		`C:\msys64\usr\bin\SH.EXE`:          "sh",
		`C:\tools\zsh`:                      "zsh",
	} {
		if got := ShellName(path); got != want {
			t.Errorf("ShellName(%q) = %q; want %q", path, got, want)
		}
	}
}

// TestUsableShell checks that WSL's bash.exe is passed over and Git Bash is
// not.
func TestUsableShell(t *testing.T) {
	for path, want := range map[string]bool{
		`C:\Program Files\Git\bin\bash.exe`:                          true,
		`C:\msys64\usr\bin\bash.exe`:                                 true,
		`C:\Windows\System32\bash.exe`:                               false,
		`C:\WINDOWS\Sysnative\bash.exe`:                              false,
		`C:\Users\name\AppData\Local\Microsoft\WindowsApps\bash.exe`: false,
	} {
		if got := usableShell(path); got != want {
			t.Errorf("usableShell(%q) = %v; want %v", path, got, want)
		}
	}
}

// isolateShells leaves no shell to be found but the ones the test puts in
// the ProgramFiles folder it returns.
func isolateShells(t *testing.T) (programFiles string) {
	t.Helper()
	programFiles = t.TempDir()
	t.Setenv("SHELL", "")
	t.Setenv("PATH", "")
	t.Setenv("ProgramFiles", programFiles)
	t.Setenv("ProgramFiles(x86)", "")
	t.Setenv("LocalAppData", "")
	return programFiles
}

// TestDefaultShell_FindsGitBash checks that Git Bash is found where Git for
// Windows installs it, without PATH.
func TestDefaultShell_FindsGitBash(t *testing.T) {
	bash := filepath.Join(isolateShells(t), "Git", "bin", "bash.exe")
	if err := os.MkdirAll(filepath.Dir(bash), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bash, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DefaultShell(); got != bash {
		t.Errorf("DefaultShell() = %q; want %q", got, bash)
	}
}

// TestBash_NoShell checks that without a POSIX shell the schema names no
// default and a call fails with ErrNoShell.
func TestBash_NoShell(t *testing.T) {
	isolateShells(t)
	bt := Bash()
	if desc := bt.Schema.Properties["shell"].Description; strings.Contains(desc, "Defaults to") {
		t.Errorf("shell argument description %q names a default", desc)
	}
	if _, err := bt.Execute(t.Context(), map[string]any{"command": "echo out"}); !errors.Is(err, ErrNoShell) {
		t.Errorf("error = %v; want ErrNoShell", err)
	}
}

// TestBash_GitBash runs commands with the installed Git Bash, as GitHub's
// Windows runners have, and checks that a timeout kills the processes the
// shell started, not only the launcher.
func TestBash_GitBash(t *testing.T) {
	if DefaultShell() == "" {
		t.Skip("Git Bash is not installed")
	}
	bt := Bash()

	out, err := bt.Execute(t.Context(), map[string]any{"command": "echo out; echo err >&2"})
	if want := (agent.ToolOutput{Text: "out\nerr\n"}); err != nil || !reflect.DeepEqual(out, want) {
		t.Errorf("= %+v, %v; want %+v, nil", out, err, want)
	}

	marker := filepath.ToSlash(filepath.Join(t.TempDir(), "survived"))
	start := time.Now()
	out, err = bt.Execute(t.Context(), map[string]any{
		"command":    fmt.Sprintf("(sleep 1; touch '%s') & wait", marker),
		"timeout_ms": float64(200),
	})
	if want := (agent.ToolOutput{Text: "[timeout after 0.2s]", IsError: true}); err != nil || !reflect.DeepEqual(out, want) {
		t.Fatalf("= %+v, %v; want %+v, nil", out, err, want)
	}
	// The child would create the marker 1s after the start.
	time.Sleep(time.Until(start.Add(2 * time.Second)))
	if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("child process outlived the timeout: stat %s: %v", marker, err)
	}
}
