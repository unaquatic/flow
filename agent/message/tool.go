package message

import (
	"encoding/json"
	"time"

	"github.com/unaquatic/flow/internal/timestamp"
)

// ToolState is one state in a tool call's lifecycle.
type ToolState interface {
	toolStateMarker()
}

// ToolStateStatus is the discriminator carried in JSON.
type ToolStateStatus string

const (
	// ToolStatusPending identifies streaming tool input.
	ToolStatusPending ToolStateStatus = "pending"
	// ToolStatusRunning identifies active execution.
	ToolStatusRunning ToolStateStatus = "running"
	// ToolStatusCompleted identifies successful execution.
	ToolStatusCompleted ToolStateStatus = "completed"
	// ToolStatusError identifies failed execution.
	ToolStatusError ToolStateStatus = "error"
)

// ToolStatePending contains tool input while it is still streaming.
type ToolStatePending struct {
	// Input is accumulated raw JSON and may be empty or incomplete.
	Input json.RawMessage `json:"input,omitempty"`
}

func (ToolStatePending) toolStateMarker() {}

// ToolStateRunning contains finalized input for an active execution.
type ToolStateRunning struct {
	// Input contains finalized raw JSON arguments.
	Input json.RawMessage `json:"input"`
	// Output contains the output produced so far.
	Output string `json:"output,omitempty"`
	// StartedAt is when execution began.
	StartedAt time.Time `json:"started_at"`
}

func (ToolStateRunning) toolStateMarker() {}

// ToolStateCompleted is a successful terminal state.
type ToolStateCompleted struct {
	// Input contains finalized raw JSON arguments.
	Input json.RawMessage `json:"input"`
	// Output contains tool output.
	Output string `json:"output"`
	// Title is an optional human-readable summary.
	Title string `json:"title,omitempty"`
	// Metadata contains optional tool-specific result data.
	Metadata map[string]any `json:"metadata,omitempty"`
	// Attachments are files the tool returned for the model to see beside
	// Output, such as the image a read of an image file returns.
	Attachments []FilePart `json:"attachments,omitempty"`
	// StartedAt is when execution began.
	StartedAt time.Time `json:"started_at"`
	// EndedAt is when execution completed.
	EndedAt time.Time `json:"ended_at"`
}

func (ToolStateCompleted) toolStateMarker() {}

// ToolStateError is a failed terminal state.
type ToolStateError struct {
	// Input contains finalized or partial raw JSON arguments.
	Input json.RawMessage `json:"input"`
	// Error contains the failure message.
	Error string `json:"error"`
	// StartedAt is when execution began, if known.
	StartedAt time.Time `json:"started_at"`
	// EndedAt is when execution failed, if known.
	EndedAt time.Time `json:"ended_at"`
}

func (ToolStateError) toolStateMarker() {}

// ToolPart combines a provider tool call with its current execution state.
type ToolPart struct {
	// ID identifies the content part.
	ID PartID `json:"id,omitempty"`
	// CallID is the provider-assigned tool-call identifier.
	CallID string `json:"call_id"`
	// Name identifies the tool.
	Name string `json:"name"`
	// Metadata is the protocol's data about the call, such as who made it,
	// which goes back with the call to the model that made it.
	Metadata ProtocolMetadata `json:"metadata,omitempty"`
	// State contains the current lifecycle state.
	State ToolState `json:"state"`
	// CacheControl contains provider prompt-cache hints.
	CacheControl *CacheControl `json:"cache_control,omitempty"`
}

func (ToolPart) PartType() ContentType { return TypeTool }
func (p ToolPart) PartID() PartID      { return p.ID }

// WithState returns a copy of the part with State replaced.
func (p ToolPart) WithState(state ToolState) ToolPart {
	p.State = state
	return p
}

// IsTerminal reports whether the state is completed or failed.
func (t ToolPart) IsTerminal() bool {
	switch t.State.(type) {
	case ToolStateCompleted, ToolStateError:
		return true
	}
	return false
}

// InterruptedError is the error of a tool call that a stop, a failure, or the
// end of a previous process left unfinished.
const InterruptedError = "execution interrupted"

