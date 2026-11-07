package ui

import (
	"math"
	"sync/atomic"
	"time"

	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/textbuf"
)

// IndicatorTick is how often the time a run has taken is brought up to date
// in the indicator.
const IndicatorTick = time.Second

// indicatorFg is the foreground of the indicator, and indicatorHintFg that of
// a line with a hint, as the time of a run in progress with the key that
// cancels it: PanelDimFg, the color of the keys a panel names after its
// title.
var (
	indicatorFg     = style.ColorFromInts(120, 120, 120, 255)
	indicatorHintFg = PanelDimFg
)

// The shimmer is a lighter band that sweeps across the indicator from left
// to right, again and again, while it shows the time of a run in progress.
// Each sweep starts once the one before has left at the right and a pause
// has passed.
const (
	// ShimmerTick is how often the shimmer's band moves.
	ShimmerTick = 50 * time.Millisecond
	// shimmerSweep is how long one sweep takes, from the band entering at
	// the left until it has left at the right.
	shimmerSweep = 1800 * time.Millisecond
	// shimmerPause is how long the text stays as it is between two sweeps.
	shimmerPause = 800 * time.Millisecond
	// shimmerReach is how many columns the band lightens on each side of its
	// center.
	shimmerReach = 4
)

// shimmerFg is the foreground at the center of the shimmer's band.
var shimmerFg = style.ColorFromInts(230, 230, 230, 255)

// Shimmer is where the shimmer's band is. It sweeps across the time the
// indicator shows while a run is in progress, and in step with it across the
// header of each row whose content is busy, such as a tool call the run
// executes, as PartBox.Shimmers tells.
type Shimmer struct {
	// On reports that the band sweeps: while the indicator shows the time of
	// a run in progress. AnimateShimmer reads it off the event loop.
	On atomic.Bool
	// Start is when the band started sweeping.
	Start time.Time
	// Phase is how far across its text the band is, from 0 to 1, as
	// ShimmerFg takes it.
	Phase float64
	// MarkerOff reports that the markers that blink while the band sweeps
	// are hidden, as BlinkOff tells from Start: the marker before the
	// indicator's time, and those of the rows whose content is busy and of
	// the text a reply is writing.
	MarkerOff bool
	// Rows are the rows the band may sweep across, or was last drawn
	// across, as PartBox.SyncShimmer tracks them. SyncRows repaints
	// them as the band moves, starts, or stops: a frame reaches only the
	// parts of the scene that changed, which a row the band sweeps is not.
	Rows map[*PartBox]struct{}
}

// Track records row among Rows while tracked is true, and forgets it
// otherwise.
func (s *Shimmer) Track(row *PartBox, tracked bool) {
	if !tracked {
		delete(s.Rows, row)
		return
	}
	if s.Rows == nil {
		s.Rows = make(map[*PartBox]struct{})
	}
	s.Rows[row] = struct{}{}
}

// SyncRows repaints each of Rows that the band now crosses otherwise than
// when the row was drawn, as PartBox.SyncShimmer does.
func (s *Shimmer) SyncRows() {
	for row := range s.Rows {
		row.SyncShimmer()
	}
}

// ShowIndicator displays IndicatorText of e between the transcript and the
// input, and while it shows the time of a run in progress, the shimmer
// across the time.
func (ui *UI) ShowIndicator(e EventIndicatorShow) {
	ui.indicatorLabel.SetStyledContent(IndicatorText(e))
	ui.indicatorBox.SetGone(false)
	ui.ShowIndicatorMarker()
	ui.SyncShimmer()
}

// IndicatorText returns the line the indicator shows for e: its text, in the
// indicator's color, or, when e has a hint, its text and then a space and the
// hint, all in indicatorHintFg. The text stays the line's first chunk, which
// the shimmer sweeps.
func IndicatorText(e EventIndicatorShow) style.StyledText {
	if e.Hint == "" {
		return style.NewStyledText(style.Plain(e.Text))
	}
	fg := indicatorHintFg
	return style.NewStyledText(
		style.TextChunk{Text: e.Text, Fg: fg},
		style.TextChunk{Text: " " + e.Hint, Fg: fg},
	)
}

// TickIndicator brings the time a run has taken up to date every
// IndicatorTick, as Tick does, through enqueue, which runs a function on the
// event loop, until done is closed.
func (ui *UI) TickIndicator(enqueue func(func()), done <-chan struct{}) {
	ticker := time.NewTicker(IndicatorTick)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			enqueue(ui.Tick)
		}
	}
}

// Tick brings the time the turn in progress has taken up to date: in the
// indicator, and in the worked time of the slash menu's status row while the
// menu is open.
func (ui *UI) Tick() {
	ui.Apply(ui.service.TickIndicator())
	if ui.service.TurnElapsed() > 0 {
		ui.SyncSlashStatus()
	}
}

// HideIndicator dismisses the indicator.
func (ui *UI) HideIndicator() {
	ui.indicatorBox.SetGone(true)
	ui.SyncShimmer()
}

// ShowIndicatorWhile shows text in the indicator and draws a frame at once,
// so that the terminal shows it, then runs fn and puts the indicator back as
// it was. The terminal keeps showing text until the next
// frame, the one that draws what fn changed, is drawn: text says what holds
// the event loop when that frame is slow, as when it lays out a long
// transcript again.
func (ui *UI) ShowIndicatorWhile(text string, fn func()) {
	content, shown, shimmer := ui.indicatorLabel.Content(), ui.indicatorBox.Visible(), ui.shimmer.On.Load()
	ui.ShowIndicator(EventIndicatorShow{Text: text})
	// The shimmer marks the time of a run, not text.
	ui.shimmer.On.Store(false)
	ui.renderer.Render()
	fn()
	ui.indicatorLabel.SetStyledContent(content)
	ui.indicatorBox.SetGone(!shown)
	ui.shimmer.On.Store(shimmer)
}

