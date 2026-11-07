package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
)

// Tools holds tools by name and executes provider tool calls.
type Tools struct {
	tools map[string]Tool
}

// NewTools returns an empty Tools.
func NewTools() *Tools {
	return &Tools{tools: make(map[string]Tool)}
}

// Register adds a tool, replacing any tool with the same name.
func (s *Tools) Register(t Tool) {
	slog.Debug("register tool", "name", t.Name)
	s.tools[t.Name] = t
}

// Get returns the named tool and reports whether it exists.
func (s *Tools) Get(name string) (Tool, bool) {
	t, ok := s.tools[name]
	return t, ok
}

// List returns the registered tools in unspecified order.
func (s *Tools) List() []Tool {
	return slices.Collect(maps.Values(s.tools))
}

// WireTools returns model-visible definitions sorted by name. The tools
// lead the prompt, so a stable order keeps the prefix that providers cache
// the same from one request to the next.
func (s *Tools) WireTools() []ToolDefinition {
	out := make([]ToolDefinition, 0, len(s.tools))
	for _, name := range slices.Sorted(maps.Keys(s.tools)) {
		out = append(out, s.tools[name].WireTool())
	}
	return out
}

// Execute decodes call arguments and invokes the named tool. Lookup, decoding,
// and execution failures, and failed runs, are returned in a ToolResult with
// IsError set. A failure's Content is its message, then the marker
// "[tool execution failed]" after a blank line, as a failed run's output ends
// with a marker such as "[exit 1]".
func (s *Tools) Execute(ctx context.Context, call ToolCall) ToolResult {
	tool, ok := s.Get(call.Name)
	if !ok {
		slog.Error("run tool", "name", call.Name, "err", ErrToolNotFound)
		return ToolFailure(call.ID, fmt.Sprintf("%v: %s", ErrToolNotFound, call.Name))
	}

	var args map[string]any
	if len(call.Args) > 0 {
		if err := json.Unmarshal(call.Args, &args); err != nil {
			slog.Error("run tool", "name", call.Name, "err", fmt.Errorf("%w: %w", ErrInvalidArgs, err))
			return ToolFailure(call.ID, ToolErrorText(err))
		}
	}

	out, err := tool.Execute(ctx, args)
	if err != nil {
		slog.Error("run tool", "name", call.Name, "err", fmt.Errorf("%w: %w", ErrExecutionFailed, err))
		return ToolFailure(call.ID, ToolErrorText(err))
	}

	slog.Debug("run tool", "name", call.Name, "result_len", len(out.Text), "is_error", out.IsError)
	return ToolResult{
		CallID:  call.ID,
		Content: out.Text,
		Title:   out.Title,
		IsError: out.IsError,
	}
}

// ToolFailure returns the result of call callID that failed with message: the
// message, then the marker "[tool execution failed]" after a blank line.
func ToolFailure(callID, message string) ToolResult {
	return ToolResult{
		CallID:  callID,
		Content: message + "\n\n[" + ErrExecutionFailed.Error() + "]",
		IsError: true,
	}
}

// FailedToolRun returns out marked as a failed run, followed by note in
// brackets after a blank line, or the note alone when there is no output. A
// tool whose run fails returns it, as the bash tool does for a command that
// exits nonzero, so that the run's output ends with a marker as a
// ToolFailure does.
func FailedToolRun(out, note string) ToolOutput {
	out = strings.TrimRight(out, "\n")
	if out == "" {
		return ToolOutput{Text: "[" + note + "]", IsError: true}
	}
	return ToolOutput{Text: out + "\n\n[" + note + "]", IsError: true}
}
