package ui

import (
	"iter"
	"maps"
	"slices"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/product"
	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/render"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/textbuf"
	"github.com/unaquatic/fluid/widget"
)

// ResetTranscript clears the active transcript and its widget indexes.
// Inline, the terminal then writes the transcript again in full, as
// ReprintCommitted makes it, in place of rows committed to it.
func (ui *UI) ResetTranscript() {
	ui.ReprintCommitted()
	ui.DropTranscript()
}

// DropTranscript detaches every row of the transcript and drops its widgets
// and its widget indexes.
func (ui *UI) DropTranscript() {
	ui.ClearTranscriptRows()
	ui.messageWidgets = make(map[message.ID]*ChatMessageBox)
	ui.noticeMarkers = nil
	// The rows the blink tracks went with the transcript.
	clear(ui.blink.Rows)
	ui.committed = false
	ui.trimmed = false
}

// LoadTranscript replaces the active transcript with msgs, as
// BuildTranscript builds it.
func (ui *UI) LoadTranscript(msgs []ChatMessage) {
	ui.ResetTranscript()
	ui.BuildTranscript(msgs)
}

// AddMessage appends a message or merges it into its existing parent bubble,
// adding the row the message takes there, if any, under the bubble's last. A
// notice takes a row of its own, as AddNotice adds it.
func (ui *UI) AddMessage(msg ChatMessage) {
	if msg.Notice != nil {
		ui.AddNotice(*msg.Notice)
		return
	}
	if msg.SharesBubble() {
		if cm, ok := ui.messageWidgets[msg.ParentID]; ok {
			if row, above := cm.AddSegment(msg); row != nil {
				ui.InsertTranscriptRow(row, above)
			}
			ui.messageWidgets[msg.ID] = cm
			return
		}
	}

	// The transcript's markers blink while its calls execute, its views
	// fetch what they show, and inline it holds back what may still change.
	cm := NewChatMessageBox(msg, append(ui.ChatMessageOptions(msg.Role),
		ChatMessageWithBlink(&ui.blink), ChatMessageWithEnqueue(ui.renderer.Enqueue),
		ChatMessageWithHolds(ui.inline))...)
	for row := range cm.Rows() {
		ui.AddTranscriptRow(row)
	}
	ui.messageWidgets[msg.ID] = cm
}

// PromptMarkerFg returns the color of the marker that the user's text starts
// with: the bar of a prompt in PromptBar, and the left edge of the input and
// of a queued prompt, as RoleMarkerFg gives it for a prompt.
func (ui *UI) PromptMarkerFg() style.Color { return RoleMarkerFg(ui.theme, message.RoleUser) }

// ChatMessageOptions returns bubble options for role.
func (ui *UI) ChatMessageOptions(role message.Role) []ChatMessageOption {
	// Agent prose sits on the canvas, as does the system message, whose
	// text shows in a box like a tool call's; that of prompts fills a
	// block, but in PromptBar, where a prompt sits on the canvas by a bar.
	bg, bar := ui.BoxBg(), style.ColorTransparent
	switch {
	case role == message.RoleAgent || role == message.RoleSystem:
		bg = style.ColorTransparent
	case role == message.RoleUser && ui.service.state.PromptStyle == PromptBar:
		bg, bar = style.ColorTransparent, ui.PromptMarkerFg()
	}
	return []ChatMessageOption{
		ChatMessageWithTheme(ui.theme),
		ChatMessageWithBg(bg),
		ChatMessageWithBar(bar),
		ChatMessageWithToolsExpanded(ui.service.state.ToolsExpanded),
		ChatMessageWithExpandStyle(ui.service.state.ExpandStyle),
		ChatMessageWithToolMarkers(!ui.service.state.HideToolMarkers),
		ChatMessageWithToolColor(ui.service.state.ToolColor),
		ChatMessageWithMarkerShape(ui.service.state.MarkerShape),
		ChatMessageWithDebug(ui.service.state.Debug),
		ChatMessageWithCacheMode(ui.service.state.CacheMode),
		ChatMessageWithExtensions(ui.extensions),
	}
}

// SetExpandStyle shows every expanded tool call in the transcript as ts says.
// Each of these settings reaches the committed rows of the inline transcript
// as ReprintCommitted writes them again.
func (ui *UI) SetExpandStyle(ts ExpandedStyle) {
	for cm := range ui.Bubbles() {
		cm.SetExpandStyle(ts)
	}
	ui.ReprintCommitted()
}

