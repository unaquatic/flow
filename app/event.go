package app

import (
	"errors"
	"time"
	"unicode/utf8"

	"github.com/unaquatic/flow/agent"
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
	// Provider names the provider that answered with an error response, or
	// is empty for a failure of another kind.
	Provider agent.ProviderID `json:"provider,omitempty"`
	// Status is the HTTP status of the provider's error response, or zero.
	Status int `json:"status,omitempty"`
	// Body is the body of the provider's error response, as it sent it,
	// such as a JSON error object, or is empty.
	Body string `json:"body,omitempty"`
}

// NewUnknownError creates an unclassified session failure.
func NewUnknownError(message string) UnknownError {
	return UnknownError{Type: ErrorTypeUnknown, Message: message}
}

// MaxErrorBody is how many bytes of a provider's error response body an
// UnknownError holds, which is ample for an error object, while an error
// page, which some gateways answer with, is cut short.
const MaxErrorBody = 16 << 10

// ErrorOf returns the unclassified session failure of err, whose Message is
// err's, and which, when err is a provider's error response, as an
// *agent.ResponseError, also holds the provider, the status and the body it
// answered with, cut to MaxErrorBody bytes, at a rune boundary.
func ErrorOf(err error) UnknownError {
	e := NewUnknownError(err.Error())
	if resp, ok := errors.AsType[*agent.ResponseError](err); ok {
		e.Provider, e.Status, e.Body = agent.ProviderID(resp.Provider), resp.StatusCode, cutBody(resp.ResponseBody, MaxErrorBody)
	}
	return e
}

// cutBody returns body, or its first n bytes or fewer, ending at a rune
// boundary, when it is longer.
func cutBody(body string, n int) string {
	if len(body) <= n {
		return body
	}
	for n > 0 && !utf8.RuneStart(body[n]) {
		n--
	}
	return body[:n]
}

// eventBase returns a process-level event stamp.
func (a *App) eventBase() EventBase {
	return EventBase{Timestamp: time.Now().UTC()}
}

// publish sends an event to the app broker.
func (a *App) publish(ev Event) {
	a.broker.Publish(ev)
}
