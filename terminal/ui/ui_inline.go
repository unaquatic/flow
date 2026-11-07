package ui

import (
	"maps"
	"slices"

	"github.com/unaquatic/fluid/input"
	"github.com/unaquatic/fluid/widget"
)

// AddTranscriptRow appends a bubble or notice to the transcript.
//
// Fullscreen mode puts rows in a scroll container. Inline mode puts them
// directly under the root, because a container sized to its children reports
// its whole area as damaged whenever a row is added or grows, which repaints
// the entire transcript on every frame. The root's height is fixed, so it
// reports nothing and the damage stays at the rows that changed.
func (ui *UI) AddTranscriptRow(row *widget.Box) {
	ui.transcriptRows = append(ui.transcriptRows, row)
	if ui.inline {
		ui.renderer.Root().InsertBefore(row, ui.indicatorBox)
		return
	}
	ui.messageBox.Add(row)
}

// InsertTranscriptRow adds row to the transcript right under above, as a
// segment added to a reply takes a row under the reply's last, or at the
// end, as AddTranscriptRow does, when above is not a transcript row.
func (ui *UI) InsertTranscriptRow(row, above *widget.Box) {
	i := slices.Index(ui.transcriptRows, above)
	if i < 0 {
		ui.AddTranscriptRow(row)
		return
	}
	ui.transcriptRows = slices.Insert(ui.transcriptRows, i+1, row)
	above.Parent().InsertAfter(row, above)
}

// ClearTranscriptRows detaches every transcript row from the active parent.
func (ui *UI) ClearTranscriptRows() {
	for _, row := range ui.transcriptRows {
		if parent := row.Parent(); parent != nil {
			parent.Remove(row.ID())
		}
	}
	ui.transcriptRows = nil
}

// SetInlineMode switches between inline and alternate-screen rendering, moving
// the transcript to the parent the new mode uses. Inline, the bubbles hold
// back what of their parts may still change, as ChatMessageBox.SyncHeld says.
//
// Rows are reparented rather than rebuilt, so streaming content, tool state,
// and the widget index all survive the switch. Rows committed to the terminal
// or trimmed left the transcript, though, so while any did, the switch to the
// alternate screen builds the transcript again, as RebuildTranscript does.
// The switch to inline keeps the last TailRows rows, as TrimTranscript keeps
// them, as BuildTranscript would build it. Like every UI event, this runs on
// the renderer's event loop, which is where the renderer expects mode
// changes.
func (ui *UI) SetInlineMode(e EventInlineModeSet) {
	if ui.inline == e.Inline {
		return
	}

	rows := ui.transcriptRows
	ui.ClearTranscriptRows()

	ui.inline = e.Inline
	ui.messageBox.SetGone(e.Inline)

	if ui.committed || ui.trimmed {
		ui.RebuildTranscript()
	} else {
		for _, row := range rows {
			row.SetGone(false)
			ui.AddTranscriptRow(row)
		}
		for cm := range maps.Values(ui.messageWidgets) {
			cm.SetHolds(e.Inline)
		}
		if e.Inline {
			ui.TrimTranscript(TailRows)
		}
	}

	ui.renderer.SetInlineMode(e.Inline)
	ui.renderer.RequestRender()
}

// LiveTop returns the first row of the live area of inline output, as last
// laid out, which terminal.Renderer's SetInlineLiveTop takes: the first row
// of the transcript that may still change, as ChatMessageBox.LiveRow gives
// it, or else the top of the input box, with the indicator above it, which
// change as a run goes on and as the user types. The renderer scrolls the
// rows the transcript gains above the live area in without rewriting it, and
// FitInputBox keeps the live area on screen.
func (ui *UI) LiveTop() int {
	top := ui.InputBoxTop()
	for cm := range maps.Values(ui.messageWidgets) {
		if row, ok := cm.LiveRow(); ok {
			top = min(top, row)
		}
	}
	return top
}

// InputBoxTop returns the first row, as last laid out, of the input box,
// with the indicator above it when it is shown, as InputBoxHeight counts
// them. Once the input box is gone, as an inline quit leaves it, it returns
// the row where the box would start: under the transcript's last row and
// the blank row that ends it.
func (ui *UI) InputBoxTop() int {
	if ui.indicatorBox.Visible() {
		n := ui.indicatorBox.LayoutNode()
		top, _ := n.MarginTop.Resolve(0)
		return n.Computed.Y - top
	}
	if ui.inputWrapper.Visible() {
		return ui.inputWrapper.LayoutNode().Computed.Y
	}
	for _, row := range slices.Backward(ui.transcriptRows) {
		if row.Parent() == nil || !row.Visible() {
			continue
		}
		n := row.LayoutNode()
		bottom, _ := n.MarginBottom.Resolve(0)
		return n.Computed.Y + n.Computed.Height + bottom
	}
	return 0
}

// LiveRows returns the rows of the transcript in the live area, as last
// laid out: those from LiveTop down to the input box.
func (ui *UI) LiveRows() int {
	return ui.InputBoxTop() - ui.LiveTop()
}

// ScrollTranscript scrolls the transcript viewport and reports whether it
// handled the key. Inline mode leaves scrolling to the terminal, because rows
// above the viewport are ordinary scrollback.
func (ui *UI) ScrollTranscript(k *input.KeyEvent) bool {
	if ui.inline {
		return false
	}
	ui.messageBox.HandleKeyPress(k)
	return true
}