// SetToolMarkers shows or hides ToolMarker before the name of every tool call
// and every footer in the transcript, and before the
// title of the Usage box, as show says.
func (ui *UI) SetToolMarkers(show bool) {
	for cm := range ui.Bubbles() {
		cm.SetToolMarkers(show)
	}
	ui.ShowStatusMarker()
	ui.ReprintCommitted()
}

// SetMarkerShape shows the marker of every tool call and footer in the
// transcript, of the Usage box, and of the
// indicator, in m.
func (ui *UI) SetMarkerShape(m MarkerShape) {
	for cm := range ui.Bubbles() {
		cm.SetMarkerShape(m)
	}
	if ui.indicatorMarker != nil {
		StyleMarker(ui.indicatorMarker, m, ui.IndicatorMarkerFg())
	}
	for _, marker := range ui.noticeMarkers {
		StyleMarker(marker, m, titleFg)
	}
	ui.RepaintEdgeMarks()
	ui.ShowStatusMarker()
	ui.ReprintCommitted()
}

// SetToolColor shows the marker and the name of every tool call in the
// transcript in the color c names.
func (ui *UI) SetToolColor(c ToolNameColor) {
	for cm := range ui.Bubbles() {
		cm.SetToolColor(c)
	}
	ui.ShowStatusMarker()
	ui.ReprintCommitted()
}

// ShowStatusMarker shows the Usage box's row with the tool marker, while
// /settings shows it, and in the tool name color, as a row with nothing to
// expand shows them, whether tool calls are expanded or not.
func (ui *UI) ShowStatusMarker() {
	if ui.statusHeader == nil {
		return // The UI was built without its input box.
	}
	st := ui.service.state
	ui.statusRow.SetMarker(!st.HideToolMarkers)
	ui.statusRow.SetNameFg(st.ToolColor.Fg(ui.theme))
	ui.statusRow.SetMarkerShape(st.MarkerShape)
	ui.SyncSlashStatus()
}

// SetDebug shows what helps debug flow in the transcript, such as the snapshot
// every prompt and reply ends with, or hides it, as on says.
func (ui *UI) SetDebug(on bool) {
	for cm := range ui.Bubbles() {
		cm.SetDebug(on)
	}
	ui.ReprintCommitted()
}

// SetCacheMode shows the cache use of every reply in the transcript where mode
// says.
func (ui *UI) SetCacheMode(mode CacheMode) {
	for cm := range ui.Bubbles() {
		cm.SetCacheMode(mode)
	}
	ui.ReprintCommitted()
}

// SetToolsExpanded shows or hides the details of every tool call and of the
// changes of every step in the transcript, and of
// those added later.
func (ui *UI) SetToolsExpanded(expanded bool) {
	for cm := range ui.Bubbles() {
		cm.SetToolsExpanded(expanded)
	}
	ui.ReprintCommitted()
}

// TogglesTools reports whether SetToolsExpanded(expanded) would show or hide
// the details of any tool call or changes shown in the transcript.
func (ui *UI) TogglesTools(expanded bool) bool {
	for cm := range ui.Bubbles() {
		if cm.TogglesTools(expanded) {
			return true
		}
	}
	return false
}

// Bubbles yields every bubble in the transcript. A bubble is yielded once
// for each of its messages.
func (ui *UI) Bubbles() iter.Seq[*ChatMessageBox] {
	return maps.Values(ui.messageWidgets)
}

// UpdateMessage applies the latest parts, which of them are live, and usage,
// shows the message in the views extensions show under it, and appends any
// error as an ErrorPart, which reads as the indicator's error does. A zero
// Usage leaves the usage shown unchanged, since only the end of a message
// carries it.
func (ui *UI) UpdateMessage(msg ChatMessage) {
	cm, ok := ui.messageWidgets[msg.ID]
	if !ok {
		return
	}
	parts := msg.Parts
	if msg.Error != nil {
		parts = append(slices.Clip(parts), ErrorPart{Err: msg.Error})
	}
	cm.SetSegmentParts(msg.ID, parts)
	cm.SetSegmentLive(msg.ID, msg.Live)
	cm.SetSegmentMessage(msg)
	if msg.Usage != (message.Usage{}) {
		cm.SetUsage(msg)
	}
}