// SyncShimmer runs the shimmer while the indicator shows the time of a run in
// progress, its band entering at the left and the blinking markers shown as
// it starts, and stops it otherwise, showing the markers again.
func (ui *UI) SyncShimmer() {
	on := ui.indicatorBox.Visible() && ui.service.state.Working
	was := ui.shimmer.On.Load()
	if on && !was {
		ui.shimmer.Start, ui.shimmer.Phase = time.Now(), 0
	}
	if on != was {
		ui.shimmer.MarkerOff = false
		ui.indicatorMarker.RequestRender()
		ui.RepaintTextMarkers()
	}
	ui.shimmer.On.Store(on)
	ui.shimmer.SyncRows()
}

// ShowIndicatorMarker shows the marker before the indicator's text while a
// turn is in progress, in its left padding, as a tool call shows its marker,
// and hides it otherwise. The text stays where it is either way.
func (ui *UI) ShowIndicatorMarker() {
	show := ui.service.state.Working
	if ui.indicatorMarker.Visible() == show {
		return
	}
	ui.indicatorMarker.SetGone(!show)
	margin := 0
	if show {
		margin = -PaddingX
	}
	SetEdge(ui.indicatorRow, &ui.indicatorRow.LayoutNode().MarginLeft, margin)
}

// IndicatorMarkerHidden reports whether the marker before the indicator's
// text is hidden: while the shimmer runs and blinks it off.
func (ui *UI) IndicatorMarkerHidden() bool {
	return ui.shimmer.On.Load() && ui.shimmer.MarkerOff
}

// RepaintTextMarkers repaints the text that every bubble is writing, so that
// each draws its marker as the blink now shows it.
func (ui *UI) RepaintTextMarkers() {
	for cm := range ui.Bubbles() {
		cm.RepaintWritingMarkers()
	}
}

// AnimateShimmer moves the shimmer's band every ShimmerTick while the
// shimmer runs, through enqueue, which runs a function on the event loop,
// until done is closed. It leaves the event loop alone while the shimmer is
// stopped.
func (ui *UI) AnimateShimmer(enqueue func(func()), done <-chan struct{}) {
	ticker := time.NewTicker(ShimmerTick)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case now := <-ticker.C:
			if ui.shimmer.On.Load() {
				enqueue(func() { ui.StepShimmer(now) })
			}
		}
	}
}

// StepShimmer moves the shimmer's band to where it is at now, and repaints
// the indicator and the tool call rows the band sweeps when the band moved,
// while the shimmer runs. During the pause after a sweep the band stays past
// the right edge. It also shows or hides the blinking markers as BlinkOff
// tells, and repaints them when that changed. The repaint leaves the layout
// as it is, since only colors and markers change.
func (ui *UI) StepShimmer(now time.Time) {
	if !ui.shimmer.On.Load() {
		return
	}
	t := now.Sub(ui.shimmer.Start) % (shimmerSweep + shimmerPause)
	phase := min(float64(t)/float64(shimmerSweep), 1)
	off := BlinkOff(ui.shimmer.Start, now)
	moved, blinked := phase != ui.shimmer.Phase, off != ui.shimmer.MarkerOff
	if !moved && !blinked {
		return
	}
	ui.shimmer.Phase, ui.shimmer.MarkerOff = phase, off
	if moved {
		ui.indicatorLabel.RequestRender()
	}
	if blinked {
		ui.indicatorMarker.RequestRender()
		ui.RepaintTextMarkers()
	}
	ui.shimmer.SyncRows()
}

// DrawShimmer lightens the indicator's text where the shimmer's band is,
// over the color the text was drawn in, while the shimmer runs. The band
// sweeps across the run's time, the line's first chunk, and leaves the hint
// after it alone. It runs after the label draws itself.
func (ui *UI) DrawShimmer(buf *buffer.Buffer, _ time.Duration) {
	if !ui.shimmer.On.Load() {
		return
	}
	label := ui.indicatorLabel
	chunks := label.Content().Chunks
	if len(chunks) == 0 {
		return
	}
	x0, y := label.X(), label.Y()
	cols := min(label.Width(), textbuf.StringWidth(chunks[0].Text))
	for x := range cols {
		cell := buf.Cell(x0+x, y)
		if cell.IsContinuation() {
			continue
		}
		cell.Fg = ShimmerFg(cell.Fg, x, cols, ui.shimmer.Phase)
		buf.SetCellDirect(x0+x, y, cell)
	}
}

// ShimmerFg returns the foreground of column x of a text cols columns wide,
// drawn in base, with the shimmer's band phase of its way across, from 0 to
// 1: the band enters at the left at 0 and has left at the right at 1, and
// lightens the columns within shimmerReach of its center toward shimmerFg,
// the more the nearer.
func ShimmerFg(base style.Color, x, cols int, phase float64) style.Color {
	center := -shimmerReach + phase*float64(cols+2*shimmerReach)
	d := math.Abs(float64(x) - center)
	if d >= shimmerReach {
		return base
	}
	return base.Blend(shimmerFg, (1+math.Cos(math.Pi*d/shimmerReach))/2)
}
