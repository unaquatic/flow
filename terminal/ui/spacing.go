package ui

import (
	"slices"

	"github.com/unaquatic/fluid/render"
	"github.com/unaquatic/fluid/widget"
)

// RowKind is how a row of a bubble sits among the rows around it. It is all
// that decides the blank rows between two rows, as Gap says; no other code
// spaces the rows of a bubble. A new kind of row only has to say which RowKind
// it is, as KindOf does.
type RowKind int

const (
	// KindBlock is a row that parts itself from the rows around it: prose, a
	// code block, a reasoning block, an expanded tool call, a step's changes,
	// and a row the PartBox of which is Apart, such as a snapshot or the
	// cache row.
	KindBlock RowKind = iota
	// KindRow is a PartBox that shows as its header alone, one row on the
	// canvas, such as a collapsed tool call. It stacks right under another
	// such row, so that they read as a list.
	KindRow
	// KindFooter is the footer of the reply that ends a turn. It is parted
	// from the row above it, whatever that row is: it never stacks under one.
	KindFooter
)

// KindOf returns the RowKind of r, a widget of a bubble: KindFooter for a
// FooterBox, KindRow for a Framed widget whose box shows as a row and is not
// Apart, and KindBlock for any other, a nil r included.
func KindOf(r render.Renderable) RowKind {
	switch r := r.(type) {
	case *FooterBox:
		return KindFooter
	case Framed:
		if f := r.Frame(); f.Row() && !f.Apart {
			return KindRow
		}
	}
	return KindBlock
}

// IsToolRow reports whether r shows in a PartBox that shows as a row, as a
// collapsed tool call does: whether its KindOf is KindRow.
func IsToolRow(r render.Renderable) bool { return KindOf(r) == KindRow }

// Gap returns the number of blank rows between two adjacent rows of a bubble,
// below shown right under above: none between two rows, which stack, and one
// between any other two. It is the only spacing rule.
func Gap(above, below RowKind) int {
	if above == KindRow && below == KindRow {
		return 0
	}
	return 1
}

// SpaceRows parts each of rows from the one above it by their Gap, with the top
// margin of the lower, and leaves the first with none. The rows are widgets
// that all show, in display order, and need not share a parent: their margins
// are all that spaces them.
func SpaceRows(rows []render.Renderable) {
	for i, r := range rows {
		gap := 0
		if i > 0 {
			gap = Gap(KindOf(rows[i-1]), KindOf(r))
		}
		SetEdge(r, &r.LayoutNode().MarginTop, gap)
	}
}

// ShownIn returns the children of col that show, in order. The slice is a
// copy.
func ShownIn(col *widget.Box) []render.Renderable {
	return slices.DeleteFunc(slices.Clone(col.ChildrenRaw()), func(r render.Renderable) bool { return !r.Visible() })
}
