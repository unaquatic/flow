package ui

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// TestCtrlCQuitsWithinQuitWindow checks that an idle Ctrl+C arms the quit,
// that a second press quits while less than QuitWindow has passed, and that
// a press once it has passed arms the quit again instead.
func TestCtrlCQuitsWithinQuitWindow(t *testing.T) {
	tests := []struct {
		name  string
		after time.Duration
		quit  bool
	}{
		{name: "right after", after: 0, quit: true},
		{name: "just within", after: QuitWindow - time.Nanosecond, quit: true},
		{name: "lapsed", after: QuitWindow, quit: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := &Service{state: &UIState{}}
				if handled, out := s.HandleCtrlC(false); !handled || !slices.Contains(out, UIEvent(EventQuitArmed{})) {
					t.Fatalf("first Ctrl+C = %v, %v; want it to arm the quit", handled, out)
				}
				time.Sleep(tt.after)
				_, out := s.HandleCtrlC(false)
				if got := slices.Contains(out, UIEvent(EventQuit{})); got != tt.quit {
					t.Errorf("Ctrl+C %v later quits %v, want %v", tt.after, got, tt.quit)
				}
				if armed := slices.Contains(out, UIEvent(EventQuitArmed{})); armed == tt.quit {
					t.Errorf("Ctrl+C %v later arms the quit %v, want %v", tt.after, armed, !tt.quit)
				}
			})
		})
	}
}

// TestCtrlCWithInputDoesNotArmQuit checks that a Ctrl+C that clears the
// input leaves the quit unarmed, so that it shows no key that quits.
func TestCtrlCWithInputDoesNotArmQuit(t *testing.T) {
	s := &Service{state: &UIState{}}
	_, out := s.HandleCtrlC(true)
	if slices.Contains(out, UIEvent(EventQuitArmed{})) || s.QuitArmed(time.Now()) {
		t.Errorf("Ctrl+C on input = %v; want it to clear the input without arming the quit", out)
	}
}
