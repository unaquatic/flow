package ui

import (
	"iter"
	"slices"
	"time"

	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/render"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/widget"
)

// InputBg returns the fill of the input box and the panels that open in it:
// BoxBg, or none in InputBar, where they sit on the canvas as a tool call's
// row does.
func (ui *UI) InputBg() style.Color {
	if ui.service.state.InputStyle == InputBar {
		return style.ColorTransparent
	}
	return ui.BoxBg()
}

// SetInputStyle shows the input box and the panels that open in it as s
// says: every fill of BoxBg they were built with, those of the panels not
// open now included, takes InputBg, the box's blank top and bottom rows show
// only while it is filled, the queued prompts are drawn again, and the markers MarkEdge
// draws are repainted. The fills are found once, as
// FillsOf finds them, so that a widget filled with none, such as the footer
// under the box, stays so.
func (ui *UI) SetInputStyle(s InputStyle) {
	if ui.inputFills == nil {
		for _, r := range []render.Renderable{ui.inputWrapper, ui.slashMenuRow, ui.sessionPanel, ui.bannerPanel, ui.treePanel} {
			ui.inputFills = slices.AppendSeq(ui.inputFills, FillsOf(r, ui.BoxBg()))
		}
	}
	bg := ui.BoxBg()
	if s == InputBar {
		bg = style.ColorTransparent
	}
	for _, fill := range ui.inputFills {
		fill(bg)
	}
	// On the canvas, the blank rows the box starts and ends with would
	// double the one above it, as SyncInputGap keeps it, and the footer's
	// row under it.
	ui.inputTopBorder.SetGone(s == InputBar)
	ui.inputBottomBorder.SetGone(s == InputBar)
	ui.SyncInputGap()
	// The queued prompts' boxes are made anew, in InputBg.
	ui.DrawQueue()
	ui.RepaintEdgeMarks()
}

// SyncInputGap keeps a single blank row between the input box, with the
// Usage box and the queued prompts above it, and what is above it, while
// the input shows in InputBar: none of its own when that ends with a blank
// row, as a message and the indicator do, and one when it does not, as at
// the top of the screen with nothing above it. The filled box starts with a
// blank row of its own, so it takes none. It runs before each frame is laid
// out, as what is above the box changes.
func (ui *UI) SyncInputGap() {
	if ui.indicatorBox == nil {
		return // The UI is still being built; the first frame syncs it.
	}
	gap := 0
	if ui.service.state.InputStyle == InputBar && !ui.AboveInputEndsBlank() {
		gap = 1
	}
	SetEdge(ui.inputWrapper, &ui.inputWrapper.LayoutNode().MarginTop, gap)
}

// AboveInputEndsBlank reports whether what shows right above the input box
// ends with a blank row: the indicator, or else the last row of the
// transcript or of the session preview, which a blank row ends as a margin.
// Nothing above the box, as an empty transcript, needs no blank row either.
// A fullscreen transcript, or preview, ends with its last row only while it
// is scrolled to its bottom; scrolled anywhere else, as when it keeps its
// place while its last row grows, the row it ends with is any, so it counts
// as one that is not blank.
func (ui *UI) AboveInputEndsBlank() bool {
	if ui.indicatorBox.Visible() {
		return true
	}
	var rows []render.Renderable
	scroll := ui.messageBox
	switch {
	case ui.previewMessageBox.Visible():
		scroll = ui.previewMessageBox
		rows = scroll.Children()
	case ui.inline:
		scroll = nil
		for _, row := range ui.transcriptRows {
			rows = append(rows, row)
		}
	default:
		rows = scroll.Children()
	}
	if scroll != nil && scroll.ContentHeight() > scroll.ViewportHeight() && !scroll.IsAtBottom() {
		return false
	}
	for _, row := range slices.Backward(rows) {
		if row.Parent() == nil {
			continue
		}
		if blank, shown := EndsBlank(row); shown {
			return blank
		}
	}
	return true
}

// EndsBlank reports whether r ends with a blank row, as a margin or a
// padding under it, or under the last widget it shows, or as a box of its
// own that holds nothing, and whether r shows anything at all: a widget
// that is gone, or a box whose widgets show nothing, as a prompt's tail row
// without a part, shows nothing.
func EndsBlank(r render.Renderable) (blank, shown bool) {
	if !r.Visible() {
		return false, false
	}
	n := r.LayoutNode()
	margin, _ := n.MarginBottom.Resolve(0)
	padding, _ := n.PaddingBottom.Resolve(0)
	if margin > 0 || padding > 0 {
		return true, true
	}
	children := r.Children()
	for _, child := range slices.Backward(children) {
		if child.LayoutNode().Position == layout.PositionAbsolute {
			continue
		}
		if blank, shown := EndsBlank(child); shown {
			return blank, true
		}
	}
	if len(children) > 0 {
		return false, false
	}
	// A box that holds nothing is blank rows, if it takes any; another
	// widget, such as a text, shows what it holds.
	if _, box := r.(*widget.Box); box {
		return true, n.Computed.Height > 0
	}
	return false, true
}

