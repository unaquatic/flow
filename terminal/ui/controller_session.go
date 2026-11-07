package ui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"log/slog"
	"slices"
	"strconv"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/product"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/widget"
)

// StatusNotice returns the events that show lines as the session info does:
// LogoNotice under the product logo.
func StatusNotice(lines []style.StyledText) []UIEvent {
	return LogoNotice(slices.Collect(LogoLines()), lines)
}

// LogoNotice returns the events that show logo on the canvas, from the
// transcript's left edge, and under it lines in a notice on the system
// surface.
func LogoNotice(logo, lines []style.StyledText) []UIEvent {
	return []UIEvent{
		EventCanvasShow{Lines: logo},
		EventToastShow{Lines: lines, System: true},
	}
}

// ShowStatus runs /status: ShowInfoUnder the product logo drawn once in each
// of the LogoColorRows of product.LogoInitialColor, to compare them.
func (s *Service) ShowStatus() []UIEvent {
	return s.ShowInfoUnder(LogoColorLines(LogoColorRows(product.LogoInitialColor)))
}

// ShowInfo reports the running version and the active session in the
// transcript, as StatusNotice shows them under the product logo, as a new
// session starts.
func (s *Service) ShowInfo() []UIEvent {
	return s.ShowInfoUnder(slices.Collect(LogoLines()))
}

// ShowInfoUnder reports the running version and the active session in the
// transcript, as LogoNotice shows them under logo: the session's ID, title,
// last update, message count, model and working directory. The title is
// omitted when empty, and the last update until the session is stored.
func (s *Service) ShowInfoUnder(logo []style.StyledText) []UIEvent {
	if s.state.Session == nil {
		return s.ShowToast("No active session")
	}
	id := s.state.Session.ID
	stored, err := s.app.Sessions().Session(s.ctx, id)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return s.ShowToast(fmt.Sprintf("Loading session failed: %v", err))
	}

	dim := style.New().WithFg(style.ColorFromInts(140, 140, 140, 255)).Func()
	var lines []style.StyledText
	add := func(label, value string) {
		lines = append(lines, style.NewStyledText(dim(fmt.Sprintf("%-10s", label)), style.Plain(value)))
	}
	add("Version", product.Name+" "+product.Version())
	add("Session", string(id))
	if stored != nil {
		if stored.Title != "" {
			add("Title", stored.Title)
		}
		add("Updated", stored.UpdatedAt.Local().Format(time.DateTime))
	}
	add("Messages", strconv.Itoa(len(s.state.Messages)))
	add("Model", s.state.Model)
	add("Directory", s.app.WorkingDir())
	return LogoNotice(logo, lines)
}

// HandleSessionList displays the stored sessions, one row each, searched
// from the input.
func (s *Service) HandleSessionList() []UIEvent {
	sessions, err := s.app.Sessions().ListSessions(s.ctx)
	if err != nil {
		return s.ShowToast(fmt.Sprintf("Loading sessions failed: %v", err))
	}
	if len(sessions) == 0 {
		return s.ShowToast("No sessions found")
	}

	s.state.SessionChoices = sessions
	s.state.SessionMenuVisible = true
	return s.FilterSessions("")
}

// FilterSessions lists the sessions whose row matches query, best first as
// [RankByName] ranks them.
func (s *Service) FilterSessions(query string) []UIEvent {
	if !s.state.SessionMenuVisible {
		return nil
	}
	name := func(item widget.SelectItem) string { return item.Name }
	menu := EventSessionMenuShow{Items: RankByName(query, s.SessionRows(), name), Query: query}
	if len(menu.Items) == 0 {
		menu.Text = NoMatchesText
	}
	return []UIEvent{menu}
}

// CurrentHint follows the active session's row in the session list.
const CurrentHint = "[current]"

// SessionRows returns a row for each listed session, marking the active
// session's row with CurrentHint after its name, which the search leaves
// out.
func (s *Service) SessionRows() []widget.SelectItem {
	items := make([]widget.SelectItem, 0, len(s.state.SessionChoices))
	for _, sess := range s.state.SessionChoices {
		item := widget.SelectItem{Name: SessionRow(sess), Value: sess.ID}
		if s.state.Session != nil && sess.ID == s.state.Session.ID {
			item.Hint = CurrentHint
		}
		items = append(items, item)
	}
	return items
}

