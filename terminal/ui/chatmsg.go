package ui

import (
	"iter"
	"slices"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/render"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/widget"
)

// MessageSegment holds the widgets for one message in a shared chat bubble.
type MessageSegment struct {
	// MsgID is the message this segment renders.
	MsgID message.ID

	// Box wraps the segment's content and its footer.
	Box *widget.Box

	// ContentBox contains the widgets that render the message's parts.
	ContentBox *PartsBox

	// Views are the widgets that extensions show under the message's parts,
	// such as the changes the message made.
	Views []SegmentView

	// Cache is the row of the message's cache use, under the parts and the
	// Views, which shows in the CachePerMessage mode.
	Cache *CacheBox

	// Footer reports how long the turn took below the reply that ends it.
	Footer *FooterBox

	// Parts maps each part ID to its widget.
	Parts map[string]render.Renderable

	// Usage is the token counts of the message's request, whose cache use
	// SyncCache shows.
	Usage message.Usage
}

// ChatMessageBox renders consecutive messages from the same role in one
// bubble, each segment spaced from the one above it by a blank row, but for
// prompts, which take a bubble each (see ChatMessage.SharesBubble). The
// bubble fills no background of its own: each of its items shows as it does
// in any bubble, a row on the canvas or a block that fills its own, as an
// expanded tool call and a prompt's text do.
//
// A bubble takes the rows Rows yields in the transcript. A prompt's takes two
// so that its text pins to the top of the fullscreen transcript without the
// rest of it: Box holds the text, and TailRow under it the other parts, such
// as the snapshot, which scroll like the rows of a reply.
type ChatMessageBox struct {
	*widget.Box
	// Frame pads the segment column with the blank row below it that parts
	// the bubble from the row under it, as ChatMessageContainer says.
	Frame     *widget.Box
	middleCol *widget.Box
	// TailRow is the row of the transcript under Box that holds the parts of
	// a prompt that scroll away while its text stays pinned, such as its
	// snapshot, as PartsBox says. It is nil for a bubble of another role,
	// which takes the one row.
	TailRow *widget.Box
	// ToolsExpanded reports whether tool calls and step changes added to the
	// bubble show their details.
	ToolsExpanded bool
	// ToolStyle is how the bubble's collapsed tool calls show.
	ToolStyle ToolCallStyle
	// ToolMarkers reports that the bubble's tool calls show ToolMarker
	// before their name.
	ToolMarkers bool
	// ToolColor is the color of the marker and the name of the bubble's
	// tool calls.
	ToolColor ToolNameColor
	// Debug reports that the bubble shows what helps debug flow, such as
	// the snapshot each message ends with.
	Debug bool
	// Shimmer, when set, is the shimmer whose band sweeps across the rows of
	// the bubble's tool calls while they execute.
	Shimmer *Shimmer
	// CacheMode is where the bubble's replies show the cache use of their
	// requests.
	CacheMode CacheMode
	// Extensions add the widgets of the parts they define, and of what they
	// show under each message.
	Extensions []Extension
	// Enqueue runs a function on the UI's event loop, as ViewHost.Enqueue
	// describes, or is nil where the bubble shows a stored session.
	Enqueue  func(func())
	theme    *style.Theme
	fg       style.Color
	bg       style.Color
	role     message.Role
	segments []*MessageSegment
}

// ChatMessageOption configures a ChatMessageBox during construction.
type ChatMessageOption func(*ChatMessageBox)

// ChatMessageWithTheme sets the bubble's palette. A nil theme uses the default.
func ChatMessageWithTheme(t *style.Theme) ChatMessageOption {
	return func(cm *ChatMessageBox) {
		if t != nil {
			cm.theme = t
		}
	}
}

// ChatMessageWithBg sets the background the bubble's prose fills, as a block
// padded by a row above and below, as a prompt's text is. Transparent prose,
// the default, sits on the canvas, as a reply's does.
func ChatMessageWithBg(c style.Color) ChatMessageOption {
	return func(cm *ChatMessageBox) { cm.bg = c }
}