// FillsOf yields a setter for each background of r, and of every widget under
// it, that is c: the background of a box or a text, of a line or a cell, and
// the backgrounds of a menu, its selected row's included.
func FillsOf(r render.Renderable, c style.Color) iter.Seq[func(style.Color)] {
	return func(yield func(func(style.Color)) bool) {
		walkFills(r, c, yield)
	}
}

// walkFills yields the setters FillsOf yields for r, and reports whether
// yield asked for more.
func walkFills(r render.Renderable, c style.Color, yield func(func(style.Color)) bool) bool {
	type fill struct {
		get func() style.Color
		set func(style.Color)
	}
	var fills []fill
	switch w := r.(type) {
	case *widget.Box:
		fills = []fill{{w.Bg, w.SetBg}}
	case *widget.Text:
		fills = []fill{{w.Bg, w.SetBg}}
	case *widget.CharFill:
		fills = []fill{{w.Bg, w.SetBg}}
	case *widget.Select:
		fills = []fill{{w.Bg, w.SetBg}, {w.FocusedBg, w.SetFocusedBg}, {w.SelectedBg, w.SetSelectedBg}}
	}
	for _, f := range fills {
		if f.get() == c && !yield(f.set) {
			return false
		}
	}
	if p, ok := r.(interface{ Children() []render.Renderable }); ok {
		for _, child := range p.Children() {
			if !walkFills(child, c, yield) {
				return false
			}
		}
	}
	return true
}

// MarkEdge has edge, the left border cell of a row of the input box, draw
// the marker shape /settings sets, in the color fg gives, on its first row
// while the input shows in InputBar, as EdgeMarker draws it: the cell before
// the input's first row, or before a panel's title, where a tool call's row
// starts with its marker. RepaintEdgeMarks repaints it.
func (ui *UI) MarkEdge(edge *widget.CharFill, fg func() style.Color) {
	ui.edgeMarks = append(ui.edgeMarks, edge)
	edge.RenderAfterHook = ui.EdgeMarker(edge, fg)
}

// EdgeMarker returns the RenderAfterHook of edge that draws the marker MarkEdge
// describes, for a cell that is drawn anew whenever the input style or the
// marker shape changes, as a queued prompt's is, and so needs no repaint.
func (ui *UI) EdgeMarker(edge *widget.CharFill, fg func() style.Color) func(*buffer.Buffer, time.Duration) {
	return func(buf *buffer.Buffer, _ time.Duration) {
		if ui.service.state.InputStyle != InputBar || edge.Height() == 0 {
			return
		}
		shape, c := ui.service.state.MarkerShape, fg()
		x, y := edge.X(), edge.Y()
		bg := buf.Cell(x, y).Bg
		if shape.Filled() {
			bg = c
		}
		buf.SetCell(x, y, []rune(shape.Glyph())[0], c, bg, 0)
	}
}

// RepaintEdgeMarks repaints the cells MarkEdge marks, as the input style or
// the marker shape changes.
func (ui *UI) RepaintEdgeMarks() {
	for _, edge := range ui.edgeMarks {
		edge.RequestRender()
	}
}

// LeftEdge returns the left border cell of row, a row BuildContentRow
// builds, or nil when row has none.
func LeftEdge(row *widget.Box) *widget.CharFill {
	children := row.Children()
	if len(children) == 0 {
		return nil
	}
	edge, _ := children[0].(*widget.CharFill)
	return edge
}

// MarkInputEdges marks the left edge of the input's first row, in the
// primary color, as a prompt's bar is, and of the title of every panel that
// opens in the input box, in titleFg, the color of the title, as MarkEdge
// marks them.
func (ui *UI) MarkInputEdges() {
	if edge := LeftEdge(ui.inputContentRow); edge != nil {
		ui.MarkEdge(edge, func() style.Color { return productFg })
	}
	for _, h := range []*PanelHeader{ui.slashHeader, ui.sessionHeader, ui.bannerHeader, ui.treeHeader} {
		if edge := LeftEdge(h.TitleRow); edge != nil {
			ui.MarkEdge(edge, func() style.Color { return titleFg })
		}
	}
}
