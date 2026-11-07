package ui

import (
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/terminal"
	"github.com/unaquatic/fluid/widget"
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

// TestShimmerFg checks that the shimmer's band lightens the columns near its
// center toward shimmerFg, the most at its center, and leaves the text as it
// was before the band enters and once it has left.
func TestShimmerFg(t *testing.T) {
	const cols = 14
	half := indicatorFg.Blend(shimmerFg, 0.5)
	tests := []struct {
		name  string
		x     int
		phase float64
		want  style.Color
	}{
		{name: "before the band enters", x: 0, phase: 0, want: indicatorFg},
		{name: "after the band has left", x: cols - 1, phase: 1, want: indicatorFg},
		// Halfway across, the band's center is at column 7.
		{name: "center", x: 7, phase: 0.5, want: shimmerFg},
		{name: "halfway to the edge", x: 5, phase: 0.5, want: half},
		{name: "halfway to the other edge", x: 9, phase: 0.5, want: half},
		{name: "beyond the reach", x: 3, phase: 0.5, want: indicatorFg},
	}
	for _, tt := range tests {
		if got := ShimmerFg(indicatorFg, tt.x, cols, tt.phase); got != tt.want {
			t.Errorf("%s: ShimmerFg(x %d, phase %v) = %v, want %v", tt.name, tt.x, tt.phase, got, tt.want)
		}
	}
}

// TestShimmerFollowsTheRun checks that the shimmer moves every ShimmerTick
// while the indicator shows the time of a run in progress, from the left as
// the run starts, and leaves the event loop alone otherwise, as when the
// indicator shows an error.
func TestShimmerFollowsTheRun(t *testing.T) {
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
		wg.Go(func() { ui.AnimateShimmer(enqueue, done) })
		defer wg.Wait()
		defer close(done)

		idle := func(when string) {
			t.Helper()
			time.Sleep(3 * ShimmerTick)
			synctest.Wait()
			select {
			case <-work:
				t.Errorf("%s: the shimmer woke the event loop", when)
			default:
			}
		}
		idle("before a run")

		ui.Apply(ui.service.ShowIndicator(""))
		if !ui.shimmer.On.Load() || ui.shimmer.Phase != 0 {
			t.Fatalf("run started: shimmering %v at %v; want it from the left", ui.shimmer.On.Load(), ui.shimmer.Phase)
		}
		(<-work)()
		if want := float64(ShimmerTick) / float64(shimmerSweep); ui.shimmer.Phase != want {
			t.Errorf("after a tick the band is at %v, want %v", ui.shimmer.Phase, want)
		}

		ui.Apply(ui.service.ShowIndicator("Request failed"))
		if ui.shimmer.On.Load() {
			t.Error("the shimmer runs over an error")
		}
		idle("over an error")
	})
}

// TestShimmerPausesBetweenSweeps checks that each sweep starts at the left
// only once the band before it has left at the right and shimmerPause has
// passed, the band staying past the right edge meanwhile.
func TestShimmerPausesBetweenSweeps(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	ui.Apply(ui.service.ShowIndicator(""))
	start := ui.shimmer.Start
	for _, tt := range []struct {
		name  string
		at    time.Duration
		phase float64
	}{
		{name: "halfway across", at: shimmerSweep / 2, phase: 0.5},
		{name: "left at the right", at: shimmerSweep, phase: 1},
		{name: "paused", at: shimmerSweep + shimmerPause/2, phase: 1},
		{name: "next sweep", at: shimmerSweep + shimmerPause, phase: 0},
		{name: "halfway across again", at: shimmerSweep + shimmerPause + shimmerSweep/2, phase: 0.5},
	} {
		ui.StepShimmer(start.Add(tt.at))
		if ui.shimmer.Phase != tt.phase {
			t.Errorf("%s: band at %v, want %v", tt.name, ui.shimmer.Phase, tt.phase)
		}
	}
}

// TestShimmerSkipsOtherText checks that text ShowIndicatorWhile shows during
// a run does not shimmer, and that the shimmer comes back with the run's
// time.
func TestShimmerSkipsOtherText(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	ui.Apply(ui.service.ShowIndicator(""))
	ui.ShowIndicatorWhile(ExpandingToolsText, func() {
		if ui.shimmer.On.Load() {
			t.Errorf("%q shimmers", ExpandingToolsText)
		}
	})
	if !ui.shimmer.On.Load() {
		t.Error("the shimmer did not come back with the run's time")
	}
}

