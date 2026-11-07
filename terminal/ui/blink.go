package ui

import (
	"sync/atomic"
	"time"

	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/widget"
)

// BlinkInterval is how long a blinking marker shows, and then how long it is
// hidden, while a run is in progress: the marker before the indicator's
// time, that of a tool call that executes, and that of the text a reply is
// writing.
const BlinkInterval = 500 * time.Millisecond

// BlinkTick is how often the blink checks whether its markers turn on or
// off, so that each turns within a tick of when BlinkOff says.
const BlinkTick = 50 * time.Millisecond

// BlinkOff reports whether a marker that started blinking at start is hidden
// at now: it shows for the first BlinkInterval, is hidden for the next, and
// so on.
func BlinkOff(start, now time.Time) bool {
	return now.Sub(start)/BlinkInterval%2 == 1
}

// Blink is the clock of the markers that blink while a run is in progress,
// all in step: the marker before the indicator's time, those of the rows
// whose content is busy, such as a tool call the run executes, as
// PartBox.Blinks tells, and those of the text a reply is writing.
type Blink struct {
	// On reports that the markers blink: while the indicator shows the time
	// of a run in progress. AnimateBlink reads it off the event loop.
	On atomic.Bool
	// Start is when the markers started blinking.
	Start time.Time
	// Off reports that the blinking markers are hidden now, as BlinkOff
	// tells from Start.
	Off bool
	// Rows are the rows whose marker may blink, or was last drawn hidden,
	// as PartBox.SyncBlink tracks them. SyncRows repaints them as the blink
	// turns, starts, or stops: a frame reaches only the parts of the scene
	// that changed, which a blinking marker is not.
	Rows map[*PartBox]struct{}
}

// Track records row among Rows while tracked is true, and forgets it
// otherwise.
func (b *Blink) Track(row *PartBox, tracked bool) {
	if !tracked {
		delete(b.Rows, row)
		return
	}
	if b.Rows == nil {
		b.Rows = make(map[*PartBox]struct{})
	}
	b.Rows[row] = struct{}{}
}

// SyncRows repaints the marker of each of Rows that the blink now hides
// otherwise than when the row was drawn, as PartBox.SyncBlink does.
func (b *Blink) SyncRows() {
	for row := range b.Rows {
		row.SyncBlink()
	}
}

// BlinkHooks returns the RenderBeforeHook and RenderAfterHook of m, a marker
// NewMarker made, that hide it while hidden reports true: before m draws,
// before records the background under it, and after m draws, after puts that
// background back in its place. Only the cells m covers change, so the
// layout stays as it is.
func BlinkHooks(m *widget.Text, hidden func() bool) (before, after func(*buffer.Buffer, time.Duration)) {
	var under []buffer.Cell
	before = func(buf *buffer.Buffer, _ time.Duration) {
		under = under[:0]
		for x := range m.Width() {
			under = append(under, buf.Cell(m.X()+x, m.Y()))
		}
	}
	after = func(buf *buffer.Buffer, _ time.Duration) {
		if !hidden() {
			return
		}
		// FillRect writes a transparent background as it is, where
		// SetCellDirect would keep the marker's own, as a filled marker has.
		for x, cell := range under {
			buf.FillRect(m.X()+x, m.Y(), 1, 1, cell.Bg)
		}
	}
	return before, after
}
