package ui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/textbuf"
	"github.com/unaquatic/fluid/widget"
)

// StatusNotice returns the notice that shows lines as the session info does:
// on the system surface, titled StatusTitle.
func StatusNotice(lines []style.StyledText) Notice {
	return Notice{Lines: lines, Surface: NoticeSystem, Title: StatusTitle}
}

// StatusTitle heads the session info while prompts show in PromptBar.
const StatusTitle = "Status"

// StatusInfo holds the facts the session info shows, as StatusLines lays
// them out.
type StatusInfo struct {
	// Session identifies the active session.
	Session string
	// Title is the session's title, or empty when it has none.
	Title string
	// Account names the provider of the terminal's model and the kind of
	// its active account, as AccountName names them, such as "Provider A
	// OAuth", or is empty when the model names no provider.
	Account string
	// Model is the terminal's model variant as DisplayName names it, such
	// as "Model A Fast High", or empty when no model is set.
	Model string
	// Usage is the session's token use, whose context and window the
	// Context row shows.
	Usage SessionUsage
	// Directory is the working directory.
	Directory string
	// Branch is the git branch checked out in Directory, as GitBranch
	// reads it, or empty when it is in no git work tree.
	Branch string
}

// ShortSessionID returns the last part of id, after its last dash, such as
// "4229c726c313" of "01a0e33d-4571-7ed4-b06b-4229c726c313", or id whole
// when it has no dash.
func ShortSessionID(id string) string {
	return id[strings.LastIndexByte(id, '-')+1:]
}

// GitBranch returns the branch checked out in the git work tree that holds
// dir, such as "main", read from its HEAD file, the first 7 characters of
// the commit when HEAD names none, or "" when dir is in no work tree.
func GitBranch(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		gitDir := filepath.Join(d, ".git")
		// A linked work tree's .git is a file that names its git directory.
		if b, err := os.ReadFile(gitDir); err == nil {
			path, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir: ")
			if !ok {
				return ""
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(d, path)
			}
			gitDir = path
		}
		if head, err := os.ReadFile(filepath.Join(gitDir, "HEAD")); err == nil {
			ref := strings.TrimSpace(string(head))
			if branch, ok := strings.CutPrefix(ref, "ref: refs/heads/"); ok {
				return branch
			}
			return ref[:min(7, len(ref))]
		}
		if filepath.Dir(d) == d {
			return ""
		}
	}
}

// statusLabelWidth is the width of the session info's label column.
const statusLabelWidth = 10

// StatusNone stands in the session info for an account or model that is
// not set.
const StatusNone = "None"

// StatusUnknown names, in the session info and the Usage box, a provider,
// lab or model the catalog does not list, before its ID in brackets, as
// Unknown writes it.
const StatusUnknown = "Unknown"

// Unknown returns id as the session info names a provider, lab or model the
// catalog does not list, such as "Unknown [provider-z]".
func Unknown(id string) string {
	return StatusUnknown + " " + Tag(id)
}

// tagPattern matches a bracketed ID, such as the "[model-x]" Unknown writes.
var tagPattern = regexp.MustCompile(`\[[^\]]*\]`)

// TagChunks returns name, as DisplayName or AccountName writes it, in text,
// and each bracketed ID in it, such as "[model-x]", in dim.
func TagChunks(name string, text, dim func(string) style.TextChunk) []style.TextChunk {
	var chunks []style.TextChunk
	last := 0
	for _, m := range tagPattern.FindAllStringIndex(name, -1) {
		if m[0] > last {
			chunks = append(chunks, text(name[last:m[0]]))
		}
		chunks = append(chunks, dim(name[m[0]:m[1]]))
		last = m[1]
	}
	if last < len(name) {
		chunks = append(chunks, text(name[last:]))
	}
	return chunks
}

// StatusLines returns the rows of the session info, each a dim label and its
// value: the session's title, as SessionTitle names it, with its ID after
// it, as ShortSessionID cuts it, dim and bracketed, such as "Fix the login
// [4229c726c313]", the context, the model, the model's provider and
// account, and the working directory, as SessionDir writes it, with its git
// branch after it, dim and bracketed, such as "~/work/project [main]". An
// account or model that is not set reads as StatusNone, dim as the labels
// are, as is the bracketed ID of one the catalog does not list, as
// TagChunks dims it. The context reads as the tokens of the latest request,
// the model's window and the share of it they fill, such as "18K / 400K
// 5%", or the tokens alone when the window is not known, in th's warning or
// danger color once they fill most of the window, as the slash menu's
// status row colors them. The labels are in th's TextMuted. A nil th uses
// the default theme.
func StatusLines(info StatusInfo, th *style.Theme) []style.StyledText {
	dim := style.New().WithFg(themeOrDefault(th).TextMuted).Func()
	orNone := func(value string) []style.TextChunk {
		if value == "" {
			return []style.TextChunk{dim(StatusNone)}
		}
		return TagChunks(value, style.Plain, dim)
	}
	directory := []style.TextChunk{style.Plain(SessionDir(info.Directory))}
	if info.Branch != "" {
		directory = append(directory, style.Plain(" "), dim(Tag(info.Branch)))
	}
	return LabeledLines([]LabeledRow{
		{"Session", []style.TextChunk{style.Plain(SessionTitle(info.Title) + " "), dim(Tag(ShortSessionID(info.Session)))}},
		{"Context", []style.TextChunk{ContextValue(info.Usage, th)}},
		{"Model", orNone(info.Model)},
		{"Account", orNone(info.Account)},
		{"Directory", directory},
	}, statusLabelWidth, th)
}

