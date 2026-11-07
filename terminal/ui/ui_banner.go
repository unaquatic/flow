package ui

import (
	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/textbuf"
)

// bannerMaxRows is the number of banner items shown before the list
// scrolls.
const bannerMaxRows = 12

// bannerNameGap is the space between the longest item name and the column
// of descriptions.
const bannerNameGap = 2

// ShowBanner displays a banner at the top of the input box: its header, the
// title with the banner's keys after it, and below it its text and menu.
// When the banner searches its menu, the input below it holds the query.
// Otherwise the input is hidden and the menu takes the keyboard.
func (ui *UI) ShowBanner(e EventBannerShow) {
	ui.treePanel.SetGone(true)
	ui.bannerHeader.Set(e.Title, "")
	ui.bannerHints = e.Hints()
	ui.SetHeaderKeys(ui.bannerHeader, ui.bannerHints)
	ui.SetBannerMenu(e)
	ui.SetBannerText(e.Text)
	ui.bannerPanel.SetGone(false)
	ui.SyncFooter()
	ui.bannerSearch = e.Search
	ui.SetQuery(e.Query)
	ui.SetInputShown(e.Search || e.Input)
	if e.Search || e.Input {
		ui.renderer.FocusRenderable(ui.input)
	} else {
		// A hidden input that kept the focus would leave its last cursor on
		// screen. HandleBannerKey consumes every key, so the menu gets none.
		ui.renderer.FocusRenderable(ui.bannerMenu)
	}
}

// SetBannerText replaces the body text and hides its row when empty.
func (ui *UI) SetBannerText(text string) {
	ui.bannerText.SetContent(text)
	ui.bannerTextRow.SetGone(text == "")
	ui.SyncBannerDivider()
}

// SyncBannerDivider shows the divider under the banner's header only when
// text or a menu follows it.
func (ui *UI) SyncBannerDivider() {
	body := ui.bannerTextRow.Visible() || ui.bannerMenu.ItemCount() > 0
	ui.bannerDividerRow.SetGone(!ui.bannerHeader.Visible() || !body)
}

// SetBannerMenu sets banner items, aligns their descriptions in a column
// after the longest name, and shows at most BannerRows of them.
func (ui *UI) SetBannerMenu(e EventBannerShow) {
	width := e.NameWidth
	for _, item := range e.Items {
		width = max(width, textbuf.StringWidth(item.Name)+bannerNameGap)
	}
	ui.bannerMenu.SetNameWidth(width)
	ui.bannerMenu.SetItems(e.Items)

	rows := ui.BannerRows()
	ui.bannerMenu.SetHeight(layout.Points(rows))
	if len(e.Items) > 0 {
		ui.SelectBannerRow(e.SelectedIdx, rows, len(e.Items))
	}
	ui.SyncBannerCount()
	ui.SyncBannerDivider()
}

// BannerRows returns the number of banner menu rows shown: every item up
// to bannerMaxRows, within the input box's bound.
func (ui *UI) BannerRows() int {
	return ui.MenuRows(min(ui.bannerMenu.ItemCount(), bannerMaxRows))
}

// SyncBannerCount shows the selected row's place in the banner menu, such as
// "[3/40]", where CountPlace says, while the rows do not all fit.
func (ui *UI) SyncBannerCount() {
	ui.ShowCount(ui.bannerHeader, ui.bannerCount, TitleCount(ui.bannerMenu.SelectedIndex(), ui.bannerMenu.ItemCount(), ui.BannerRows()))
}

// SelectBannerRow selects idx and scrolls it into view. Invalid indices select
// the first entry.
//
// The explicit scroll also handles an unchanged selected index.
func (ui *UI) SelectBannerRow(idx, rows, total int) {
	if idx < 0 || idx >= total {
		idx = 0
	}
	ui.bannerMenu.SetSelectedIndex(idx)
	ui.bannerMenu.SetScrollOffset(max(idx-rows+1, 0))
}

// HideBanner takes the banner or session tree down, empties its query from
// the input, and returns the focus to the input.
func (ui *UI) HideBanner() {
	ui.bannerPanel.SetGone(true)
	ui.treePanel.SetGone(true)
	ui.bannerHints = nil
	ui.SyncFooter()
	ui.bannerSearch = false
	ui.SetQuery("")
	ui.SetInputShown(true)
	ui.renderer.FocusRenderable(ui.input)
}
