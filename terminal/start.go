package terminal

import (
	"context"
	"fmt"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/fluid/style"
)

// StartMode is how the terminal UI picks its first session.
type StartMode int

const (
	// StartNew opens a new session.
	StartNew StartMode = iota
	// StartPick opens a new session with the session list open over it.
	StartPick
	// StartLatest opens the project's most recently updated session, or a
	// new session when the project has none.
	StartLatest
	// StartID opens the stored session Start.ID.
	StartID
)

// Start selects the first session of the terminal UI. The zero Start opens a
// new session.
type Start struct {
	// Mode is how the first session is picked.
	Mode StartMode
	// ID is the session StartID opens.
	ID message.SessionID
	// Model is the model variant the terminal starts with, in
	// "provider/model:option:effort" format, which a stored first session
	// keeps. Empty takes the app's default model, or the model the first
	// session last ran.
	Model string
	// ColorMode is the color mode the terminal UI draws in. Nil detects it
	// from the terminal.
	ColorMode *style.ColorMode
	// NoColor prints the resume notice that Run leaves after fullscreen
	// mode without colors, as where standard output shows none. The UI
	// draws in colors all the same.
	NoColor bool
}

// FirstSession returns the stored session that s opens first, or "" for a
// new session. It fails when StartID names no stored session of the project,
// which the app works in the folder of once it was opened on that session.
func (s Start) FirstSession(ctx context.Context, sessions *session.Store) (message.SessionID, error) {
	switch s.Mode {
	case StartLatest:
		list, err := sessions.ListSessions(ctx)
		if err != nil || len(list) == 0 {
			return "", err
		}
		// Sessions are listed newest first.
		return list[0].ID, nil
	case StartID:
		ok, err := sessions.Stored(ctx, s.ID)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", fmt.Errorf("no stored session matches %q", s.ID)
		}
		return s.ID, nil
	}
	return "", nil
}
