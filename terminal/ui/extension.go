package ui

import (
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/render"
	"github.com/unaquatic/fluid/style"
)

// Extension adds the widgets of a feature to the transcript: a view of each
// content part the feature defines, and a view under the parts of each
// message, such as the changes a reply made. It is the terminal side of an
// extension.Extension, which adds the parts to the messages. Either method
// returns nil for none. An Extension that implements ToolFormatter also
// describes how the calls of the tools it knows show, and one that implements
// CommandExtension adds slash commands. Options.Extensions lists the
// extensions the UI runs with.
type Extension interface {
	// NewPartView returns the widget of part, or nil when part is not one
	// the extension shows.
	NewPartView(part message.ContentPart, host ViewHost) PartView
	// NewSegmentView returns a widget to show under the parts of msg, or nil
	// for none.
	NewSegmentView(msg ChatMessage, host ViewHost) SegmentView
}

// PartView is the widget of one content part an extension shows. It takes
// the place of the part among the parts of its message. A view that shows in
// a PartBox, as Framed tells, behaves in the transcript as a tool call does:
// Ctrl+O and /config show it as they show the calls, and it stacks right
// under a row above it while it shows as a row. A part the views of no
// extension show is shown as Markdown, or not at all when it is
// message.Metadata.
type PartView interface {
	render.Renderable
	// SetPart shows part, a new form of the part the view was made for.
	SetPart(part message.ContentPart)
}

// SegmentView is a widget an extension shows under the parts of a message,
// above the footer of the turn the message ends. A view that shows in a
// PartBox, as Framed tells, behaves as a PartView that does.
type SegmentView interface {
	render.Renderable
	// SetMessage shows msg, a new form of the message the view was made
	// for, as it streams.
	SetMessage(msg ChatMessage)
}

// StyledView is a view that follows the transcript's ViewStyle beyond what
// its PartBox follows, such as whether debug shows. The bubble sets it once
// the view is made, and whenever /config changes it.
type StyledView interface {
	SetViewStyle(ViewStyle)
}

// ViewStyle is how the transcript shows its parts, as /config sets it.
type ViewStyle struct {
	// ToolMarkers shows ToolMarker before the name of each tool call.
	ToolMarkers bool
	// MarkerShape is the symbol of the marker.
	MarkerShape MarkerShape
	// NameFg colors the marker and the name of each tool call.
	NameFg style.Color
	// Debug shows what helps debug flow.
	Debug bool
}

// ViewHost is the bubble an extension's view shows in.
type ViewHost struct {
	// Theme is the bubble's palette.
	Theme *style.Theme
	// Fg is the color of the bubble's prose.
	Fg style.Color
	// Style is how the transcript shows its parts as the view is made.
	Style ViewStyle
	// Enqueue runs fn on the UI's event loop, where widgets change, as when
	// what a view fetches off the loop arrives. It is nil where the bubble
	// shows a stored session rather than the active one, as the session
	// preview does, where views fetch nothing.
	Enqueue func(fn func())
	// Relayout lays the bubble out again once a view shows more or less
	// than it did, on the event loop.
	Relayout func()
}

// CommandExtension is an Extension that adds slash commands to the menu. They
// come after the UI's own, and before /quit, which ends the menu, as
// CommandsWith puts them.
type CommandExtension interface {
	Extension
	// SlashCommands returns the commands the extension adds, in the order
	// the menu offers them.
	SlashCommands() []SlashCommand
}

// ToolFormatter is an Extension that describes how the calls of the tools it
// knows show in their boxes, which the transcript draws as it draws every
// tool call. A box asks the bubble's extensions in order, and shows a call
// that none describes as any tool's, as the zero ToolView says.
type ToolFormatter interface {
	Extension
	// FormatTool returns how call shows, and reports whether the extension
	// describes the calls of call's tool.
	FormatTool(call message.ToolPart) (ToolView, bool)
}

// ToolView is how a tool call shows in its box, as a ToolFormatter describes
// it. The zero ToolView shows a call as any tool's: the header names the tool
// as ToolLabel does and sums the input up as ToolInputSummary does; the
// details show the first of SummaryKeys that the input holds, and then a note
// on each other param, as ParamNotes gives them; and the output is text,
// under them.
type ToolView struct {
	// Label names the tool in the header, or is "" for ToolLabel's name.
	Label string
	// HeaderNote follows the summary of the input in the header, such as
	// the lines a read reads.
	HeaderNote string
	// ParamKey is the param the details show first, as Param, or "" for the
	// first of SummaryKeys that the input holds. No note repeats it.
	ParamKey string
	// Param is what the details show of ParamKey, when ParamKey is set.
	Param string
	// Shell shows Param as a command line, "$ command", highlighted as
	// bash.
	Shell bool
	// ParamInHeader keeps the summary of the input, such as a path, after
	// the tool's name in the header while the call is expanded, rather than
	// showing it in the details, which then show only the notes.
	ParamInHeader bool
	// Notes follow Param, before the notes on the other params, such as a
	// command's timeout.
	Notes []string
	// Unnoted are the params that get no note, since the box shows them
	// otherwise, or not at all.
	Unnoted []string
	// Body is shown under the params, as code in BodyLang, such as the diff
	// of an edit.
	Body string
	// BodyLang is the language of Body, or of the output when OutputInBody
	// is set.
	BodyLang string
	// OutputInBody shows the call's output in place of Body, as code in
	// BodyLang, rather than under it as text, such as the file a read
	// reads.
	OutputInBody bool
	// Lines, when set, show in place of Body, and so of the output when
	// OutputInBody is set, as lines of text, each in the box's text color
	// or, when Muted, in the muted text color, such as the items of a todo
	// list, of which only the one in progress stands out.
	Lines []ToolLine
	// Status, when set, is the call's status in place of the one its state
	// gives, such as the matches of a completed search.
	Status string
	// KeepExpanded shows the call expanded whatever Ctrl+O says, as
	// PartBox.KeepExpanded describes, such as a todo list, which is read
	// whole.
	KeepExpanded bool
}

// ToolLine is one of the Lines a ToolView shows.
type ToolLine struct {
	// Text is what the line says, on one row, or more when it wraps.
	Text string
	// Muted shows the line in the muted text color, as an item that is
	// done or not started yet.
	Muted bool
}

// NameLabel returns Label, or ToolLabel(name) when Label is "".
func (v ToolView) NameLabel(name string) string {
	if v.Label != "" {
		return v.Label
	}
	return ToolLabel(name)
}
