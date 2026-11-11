package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"syscall"
)

var (
	// ErrToolNotFound identifies an unregistered tool.
	ErrToolNotFound = errors.New("unknown tool")

	// ErrInvalidArgs identifies malformed tool arguments.
	ErrInvalidArgs = errors.New("invalid tool arguments")

	// ErrExecutionFailed identifies a tool execution error.
	ErrExecutionFailed = errors.New("tool execution failed")
)

// ToolErrorText returns a short message for err, as a tool result shows it
// to the model. It rewrites the errors of file-system calls, JSON decoding,
// starting a command and context ends in fixed words, the cause before the
// path, such as "not found: a.go" or "truncated json". It looks at err
// itself, not the errors it wraps, so a tool that adds its own context keeps
// it. Any other error keeps its text.
func ToolErrorText(err error) string {
	switch e := err.(type) {
	case *fs.PathError:
		return pathText(e.Path, e.Err)
	case *os.LinkError:
		return pathText(e.Old, e.Err)
	case *os.SyscallError:
		return causeText(e.Err)
	case *json.SyntaxError:
		if e.Error() == "unexpected end of JSON input" {
			return "truncated json"
		}
		return fmt.Sprintf("bad json at byte %d", e.Offset)
	case *json.UnmarshalTypeError:
		if e.Field == "" {
			return fmt.Sprintf("must be a json object, not %s", e.Value)
		}
		return fmt.Sprintf("%s must be %s, not %s", e.Field, e.Type, e.Value)
	case *exec.Error:
		return causeText(e.Err) + ": " + e.Name
	}
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timed out"
	}
	return err.Error()
}

// pathText describes cause, the error of a call on path, as the cause and
// then the path.
func pathText(path string, cause error) string {
	return causeText(cause) + ": " + path
}

// causeText names cause in fixed words where it is a common file-system or
// command failure, and returns its own text otherwise.
func causeText(cause error) string {
	switch {
	case errors.Is(cause, syscall.ENOTDIR):
		return "is not a directory"
	case errors.Is(cause, syscall.EISDIR):
		return "is a directory"
	case errors.Is(cause, fs.ErrNotExist), errors.Is(cause, exec.ErrNotFound):
		return "not found"
	case errors.Is(cause, fs.ErrPermission):
		return "permission denied"
	case errors.Is(cause, fs.ErrExist):
		return "already exists"
	}
	return cause.Error()
}
