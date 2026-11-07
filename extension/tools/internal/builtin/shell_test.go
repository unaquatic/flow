//go:build unix

package builtin

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent"
)

// maxBashWait bounds how long a killed or finished command may take to
// return. Commands that must be killed sleep far longer than this.
const maxBashWait = 10 * time.Second

// TestBash_Result checks the output reported for each way a command can end.
// None of them is an error: a failed run is an Output with IsError set.
func TestBash_Result(t *testing.T) {
	tests := map[string]struct {
		command string
		timeout time.Duration
		ctx     func(*testing.T) context.Context
		want    agent.ToolOutput
	}{
		"success": {
			command: "echo out; echo err >&2",
			want:    agent.ToolOutput{Text: "out\nerr\n"},
		},
		"exit status": {
			command: "echo out; exit 3",
			want:    agent.ToolOutput{Text: "out\n\n[exit 3]", IsError: true},
		},
		"timeout": {
			command: "sleep 30",
			timeout: 200 * time.Millisecond,
			want:    agent.ToolOutput{Text: "[timeout after 0.2s]", IsError: true},
		},
		"caller cancelled": {
			command: "sleep 30",
			ctx:     cancelAfter(200 * time.Millisecond),
			want:    agent.ToolOutput{Text: "[cancelled]", IsError: true},
		},
		"caller deadline": {
			command: "sleep 30",
			ctx:     deadlineAfter(200 * time.Millisecond),
			want:    agent.ToolOutput{Text: "[cancelled]", IsError: true},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()
			if tt.ctx != nil {
				ctx = tt.ctx(t)
			}
			start := time.Now()
			out, err := runBash(ctx, tt.command, tt.timeout)
			if elapsed := time.Since(start); elapsed > maxBashWait {
				t.Errorf("returned after %s; want at most %s", elapsed, maxBashWait)
			}
			if err != nil {
				t.Errorf("error = %v; want nil", err)
			}
			if !reflect.DeepEqual(out, tt.want) {
				t.Errorf("output = %+v; want %+v", out, tt.want)
			}
		})
	}
}

// TestBash_TimeoutKillsProcessGroup checks that a timeout kills the processes
// the shell started, not only the shell.
func TestBash_TimeoutKillsProcessGroup(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "survived")
	command := fmt.Sprintf("(sleep 1; touch '%s') & wait", marker)

	start := time.Now()
	out, err := runBash(t.Context(), command, 200*time.Millisecond)
	want := agent.ToolOutput{Text: "[timeout after 0.2s]", IsError: true}
	if err != nil || !reflect.DeepEqual(out, want) {
		t.Fatalf("= %+v, %v; want %+v, nil", out, err, want)
	}

	// The child would create the marker 1s after the start.
	time.Sleep(time.Until(start.Add(2 * time.Second)))
	if _, err := os.Stat(marker); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("child process outlived the timeout: stat %s: %v", marker, err)
	}
}

// TestBash_BackgroundProcessDoesNotBlock checks that a background process
// holding the output open does not keep the call waiting for it to exit.
func TestBash_BackgroundProcessDoesNotBlock(t *testing.T) {
	start := time.Now()
	out, err := runBash(t.Context(), "sleep 30 & echo $!", 0)
	elapsed := time.Since(start)

	pid, convErr := strconv.Atoi(strings.TrimSpace(out.Text))
	if convErr != nil {
		t.Fatalf("output %q is not a process ID: %v", out.Text, convErr)
	}
	t.Cleanup(func() {
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			t.Errorf("kill background process %d: %v", pid, err)
		}
	})

	if err != nil || out.IsError {
		t.Errorf("= %+v, %v; want a successful run", out, err)
	}
	if elapsed > maxBashWait {
		t.Errorf("returned after %s; want at most %s", elapsed, maxBashWait)
	}
}

// TestDefaultShell checks that $SHELL is used only when it names an executable
// POSIX shell, and that otherwise a POSIX shell is found.
func TestDefaultShell(t *testing.T) {
	t.Setenv("SHELL", "")
	fallback := DefaultShell()
	if !slices.Contains(posixShells, filepath.Base(fallback)) {
		t.Fatalf("fallback shell %q is not a POSIX shell", fallback)
	}

	dir := t.TempDir()
	fish := filepath.Join(dir, "fish")
	if err := os.WriteFile(fish, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	tests := map[string]struct {
		shell string
		want  string
	}{
		"posix shell":     {shell: "/bin/sh", want: "/bin/sh"},
		"non-posix shell": {shell: fish, want: fallback},
		"missing shell":   {shell: filepath.Join(dir, "zsh"), want: fallback},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SHELL", tt.shell)
			if got := DefaultShell(); got != tt.want {
				t.Errorf("DefaultShell() with SHELL=%q = %q; want %q", tt.shell, got, tt.want)
			}
		})
	}
}

