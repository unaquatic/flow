package agent

import "encoding/json"

// StreamPartType identifies the kind of stream part.
type StreamPartType string

const (
	// StreamPartTextDelta identifies incremental agent text.
	StreamPartTextDelta StreamPartType = "text_delta"
	// StreamPartReasoningDelta identifies incremental reasoning text.
	StreamPartReasoningDelta StreamPartType = "reasoning_delta"
	// StreamPartReasoningSignature identifies the signature of a reasoning
	// block.
	StreamPartReasoningSignature StreamPartType = "reasoning_signature"
	// StreamPartToolUseStart identifies the start of a tool call.
	StreamPartToolUseStart StreamPartType = "tool_use_start"
	// StreamPartToolUseDelta identifies incremental tool arguments.
	StreamPartToolUseDelta StreamPartType = "tool_use_delta"
	// StreamPartToolUseEnd identifies the complete arguments of a tool call.
	StreamPartToolUseEnd StreamPartType = "tool_use_end"
	// StreamPartUsage identifies token counts.
	StreamPartUsage StreamPartType = "usage"
	// StreamPartStop identifies the end of a response.
	StreamPartStop StreamPartType = "stop"
)

// StreamPart is one typed item yielded by StreamResponse. The parts of a
// response are all there is of it: a consumer builds the agent message
// from them alone.
type StreamPart interface {
	streamPartType() StreamPartType
}

// Block identifies the provider content block that emitted a delta.
type Block int

// TextDelta is a chunk of agent text content.
type TextDelta struct {
	// Block identifies the source content block.
	Block Block
	// Text contains incremental agent text.
	Text string
}

func (TextDelta) streamPartType() StreamPartType { return StreamPartTextDelta }

// ReasoningDelta is a chunk of model reasoning content.
type ReasoningDelta struct {
	// Block identifies the source content block.
	Block Block
	// Text contains incremental reasoning text.
	Text string
}

func (ReasoningDelta) streamPartType() StreamPartType { return StreamPartReasoningDelta }

// ReasoningSignature carries the signature that verifies a reasoning block,
// which a provider may require to accept the block back in a later request.
// A block may have a signature and no text.
type ReasoningSignature struct {
	// Block identifies the reasoning block, as its deltas did.
	Block Block
	// Signature is the block's verification data, or its reasoning,
	// encrypted, when Redacted is set.
	Signature string
	// Redacted reports that the provider withheld the block's reasoning,
	// which streams no text, as Anthropic's redacted_thinking blocks do.
	Redacted bool
}

func (ReasoningSignature) streamPartType() StreamPartType { return StreamPartReasoningSignature }

// ToolUseStart begins a streamed tool invocation.
type ToolUseStart struct {
	// ID is the provider-assigned tool-call identifier.
	ID string
	// Name identifies the tool.
	Name string
}

func (ToolUseStart) streamPartType() StreamPartType { return StreamPartToolUseStart }

// ToolUseDelta contains incremental JSON input of one streamed tool call.
// Calls a response makes in parallel may stream their input interleaved, so
// each delta names its call.
type ToolUseDelta struct {
	// ID identifies the tool call, as its ToolUseStart did.
	ID string
	// JSON is a partial JSON fragment.
	JSON string
}

func (ToolUseDelta) streamPartType() StreamPartType { return StreamPartToolUseDelta }

// ToolUseEnd completes a streamed tool call with its whole input, which
// replaces the input its deltas built up. A provider yields it once per call,
// when the call's arguments are final, so a call whose arguments arrive in
// one piece at its end, with no deltas, still has them. A call that takes no
// arguments ends with an empty object, so a call without a ToolUseEnd is one
// whose input did not arrive in full.
type ToolUseEnd struct {
	// ID identifies the tool call, as its ToolUseStart did.
	ID string
	// Input is the call's complete JSON input.
	Input json.RawMessage
}

func (ToolUseEnd) streamPartType() StreamPartType { return StreamPartToolUseEnd }

// streamPartType lets Usage be yielded as a StreamPart.
func (Usage) streamPartType() StreamPartType { return StreamPartUsage }

// Stop signals the end of the model's response and carries the reason.
type Stop struct {
	// Reason describes why generation stopped.
	Reason StopReason
}

func (Stop) streamPartType() StreamPartType { return StreamPartStop }