// TestToolRowShimmers checks that the shimmer's band sweeps across the row of
// a collapsed compact call while the call executes and the band sweeps the
// indicator's time, and across no other call: not a queued or ended one, not
// an expanded one, and not one without a shimmer. A row shimmers whichever
// way expanded calls show.
func TestToolRowShimmers(t *testing.T) {
	input := json.RawMessage(`{"command":"go test ./..."}`)
	running := message.ToolStateRunning{Input: input}
	tests := []struct {
		name     string
		state    message.ToolState
		style    ExpandedStyle
		expanded bool
		// still leaves the band still, as between runs, and none leaves the
		// call without a shimmer.
		still, none bool
		want        bool
	}{
		{name: "executing row", state: running, want: true},
		{name: "queued", state: message.ToolStatePending{Input: input}},
		{name: "completed", state: message.ToolStateCompleted{Input: input, Output: "ok"}},
		{name: "failed", state: message.ToolStateError{Input: input, Error: "FAIL\n\n[exit 1]"}},
		{name: "plain row", state: running, style: ExpandedPlain, want: true},
		{name: "expanded", state: running, expanded: true},
		{name: "band still", state: running, still: true},
		{name: "no shimmer", state: running, none: true},
	}
	for _, tt := range tests {
		box := newTestToolBox(t, message.ToolPart{Name: "bash", State: tt.state}, DefaultTheme(), style.ColorWhite)
		if !tt.none {
			box.Shimmer = new(Shimmer)
			box.Shimmer.On.Store(!tt.still)
		}
		box.SetStyle(tt.style)
		box.SetExpanded(tt.expanded)
		if got := box.Shimmers(); got != tt.want {
			t.Errorf("%s: Shimmers() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// TestToolRowShimmerRepaints checks that frames that repaint only what
// changed, as the inline renderer draws them, move the band across an
// executing row each time the shimmer steps, while only the indicator beside
// the row changes otherwise, and leave the row in the colors it has without
// the band once the band stops. The shimmer tracks the row while the call
// executes, and forgets it once the call has ended.
func TestToolRowShimmerRepaints(t *testing.T) {
	const w = 40
	theme := DefaultTheme()
	input := json.RawMessage(`{"command":"go test ./..."}`)
	call := message.ToolPart{Name: "bash", State: message.ToolStateRunning{Input: input}}
	fgs := func(buf *buffer.Buffer) []style.Color {
		var got []style.Color
		for x := range w {
			got = append(got, buf.Cell(x, 0).Fg)
		}
		return got
	}
	// rowAt draws the call alone, with the band at phase while swept is
	// true, and without it otherwise.
	rowAt := func(phase float64, swept bool) []style.Color {
		box := newTestToolBox(t, call, theme, theme.ResolveTextBase())
		if swept {
			box.Shimmer = &Shimmer{Phase: phase}
			box.Shimmer.On.Store(true)
		}
		return fgs(renderFrames(w, 1, box))
	}

	shimmer := &Shimmer{Phase: 0.25}
	shimmer.On.Store(true)
	box := newTestToolBox(t, call, theme, theme.ResolveTextBase())
	box.Shimmer = shimmer
	box.SyncShimmer()
	// label stands in for the indicator, which each step of the band
	// repaints.
	label := widget.NewText("Working [12s]")
	root := terminal.NewRootRenderable(w, 2)
	root.Add(box)
	root.Add(label)
	buf := buffer.New(w, 2)
	// frame draws what changed since the last frame, or all of it while
	// full is true, as the renderer draws a frame.
	frame := func(full bool) []style.Color {
		root.PrepareLayout(0)
		root.SyncPositions(0)
		damage := root.TakeDamage()
		if full {
			damage = buffer.Rect{}
		} else if damage.IsEmpty() {
			return fgs(buf)
		}
		root.Draw(buf, 0, damage)
		return fgs(buf)
	}
	tracked := func() bool { _, ok := shimmer.Rows[box.PartBox]; return ok }

	if got, want := frame(true), rowAt(0.25, true); !slices.Equal(got, want) {
		t.Errorf("first frame: row in %v, want %v", got, want)
	}
	// The band steps as StepShimmer steps it.
	shimmer.Phase = 0.5
	label.RequestRender()
	shimmer.SyncRows()
	if got, want := frame(false), rowAt(0.5, true); !slices.Equal(got, want) {
		t.Errorf("after a step: row in %v, want the band moved to %v", got, want)
	}
	shimmer.On.Store(false)
	shimmer.SyncRows()
	if got, want := frame(false), rowAt(0, false); !slices.Equal(got, want) {
		t.Errorf("after the band stopped: row in %v, want %v", got, want)
	}
	if !tracked() {
		t.Error("the shimmer forgot the row while its call executes")
	}
	box.SetTool(message.ToolPart{Name: "bash", State: message.ToolStateCompleted{Input: input, Output: "ok"}})
	shimmer.SyncRows()
	if tracked() {
		t.Error("the shimmer still tracks the row once its call has ended")
	}
}
