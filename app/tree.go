package app

import (
	"errors"
	"slices"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
)

// Tree is the whole of a session's history: every branch, and the point on
// one of them that the session's transcript ends at.
type Tree struct {
	// Messages are every message of the session, on every branch, in the
	// order they were stored. Each names the message it continues, which
	// precedes it.
	Messages []message.Content
	// Head is the last message the session's transcript shows, or empty when
	// it shows none.
	Head message.ID
}

// Tree returns the session's whole history. Like Branches, it reads without
// holding the session, so it stays answerable while a run is in progress.
func (s Session) Tree() (Tree, error) {
	// The head comes first, so the messages read next include it even when a
	// run appends meanwhile.
	h, err := s.head()
	if err != nil {
		return Tree{}, err
	}
	msgs, err := s.app.sessions.Messages(s.ctx, s.id)
	if err != nil {
		return Tree{}, err
	}
	return Tree{Messages: msgs, Head: h.ID}, nil
}

// ErrAlreadyThere reports that a move would leave the session where it is.
var ErrAlreadyThere = errors.New("the session is already there")

// Navigate moves the session so that its transcript ends at messageID, on
// whichever branch holds it, and resets the workspace to the state after it.
// The next prompt, or Continue, picks up from messageID.
//
// A move along the branch the session is on keeps the rest of that branch
// ahead, for Redo to return to. A move onto another branch is sideways, like
// a branch switch, and leaves nothing ahead. A move to where the session
// already is changes nothing, and reports ErrAlreadyThere. An empty
// messageID reports [session.ErrMessageNotFound].
func (s Session) Navigate(messageID message.ID) (MoveResult, error) {
	return s.change(func() (MoveResult, error) { return s.navigate(messageID) })
}

// navigate makes the move Navigate describes.
func (s Session) navigate(messageID message.ID) (MoveResult, error) {
	if messageID == "" {
		return MoveResult{}, session.ErrMessageNotFound
	}
	v, err := s.branchView()
	if err != nil {
		return MoveResult{}, err
	}
	if !slices.ContainsFunc(v.Messages, sameID(messageID)) {
		branch, _, err := s.branchThrough(messageID)
		if err != nil {
			return MoveResult{}, err
		}
		return s.moveTo(branch, session.Head{ID: messageID})
	}
	h := session.Head{ID: messageID}
	if messageID != v.Leaf() {
		h.Forward = v.Leaf()
	}
	if h == v.Head {
		return MoveResult{}, ErrAlreadyThere
	}
	return s.moveTo(v.Messages, h)
}
