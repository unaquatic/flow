package app

import (
	"time"

	"github.com/unaquatic/flow/agent/message"
)

// EventType identifies an event variant.
type EventType string

// Event is implemented by every app event. Events are Go values, which the
// terminal UI reads as they are, and which encode as the JSON the server
// streams, after their EventType. Their JSON is part of the HTTP API, which
// testdata/events.golden records.
type Event interface {
	event()
	// EventType identifies the concrete event.
	EventType() EventType
	// Base returns shared event metadata.
	Base() EventBase
}

// EventBase contains metadata shared by events, which every event's JSON
// starts with. It must not implement json.Marshaler, which every event would
// take as its own.
type EventBase struct {
	// SessionID identifies the related session and may be empty.
	SessionID message.SessionID `json:"session_id"`
	// Timestamp records when the event was created.
	Timestamp time.Time `json:"timestamp"`
}

func (EventBase) event() {}

// Base returns the event's shared stamp.
func (e EventBase) Base() EventBase { return e }

// ErrorType identifies a session failure variant.
type ErrorType string

// ErrorTypeUnknown identifies an unclassified error.
const ErrorTypeUnknown ErrorType = "unknown"

// UnknownError is the common error shape for unclassified agent and tool
// failures.
type UnknownError struct {
	// Type identifies the error category.
	Type ErrorType `json:"type"`
	// Message contains the failure detail.
	Message string `json:"message"`
}

// NewUnknownError creates an unclassified session failure.
func NewUnknownError(message string) UnknownError {
	return UnknownError{Type: ErrorTypeUnknown, Message: message}
}

// eventBase returns a process-level event stamp.
func (a *App) eventBase() EventBase {
	return EventBase{Timestamp: time.Now().UTC()}
}

// publish sends an event to the app broker.
func (a *App) publish(ev Event) {
	a.broker.Publish(ev)
}