// ChatMessageWithToolsExpanded sets whether the bubble's tool calls and step
// changes show their details. They are collapsed by default.
func ChatMessageWithToolsExpanded(expanded bool) ChatMessageOption {
	return func(cm *ChatMessageBox) { cm.ToolsExpanded = expanded }
}

// ChatMessageWithToolStyle sets how the bubble's collapsed tool calls show.
// They are ToolCallsCompact by default.
func ChatMessageWithToolStyle(ts ToolCallStyle) ChatMessageOption {
	return func(cm *ChatMessageBox) { cm.ToolStyle = ts }
}

// ChatMessageWithToolColor sets the color of the marker and the name of the
// bubble's tool calls. It is ToolNameGray by default.
func ChatMessageWithToolColor(c ToolNameColor) ChatMessageOption {
	return func(cm *ChatMessageBox) { cm.ToolColor = c }
}

// ChatMessageWithDebug sets whether the bubble shows what helps debug flow.
// It does not by default.
func ChatMessageWithDebug(on bool) ChatMessageOption {
	return func(cm *ChatMessageBox) { cm.Debug = on }
}

// ChatMessageWithToolMarkers sets whether the bubble's tool calls show
// ToolMarker before their name. They show none by default.
func ChatMessageWithToolMarkers(show bool) ChatMessageOption {
	return func(cm *ChatMessageBox) { cm.ToolMarkers = show }
}

// ChatMessageWithShimmer sets the shimmer whose band sweeps across the rows
// of the bubble's tool calls while they execute. Without one they never
// shimmer.
func ChatMessageWithShimmer(s *Shimmer) ChatMessageOption {
	return func(cm *ChatMessageBox) { cm.Shimmer = s }
}

// ChatMessageWithCacheMode sets where the bubble's replies show the cache use
// of their requests. It is CachePerMessage by default.
func ChatMessageWithCacheMode(mode CacheMode) ChatMessageOption {
	return func(cm *ChatMessageBox) { cm.CacheMode = mode }
}

// ChatMessageWithExtensions sets the extensions whose widgets the bubble
// shows.
func ChatMessageWithExtensions(exts []Extension) ChatMessageOption {
	return func(cm *ChatMessageBox) { cm.Extensions = exts }
}

// ChatMessageWithEnqueue sets the function that runs a function on the UI's
// event loop, for the views of the bubble to fetch what they show with. A
// bubble without one, as the session preview's, fetches nothing.
func ChatMessageWithEnqueue(enqueue func(func())) ChatMessageOption {
	return func(cm *ChatMessageBox) { cm.Enqueue = enqueue }
}

// NewChatMessageBox creates a chat message bubble rendering msg.
func NewChatMessageBox(msg ChatMessage, opts ...ChatMessageOption) *ChatMessageBox {
	cm := &ChatMessageBox{
		theme: DefaultTheme(),
		bg:    style.ColorTransparent,
		role:  msg.Role,
	}
	for _, opt := range opts {
		opt(cm)
	}
	cm.fg = RoleFg(cm.theme, msg.Role)
	cm.build(msg)
	return cm
}

// RoleFg returns the prose color for a message role.
func RoleFg(t *style.Theme, role message.Role) style.Color {
	switch role {
	case message.RoleAgent:
		return t.ResolveTextBase()
	case message.RoleSystem:
		return t.TextMuted
	default:
		return t.TextStrong
	}
}

// Filled reports whether the bubble's prose fills a background, as a
// prompt's does, and so shows as blocks that pad themselves.
func (cm *ChatMessageBox) Filled() bool { return !cm.bg.IsTransparent() }

// build assembles the first segment and the bubble container.
func (cm *ChatMessageBox) build(msg ChatMessage) {
	cm.Frame, cm.middleCol = ChatMessageContainer()
	if msg.Role == message.RoleUser {
		cm.TailRow = NewTailRow()
	}

	seg := cm.BuildSegment(msg)
	cm.segments = append(cm.segments, seg)
	cm.middleCol.Add(seg.Box)

	// A bubble keeps its content height. Inline mode parents it to a root of
	// fixed height, where a shrinkable bubble collapses once the transcript
	// fills that height, and its rows then paint over the bubble above.
	cm.Box = widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionRow),
			layout.WithAlignItems(layout.AlignStretch),
			layout.WithFlexShrink(0),
		),
		widget.Children(cm.Frame),
	)
	if msg.Role == message.RoleUser {
		// A prompt pins to the top while the reply under it scrolls, and
		// stays until the next prompt takes its place. Only its text pins:
		// the rest of it, in TailRow, scrolls as any row does.
		cm.Box.LayoutNode().Sticky = layout.StickyTop
	}
	cm.SyncRows()
}

