package ui

import (
	"strings"

	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/widget"
)

// The hints after a queued prompt: QueuedHint after each, and SendNowHint
// after the first, which Alt+Enter on an empty input sends at once.
const (
	QueuedHint  = "[queued]"
	SendNowHint = "[alt+enter send now]"
)

// queueItemRows is the rows each queued prompt takes above the input box: its
// box's edge rows, its line between them, and the gap under the box.
const queueItemRows = 4

// ShowQueue shows each of prompts in a box of its own above the input box,
// the first sent next at the top, as DrawQueue draws them. It removes the
// boxes when prompts is empty.
func (ui *UI) ShowQueue(prompts []string) {
	ui.queuePrompts = prompts
	if len(prompts) <= ui.queueLimit {
		ui.queueLimit = 0
	}
	ui.DrawQueue()
}

// QueueHints returns the hints after the queued prompt at index i, as
// QueuedHint and SendNowHint give them.
func QueueHints(i int) string {
	if i == 0 {
		return QueuedHint + " " + SendNowHint
	}
	return QueuedHint
}

// DrawQueue replaces the boxes of the queued prompts: within the bound a
// short terminal sets, as QueuePart bounds it, the prompts sent first, each
// in a box like the input box, followed by a row of the canvas. A box shows
// the first line of its prompt, in the input's text color and cut to fit,
// then its hints, as QueueHints gives them, in PanelDimFg.
func (ui *UI) DrawQueue() {
	for _, child := range ui.queueBox.Children() {
		ui.queueBox.Remove(child.ID())
	}
	shown := ui.queuePrompts
	if ui.queueLimit > 0 {
		shown = shown[:min(len(shown), ui.queueLimit)]
	}
	for i, prompt := range shown {
		first, _, _ := strings.Cut(prompt, "\n")
		text := widget.NewText(first,
			widget.TextWithFg(textFg),
			widget.TextWithTruncate(true),
			widget.TextWithLayout(layout.WithFlexShrink(1)),
		)
		hints := widget.NewText(QueueHints(i),
			widget.TextWithFg(PanelDimFg),
			widget.TextWithLayout(layout.WithFlexShrink(0)),
		)
		// The prompt is cut short, so that its hints stay whole.
		line := widget.NewBox(
			widget.BoxWithLayout(
				layout.WithFlexDirection(layout.DirectionRow),
				layout.WithColumnGap(1),
			),
			widget.Children(text, hints),
		)
		box, _ := StreamableMessageBox(ui.BorderFg(), ui.BoxBg(), line)
		gap := widget.NewBox(
			widget.BoxWithLayout(layout.WithHeight(layout.Points(1)), layout.WithFlexShrink(0)),
		)
		ui.queueBox.Add(widget.NewBox(
			widget.BoxWithLayout(
				layout.WithFlexShrink(0),
				layout.WithAlignItems(layout.AlignStretch),
			),
			widget.Children(box, gap),
		))
	}
	ui.queueBox.SetGone(len(shown) == 0)
}

// QueuePart returns the boxes of the queued prompts as a part of the input
// box that gives up whole boxes, the last ones first, and keeps the first,
// sent next, and false while nothing is queued.
func (ui *UI) QueuePart() (FitPart, bool) {
	n := len(ui.queuePrompts)
	if n == 0 {
		return FitPart{}, false
	}
	shown := n
	if ui.queueLimit > 0 {
		shown = min(n, ui.queueLimit)
	}
	return FitPart{
		Rows: shown * queueItemRows,
		Want: n * queueItemRows,
		Min:  queueItemRows,
		Step: queueItemRows,
		Bound: func(rows int) {
			limit := rows / queueItemRows
			if limit >= n {
				limit = 0
			}
			ui.queueLimit = limit
			ui.DrawQueue()
		},
	}, true
}
