package ui

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
)

// TestTickIndicator checks that the working indicator counts the time the
// turn in progress has taken, from its prompt, and that a tick shows nothing
// once the indicator is hidden or shows an error instead.
func TestTickIndicator(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &Service{state: &UIState{}}
		now := func() app.EventBase { return app.EventBase{Timestamp: time.Now()} }
		if out := s.TickIndicator(); out != nil {
			t.Errorf("tick before any run = %v, want nothing", out)
		}

		// The prompt's run has not started yet.
		if got, want := s.ShowIndicator(""), []UIEvent{WorkingIndicator(0, false)}; !slices.Equal(got, want) {
			t.Errorf("ShowIndicator(\"\") = %v, want %v", got, want)
		}
		s.ApplyAgentEvent(app.EventAgentStarted{EventBase: now()})
		time.Sleep(time.Second)
		s.ApplyAgentEvent(app.EventMessageStarted{EventBase: now(), Message: app.Message{ID: "u1", Role: message.RoleUser}})
		time.Sleep(12 * time.Second)
		if got, want := s.TickIndicator(), []UIEvent{WorkingIndicator(12*time.Second, false)}; !slices.Equal(got, want) {
			t.Errorf("tick 12s after the prompt = %v, want %v", got, want)
		}

		s.HideIndicator()
		if out := s.TickIndicator(); out != nil {
			t.Errorf("tick once hidden = %v, want nothing", out)
		}
		s.ShowIndicator("")
		s.ShowIndicator("Request failed")
		if out := s.TickIndicator(); out != nil {
			t.Errorf("tick over an error = %v, want nothing", out)
		}
	})
}

// TestWorkingIndicator checks that the indicator of a run in progress shows
// its time, followed by the key that cancels it and what Ctrl+O does next to
// the details of the tool calls.
func TestWorkingIndicator(t *testing.T) {
	want := EventIndicatorShow{Text: "Working [1m 05s]", Hint: "[esc cancel] [ctrl+o expand]"}
	if got := WorkingIndicator(65*time.Second, false); got != want {
		t.Errorf("WorkingIndicator(65s, false) = %+v, want %+v", got, want)
	}
	want.Hint = "[esc cancel] [ctrl+o collapse]"
	if got := WorkingIndicator(65*time.Second, true); got != want {
		t.Errorf("WorkingIndicator(65s, true) = %+v, want %+v", got, want)
	}
}

// TestToolsToggleText checks that the indicator while Ctrl+O lays the
// transcript out again names the toggle, and the time of a turn in progress.
func TestToolsToggleText(t *testing.T) {
	for _, tt := range []struct {
		expanded bool
		elapsed  time.Duration
		want     string
	}{
		{expanded: true, want: "Expanding"},
		{expanded: false, want: "Collapsing"},
		{expanded: true, elapsed: 12 * time.Second, want: "Expanding [12s]"},
		{expanded: false, elapsed: 65 * time.Second, want: "Collapsing [1m 05s]"},
	} {
		if got := ToolsToggleText(tt.expanded, tt.elapsed); got != tt.want {
			t.Errorf("ToolsToggleText(%v, %v) = %q, want %q", tt.expanded, tt.elapsed, got, tt.want)
		}
	}
}

// TestSessionSelectSettlesIndicator checks that switching to a session with
// no run in progress hides the working indicator, which the end of the run
// of the session left would otherwise never hide.
func TestSessionSelectSettlesIndicator(t *testing.T) {
	s, a := newTestService(t)
	s.InitSession(t.Context(), "")
	id := storePrompts(t, a, "run the tests")
	s.state.Running = true
	s.ShowIndicator("")

	out := s.HandleSessionSelect(id)
	if !slices.Contains(out, UIEvent(EventIndicatorHide{})) || s.state.Working {
		t.Errorf("switch = %v with working %v, want the indicator hidden", out, s.state.Working)
	}
}
