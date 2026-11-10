package ui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/fluid/style"
)

// Service updates terminal state and returns events for the UI to apply. It
// does not access the widget tree directly.
type Service struct {
	app   *app.App
	ctx   context.Context
	state *UIState
	// active is the ID of state.Session as SetSession last set it, for
	// readers off the event loop.
	active atomic.Pointer[message.SessionID]
	// relaunch is the session of another folder that RelaunchIn recorded,
	// for readers off the event loop, as RelaunchID reads it.
	relaunch atomic.Pointer[message.SessionID]
	// Settings saves the settings /config sets, each time one changes, or
	// is nil to save none.
	Settings SettingsStore
	// Commands are the slash commands, in the order the menu offers them,
	// as CommandsWith gives them for the UI's extensions, or nil for
	// SlashCommands alone.
	Commands []SlashCommand
	// Updated is the release of flow that an update installed, when this
	// is its first start, which InitSession tells of under the session
	// info, as ShowUpdated words it, and then clears. Empty tells of none.
	Updated string
}

// CommandList returns Commands, or SlashCommands when Commands is nil.
func (s *Service) CommandList() []SlashCommand {
	if s.Commands == nil {
		return SlashCommands
	}
	return s.Commands
}

// NewService creates a Service and initializes its status and model labels.
// The terminal sends its prompts to state.Model, at the reasoning effort it
// names, or the default level when it names none, fitted to the model as
// Models.FitEffort fits it, which is also the default model of its new
// sessions. Operations started by the service use ctx.
func NewService(ctx context.Context, a *app.App, state *UIState) *Service {
	s := &Service{app: a, ctx: ctx, state: state, Settings: a}
	state.StatusContent = StatusHintStyled()
	v := a.Models().ParseVariant(state.Model)
	state.ReasoningEffort = v.Effort
	state.Model = a.Models().FitEffort(v).String()
	state.DefaultModel = state.Model
	state.ModelLabel = state.Model
	return s
}

// SessionID returns the ID of the active session, or "" before session setup.
func (s *Service) SessionID() message.SessionID {
	if s.state.Session == nil {
		return ""
	}
	return s.state.Session.ID
}

// SetSession makes sess the active session, with no turn in progress. The
// caller must call it from the event-loop goroutine.
func (s *Service) SetSession(sess *session.Session) {
	s.state.Session = sess
	s.state.TurnStart = time.Time{}
	id := sess.ID
	s.active.Store(&id)
}

// ActiveSessionID returns the ID of the session that SetSession last made
// active, or "" before session setup. Unlike SessionID, it may be called from
// any goroutine, such as once the UI has closed while the event loop may
// still be handling a key.
func (s *Service) ActiveSessionID() message.SessionID {
	if id := s.active.Load(); id != nil {
		return *id
	}
	return ""
}

// RelaunchID returns the session of another folder that RelaunchIn recorded,
// for the UI to close and have reopened in its folder, or "". Like
// ActiveSessionID, it may be called from any goroutine.
func (s *Service) RelaunchID() message.SessionID {
	if id := s.relaunch.Load(); id != nil {
		return *id
	}
	return ""
}

// ActiveSession returns a handle on the active session, and false before
// session setup.
func (s *Service) ActiveSession() (app.Session, bool) {
	id := s.SessionID()
	if id == "" {
		return app.Session{}, false
	}
	return s.app.Session(id), true
}

// Streaming reports whether a run the terminal started on the active session
// has not ended yet, as UIState.Running records it.
func (s *Service) Streaming() bool {
	return s.state.Running
}

// Stop cancels the run in progress on the active session, if any.
func (s *Service) Stop() {
	if sess, ok := s.ActiveSession(); ok {
		sess.Stop()
	}
}

// SendPrompt starts a run that answers text with the terminal's model in the
// active session, as StartRun does.
func (s *Service) SendPrompt(text string) error {
	input := app.SendInput{Text: text, Model: s.state.Model}
	return s.StartRun(func(sess app.Session) error { return sess.Send(input) })
}

// ContinueRun starts a run that picks up from where the active session's
// transcript ends, with the terminal's model, as StartRun does.
func (s *Service) ContinueRun() error {
	model := s.state.Model
	return s.StartRun(func(sess app.Session) error { return sess.Continue(model) })
}

// HandleContinue picks the work up from where the transcript ends, whatever
// it ends at, without a new prompt. It reports in a toast when a run is
// already in progress or the transcript is empty.
func (s *Service) HandleContinue() []UIEvent {
	if s.Streaming() {
		return s.ShowToast("A run is already in progress")
	}
	switch err := s.ContinueRun(); {
	case errors.Is(err, app.ErrNothingToContinue):
		return s.ShowToast("Nothing to continue")
	case err != nil:
		return s.ShowToast(fmt.Sprintf("Continue failed: %v", err))
	}
	return slices.Concat(s.ResetError(), s.ShowIndicator(""))
}

// StartRun starts a run in the active session with start, such as
// app.Session.Demo, and marks it running until its EventAgentEnded. It
// reports the error start returns, such as app.ErrBusy when the session has
// an operation in progress, and does nothing before session setup.
func (s *Service) StartRun(start func(app.Session) error) error {
	sess, ok := s.ActiveSession()
	if !ok {
		return nil
	}
	if err := start(sess); err != nil {
		return err
	}
	s.state.Running = true
	return nil
}

// SetStatus replaces the hint shown in the status line.
func (s *Service) SetStatus(st style.StyledText) []UIEvent {
	s.state.StatusContent = st
	return []UIEvent{EventStatusChanged{Content: st}}
}

// ClearInput empties the input box, and updates what follows the input's
// text as HandleInputChange does when the user empties it, since emptying
// the input reports no change itself: a menu that the input searches shows
// all its rows again, and the slash menu, which the input's leading slash
// opened, closes.
func (s *Service) ClearInput() []UIEvent {
	events := append([]UIEvent{EventInputClear{}}, s.FilterMenu("")...)
	if s.state.SlashMenuVisible {
		events = append(events, s.HideSlashMenu()...)
	}
	return events
}

// SetInput replaces the input box's text with text, as a change that the
// input reports, and parks the cursor at its end.
func (s *Service) SetInput(text string) []UIEvent {
	return []UIEvent{EventInputSet{Text: text}}
}
