package ui

import (
	"encoding/base64"
	"time"

	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/input"
)

// HandleKey routes keyboard input to the active terminal surface. Ctrl+U
// pins or unpins the Usage box, ahead of the input, which would take it to
// delete to the start of its line, and Ctrl+O expands or collapses tool
// calls and step changes, whichever surface is active, except the session
// tree, which keeps Ctrl+O for its filter.
func (ui *UI) HandleKey(k *input.KeyEvent) {
	if k.Mods.Has(input.ModCtrl) && k.Name == "c" {
		handled, events := ui.service.HandleCtrlC(ui.input.Text() != "")
		ui.Apply(events)
		if handled {
			k.PreventDefault()
			return
		}
	}
	if k.Mods.Has(input.ModCtrl) && k.Name == "u" {
		ui.Apply(ui.service.HandleCtrlU())
		k.PreventDefault()
		return
	}
	if k.Mods.Has(input.ModCtrl) && k.Name == "o" {
		if handled, events := ui.service.HandleCtrlO(); handled {
			ui.Apply(events)
			k.PreventDefault()
			return
		}
	}
	if k.Name == input.KeyEnter {
		switch {
		case NewlineKey(k):
			ui.HandleNewline()
		case k.Mods.Has(input.ModAlt):
			ui.HandleSendNow()
		default:
			ui.HandleEnter()
		}
		k.PreventDefault()
		return
	}
	switch {
	case ui.service.state.Banner == TreeBanner:
		ui.HandleTreeKey(k)
	case ui.service.state.Banner != NoBanner:
		ui.HandleBannerKey(k)
	case ui.service.state.SessionMenuVisible:
		ui.HandleSessionMenuKey(k)
	case ui.service.state.SlashMenuVisible:
		ui.HandleSlashMenuKey(k)
	default:
		ui.HandleGlobalKey(k)
	}
}

// HandleEnter selects from the active surface or submits the input.
func (ui *UI) HandleEnter() {
	if ui.service.state.Login != nil {
		ui.Apply(ui.service.HandleSubmit(ui.input.Text()))
		return
	}
	if ui.service.state.Banner != NoBanner {
		switch ui.service.state.Banner {
		case GraphemeBanner:
			ui.Apply(ui.service.CancelBanner())
			return
		case TreeBanner:
			ui.Apply(ui.service.ChooseTreeRow())
			return
		}
		if item := ui.bannerMenu.SelectedItem(); item != nil {
			ui.Apply(ui.service.HandleBannerSelect(item.Value))
		}
		return
	}
	if ui.service.state.SessionMenuVisible {
		if item := ui.sessionMenu.SelectedItem(); item != nil {
			ui.Apply(ui.service.HandleSessionSelect(item.Value))
		}
		return
	}
	if ui.service.state.SlashMenuVisible {
		if item := ui.slashMenu.SelectedItem(); item != nil {
			ui.Apply(ui.service.HandleSlashSelect(item.Value.(Command)))
		}
		return
	}
	ui.Apply(ui.service.HandleSubmit(ui.input.Text()))
}

// NewlineKey reports whether k, an Enter key, breaks the input's line rather
// than submitting it: Shift+Enter, which a terminal tells apart from Enter
// through the Kitty keyboard protocol, or a line feed, which Ctrl+J types,
// and which terminals without the protocol can be set to send for
// Shift+Enter.
func NewlineKey(k *input.KeyEvent) bool {
	if k.Mods.Has(input.ModCtrl) || k.Mods.Has(input.ModAlt) {
		return false
	}
	return k.Mods.Has(input.ModShift) || k.Raw == "\n"
}

// HandleNewline breaks the input's line at the cursor, while the input takes
// a prompt. With a banner open or a login asked for, it acts as Enter.
func (ui *UI) HandleNewline() {
	if ui.service.state.Login != nil || ui.service.state.Banner != NoBanner {
		ui.HandleEnter()
		return
	}
	ui.input.InsertText("\n")
}

// HandleSendNow sends the input ahead of the queue on Alt+Enter, stopping the
// run in progress. With a banner or menu open it acts as Enter.
func (ui *UI) HandleSendNow() {
	st := ui.service.state
	if st.Banner != NoBanner || st.SessionMenuVisible || st.SlashMenuVisible {
		ui.HandleEnter()
		return
	}
	ui.Apply(ui.service.HandleSendNow(ui.input.Text()))
}

