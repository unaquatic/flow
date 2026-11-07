package ui

import "testing"

// TestToggleInlineMode checks that the toggle switches between inline mode
// and fullscreen, and adds no notice to the transcript.
func TestToggleInlineMode(t *testing.T) {
	state := &UIState{}
	s := &Service{state: state}

	events := s.ToggleInlineMode()

	if !state.Inline {
		t.Fatal("expected inline mode to be enabled")
	}
	if len(events) != 1 {
		t.Fatalf("got %d events, want the mode change alone", len(events))
	}
	if e, ok := events[0].(EventInlineModeSet); !ok || !e.Inline {
		t.Fatalf("event is %#v, want EventInlineModeSet{Inline: true}", events[0])
	}

	events = s.ToggleInlineMode()

	if state.Inline {
		t.Fatal("expected inline mode to be disabled")
	}
	if e := events[0].(EventInlineModeSet); e.Inline {
		t.Error("expected EventInlineModeSet.Inline=false")
	}
}