// ChatMessageContainer returns the outer container, which pads its segment
// column with a blank row below it, and the column. Neither fills a
// background. Like every row of the transcript, such as a notice, a bubble
// ends in a blank row that parts it from the row under it, and pads none
// above it.
func ChatMessageContainer() (*widget.Box, *widget.Box) {
	middleCol := widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionColumn),
			layout.WithFlexGrow(1),
			layout.WithAlignItems(layout.AlignStretch),
		),
	)
	outerBox := widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexGrow(1),
			layout.WithFlexDirection(layout.DirectionColumn),
			layout.WithAlignItems(layout.AlignStretch),
			layout.WithPaddingBottom(layout.Points(1)),
		),
		widget.Children(middleCol),
	)
	return outerBox, middleCol
}

// SyncRows spaces the rows of the bubble, as Gap says, and keeps an agent
// bubble from showing blank rows around content it does not have yet, as when
// its turn has only begun. The rows of a segment, as Flow lists them, are
// parted from each other, and the segment from the one above it, by the Gap of
// the rows that meet: so rows stack under rows, across segments too, and
// nothing stacks under a footer. An agent segment with nothing to show takes
// no rows, and a bubble that shows none takes no rows at all, its blank row
// below too. Other roles always show their segments, and the blank row below
// them; a prompt's TailRow shows its blank row only while it shows a part, as
// SyncTail says.
func (cm *ChatMessageBox) SyncRows() {
	agent := cm.role == message.RoleAgent
	shown := 0
	// above is the kind of the last row of the segments shown so far.
	above := KindBlock
	for _, seg := range cm.segments {
		rows := seg.Flow()
		show := !agent || len(rows) > 0
		if seg.Box.Visible() != show {
			seg.Box.SetGone(!show)
		}
		if !show {
			continue
		}
		SpaceRows(rows)
		// A segment that shows no rows, as a notice can be, stands as a block.
		first, last := KindBlock, KindBlock
		if len(rows) > 0 {
			first, last = KindOf(rows[0]), KindOf(rows[len(rows)-1])
		}
		margin := 0
		if shown > 0 {
			margin = Gap(above, first)
		}
		SetEdge(seg.Box, &seg.Box.LayoutNode().MarginTop, margin)
		above = last
		shown++
	}
	if agent {
		SetEdge(cm.Frame, &cm.Frame.LayoutNode().PaddingBottom, min(shown, 1))
	}
	cm.SyncTail()
}

// Flow returns the widgets seg shows in its Box, in display order: the parts
// of its ContentBox that stay with the message's first row, then its Views,
// its cache row, and its footer. A prompt's other parts show in the bubble's
// TailRow, which SyncTail spaces. The slice is a copy.
func (seg *MessageSegment) Flow() []render.Renderable {
	rows := ShownIn(seg.ContentBox.Head)
	for _, v := range seg.Views {
		if v.Visible() {
			rows = append(rows, v)
		}
	}
	if seg.Cache.Visible() {
		rows = append(rows, seg.Cache)
	}
	if seg.Footer.Visible() {
		rows = append(rows, seg.Footer)
	}
	return rows
}

// SetEdge sets edge, a margin or padding of r's layout node, to n rows, and
// lays r out again when that changes it.
func SetEdge(r render.Renderable, edge *layout.Scalar, n int) {
	if rows := layout.Points(n); *edge != rows {
		*edge = rows
		r.LayoutNode().MarkDirty()
		r.RequestRender()
	}
}

