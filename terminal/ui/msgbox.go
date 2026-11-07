package ui

import (
	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/render"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/widget"
)

// PaddingX is the number of columns between the left and right edges of a
// message, a tool box, or the input box and what they show. The footers under
// a reply and under the input box, and the indicator, are inset by as much, so
// that they line up with the text above them.
const PaddingX = 2

// BorderStyle defines the characters used for box borders.
type BorderStyle struct {
	// TopLeft is the top-left corner.
	TopLeft rune
	// TopRight is the top-right corner.
	TopRight rune
	// BottomLeft is the bottom-left corner.
	BottomLeft rune
	// BottomRight is the bottom-right corner.
	BottomRight rune
	// Horizontal draws the top, bottom, and divider spans.
	Horizontal rune
	// Vertical draws the side borders.
	Vertical rune
	// TeeLeft joins a divider to the left border.
	TeeLeft rune
	// TeeRight joins a divider to the right border.
	TeeRight rune
}

var (
	// BorderFill draws every border cell blank, so a box shows as a solid
	// block of its background with a one-cell inset and blank divider rows.
	BorderFill = BorderStyle{
		TopLeft: ' ', TopRight: ' ',
		BottomLeft: ' ', BottomRight: ' ',
		Horizontal: ' ', Vertical: ' ',
		TeeLeft: ' ', TeeRight: ' ',
	}
)

// BuildContentRow wraps child between the default vertical border cells,
// PaddingX columns from either edge: each border cell takes one of them, and
// the row pads the rest.
func BuildContentRow(child render.Renderable, borderFg, bg style.Color) *widget.Box {
	bs := BorderFill
	inner := widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionColumn),
			layout.WithAlignItems(layout.AlignStretch),
			layout.WithFlexGrow(1),
			layout.WithPaddingLeft(layout.Points(PaddingX-1)),
			layout.WithPaddingRight(layout.Points(PaddingX-1)),
		),
		widget.Children(child),
	)
	return widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionRow),
			layout.WithAlignItems(layout.AlignStretch),
		),
		widget.Children(
			widget.NewVLine(bs.Vertical, borderFg, bg),
			inner,
			widget.NewVLine(bs.Vertical, borderFg, bg),
		),
	)
}

// BuildArrowRow is BuildContentRow for a child whose rows start with an arrow
// column PaddingX cells wide, as a Select's do, or as the lines of the
// session tree do: the child starts at the row's left edge, so the arrow
// takes the left border cell and the text after it lines up with the text of
// the rows around it. The right side is padded as BuildContentRow pads it.
func BuildArrowRow(child render.Renderable, borderFg, bg style.Color) *widget.Box {
	bs := BorderFill
	inner := widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionColumn),
			layout.WithAlignItems(layout.AlignStretch),
			layout.WithFlexGrow(1),
			layout.WithPaddingRight(layout.Points(PaddingX-1)),
		),
		widget.Children(child),
	)
	return widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionRow),
			layout.WithAlignItems(layout.AlignStretch),
		),
		widget.Children(
			inner,
			widget.NewVLine(bs.Vertical, borderFg, bg),
		),
	)
}

// AddArrowRow appends a row that BuildArrowRow builds to middleCol and
// returns it.
func AddArrowRow(middleCol *widget.Box, child render.Renderable, borderFg, bg style.Color) *widget.Box {
	row := BuildArrowRow(child, borderFg, bg)
	middleCol.Add(row)
	return row
}

// BuildMenuRow is BuildArrowRow for a menu, which spans the whole row, its
// right border cell included, with no padding, so that a fill of its selected
// row, as UI.MarkSelection draws it, can reach either edge of the box. The
// menu draws every cell of the fill, which it repaints whenever its selection
// moves; MarkSelection keeps its text where BuildArrowRow pads it.
func BuildMenuRow(menu *widget.Select) *widget.Box {
	inner := widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionColumn),
			layout.WithAlignItems(layout.AlignStretch),
			layout.WithFlexGrow(1),
		),
		widget.Children(menu),
	)
	return widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionRow),
			layout.WithAlignItems(layout.AlignStretch),
		),
		widget.Children(inner),
	)
}

