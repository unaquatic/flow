package builtin

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"time"

	"github.com/unaquatic/flow/agent"
)

// DefaultBashTimeout applies when a call omits timeout_ms.
const DefaultBashTimeout = 60 * time.Second

// MaxBashTimeout limits one shell command.
const MaxBashTimeout = time.Hour

// Bash runs a shell command with DefaultShell, or with another installed
// POSIX shell that the call names. The shells are found when Bash is
// called. Where none is installed, as on Windows without Git for Windows,
// a call fails with ErrNoShell.
func Bash() agent.Tool {
	shells := FindShells()
	description := fmt.Sprintf("Run a shell command and return its combined stdout and stderr. Output is truncated to the last %d lines or %dKB (whichever is hit first). If truncated, the full output is saved to a temp file.", MaxLines, MaxBytes/1024)
	if runtime.GOOS == "windows" {
		description += " Commands run in Git Bash on Windows: use POSIX syntax, and forward slashes in Windows paths, such as C:/Users/name."
	}
	shellDescription := "Optional shell to run the command with."
	if shells.Default != "" {
		shellDescription += fmt.Sprintf(" Defaults to %s.", ShellName(shells.Default))
	}
	return agent.Tool{
		Name:        "bash",
		Description: description,
		Schema: agent.InputSchema{
			Type: "object",
			Properties: map[string]agent.PropertySchema{
				"command":     {Type: "string", Description: "Shell command to execute."},
				"description": {Type: "string", Description: "Clear, concise description of what the command does in 5-10 words, shown to the user. Examples: \"Lists files in current directory\", \"Runs the calculator tests\"."},
				"shell":       {Type: "string", Description: shellDescription, Enum: shells.Names()},
				"timeout_ms":  {Type: "integer", Description: fmt.Sprintf("Optional per-command timeout in milliseconds. Defaults to %d, capped at %d.", DefaultBashTimeout.Milliseconds(), MaxBashTimeout.Milliseconds())},
			},
			Required: []string{"command", "description"},
		},
		Execute: func(ctx context.Context, args map[string]any) (agent.ToolOutput, error) {
			cmd, err := stringArg(args, "command")
			if err != nil {
				return agent.ToolOutput{}, err
			}

			var name string
			if v, ok := args["shell"].(string); ok {
				name = v
			}
			shell, err := shells.Path(name)
			if err != nil {
				return agent.ToolOutput{}, err
			}

			return runShell(ctx, shell, cmd, BashTimeout(args))
		},
	}
}

// BashTimeout returns the timeout a bash call with args runs under: its
// positive timeout_ms capped at MaxBashTimeout, or DefaultBashTimeout.
func BashTimeout(args map[string]any) time.Duration {
	ms, ok := NumberAsInt64(args["timeout_ms"])
	if !ok || ms <= 0 {
		return DefaultBashTimeout
	}
	// Cap before converting, so that a huge value cannot overflow.
	return time.Duration(min(ms, MaxBashTimeout.Milliseconds())) * time.Millisecond
}

// FormatSeconds formats d in seconds, as "120s" or "1.5s", as a bash
// call's timeout is shown.
func FormatSeconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', -1, 64) + "s"
}

// NumberAsInt64 converts supported numeric values and decimal strings to int64.
// JSON numbers decoded into map[string]any arrive as float64.
func NumberAsInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	case int64:
		return n, true
	case string:
		i, err := strconv.ParseInt(n, 10, 64)
		if err == nil {
			return i, true
		}
	}
	return 0, false
}