// TestBash_UsesDefaultShell checks that the tool runs commands with the shell
// chosen by DefaultShell and names it as the shell argument's default. It uses
// bash, so that a tool running /bin/sh regardless fails.
func TestBash_UsesDefaultShell(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash not available: %v", err)
	}
	t.Setenv("SHELL", bash)
	bt := Bash()
	if desc := bt.Schema.Properties["shell"].Description; !strings.HasSuffix(desc, "Defaults to bash.") {
		t.Errorf("shell argument description %q does not name bash as the default", desc)
	}
	out, err := bt.Execute(t.Context(), map[string]any{"command": "echo $0"})
	if want := (agent.ToolOutput{Text: bash + "\n"}); err != nil || !reflect.DeepEqual(out, want) {
		t.Errorf("= %+v, %v; want %+v, nil", out, err, want)
	}
}

// TestShells_Path checks that a shell name resolves to an installed shell, and
// that the default shell's name keeps its $SHELL path.
func TestShells_Path(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	bash := filepath.Join(t.TempDir(), "bash")
	if err := os.WriteFile(bash, []byte("#!/bin/sh\nexec /bin/sh \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", bash)
	shells := FindShells()
	if names := shells.Names(); !slices.IsSorted(names) || !slices.Contains(names, "bash") || !slices.Contains(names, "sh") {
		t.Errorf("Names() = %v; want a sorted list containing bash and sh", names)
	}

	tests := map[string]struct {
		name    string
		want    string
		wantErr string
	}{
		"empty name":      {name: "", want: bash},
		"default by name": {name: "bash", want: bash},
		"other shell":     {name: "sh", want: sh},
		"not installed":   {name: "fish", wantErr: `shell fish not installed`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := shells.Path(tt.name)
			if tt.wantErr != "" {
				if err == nil || !strings.HasPrefix(err.Error(), tt.wantErr) {
					t.Errorf("Path(%q) error = %v; want prefix %q", tt.name, err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("Path(%q) = %q, %v; want %q, nil", tt.name, got, err, tt.want)
			}
		})
	}
}

// TestBash_ShellArgument checks that a call can run its command with an
// installed shell other than the default, and that the schema lists it.
func TestBash_ShellArgument(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash not available: %v", err)
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", bash)
	bt := Bash()
	if enum := bt.Schema.Properties["shell"].Enum; !slices.Contains(enum, "sh") {
		t.Errorf("shell enum = %v; want it to contain sh", enum)
	}
	out, err := bt.Execute(t.Context(), map[string]any{"command": "echo $0", "shell": "sh"})
	if want := (agent.ToolOutput{Text: sh + "\n"}); err != nil || !reflect.DeepEqual(out, want) {
		t.Errorf("= %+v, %v; want %+v, nil", out, err, want)
	}
}

// runBash calls the bash tool with arguments shaped as decoded from JSON. A
// zero timeout leaves timeout_ms unset.
func runBash(ctx context.Context, command string, timeout time.Duration) (agent.ToolOutput, error) {
	args := map[string]any{"command": command}
	if timeout > 0 {
		args["timeout_ms"] = float64(timeout.Milliseconds())
	}
	return Bash().Execute(ctx, args)
}

// cancelAfter returns a context that is cancelled after d, as when the user
// stops a run.
func cancelAfter(d time.Duration) func(*testing.T) context.Context {
	return func(t *testing.T) context.Context {
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)
		time.AfterFunc(d, cancel)
		return ctx
	}
}

// deadlineAfter returns a context whose deadline, set by the caller rather
// than by the tool, passes after d.
func deadlineAfter(d time.Duration) func(*testing.T) context.Context {
	return func(t *testing.T) context.Context {
		ctx, cancel := context.WithTimeout(t.Context(), d)
		t.Cleanup(cancel)
		return ctx
	}
}
