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
	"strings"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/product"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/textbuf"
	"github.com/unaquatic/fluid/widget"
)

// StatusNotice returns the notices that show lines as the session info does:
// LogoNotice under the product logo.
func StatusNotice(lines []style.StyledText) []Notice {
	return LogoNotice(slices.Collect(LogoLines()), lines)
}

// StatusTitle heads the session info while prompts show in PromptBar.
const StatusTitle = "Status"

// LogoNotice returns the notices that show logo on the canvas, from the
// transcript's left edge, and under it lines on the system surface, titled
// StatusTitle. An empty logo leaves the canvas notice out.
func LogoNotice(logo, lines []style.StyledText) []Notice {
	info := Notice{Lines: lines, Surface: NoticeSystem, Title: StatusTitle}
	if len(logo) == 0 {
		return []Notice{info}
	}
	return []Notice{{Lines: logo, Surface: NoticeCanvas}, info}
}

// ShowStatus runs /status, showing the session info as ShowInfoUnder does,
// without the product logo, which only a new session opens under.
func (s *Service) ShowStatus() []UIEvent {
	return s.ShowInfoUnder(nil)
}

// StatusInfo holds the facts the session info shows, as StatusLines lays
// them out.
type StatusInfo struct {
	// Version names the running product and its version.
	Version string
	// Session identifies the active session.
	Session string
	// Title is the session's title, or empty when it has none.
	Title string
	// Updated is when the session was last stored, or zero before it is.
	Updated time.Time
	// Messages counts the transcript's messages.
	Messages int
	// Model is the terminal's model variant.
	Model string
	// Usage is the session's token use, whose context and window the
	// Context row shows.
	Usage SessionUsage
	// Directory is the working directory.
	Directory string
}

// statusLabelWidth is the width of the session info's label column.
const statusLabelWidth = 10

// StatusLines returns the rows of the session info, each a dim label and its
// value: the version, the session's ID, its title when it has one, its last
// update once it is stored, its message count, the model, the context, and
// the working directory. The context reads as the tokens of the latest
// request, the model's window and the share of it they fill, such as
// "18K / 400K 5%", or the tokens alone when the window is not known, in th's
// warning or danger color once they fill most of the window, as the slash
// menu's status row colors them.
func StatusLines(info StatusInfo, th *style.Theme) []style.StyledText {
	dim := style.New().WithFg(style.ColorFromInts(140, 140, 140, 255)).Func()
	var lines []style.StyledText
	add := func(label string, value style.TextChunk) {
		lines = append(lines, style.NewStyledText(dim(fmt.Sprintf("%-*s", statusLabelWidth, label)), value))
	}
	add("Version", style.Plain(info.Version))
	add("Session", style.Plain(info.Session))
	if info.Title != "" {
		add("Title", style.Plain(info.Title))
	}
	if !info.Updated.IsZero() {
		add("Updated", style.Plain(info.Updated.Local().Format(time.DateTime)))
	}
	add("Messages", style.Plain(strconv.Itoa(info.Messages)))
	add("Model", style.Plain(info.Model))
	context := FormatTokens(info.Usage.Context)
	if info.Usage.Window > 0 {
		context += " / " + FormatTokens(info.Usage.Window) + " " + ContextPercent(info.Usage)
	}
	chunk := style.Plain(context)
	if fg, ok := ContextWarnFg(info.Usage, th); ok {
		chunk = style.New().WithFg(fg).Func()(context)
	}
	add("Context", chunk)
	add("Directory", style.Plain(info.Directory))
	return lines
}

// ShowInfo reports the running version and the active session in the
// transcript, as StatusNotice shows them under the product logo, as a new
// session starts.
func (s *Service) ShowInfo() []UIEvent {
	return s.ShowInfoUnder(slices.Collect(LogoLines()))
}

// ShowLogo shows the product logo on the canvas, from the transcript's left
// edge, as a new session opens under it.
func (s *Service) ShowLogo() []UIEvent {
	return s.AddNotice(Notice{Lines: slices.Collect(LogoLines()), Surface: NoticeCanvas})
}

// ShowInfoUnder reports the running version and the active session in the
// transcript, as StatusLines lays them out, under logo as LogoNotice shows
// it, or alone when logo is empty.
func (s *Service) ShowInfoUnder(logo []style.StyledText) []UIEvent {
	if s.state.Session == nil {
		return s.ShowToast("No active session")
	}
	id := s.state.Session.ID
	stored, err := s.app.Sessions().Session(s.ctx, id)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return s.ShowToast(fmt.Sprintf("Loading session failed: %v", err))
	}

	info := StatusInfo{
		Version:   product.Name + " " + product.Version(),
		Session:   string(id),
		Messages:  s.MessageCount(),
		Model:     s.state.Model,
		Usage:     s.SessionUsage(),
		Directory: s.app.WorkingDir(),
	}
	if stored != nil {
		info.Title = stored.Title
		info.Updated = stored.UpdatedAt
	}
	return s.AddNotice(LogoNotice(logo, StatusLines(info, nil))...)
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

