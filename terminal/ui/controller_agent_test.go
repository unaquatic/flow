package ui

import (
	"slices"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/app"
)

// turnT0 is the time the runs of turn tests start at.
var turnT0 = time.Date(2026, 10, 14, 9, 0, 0, 0, time.Local)

// at stamps an event d after turnT0.
func at(d time.Duration) app.EventBase { return app.EventBase{Timestamp: turnT0.Add(d)} }

// openPrompt opens prompt id d after turnT0.
func openPrompt(id message.ID, d time.Duration) app.Event {
	return app.EventMessageStarted{EventBase: at(d), Message: app.Message{ID: id, Role: message.RoleUser}}
}

// openReply opens reply id d after turnT0.
func openReply(id message.ID, d time.Duration) app.Event {
	return app.EventMessageStarted{EventBase: at(d), Message: app.Message{ID: id, Role: message.RoleAgent}}
}

// turnsEnded returns the EventTurnEnded events in out.
func turnsEnded(out []UIEvent) []EventTurnEnded {
	var ended []EventTurnEnded
	for _, ev := range out {
		if e, ok := ev.(EventTurnEnded); ok {
			ended = append(ended, e)
		}
	}
	return ended
}

// TestTurnWorked checks that a turn's time shows under its last reply once
// its run ends, or once a prompt in the same run opens the next turn, and
// that a turn that ends without a reply of its own shows none.
func TestTurnWorked(t *testing.T) {
	failed := &app.UnknownError{Message: "no model"}
	tests := []struct {
		name   string
		events []app.Event
		want   []EventTurnEnded
	}{
		// The turn starts with its prompt, and ends with its run.
		{name: "prompt and replies", events: []app.Event{
			app.EventAgentStarted{EventBase: at(0)},
			openPrompt("u1", time.Second),
			openReply("a1", 2*time.Second),
			openReply("a2", 20*time.Second),
			app.EventAgentEnded{EventBase: at(43 * time.Second)},
		}, want: []EventTurnEnded{{MessageID: "a2", Worked: 42 * time.Second}}},
		// A run without a prompt, as /continue starts, is timed from its start.
		{name: "continue", events: []app.Event{
			app.EventAgentStarted{EventBase: at(0)},
			openReply("a1", time.Second),
			app.EventAgentEnded{EventBase: at(3 * time.Second)},
		}, want: []EventTurnEnded{{MessageID: "a1", Worked: 3 * time.Second}}},
		// Each prompt of a demo ends the turn before it.
		{name: "demo", events: []app.Event{
			app.EventAgentStarted{EventBase: at(0)},
			openPrompt("u1", 0),
			openReply("a1", time.Second),
			openPrompt("u2", 5*time.Second),
			openReply("a2", 6*time.Second),
			app.EventAgentEnded{EventBase: at(12 * time.Second)},
		}, want: []EventTurnEnded{
			{MessageID: "a1", Worked: 5 * time.Second},
			{MessageID: "a2", Worked: 7 * time.Second},
		}},
		{name: "failed before the prompt", events: []app.Event{
			app.EventAgentStarted{EventBase: at(0)},
			app.EventAgentEnded{EventBase: at(time.Second), Error: failed},
		}},
		{name: "failed before a reply", events: []app.Event{
			app.EventAgentStarted{EventBase: at(0)},
			openPrompt("u1", 0),
			app.EventAgentEnded{EventBase: at(time.Second), Error: failed},
		}},
		// A run that fails before it adds a reply leaves the turn before it
		// as it was.
		{name: "failed after a turn", events: []app.Event{
			app.EventAgentStarted{EventBase: at(0)},
			openPrompt("u1", 0),
			openReply("a1", time.Second),
			app.EventAgentEnded{EventBase: at(4 * time.Second)},
			app.EventAgentStarted{EventBase: at(time.Minute)},
			app.EventAgentEnded{EventBase: at(time.Minute + time.Second), Error: failed},
		}, want: []EventTurnEnded{{MessageID: "a1", Worked: 4 * time.Second}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Service{state: &UIState{}}
			var got []EventTurnEnded
			for _, ev := range tt.events {
				got = append(got, turnsEnded(s.ApplyAgentEvent(ev))...)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("turns ended %+v, want %+v", got, tt.want)
			}
			// The transcript records each time, so a frame drawn from it
			// again shows the same.
			for _, e := range tt.want {
				i := slices.IndexFunc(s.state.Messages, func(m ChatMessage) bool { return m.ID == e.MessageID })
				if i < 0 {
					t.Fatalf("no reply %s in the transcript", e.MessageID)
				}
				if m := s.state.Messages[i]; m.Worked != e.Worked {
					t.Errorf("reply %s records worked %v, want %v", e.MessageID, m.Worked, e.Worked)
				}
			}
		})
	}
}

