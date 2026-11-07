package ui

import (
	"iter"
	"slices"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/render"
	"github.com/unaquatic/fluid/widget"
)

// PartsBox holds the widgets of a message's parts, in the order of the parts,
// in one or two columns.
//
// Only a direct child of the transcript's scroll container can pin to its top
// edge, and it pins whole. So that a prompt's text may pin without the rows
// under it, such as its snapshot, a prompt's parts split in two columns that
// the bubble shows in two rows of the transcript: the text in Head, in the
// pinned row, and every other part in Tail, in the row under it, which
// scrolls like any other. The columns are the widgets; the PartsBox is not,
// and is never added to one.
type PartsBox struct {
	// Head is the column of the parts that stay with the message's first row:
	// all of them but for a prompt's.
	Head *widget.Box
	// Tail is the column of the parts that scroll away from a pinned row:
	// every part of a prompt but its text. It is nil when Head holds all
	// parts.
	Tail *widget.Box
}

// NewPartsBox returns a box whose Head is a new column and whose Tail is
// tail, a column such as NewPartsColumn makes, or nil for none.
func NewPartsBox(tail *widget.Box) *PartsBox {
	return &PartsBox{Head: NewPartsColumn(), Tail: tail}
}

// NewPartsColumn returns a column for the widgets of a message's parts. It
// puts no space between them: the bubble's SyncRows and SyncTail do, as Gap
// says.
func NewPartsColumn() *widget.Box {
	return widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionColumn),
			layout.WithFlexGrow(1),
		),
	)
}

// ColumnFor returns the column that holds the widget of part: Head, but for
// a part other than a prompt's text, which goes to Tail when there is one.
func (b *PartsBox) ColumnFor(part message.ContentPart) *widget.Box {
	if _, text := part.(message.TextPart); !text && b.Tail != nil {
		return b.Tail
	}
	return b.Head
}

// AddPart adds w, the widget of part, after those of the parts before it.
func (b *PartsBox) AddPart(part message.ContentPart, w render.Renderable) {
	b.ColumnFor(part).Add(w)
}

// ChildrenRaw returns the widgets of all parts, in the order of the parts
// that added them: those of Head, then those of Tail. It is for reading only.
// It shares storage with Head when there is no Tail, and is a copy otherwise.
func (b *PartsBox) ChildrenRaw() []render.Renderable {
	if b.Tail == nil {
		return b.Head.ChildrenRaw()
	}
	return slices.Concat(b.Head.ChildrenRaw(), b.Tail.ChildrenRaw())
}

// Children returns a copy of the widgets of all parts, as ChildrenRaw orders
// them.
func (b *PartsBox) Children() []render.Renderable {
	return slices.Clone(b.ChildrenRaw())
}

// TailRows returns the widgets of Tail that show, in order, or none when there
// is no Tail. The slice is a copy.
func (b *PartsBox) TailRows() []render.Renderable {
	if b.Tail == nil {
		return nil
	}
	return ShownIn(b.Tail)
}

// NewTailRow returns the row of the transcript under a prompt's pinned row,
// which holds the parts that scroll: it is a column that keeps its content
// height, as the bubble does, and takes no rows while it shows nothing.
// SyncTail pads it with the blank row below that parts it from the row under
// it, as ChatMessageContainer says.
func NewTailRow() *widget.Box {
	return widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionColumn),
			layout.WithAlignItems(layout.AlignStretch),
			layout.WithFlexShrink(0),
		),
	)
}

// NewSegmentParts returns the box for the parts of a new segment of the
// bubble. A prompt's has a Tail, a column in the bubble's TailRow.
func (cm *ChatMessageBox) NewSegmentParts() *PartsBox {
	if cm.TailRow == nil {
		return NewPartsBox(nil)
	}
	tail := NewPartsColumn()
	cm.TailRow.Add(tail)
	return NewPartsBox(tail)
}

// Rows yields the rows the bubble takes in the transcript, in display order:
// Box, then the row of the parts that scroll away from a pinned Box, when it
// has one. A prompt's bubble has both.
func (cm *ChatMessageBox) Rows() iter.Seq[*widget.Box] {
	return func(yield func(*widget.Box) bool) {
		if yield(cm.Box) && cm.TailRow != nil {
			yield(cm.TailRow)
		}
	}
}

// SyncTail spaces the parts that show in the row under a prompt's pinned row,
// as Gap says, and pads the row with the blank row below it while it shows a
// part, taking the row back while it shows none, as when the snapshot of a
// prompt is not shown. It does nothing for a bubble without that row.
func (cm *ChatMessageBox) SyncTail() {
	if cm.TailRow == nil {
		return
	}
	rows := 0
	for _, seg := range cm.segments {
		tail := seg.ContentBox.TailRows()
		SpaceRows(tail)
		if len(tail) > 0 {
			rows = 1
		}
	}
	SetEdge(cm.TailRow, &cm.TailRow.LayoutNode().PaddingBottom, rows)
}

// SharesBubble reports whether the message joins the bubble of the message
// that ParentID names, as consecutive messages of a role do. Prompts never
// do: each pins on its own, until the next prompt takes its place.
func (m ChatMessage) SharesBubble() bool {
	return m.HasParent() && m.Role != message.RoleUser
}
