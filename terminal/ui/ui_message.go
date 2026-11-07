package ui

import (
	"iter"
	"maps"
	"slices"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/product"
	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/textbuf"
	"github.com/unaquatic/fluid/widget"
)

// ResetTranscript clears the active transcript and its widget indexes.
func (ui *UI) ResetTranscript() {
	ui.ClearTranscriptRows()
	ui.messageWidgets = make(map[message.ID]*ChatMessageBox)
	// The rows the shimmer tracks went with the transcript.
	clear(ui.shimmer.Rows)
}

// LoadTranscript replaces the active transcript with msgs.
func (ui *UI) LoadTranscript(msgs []ChatMessage) {
	ui.ResetTranscript()
	for _, msg := range msgs {
		ui.AddMessage(msg)
	}
}

// AddMessage appends a message or merges it into its existing parent bubble.
func (ui *UI) AddMessage(msg ChatMessage) {
	if msg.SharesBubble() {
		if cm, ok := ui.messageWidgets[msg.ParentID]; ok {
			cm.AddSegment(msg)
			ui.messageWidgets[msg.ID] = cm
			return
		}
	}

	// The transcript's calls shimmer while they execute, and its views fetch
	// what they show, unlike those of the session preview, which shows
	// another session as it was stored.
	cm := NewChatMessageBox(msg, append(ui.ChatMessageOptions(msg.Role),
		ChatMessageWithShimmer(&ui.shimmer), ChatMessageWithEnqueue(ui.renderer.Enqueue))...)
	for row := range cm.Rows() {
		ui.AddTranscriptRow(row)
	}
	ui.messageWidgets[msg.ID] = cm
}

// ChatMessageOptions returns bubble options for role.
func (ui *UI) ChatMessageOptions(role message.Role) []ChatMessageOption {
	// Agent prose sits on the canvas; that of other roles fills a block.
	bg := ui.BoxBg()
	if role == message.RoleAgent {
		bg = style.ColorTransparent
	}
	return []ChatMessageOption{
		ChatMessageWithTheme(ui.theme),
		ChatMessageWithBg(bg),
		ChatMessageWithToolsExpanded(ui.service.state.ToolsExpanded),
		ChatMessageWithToolStyle(ui.service.state.ToolStyle),
		ChatMessageWithToolMarkers(ui.service.state.ToolMarkers),
		ChatMessageWithToolColor(ui.service.state.ToolColor),
		ChatMessageWithDebug(ui.service.state.Debug),
		ChatMessageWithCacheMode(ui.service.state.CacheMode),
		ChatMessageWithExtensions(ui.extensions),
	}
}

// SetToolStyle shows every collapsed tool call in the transcript and the
// session preview as ts says.
func (ui *UI) SetToolStyle(ts ToolCallStyle) {
	for cm := range ui.Bubbles() {
		cm.SetToolStyle(ts)
	}
}

// SetToolMarkers shows or hides ToolMarker before the name of every tool call
// in the transcript and the session preview, as show says.
func (ui *UI) SetToolMarkers(show bool) {
	for cm := range ui.Bubbles() {
		cm.SetToolMarkers(show)
	}
}

// SetToolColor shows the marker and the name of every tool call in the
// transcript and the session preview in the color c names.
func (ui *UI) SetToolColor(c ToolNameColor) {
	for cm := range ui.Bubbles() {
		cm.SetToolColor(c)
	}
}

// SetDebug shows what helps debug flow in the transcript and the session
// preview, such as the snapshot every prompt and reply ends with, or hides
// it, as on says.
func (ui *UI) SetDebug(on bool) {
	for cm := range ui.Bubbles() {
		cm.SetDebug(on)
	}
}

// SetCacheMode shows the cache use of every reply in the transcript and the
// session preview where mode says.
func (ui *UI) SetCacheMode(mode CacheMode) {
	for cm := range ui.Bubbles() {
		cm.SetCacheMode(mode)
	}
}

// SetToolsExpanded shows or hides the details of every tool call and of the
// changes of every step in the transcript and the session preview, and of
// those added later.
func (ui *UI) SetToolsExpanded(expanded bool) {
	for cm := range ui.Bubbles() {
		cm.SetToolsExpanded(expanded)
	}
}

// TogglesTools reports whether SetToolsExpanded(expanded) would show or hide
// the details of any tool call or changes shown in the transcript or the
// session preview.
func (ui *UI) TogglesTools(expanded bool) bool {
	for cm := range ui.Bubbles() {
		if cm.TogglesTools(expanded) {
			return true
		}
	}
	return false
}

// Bubbles yields every bubble in the transcript and the session preview. A
// bubble is yielded once for each of its messages.
func (ui *UI) Bubbles() iter.Seq[*ChatMessageBox] {
	return func(yield func(*ChatMessageBox) bool) {
		for _, widgets := range []map[message.ID]*ChatMessageBox{ui.messageWidgets, ui.previewWidgets} {
			for cm := range maps.Values(widgets) {
				if !yield(cm) {
					return
				}
			}
		}
	}
}

