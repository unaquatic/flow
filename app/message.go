package app

import (
	"encoding/json"
	"time"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
)

// Message is a message at its position among the alternatives at a fork, as
// the session events carry it and the HTTP API writes it.
type Message struct {
	message.Content
	// Branch locates the message among the alternatives at a fork, and is
	// zero for a message that has no siblings or when it is not known, as
	// for a message still streaming.
	Branch session.Position
}

// MarshalJSON encodes the message with its part list in tagged form. It
// leaves out a parent, a branch position, and a creation time that are not
// known.
func (m Message) MarshalJSON() ([]byte, error) {
	parts, err := message.MarshalParts(m.Parts)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		ID        message.ID         `json:"id"`
		ParentID  *message.ID        `json:"parent_id,omitempty"`
		Role      message.Role       `json:"role"`
		Parts     json.RawMessage    `json:"parts"`
		Provider  gateway.ProviderID `json:"provider,omitempty"`
		Model     string             `json:"model,omitempty"`
		Branch    session.Position   `json:"branch,omitzero"`
		CreatedAt time.Time          `json:"created_at,omitzero"`
	}{m.ID, m.ParentID, m.Role, parts, m.Provider, m.Model, m.Branch, m.CreatedAt})
}

// Messages returns the messages of the session's active branch, from its
// root, each at its position among the alternatives at a fork. A session
// with no messages, such as one not stored yet, has an empty list.
func (s Session) Messages() ([]Message, error) {
	branch, err := s.app.sessions.Branch(s.ctx, s.id)
	if err != nil {
		return nil, err
	}
	return s.located(branch)
}

// Message returns one message of the session. Its position at a fork is
// known only while it is on the session's active branch.
func (s Session) Message(id message.ID) (Message, error) {
	m, err := s.app.sessions.Message(s.ctx, s.id, id)
	if err != nil {
		return Message{}, err
	}
	branch, err := s.app.sessions.Branch(s.ctx, s.id)
	if err != nil {
		return Message{}, err
	}
	at, err := s.branchPositions(branch)
	if err != nil {
		return Message{}, err
	}
	return Message{Content: *m, Branch: at[id]}, nil
}

// located returns the messages of branch, one of the session's branches,
// each at its position among the alternatives at a fork. The list is empty,
// not nil, for an empty branch, whose forks it does not read.
func (s Session) located(branch []message.Content) ([]Message, error) {
	out := make([]Message, 0, len(branch))
	if len(branch) == 0 {
		return out, nil
	}
	at, err := s.branchPositions(branch)
	if err != nil {
		return nil, err
	}
	for _, m := range branch {
		out = append(out, Message{Content: m, Branch: at[m.ID]})
	}
	return out, nil
}
