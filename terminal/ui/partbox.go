package ui

import (
	"github.com/unaquatic/fluid/input"
	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/render"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/widget"
)

// BoxShape is how a PartBox shows while it is collapsed.
type BoxShape int

const (
	// ShapeByStyle shows the box as a row while it is collapsed, and as a
	// block, as its Style says, while it is open. A tool call shows so.
	ShapeByStyle BoxShape = iota
	// ShapeRow always shows the box as a row, as a snapshot being taken
	// shows.
	ShapeRow
	// ShapeBlock always shows the box as a block, as a step's changes show.
	ShapeBlock
)

// BoxContent is what a PartBox frames: a tool call, a snapshot being taken,
// a step's changes. The content writes the box's Header line and status, and
// adds the widgets of its details to Details, which may be any widgets. The
// box decides how they show: as a row or a block, expanded or not, with a
// marker, which blinks while the content is busy.
type BoxContent interface {
	// ShowHeader writes the box's Header line as the box now stands. The box
	// calls it whenever that may change what the line shows: as the box
	// expands or collapses, and as its NameFg changes. A tool call's line,
	// for one, drops the input that its expanded details show.
	ShowHeader()
}

// Framed is a widget that shows in a PartBox, which the transcript expands
// with Ctrl+O, shows as /config says, and stacks with the rows around it: a
// tool call, or the view of an extension that embeds a PartBox, such as a
// snapshot being taken.
type Framed interface {
	render.Renderable
	// Frame returns the box the widget shows in.
	Frame() *PartBox
}

// PartBox is the frame of one item of a message in the transcript. It owns
// how the item shows, and its Content what it shows. While collapsed it is a
// row on the canvas or a block that fills Surface, as Shape says; a click on
// its header, or Ctrl+O, expands it into a block that shows Details, when it
// is Expandable, unless it is Held. Its header row holds Marker, while
// /config shows it and the box is Marked, then Header, which is cut off
// with an ellipsis when the row is full, and then Status, which stays whole.
// While its content is Busy, Marker blinks.
type PartBox struct {
	*widget.Box
	// Content writes Header and fills Details, as BoxContent describes.
	Content BoxContent
	// HeaderRow lays out Marker, Header, and Status on one row, and expands
	// the box when clicked. While Marker shows, it starts PaddingX columns to
	// the left of the box's content, in its padding, which Marker and the gap
	// after it fill, so that Header starts where the content does either
	// way.
	HeaderRow *widget.Box
	// Marker shows its MarkerShape before Header, in NameFg, while the box is
	// Marked, collapsed or open in ExpandedPlain, and SetMarker shows it.
	Marker *widget.Text
	// Header is the line the Content writes, such as a tool's name and a
	// summary of its input. It fills no background of its own, so that the
	// box's shows through it.
	Header *widget.Text
	// Status shows the content's status, such as "[queued]", right after
	// Header, in the theme's TextMuted. It is gone while the status is empty.
	Status *widget.Text
	// Details holds the widgets the Content shows while the box is Open.
	Details *widget.Box
	// Expanded reports whether the box shows Details, as a click or Ctrl+O
	// asks, once it is not Held.
	Expanded bool
	// Held keeps the box collapsed whatever Expanded says, as SetHeld sets
	// it, until its content, and the content above it, take their final
	// form.
	Held bool
	// Expandable reports whether a click on the header and Ctrl+O expand
	// the box. A box that is not stays collapsed.
	Expandable bool
	// KeepExpanded shows the box expanded whatever Ctrl+O says, as for a
	// todo list, which is read whole: the transcript adopts it expanded,
	// and Ctrl+O leaves it as it is. A click on its header still expands
	// and collapses it.
	KeepExpanded bool
	// Marked reports whether the box shows Marker as /config says, as a
	// tool call does.
	Marked bool
	// MarkerShape is the symbol of Marker, as SetMarkerShape sets it.
	MarkerShape MarkerShape
	// MarkerOn reports that SetMarker shows Marker. It is off by default.
	MarkerOn bool
	// NameFg colors the name the Content writes in Header, and Marker unless
	// MarkerFg is set, as SetNameFg sets it. It is the theme's TextMuted by
	// default.
	NameFg style.Color
	// MarkerFg, when set, colors Marker in place of NameFg, as for a box
	// whose header starts with a title rather than a tool's name.
	MarkerFg style.Color
	// Shape is how the box shows while it is collapsed.
	Shape BoxShape
	// Apart parts the box from the rows around it while it shows as a row,
	// by a blank row above and below, as a block is parted, rather than
	// stacking it under and over other rows, as collapsed tool calls stack.
	// A snapshot and the cache row show apart.
	Apart bool
	// Style is how the box shows as a block: on Surface, or on the canvas.
	Style ExpandedStyle
	// Surface is the background a block fills, padding included, in
	// ExpandedBoxed. A row fills none, so that it sits on the canvas, and
	// neither does a block in ExpandedPlain.
	Surface style.Color
	// Busy reports that the content is at work, as an executing tool call
	// is, which Marker shows by blinking.
	Busy bool
	// Blink, when set, is the clock Marker blinks by while Blinks reports
	// it, in step with the marker before the indicator's time. SyncBlink
	// keeps the box among its Rows meanwhile.
	Blink *Blink
	// MarkerHidden reports that Marker was last drawn hidden, as Blinks and
	// the blink's Off hid it.
	MarkerHidden bool
	// OnShapeChange, when set, is called each time ShowShape lays the box
	// out, as when it expands or its Style changes, so that the bubble that
	// holds it can space it from the rows around it.
	OnShapeChange func()
	// StatusText is the content's status, as SetStatus sets it.
	StatusText string
}

