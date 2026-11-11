package builtin

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/unaquatic/flow/agent"
)

// bashWaitDelay bounds how long background processes may keep a command's
// output open after the shell exits or is killed.
const bashWaitDelay = time.Second

// posixShells lists, by ShellName, the shells that accept POSIX sh syntax and
// the -c flag.
var posixShells = []string{"bash", "dash", "ksh", "sh", "zsh"}

// ErrNoShell reports that no POSIX shell is installed, as on Windows without
// Git for Windows.
var ErrNoShell = errors.New("no POSIX shell found: install Git for Windows (https://git-scm.com/download/win), which brings Git Bash, or put bash.exe on PATH")

// ShellName returns the name of the shell at path: its file name, lowercased
// and without .exe on Windows, where file names ignore case.
func ShellName(path string) string {
	name := filepath.Base(path)
	if runtime.GOOS == "windows" {
		name = strings.TrimSuffix(strings.ToLower(name), ".exe")
	}
	return name
}

// DefaultShell returns the shell that the bash tool runs commands with: $SHELL
// when it names an executable POSIX shell, and otherwise the first executable
// of fallbackShells (/bin/zsh on macOS, then bash from PATH; Git Bash on
// Windows), or else /bin/sh, or "" on Windows, which has no shell of its own.
func DefaultShell() string {
	for _, c := range slices.Concat([]string{os.Getenv("SHELL")}, fallbackShells()) {
		if !slices.Contains(posixShells, ShellName(c)) {
			continue
		}
		if path, err := exec.LookPath(c); err == nil && usableShell(path) {
			return path
		}
	}
	return lastResortShell
}

// Shells holds the installed POSIX shells that commands can run with.
type Shells struct {
	// Default is the path of the shell used when a command names none, or ""
	// when none is installed.
	Default string
	byName  map[string]string
}

// FindShells looks up the installed POSIX shells. Default is DefaultShell, and
// the shell of the same name resolves to it rather than to the first match in
// PATH.
func FindShells() Shells {
	def := DefaultShell()
	byName := make(map[string]string, len(posixShells))
	for _, name := range posixShells {
		if path, err := exec.LookPath(name); err == nil && usableShell(path) {
			byName[name] = path
		}
	}
	if def != "" {
		byName[ShellName(def)] = def
	}
	return Shells{Default: def, byName: byName}
}

// Names returns the names of the installed shells in sorted order.
func (s Shells) Names() []string {
	return slices.Sorted(maps.Keys(s.byName))
}

// Path returns the path of the named shell. An empty name selects Default,
// and fails with ErrNoShell when there is none.
func (s Shells) Path(name string) (string, error) {
	if name == "" {
		if s.Default == "" {
			return "", ErrNoShell
		}
		return s.Default, nil
	}
	if path, ok := s.byName[name]; ok {
		return path, nil
	}
	return "", fmt.Errorf("shell not installed: %s", name)
}

// runShell runs command via shell -c and returns its combined output. When
// ctx is done or timeout elapses, the shell's whole process group is killed.
// Background processes that keep the output open delay the result by at most
// bashWaitDelay after the shell exits.
//
// Output beyond MaxLines lines or MaxBytes bytes keeps only its last lines,
// with a notice naming a temporary file that holds all of it.
//
// A command that exits nonzero, times out, or is cancelled yields an Output
// with IsError set and a trailing marker saying why, such as "[exit 3]". A
// successful command without output yields "[no output]". The error is
// non-nil only when the shell could not be run.
func runShell(ctx context.Context, shell, command string, timeout time.Duration) (agent.ToolOutput, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c := exec.CommandContext(runCtx, shell, "-c", command)
	buf := NewTailBuffer()
	c.Stdout = buf
	c.Stderr = buf
	c.WaitDelay = bashWaitDelay
	killProcessGroupOnCancel(c)

	runErr := c.Run()

	out := buf.Text()

	switch {
	case runErr == nil, errors.Is(runErr, exec.ErrWaitDelay):
		if out == "" {
			out = "[no output]"
		}
		return agent.ToolOutput{Text: out}, nil
	case ctx.Err() != nil:
		return agent.FailedToolRun(out, "cancelled"), nil
	case runCtx.Err() != nil:
		return agent.FailedToolRun(out, "timeout after "+FormatSeconds(timeout)), nil
	}
	if ee, ok := errors.AsType[*exec.ExitError](runErr); ok {
		return agent.FailedToolRun(out, fmt.Sprintf("exit %d", ee.ExitCode())), nil
	}
	return agent.ToolOutput{}, runErr
}