// SessionRows returns a row for each listed session, as SessionRowNames
// names them, marking the active session's row with CurrentHint after its
// name, which the search leaves out.
func (s *Service) SessionRows() []widget.SelectItem {
	names := SessionRowNames(s.state.SessionChoices)
	items := make([]widget.SelectItem, 0, len(s.state.SessionChoices))
	for i, sess := range s.state.SessionChoices {
		item := widget.SelectItem{Name: names[i], Value: sess.ID}
		if s.state.Session != nil && sess.ID == s.state.Session.ID {
			item.Hint = CurrentHint
		}
		items = append(items, item)
	}
	return items
}

// UntitledName names a session that has no title, in the session list and
// the terminal title.
const UntitledName = "Untitled"

// sessionTitleMax is the most cells a title takes in the session list,
// which cuts a longer one, so that the IDs stay in view.
const sessionTitleMax = 32

// sessionColumnGap is the space between the session list's columns.
const sessionColumnGap = "  "

// SessionRowNames returns the text of each session's row in the session
// list: its title, or UntitledName, in a column as wide as the widest of
// them, cut to sessionTitleMax cells, then its ID, and when it was last
// updated, in local time. Session IDs are UUIDs of one width, so the times
// line up in a column too.
func SessionRowNames(sessions []session.Session) []string {
	titles := make([]string, len(sessions))
	width := 0
	for i, sess := range sessions {
		titles[i] = TruncateWidth(SessionTitle(sess.Title), sessionTitleMax)
		width = max(width, textbuf.StringWidth(titles[i]))
	}
	names := make([]string, len(sessions))
	for i, sess := range sessions {
		pad := strings.Repeat(" ", width-textbuf.StringWidth(titles[i]))
		names[i] = titles[i] + pad + sessionColumnGap + string(sess.ID) + sessionColumnGap + sess.UpdatedAt.Local().Format(time.DateTime)
	}
	return names
}

// SessionTitle returns title, a session's title on one line, or
// UntitledName when it has none.
func SessionTitle(title string) string {
	if title = OneLine(title); title == "" {
		return UntitledName
	}
	return title
}

// TruncateWidth returns s cut to at most n cells, as the terminal draws
// them, with an ellipsis in the last cell when it is cut.
func TruncateWidth(s string, n int) string {
	if textbuf.StringWidth(s) <= n {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		w := textbuf.RuneWidth(r)
		if used+w > n-1 {
			break
		}
		b.WriteRune(r)
		used += w
	}
	return b.String() + "…"
}

// HideSessionMenu dismisses the session list.
func (s *Service) HideSessionMenu() []UIEvent {
	s.state.SessionMenuVisible = false
	s.state.SessionChoices = nil
	return []UIEvent{EventSessionMenuHide{}}
}

// SessionPrompts loads the latest limit prompts on a session's active branch
// for the session list's preview, without changing the active session or
// reading its state, so that it can run off the event loop, until ctx is
// done, and counts the earlier ones it leaves unread. It reads none of the session's
// replies, so that moving through the list of long sessions stays quick. A
// session that fails to load previews no prompts.
func (s *Service) SessionPrompts(ctx context.Context, sessionID message.SessionID, limit int) []UIEvent {
	msgs, total, err := s.app.Sessions().Prompts(ctx, sessionID, limit)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("session prompts", "session", sessionID, "err", err)
		}
		return []UIEvent{EventSessionPrompts{}}
	}
	return []UIEvent{EventSessionPrompts{Prompts: slices.Collect(Prompts(msgs)), Earlier: total - len(msgs)}}
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
// when id is empty, and returns the load of its transcript, the product logo
// followed by its messages, without the session info /status shows, and its
// terminal title. A stored session
// takes the model variant its messages last ran, unless state.KeepModel keeps
// the terminal's. A stored session that fails to load leaves the new session
// active, and says so under the logo.
//
// The transcript is loaded whole, rather than a message at a time, so that
// the UI builds the rows of the last messages alone, as BuildTranscript
// builds them.
func (s *Service) InitSession(_ context.Context, id message.SessionID) []UIEvent {
	s.NewSession()
	var restored []UIEvent
	if id != "" {
		if err := s.OpenSession(id); err != nil {
			// The service records each notice among the messages, which
			// the load shows, so their events are not needed.
			s.ShowToast(fmt.Sprintf("Loading session failed: %v", err))
		} else if !s.state.KeepModel {
			restored = s.RestoreSessionModel()
		}
	}
	// The logo heads the transcript, above the messages it follows in.
	loaded := len(s.state.Messages)
	s.ShowLogo()
	s.state.Messages = slices.Concat(s.state.Messages[loaded:], s.state.Messages[:loaded])
	return slices.Concat(restored, []UIEvent{EventSessionLoad{Messages: s.state.Messages}}, s.UpdateUsage(), s.UpdateTitle())
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