var _ Framed = (*PartBox)(nil)

// NewPartBox creates a collapsed, expandable box whose Header is in fg, and
// whose block, in ExpandedBoxed, fills surface, padded by a row above and
// below and by PaddingX at the sides. Its Shape is ShapeByStyle and its Style
// ExpandedPlain, so that it shows as a row until the caller says otherwise
// and calls ShowShape. The caller sets Content before the box shows its
// header.
func NewPartBox(theme *style.Theme, fg, surface style.Color) *PartBox {
	theme = themeOrDefault(theme)
	b := &PartBox{
		Box: widget.NewBox(
			widget.BoxWithLayout(
				layout.WithFlexDirection(layout.DirectionColumn),
				layout.WithAlignItems(layout.AlignStretch),
				layout.WithPadding(layout.Points(1)),
				layout.WithPaddingLeft(layout.Points(PaddingX)),
				layout.WithPaddingRight(layout.Points(PaddingX)),
				layout.WithGap(1),
			),
			widget.BoxWithBg(theme.ResolveSurfaceRaised()),
		),
		HeaderRow: widget.NewBox(
			widget.BoxWithLayout(layout.WithFlexDirection(layout.DirectionRow), layout.WithGap(1)),
		),
		Marker: NewMarker(MarkerSquare, theme.TextMuted),
		Header: widget.NewText("", widget.TextWithFg(fg)),
		Status: widget.NewText("",
			widget.TextWithFg(theme.TextMuted),
			widget.TextWithTruncate(true),
			widget.TextWithLayout(layout.WithFlexShrink(0)),
		),
		Details: widget.NewBox(
			widget.BoxWithLayout(
				layout.WithFlexDirection(layout.DirectionColumn),
				layout.WithAlignItems(layout.AlignStretch),
				layout.WithGap(1),
			),
		),
		Expandable: true,
		Marked:     true,
		NameFg:     theme.TextMuted,
		Surface:    surface,
	}
	// Header shrinks, and Status does not, when the two do not fit.
	b.Header.SetTruncate(true)
	b.Marker.RenderBeforeHook, b.Marker.RenderAfterHook = BlinkHooks(b.Marker, b.HideMarker)
	b.Header.OnMouseDownHandler = b.OnHeaderClick
	b.HeaderRow.OnMouseDownHandler = b.OnHeaderClick
	b.Add(b.HeaderRow)
	b.Add(b.Details)
	b.HeaderRow.Add(b.Marker)
	b.Marker.SetGone(true)
	b.HeaderRow.Add(b.Header)
	b.HeaderRow.Add(b.Status)
	b.Status.SetGone(true)
	b.Details.SetGone(true)
	return b
}

// Frame returns b.
func (b *PartBox) Frame() *PartBox { return b }

// OnHeaderClick expands or collapses an Expandable box as a left click on its
// header row asks, in place, as ToggleInPlace does.
func (b *PartBox) OnHeaderClick(e *input.MouseEvent) {
	if e.Button == input.ButtonLeft && b.Expandable {
		b.ToggleInPlace()
		e.StopPropagation()
	}
}

// Open reports whether the box shows Details: while it is Expanded and not
// Held.
func (b *PartBox) Open() bool { return b.Expanded && !b.Held }

// Row reports whether the box shows as its header alone, one row on the
// canvas: always when its Shape is ShapeRow, never when it is ShapeBlock,
// and otherwise while it is not Open.
func (b *PartBox) Row() bool {
	switch b.Shape {
	case ShapeRow:
		return true
	case ShapeBlock:
		return false
	}
	return !b.Open()
}

