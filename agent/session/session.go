// Package session stores conversation sessions: the tree of messages each
// session holds, and the head that says which branch of it the session
// shows.
//
// It keeps its data as an extension does, in its own tables of the app's
// database, which [Open] brings up to date with database.Migrate under
// [Name]. It is no extension, though: the app cannot run without it, so it
// loads no settings and cannot be turned off.
//
// The messages themselves, and the IDs of sessions, are package message's,
// which the agent and the providers use without the database. The agent
// stores the messages of its turns through agent.MessageStore, which [Store]
// implements.
package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/database"
)

// Name is the ID the database's migrations table records the package's
// migrations under, as it records those of an extension under the
// extension's name.
const Name = "session"

// ErrMessageNotFound reports a message that a session does not hold.
var ErrMessageNotFound = errors.New("no such message in this session")

// ErrMessageType reports a message stored where its type does not belong:
// one that is not chat in the conversation, or a chat one in a fork.
var ErrMessageType = errors.New("message of the wrong type")

// Open creates the session tables of db, which must have been opened by
// [database.Open], or brings them up to date with [Migrations], as Migrate
// does, and returns a store working on behalf of project in it. Closing db
// is up to the caller.
func Open(ctx context.Context, db *sql.DB, project database.ProjectID) (*Store, error) {
	if err := Migrate(ctx, db); err != nil {
		return nil, err
	}
	return NewStore(db, project), nil
}

// Migrate creates the session tables of db, which must have been opened by
// [database.Open], or brings them up to date with [Migrations], so that
// they can be read before a store is opened, as WorktreeOf reads them.
func Migrate(ctx context.Context, db *sql.DB) error {
	if err := database.Migrate(ctx, db, Name, Migrations); err != nil {
		return fmt.Errorf("session: %w", err)
	}
	return nil
}

// WorktreeOf returns the worktree of the project that the stored session id
// belongs to, the folder its tools ran in, and reports whether db holds the
// session. The session tables must exist, as Migrate creates them.
func WorktreeOf(ctx context.Context, db *sql.DB, id message.SessionID) (string, bool, error) {
	var worktree string
	err := db.QueryRowContext(ctx, `
		SELECT p.worktree FROM sessions s JOIN projects p ON p.id = s.project
		WHERE s.id = ?`, id).Scan(&worktree)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", false, nil
	case err != nil:
		return "", false, fmt.Errorf("session %s: %w", id, err)
	}
	return worktree, true, nil
}

// Session contains persisted conversation metadata. It encodes as the JSON
// of the HTTP API's session routes, which leave out its Head.
type Session struct {
	// ID identifies the session.
	ID message.SessionID `json:"id"`
	// Title is optional display text.
	Title string `json:"title,omitempty"`
	// CreatedAt is when the session was stored, with its first message.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt is the time of the latest message mutation.
	UpdatedAt time.Time `json:"updated_at"`

	// Head selects the branch the session is currently on.
	Head Head `json:"-"`
	// Worktree is the worktree of the project the session belongs to, the
	// folder its tools ran in. It is empty for a session not yet stored.
	Worktree string `json:"worktree,omitempty"`
}

// Head records where a session's visible transcript ends.
//
// Messages form a tree, so a session needs a leaf to read back from: the
// visible transcript is the path from the session's root to ID. Moving Head
// backwards hides a suffix without deleting it, and a prompt sent from a
// moved Head forks a new branch rather than overwriting the old one.
type Head struct {
	// ID is the last visible message. It is empty when the session shows no
	// messages, either because it is new or because it was moved back past
	// its first message.
	ID message.ID
	// Forward is the leaf a redo returns to. It is empty when Head sits at
	// the end of its branch and there is nothing to move forward to.
	Forward message.ID
}

// MovedBack reports whether the session is parked behind a branch it can
// return to.
func (h Head) MovedBack() bool { return h.Forward != "" }

// Position locates a message among the alternatives at a fork: the messages
// that continue the same parent.
type Position struct {
	// Index is the message's one-based place among its siblings.
	Index int `json:"index"`
	// Of is the number of siblings, and is at least two.
	Of int `json:"of"`
}
