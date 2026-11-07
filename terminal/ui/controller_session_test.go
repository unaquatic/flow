package ui

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/product"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/widget"
)

// openSessionMenu opens the session list of s on a fixed newer and older
// session, the newer one active, and returns them.
func openSessionMenu(s *Service) (newer, older session.Session) {
	newer = session.Session{ID: "0199bbbb-2222", Title: "Fix the login", UpdatedAt: time.Date(2026, 9, 27, 14, 3, 11, 0, time.Local)}
	older = session.Session{ID: "0199aaaa-1111", UpdatedAt: time.Date(2026, 9, 26, 9, 30, 0, 0, time.Local)}
	s.state.Session = &session.Session{ID: newer.ID}
	s.state.SessionMenuVisible = true
	s.state.SessionChoices = []session.Session{newer, older}
	return newer, older
}

// sessionMenuShown returns the session list that out shows.
func sessionMenuShown(t *testing.T, out []UIEvent) EventSessionMenuShow {
	t.Helper()
	for _, ev := range out {
		if show, ok := ev.(EventSessionMenuShow); ok {
			return show
		}
	}
	t.Fatalf("events %v show no session list", out)
	return EventSessionMenuShow{}
}

// TestSessionMenuRows checks that each session's row reads its title, or
// Untitled, in a column as wide as the widest, then its ID and last update.
func TestSessionMenuRows(t *testing.T) {
	s, _ := newTestService(t)
	newer, older := openSessionMenu(s)

	show := sessionMenuShown(t, s.FilterSessions(""))
	want := []widget.SelectItem{
		{Name: "Fix the login  0199bbbb-2222  2026-09-27 14:03:11", Hint: CurrentHint, Value: newer.ID},
		{Name: "Untitled       0199aaaa-1111  2026-09-26 09:30:00", Value: older.ID},
	}
	if !slices.Equal(show.Items, want) {
		t.Errorf("rows = %+v, want %+v", show.Items, want)
	}
}

func TestSessionMenuSearch(t *testing.T) {
	tests := []struct {
		name, query string
		want        func(newer, older session.Session) message.SessionID
	}{
		{name: "date", query: "09-26", want: func(_, older session.Session) message.SessionID { return older.ID }},
		{name: "ID", query: "0199B", want: func(newer, _ session.Session) message.SessionID { return newer.ID }},
		{name: "time", query: "09:30", want: func(_, older session.Session) message.SessionID { return older.ID }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newTestService(t)
			newer, older := openSessionMenu(s)

			show := sessionMenuShown(t, s.FilterSessions(tt.query))
			if want := tt.want(newer, older); len(show.Items) == 0 || show.Items[0].Value != want {
				t.Errorf("search %q lists %+v, want %s first", tt.query, show.Items, want)
			}
		})
	}
}

// storePrompts stores a new session of a in which each of prompts is
// answered, and returns its ID.
func storePrompts(t *testing.T, a *app.App, prompts ...string) message.SessionID {
	t.Helper()
	id := message.NewSessionID()
	for _, prompt := range prompts {
		for _, msg := range []message.Content{
			{ID: message.NewID(), Role: message.RoleUser, Parts: []message.ContentPart{message.TextPart{Text: prompt}}},
			{ID: message.NewID(), Role: message.RoleAgent, Parts: []message.ContentPart{message.TextPart{Text: "Done."}}},
		} {
			if err := a.Sessions().AppendMessage(t.Context(), id, &msg); err != nil {
				t.Fatal(err)
			}
		}
	}
	return id
}

// sessionPrompts returns the prompts that out previews, and the count of the
// earlier ones it leaves unloaded.
func sessionPrompts(t *testing.T, out []UIEvent) ([]string, int) {
	t.Helper()
	if len(out) != 1 {
		t.Fatalf("events %v, want one", out)
	}
	e, ok := out[0].(EventSessionPrompts)
	if !ok {
		t.Fatalf("event %T, want EventSessionPrompts", out[0])
	}
	return e.Prompts, e.Earlier
}

// TestSessionPrompts checks that a session's prompts are previewed oldest
// first, one line each, without the replies, that only the latest up to the
// limit are loaded, the earlier ones counted, and that a session that is not
// stored previews none.
func TestSessionPrompts(t *testing.T) {
	s, a := newTestService(t)
	id := storePrompts(t, a, "add a Divide method\nto the calculator", "also add tests", "commit it")

	want := []string{"add a Divide method to the calculator", "also add tests", "commit it"}
	if got, earlier := sessionPrompts(t, s.SessionPrompts(t.Context(), id, 10)); !slices.Equal(got, want) || earlier != 0 {
		t.Errorf("prompts = %q after %d earlier, want %q after none", got, earlier, want)
	}
	if got, earlier := sessionPrompts(t, s.SessionPrompts(t.Context(), id, 2)); !slices.Equal(got, want[1:]) || earlier != 1 {
		t.Errorf("limit 2: prompts = %q after %d earlier, want %q after 1", got, earlier, want[1:])
	}
	if got, earlier := sessionPrompts(t, s.SessionPrompts(t.Context(), message.NewSessionID(), 10)); len(got) != 0 || earlier != 0 {
		t.Errorf("prompts of a session not stored = %q after %d earlier, want none", got, earlier)
	}
}