// TestTurnWorkedSessionSet checks that a run whose start came before its
// session was made active, as one still ending in a session the terminal
// opens, shows no time.
func TestTurnWorkedSessionSet(t *testing.T) {
	s := &Service{state: &UIState{}}
	s.SetSession(&session.Session{ID: "s1"})
	base := func(d time.Duration) app.EventBase {
		b := at(d)
		b.SessionID = "s1"
		return b
	}
	s.ApplyAgentEvent(app.EventAgentStarted{EventBase: base(0)})
	s.ApplyAgentEvent(app.EventMessageStarted{EventBase: base(time.Second),
		Message: app.Message{ID: "a1", Role: message.RoleAgent}})

	s.SetSession(&session.Session{ID: "s1"})
	if ended := turnsEnded(s.ApplyAgentEvent(app.EventAgentEnded{EventBase: base(5 * time.Second)})); len(ended) != 0 {
		t.Errorf("turns ended %+v, want none", ended)
	}
}

// TestReplyWindow checks that a reply that ends with usage carries the prompt
// window of its model, as the catalog lists it, to its footer, both as it
// ends and once loaded, and that a reply without usage, or of a model the
// catalog lacks, carries none.
func TestReplyWindow(t *testing.T) {
	s, a := newTestService(t)
	want := offeringOf(t, a, "openai/gpt-5").Limit.PromptWindow()
	usage := message.Usage{InputTokens: 1_000, OutputTokens: 100}

	reply := message.Content{ID: "a1", Role: message.RoleAgent, Model: "openai/gpt-5"}
	s.ApplyAgentEvent(app.EventMessageStarted{Message: app.Message{Content: reply}})
	reply.Parts = []message.ContentPart{message.UsagePart{ID: "a1-usage", Usage: usage}}
	var shown []int
	for _, ev := range s.ApplyAgentEvent(app.EventMessageEnded{Message: app.Message{Content: reply}}) {
		if e, ok := ev.(EventMessageUpdated); ok {
			shown = append(shown, e.Msg.Window)
		}
	}
	if !slices.Equal(shown, []int{want}) || s.state.Messages[0].Window != want {
		t.Errorf("ended reply shows windows %v and records %d, want %d", shown, s.state.Messages[0].Window, want)
	}

	loaded := []ChatMessage{
		{ID: "a1", Role: message.RoleAgent, Model: "openai/gpt-5", Usage: usage},
		{ID: "a2", Role: message.RoleAgent, Model: "openai/gpt-5"},
		{ID: "a3", Role: message.RoleAgent, Model: "provider/model", Usage: usage},
	}
	s.AssignWindows(loaded)
	for i, w := range []int{want, 0, 0} {
		if loaded[i].Window != w {
			t.Errorf("loaded %s window = %d, want %d", loaded[i].ID, loaded[i].Window, w)
		}
	}
}

func TestAssignWorked(t *testing.T) {
	msg := func(id message.ID, role message.Role, d time.Duration) ChatMessage {
		return ChatMessage{ID: id, Role: role, CreatedAt: turnT0.Add(d)}
	}
	msgs := []ChatMessage{
		msg("u1", message.RoleUser, 0),
		msg("a1", message.RoleAgent, 3*time.Second),
		msg("a2", message.RoleAgent, 42*time.Second),
		// A prompt whose run failed before a reply, and the prompt sent
		// after it, which opens the turn.
		msg("u2", message.RoleUser, time.Minute),
		msg("u3", message.RoleUser, 2*time.Minute),
		msg("a3", message.RoleAgent, 2*time.Minute+5*time.Second),
		// A reply without a creation time records nothing.
		msg("u4", message.RoleUser, 3*time.Minute),
		{ID: "a4", Role: message.RoleAgent},
	}
	AssignWorked(msgs)

	// Each turn finishes when its last reply was stored.
	want := map[message.ID]time.Duration{
		"a2": 42 * time.Second,
		"a3": 5 * time.Second,
	}
	for _, m := range msgs {
		if m.Worked != want[m.ID] {
			t.Errorf("%s worked %v, want %v", m.ID, m.Worked, want[m.ID])
		}
	}
}

// TestOpenSessionTimesTurns checks that a stored session's turns are timed
// from when their prompt and last reply were stored.
func TestOpenSessionTimesTurns(t *testing.T) {
	s, a := newTestService(t)
	id := storePrompts(t, a, "run the tests", "commit it")
	if err := s.OpenSession(id); err != nil {
		t.Fatal(err)
	}
	if len(s.state.Messages) != 4 {
		t.Fatalf("got %d messages, want 4", len(s.state.Messages))
	}
	for turn := range slices.Chunk(s.state.Messages, 2) {
		prompt, reply := turn[0], turn[1]
		if prompt.CreatedAt.IsZero() || reply.CreatedAt.IsZero() {
			t.Fatalf("turn %s, %s has no creation times", prompt.ID, reply.ID)
		}
		if want := reply.CreatedAt.Sub(prompt.CreatedAt); reply.Worked != want {
			t.Errorf("reply %s worked = %v, want %v", reply.ID, reply.Worked, want)
		}
	}
}
