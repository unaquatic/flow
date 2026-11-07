package ui

import (
	"fmt"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/widget"
)

// sessionMaxRows is the number of sessions the session list shows before it
// scrolls.
const sessionMaxRows = 8

// slashMaxRows is the number of rows the slash menu shows before it scrolls,
// the gaps between groups among them. Its count shows the selected command's
// place under it meanwhile.
const slashMaxRows = 5

// SlashHints returns the slash menu's keys: esc, which closes it, the arrows
// that move through it, and tab, which completes the selected command.
func SlashHints() []KeyHint {
	return []KeyHint{HintCancel, HintMove, HintComplete}
}

// ShowSlashMenu shows matching commands at the top of the input box, above
// the input text, the status box above the input box, and the menu's key
// hints under it.
func (ui *UI) ShowSlashMenu(items []widget.SelectItem) {
	ui.slashMenu.SetItems(items)
	ui.slashMenu.SetSelectedIndex(0)
	ui.slashMenu.SetHeight(layout.Points(ui.SlashRows()))

	if ui.slashMenuRow.Parent() == nil {
		// Queued prompts insert above the input text, so they stay below the
		// menu.
		ui.inputMiddleCol.InsertBefore(ui.slashMenuRow, ui.inputMiddleCol.Children()[0])
	}
	ui.SyncSlashStatus()
	ui.SyncFooter()
}

// SyncSlashStatus brings the status line up to date while the Usage box
// shows, as StatusBoxWanted reports: the model, the token use, and how long
// the session's turns have taken by now.
func (ui *UI) SyncSlashStatus() {
	if !ui.StatusBoxWanted() {
		return
	}
	ui.slashStatus.SetStyledContent(StatusLine(ui.statusModel, ui.statusUsage, ui.service.SessionWorked(), ui.theme))
}

// SlashMenuOnly reports whether the slash menu is the panel open at the top
// of the input box.
func (ui *UI) SlashMenuOnly() bool {
	if ui.treePanel.Visible() || ui.sessionPanel.Visible() || ui.bannerPanel.Visible() {
		return false
	}
	return ui.slashMenuRow.Parent() != nil
}

// UsageHints returns the Usage box's keys: ctrl+u, which pins the box so
// that it shows while the slash menu is closed too, or unpins it once pinned.
func UsageHints(pinned bool) []KeyHint {
	if pinned {
		return []KeyHint{{Key: "ctrl+u", Action: "unpin"}}
	}
	return []KeyHint{{Key: "ctrl+u", Action: "pin"}}
}

// UsageFooterHints returns UsageHints as the footer names them, away from
// the box they act on: "pin usage", or "unpin usage" once pinned.
func UsageFooterHints(pinned bool) []KeyHint {
	hints := UsageHints(pinned)
	for i := range hints {
		hints[i].Action += " usage"
	}
	return hints
}

// StatusBoxWanted reports whether the Usage box belongs above the input box:
// while it is pinned, and while the slash menu is the panel open in the box.
func (ui *UI) StatusBoxWanted() bool {
	return ui.service.state.UsagePinned || ui.SlashMenuOnly()
}

// UsageKeys returns the keys the Usage box names: UsageHints while the
// slash menu is the panel open in the input box, and none otherwise, as
// while the pinned box shows with the menu closed.
func (ui *UI) UsageKeys() []KeyHint {
	if !ui.SlashMenuOnly() {
		return nil
	}
	return UsageHints(ui.service.state.UsagePinned)
}

// SyncStatusBox shows the Usage box above the input box while
// StatusBoxWanted reports so, unless a short terminal leaves the box no room,
// as StatusPart bounds it, and hides it otherwise. It names the box's keys,
// as UsageKeys gives them.
func (ui *UI) SyncStatusBox() {
	ui.SetHeaderKeys(ui.statusHeader, ui.UsageKeys())
	if shown := ui.StatusBoxWanted() && !ui.statusBounded; ui.slashStatusBox.Visible() != shown {
		ui.slashStatusBox.SetGone(!shown)
	}
}