func TestPrompts(t *testing.T) {
	text := func(role message.Role, s string) message.Content {
		return message.Content{Role: role, Parts: []message.ContentPart{message.TextPart{Text: s}}}
	}
	msgs := []message.Content{
		text(message.RoleUser, "  Run   the\ttests\n"),
		text(message.RoleAgent, "All tests pass."),
		// A prompt without text has nothing to preview.
		text(message.RoleUser, " \n "),
		text(message.RoleUser, "commit it"),
	}
	want := []string{"Run the tests", "commit it"}
	if got := slices.Collect(Prompts(msgs)); !slices.Equal(got, want) {
		t.Errorf("prompts = %q, want %q", got, want)
	}
}

func TestSessionMenuNoMatch(t *testing.T) {
	s, _ := newTestService(t)
	openSessionMenu(s)

	show := sessionMenuShown(t, s.FilterSessions("zzz"))
	if len(show.Items) != 0 || show.Text != NoMatchesText {
		t.Errorf("search lists %+v with text %q, want none with a notice", show.Items, show.Text)
	}

	s.HideSessionMenu()
	if out := s.FilterSessions(""); out != nil {
		t.Errorf("search after close = %v, want nothing", out)
	}
}

// TestActiveSessionIDOffLoop checks that the active session can be read off
// the event loop while the loop switches sessions, as Run reads it once the
// UI closes. The race detector reports a read that is not.
func TestActiveSessionIDOffLoop(t *testing.T) {
	s, _ := newTestService(t)
	s.NewSession()
	synctest.Test(t, func(t *testing.T) {
		var wg sync.WaitGroup
		wg.Go(func() {
			for range 100 {
				s.NewSession()
			}
		})
		for range 100 {
			if s.ActiveSessionID() == "" {
				t.Fatal("no active session")
			}
		}
		wg.Wait()
	})
	if got, want := s.ActiveSessionID(), s.SessionID(); got != want {
		t.Errorf("active session = %q, want the last one set, %q", got, want)
	}
}

