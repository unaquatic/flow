package message

import (
	"uuid"
)

// SessionID identifies a session, which package session stores.
type SessionID string

// NewSessionID returns a new session ID. Version 7 UUIDs lead with a
// timestamp, so the IDs sort in creation order.
func NewSessionID() SessionID {
	return SessionID(uuid.NewV7().String())
}
