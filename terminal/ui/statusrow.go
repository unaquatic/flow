package ui

import (
	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/widget"
)

// StatusLabel is the name the Usage box shows, as ToolLabel names a tool.
const StatusLabel = "Usage"

// StatusRow is the Usage box above the input box, which tells of the
// session: the model, the context, the session's tokens, cost, and worked
// time, as StatusLine gives them. It is a row like a collapsed tool call's,
// with nothing to expand, as a cache row has not: one row on the canvas,
// StatusLabel in NameFg and then the line in its own colors, as StatusLine
// gives them, with the box's keys as its status, and ToolMarker before
// it while SetMarker shows it, whether tool calls are expanded or not.
type StatusRow struct {
	*PartBox
	// Line holds the status line in its own colors, as StatusLine gives it.
	// It is not shown.
	Line *widget.Text
	// Summary is the status line as the header shows it, in its own colors,
	// as SetLine sets it.
	Summary style.StyledText
}

var _ BoxContent = (*StatusRow)(nil)

// NewStatusRow creates the Usage box's row. Its line is cut off with an
// ellipsis at the right end on a narrow terminal.
func NewStatusRow(theme *style.Theme) *StatusRow {
	theme = themeOrDefault(theme)
	b := &StatusRow{
		PartBox: NewPartBox(theme, style.Color{}, style.ColorTransparent),
		Line: widget.NewStyledText(style.StyledText{},
			widget.TextWithID("slash-status"),
			widget.TextWithLayout(layout.WithHeight(layout.Points(1))),
		),
	}
	b.Content = b
	b.Shape = ShapeRow
	b.Pin()
	b.ShowHeader()
	b.ShowShape()
	return b
}

// SetLine shows line, the status line, and keys, the box's keys as a
// panel's title names them, as the box's status.
func (b *StatusRow) SetLine(line style.StyledText, keys string) {
	b.Line.SetStyledContent(line)
	b.Summary = line
	b.ShowHeader()
	b.SetStatus(keys)
}

// ShowHeader writes StatusLabel in NameFg, not bold, then Summary in its
// own colors: its values bright, their labels dim, and the context in the
// warning or danger color once it fills most of the window.
func (b *StatusRow) ShowHeader() {
	header := style.NewStyledText(ToolNameChunk(StatusLabel, b.NameFg))
	if len(b.Summary.Chunks) > 0 {
		header.Chunks = append(header.Chunks, style.Plain(" "))
		header.Chunks = append(header.Chunks, b.Summary.Chunks...)
	}
	b.Header.SetStyledContent(header)
}