// SetStyle shows the box as ts says while it is a block.
func (b *PartBox) SetStyle(ts ExpandedStyle) {
	if ts == b.Style {
		return
	}
	b.Style = ts
	b.ShowMarker()
	b.ShowShape()
}

// SetMarker shows Marker at the left edge of a Marked box while it is
// collapsed, or open in ExpandedPlain, or hides it, as show says.
func (b *PartBox) SetMarker(show bool) {
	b.MarkerOn = show
	b.ShowMarker()
}

// ShowMarker shows Marker while the box is Marked and MarkerOn is set, and
// is collapsed, or open in ExpandedPlain, on the canvas as a row is, and
// hides it otherwise. Header stays where it is either way.
func (b *PartBox) ShowMarker() {
	show := b.Marked && b.MarkerOn && (!b.Open() || b.Style == ExpandedPlain)
	if b.Marker.Visible() == show {
		return
	}
	b.Marker.SetGone(!show)
	if !show {
		// A gone marker draws nothing, so it is not hidden by the blink.
		b.MarkerHidden = false
	}
	margin := 0
	if show {
		margin = -PaddingX
	}
	SetEdge(b.HeaderRow, &b.HeaderRow.LayoutNode().MarginLeft, margin)
}

// SetMarkerShape shows Marker in shape.
func (b *PartBox) SetMarkerShape(shape MarkerShape) {
	b.MarkerShape = shape
	StyleMarker(b.Marker, shape, b.MarkerColor())
}

// MarkerColor returns the color of Marker: MarkerFg when set, and NameFg
// otherwise.
func (b *PartBox) MarkerColor() style.Color {
	if !b.MarkerFg.IsTransparent() {
		return b.MarkerFg
	}
	return b.NameFg
}

// SetNameFg colors the name the Content writes in Header fg, and Marker
// unless MarkerFg is set.
func (b *PartBox) SetNameFg(fg style.Color) {
	if fg == b.NameFg {
		return
	}
	b.NameFg = fg
	StyleMarker(b.Marker, b.MarkerShape, b.MarkerColor())
	b.ShowHeader()
}

// ShowHeader has the Content write Header, when the box has a Content.
func (b *PartBox) ShowHeader() {
	if b.Content != nil {
		b.Content.ShowHeader()
	}
}

// SetSurface paints the box, and so its header, on bg. A transparent color
// lets the enclosing surface show through.
func (b *PartBox) SetSurface(bg style.Color) {
	b.SetBg(bg)
}

// ShowShape lays the box out as Row reports: as its header alone, with no
// padding above or below it and no background, or as a block, which in
// ExpandedBoxed fills Surface and pads its content by a row above and below,
// and in ExpandedPlain does neither. Then it calls OnShapeChange.
func (b *PartBox) ShowShape() {
	bg, pad := b.Surface, 1
	if b.Row() || b.Style == ExpandedPlain {
		bg, pad = style.ColorTransparent, 0
	}
	if b.Bg() != bg {
		b.SetSurface(bg)
	}
	SetEdge(b, &b.LayoutNode().PaddingTop, pad)
	SetEdge(b, &b.LayoutNode().PaddingBottom, pad)
	// The marker shows or hides with the shape, and blinks only while shown.
	b.SyncBlink()
	if b.OnShapeChange != nil {
		b.OnShapeChange()
	}
}

// SetExpanded shows or hides Details, as expanded says, when the box is
// Expandable, as ShowOpen does. A box that is not Expandable stays
// collapsed, and a Held one stays collapsed until SetHeld releases it.
func (b *PartBox) SetExpanded(expanded bool) {
	if expanded && !b.Expandable {
		return
	}
	b.Expanded = expanded
	b.ShowOpen()
}

// SetHeld keeps the box collapsed, whatever Expanded says, while held is
// set, and otherwise shows it as Expanded says, as ShowOpen does.
//
// Inline mode holds a box while its content, or the content above it, has
// yet to take its final form, as a tool call that runs has: rows that
// scroll into the terminal's scrollback cannot be redrawn, so the box shows
// only its header, which stays on screen, until nothing above it changes.
func (b *PartBox) SetHeld(held bool) {
	if held == b.Held {
		return
	}
	b.Held = held
	b.ShowOpen()
}

// ShowOpen shows Details while the box is Open and hides it otherwise, and
// then has the Content write Header, and shows Marker and the shape, which
// follow it.
func (b *PartBox) ShowOpen() {
	b.Details.SetGone(!b.Open())
	b.ShowHeader()
	b.ShowMarker()
	b.ShowShape()
}