// HandleBannerKey handles banner navigation, ←/→ without modifiers, which
// change the value of the selected row when it has values, as in /settings,
// Tab, which enters an entry the menu does not list when it takes one, as in
// /model, and Ctrl+S, which saves the selected row as a default when it can
// be saved, as a model in /model. Other keys go to the input when it searches the banner's
// menu, where ←/→ move its cursor, and are dropped otherwise so that they
// cannot edit the hidden input.
func (ui *UI) HandleBannerKey(k *input.KeyEvent) {
	if ui.service.state.Login != nil {
		if k.Name == "l" && k.Mods == input.ModCtrl {
			// OSC 52 copies through the terminal, including remote sessions.
			url := ui.service.state.Login.URL
			_, err := ui.renderer.Backend().WriteOut([]byte("\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(url)) + "\x1b\\"))
			if err != nil {
				ui.SetBannerText(ui.service.LoginPanel("Could not send URL to terminal clipboard: " + err.Error()).Text)
			} else {
				ui.SetBannerText(ui.service.LoginPanel("URL sent to terminal clipboard.").Text)
			}
			k.PreventDefault()
			return
		}
		if k.Name == input.KeyEscape {
			_, out := ui.service.HandleEscape()
			ui.Apply(out)
			k.PreventDefault()
		}
		return
	}
	plain := k.Mods == 0
	switch {
	case k.Name == input.KeyUp:
		ui.bannerMenu.MoveUp()
	case k.Name == input.KeyDown:
		ui.bannerMenu.MoveDown()
	case k.Name == input.KeyEscape:
		ui.Apply(ui.service.CancelBanner())
	case plain && k.Name == input.KeyLeft && ui.ChangeBannerRow(-1):
	case plain && k.Name == input.KeyRight && ui.ChangeBannerRow(1):
	case plain && k.Name == input.KeyTab && ui.CustomBanner():
	case k.Mods == input.ModCtrl && k.Name == "s" && ui.SaveBannerRow():
	default:
		if ui.bannerSearch {
			return
		}
	}
	k.PreventDefault()
}

// ChangeBannerRow moves the value of the selected banner row step places, as
// ←/→ do, and reports whether the row has values to change.
func (ui *UI) ChangeBannerRow(step int) bool {
	item := ui.bannerMenu.SelectedItem()
	if item == nil {
		return false
	}
	changed, events := ui.service.HandleBannerChange(item.Value, step)
	ui.Apply(events)
	return changed
}

// SaveBannerRow saves the selected banner row as a default, as Ctrl+S does,
// and reports whether the row can be saved.
func (ui *UI) SaveBannerRow() bool {
	item := ui.bannerMenu.SelectedItem()
	if item == nil {
		return false
	}
	saved, events := ui.service.HandleBannerSave(item.Value)
	ui.Apply(events)
	return saved
}

// CustomBanner enters an entry the open menu does not list, as Tab does, and
// reports whether the menu takes one.
func (ui *UI) CustomBanner() bool {
	custom, events := ui.service.HandleBannerCustom()
	ui.Apply(events)
	return custom
}

// HandleSessionMenuKey handles navigation while the session menu is open.
// Other keys go to the input, which searches the list.
func (ui *UI) HandleSessionMenuKey(k *input.KeyEvent) {
	switch k.Name {
	case input.KeyUp, input.KeyDown:
		ui.sessionMenu.HandleKeyPress(k)
	case input.KeyEscape:
		ui.Apply(ui.service.HideSessionMenu())
	default:
		return
	}
	k.PreventDefault()
}

// HandleSlashMenuKey handles navigation and completion in the slash menu.
func (ui *UI) HandleSlashMenuKey(k *input.KeyEvent) {
	switch k.Name {
	case input.KeyUp, input.KeyDown:
		ui.slashMenu.HandleKeyPress(k)
	case input.KeyTab:
		ui.CompleteSlashCommand()
	case input.KeyEscape:
		ui.Apply(ui.service.HideSlashMenu())
	default:
		return
	}
	k.PreventDefault()
}

// HandleGlobalKey handles scrolling, interruption, and cycling the reasoning
// level with Shift+Tab, outside menus.
func (ui *UI) HandleGlobalKey(k *input.KeyEvent) {
	switch k.Name {
	case input.KeyTab:
		if k.Mods.Has(input.ModShift) {
			ui.Apply(ui.service.CycleEffort())
			k.PreventDefault()
		}
	case input.KeyPageUp, input.KeyPageDown:
		if ui.ScrollTranscript(k) {
			k.PreventDefault()
		}
	case input.KeyEscape:
		interrupted, events := ui.service.HandleEscape()
		ui.Apply(events)
		if interrupted {
			k.PreventDefault()
		}
	}
}

// ShowQuitHint shows QuitHint in the empty input while an idle Ctrl+C has
// armed the quit. Once QuitWindow has passed and a second press no longer
// quits, it syncs the hint again through enqueue, which runs a function on
// the event loop, and so hides it, or shows what the input showed before.
func (ui *UI) ShowQuitHint(enqueue func(func())) {
	ui.SyncInputHint()
	time.AfterFunc(QuitWindow, func() { enqueue(ui.SyncInputHint) })
}

// SyncInputHint shows the hint the empty input shows now, as
// Service.InputHint gives it: QuitHint while a second Ctrl+C quits, and
// otherwise the placeholder of an open question, such as
// CustomModelPlaceholder.
func (ui *UI) SyncInputHint() {
	ui.SetInputHint(ui.service.InputHint(time.Now()))
}

// SetInputHint shows hint in the input while it is empty, in PanelDimFg, the
// color the other bracketed hints show in, or nothing for an empty hint. The
// input hides its cursor meanwhile, as HideCoveringCursor does.
func (ui *UI) SetInputHint(hint string) {
	ui.input.EditSurface().SetPlaceholderText(hint, PanelDimFg)
	ui.input.RequestRender()
}

// HideCoveringCursor hides the input's cursor where its block would draw a
// character inverted that the input colors: the first character of a hint
// the empty input shows, such as QuitHint, and a selected character, as the
// first one is when shift and the left arrow select leftward, which keeps the
// selection's fill. It runs after the input draws itself, and so after the
// input places the cursor; once the cursor covers neither, the input's next
// frame places it again.
func (ui *UI) HideCoveringCursor(*buffer.Buffer, time.Duration) {
	surface := ui.input.EditSurface()
	hint, _ := surface.PlaceholderText()
	underHint := hint != "" && surface.IsEmpty()
	if !ui.input.Focused() || !underHint && !ui.CursorOnSelection() {
		return
	}
	ui.input.Host().SetCursorPosition(ui.input.X(), ui.input.Y(), false)
}

// CursorOnSelection reports whether the input's cursor sits on a selected
// character: the first one of a selection made leftward, and not the one after
// a selection made rightward.
func (ui *UI) CursorOnSelection() bool {
	sel := ui.input.EditSurface().Selection()
	if sel == nil {
		return false
	}
	at := ui.input.CursorOffset()
	return sel.Start <= at && at < sel.End
}

// SetQuery puts the query of an open menu in the input, unless the input
// already holds it, and parks the cursor at its end. The change is not
// reported, since the menu already matches query.
func (ui *UI) SetQuery(query string) {
	if ui.input.Text() == query {
		return
	}
	ui.input.SetText(query)
	ui.input.GotoBufferEnd()
}

// SetInputShown shows or hides the input row. A banner without a search
// hides it, since the input takes no keys.
func (ui *UI) SetInputShown(shown bool) {
	ui.inputContentRow.SetGone(!shown)
	ui.SyncInputDividers()
}

// SyncInputDividers hides the divider under the last row of the input box,
// which would otherwise double the blank bottom edge. The input row is last
// unless a banner hides it, which leaves the banner last.
func (ui *UI) SyncInputDividers() {
	ui.bannerTailRow.SetGone(!ui.inputContentRow.Visible())
}

// ClearInput empties the input box.
func (ui *UI) ClearInput() {
	ui.input.SetText("")
}

// SetInputText replaces the input box's contents and parks the cursor
// at the end, so the user can keep typing after a completion.
func (ui *UI) SetInputText(text string) {
	ui.input.ReplaceText(text)
	ui.input.GotoBufferEnd()
}

// CompleteSlashCommand replaces the input with the selected slash command
// and refreshes the menu filter.
func (ui *UI) CompleteSlashCommand() {
	item := ui.slashMenu.SelectedItem()
	if item == nil {
		return
	}
	text := item.Value.(Command).String()
	ui.SetInputText(text)
	ui.Apply(ui.service.UpdateSlashMenu(text))
}
