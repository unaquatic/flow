package agent

import (
	"time"

	"github.com/unaquatic/flow/agent/message"
)

// Event is what an Agent reports as it runs, to its OnEvent, in order. Each
// turn's agent message is opened by MessageStarted, gets its parts
// through PartStarted, PartUpdated, and PartEnded, and is closed by
// MessageEnded. A request for it that fails and is sent again is reported by
// Retrying, before its first part.
type Event interface {
	agentEvent()
}

// Retrying reports that the request for the message in progress failed with
// a transient error, as Classify judges it, and that the agent sends it again
// once Delay has passed. Each failed attempt but the last is reported; the
// last one's error ends the message.
type Retrying struct {
	// MessageID identifies the message in progress.
	MessageID message.ID
	// Attempt is the number of the attempt that failed, from 1.
	Attempt int
	// MaxAttempts is the number of attempts the agent's RetryPolicy allows.
	MaxAttempts int
	// Delay is how long the agent waits before the next attempt.
	Delay time.Duration
	// Reason is why the attempt failed, in a few words, as Classify gives
	// it, such as "rate limited".
	Reason string
	// Err is the failure.
	Err error
}

// MessageStarted opens the agent message of a turn. The message starts
// empty, before the model is asked for it.
type MessageStarted struct {
	// Message is the message, with no parts.
	Message message.Content
}

// PartChange is a part of the message in progress, as the part events carry
// it. Each event holds the whole part, which replaces the one with the same
// ID.
type PartChange struct {
	// MessageID identifies the message the part belongs to.
	MessageID message.ID
	// Part is the part as it is now.
	Part message.ContentPart
}

// PartStarted adds a part to the message in progress: the first chunk of a
// text or reasoning block, a tool call the model began, the usage of the
// response, or a part a turn hook adds.
type PartStarted struct{ PartChange }

// PartUpdated replaces a part of the message in progress: a text or
// reasoning block that grew, the arguments of a tool call as they stream, or
// a tool call that began running.
type PartUpdated struct{ PartChange }

// PartEnded replaces a part of the message in progress with its final form.
// Text and reasoning end when the response stops streaming, a tool call when
// it completes, fails, or is interrupted, and usage as soon as it starts.
// Every PartStarted is followed by exactly one.
type PartEnded struct{ PartChange }

// MessageEnded closes the message with its final content, whether it
// finished, was stopped, or failed. Every MessageStarted is followed by
// exactly one.
type MessageEnded struct {
	// Message is the message with its final parts, and, once it is stored,
	// its parent and creation time.
	Message message.Content
	// Finish is why the model stopped, or empty for a response that did not
	// get as far.
	Finish StopReason
	// Stopped reports that the run was stopped before the message ended.
	Stopped bool
	// Err is why the message failed, or why a message that finished or was
	// stopped could not be stored. It is nil otherwise.
	Err error
}

func (MessageStarted) agentEvent() {}
func (PartStarted) agentEvent()    {}
func (PartUpdated) agentEvent()    {}
func (PartEnded) agentEvent()      {}
func (MessageEnded) agentEvent()   {}
func (Retrying) agentEvent()       {}