// SetTurnEnd shows under reply msgID that the turn it ends took worked.
func (ui *UI) SetTurnEnd(msgID message.ID, worked time.Duration) {
	if cm, ok := ui.messageWidgets[msgID]; ok {
		cm.SetTurnEnd(msgID, worked)
	}
}

// AddNotice adds notices to the transcript, under the rows already there, in
// the rows NewNoticeRow makes, as a message's rows are added.
func (ui *UI) AddNotice(notices ...Notice) {
	for _, n := range notices {
		if row := ui.NewNoticeRow(n); row != nil {
			ui.AddTranscriptRow(row)
		}
	}
}

// NewNoticeRow returns the row that shows n, followed by a blank row, or nil
// for a notice that shows nothing. Its rows are on the canvas, from its left
// edge, or else in a box filled with its surface, padded by a row above and
// below and by PaddingX at the sides, in textFg where they have no color of
// their own. While prompts show in PromptBar, a notice that would show in a
// box shows as a prompt does instead, as NewNoticeBarRow makes it.
func (ui *UI) NewNoticeRow(n Notice) *widget.Box {
	lines := n.Lines
	if len(lines) == 0 && n.Text != "" {
		lines = []style.StyledText{style.NewStyledText(style.Plain(n.Text))}
	}
	content := JoinStyledLines(lines)
	if len(lines) == 0 || (n.Surface != NoticeCanvas && len(content.Chunks) == 0) {
		return nil
	}
	id := string(message.NewID())
	if n.Surface == NoticeCanvas {
		return widget.NewBox(
			widget.BoxWithID(id),
			widget.BoxWithLayout(layout.WithFlexShrink(0), layout.WithMarginBottom(layout.Points(1))),
			widget.Children(widget.NewStyledText(content, widget.TextWithID(id+"-text"))),
		)
	}
	if ui.service.state.PromptStyle == PromptBar {
		return ui.NewNoticeBarRow(id, n, lines)
	}
	bg := ui.theme.SurfaceRaised
	if n.Surface == NoticeSystem {
		bg = ui.SystemBg()
	}
	return widget.NewBox(
		widget.BoxWithID(id),
		widget.BoxWithLayout(
			layout.WithFlexShrink(0),
			layout.WithPadding(layout.Points(1)),
			layout.WithPaddingLeft(layout.Points(PaddingX)),
			layout.WithPaddingRight(layout.Points(PaddingX)),
			layout.WithMarginBottom(layout.Points(1)),
		),
		widget.BoxWithBg(bg),
		widget.Children(widget.NewStyledText(content, widget.TextWithID(id+"-text"), widget.TextWithFg(textFg))),
	)
}

// NewNoticeBarRow returns the row, with id, that shows lines, those of n, as
// PromptBar shows a prompt: on the canvas, with no padding above or below,
// inset by PaddingX at the sides like the prose. Its header starts with a
// marker in its left padding, in the marker shape /settings sets and in
// titleFg, the color of the header it heads, and then n's Title, or without
// one the first of lines.
// The other lines follow under the header, unmarked. A blank row follows
// it.
func (ui *UI) NewNoticeBarRow(id string, n Notice, lines []style.StyledText) *widget.Box {
	st := ui.service.state
	header, body := style.NewStyledText(style.Plain(n.Title)), lines
	if n.Title == "" {
		header, body = lines[0], lines[1:]
	}
	marker := NewMarker(st.MarkerShape, titleFg)
	ui.noticeMarkers = append(ui.noticeMarkers, marker)
	// The marker fills the left padding with the gap after it, so that the
	// header starts where the lines under it do.
	headerRow := widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionRow),
			layout.WithGap(PaddingX-1),
			layout.WithPaddingRight(layout.Points(PaddingX)),
		),
		widget.Children(marker, widget.NewStyledText(header,
			widget.TextWithID(id+"-title"),
			widget.TextWithFg(textFg),
			widget.TextWithLayout(layout.WithFlexGrow(1), layout.WithFlexShrink(1)),
		)),
	)
	children := []render.Renderable{headerRow}
	if len(body) > 0 {
		children = append(children, widget.NewBox(
			widget.BoxWithLayout(
				layout.WithPaddingLeft(layout.Points(PaddingX)),
				layout.WithPaddingRight(layout.Points(PaddingX)),
			),
			widget.Children(widget.NewStyledText(JoinStyledLines(body), widget.TextWithID(id+"-text"), widget.TextWithFg(textFg))),
		))
	}
	return widget.NewBox(
		widget.BoxWithID(id),
		widget.BoxWithLayout(
			layout.WithFlexShrink(0),
			layout.WithFlexDirection(layout.DirectionColumn),
			layout.WithAlignItems(layout.AlignStretch),
			layout.WithMarginBottom(layout.Points(1)),
		),
		widget.Children(children...),
	)
}

