package ui

import (
	"slices"

	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/render"
	"github.com/unaquatic/fluid/widget"
)

// TailRows is the fewest rows the inline transcript holds once built from a
// session's messages, as BuildTranscript builds it: the last rows of its
// messages, as few as add up to TailRows, or every row when they add up to
// fewer. The terminal parses every row it is given into its scrollback,
// which takes it seconds for a long session, so the scrollback then holds no
// row above them.
const TailRows = 1000

// tailBatch is the number of last messages whose rows BuildTranscript builds
// first, before it knows how many rows a message takes.
const tailBatch = 64

// CommitScrolledRows commits the rows of the inline transcript that scrolled
// into the terminal's scrollback, as terminal.Renderer's CommitInline picks
// them out of the InlineRows, and drops what of their widgets nothing else
// holds, as DropCommittedRows does. What each frame lays out, draws and holds
// is then the rows on screen, however long the transcript grows.
//
// It runs before each frame is laid out, while the rows are where the last
// frame put them.
func (ui *UI) CommitScrolledRows() {
	if rows := ui.InlineRows(); len(rows) > 0 {
		ui.DropCommittedRows(ui.renderer.CommitInline(rows...))
	}
}

// InlineRows returns the rows of the inline transcript, in order, or none
// outside inline mode.
func (ui *UI) InlineRows() []render.Renderable {
	if !ui.inline {
		return nil
	}
	rows := make([]render.Renderable, len(ui.transcriptRows))
	for i, row := range ui.transcriptRows {
		rows[i] = row
	}
	return rows
}

// DropCommittedRows drops the first n rows of the transcript, which the
// terminal now holds, as DropLeadingRows does.
func (ui *UI) DropCommittedRows(n int) {
	if n == 0 {
		return
	}
	ui.DropLeadingRows(n)
	ui.committed = true
}

// DropLeadingRows drops the first n rows of the transcript, taking them out of
// their parent if the terminal did not, and the bubbles none of whose rows
// stays: they no longer show their messages, and their tool calls no longer
// shimmer. A bubble with a row that stays keeps its widgets, which space the
// rows that stay.
func (ui *UI) DropLeadingRows(n int) {
	for _, row := range ui.transcriptRows[:n] {
		if parent := row.Parent(); parent != nil {
			parent.Remove(row.ID())
		}
	}
	ui.transcriptRows = slices.Delete(ui.transcriptRows, 0, n)

	// A bubble takes the map entry of each of its messages.
	kept := make(map[*ChatMessageBox]bool)
	for id, cm := range ui.messageWidgets {
		shown, seen := kept[cm]
		if !seen {
			shown = cm.Shown()
			kept[cm] = shown
			if !shown {
				for box := range cm.Boxes() {
					ui.shimmer.Track(box, false)
				}
			}
		}
		if !shown {
			delete(ui.messageWidgets, id)
		}
	}
}

// RebuildTranscript builds the transcript again from the messages of the
// active session, and the notices among them, as BuildTranscript builds it,
// with the rows as they show now: the tool calls and the views as Ctrl+O and
// /settings now show them.
func (ui *UI) RebuildTranscript() {
	ui.DropTranscript()
	ui.BuildTranscript(ui.service.state.Messages)
}

// BuildTranscript adds the rows of msgs, messages and the notices among them,
// to the empty transcript: in fullscreen mode those of every message, and
// inline the last TailRows of them, as TrimTranscript keeps them.
//
// Inline, it builds the rows of the last messages alone: first of tailBatch
// of them, then of as many as those suggest add up to TailRows, and a quarter
// more, until they do. It so builds about as many rows as it keeps, however
// long the session, and starts with a message that starts a bubble, as
// BubbleStart picks it.
func (ui *UI) BuildTranscript(msgs []ChatMessage) {
	n := len(msgs)
	if ui.inline {
		n = min(n, tailBatch)
	}
	for {
		start := BubbleStart(msgs, len(msgs)-n)
		for _, msg := range msgs[start:] {
			ui.AddMessage(msg)
		}
		if !ui.inline {
			return
		}
		height := ui.TranscriptHeight()
		if start == 0 || height >= TailRows {
			ui.TrimTranscript(TailRows)
			return
		}
		ui.DropTranscript()
		built := len(msgs) - start
		n = max(built+tailBatch, built*TailRows/max(height, 1)*5/4)
	}
}

// BubbleStart returns where in msgs a transcript of their last messages
// from i on starts: at i, unless the first message from i on, past
// notices, joins the bubble of a message before it, as SharesBubble says,
// at that message, which starts the bubble. A bubble so shows each of its
// messages built from i on, not some in a bubble of their own.
func BubbleStart(msgs []ChatMessage, i int) int {
	i = max(i, 0)
	for _, m := range msgs[i:] {
		if m.Notice != nil {
			continue
		}
		if !m.SharesBubble() {
			return i
		}
		for k := i - 1; k >= 0; k-- {
			if msgs[k].ID == m.ParentID {
				return k
			}
		}
		return i
	}
	return i
}

// TranscriptHeight lays out the inline transcript, in the root as it is now
// sized, and returns the rows it takes: from the top margin of its first row
// that takes part in layout to the bottom margin of its last.
func (ui *UI) TranscriptHeight() int {
	ui.renderer.Root().CalculateLayout()
	top, bottom := -1, 0
	for _, row := range ui.transcriptRows {
		n := row.LayoutNode()
		if n.Visibility == layout.Gone {
			continue
		}
		if top < 0 {
			margin, _ := n.MarginTop.Resolve(0)
			top = n.Computed.Y - margin
		}
		bottom = RowBottom(row)
	}
	if top < 0 {
		return 0
	}
	return bottom - top
}

// RowBottom returns the first terminal row under row, a row of the inline
// transcript, and its bottom margin, as last laid out.
func RowBottom(row *widget.Box) int {
	n := row.LayoutNode()
	margin, _ := n.MarginBottom.Resolve(0)
	return n.Computed.Y + n.Computed.Height + margin
}

// TrimTranscript drops the first rows of the inline transcript, as
// DropLeadingRows drops them, as many as leave rows rows at least under
// them, as the root lays them out now. It records that the transcript then
// lacks them, though the terminal never showed them.
func (ui *UI) TrimTranscript(rows int) {
	ui.renderer.Root().CalculateLayout()
	last := 0
	for _, row := range ui.transcriptRows {
		if row.LayoutNode().Visibility != layout.Gone {
			last = RowBottom(row)
		}
	}
	drop := 0
	for i, row := range ui.transcriptRows {
		if row.LayoutNode().Visibility == layout.Gone {
			continue
		}
		if last-RowBottom(row) < rows {
			break
		}
		drop = i + 1
	}
	if drop == 0 {
		return
	}
	ui.DropLeadingRows(drop)
	ui.trimmed = true
}

// ReprintTranscript puts back the rows of the transcript committed to the
// terminal, as terminal.Renderer's SetInlineReprint takes it, by building
// the transcript again, as RebuildTranscript does, while any are.
func (ui *UI) ReprintTranscript() {
	if ui.committed {
		ui.RebuildTranscript()
	}
}

// ReprintCommitted has the next frame write the whole inline transcript
// again, its committed rows too, as terminal.Renderer's ReprintInline does,
// while any are: a change to how rows show, as Ctrl+O and /settings make,
// reaches only the rows the transcript still holds.
func (ui *UI) ReprintCommitted() {
	if ui.inline && ui.committed {
		ui.renderer.ReprintInline()
	}
}
