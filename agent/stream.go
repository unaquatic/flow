package agent

import (
	"encoding/json"

	"github.com/unaquatic/flow/agent/message"
)

// StreamPartType identifies the kind of stream part.
type StreamPartType string

const (
	// StreamPartTextDelta identifies incremental agent text.
	StreamPartTextDelta StreamPartType = "text_delta"
	// StreamPartReasoningDelta identifies incremental reasoning text.
	StreamPartReasoningDelta StreamPartType = "reasoning_delta"
	// StreamPartReasoningMetadata identifies the protocol data of a
	// reasoning block.
	StreamPartReasoningMetadata StreamPartType = "reasoning_metadata"
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

// ReasoningMetadata carries the protocol's data about a reasoning block,
// such as the signature that verifies it or its reasoning, encrypted, which
// a provider may require to accept the block back in a later request. A
// block may have metadata and no text, as one whose reasoning the provider
// withheld has. Each ReasoningMetadata of a block sets its fields over
// those an earlier one set.
type ReasoningMetadata struct {
	// Block identifies the reasoning block, as its deltas did.
	Block Block
	// Metadata is the block's protocol data.
	Metadata message.ProtocolMetadata
}

func (ReasoningMetadata) streamPartType() StreamPartType { return StreamPartReasoningMetadata }

// ToolUseStart begins a streamed tool invocation.
type ToolUseStart struct {
	// ID is the provider-assigned tool-call identifier.
	ID string
	// Name identifies the tool.
	Name string
	// Metadata is the protocol's data about the call, such as who made it,
	// which the call's part keeps to send back. It is nil when there is
	// none.
	Metadata message.ProtocolMetadata
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