// productFg is the product's primary color, product.PrimaryColor, as the
// resume command printed on exit draws the product name. It marks the product
// name in the resume command, the keyword of a notice, such as the model it
// names, and the selected row of a menu.
var productFg = style.ColorFromHex(product.PrimaryColor)

// logoInitialFg is the orange of the "f" of the logo, product.LogoInitialColor,
// as --version and the README logos draw it. It is productFg.
var logoInitialFg = style.ColorFromHex(product.LogoInitialColor)

// LogoLines yields the lines of the product logo, the "f" in logoInitialFg
// and the rest in textFg.
func LogoLines() iter.Seq[style.StyledText] { return LogoLinesIn(logoInitialFg) }

// LogoLinesIn yields the lines of the product logo, the "f" in c and the rest
// in textFg.
func LogoLinesIn(c style.Color) iter.Seq[style.StyledText] {
	initial := style.New().WithFg(c).Func()
	rest := style.New().WithFg(textFg).Func()
	return func(yield func(style.StyledText) bool) {
		for f, r := range product.LogoParts() {
			if !yield(style.NewStyledText(initial(f), rest(r))) {
				return
			}
		}
	}
}

// ResumeIndent is the number of columns the resume command is indented by,
// under the line that introduces it, in the notice inline mode leaves and in
// the lines printed after fullscreen mode.
const ResumeIndent = 2

// ResumeIntro returns the line that introduces the command that resumes a
// session, bold, in fg.
func ResumeIntro(fg style.Color) style.StyledText {
	return style.NewStyledText(style.New().WithFg(fg).Bold().Func()("Resume this session with:"))
}

// ResumeCommand returns the command that resumes session id: the product
// name bold in productFg, then the flag that names id, in fg.
func ResumeCommand(id message.SessionID, fg style.Color) style.StyledText {
	return style.NewStyledText(
		style.New().WithFg(productFg).Bold().Func()(product.Name),
		style.New().WithFg(fg).Func()(" --session "+string(id)),
	)
}

// NewResumeNotice returns the notice that tells how to resume session id, on
// the canvas rather than in a box, from the left edge of the terminal, with a
// blank row under it: the product logo, then after a blank row ResumeIntro,
// and right under it ResumeCommand, indented by ResumeIndent, both in the
// theme's text color. terminal.ResumeText is the notice as text to print.
func NewResumeNotice(id message.SessionID, theme *style.Theme) *widget.Box {
	theme = themeOrDefault(theme)
	fg := theme.ResolveTextBase()
	logo := widget.NewBox(
		widget.BoxWithLayout(layout.WithMarginBottom(layout.Points(1))),
		widget.Children(widget.NewStyledText(JoinStyledLines(slices.Collect(LogoLines())))),
	)
	intro := widget.NewStyledText(ResumeIntro(fg))
	command := widget.NewStyledText(ResumeCommand(id, fg), widget.TextWithWrapMode(textbuf.WrapWord))
	return widget.NewBox(
		widget.BoxWithID("resume-notice"),
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionColumn),
			layout.WithAlignItems(layout.AlignStretch),
			layout.WithFlexShrink(0),
			layout.WithMarginBottom(layout.Points(1)),
		),
		widget.Children(logo, intro, widget.NewBox(
			widget.BoxWithLayout(layout.WithPaddingLeft(layout.Points(ResumeIndent))),
			widget.Children(command),
		)),
	)
}

// AppendResumeNotice adds the notice that tells how to resume session id
// at the very end, under the indicator too, as the last thing the final
// frame shows.
func (ui *UI) AppendResumeNotice(id message.SessionID) {
	ui.renderer.Root().Add(NewResumeNotice(id, ui.theme))
}
