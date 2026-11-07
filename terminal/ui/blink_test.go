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

// TestStepBlink checks that StepBlink hides the blinking markers every
// other BlinkInterval while the blink runs, and that the blink's end shows
// them again.
func TestStepBlink(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	ui.Apply(ui.service.ShowIndicator(""))
	start := ui.blink.Start
	for _, tt := range []struct {
		at   time.Duration
		want bool
	}{
		{BlinkInterval / 2, false},
		{BlinkInterval, true},
		{3 * BlinkInterval / 2, true},
		{2 * BlinkInterval, false},
	} {
		ui.StepBlink(start.Add(tt.at))
		if ui.blink.Off != tt.want || ui.IndicatorMarkerHidden() != tt.want {
			t.Errorf("at %v: marker off %v, hidden %v; want %v", tt.at, ui.blink.Off, ui.IndicatorMarkerHidden(), tt.want)
		}
	}
	ui.StepBlink(start.Add(BlinkInterval))
	ui.Apply(ui.service.HideIndicator())
	if ui.blink.Off || ui.IndicatorMarkerHidden() {
		t.Error("the marker stays hidden after the blink stopped")
	}
}

// TestToolMarkerBlinkRepaints checks that frames that repaint only what
// changed, as the inline renderer draws them, hide and show the marker of an
// executing call as the blink says, without moving its header, and show it
// once the call has ended. The blink keeps tracking the row until it has
// drawn the marker shown again.
func TestToolMarkerBlinkRepaints(t *testing.T) {
	const w = 40
	theme := DefaultTheme()
	input := json.RawMessage(`{"command":"go test ./..."}`)
	call := message.ToolPart{Name: "bash", State: message.ToolStateRunning{Input: input}}

	blink := &Blink{}
	blink.On.Store(true)
	box := newTestToolBox(t, call, theme, theme.ResolveTextBase())
	box.Blink = blink
	box.SetMarker(true)
	box.SyncBlink()
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

	blink.Off = true
	blink.SyncRows()
	hidden := frame(false)
	if []rune(hidden)[0] != ' ' || string([]rune(hidden)[1:]) != string([]rune(shown)[1:]) {
		t.Errorf("blinked off: %q, want %q without its marker", hidden, shown)
	}

	blink.Off = false
	blink.SyncRows()
	if got := frame(false); got != shown {
		t.Errorf("blinked on: %q, want %q", got, shown)
	}

	blink.Off = true
	blink.SyncRows()
	frame(false)
	box.SetTool(message.ToolPart{Name: "bash", State: message.ToolStateCompleted{Input: input, Output: "ok"}})
	blink.SyncRows()
	if got := frame(false); []rune(got)[0] != marker {
		t.Errorf("call ended while hidden: %q, want the marker back", got)
	}
	// The next step of the blink forgets the row it drew shown.
	blink.SyncRows()
	if _, ok := blink.Rows[box.PartBox]; ok {
		t.Error("the blink still tracks the row once its call has ended and its marker shows")
	}
}
