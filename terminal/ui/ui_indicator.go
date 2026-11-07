package ui

import (
	"time"

	"github.com/unaquatic/fluid/style"
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

// ShowIndicator displays IndicatorText of e between the transcript and the
// input, with e's detail in a box under it, and while it shows the time of a
// run in progress, blinks the markers.
func (ui *UI) ShowIndicator(e EventIndicatorShow) {
	ui.indicatorError = e.Error
	ui.indicatorLabel.SetStyledContent(IndicatorText(e, ui.ErrorFg()))
	ui.ShowIndicatorDetail(e)
	ui.indicatorBox.SetGone(false)
	ui.ShowIndicatorMarker()
	ui.SyncBlink()
}

// ErrorFg returns the color of the marker and the "Error" an error starts
// with in the indicator: the theme's Danger.
func (ui *UI) ErrorFg() style.Color { return themeOrDefault(ui.theme).Danger }

// IndicatorErrorTitle starts the line of an error in the indicator.
const IndicatorErrorTitle = "Error"

// IndicatorText returns the line the indicator shows for e: its text, in the
// indicator's color, or, when e has a hint, its text and then a space and the
// hint, all in indicatorHintFg. An error's text follows IndicatorErrorTitle,
// in errorFg, and a space.
func IndicatorText(e EventIndicatorShow, errorFg style.Color) style.StyledText {
	if e.Error {
		return style.NewStyledText(
			style.TextChunk{Text: IndicatorErrorTitle, Fg: errorFg},
			style.TextChunk{Text: " " + e.Text, Fg: indicatorFg},
		)
	}
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

// ShowIndicatorDetail shows e's detail in the box under the indicator's
// line, JSON highlighted as code is, or hides the box when e has none. A
// hidden box keeps the detail it showed, so that ShowIndicatorWhile can
// show it again.
func (ui *UI) ShowIndicatorDetail(e EventIndicatorShow) {
	if ui.indicatorDetail == nil {
		return
	}
	ui.indicatorDetail.SetGone(e.Detail == "")
	if e.Detail != "" {
		ui.indicatorDetailText.SetStyledContent(ui.DetailText(e.Detail, e.JSON))
	}
}

// DetailText returns detail as the indicator's detail box shows it: in
// textFg, and, when isJSON reports it is JSON, highlighted as JSON code is.
// JSON the highlighter fails on shows in textFg.
func (ui *UI) DetailText(detail string, isJSON bool) style.StyledText {
	if isJSON && ui.highlighter != nil {
		lines, err := ui.highlighter.Highlight(detail, "json", textFg, style.ColorTransparent)
		if err == nil && len(lines) > 0 {
			var chunks []style.TextChunk
			for i, line := range lines {
				if i > 0 {
					chunks = append(chunks, style.Plain("\n"))
				}
				chunks = AppendSpans(chunks, line)
			}
			return style.NewStyledText(chunks...)
		}
	}
	return style.NewStyledText(style.TextChunk{Text: detail, Fg: textFg})
}

// HideIndicator dismisses the indicator.
func (ui *UI) HideIndicator() {
	ui.indicatorBox.SetGone(true)
	ui.indicatorError = false
	ui.SyncBlink()
}

// ShowIndicatorWhile shows text in the indicator and draws a frame at once,
// so that the terminal shows it, then runs fn and puts the indicator back as
// it was. The terminal keeps showing text until the next
// frame, the one that draws what fn changed, is drawn: text says what holds
// the event loop when that frame is slow, as when it lays out a long
// transcript again.
func (ui *UI) ShowIndicatorWhile(text string, fn func()) {
	content, shown, blink := ui.indicatorLabel.Content(), ui.indicatorBox.Visible(), ui.blink.On.Load()
	wasError := ui.indicatorError
	detail := ui.indicatorDetail != nil && ui.indicatorDetail.Visible()
	ui.ShowIndicator(EventIndicatorShow{Text: text})
	// The blink marks the time of a run, not text.
	ui.blink.On.Store(false)
	ui.renderer.Render()
	fn()
	ui.indicatorLabel.SetStyledContent(content)
	if ui.indicatorDetail != nil {
		ui.indicatorDetail.SetGone(!detail)
	}
	ui.indicatorError = wasError
	ui.ShowIndicatorMarker()
	ui.indicatorBox.SetGone(!shown)
	ui.blink.On.Store(blink)
}

// SyncBlink blinks the markers while the indicator shows the time of a run
// in progress, shown as the blink starts, and stops it otherwise, showing
// the markers again.
func (ui *UI) SyncBlink() {
	on := ui.indicatorBox.Visible() && ui.service.state.Working
	was := ui.blink.On.Load()
	if on && !was {
		ui.blink.Start = time.Now()
	}
	if on != was {
		ui.blink.Off = false
		ui.indicatorMarker.RequestRender()
		ui.RepaintTextMarkers()
	}
	ui.blink.On.Store(on)
	ui.blink.SyncRows()
}

// ShowIndicatorMarker shows the marker before the indicator's text while a
// turn is in progress, or the indicator shows an error, in its left padding,
// as a tool call shows its marker, in the color IndicatorMarkerFg gives it,
// and hides it otherwise. The text stays where it is either way.
func (ui *UI) ShowIndicatorMarker() {
	if ui.indicatorMarker == nil {
		return
	}
	StyleMarker(ui.indicatorMarker, ui.service.state.MarkerShape, ui.IndicatorMarkerFg())
	show := ui.service.state.Working || ui.indicatorError
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

// IndicatorMarkerFg returns the color of the marker before the indicator's
// text: ErrorFg while it shows an error, and indicatorHintFg otherwise.
func (ui *UI) IndicatorMarkerFg() style.Color {
	if ui.indicatorError {
		return ui.ErrorFg()
	}
	return indicatorHintFg
}

// IndicatorMarkerHidden reports whether the marker before the indicator's
// text is hidden: while the blink runs and turns it off.
func (ui *UI) IndicatorMarkerHidden() bool {
	return ui.blink.On.Load() && ui.blink.Off
}

// RepaintTextMarkers repaints the text that every bubble is writing, so that
// each draws its marker as the blink now shows it.
func (ui *UI) RepaintTextMarkers() {
	for cm := range ui.Bubbles() {
		cm.RepaintWritingMarkers()
	}
}

// AnimateBlink checks the blink every BlinkTick while the markers blink,
// through enqueue, which runs a function on the event loop, until done is
// closed. It leaves the event loop alone while the blink is stopped.
func (ui *UI) AnimateBlink(enqueue func(func()), done <-chan struct{}) {
	ticker := time.NewTicker(BlinkTick)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case now := <-ticker.C:
			if ui.blink.On.Load() {
				enqueue(func() { ui.StepBlink(now) })
			}
		}
	}
}

// StepBlink shows or hides the blinking markers as BlinkOff tells at now,
// while the blink runs, and repaints them when that changed: the marker
// before the indicator's time, those of the text replies are writing, and
// those of the rows whose content is busy. The repaint leaves the layout as
// it is, since only the markers change.
func (ui *UI) StepBlink(now time.Time) {
	if !ui.blink.On.Load() {
		return
	}
	off := BlinkOff(ui.blink.Start, now)
	if off == ui.blink.Off {
		return
	}
	ui.blink.Off = off
	ui.indicatorMarker.RequestRender()
	ui.RepaintTextMarkers()
	ui.blink.SyncRows()
}