// SlashRows returns the number of rows the slash menu shows: every match up
// to slashMaxRows, within the input box's bound.
func (ui *UI) SlashRows() int {
	return ui.MenuRows(min(ui.slashMenu.ItemCount(), slashMaxRows))
}

// HideSlashMenu removes the command menu, unless the menu has already
// reopened.
func (ui *UI) HideSlashMenu() {
	if ui.service.state.SlashMenuVisible {
		return
	}
	if ui.slashMenuRow.Parent() == nil {
		return
	}
	ui.inputMiddleCol.Remove(ui.slashMenuRow.ID())
	ui.SyncFooter()
}

// ShowSessionMenu opens the session list at the top of the input box, like
// every other menu, with the input below it holding its query. In fullscreen
// mode a preview of the selected session takes the transcript's place above
// it. Inline mode leaves the transcript as it is: its rows are the terminal's
// own lines, and hiding them would collapse the screen. It lists the selected
// session's prompts under the rows instead.
//
// It also refreshes an open list as its search changes, selecting the first
// row. The event's text replaces the rows when there are none.
func (ui *UI) ShowSessionMenu(e EventSessionMenuShow) {
	if !ui.inline {
		ui.messageBox.SetGone(true)
		ui.previewMessageBox.SetGone(false)
	}

	ui.sessionMenu.SetItems(e.Items)
	// Set the viewport before selecting a row so scrolling can be calculated.
	ui.sessionMenu.SetHeight(layout.Points(ui.SessionRows()))
	if len(e.Items) > 0 {
		ui.sessionMenu.SetSelectedIndex(0)
		ui.sessionMenu.SetScrollOffset(0)
	}
	ui.SyncSessionCount()
	ui.sessionText.SetContent(e.Text)
	ui.sessionTextRow.SetGone(e.Text == "")

	ui.sessionPanel.SetGone(false)
	ui.SyncFooter()
	ui.SetQuery(e.Query)
	ui.renderer.FocusRenderable(ui.input)

	ui.PreviewSelectedSession()
}

// SessionRows returns the number of sessions the list shows: every row up to
// sessionMaxRows, within the input box's bound.
func (ui *UI) SessionRows() int {
	return ui.MenuRows(min(ui.sessionMenu.ItemCount(), sessionMaxRows))
}

// SyncSessionCount shows the selected session's place in the list, such as
// "[3/20]", where CountPlace says, while the rows do not all fit.
func (ui *UI) SyncSessionCount() {
	ui.ShowCount(ui.sessionHeader, ui.sessionCount, TitleCount(ui.sessionMenu.SelectedIndex(), ui.sessionMenu.ItemCount(), ui.SessionRows()))
}

// HideSessionMenu closes the session list, empties its query from the input,
// and in fullscreen mode puts the transcript back in place of the preview.
func (ui *UI) HideSessionMenu() {
	ui.previewMessageBox.SetGone(true)
	// Inline mode never shows the scroll container: its rows are the root's.
	ui.messageBox.SetGone(ui.inline)
	ui.ResetPreview()

	ui.sessionPanel.SetGone(true)
	ui.SyncFooter()
	ui.SetQuery("")
}

// PreviewSelectedSession loads the selected session into the preview pane, or
// empties the pane when no row is selected.
func (ui *UI) PreviewSelectedSession() {
	item := ui.sessionMenu.SelectedItem()
	if item == nil {
		ui.ResetPreview()
		return
	}
	ui.PreviewSession(item.Value)
}

// PreviewSession previews the session identified by value: fullscreen mode
// loads its transcript into the preview pane, and inline mode, which leaves
// the transcript on screen, lists its prompts under the session list.
func (ui *UI) PreviewSession(value any) {
	id, ok := value.(message.SessionID)
	switch {
	case !ok:
		return
	case ui.inline:
		ui.Apply(ui.service.SessionPrompts(id))
	default:
		ui.Apply(ui.service.PreviewSession(id))
	}
}

// promptLeastRows is the fewest rows the prompt preview shows when it shows
// any: its divider and one row of prompts.
const promptLeastRows = 2