// Input returns the input stored in the current state.
func (t ToolPart) Input() json.RawMessage {
	switch s := t.State.(type) {
	case ToolStatePending:
		return s.Input
	case ToolStateRunning:
		return s.Input
	case ToolStateCompleted:
		return s.Input
	case ToolStateError:
		return s.Input
	}
	return nil
}

// ToolStateJSON is the tagged JSON representation of a ToolState.
type ToolStateJSON struct {
	// Status selects the concrete state.
	Status ToolStateStatus `json:"status"`
	// Input contains raw tool arguments.
	Input json.RawMessage `json:"input,omitempty"`
	// PartialInput contains tool arguments that are not valid JSON, which
	// Input cannot hold: those of a call still streaming, or that a stop or
	// a malformed request left incomplete.
	PartialInput string `json:"partial_input,omitempty"`
	// Output contains tool output, so far while the tool is running.
	Output string `json:"output,omitempty"`
	// Title contains an optional result summary.
	Title string `json:"title,omitempty"`
	// Metadata contains state-specific data.
	Metadata map[string]any `json:"metadata,omitempty"`
	// Attachments are the files a completed tool returned.
	Attachments []FilePart `json:"attachments,omitempty"`
	// Error contains a failure message.
	Error string `json:"error,omitempty"`
	// StartedAt is when execution began, in timestamp.Layout, as the
	// stored parts and the API write it.
	StartedAt timestamp.Time `json:"started_at"`
	// EndedAt is when execution completed or failed, in timestamp.Layout.
	EndedAt timestamp.Time `json:"ended_at"`
}

func (s ToolStateJSON) decode() ToolState {
	started, ended := time.Time(s.StartedAt), time.Time(s.EndedAt)
	if s.PartialInput != "" {
		s.Input = json.RawMessage(s.PartialInput)
	}
	switch s.Status {
	case ToolStatusPending:
		return ToolStatePending{Input: s.Input}
	case ToolStatusRunning:
		return ToolStateRunning{Input: s.Input, Output: s.Output, StartedAt: started}
	case ToolStatusCompleted:
		return ToolStateCompleted{
			Input:       s.Input,
			Output:      s.Output,
			Title:       s.Title,
			Metadata:    s.Metadata,
			Attachments: s.Attachments,
			StartedAt:   started,
			EndedAt:     ended,
		}
	case ToolStatusError:
		return ToolStateError{
			Input:     s.Input,
			Error:     s.Error,
			StartedAt: started,
			EndedAt:   ended,
		}
	}
	return ToolStatePending{}
}

// encodeToolState returns the tagged form of state. Input that is not valid
// JSON is kept as PartialInput.
func encodeToolState(state ToolState) ToolStateJSON {
	j := toolStateJSON(state)
	if len(j.Input) > 0 && !json.Valid(j.Input) {
		j.PartialInput, j.Input = string(j.Input), nil
	}
	return j
}

func toolStateJSON(state ToolState) ToolStateJSON {
	switch v := state.(type) {
	case ToolStatePending:
		return ToolStateJSON{Status: ToolStatusPending, Input: v.Input}
	case ToolStateRunning:
		return ToolStateJSON{Status: ToolStatusRunning, Input: v.Input, Output: v.Output, StartedAt: timestamp.Time(v.StartedAt)}
	case ToolStateCompleted:
		return ToolStateJSON{
			Status:      ToolStatusCompleted,
			Input:       v.Input,
			Output:      v.Output,
			Title:       v.Title,
			Metadata:    v.Metadata,
			Attachments: v.Attachments,
			StartedAt:   timestamp.Time(v.StartedAt),
			EndedAt:     timestamp.Time(v.EndedAt),
		}
	case ToolStateError:
		return ToolStateJSON{
			Status:    ToolStatusError,
			Input:     v.Input,
			Error:     v.Error,
			StartedAt: timestamp.Time(v.StartedAt),
			EndedAt:   timestamp.Time(v.EndedAt),
		}
	}
	return ToolStateJSON{Status: ToolStatusPending}
}
