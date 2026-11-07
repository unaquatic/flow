package ui

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/terminal"
	"github.com/unaquatic/fluid/widget"
)

// TestBlinkOff checks that a marker shows for the first BlinkInterval after
// it starts blinking, is hidden for the next, and so on.
func TestBlinkOff(t *testing.T) {
	start := time.Unix(0, 0)
	for _, tt := range []struct {
		at   time.Duration
		want bool
	}{
		{0, false},
		{BlinkInterval - time.Millisecond, false},
		{BlinkInterval, true},
		{2*BlinkInterval - time.Millisecond, true},
		{2 * BlinkInterval, false},
		{3 * BlinkInterval, true},
	} {
		if got := BlinkOff(start, start.Add(tt.at)); got != tt.want {
			t.Errorf("BlinkOff at %v = %v, want %v", tt.at, got, tt.want)
		}
	}
}

// TestStepShimmerBlinks checks that StepShimmer hides the blinking markers
// every other BlinkInterval while the shimmer runs, the band paused or not,
// and that the shimmer's end shows them again.
func TestStepShimmerBlinks(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	ui.Apply(ui.service.ShowIndicator(""))
	start := ui.shimmer.Start
	for _, tt := range []struct {
		at   time.Duration
		want bool
	}{
		{BlinkInterval / 2, false},
		{BlinkInterval, true},
		// The band pauses past the right edge here, and the marker still
		// blinks.
		{shimmerSweep + shimmerPause/2, BlinkOff(start, start.Add(shimmerSweep+shimmerPause/2))},
		{2 * BlinkInterval, false},
	} {
		ui.StepShimmer(start.Add(tt.at))
		if ui.shimmer.MarkerOff != tt.want || ui.IndicatorMarkerHidden() != tt.want {
			t.Errorf("at %v: marker off %v, hidden %v; want %v", tt.at, ui.shimmer.MarkerOff, ui.IndicatorMarkerHidden(), tt.want)
		}
	}
	ui.StepShimmer(start.Add(BlinkInterval))
	ui.Apply(ui.service.HideIndicator())
	if ui.shimmer.MarkerOff || ui.IndicatorMarkerHidden() {
		t.Error("the marker stays hidden after the shimmer stopped")
	}
}

// TestToolMarkerBlinkRepaints checks that frames that repaint only what
// changed, as the inline renderer draws them, hide and show the marker of an
// executing call as the blink says, without moving its header, and show it
// once the call has ended. The shimmer keeps tracking the row until it has
// drawn the marker shown again.
func TestToolMarkerBlinkRepaints(t *testing.T) {
	const w = 40
	theme := DefaultTheme()
	input := json.RawMessage(`{"command":"go test ./..."}`)
	call := message.ToolPart{Name: "bash", State: message.ToolStateRunning{Input: input}}

	shimmer := &Shimmer{}
	shimmer.On.Store(true)
	box := newTestToolBox(t, call, theme, theme.ResolveTextBase())
	box.Shimmer = shimmer
	box.SetMarker(true)
	box.SyncShimmer()
	label := widget.NewText("Working [12s]")
	root := terminal.NewRootRenderable(w, 2)
	root.Add(box)
	root.Add(label)
	buf := buffer.New(w, 2)
	frame := func(full bool) string {
		root.PrepareLayout(0)
		root.SyncPositions(0)
		damage := root.TakeDamage()
		if full {
			damage = buffer.Rect{}
		}
		if full || !damage.IsEmpty() {
			root.Draw(buf, 0, damage)
		}
		var row []rune
		for x := range w {
			row = append(row, buf.Cell(x, 0).Char)
		}
		return string(row)
	}
	marker := []rune(ToolMarker)[0]
	shown := frame(true)
	if []rune(shown)[0] != marker {
		t.Fatalf("first frame %q, want it to start with the marker", shown)
	}

	shimmer.MarkerOff = true
	shimmer.SyncRows()
	hidden := frame(false)
	if []rune(hidden)[0] != ' ' || string([]rune(hidden)[1:]) != string([]rune(shown)[1:]) {
		t.Errorf("blinked off: %q, want %q without its marker", hidden, shown)
	}

	shimmer.MarkerOff = false
	shimmer.SyncRows()
	if got := frame(false); got != shown {
		t.Errorf("blinked on: %q, want %q", got, shown)
	}

	shimmer.MarkerOff = true
	shimmer.SyncRows()
	frame(false)
	box.SetTool(message.ToolPart{Name: "bash", State: message.ToolStateCompleted{Input: input, Output: "ok"}})
	shimmer.SyncRows()
	if got := frame(false); []rune(got)[0] != marker {
		t.Errorf("call ended while hidden: %q, want the marker back", got)
	}
	// The next step of the shimmer forgets the row it drew shown.
	shimmer.SyncRows()
	if _, ok := shimmer.Rows[box.PartBox]; ok {
		t.Error("the shimmer still tracks the row once its call has ended and its marker shows")
	}
}