// LabeledRow is a row of a notice laid out as a column of labels and their
// values, such as the session info.
type LabeledRow struct {
	// Label names the value.
	Label string
	// Value is what the row shows after the label.
	Value []style.TextChunk
}

// LabeledLines returns rows as lines, each its label padded to width cells
// in th's TextMuted, then its value. A nil th uses the default theme.
func LabeledLines(rows []LabeledRow, width int, th *style.Theme) []style.StyledText {
	dim := style.New().WithFg(themeOrDefault(th).TextMuted).Func()
	lines := make([]style.StyledText, len(rows))
	for i, r := range rows {
		lines[i] = style.NewStyledText(append([]style.TextChunk{dim(PadWidth(r.Label, width))}, r.Value...)...)
	}
	return lines
}

// ContextValue returns the context of u as the session info shows it: the
// tokens of the latest request, the model's window and the share of it they
// fill, such as "18K / 400K 5%", or the tokens alone when the window is not
// known, in th's warning or danger color once they fill most of the window.
// A nil th uses the default theme.
func ContextValue(u SessionUsage, th *style.Theme) style.TextChunk {
	context := FormatTokens(u.Context)
	if u.Window > 0 {
		context += " / " + FormatTokens(u.Window) + " " + ContextPercent(u)
	}
	if fg, ok := ContextWarnFg(u, th); ok {
		return style.New().WithFg(fg).Func()(context)
	}
	return style.Plain(context)
}

// ShowInfo runs /status, and heads the transcript as a session starts: it
// reports the active session in the transcript, as StatusLines lays it out
// and StatusNotice shows it.
func (s *Service) ShowInfo() []UIEvent {
	if s.state.Session == nil {
		return s.ShowToast("No active session")
	}
	id := s.state.Session.ID
	stored, err := s.app.Sessions().Session(s.ctx, id)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return s.ShowToast(fmt.Sprintf("Loading session failed: %v", err))
	}

	info := StatusInfo{
		Session:   string(id),
		Model:     s.ModelDisplayName(s.state.Model),
		Usage:     s.SessionUsage(),
		Directory: s.app.WorkingDir(),
		Branch:    GitBranch(s.app.WorkingDir()),
	}
	if provider, _ := agent.ParseModel(s.state.Model); provider != "" {
		info.Account = s.AccountName(provider)
	}
	if stored != nil {
		info.Title = stored.Title
	}
	return s.AddNotice(StatusNotice(StatusLines(info, nil)))
}

// HandleSessionList displays the stored sessions of every folder, one row
// each, searched from the input.
func (s *Service) HandleSessionList() []UIEvent {
	sessions, err := s.app.Sessions().ListAllSessions(s.ctx)
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
// which cuts a longer one, so that the other columns stay in view.
const sessionTitleMax = 32

// sessionDirMax is the most cells a folder takes in the session list, which
// cuts a longer one from the left, keeping the folder's own name in view.
const sessionDirMax = 32

// sessionColumnGap is the space between the session list's columns.
const sessionColumnGap = "  "

// SessionRowNames returns the text of each session's row in the session
// list: its title, or UntitledName, cut to sessionTitleMax cells, then its
// folder, as SessionDir names it, cut to sessionDirMax cells from the left,
// each in a column as wide as the widest of them, then when it was last
// updated, in local time, and its ID. The times are of one width, so the IDs
// line up in a column too. The folder column is left out when no session
// has a folder.
func SessionRowNames(sessions []session.Session) []string {
	titles := make([]string, len(sessions))
	dirs := make([]string, len(sessions))
	titleWidth, dirWidth := 0, 0
	for i, sess := range sessions {
		titles[i] = TruncateWidth(SessionTitle(sess.Title), sessionTitleMax)
		titleWidth = max(titleWidth, textbuf.StringWidth(titles[i]))
		dirs[i] = TruncateWidthLeft(SessionDir(sess.Worktree), sessionDirMax)
		dirWidth = max(dirWidth, textbuf.StringWidth(dirs[i]))
	}
	names := make([]string, len(sessions))
	for i, sess := range sessions {
		var b strings.Builder
		b.WriteString(PadWidth(titles[i], titleWidth))
		if dirWidth > 0 {
			b.WriteString(sessionColumnGap + PadWidth(dirs[i], dirWidth))
		}
		b.WriteString(sessionColumnGap + sess.UpdatedAt.Local().Format(time.DateTime))
		b.WriteString(sessionColumnGap + string(sess.ID))
		names[i] = b.String()
	}
	return names
}

// PadWidth returns s padded with spaces to n cells, as the terminal draws
// them, or s itself when it is as wide already.
func PadWidth(s string, n int) string {
	return s + strings.Repeat(" ", max(0, n-textbuf.StringWidth(s)))
}

// SessionDir returns dir, the folder of a session, as the session list
// shows it: with the home folder written as ~, as shells write it.
func SessionDir(dir string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return dir
	}
	return TildeDir(dir, home)
}

