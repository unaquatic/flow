package app

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
)

// ErrNoBranches indicates that the session's history has no alternatives to
// choose between.
var ErrNoBranches = errors.New("this session has no branches")

// summaryLimit bounds a branch summary, which labels one entry in a picker.
const summaryLimit = 72

// Branch describes one alternative continuation at a fork in a session's
// history.
type Branch struct {
	// MessageID identifies the message that opens the branch.
	MessageID message.ID `json:"message_id"`
	// Position is the branch's one-based place among its siblings.
	Position int `json:"position"`
	// Summary is the first line of text in the opening message.
	Summary string `json:"summary"`
	// Messages counts the opening message and everything below it.
	Messages int `json:"messages"`
	// CreatedAt is when the branch was opened.
	CreatedAt time.Time `json:"created_at"`
	// Active reports whether the session is currently on this branch.
	Active bool `json:"active"`
}

// Fork is the set of alternatives available at one point in a session's
// history. It encodes as the JSON of the HTTP API's branch list.
type Fork struct {
	// Parent identifies the message the alternatives continue. It is empty
	// when they are the session's root messages.
	Parent message.ID `json:"parent,omitempty"`
	// Branches are the alternatives in creation order. A fork that
	// [Session.Branches] returns has at least two.
	Branches []Branch `json:"branches"`
}

// Branches returns the newest fork on the session's visible branch, which is
// the most recent point the conversation could have gone another way.
//
// It reads without holding the session, so it stays answerable while a run
// is in progress.
func (s Session) Branches() (Fork, error) {
	v, err := s.branchView()
	if err != nil {
		return Fork{}, err
	}
	return s.forkOn(v.Visible())
}

// SwitchBranch moves the session onto the branch opened by messageID.
func (s Session) SwitchBranch(messageID message.ID) ([]FileDiff, error) {
	return s.change(func() ([]FileDiff, error) { return s.switchBranch(messageID) })
}

// forkOn returns the newest fork among the messages of one branch.
func (s Session) forkOn(branch []message.Content) (Fork, error) {
	forks, err := s.app.sessions.Forks(s.ctx, s.id)
	if err != nil {
		return Fork{}, err
	}
	if len(forks) == 0 {
		return Fork{}, ErrNoBranches
	}
	for _, m := range slices.Backward(branch) {
		parent := parentID(m)
		if forks[parent] < 2 {
			continue
		}
		return s.describeFork(parent, m.ID)
	}
	return Fork{}, ErrNoBranches
}

// describeFork labels the children of parent, marking the one on the active
// branch.
func (s Session) describeFork(parent, active message.ID) (Fork, error) {
	siblings, err := s.app.sessions.Children(s.ctx, s.id, parent)
	if err != nil {
		return Fork{}, err
	}
	branches := make([]Branch, 0, len(siblings))
	for i, m := range siblings {
		size, err := s.app.sessions.Size(s.ctx, s.id, m.ID)
		if err != nil {
			return Fork{}, err
		}
		branches = append(branches, Branch{
			MessageID: m.ID,
			Position:  i + 1,
			Summary:   summarize(m),
			Messages:  size,
			CreatedAt: m.CreatedAt,
			Active:    m.ID == active,
		})
	}
	return Fork{Parent: parent, Branches: branches}, nil
}

// branchPositions locates every message of a branch that sits at a fork. It
// returns nil when the session has no forks, which is the common case.
func (s Session) branchPositions(branch []message.Content) (map[message.ID]session.Position, error) {
	forks, err := s.app.sessions.Forks(s.ctx, s.id)
	if err != nil {
		return nil, err
	}
	if len(forks) == 0 {
		return nil, nil
	}
	out := make(map[message.ID]session.Position)
	for _, m := range branch {
		parent := parentID(m)
		n := forks[parent]
		if n < 2 {
			continue
		}
		siblings, err := s.app.sessions.Children(s.ctx, s.id, parent)
		if err != nil {
			return nil, err
		}
		for i, sibling := range siblings {
			out[sibling.ID] = session.Position{Index: i + 1, Of: n}
		}
	}
	return out, nil
}

// parentID returns the message a message continues, or an empty ID at a root.
func parentID(m message.Content) message.ID {
	if m.ParentID == nil {
		return ""
	}
	return *m.ParentID
}

// summarize returns a one-line label for a message.
func summarize(m message.Content) string {
	for _, part := range m.Parts {
		text, ok := part.(message.TextPart)
		if !ok {
			continue
		}
		line, _, _ := strings.Cut(strings.TrimSpace(text.Text), "\n")
		if line == "" {
			continue
		}
		return truncate(line, summaryLimit)
	}
	return string(m.ID)
}

// truncate shortens s to at most n runes, marking a cut with an ellipsis.
// Ranging over a string yields the byte offset of each rune, so the cut never
// lands inside one.
func truncate(s string, n int) string {
	count := 0
	for i := range s {
		if count == n {
			return strings.TrimSpace(s[:i]) + "..."
		}
		count++
	}
	return s
}
