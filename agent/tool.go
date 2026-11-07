package agent

import (
	"context"
	"encoding/json"
)

// ToolDefinition is a model-visible tool definition without a local executor,
// as a provider sends it to the model.
type ToolDefinition struct {
	// Name identifies the tool.
	Name string
	// Description explains how the model should use the tool.
	Description string
	// InputSchema describes accepted arguments.
	InputSchema InputSchema
}

// InputSchema describes a tool's JSON input.
type InputSchema struct {
	// Type is the JSON Schema type.
	Type string `json:"type"`
	// Properties contains named argument schemas.
	Properties map[string]PropertySchema `json:"properties,omitempty"`
	// Required lists mandatory property names.
	Required []string `json:"required,omitempty"`
}

// PropertySchema describes a single parameter in the input schema.
type PropertySchema struct {
	// Type is the JSON Schema type.
	Type string `json:"type"`
	// Description explains the property to the model.
	Description string `json:"description,omitempty"`
	// Enum restricts accepted string values.
	Enum []string `json:"enum,omitempty"`
	// Items describes array elements.
	Items *PropertySchema `json:"items,omitempty"`
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
}
