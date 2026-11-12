package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"

	"github.com/unaquatic/flow/agent/message"
)

// ToolDefinition is a model-visible tool definition without a local
// executor, as a provider sends it to the model and a message.ToolSetPart
// stores it.
type ToolDefinition = message.ToolDefinition

// InputSchema describes a tool's JSON input, as message.InputSchema does.
type InputSchema = message.InputSchema

// PropertySchema describes a single parameter in the input schema, as
// message.PropertySchema does.
type PropertySchema = message.PropertySchema

// ToolsIn returns the tools in force at the end of history, those of the
// message.ToolSetPart of its last system message that holds one, and reports
// whether one does. A system message without one, as one of text alone,
// leaves the tools as they were.
func ToolsIn(history []Message) ([]ToolDefinition, bool) {
	for _, m := range slices.Backward(history) {
		if m.Role != message.RoleSystem {
			continue
		}
		if p, ok := message.LastPart[message.ToolSetPart](m.Content); ok {
			return p.Tools, true
		}
	}
	return nil, false
}

// SameTools reports whether a and b define the same tools, in the same
// order, as a provider is sent them: a field one leaves empty and the other
// sets to an empty list or map is the same.
func SameTools(a, b []ToolDefinition) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(ja, jb)
}

// Tool describes a model-visible tool and its local implementation.
type Tool struct {
	// Name identifies the tool to the model.
	Name string
	// Description explains when and how the model should use the tool.
	Description string
	// Schema describes accepted arguments.
	Schema InputSchema
	// Execute runs the local implementation. It returns an error only when
	// the tool cannot do its work, such as for invalid arguments; a run that
	// ends in failure, such as a command exiting nonzero, is a ToolOutput with
	// IsError set.
	Execute func(ctx context.Context, args map[string]any) (ToolOutput, error)
}

// ToolOutput is what a tool run produced.
type ToolOutput struct {
	// Text is the output shown to the model.
	Text string
	// Title is an optional human-readable summary.
	Title string
	// IsError reports whether the run failed. Text then describes the
	// failure along with any output produced before it.
	IsError bool
	// Attachments are files the run returns for the model to see beside
	// Text, such as the image a read of an image file returns. A failed
	// run's are dropped.
	Attachments []message.FilePart
}

// WireTool returns the model-visible definition without the local executor.
func (t Tool) WireTool() ToolDefinition {
	return ToolDefinition{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: t.Schema,
	}
}

// ToolCall is a provider request to invoke a tool.
type ToolCall struct {
	// ID is the provider-assigned call identifier.
	ID string
	// Name identifies the requested tool.
	Name string
	// Args contains the raw JSON arguments.
	Args json.RawMessage
}

// ToolResult contains the displayable outcome of a tool call.
type ToolResult struct {
	// CallID identifies the corresponding ToolCall.
	CallID string
	// Content contains output or an error message.
	Content string
	// Title is an optional human-readable summary.
	Title string
	// IsError reports whether Content contains an error message.
	IsError bool
	// Attachments are the files the tool returned beside Content.
	Attachments []message.FilePart
}