// ToggleExpanded shows or hides Details.
func (b *PartBox) ToggleExpanded() { b.SetExpanded(!b.Expanded) }

// FollowsToolsExpanded reports whether Ctrl+O expands and collapses the box:
// whether it is Expandable and not KeepExpanded.
func (b *PartBox) FollowsToolsExpanded() bool { return b.Expandable && !b.KeepExpanded }

// ToggleInPlace shows or hides Details as a click on the header does,
// keeping the header on the row it was clicked on. A scroll box that holds
// the box and follows its bottom edge, as the fullscreen transcript does,
// would otherwise push the header up by the height of the details it shows,
// so it stops following, and the details open downward from the header.
// Scrolling back to the bottom, or collapsing the details there, follows the
// edge again.
func (b *PartBox) ToggleInPlace() {
	if sb := EnclosingScrollBox(b); sb != nil {
		sb.StopFollowing()
	}
	b.ToggleExpanded()
}

// SetStatus shows status, the content's status, in Status.
func (b *PartBox) SetStatus(status string) {
	if status == b.StatusText {
		return
	}
	b.StatusText = status
	b.Status.SetContent(status)
	b.Status.SetGone(status == "")
}

// SetBusy records whether the content is at work, which Marker shows by
// blinking.
func (b *PartBox) SetBusy(busy bool) {
	b.Busy = busy
	b.SyncBlink()
}

// Blinks reports whether Marker blinks: while it shows, the content is
// Busy, and the blink runs, as the indicator's marker blinks beside the
// run's time.
func (b *PartBox) Blinks() bool {
	return b.Blink != nil && b.Blink.On.Load() && b.Busy && b.Marker.Visible()
}

// HideMarker reports whether Marker is hidden now, while it Blinks and the
// blink's Off hides it, and records that in MarkerHidden. It runs as Marker
// draws itself.
func (b *PartBox) HideMarker() bool {
	b.MarkerHidden = b.Blinks() && b.Blink.Off
	return b.MarkerHidden
}

// SyncBlink repaints Marker when the blink hides it otherwise than when it
// was drawn: as the blink turns, starts, or stops, or as the content stops
// being busy. Only the marker changes, so the layout stays as it is. It
// keeps the box among the blink's Rows while its marker may blink, or was
// drawn hidden, so that the blink repaints it as it turns.
func (b *PartBox) SyncBlink() {
	if b.Blink == nil {
		return
	}
	b.Blink.Track(b, b.Busy && b.Marker.Visible() || b.MarkerHidden)
	if hide := b.Blinks() && b.Blink.Off; hide != b.MarkerHidden {
		b.Marker.RequestRender()
	}
}

// Pin stops the header from expanding or collapsing the box, keeping its
// current state.
func (b *PartBox) Pin() { b.Expandable = false }

// EnclosingScrollBox returns the nearest scroll box that holds r, or nil when
// none does, as in inline mode, where the terminal scrolls the transcript. A
// parent is the base its widget embeds, so each is looked up as the widget
// it belongs to, as fluid does when it dispatches mouse events.
func EnclosingScrollBox(r render.Renderable) *widget.ScrollBox {
	for p := r.Parent(); p != nil; p = p.Parent() {
		if sb, ok := render.LookupRenderable(p.Num()).(*widget.ScrollBox); ok {
			return sb
		}
	}
	return nil
}

// ToolMarker marks the header of every tool call, at the left edge of its
// box, before the tool's name, when /config shows it, in the symbol of the
// default MarkerShape.
var ToolMarker = MarkerSquare.Glyph()

// NewMarker returns a marker in shape, in c, which StyleMarker changes.
func NewMarker(shape MarkerShape, c style.Color) *widget.Text {
	m := widget.NewText("", widget.TextWithLayout(layout.WithFlexShrink(0)))
	StyleMarker(m, shape, c)
	return m
}

// StyleMarker shows m, a marker NewMarker made, in shape and c: its symbol
// drawn in c, or for a filled shape a space filled with c.
func StyleMarker(m *widget.Text, shape MarkerShape, c style.Color) {
	m.SetContent(shape.Glyph())
	if shape.Filled() {
		m.SetFg(c)
		m.SetBg(c)
		return
	}
	m.SetFg(c)
	m.SetBg(style.ColorTransparent)
}

// TitleChunk returns title in titleFg and not bold, as a panel shows its
// title, for the label a patch's header starts with.
func TitleChunk(title string) style.TextChunk {
	fg := titleFg
	return style.TextChunk{Text: title, Fg: fg}
}
