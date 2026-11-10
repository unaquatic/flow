package ui

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/extension/demo"
)

// menuCommands returns the commands the slash menu offers while the input
// holds text, with debug off, best first, or nil when the menu is hidden.
func menuCommands(t *testing.T, text string) []Command {
	t.Helper()
	return menuCommandsIn(t, &Service{state: &UIState{}, Commands: builtinCommands(t)}, text)
}

// menuCommandsIn returns the commands the slash menu of s offers while the
// input holds text, as menuCommands does.
func menuCommandsIn(t *testing.T, s *Service, text string) []Command {
	t.Helper()
	var cmds []Command
	for _, ev := range s.UpdateSlashMenu(text) {
		if show, ok := ev.(EventSlashMenuShow); ok {
			for _, item := range show.Items {
				cmds = append(cmds, item.Value.(Command))
			}
		}
	}
	return cmds
}

func TestSlashMenuOrder(t *testing.T) {
	tests := []struct {
		name, text string
		want       []Command
	}{
		// Every command, group after group, with no gap between groups,
		// and without /demo while debug is off.
		{name: "slash", text: "/", want: []Command{
			CommandResume, CommandNew, CommandModel, CommandStatus, CommandUsage,
			CommandUndo, CommandRedo, CommandContinue, CommandCheckout,
			CommandCopy, CommandExport,
			CommandConfig, CommandLogin, CommandHelp, CommandQuit,
		}},
		// Prefixes rank ahead of the subsequence in the alias "history".
		{name: "prefix before subsequence", text: "/st", want: []Command{CommandStatus, CommandConfig, CommandResume}},
		// Prefixes rank ahead of the substring in "tree".
		{name: "prefixes in menu order", text: "/re", want: []Command{CommandResume, CommandRedo, CommandCheckout}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := menuCommands(t, tt.text); !slices.Equal(got, tt.want) {
				t.Errorf("menu for %q = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

// TestDebugCommands checks that /demo, a debug command, shows in the menu
// and in /help only while debug is on, and runs when typed either way.
func TestDebugCommands(t *testing.T) {
	const demo Command = "demo"
	for _, debug := range []bool{false, true} {
		s, _ := newTestService(t)
		s.Commands = builtinCommands(t)
		s.InitSession(t.Context(), "")
		s.state.Debug = debug
		if got := slices.Contains(menuCommandsIn(t, s, "/"), demo); got != debug {
			t.Errorf("debug %v: menu offers /demo = %v", debug, got)
		}
		if got := slices.Contains(menuCommandsIn(t, s, "/dem"), demo); got != debug {
			t.Errorf("debug %v: search finds /demo = %v", debug, got)
		}
		help := JoinStyledLines(HelpLines(s.ShownCommands())).PlainText()
		if got := strings.Contains(help, demo.String()); got != debug {
			t.Errorf("debug %v: /help lists /demo = %v", debug, got)
		}
		if out := s.HandleSubmit(demo.String()); s.state.Banner == NoBanner {
			t.Errorf("debug %v: typed /demo = %#v, want the demo menu", debug, out)
		}
		s.CancelBanner()
	}
}

func TestSlashMenuBestMatch(t *testing.T) {
	tests := []struct {
		name, text string
		want       Command
	}{
		{name: "exact", text: "/status", want: CommandStatus},
		{name: "upper case", text: "/Status", want: CommandStatus},
		{name: "prefix", text: "/conf", want: CommandConfig},
		// A name ranks ahead of an alias that matches as well: "context" names /usage.
		{name: "name before alias", text: "/con", want: CommandContinue},
		{name: "alias prefix", text: "/set", want: CommandConfig},
		{name: "subsequence", text: "/chk", want: CommandCheckout},
		{name: "alias subsequence", text: "/tr", want: CommandCheckout},
		{name: "subsequence fewer skips", text: "/sts", want: CommandStatus},
		{name: "subsequence vowels dropped", text: "/mdl", want: CommandModel},
		{name: "prefix export", text: "/exp", want: CommandExport},
		{name: "alias context", text: "/ctx", want: CommandUsage},
		{name: "subsequence short", text: "/qt", want: CommandQuit},
		{name: "missing letter", text: "/chekout", want: CommandCheckout},
		{name: "swapped letters", text: "/lgoin", want: CommandLogin},
		{name: "swapped at end", text: "/modle", want: CommandModel},
		{name: "swapped short", text: "/nwe", want: CommandNew},
		{name: "swapped middle", text: "/stauts", want: CommandStatus},
		{name: "neighboring key", text: "/stqtus", want: CommandStatus},
		{name: "extra letter", text: "/quiit", want: CommandQuit},
		{name: "typo in partial input", text: "/lgo", want: CommandLogin},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := menuCommands(t, tt.text)
			if len(got) == 0 || got[0] != tt.want {
				t.Errorf("menu for %q = %q, want %q first", tt.text, got, tt.want)
			}
		})
	}
}

// resumeOther returns a step that resumes a stored session other than the
// active one.
func resumeOther(t *testing.T, a *app.App) step {
	id := message.NewSessionID()
	err := a.Sessions().AppendMessage(t.Context(), id, &message.Content{
		ID:    message.NewID(),
		Role:  message.RoleUser,
		Parts: []message.ContentPart{message.TextPart{Text: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return func(s *Service) []UIEvent { return s.HandleSessionSelect(id) }
}

// chooseInTree returns a step that chooses a reply other than the head from
// the session tree.
func chooseInTree(*testing.T, *app.App) step {
	return func(s *Service) []UIEvent {
		s.OpenTree(app.Tree{Messages: []message.Content{{ID: message.NewID(), Role: message.RoleAgent}}})
		return s.ChooseTreeRow()
	}
}

func TestCommandsWhileStreaming(t *testing.T) {
	tests := []struct {
		name  string
		step  func(*testing.T, *app.App) step
		stops bool
	}{
		{name: "config", step: func(*testing.T, *app.App) step { return submit("/config") }},
		{name: "status", step: func(*testing.T, *app.App) step { return submit("/status") }},
		{name: "usage", step: func(*testing.T, *app.App) step { return submit("/usage") }},
		{name: "help", step: func(*testing.T, *app.App) step { return submit("/help") }},
		{name: "copy", step: func(*testing.T, *app.App) step { return submit("/copy") }},
		{name: "export", step: func(*testing.T, *app.App) step { return submit("/export") }},
		{name: "login", step: func(*testing.T, *app.App) step { return submit("/login") }},
		{name: "quit", step: func(*testing.T, *app.App) step { return submit("/quit") }},
		{name: "resume menu", step: func(*testing.T, *app.App) step { return submit("/resume") }},
		{name: "checkout", step: func(*testing.T, *app.App) step { return submit("/checkout") }},
		// A run in progress is left to go on.
		{name: "continue", step: func(*testing.T, *app.App) step { return submit("/continue") }},
		{name: "new", step: func(*testing.T, *app.App) step { return submit("/new") }, stops: true},
		{name: "undo", step: func(*testing.T, *app.App) step { return submit("/undo") }, stops: true},
		{name: "redo", step: func(*testing.T, *app.App) step { return submit("/redo") }, stops: true},
		{name: "resume choice", step: resumeOther, stops: true},
		{name: "tree choice", step: chooseInTree, stops: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, a := newTestService(t)
				step := tt.step(t, a)
				s.NewSession()
				sess := a.Session(s.SessionID())
				s.StartScript("Demo", demo.Script())
				synctest.Wait()
				if !s.Streaming() {
					t.Fatal("the demo is not streaming")
				}

				if out := step(s); len(out) == 0 {
					t.Error("the command was ignored")
				}
				synctest.Wait()
				if stopped := !sess.Busy(); stopped != tt.stops {
					t.Errorf("run stopped = %v, want %v", stopped, tt.stops)
				}
				sess.Stop()
			})
		})
	}
}

func TestApplyAgentEventSession(t *testing.T) {
	tests := []struct {
		name    string
		session message.SessionID
		applied bool
	}{
		{name: "active session", session: "ses_active", applied: true},
		{name: "no session", session: "", applied: true},
		{name: "replaced session", session: "ses_replaced"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Service{state: &UIState{Session: &session.Session{ID: "ses_active"}}}
			out := s.ApplyAgentEvent(app.EventAgentEnded{EventBase: app.EventBase{SessionID: tt.session}})
			if applied := len(out) > 0; applied != tt.applied {
				t.Errorf("event of session %q applied = %v, want %v", tt.session, applied, tt.applied)
			}
		})
	}
}

func TestSlashMenuNoMatch(t *testing.T) {
	tests := []struct {
		name, text string
	}{
		{name: "empty", text: ""},
		{name: "no slash", text: "status"},
		{name: "unrelated letters", text: "/xyz"},
		{name: "too short for a typo", text: "/gl"},
		{name: "path", text: "/path/to/file"},
		{name: "whitespace", text: "/new feature"},
		{name: "trailing space", text: "/new "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := menuCommands(t, tt.text); len(got) != 0 {
				t.Errorf("menu for %q = %q, want none", tt.text, got)
			}
		})
	}
}

func TestAgentEndedIndicator(t *testing.T) {
	failed := app.NewError(app.CodeUnknown, "connection reset by peer")
	rejected := app.Error{Code: app.CodeProviderError, Message: "provider-a response error (400): bad", Provider: providerA, Status: 400,
		Body: `{"error":{"message":"bad"}}`}
	tests := []struct {
		name  string
		event app.Event
		want  UIEvent
	}{
		{name: "run finished", event: app.EventAgentEnded{}, want: EventIndicatorHide{}},
		{name: "run failed", event: app.EventAgentEnded{Error: &failed},
			want: EventIndicatorShow{Text: failed.Message, Error: true}},
		// A provider's error response shows its provider and status over
		// its body, formatted.
		{name: "run rejected", event: app.EventAgentEnded{Error: &rejected},
			want: EventIndicatorShow{Text: "provider-a response error (400)", Error: true, JSON: true,
				Detail: "{\n  \"error\": {\n    \"message\": \"bad\"\n  }\n}"}},
		{name: "message failed", event: app.EventMessageEnded{Message: app.Message{ID: "m1"}, Error: &failed}},
		{name: "message stopped", event: app.EventMessageEnded{Message: app.Message{ID: "m1"}, Stopped: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Service{state: &UIState{}}
			var got []UIEvent
			for _, ev := range s.ApplyAgentEvent(tt.event) {
				switch ev.(type) {
				case EventIndicatorShow, EventIndicatorHide:
					got = append(got, ev)
				}
			}
			var want []UIEvent
			if tt.want != nil {
				want = []UIEvent{tt.want}
			}
			if !slices.Equal(got, want) {
				t.Errorf("indicator events = %v, want %v", got, want)
			}
		})
	}
}

func TestUsageShowsOnMessageEnd(t *testing.T) {
	usage := message.Usage{InputTokens: 1200, OutputTokens: 300}
	msg := message.Content{ID: "a1", Role: message.RoleAgent, Parts: []message.ContentPart{
		message.TextPart{ID: "a1-text", Text: "Done."},
		message.UsagePart{ID: "a1-usage", Usage: usage},
	}}
	tests := []struct {
		name  string
		event app.Event
		want  message.Usage
	}{
		{name: "part", event: app.EventPartUpdated{PartChange: app.PartChange{MessageID: "a1", Part: msg.Parts[0]}}},
		// The usage part arrives before the message's tools run, and its
		// counts wait for the message to end.
		{name: "usage part", event: app.EventPartEnded{PartChange: app.PartChange{MessageID: "a1", Part: msg.Parts[1]}}},
		{name: "end", event: app.EventMessageEnded{Message: app.Message{Content: msg}}, want: usage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newTestService(t)
			s.state.Messages = []ChatMessage{ChatMessageOf(msg)}
			updated := false
			for _, ev := range s.ApplyAgentEvent(tt.event) {
				if u, ok := ev.(EventMessageUpdated); ok {
					updated = true
					if u.Msg.Usage != tt.want {
						t.Errorf("usage = %+v, want %+v", u.Msg.Usage, tt.want)
					}
				}
			}
			if !updated {
				t.Error("no message update")
			}
		})
	}
}

// TestSlashAliases checks that each command runs by its name and by each of
// its aliases, that no two commands share a name, and that a command's
// description contains each of its aliases, so that the menu row reads as
// what the alias asks for.
func TestSlashAliases(t *testing.T) {
	seen := make(map[Command]Command)
	cmds := builtinCommands(t)
	for _, cmd := range cmds {
		for name := range cmd.Names() {
			if other, ok := seen[name]; ok {
				t.Errorf("%q names both %s and %s", name, other, cmd.Name)
			}
			seen[name] = cmd.Name
			if got, ok := LookupCommand(cmds, name); !ok || got.Name != cmd.Name {
				t.Errorf("LookupCommand(%q) = %s, %v; want %s", name, got.Name, ok, cmd.Name)
			}
		}
		for _, alias := range cmd.Aliases {
			if !strings.Contains(strings.ToLower(cmd.Desc), string(alias)) {
				t.Errorf("%s description %q lacks its alias %q", cmd.Name, cmd.Desc, alias)
			}
		}
	}
	for alias, want := range map[Command]Command{
		"session": CommandResume, "history": CommandResume, "clear": CommandNew, "info": CommandStatus,
		"settings": CommandConfig, "auth": CommandLogin, "exit": CommandQuit, "context": CommandUsage,
		"tree": CommandCheckout,
	} {
		if seen[alias] != want {
			t.Errorf("alias %q runs %q, want %s", alias, seen[alias], want)
		}
	}
}

// TestSlashMenuFindsAliases checks that a search finds a command by an
// alias, and shows the command's row as it always is: its name alone, with
// the alias in its description.
func TestSlashMenuFindsAliases(t *testing.T) {
	s := &Service{state: &UIState{}}
	for query, want := range map[string]Command{
		"hist": CommandResume, "session": CommandResume, "clear": CommandNew, "exit": CommandQuit, "settings": CommandConfig,
		"context": CommandUsage,
	} {
		opts := s.SlashMenuItems(query)
		if len(opts) == 0 {
			t.Errorf("%q matches nothing, want %s first", query, want)
			continue
		}
		if got := opts[0]; got.Value != want || got.Name != want.String() || got.Hint != "" {
			t.Errorf("%q first row = %q with hint %q, want %s alone", query, got.Name, got.Hint, want)
		}
	}
}

// TestSubmitAlias checks that a command typed by an alias runs.
func TestSubmitAlias(t *testing.T) {
	s, _ := newTestService(t)
	s.InitSession(t.Context(), "")
	if !slices.Contains(s.HandleSubmit("/exit"), UIEvent(EventQuit{})) {
		t.Error("/exit did not quit")
	}
}
