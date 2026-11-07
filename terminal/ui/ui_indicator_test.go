package ui

import (
	"encoding/json"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/fluid/style"
)

// TestTickIndicatorCounts checks that the indicator's time goes up every
// IndicatorTick while a run is in progress, and that the run's end leaves the
// indicator hidden, ticks or not.
func TestTickIndicatorCounts(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	synctest.Test(t, func(t *testing.T) {
		// work stands in for the event loop, which runs what the ticker
		// enqueues.
		work, done := make(chan func()), make(chan struct{})
		enqueue := func(fn func()) {
			select {
			case work <- fn:
			case <-done:
			}
		}
		var wg sync.WaitGroup
		wg.Go(func() { ui.TickIndicator(enqueue, done) })
		defer wg.Wait()
		defer close(done)

		now := func() app.EventBase { return app.EventBase{Timestamp: time.Now()} }
		ui.Apply(ui.service.ShowIndicator(""))
		ui.Apply(ui.service.ApplyAgentEvent(app.EventAgentStarted{EventBase: now()}))
		ui.Apply(ui.service.ApplyAgentEvent(app.EventMessageStarted{EventBase: now(),
			Message: app.Message{ID: "u1", Role: message.RoleUser}}))
		for _, want := range []string{
			"Working [1s] [esc cancel] [ctrl+o expand]",
			"Working [2s] [esc cancel] [ctrl+o expand]",
		} {
			(<-work)()
			if got := ui.indicatorLabel.PlainText(); got != want || !ui.indicatorBox.Visible() {
				t.Errorf("indicator = %q, visible %v; want %q shown", got, ui.indicatorBox.Visible(), want)
			}
		}

		ui.Apply(ui.service.ApplyAgentEvent(app.EventAgentEnded{EventBase: now()}))
		(<-work)()
		if ui.indicatorBox.Visible() {
			t.Errorf("indicator %q shown after the run ended and a tick", ui.indicatorLabel.PlainText())
		}
	})
}

// TestBlinkFollowsTheRun checks that the blink is checked every BlinkTick
// while the indicator shows the time of a run in progress, its markers shown
// as the run starts, and leaves the event loop alone otherwise, as when the
// indicator shows an error.
func TestBlinkFollowsTheRun(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	synctest.Test(t, func(t *testing.T) {
		work, done := make(chan func()), make(chan struct{})
		enqueue := func(fn func()) {
			select {
			case work <- fn:
			case <-done:
			}
		}
		var wg sync.WaitGroup
		wg.Go(func() { ui.AnimateBlink(enqueue, done) })
		defer wg.Wait()
		defer close(done)

		idle := func(when string) {
			t.Helper()
			time.Sleep(3 * BlinkTick)
			synctest.Wait()
			select {
			case <-work:
				t.Errorf("%s: the blink woke the event loop", when)
			default:
			}
		}
		idle("before a run")

		ui.Apply(ui.service.ShowIndicator(""))
		if !ui.blink.On.Load() || ui.blink.Off {
			t.Fatalf("run started: blinking %v, off %v; want it blinking, its markers shown", ui.blink.On.Load(), ui.blink.Off)
		}
		(<-work)()
		if ui.blink.Off {
			t.Error("the markers are hidden a tick into the run, want them shown for BlinkInterval")
		}

		ui.Apply(ui.service.ShowIndicator("Request failed"))
		if ui.blink.On.Load() {
			t.Error("the markers blink over an error")
		}
		idle("over an error")
	})
}

// TestBlinkSkipsOtherText checks that the marker does not blink before text
// ShowIndicatorWhile shows during a run, and that the blink comes back with
// the run's time.
func TestBlinkSkipsOtherText(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	ui.Apply(ui.service.ShowIndicator(""))
	ui.ShowIndicatorWhile(ExpandingToolsText, func() {
		if ui.blink.On.Load() {
			t.Errorf("the marker blinks before %q", ExpandingToolsText)
		}
	})
	if !ui.blink.On.Load() {
		t.Error("the blink did not come back with the run's time")
	}
}

// TestToolMarkerBlinks checks that the marker of a call blinks while the
// call executes, the marker shows, and the blink runs, and that of no other
// call: not a queued or ended one, not one whose marker is off, and not one
// without a blink, or while the blink is stopped.
func TestToolMarkerBlinks(t *testing.T) {
	input := json.RawMessage(`{"command":"go test ./..."}`)
	running := message.ToolStateRunning{Input: input}
	tests := []struct {
		name  string
		state message.ToolState
		// unmarked hides the marker, stopped stops the blink, as between
		// runs, and none leaves the call without a blink.
		unmarked, stopped, none bool
		want                    bool
	}{
		{name: "executing", state: running, want: true},
		{name: "queued", state: message.ToolStatePending{Input: input}},
		{name: "completed", state: message.ToolStateCompleted{Input: input, Output: "ok"}},
		{name: "failed", state: message.ToolStateError{Input: input, Error: "FAIL\n\n[exit 1]"}},
		{name: "unmarked", state: running, unmarked: true},
		{name: "blink stopped", state: running, stopped: true},
		{name: "no blink", state: running, none: true},
	}
	for _, tt := range tests {
		box := newTestToolBox(t, message.ToolPart{Name: "bash", State: tt.state}, DefaultTheme(), style.ColorWhite)
		if !tt.none {
			box.Blink = new(Blink)
			box.Blink.On.Store(!tt.stopped)
		}
		box.SetMarker(!tt.unmarked)
		if got := box.Blinks(); got != tt.want {
			t.Errorf("%s: Blinks() = %v, want %v", tt.name, got, tt.want)
		}
	}
}
