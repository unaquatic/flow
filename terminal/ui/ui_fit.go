package ui

import (
	"slices"

	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/widget"
)

// fitPasses bounds the layout passes one fit takes. A pass changes one part,
// and a list that stops fitting may also gain a count line under it, so a fit
// settles in two or three.
const fitPasses = 8

// BoundRows returns n, or bound when it is smaller. A bound of zero or less
// leaves n.
func BoundRows(n, bound int) int {
	if bound <= 0 {
		return n
	}
	return min(n, bound)
}

// MenuRows returns n, within the bound the input box sets on its lists.
func (ui *UI) MenuRows(n int) int {
	return BoundRows(n, ui.service.state.MenuRows)
}

// FitPart is a part of the input box that shows fewer rows when the box would
// not otherwise fit the terminal, such as a menu or the input text.
type FitPart struct {
	// Rows is the number of rows the part shows.
	Rows int
	// Want is the number of rows its content takes.
	Want int
	// Min is the fewest rows it shows.
	Min int
	// Least is the fewest rows it shows when it shows any, for a part whose
	// Min is zero: a bound that leaves it fewer hides it instead. Zero lets
	// it show any number.
	Least int
	// Step, when above one, is the rows the part gives up and takes back at
	// a time, as the queue gives up a whole box: Give rounds up to it, and
	// Take down.
	Step int
	// Bound makes the part show at most rows. A bound of Want or more is
	// lifted.
	Bound func(rows int)
}

// Give returns the rows p gives up toward need rows: as many as it has above
// Min, and all of those when what is left would be fewer than Least.
func (p FitPart) Give(need int) int {
	give := min(need, p.Rows-p.Min)
	if p.Step > 1 && give%p.Step != 0 {
		give = min(give+p.Step-give%p.Step, p.Rows-p.Min)
	}
	if left := p.Rows - give; left > 0 && left < p.Least {
		give = p.Rows - p.Min
	}
	return give
}

// Take returns the rows p takes back of spare rows: as many as its content
// wants, and none when it would still show fewer than Least.
func (p FitPart) Take(spare int) int {
	take := min(spare, p.Want-p.Rows)
	if p.Step > 1 {
		take -= take % p.Step
	}
	if p.Rows+take < p.Least {
		return 0
	}
	return take
}

// KeepInputBoxInView fits the input box to the terminal when its layout or
// the terminal's height has changed since the last fit. It runs before every
// frame is laid out.
func (ui *UI) KeepInputBoxInView() {
	height := ui.renderer.Height()
	if height == ui.fitHeight && !ui.renderer.Root().LayoutNode().IsDirty() {
		return
	}
	ui.fitHeight = height
	ui.FitInputBox(height)
}

// FitInputBox keeps the input box, with the indicator above it, within the
// rows of height, the terminal's height, that the live rows of the
// transcript, as ReserveLiveRows keeps them, and a spare row leave it. It
// bounds the open list first, then the banner text, the queue, and the
// input text, and gives rows back in the reverse order as room returns.
//
// Inline mode needs the live area on screen. The terminal scrolls the rows
// above its screen into the scrollback, where they cannot be redrawn, so a
// box taller than the screen, or one that pushed a running call or the text
// a reply is writing above it, could not be redrawn in place as they change.
// The spare row is where the renderer scrolls the rows the transcript gains
// in, above the live area, without redrawing it.
func (ui *UI) FitInputBox(height int) {
	root := ui.renderer.Root()
	for range fitPasses {
		root.CalculateLayout()
		budget := max(height-1-ui.ReserveLiveRows(), 1)
		if !ui.Refit(budget - ui.InputBoxHeight()) {
			return
		}
	}
}

// ReserveLiveRows returns the rows FitInputBox keeps for the live rows of
// the transcript: those LiveRows counts, and while a run goes on, the most
// it has counted since the run began. Each step of a run brings live rows
// and ends them, as a reply's text and a call that runs, and
// keeping their rows for the whole run keeps the input box from growing and
// shrinking, and its text from moving, at every step.
func (ui *UI) ReserveLiveRows() int {
	rows := ui.LiveRows()
	if ui.service.state.Running {
		rows = max(rows, ui.liveReserve)
	}
	ui.liveReserve = rows
	return rows
}

// InputBoxHeight returns the rows the input box and its footer take, with the
// indicator and its margins above them when it is shown, as last laid out.
func (ui *UI) InputBoxHeight() int {
	h := 0
	if ui.inputWrapper.Visible() {
		h += ui.inputWrapper.LayoutNode().Computed.Height
	}
	if ui.indicatorBox.Visible() {
		n := ui.indicatorBox.LayoutNode()
		top, _ := n.MarginTop.Resolve(0)
		bottom, _ := n.MarginBottom.Resolve(0)
		h += top + n.Computed.Height + bottom
	}
	return h
}

// Refit bounds parts of the input box to take spare rows away from it, or to
// give them back when spare is positive, one part at a time, and reports
// whether it changed a part. It first lifts a bound that holds a part taller
// than its content.
func (ui *UI) Refit(spare int) bool {
	parts := ui.FitParts()
	for _, p := range parts {
		if p.Rows > p.Want {
			p.Bound(p.Want)
			return true
		}
	}
	if spare < 0 {
		for _, p := range parts {
			if give := p.Give(-spare); give > 0 {
				p.Bound(p.Rows - give)
				return true
			}
		}
		return false
	}
	for _, p := range slices.Backward(parts) {
		if take := p.Take(spare); take > 0 {
			p.Bound(p.Rows + take)
			return true
		}
	}
	return false
}