// BuildSegment creates the widgets for one message segment.
func (cm *ChatMessageBox) BuildSegment(msg ChatMessage) *MessageSegment {
	contentBox := cm.NewSegmentParts()
	views := cm.NewSegmentViews(msg)
	// The cache row is a box like a tool call's, shown as the bubble shows
	// them, under the views.
	cache := NewCacheBox()
	cm.AdoptBox(cache.PartBox)
	// The turn's time and cache use show in the color of the keys a panel
	// names after its title, as a tool call's status does.
	footer := NewFooterBox(cm.TurnWorked(msg), PanelDimFg)
	// The views and the cache row sit beside ContentBox rather than inside
	// it, so that the content children stay aligned one-to-one with the
	// message's parts.
	children := []render.Renderable{contentBox.Head}
	for _, v := range views {
		children = append(children, v)
	}
	seg := &MessageSegment{
		MsgID:      msg.ID,
		ContentBox: contentBox,
		Views:      views,
		Cache:      cache,
		Footer:     footer,
		Parts:      make(map[string]render.Renderable),
		Usage:      msg.Usage,
		Box: widget.NewBox(
			widget.BoxWithLayout(
				layout.WithFlexDirection(layout.DirectionColumn),
				layout.WithAlignItems(layout.AlignStretch),
			),
			widget.Children(append(children, cache, footer)...),
		),
	}
	for _, part := range msg.Parts {
		sm := cm.NewContentPart(part)
		contentBox.AddPart(part, sm)
		if id := string(part.PartID()); id != "" {
			seg.Parts[id] = sm
		}
	}
	cm.SyncCache(seg)
	return seg
}

// SyncCache shows the cache use of seg's request where CacheMode says, and
// nowhere else: per message in seg's cache row, and per turn in seg's footer,
// as CacheText gives it, which shows once seg ends a turn. Only an agent's
// segments show it. The caller lays the bubble's rows out again with
// SyncRows, since the cache row may now show or hide.
func (cm *ChatMessageBox) SyncCache(seg *MessageSegment) {
	var usage message.Usage
	if cm.role == message.RoleAgent {
		usage = seg.Usage
	}
	seg.Cache.SetUsage(usage)
	seg.Cache.SetShown(cm.CacheMode == CachePerMessage)
	cache := ""
	if cm.CacheMode == CachePerTurn {
		cache = CacheText(usage)
	}
	seg.Footer.SetCache(cache)
}

// SetCacheMode shows the cache use of the bubble's replies, and of those
// added to it later, where mode says.
func (cm *ChatMessageBox) SetCacheMode(mode CacheMode) {
	if mode == cm.CacheMode {
		return
	}
	cm.CacheMode = mode
	for _, seg := range cm.segments {
		cm.SyncCache(seg)
	}
	cm.SyncRows()
}

// TurnWorked returns how long the turn that msg ends took, as its footer
// shows it. It returns zero for a non-agent message so that the footer
// remains hidden.
func (cm *ChatMessageBox) TurnWorked(msg ChatMessage) time.Duration {
	if cm.role != message.RoleAgent {
		return 0
	}
	return msg.Worked
}

// AddSegment appends a message to the bubble, which SyncRows spaces from the
// segments above it.
func (cm *ChatMessageBox) AddSegment(msg ChatMessage) {
	seg := cm.BuildSegment(msg)
	cm.segments = append(cm.segments, seg)
	cm.middleCol.Add(seg.Box)
	cm.SyncRows()
}

// SegmentFor returns the segment for msgID. It returns the last segment when
// the ID is unknown because streaming updates usually target that segment.
func (cm *ChatMessageBox) SegmentFor(msgID message.ID) *MessageSegment {
	for _, seg := range slices.Backward(cm.segments) {
		if seg.MsgID == msgID {
			return seg
		}
	}
	if len(cm.segments) > 0 {
		return cm.segments[len(cm.segments)-1]
	}
	return nil
}

// SetSegmentParts updates the parts for a message. An unknown message ID
// falls back to the last segment.
func (cm *ChatMessageBox) SetSegmentParts(msgID message.ID, parts []message.ContentPart) {
	if seg := cm.SegmentFor(msgID); seg != nil {
		cm.UpdateSegmentContent(seg, parts)
	}
}

