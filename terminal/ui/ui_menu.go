package ui

import (
	"context"
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
// the session's turns have taken by now, with the box's keys.
func (ui *UI) SyncSlashStatus() {
	if !ui.StatusBoxWanted() {
		return
	}
	line := StatusLine(ui.service.ModelDisplayName(ui.statusModel), ui.statusUsage, ui.service.SessionWorked(), ui.theme)
	ui.statusRow.SetLine(line, ui.statusHeader.Keys.PlainText())
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
	ui.statusRow.SetStatus(ui.statusHeader.Keys.PlainText())
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
// every other menu, with the input below it holding its query, and lists the
// selected session's prompts under the rows, in inline and fullscreen mode
// alike. The transcript stays as it is.
//
// It also refreshes an open list as its search changes, selecting the first
// row. The event's text replaces the rows when there are none.
func (ui *UI) ShowSessionMenu(e EventSessionMenuShow) {
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

// HideSessionMenu closes the session list, its prompt preview, and empties
// its query from the input.
func (ui *UI) HideSessionMenu() {
	ui.promptLoader.Reset()
	ui.sessionPrompts.Set(nil, 0)

	ui.sessionPanel.SetGone(true)
	ui.SyncFooter()
	ui.SetQuery("")
}

// PreviewSelectedSession lists the selected session's prompts under the
// session list, or none when no row is selected.
func (ui *UI) PreviewSelectedSession() {
	item := ui.sessionMenu.SelectedItem()
	if item == nil {
		ui.promptLoader.Stop()
		ui.sessionPrompts.Set(nil, 0)
		return
	}
	ui.PreviewSession(item.Value)
}

// promptLoadLeast is the fewest prompts PreviewSession loads, as on a
// terminal whose size is not known yet.
const promptLoadLeast = 24

// PreviewSession lists the prompts of the session identified by value under
// the session list, as promptLoader loads them off the event loop, with
// LoadingText in their place until they come.
func (ui *UI) PreviewSession(value any) {
	if id, ok := value.(message.SessionID); ok {
		ui.promptLoader.Request(id)
	}
}

// LoadSessionPrompts reads the preview of session id, off the event loop: as
// many of its latest prompts as the terminal has rows for, since the preview
// shows no more, with the rest counted.
func (ui *UI) LoadSessionPrompts(ctx context.Context, id message.SessionID) []UIEvent {
	return ui.service.SessionPrompts(ctx, id, max(ui.renderer.Height(), promptLoadLeast))
}

// promptLeastRows is the fewest rows the prompt preview shows when it shows
// any: its divider and one row of prompts.
const promptLeastRows = 2

// PromptPreview lists the selected session's prompts under the session list:
// a divider, then a row for each prompt, oldest first. On a
// terminal too short for them all it lists the latest that fit, after a count
// of the earlier ones. While they load, it shows LoadingText in their place.
type PromptPreview struct {
	// Box holds the divider and Text. It is gone while no prompt shows.
	Box *widget.Box
	// Text draws the rows of prompts, one line each, cut to fit.
	Text *widget.Text
	// Theme colors the rows.
	Theme *style.Theme
	// Prompts are the latest of the selected session's prompts, oldest
	// first, on one line each.
	Prompts []string
	// Earlier counts the session's prompts before Prompts, which were not
	// loaded.
	Earlier int
	// Loading reports that the selected session's prompts are loading, as
	// SetLoading shows.
	Loading bool
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

// LoadingText takes the place of the prompts while they load.
const LoadingText = "[loading]"

// Set replaces the prompts, and the count of the earlier ones not loaded,
// ends a load SetLoading showed, and redraws the preview.
func (p *PromptPreview) Set(prompts []string, earlier int) {
	p.Prompts, p.Earlier, p.Loading = prompts, earlier, false
	p.Draw()
}

// SetLoading shows LoadingText in place of the prompts, rather than leave
// those of the session selected before, until Set replaces them.
func (p *PromptPreview) SetLoading() {
	p.Prompts, p.Earlier, p.Loading = nil, 0, true
	p.Draw()
}

// Want returns the rows the preview takes without a bound: its divider and
// LoadingText while the prompts load, none without prompts, and otherwise
// its divider, a row for each prompt, and a row for the count of the earlier
// ones when some were not loaded.
func (p *PromptPreview) Want() int {
	if p.Loading {
		return promptLeastRows
	}
	if len(p.Prompts) == 0 {
		return 0
	}
	rows := 1 + len(p.Prompts)
	if p.Earlier > 0 {
		rows++
	}
	return rows
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
	switch {
	case rows > 0 && p.Loading:
		p.Text.SetStyledContent(style.NewStyledText(style.New().WithFg(mutedFg).Func()("  " + LoadingText)))
	case rows > 0:
		p.Text.SetStyledContent(JoinStyledLines(PromptLines(p.Prompts, p.Earlier, rows-1, p.Theme)))
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
// first and follow earlier prompts not given: every prompt when they all fit
// and none came before, and otherwise a count of the earlier prompts, those
// not given among them, such as "[31 earlier]", above the latest that fit
// in the other rows, or the latest alone in a single row. Each prompt follows
// a "User" label in the color of a prompt's marker, as RoleMarkerFg gives it
// and the session tree labels prompts, and every row lines up with the
// session list's text, past its arrow.
func PromptLines(prompts []string, earlier, n int, theme *style.Theme) []style.StyledText {
	if n <= 0 || len(prompts) == 0 {
		return nil
	}
	theme = themeOrDefault(theme)
	fg := func(c style.Color) func(string) style.TextChunk { return style.New().WithFg(c).Func() }
	text, user := fg(theme.TextMuted), fg(RoleMarkerFg(theme, message.RoleUser))
	dim := text

	var lines []style.StyledText
	shown := prompts
	if earlier > 0 || len(prompts) > n {
		if n > 1 {
			n--
			shown = prompts[len(prompts)-min(n, len(prompts)):]
			hidden := earlier + len(prompts) - len(shown)
			lines = append(lines, style.NewStyledText(dim(fmt.Sprintf("  [%d earlier]", hidden))))
		} else {
			shown = prompts[len(prompts)-1:]
		}
	}
	for _, prompt := range shown {
		lines = append(lines, style.NewStyledText(text("  "), user("User "), text(prompt)))
	}
	return lines
}