// FitParts returns the parts of the input box on screen that can show fewer
// rows, in the order they give rows up. The body of an error in the
// indicator above the box goes first, down to a line, so that the input
// stays in view. The prompts previewed under the session list go next,
// before the rows they preview, and fill the rows the list leaves, up to a
// row for each prompt. The status box above the input box goes before any of
// the slash menu's commands does.
func (ui *UI) FitParts() []FitPart {
	var parts []FitPart
	if ui.indicatorDetail != nil && ui.indicatorBox.Visible() && ui.indicatorDetail.Visible() {
		parts = append(parts, NodePart(ui.indicatorDetailRow.LayoutNode(), ui.indicatorDetailText.LayoutNode(), 1))
	}
	if prompts, ok := ui.sessionPrompts.Part(); ok && ui.sessionPanel.Visible() {
		parts = append(parts, prompts)
	}
	if status, ok := ui.StatusPart(); ok {
		parts = append(parts, status)
	}
	if list, ok := ui.ListPart(); ok {
		parts = append(parts, list)
	}
	if ui.bannerPanel.Visible() && ui.bannerTextRow.Visible() {
		parts = append(parts, NodePart(ui.bannerTextRow.LayoutNode(), ui.bannerText.LayoutNode(), 1))
	}
	if queue, ok := ui.QueuePart(); ok {
		parts = append(parts, queue)
	}
	if ui.inputContentRow.Visible() {
		parts = append(parts, NodePart(ui.inputContentRow.LayoutNode(), ui.input.LayoutNode(), 1))
	}
	return parts
}

// StatusRows returns the rows the Usage box takes above the input box: its
// row and the gap under it.
func (ui *UI) StatusRows() int { return 2 }

// StatusPart returns the Usage box above the input box as a part that shows
// whole or not at all, and false while the box does not belong there, as
// StatusBoxWanted reports.
func (ui *UI) StatusPart() (FitPart, bool) {
	if !ui.StatusBoxWanted() {
		return FitPart{}, false
	}
	want := ui.StatusRows()
	rows := 0
	if !ui.statusBounded {
		rows = want
	}
	return FitPart{
		Rows:  rows,
		Want:  want,
		Least: want,
		Bound: func(rows int) {
			ui.statusBounded = rows < want
			ui.SyncStatusBox()
		},
	}, true
}

// ListPart returns the list open in the input box, which shows up to its own
// maximum of rows within the box's bound, and false when none is open.
func (ui *UI) ListPart() (FitPart, bool) {
	var want int
	switch {
	case ui.treePanel.Visible():
		if t := ui.service.state.Tree; t != nil {
			want = min(TreeLineCount(len(t.Rows)), treeMaxRows)
		}
	case ui.sessionPanel.Visible():
		want = min(ui.sessionMenu.ItemCount(), sessionMaxRows)
	case ui.bannerPanel.Visible() && ui.bannerMenu.ItemCount() > 0:
		want = min(ui.bannerMenu.ItemCount(), bannerMaxRows)
	case ui.slashMenuRow.Parent() != nil:
		want = min(ui.slashMenu.ItemCount(), slashMaxRows)
	default:
		return FitPart{}, false
	}
	return FitPart{
		Rows: ui.MenuRows(want),
		Want: want,
		Min:  min(want, 1),
		Bound: func(rows int) {
			if rows >= want {
				rows = 0
			}
			ui.BoundMenus(rows)
		},
	}, true
}

// BoundMenus makes every list in the input box show at most rows, or lifts
// the bound for a rows of zero, and redraws the open one.
func (ui *UI) BoundMenus(rows int) {
	ui.service.state.MenuRows = rows
	switch {
	case ui.treePanel.Visible():
		if v, ok := ui.service.TreeView(); ok {
			ui.DrawTree(v)
		}
	case ui.sessionPanel.Visible():
		FitSelect(ui.sessionMenu, ui.SessionRows())
		ui.SyncSessionCount()
	case ui.bannerPanel.Visible():
		FitSelect(ui.bannerMenu, ui.BannerRows())
		ui.SyncBannerCount()
	case ui.slashMenuRow.Parent() != nil:
		FitSelect(ui.slashMenu, ui.SlashRows())
	}
}

// FitSelect shows rows rows of sel, scrolled so that its selected row stays
// in view.
func FitSelect(sel *widget.Select, rows int) {
	sel.SetHeight(layout.Points(rows))
	sel.SetScrollOffset(max(sel.SelectedIndex()-rows+1, 0))
}

// NodePart returns a part of the input box: the content row row, which holds
// leaf, a text or the input, and shows as many of its lines as row's height
// allows, and at least fewest. Both must have been laid out.
//
// The bound is a height on the row, because a box sizes itself to its
// children's content and passes over a maximum set below it. A height also
// holds the row open when the content shrinks under it, so a bound part
// wants only the rows of its content, and Refit lifts a bound the content
// no longer fills. A part without a bound shows its content whole.
func NodePart(row, leaf *layout.Node, fewest int) FitPart {
	rows := row.Computed.Height
	want := rows
	if _, bounded := row.Height.Resolve(0); bounded && leaf.Measure != nil {
		want = leaf.Measure(leaf.Computed.Width, layout.MeasureExactly, 0, layout.MeasureUndefined).Height
	}
	if least, ok := leaf.MinHeight.Resolve(0); ok {
		fewest = max(fewest, least)
	}
	return FitPart{
		Rows: rows,
		Want: want,
		Min:  min(fewest, rows),
		Bound: func(n int) {
			row.Height = layout.Points(n)
			if n >= want {
				row.Height = layout.Auto()
			}
			// The boxes between the row and the leaf keep the leaf's last size
			// until it is laid out again.
			leaf.MarkDirty()
		},
	}
}
