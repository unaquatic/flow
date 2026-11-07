package ui

import (
	"time"

	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/widget"
)

// BlinkInterval is how long a blinking marker shows, and then how long it is
// hidden, while a run is in progress: the marker before the indicator's
// time, that of a tool call that executes, and that of the text a reply is
// writing.
const BlinkInterval = 500 * time.Millisecond

// BlinkOff reports whether a marker that started blinking at start is hidden
// at now: it shows for the first BlinkInterval, is hidden for the next, and
// so on.
func BlinkOff(start, now time.Time) bool {
	return now.Sub(start)/BlinkInterval%2 == 1
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
