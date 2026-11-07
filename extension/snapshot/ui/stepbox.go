package ui

import (
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension/snapshot"
	"github.com/unaquatic/flow/extension/snapshot/internal/engine"
	"github.com/unaquatic/flow/terminal/ui"
	"github.com/unaquatic/fluid/style"
)

// IndexingStatus is the status a snapshot shows after its name while it is
// being taken, as a tool call shows "[executing]" while it runs.
const IndexingStatus = "[indexing]"

// FailedStatus is the status a snapshot shows once taken when recording the
// workspace failed, and the part holds no tree.
const FailedStatus = "[failed]"

// SnapshotLabel is the name a snapshot being taken shows, as ui.ToolLabel
// names a tool.
const SnapshotLabel = "Snapshot"

// StepBox is the ui.PartBox of a step part. While the part is pending, it is
// a row like a collapsed tool call's, "Snapshot [indexing]", so that its
// message shows the snapshot being taken. Once the part holds its tree it is
// gone, unless ShowDone keeps it, as the Debug setting does: then it shows
// the tree's hash, as engine.ShortHash gives it, as in "Snapshot 7a36b04c",
// or FailedStatus. The box always shows as a row, starts with ui.ToolMarker
// as tool calls do, and has nothing to expand. The changes of a reply's step
// show in its PatchBox either way.
type StepBox struct {
	*ui.PartBox
	// Part is the step part shown.
	Part snapshot.StepPart
	// ShowDone keeps the box once the part holds its tree, as SetShowDone
	// sets it.
	ShowDone bool
}

var (
	_ ui.PartView   = (*StepBox)(nil)
	_ ui.StyledView = (*StepBox)(nil)
	_ ui.BoxContent = (*StepBox)(nil)
)

// NewStepBox creates the widget of part, a row on the canvas while part is
// pending and gone otherwise, until SetShowDone keeps it.
func NewStepBox(part snapshot.StepPart) *StepBox {
	b := &StepBox{PartBox: ui.NewPartBox(nil, style.Color{}, style.ColorTransparent)}
	b.Content = b
	b.Shape = ui.ShapeRow
	b.Pin()
	b.Show(part)
	b.ShowShape()
	return b
}

// SetPart shows part, when it is a step part other than the one already
// shown.
func (b *StepBox) SetPart(part message.ContentPart) {
	if step, ok := part.(snapshot.StepPart); ok && step != b.Part {
		b.Show(step)
	}
}

// SetViewStyle shows the box as st says: the snapshot once taken while
// st.Debug is on, the marker as tool calls show it, in their shape, and the
// name in their color.
func (b *StepBox) SetViewStyle(st ui.ViewStyle) {
	b.SetShowDone(st.Debug)
	b.SetMarker(st.ToolMarkers)
	b.SetNameFg(st.NameFg)
	b.SetMarkerShape(st.MarkerShape)
}

// Show shows part: the box while part is pending, and once it holds its tree
// only while ShowDone keeps it.
func (b *StepBox) Show(part snapshot.StepPart) {
	b.Part = part
	b.ShowHeader()
	if show := part.Pending || b.ShowDone; b.Visible() != show {
		b.SetGone(!show)
	}
}

// SetShowDone keeps the box once its part holds its tree, showing the tree's
// hash, or hides it then, as show says.
func (b *StepBox) SetShowDone(show bool) {
	if show == b.ShowDone {
		return
	}
	b.ShowDone = show
	b.Show(b.Part)
}

// ShowHeader writes SnapshotLabel in NameFg, not bold, then the part's status
// in ui.ToolFg, as StepStatus gives it.
func (b *StepBox) ShowHeader() {
	b.Header.SetStyledContent(style.NewStyledText(
		style.TextChunk{Text: SnapshotLabel, Fg: b.NameFg},
		style.Plain(" "),
		style.TextChunk{Text: StepStatus(b.Part), Fg: ui.ToolFg},
	))
}

// StepStatus returns what a step part's row shows after SnapshotLabel:
// IndexingStatus while the part is pending, then the hash of its tree, as
// engine.ShortHash gives it, or FailedStatus when it holds none.
func StepStatus(part snapshot.StepPart) string {
	switch {
	case part.Pending:
		return IndexingStatus
	case part.Snapshot.IsZero():
		return FailedStatus
	}
	return engine.ShortHash(part.Snapshot)
}