// SessionRow returns the text of sess's row in the session list: its ID, and
// when it was last updated, in local time. Session IDs are UUIDs of one
// width, so the times line up in a column.
func SessionRow(sess session.Session) string {
	return string(sess.ID) + "  " + sess.UpdatedAt.Local().Format(time.DateTime)
}

// HideSessionMenu dismisses the session list.
func (s *Service) HideSessionMenu() []UIEvent {
	s.state.SessionMenuVisible = false
	s.state.SessionChoices = nil
	return []UIEvent{EventSessionMenuHide{}}
}

// PreviewSession loads a session's active branch for preview without changing
// the active session.
func (s *Service) PreviewSession(sessionID message.SessionID) []UIEvent {
	msgs, err := s.app.Sessions().Branch(s.ctx, sessionID)
	if err != nil {
		slog.Warn("session preview", "session", sessionID, "err", err)
		return nil
	}
	var chatMsgs []ChatMessage
	for _, m := range msgs {
		// Preserve logged tool states in the preview.
		chatMsgs = append(chatMsgs, ChatMessage{
			ID:        m.ID,
			Role:      m.Role,
			Parts:     m.Parts,
			Model:     m.Model,
			Usage:     m.Usage(),
			CreatedAt: m.CreatedAt,
		})
	}
	AssignParents(chatMsgs)
	AssignPrev(chatMsgs)
	AssignWorked(chatMsgs)
	s.AssignWindows(chatMsgs)
	return []UIEvent{EventSessionPreview{Messages: chatMsgs}}
}

// SessionPrompts loads the prompts on a session's active branch for the
// session list's inline preview, without changing the active session. A
// session that fails to load previews no prompts.
func (s *Service) SessionPrompts(sessionID message.SessionID) []UIEvent {
	msgs, err := s.app.Sessions().Branch(s.ctx, sessionID)
	if err != nil {
		slog.Warn("session prompts", "session", sessionID, "err", err)
		return []UIEvent{EventSessionPrompts{}}
	}
	return []UIEvent{EventSessionPrompts{Prompts: slices.Collect(Prompts(msgs))}}
}

// Prompts yields the text of each prompt in msgs on one line, as a session
// tree row shows it, skipping prompts that hold no text.
func Prompts(msgs []message.Content) iter.Seq[string] {
	return func(yield func(string) bool) {
		for _, m := range msgs {
			if m.Role != message.RoleUser {
				continue
			}
			if text := NewTreeEntry(m).Text; text != "" && !yield(text) {
				return
			}
		}
	}
}

// HandleSessionSelect stops the active run, activates a session, loads its
// persisted messages, and takes the model variant they last ran. Prompts
// queued in the session left are dropped.
func (s *Service) HandleSessionSelect(value any) []UIEvent {
	id, ok := value.(message.SessionID)
	if !ok {
		return nil
	}
	if err := s.OpenSession(id); err != nil {
		return s.ShowToast(fmt.Sprintf("Loading session failed: %v", err))
	}
	return slices.Concat(
		[]UIEvent{EventSessionLoad{Messages: s.state.Messages}},
		s.RestoreSessionModel(),
		s.UpdateUsage(),
		s.HideSessionMenu(),
		s.DropQueue(),
		s.UpdateTitle(),
		s.SetProgress(s.state.Running),
		s.SettleIndicator(),
	)
}

// SettleIndicator hides the working indicator when no run of the active
// session is in progress, as after a switch away from a session whose run
// was stopping: the end of that run is not applied, since it belongs to the
// session left.
func (s *Service) SettleIndicator() []UIEvent {
	if !s.state.Working || s.state.Running {
		return nil
	}
	return s.HideIndicator()
}