// UpsertContentPart updates the widget that seg keys by id, or adds one
// when there is none. An empty id always adds a widget.
func (cm *ChatMessageBox) UpsertContentPart(seg *MessageSegment, id string, part message.ContentPart) {
	if sm, ok := seg.Parts[id]; ok && id != "" {
		UpdateContentPart(sm, part)
		return
	}
	sm := cm.NewContentPart(part)
	seg.ContentBox.AddPart(part, sm)
	if id != "" {
		seg.Parts[id] = sm
	}
}

// codeBlockPaddingY is the number of blank rows above and below a code block
// in a reply, inside its background.
const codeBlockPaddingY = 1

// proseBlockPaddingY is the number of blank rows above and below the prose of
// a Filled bubble, such as a prompt's text, inside its background.
const proseBlockPaddingY = 1

// NewContentPart creates the widget for a content part.
func (cm *ChatMessageBox) NewContentPart(part message.ContentPart) render.Renderable {
	switch p := part.(type) {
	case message.ToolPart:
		box := NewToolBox(p, cm.theme, cm.fg, cm.ToolFormatters()...)
		cm.AdoptBox(box.PartBox)
		return box
	case message.ReasoningPart:
		return NewReasoningBox(p, cm.theme)
	case message.UsagePart:
		box := NewUsageBox(p, cm.theme, cm.fg)
		cm.AdoptBox(box.PartBox)
		return box
	}
	if view := cm.NewPartView(part); view != nil {
		if f, ok := view.(Framed); ok {
			cm.AdoptBox(f.Frame())
		}
		return view
	}
	if _, ok := part.(message.Metadata); ok {
		// Metadata that no extension shows keeps its place among the parts,
		// unseen.
		hidden := widget.NewBox()
		hidden.SetGone(true)
		return hidden
	}
	sm := widget.NewStreamMarkdown(
		widget.StreamMarkdownWithTheme(cm.theme),
		widget.StreamMarkdownWithFg(cm.fg),
		widget.StreamMarkdownWithBg(cm.bg),
		widget.StreamMarkdownWithCodeBlockBg(cm.theme.ResolveSurfaceRaised()),
		// The code sits PaddingX columns inside its background, with a
		// single blank row above and below it.
		widget.StreamMarkdownWithCodeBlockPadding(PaddingX),
		widget.StreamMarkdownWithCodeBlockPaddingY(codeBlockPaddingY),
		// The background is inset by PaddingX on each side, so that its left
		// edge lines up with the prose, and ends after the widest line.
		widget.StreamMarkdownWithCodeBlockMargin(PaddingX),
		widget.StreamMarkdownWithCodeBlockFit(true),
		widget.StreamMarkdownWithProsePadding(PaddingX),
		widget.StreamMarkdownWithLayout(layout.WithWidth(layout.Percent(100))),
	)
	if cm.Filled() {
		// Prose that fills a background is a block, as an expanded tool call
		// is, so that the rows around it, such as the snapshot a prompt ends
		// with, sit on the canvas.
		sm.LayoutNode().PaddingTop = layout.Points(proseBlockPaddingY)
		sm.LayoutNode().PaddingBottom = layout.Points(proseBlockPaddingY)
	}
	sm.SetContent(FormatContentPart(part))
	return sm
}

// UpdateContentPart updates view when it supports the given content part.
func UpdateContentPart(view render.Renderable, part message.ContentPart) {
	switch view := view.(type) {
	case *ToolBox:
		if tool, ok := part.(message.ToolPart); ok {
			view.SetTool(tool)
		}
	case *ReasoningBox:
		if rp, ok := part.(message.ReasoningPart); ok {
			view.SetPart(rp)
		}
	case *UsageBox:
		if up, ok := part.(message.UsagePart); ok {
			view.SetPart(up)
		}
	case PartView:
		view.SetPart(part)
	case *widget.StreamMarkdown:
		view.SetContent(FormatContentPart(part))
	}
}

// UpdateSegmentContent updates the widget of each part, matched by part ID,
// and appends widgets for parts the segment does not show yet.
func (cm *ChatMessageBox) UpdateSegmentContent(seg *MessageSegment, parts []message.ContentPart) {
	for _, part := range parts {
		cm.UpsertContentPart(seg, string(part.PartID()), part)
	}
	cm.SyncRows()
}