// TestInitSessionNoticeFirst checks that a session opened at startup loads its
// transcript in one event, which the UI builds the last rows of, with the
// logo above its messages and no /status notice, and adds nothing to it
// after.
func TestInitSessionNoticeFirst(t *testing.T) {
	tests := []struct {
		name     string
		messages int
		want     []string
	}{
		{name: "new session", want: []string{"logo"}},
		{name: "stored session", messages: 2, want: []string{"logo", "message", "message"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, a := newTestService(t)
			var id message.SessionID
			if tt.messages > 0 {
				id = message.NewSessionID()
			}
			for range tt.messages {
				storeMessage(t, a, id)
			}

			var loads []EventSessionLoad
			for _, ev := range s.InitSession(t.Context(), id) {
				switch ev := ev.(type) {
				case EventSessionLoad:
					loads = append(loads, ev)
				case EventMessageAdded:
					t.Errorf("startup added %+v to the transcript, want it in the load", ev.Msg)
				}
			}
			if len(loads) != 1 {
				t.Fatalf("startup loaded the transcript %d times, want once", len(loads))
			}
			var got []string
			for _, m := range loads[0].Messages {
				switch {
				case m.Notice == nil:
					got = append(got, "message")
				case m.Notice.Surface == NoticeCanvas:
					got = append(got, "logo")
				default:
					got = append(got, "notice")
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("loaded transcript = %q, want %q", got, tt.want)
			}
			// A transcript built again from the state shows them in order too.
			messages := false
			for _, m := range s.state.Messages {
				if m.Notice != nil && messages {
					t.Errorf("notice %+v follows a message, want it above them all", *m.Notice)
				}
				messages = messages || m.Notice == nil
			}
			if id != "" && s.SessionID() != id {
				t.Errorf("active session = %q, want %q", s.SessionID(), id)
			}
		})
	}
}

// TestShowInfoIsSystemNotice checks that /status draws the logo on the
// canvas, and under it a system notice that starts with the version.
func TestShowInfoIsSystemNotice(t *testing.T) {
	s, _ := newTestService(t)
	s.InitSession(t.Context(), "")

	events := s.ShowInfo()

	if len(events) != 2 {
		t.Fatalf("ShowInfo returned %d events, want the logo and the notice", len(events))
	}
	logo, ok := canvasOf(events[0])
	if !ok {
		t.Fatalf("first event is %+v, want the logo on the canvas", events[0])
	}
	var rows []string
	for _, line := range logo.Lines {
		rows = append(rows, line.PlainText())
	}
	if want := strings.Split(product.Logo, "\n"); !slices.Equal(rows, want) {
		t.Errorf("canvas rows = %q, want the logo %q", rows, want)
	}
	toast, ok := toastOf(events[1])
	if !ok || toast.Surface != NoticeSystem {
		t.Fatalf("second event is %+v, want a system notice", events[1])
	}
	if len(toast.Lines) == 0 || !strings.HasPrefix(toast.Lines[0].PlainText(), "Version") {
		t.Errorf("notice lines = %+v, want them to start with the version", toast.Lines)
	}
}

// TestClearMessagesShowsStatusFirst checks that /new heads the emptied
// transcript with the /status notice of the new session, above the notice
// that the chat was cleared.
func TestClearMessagesShowsStatusFirst(t *testing.T) {
	s, _ := newTestService(t)
	s.InitSession(t.Context(), "")
	old := s.SessionID()

	out := s.ClearMessages()

	if s.SessionID() == old {
		t.Fatalf("session is still %s, want a new one", old)
	}
	cleared := slices.IndexFunc(out, func(e UIEvent) bool { _, ok := e.(EventClearMessages); return ok })
	if cleared < 0 {
		t.Fatalf("events = %+v, want the transcript cleared", out)
	}
	var toasts []Notice
	for i, e := range out {
		if toast, ok := toastOf(e); ok {
			if i < cleared {
				t.Errorf("notice %+v comes before the transcript is cleared", toast)
			}
			toasts = append(toasts, toast)
		}
	}
	if len(toasts) == 0 || toasts[0].Surface != NoticeSystem {
		t.Fatalf("notices = %+v, want the /status notice first", toasts)
	}
	session := fmt.Sprintf("%-10s%s", "Session", s.SessionID())
	if !slices.ContainsFunc(toasts[0].Lines, func(l style.StyledText) bool { return l.PlainText() == session }) {
		t.Errorf("first notice = %+v, want the /status of session %s", toasts[0], s.SessionID())
	}
	if !slices.ContainsFunc(toasts[1:], func(n Notice) bool { return n.Text == "Chat cleared" }) {
		t.Errorf("notices = %+v, want the chat cleared after the /status notice", toasts)
	}
}

// storeMessage appends a user message to session id of a, which stores the
// session when it is new.
func storeMessage(t *testing.T, a *app.App, id message.SessionID) {
	t.Helper()
	err := a.Sessions().AppendMessage(t.Context(), id, &message.Content{
		ID:    message.NewID(),
		Role:  message.RoleUser,
		Parts: []message.ContentPart{message.TextPart{Text: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestSessionRowNamesCutLongTitles checks that a title wider than
// sessionTitleMax cells is cut with an ellipsis, counting wide characters
// as two cells, and that a title on several lines reads on one.
func TestSessionRowNamesCutLongTitles(t *testing.T) {
	at := time.Date(2026, 9, 27, 14, 3, 11, 0, time.Local)
	names := SessionRowNames([]session.Session{
		{ID: "s1", Title: strings.Repeat("长", 20), UpdatedAt: at},
		{ID: "s2", Title: "two\nlines", UpdatedAt: at},
	})
	cut := strings.Repeat("长", 15) + "…"
	want := []string{
		cut + "  s1  2026-09-27 14:03:11",
		"two lines" + strings.Repeat(" ", 31-9) + "  s2  2026-09-27 14:03:11",
	}
	if !slices.Equal(names, want) {
		t.Errorf("rows = %q, want %q", names, want)
	}
}

// TestSessionUpdatedSetsTitle checks that a title published for the active
// session, as an extension sets it, shows in the terminal title, that one
// published for another session does not, and that none shows while
// config.EnvDisableTerminalTitle turns the terminal title off.
func TestSessionUpdatedSetsTitle(t *testing.T) {
	t.Setenv(config.EnvDisableTerminalTitle, "")
	s, _ := newTestService(t)
	s.state.Session = &session.Session{ID: "ses_active"}
	updated := func(id message.SessionID) app.EventSessionUpdated {
		return app.EventSessionUpdated{EventBase: app.EventBase{SessionID: id}, Title: "Fix the login"}
	}

	want := []UIEvent{EventTitleSet{Title: "ƒ | Fix the login"}}
	if got := s.ApplyAgentEvent(updated("ses_active")); !slices.Equal(got, want) {
		t.Errorf("events = %+v, want %+v", got, want)
	}
	if got := s.ApplyAgentEvent(updated("ses_other")); len(got) != 0 {
		t.Errorf("another session's title gave %+v, want nothing", got)
	}
	t.Setenv(config.EnvDisableTerminalTitle, "1")
	if got := s.ApplyAgentEvent(updated("ses_active")); len(got) != 0 {
		t.Errorf("a disabled terminal title gave %+v, want nothing", got)
	}
}

// TestTitle checks that the terminal title reads TitleMark and the
// session's title, or Untitled when it has none, without control
// characters.
func TestTitle(t *testing.T) {
	for title, want := range map[string]string{
		"":              "ƒ | Untitled",
		"  ":            "ƒ | Untitled",
		"Fix the login": "ƒ | Fix the login",
		"a\x1b]0;b":     "ƒ | a]0;b",
	} {
		if got := Title(title); got != want {
			t.Errorf("Title(%q) = %q, want %q", title, got, want)
		}
	}
}