// TildeDir returns dir with its leading home, the home folder, written as ~:
// "~" for home itself and "~/" before a folder inside it. Any other dir,
// such as one that only shares home's prefix, is returned as it is.
func TildeDir(dir, home string) string {
	if home == "" || home == string(filepath.Separator) {
		return dir
	}
	if dir == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(dir, home+string(filepath.Separator)); ok {
		return "~" + string(filepath.Separator) + rest
	}
	return dir
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

// TruncateWidthLeft returns s cut to at most n cells, as the terminal draws
// them, from the left, with an ellipsis in the first cell when it is cut, so
// that its end stays in view.
func TruncateWidthLeft(s string, n int) string {
	if textbuf.StringWidth(s) <= n {
		return s
	}
	runes := []rune(s)
	used, start := 0, len(runes)
	for start > 0 {
		w := textbuf.RuneWidth(runes[start-1])
		if used+w > n-1 {
			break
		}
		used += w
		start--
	}
	return "…" + string(runes[start:])
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
// persisted messages, and takes the model variant they last ran, as
// RestoreSessionModel does, for that session alone: the default model stays
// as it was. Prompts
// queued in the session left are dropped. A listed session of another
// folder is not opened here: its tools and files are that folder's, so the
// UI closes to reopen it there, as RelaunchIn does.
func (s *Service) HandleSessionSelect(value any) []UIEvent {
	id, ok := value.(message.SessionID)
	if !ok {
		return nil
	}
	if dir := s.ListedWorktree(id); dir != "" && dir != s.app.WorkingDir() {
		return s.RelaunchIn(id)
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

// ListedWorktree returns the folder of session id as the open session list
// holds it, or "" when the list holds no such session.
func (s *Service) ListedWorktree(id message.SessionID) string {
	i := slices.IndexFunc(s.state.SessionChoices, func(sess session.Session) bool { return sess.ID == id })
	if i < 0 {
		return ""
	}
	return s.state.SessionChoices[i].Worktree
}

// RelaunchIn records session id, of another folder, as the session to
// reopen there, as RelaunchID reports it, and closes the session list and
// the UI, so that the program can start flow again on it in its folder, as
// Result.Relaunch tells.
func (s *Service) RelaunchIn(id message.SessionID) []UIEvent {
	s.relaunch.Store(&id)
	return slices.Concat(s.HideSessionMenu(), []UIEvent{EventQuit{}})
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
			Provider:  m.Provider,
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
		Provider:  m.Provider,
		Model:     m.Model,
		Usage:     m.Usage(),
		CreatedAt: m.CreatedAt,
	}
}

// InitSession opens the initial session, the stored session id or a new one
// when id is empty, and returns the load of its transcript, the session info
// /status shows, as ShowInfo shows it, followed by its messages, and its
// terminal title. A stored session takes the model
// variant its messages last ran, unless state.KeepModel keeps the
// terminal's. A stored session that fails to load leaves the new session
// active, and says so under the session info.
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
	// The product icon and the session info under it head the transcript,
	// above the messages they follow in. The info shows once the session
	// and its model are set, so that it reads them. The update that started
	// this release shows under it.
	loaded := len(s.state.Messages)
	s.AddNotice(IconNotice())
	s.ShowInfo()
	if s.Updated != "" {
		s.ShowUpdated(s.Updated)
		s.Updated = ""
	}
	s.state.Messages = slices.Concat(s.state.Messages[loaded:], s.state.Messages[:loaded])
	return slices.Concat(restored, []UIEvent{EventSessionLoad{Messages: s.state.Messages}}, s.UpdateUsage(), s.UpdateTitle())
}

// ClearMessages stops the active run, empties the chat, and starts a fresh
// session on the terminal's DefaultModel, not the model of the session left,
// whose /status notice then heads the transcript as at startup. Prompts
// queued in the session left are dropped.
func (s *Service) ClearMessages() []UIEvent {
	s.Stop()
	s.state.Messages = nil
	s.NewSession()
	// The model must be set before ShowInfo, which reads it.
	restored := s.RestoreDefaultModel()
	return slices.Concat(
		[]UIEvent{EventClearMessages{}},
		restored,
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