// UpdateMessage applies the latest parts and usage, shows the message in the
// views extensions show under it, and appends any error as text. A zero
// Usage leaves the usage shown unchanged, since only the end of a message
// carries it.
func (ui *UI) UpdateMessage(msg ChatMessage) {
	cm, ok := ui.messageWidgets[msg.ID]
	if !ok {
		return
	}
	parts := msg.Parts
	if msg.Error != nil {
		errText := ErrorHeader(msg.Error) + "\n" + ErrorDetail(msg.Error)
		parts = append(parts, message.TextPart{Text: errText})
	}
	cm.SetSegmentParts(msg.ID, parts)
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

// AppendNoticeOn adds a row filled with bg to the transcript, padded by a row
// above and below and by PaddingX at the sides, followed by a blank row. Text
// in content without a color of its own is in textFg.
func (ui *UI) AppendNoticeOn(content style.StyledText, bg style.Color) {
	if len(content.Chunks) == 0 {
		return
	}
	id := string(message.NewID())
	text := widget.NewStyledText(content, widget.TextWithID(id+"-text"), widget.TextWithFg(textFg))
	ui.AddTranscriptRow(widget.NewBox(
		widget.BoxWithID(id),
		widget.BoxWithLayout(
			layout.WithFlexShrink(0),
			layout.WithPadding(layout.Points(1)),
			layout.WithPaddingLeft(layout.Points(PaddingX)),
			layout.WithPaddingRight(layout.Points(PaddingX)),
			layout.WithMarginBottom(layout.Points(1)),
		),
		widget.BoxWithBg(bg),
		widget.Children(text),
	))
}

// AppendCanvasRows adds lines to the transcript on the canvas, from its left
// edge, with no box around them and no inset, followed by a blank row, as a
// notice is.
func (ui *UI) AppendCanvasRows(lines []style.StyledText) {
	if len(lines) == 0 {
		return
	}
	id := string(message.NewID())
	ui.AddTranscriptRow(widget.NewBox(
		widget.BoxWithID(id),
		widget.BoxWithLayout(layout.WithFlexShrink(0), layout.WithMarginBottom(layout.Points(1))),
		widget.Children(widget.NewStyledText(JoinStyledLines(lines), widget.TextWithID(id+"-text"))),
	))
}

// productFg is the product's primary color, product.PrimaryColor, as the
// resume command printed on exit draws the product name. It marks the product
// name in the resume command, the keyword of a notice, such as the model it
// names, and the selected row of a menu.
var productFg = style.ColorFromHex(product.PrimaryColor)

// logoInitialFg is the orange of the "f" of the logo, product.LogoInitialColor,
// as --version and the README logos draw it. It is productFg.
var logoInitialFg = style.ColorFromHex(product.LogoInitialColor)

// logoFg is the light grey of the logo after its "f", as --version draws it.
var logoFg = style.ColorFromHex("#e4e4e4")

// LogoLines yields the lines of the product logo, the "f" in logoInitialFg
// and the rest in logoFg.
func LogoLines() iter.Seq[style.StyledText] { return LogoLinesIn(logoInitialFg) }

// LogoLinesIn yields the lines of the product logo, the "f" in c and the rest
// in logoFg.
func LogoLinesIn(c style.Color) iter.Seq[style.StyledText] {
	initial := style.New().WithFg(c).Func()
	rest := style.New().WithFg(logoFg).Func()
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

// AppendToast adds a notice to the transcript, on the system surface when it
// is a system notice. Lines takes precedence over Text.
func (ui *UI) AppendToast(e EventToastShow) {
	bg := ui.theme.SurfaceRaised
	if e.System {
		bg = ui.SystemBg()
	}
	switch {
	case len(e.Lines) > 0:
		ui.AppendNoticeOn(JoinStyledLines(e.Lines), bg)
	case e.Text != "":
		ui.AppendNoticeOn(style.NewStyledText(style.Plain(e.Text)), bg)
	}
}

// ResetPreview clears the session preview and its widget index, and the
// prompts previewed in inline mode.
func (ui *UI) ResetPreview() {
	for _, child := range ui.previewMessageBox.Children() {
		ui.previewMessageBox.Remove(child.ID())
	}
	ui.previewWidgets = make(map[message.ID]*ChatMessageBox)
	ui.sessionPrompts.Set(nil)
}

// LoadPreview replaces the session preview with msgs.
func (ui *UI) LoadPreview(msgs []ChatMessage) {
	ui.ResetPreview()
	for _, msg := range msgs {
		ui.AddPreviewMessage(msg)
	}
}

// AddPreviewMessage appends or merges a message in the session preview.
func (ui *UI) AddPreviewMessage(msg ChatMessage) {
	if msg.SharesBubble() {
		if cm, ok := ui.previewWidgets[msg.ParentID]; ok {
			cm.AddSegment(msg)
			ui.previewWidgets[msg.ID] = cm
			return
		}
	}

	cm := NewChatMessageBox(msg, ui.ChatMessageOptions(msg.Role)...)
	for row := range cm.Rows() {
		ui.previewMessageBox.Add(row)
	}
	ui.previewWidgets[msg.ID] = cm
}