// AddMenuRow appends a row that BuildMenuRow builds to middleCol and returns
// it.
func AddMenuRow(middleCol *widget.Box, menu *widget.Select) *widget.Box {
	row := BuildMenuRow(menu)
	middleCol.Add(row)
	return row
}

// GutterRow insets child by PaddingX, as the footers under a reply and under
// the input box are.
func GutterRow(child render.Renderable) *widget.Box {
	return widget.NewBox(
		widget.BoxWithLayout(
			layout.WithPaddingLeft(layout.Points(PaddingX)),
			layout.WithPaddingRight(layout.Points(PaddingX)),
		),
		widget.Children(child),
	)
}

// AddContentRow appends a bordered content row to middleCol and returns it.
func AddContentRow(middleCol *widget.Box, child render.Renderable, borderFg, bg style.Color) *widget.Box {
	row := BuildContentRow(child, borderFg, bg)
	middleCol.Add(row)
	return row
}

// BuildDividerRow creates a default divider row, which BorderFill draws blank.
func BuildDividerRow(borderFg, bg style.Color) *widget.Box {
	bs := BorderFill
	return widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionRow),
			layout.WithHeight(layout.Points(1)),
			layout.WithFlexShrink(0),
		),
		widget.Children(
			widget.NewCharCell(bs.TeeLeft, borderFg, bg),
			widget.NewHLine(bs.Horizontal, borderFg, bg),
			widget.NewCharCell(bs.TeeRight, borderFg, bg),
		),
	)
}

// NewInputPanel returns a hidden column for rows that open at the top of the
// input box, such as a banner or the session list.
func NewInputPanel(id string) *widget.Box {
	panel := widget.NewBox(
		widget.BoxWithID(id),
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionColumn),
			layout.WithAlignItems(layout.AlignStretch),
		),
	)
	panel.SetGone(true)
	return panel
}

// AddDividerRow appends a default divider row to middleCol.
func AddDividerRow(middleCol *widget.Box, borderFg, bg style.Color) {
	middleCol.Add(BuildDividerRow(borderFg, bg))
}

// StreamableMessageBox creates a message container using BorderFill, with a
// content row for each child, and returns it with its middle column, which
// rows can later be added to.
func StreamableMessageBox(borderFg, bg style.Color, children ...render.Renderable) (*widget.Box, *widget.Box) {
	bs := BorderFill
	topBorder := widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionRow),
			layout.WithHeight(layout.Points(1)),
			layout.WithFlexShrink(0),
		),
		widget.Children(
			widget.NewCharCell(bs.TopLeft, borderFg, bg),
			widget.NewHLine(bs.Horizontal, borderFg, bg),
			widget.NewCharCell(bs.TopRight, borderFg, bg),
		),
	)
	bottomBorder := widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionRow),
			layout.WithHeight(layout.Points(1)),
			layout.WithFlexShrink(0),
		),
		widget.Children(
			widget.NewCharCell(bs.BottomLeft, borderFg, bg),
			widget.NewHLine(bs.Horizontal, borderFg, bg),
			widget.NewCharCell(bs.BottomRight, borderFg, bg),
		),
	)

	// Grow the middle column to keep the bottom border at the container edge.
	middleCol := widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionColumn),
			layout.WithFlexGrow(1),
			layout.WithAlignItems(layout.AlignStretch),
		),
		widget.BoxWithBg(bg),
	)
	for _, child := range children {
		AddContentRow(middleCol, child, borderFg, bg)
	}

	box := widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexGrow(1),
			layout.WithFlexDirection(layout.DirectionColumn),
			layout.WithAlignItems(layout.AlignStretch),
		),
	)
	box.Add(topBorder)
	box.Add(middleCol)
	box.Add(bottomBorder)

	return box, middleCol
}
