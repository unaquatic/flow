package builtin

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"github.com/unaquatic/flow/agent"
)

// bashWaitDelay bounds how long background processes may keep a command's
// output open after the shell exits or is killed.
const bashWaitDelay = time.Second

// posixShells lists, by file name, the shells that accept POSIX sh syntax and
// the -c flag.
var posixShells = []string{"bash", "dash", "ksh", "sh", "zsh"}

// DefaultShell returns the shell that the bash tool runs commands with: $SHELL
// when it names an executable POSIX shell, and otherwise the first executable
// of fallbackShells (/bin/zsh on macOS, then bash from PATH), or /bin/sh.
func DefaultShell() string {
	for _, c := range slices.Concat([]string{os.Getenv("SHELL")}, fallbackShells) {
		if !slices.Contains(posixShells, filepath.Base(c)) {
			continue
		}
		if path, err := exec.LookPath(c); err == nil {
			return path
		}
	}
	return "/bin/sh"
}

// Shells holds the installed POSIX shells that commands can run with.
type Shells struct {
	// Default is the path of the shell used when a command names none.
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
		if path, err := exec.LookPath(name); err == nil {
			byName[name] = path
		}
	}
	byName[filepath.Base(def)] = def
	return Shells{Default: def, byName: byName}
}

// Names returns the names of the installed shells in sorted order.
func (s Shells) Names() []string {
	return slices.Sorted(maps.Keys(s.byName))
}

// Path returns the path of the named shell. An empty name selects Default.
func (s Shells) Path(name string) (string, error) {
	if name == "" {
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