// ToolBoxes returns the bubble's tool calls in display order.
func (cm *ChatMessageBox) ToolBoxes() iter.Seq[*ToolBox] {
	return func(yield func(*ToolBox) bool) {
		for _, seg := range cm.segments {
			for _, child := range seg.ContentBox.ChildrenRaw() {
				if box, ok := child.(*ToolBox); ok && !yield(box) {
					return
				}
			}
		}
	}
}

// StyledViews returns the views of extensions in the bubble that follow the
// transcript's ViewStyle, in display order.
func (cm *ChatMessageBox) StyledViews() iter.Seq[StyledView] {
	return func(yield func(StyledView) bool) {
		for _, seg := range cm.segments {
			for _, child := range seg.ContentBox.ChildrenRaw() {
				if v, ok := child.(StyledView); ok && !yield(v) {
					return
				}
			}
			for _, v := range seg.Views {
				if v, ok := v.(StyledView); ok && !yield(v) {
					return
				}
			}
		}
	}
}

// Boxes returns the PartBoxes of the bubble, in display order: those of the
// parts of each step, such as its tool calls, then those of the step's
// Views, such as its changes, and then its cache row.
func (cm *ChatMessageBox) Boxes() iter.Seq[*PartBox] {
	return func(yield func(*PartBox) bool) {
		for _, seg := range cm.segments {
			for _, child := range seg.ContentBox.ChildrenRaw() {
				if f, ok := child.(Framed); ok && !yield(f.Frame()) {
					return
				}
			}
			for _, v := range seg.Views {
				if f, ok := v.(Framed); ok && !yield(f.Frame()) {
					return
				}
			}
			if !yield(seg.Cache.PartBox) {
				return
			}
		}
	}
}

// AdoptBox shows box, the PartBox of one of the bubble's parts or views, as
// the bubble shows the others: with its shimmer, as its tool call style,
// markers, and name color say, expanded as they are, and spaced from the
// rows around it each time its shape changes.
func (cm *ChatMessageBox) AdoptBox(box *PartBox) {
	box.Shimmer = cm.Shimmer
	box.SetStyle(cm.ToolStyle)
	box.SetMarker(cm.ToolMarkers)
	box.SetNameFg(cm.ToolColor.Fg())
	box.SetExpanded(cm.ToolsExpanded)
	// A click that expands the box, or collapses it, changes whether it
	// stacks with the rows around it.
	box.OnShapeChange = cm.SyncRows
}

// TogglesTools reports whether SetToolsExpanded(expanded) would show or hide
// the details of any box shown in the bubble, such as a tool call's or a
// step's changes.
func (cm *ChatMessageBox) TogglesTools(expanded bool) bool {
	for b := range cm.Boxes() {
		if b.Expandable && b.Visible() && b.Expanded != expanded {
			return true
		}
	}
	return false
}

// SetToolsExpanded shows or hides the details of every Expandable box in the
// bubble, such as its tool calls and the changes of its steps, and of those
// added to it later. A box already shown that way is left alone, so that it
// is not laid out and painted again.
func (cm *ChatMessageBox) SetToolsExpanded(expanded bool) {
	cm.ToolsExpanded = expanded
	for b := range cm.Boxes() {
		if b.Expandable && b.Expanded != expanded {
			b.SetExpanded(expanded)
		}
	}
}

// SetToolStyle shows the bubble's collapsed boxes, and those added to it
// later, as ts says, where their Shape follows it, as a tool call's does.
func (cm *ChatMessageBox) SetToolStyle(ts ToolCallStyle) {
	if ts == cm.ToolStyle {
		return
	}
	cm.ToolStyle = ts
	for b := range cm.Boxes() {
		b.SetStyle(ts)
	}
}

// SetToolMarkers shows or hides ToolMarker before the header of the bubble's
// Marked boxes, such as its tool calls, and of those added to it later, as
// show says, and restyles the views that follow the ViewStyle.
func (cm *ChatMessageBox) SetToolMarkers(show bool) {
	if show == cm.ToolMarkers {
		return
	}
	cm.ToolMarkers = show
	for b := range cm.Boxes() {
		b.SetMarker(show)
	}
	cm.RestyleViews()
}