// OpenSession loads the persisted messages of session id into the state,
// stops the active run, and makes id the active session. It returns no UI
// events, and leaves the state unchanged when the messages fail to load.
func (s *Service) OpenSession(id message.SessionID) error {
	// Load the persisted branch to retain model, provider, and usage metadata.
	msgs, err := s.app.Sessions().Branch(s.ctx, id)
	if err != nil {
		return err
	}
	s.Stop()
	s.SetSession(&session.Session{ID: id})
	// A run of this session started before it was left may still be ending;
	// its EventAgentEnded clears the mark.
	s.state.Running = s.app.Session(id).Busy()
	s.state.Messages = nil
	for _, m := range msgs {
		s.state.Messages = append(s.state.Messages, ChatMessage{
			ID:        m.ID,
			Role:      m.Role,
			Parts:     m.Parts,
			Model:     m.Model,
			Usage:     m.Usage(),
			CreatedAt: m.CreatedAt,
		})
	}
	AssignParents(s.state.Messages)
	AssignPrev(s.state.Messages)
	AssignWorked(s.state.Messages)
	s.AssignWindows(s.state.Messages)
	return nil
}

// LoadMessages replaces the visible chat after a history move. No turn is in
// progress then, since a move waits for the session to be free.
func (s *Service) LoadMessages(loaded []app.Message) []UIEvent {
	s.state.Messages = s.state.Messages[:0]
	for _, m := range loaded {
		s.state.Messages = append(s.state.Messages, ChatMessageOf(m.Content))
	}
	AssignParents(s.state.Messages)
	AssignPrev(s.state.Messages)
	AssignWorked(s.state.Messages)
	s.AssignWindows(s.state.Messages)
	s.state.TurnStart = time.Time{}
	return []UIEvent{EventSessionLoad{Messages: s.state.Messages}}
}

// ChatMessageOf returns m as the transcript shows it.
func ChatMessageOf(m message.Content) ChatMessage {
	return ChatMessage{
		ID:        m.ID,
		Role:      m.Role,
		Parts:     m.Parts,
		Model:     m.Model,
		Usage:     m.Usage(),
		CreatedAt: m.CreatedAt,
	}
}

// InitSession opens the initial session, the stored session id or a new one
// when id is empty, and returns its /status notice followed by its messages,
// and its terminal title. A stored session takes the model variant its
// messages last ran, unless state.KeepModel keeps the terminal's. A stored
// session that fails to load leaves the new session active.
func (s *Service) InitSession(_ context.Context, id message.SessionID) []UIEvent {
	s.NewSession()
	var failed, restored []UIEvent
	if id != "" {
		if err := s.OpenSession(id); err != nil {
			failed = s.ShowToast(fmt.Sprintf("Loading session failed: %v", err))
		} else if !s.state.KeepModel {
			restored = s.RestoreSessionModel()
		}
	}
	// The transcript starts empty, so the messages are appended below the
	// notice; loading them would replace it.
	out := slices.Concat(restored, s.ShowInfo(), failed)
	for _, m := range s.state.Messages {
		out = append(out, EventMessageAdded{Msg: m})
	}
	return slices.Concat(out, s.UpdateUsage(), s.UpdateTitle())
}

// ClearMessages stops the active run, empties the chat, and starts a fresh
// session, whose /status notice then heads the transcript as at startup.
// Prompts queued in the session left are dropped.
func (s *Service) ClearMessages() []UIEvent {
	s.Stop()
	s.state.Messages = nil
	s.NewSession()
	return slices.Concat(
		[]UIEvent{EventClearMessages{}},
		s.ShowInfo(),
		s.UpdateUsage(),
		s.ResetError(),
		s.ShowToast("Chat cleared"),
		s.DropQueue(),
		s.UpdateTitle(),
		s.SetProgress(false),
	)
}

// Resumable returns the active session when it is stored, so that a later
// run can resume it, and "" otherwise.
func (s *Service) Resumable() message.SessionID {
	id := s.SessionID()
	if id == "" {
		return ""
	}
	ok, err := s.app.Sessions().Stored(s.ctx, id)
	if err != nil {
		slog.Warn("resumable session", "session", id, "err", err)
	}
	if !ok {
		return ""
	}
	return id
}

// NewSession creates a fresh session and sets it as active.
func (s *Service) NewSession() {
	s.SetSession(s.app.Sessions().CreateSession(s.ctx))
	s.state.Running = false
}
