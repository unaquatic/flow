package ui

import (
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
// the transcript to the parent the new mode uses.
//
// Rows are reparented rather than rebuilt, so streaming content, tool state,
// and the widget index all survive the switch. Like every UI event, this runs on
// the renderer's event loop, which is where the renderer expects mode changes.
func (ui *UI) SetInlineMode(e EventInlineModeSet) {
	if ui.inline == e.Inline {
		return
	}

	rows := ui.transcriptRows
	ui.ClearTranscriptRows()

	ui.inline = e.Inline
	ui.messageBox.SetGone(e.Inline)

	for _, row := range rows {
		row.SetGone(false)
		ui.AddTranscriptRow(row)
	}

	ui.renderer.SetInlineMode(e.Inline)
	ui.renderer.RequestRender()
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