// SetToolColor shows the marker and the name of the bubble's boxes, such as
// its tool calls, and of those added to it later, in the color c names, and
// restyles the views that follow the ViewStyle.
func (cm *ChatMessageBox) SetToolColor(c ToolNameColor) {
	if c == cm.ToolColor {
		return
	}
	cm.ToolColor = c
	for b := range cm.Boxes() {
		b.SetNameFg(c.Fg())
	}
	cm.RestyleViews()
}

// SetDebug shows what helps debug flow in the views of the bubble that
// follow the ViewStyle, such as the snapshot each message ends with, and in
// those added to it later, or hides it, as on says.
func (cm *ChatMessageBox) SetDebug(on bool) {
	if on == cm.Debug {
		return
	}
	cm.Debug = on
	cm.RestyleViews()
	cm.SyncRows()
}

// ViewStyle returns how the bubble shows its parts, which the views of
// extensions follow.
func (cm *ChatMessageBox) ViewStyle() ViewStyle {
	return ViewStyle{ToolMarkers: cm.ToolMarkers, NameFg: cm.ToolColor.Fg(), Debug: cm.Debug}
}

// RestyleViews shows the bubble's StyledViews in its ViewStyle.
func (cm *ChatMessageBox) RestyleViews() {
	st := cm.ViewStyle()
	for v := range cm.StyledViews() {
		v.SetViewStyle(st)
	}
}

// ViewHost returns the bubble as the views of its extensions see it.
func (cm *ChatMessageBox) ViewHost() ViewHost {
	return ViewHost{Theme: cm.theme, Fg: cm.fg, Style: cm.ViewStyle(), Enqueue: cm.Enqueue, Relayout: cm.SyncRows}
}

// NewPartView returns the widget the first of the bubble's extensions that
// shows part makes of it, or nil when none does.
func (cm *ChatMessageBox) NewPartView(part message.ContentPart) PartView {
	for _, e := range cm.Extensions {
		if view := e.NewPartView(part, cm.ViewHost()); view != nil {
			return view
		}
	}
	return nil
}

// ToolFormatters returns the bubble's extensions that describe how tool
// calls show, in order.
func (cm *ChatMessageBox) ToolFormatters() []ToolFormatter {
	var out []ToolFormatter
	for _, e := range cm.Extensions {
		if f, ok := e.(ToolFormatter); ok {
			out = append(out, f)
		}
	}
	return out
}

// NewSegmentViews returns the widgets the bubble's extensions show under the
// parts of msg, in the order of the extensions. A view that shows in a
// PartBox shows as AdoptBox says, so that it starts expanded as the bubble's
// tool calls are.
func (cm *ChatMessageBox) NewSegmentViews(msg ChatMessage) []SegmentView {
	var views []SegmentView
	for _, e := range cm.Extensions {
		v := e.NewSegmentView(msg, cm.ViewHost())
		if v == nil {
			continue
		}
		if f, ok := v.(Framed); ok {
			cm.AdoptBox(f.Frame())
		}
		views = append(views, v)
	}
	return views
}

// SetSegmentMessage shows msg, a new form of a message the bubble shows, in
// the Views of its segment. An unknown message ID falls back to the last
// segment.
func (cm *ChatMessageBox) SetSegmentMessage(msg ChatMessage) {
	if seg := cm.SegmentFor(msg.ID); seg != nil {
		for _, v := range seg.Views {
			v.SetMessage(msg)
		}
	}
}

// SetUsage records the token counts of the request that the segment
// rendering msg shows, and shows its cache use where CacheMode says.
func (cm *ChatMessageBox) SetUsage(msg ChatMessage) {
	if seg := cm.SegmentFor(msg.ID); seg != nil {
		seg.Usage = msg.Usage
		cm.SyncCache(seg)
		cm.SyncRows()
	}
}

// SetTurnEnd shows in the footer of the segment rendering msgID that the
// turn it ends took worked.
func (cm *ChatMessageBox) SetTurnEnd(msgID message.ID, worked time.Duration) {
	if seg := cm.SegmentFor(msgID); seg != nil && cm.role == message.RoleAgent {
		seg.Footer.SetTurnEnd(worked)
		cm.SyncRows()
	}
}
