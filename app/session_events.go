package app

import (
	"encoding/json"
	"time"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
)

const (
	// TypeMessageStarted identifies the start of a message.
	TypeMessageStarted EventType = "session.message.started"
	// TypePartStarted identifies a new part of a message in progress.
	TypePartStarted EventType = "session.part.started"
	// TypePartUpdated identifies a changed part of a message in progress.
	TypePartUpdated EventType = "session.part.updated"
	// TypePartEnded identifies a part of a message in progress becoming final.
	TypePartEnded EventType = "session.part.ended"
	// TypeMessageEnded identifies the end of a message.
	TypeMessageEnded EventType = "session.message.ended"
	// TypeAgentStarted identifies the start of an agent run.
	TypeAgentStarted EventType = "session.agent.started"
	// TypeAgentEnded identifies the end of an agent run.
	TypeAgentEnded EventType = "session.agent.ended"
	// TypeAgentRetrying identifies a failed request of an agent run that is
	// sent again.
	TypeAgentRetrying EventType = "session.agent.retrying"
	// TypeSessionLoaded identifies a transcript replacement.
	TypeSessionLoaded EventType = "session.loaded"
	// TypeSessionUpdated identifies a change to a session as the session
	// list shows it.
	TypeSessionUpdated EventType = "session.updated"
	// TypeSessionDeleted identifies the deletion of a session.
	TypeSessionDeleted EventType = "session.deleted"
)

// EventMessageStarted opens a message. A user prompt starts with its text,
// and its snapshot arrives through the part events. An agent message
// starts empty, before the model is asked for it, and its content arrives
// through the part events. Either is stored before it ends.
type EventMessageStarted struct {
	EventBase
	// Message is the message as it starts, whose branch position is not
	// known yet.
	Message Message `json:"message"`
}

func (EventMessageStarted) EventType() EventType { return TypeMessageStarted }

// PartChange is a part of a message in progress, as the part events carry
// it. Each event holds the whole part, which replaces the one
// with the same ID.
type PartChange struct {
	EventBase
	// MessageID identifies the message the part belongs to.
	MessageID message.ID
	// Part is the part as it is now.
	Part message.ContentPart
}

// MarshalJSON encodes the change with its part in tagged form. The part
// events encode as their PartChange.
func (c PartChange) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		EventBase
		MessageID message.ID       `json:"message_id"`
		Part      message.PartJSON `json:"part"`
	}{c.EventBase, c.MessageID, message.PartToJSON(c.Part)})
}

// EventPartStarted adds a part to a message in progress: the first chunk of a
// text or reasoning block, a tool call the model began, a snapshot, pending
// while it takes long, or the usage of the response.
type EventPartStarted struct{ PartChange }

func (EventPartStarted) EventType() EventType { return TypePartStarted }

// EventPartUpdated replaces a part of a message in progress: a text or
// reasoning block that grew, the arguments of a tool call as they stream, or
// a tool call that began running.
type EventPartUpdated struct{ PartChange }

func (EventPartUpdated) EventType() EventType { return TypePartUpdated }

// EventPartEnded replaces a part of a message in progress with its final
// form. Text and reasoning end when the response stops streaming, a tool call
// when it completes, fails, or is interrupted, a snapshot once the workspace
// is recorded, and usage as soon as it starts. Every EventPartStarted is
// followed by exactly one.
type EventPartEnded struct{ PartChange }

func (EventPartEnded) EventType() EventType { return TypePartEnded }

// EventMessageEnded closes a message with its final content, whether it
// finished, was stopped, or failed. Every EventMessageStarted is followed by
// exactly one.
type EventMessageEnded struct {
	EventBase
	// Message is the message with its final parts, and, once it is stored,
	// its parent and creation time. Its branch position is known for a
	// stored prompt.
	Message Message `json:"message"`
	// Finish is why the model stopped. It is empty for a user message, and
	// for a response that did not get as far.
	Finish agent.StopReason `json:"finish,omitempty"`
	// Stopped reports that the run was stopped before the message ended.
	Stopped bool `json:"stopped,omitempty"`
	// Error is why the message failed, or why a message that finished or
	// was stopped could not be stored. It is nil otherwise.
	Error *UnknownError `json:"error,omitempty"`
}

func (EventMessageEnded) EventType() EventType { return TypeMessageEnded }

// EventAgentStarted reports that the agent began a run: a queued prompt or
// demo started, before any of its messages. Every EventAgentStarted is
// followed by exactly one EventAgentEnded.
type EventAgentStarted struct {
	EventBase
}

func (EventAgentStarted) EventType() EventType { return TypeAgentStarted }

// EventAgentEnded reports that the agent's run settled: it finished, was
// stopped, or failed with Error.
type EventAgentEnded struct {
	EventBase
	// Error is why the run failed, or nil when it finished or was stopped.
	Error *UnknownError `json:"error,omitempty"`
}

func (EventAgentEnded) EventType() EventType { return TypeAgentEnded }

// EventAgentRetrying reports that the request for a message in progress
// failed with a transient error, and is sent again at RetryAt. It comes
// between the message's EventMessageStarted and its first part, once for each
// failed attempt but the last, whose error ends the message instead.
type EventAgentRetrying struct {
	EventBase
	// MessageID identifies the message in progress.
	MessageID message.ID `json:"message_id"`
	// Attempt is the number of the attempt that failed, from 1.
	Attempt int `json:"attempt"`
	// MaxAttempts is the number of attempts the run makes at most.
	MaxAttempts int `json:"max_attempts"`
	// RetryAt is when the request is sent again.
	RetryAt time.Time `json:"retry_at"`
	// Reason is why the attempt failed, in a few words, such as "rate
	// limited".
	Reason string `json:"reason"`
	// Error is the failure.
	Error UnknownError `json:"error"`
}

func (EventAgentRetrying) EventType() EventType { return TypeAgentRetrying }

// EventSessionLoaded contains the messages of a session's active branch, which
// replace a consumer's visible transcript.
type EventSessionLoaded struct {
	EventBase
	// Messages are the messages of the branch, from its root, each at its
	// position among the alternatives at a fork.
	Messages []Message `json:"messages"`
}

func (EventSessionLoaded) EventType() EventType { return TypeSessionLoaded }

// MarshalJSON encodes the event, with no messages as an empty list rather
// than null.
func (e EventSessionLoaded) MarshalJSON() ([]byte, error) {
	type plain EventSessionLoaded
	if e.Messages == nil {
		e.Messages = []Message{}
	}
	return json.Marshal(plain(e))
}

// EventSessionUpdated contains a session as the session list shows it, once
// that changed: when the session is stored, with its first prompt, and when
// its title changes. The messages it gains later, which move UpdatedAt, are
// reported by their own events.
type EventSessionUpdated struct {
	EventBase
	// Title is the session's title, or empty when it has none.
	Title string `json:"title,omitempty"`
	// CreatedAt is when the session was stored, with its first message.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the time of the session's latest message change.
	UpdatedAt time.Time `json:"updated_at"`
}

func (EventSessionUpdated) EventType() EventType { return TypeSessionUpdated }

// EventSessionDeleted reports that a session was deleted. No read returns it
// or its messages afterwards.
type EventSessionDeleted struct {
	EventBase
}

func (EventSessionDeleted) EventType() EventType { return TypeSessionDeleted }
