package agent

import (
	"context"

	"github.com/unaquatic/flow/agent/message"
)

// PartSink takes the parts a hook adds to a message, as part events start
// and end a part: Start once as a part begins, and End once with its final
// form, which has the same ID. Both are called in the hook's goroutine, and
// the hook ends each part it starts before it returns.
type PartSink struct {
	// Start adds a part to the end of the message.
	Start func(message.ContentPart)
	// End replaces the part that Start added with the same ID by its final
	// form.
	End func(message.ContentPart)
}

// Turn is an agent turn that is ending, before its message is stored.
type Turn struct {
	// SessionID is the session of the turn, or empty for a run outside a
	// session.
	SessionID message.SessionID
	// MessageID identifies the turn's agent message.
	MessageID message.ID
	// Model is the model variant that produced the message, in
	// "provider/model:option:effort" form.
	Model string
	// Stopped reports that the run was stopped during the turn.
	Stopped bool
	// Err is the failure that ended the turn, or nil when it completed or
	// was stopped.
	Err error
}

// TurnHook adds parts to each agent message as its turn ends, however it
// ends, before the message is stored. The extensions that implement
// extension.TurnHook are turn hooks.
type TurnHook interface {
	// OnTurnEnd adds parts to the message of turn t through sink. ctx
	// carries the run's values, but stopping the run does not end it; t
	// tells whether it was stopped.
	OnTurnEnd(ctx context.Context, t Turn, sink PartSink)
}