// PromptPreview lists the selected session's prompts under the session list
// in inline mode: a divider, then a row for each prompt, oldest first. On a
// terminal too short for them all it lists the latest that fit, after a count
// of the earlier ones.
type PromptPreview struct {
	// Box holds the divider and Text. It is gone while no prompt shows.
	Box *widget.Box
	// Text draws the rows of prompts, one line each, cut to fit.
	Text *widget.Text
	// Theme colors the rows.
	Theme *style.Theme
	// Prompts are the selected session's prompts, oldest first, on one line
	// each.
	Prompts []string
	// Rows is the most rows Box takes, its divider included, while Bounded
	// holds, so that the input box fits the terminal.
	Rows int
	// Bounded reports that Rows bounds the preview.
	Bounded bool
}

// NewPromptPreview returns an empty prompt preview drawn on bg.
func NewPromptPreview(borderFg, bg style.Color, theme *style.Theme) *PromptPreview {
	p := &PromptPreview{
		Box: NewInputPanel("session-prompts"),
		Text: widget.NewStyledText(style.StyledText{},
			widget.TextWithID("session-prompts-text"),
			widget.TextWithTruncate(true),
		),
		Theme: theme,
	}
	AddDividerRow(p.Box, borderFg, bg)
	// From the left edge, as the session list is, so that the rows, past
	// the arrow's two cells, line up with its text.
	AddArrowRow(p.Box, p.Text, borderFg, bg)
	return p
}

// Set replaces the prompts and redraws the preview.
func (p *PromptPreview) Set(prompts []string) {
	p.Prompts = prompts
	p.Draw()
}

// Want returns the rows the preview takes without a bound: none without
// prompts, and otherwise its divider and a row for each prompt.
func (p *PromptPreview) Want() int {
	if len(p.Prompts) == 0 {
		return 0
	}
	return 1 + len(p.Prompts)
}

// Shown returns the rows the preview takes within its bound: none when the
// bound leaves fewer than promptLeastRows.
func (p *PromptPreview) Shown() int {
	rows := p.Want()
	if p.Bounded {
		rows = min(rows, p.Rows)
	}
	if rows < promptLeastRows {
		return 0
	}
	return rows
}

// Draw fills the preview's rows within its bound, hiding it when it shows
// none.
func (p *PromptPreview) Draw() {
	rows := p.Shown()
	p.Box.SetGone(rows == 0)
	if rows > 0 {
		p.Text.SetStyledContent(JoinStyledLines(PromptLines(p.Prompts, rows-1, p.Theme)))
	}
}

// Part returns the preview as a part of the input box that shows fewer rows
// on a short terminal, and false when it has no prompts to show.
func (p *PromptPreview) Part() (FitPart, bool) {
	want := p.Want()
	if want == 0 {
		return FitPart{}, false
	}
	return FitPart{
		Rows:  p.Shown(),
		Want:  want,
		Least: promptLeastRows,
		Bound: func(rows int) {
			p.Rows, p.Bounded = rows, rows < want
			p.Draw()
		},
	}, true
}

// PromptLines returns at most n rows previewing prompts, which are oldest
// first: every prompt when they all fit, and otherwise a count of the earlier
// prompts, such as "[31 earlier]", above the latest n-1, or the latest alone
// in a single row. Each
// prompt follows a colored "user:" label, as in the session tree, and every
// row lines up with the session list's text, past its arrow.
func PromptLines(prompts []string, n int, theme *style.Theme) []style.StyledText {
	if n <= 0 || len(prompts) == 0 {
		return nil
	}
	theme = themeOrDefault(theme)
	fg := func(c style.Color) func(string) style.TextChunk { return style.New().WithFg(c).Func() }
	text, dim, accent := fg(panelTextFg), fg(PanelDimFg), fg(theme.Accent)

	var lines []style.StyledText
	shown := prompts
	if len(prompts) > n {
		if n > 1 {
			earlier := len(prompts) - (n - 1)
			lines = append(lines, style.NewStyledText(dim(fmt.Sprintf("  [%d earlier]", earlier))))
			n--
		}
		shown = prompts[len(prompts)-n:]
	}
	for _, prompt := range shown {
		lines = append(lines, style.NewStyledText(text("  "), accent("user: "), text(prompt)))
	}
	return lines
}
