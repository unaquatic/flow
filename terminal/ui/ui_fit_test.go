package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/fluid/widget"
)

// fitInputBox fits ui's input box to a terminal height rows tall, lays it
// out, and returns the rows it then takes.
func fitInputBox(ui *UI, height int) int {
	ui.FitInputBox(height)
	ui.renderer.Root().CalculateLayout()
	return ui.InputBoxHeight()
}

// longTree returns a session of n messages in one branch, ending at its last.
func longTree(n int) app.Tree {
	msgs := make([]message.Content, n)
	parent := message.ID("")
	for i := range msgs {
		id := message.ID(fmt.Sprintf("m%02d", i))
		msgs[i] = treeMsg(id, parent, message.RoleUser, fmt.Sprintf("message %d", i+1))
		parent = id
	}
	return app.Tree{Messages: msgs, Head: parent}
}

// numberedItems returns n menu rows.
func numberedItems(n int) []MenuItem {
	items := make([]MenuItem, n)
	for i := range items {
		items[i] = MenuItem{Name: fmt.Sprintf("provider/model-%02d", i+1)}
	}
	return items
}

// shownCount returns the count a list shows: after its title in h, or on
// the line under its rows, below, or "" while it shows none.
func shownCount(h *PanelHeader, below *PanelStatus) string {
	if h.Count != "" {
		return h.Count
	}
	if below.Row.Visible() {
		return below.Text.PlainText()
	}
	return ""
}

// fitPanels open each panel of the input box that holds a list, and return
// the count of its rows that it shows, or have no count, as the slash menu,
// which counts none. Every list has more rows than a short terminal shows.
var fitPanels = []struct {
	name  string
	open  func(ui *UI)
	count func(ui *UI) string
}{
	{name: "slash menu", open: func(ui *UI) {
		ui.ShowSlashMenu(ui.service.SlashMenuItems(""))
	}},
	{name: "session list", open: func(ui *UI) {
		rows := make([]widget.SelectItem, 10)
		for i := range rows {
			rows[i] = widget.SelectItem{Name: fmt.Sprintf("session %d", i+1), Value: "s"}
		}
		ui.ShowSessionMenu(EventSessionMenuShow{Items: rows})
	}, count: func(ui *UI) string { return shownCount(ui.sessionHeader, ui.sessionCount) }},
	{name: "banner menu", open: func(ui *UI) {
		items := numberedItems(15)
		ui.Apply(ui.service.ShowSearchMenu(&SearchMenu{Title: "Models", Items: func() []MenuItem { return items }}))
	}, count: func(ui *UI) string { return shownCount(ui.bannerHeader, ui.bannerCount) }},
	{name: "session tree", open: func(ui *UI) {
		ui.Apply(ui.service.OpenTree(longTree(30)))
	}, count: func(ui *UI) string { return shownCount(ui.treeHeader, ui.treeStatus) }},
}

// TestFitInputBoxKeepsMenusInView checks that every list in the input box
// shows only the rows that keep the box, with the indicator above it, a row
// short of the terminal's height, and that each list that counts its rows
// counts the rows it then leaves out. The shortest terminal leaves the list
// one row.
func TestFitInputBoxKeepsMenusInView(t *testing.T) {
	for _, panel := range fitPanels {
		for _, height := range []int{13, 17, 21} {
			t.Run(fmt.Sprintf("%s/%d rows", panel.name, height), func(t *testing.T) {
				ui := newBuiltTestUI(t, true)
				ui.ShowIndicator(EventIndicatorShow{Text: "Request failed"})
				panel.open(ui)

				if got := fitInputBox(ui, height); got > height-1 {
					t.Errorf("input box takes %d rows of %d, want at most %d", got, height, height-1)
				}
				list, ok := ui.ListPart()
				if !ok || list.Rows < 1 {
					t.Fatalf("list shows %d rows, want at least one", list.Rows)
				}
				if panel.count == nil {
					return
				}
				if count := panel.count(ui); !strings.Contains(count, "/") {
					t.Errorf("count %q, want the rows left out counted", count)
				}
			})
		}
	}
}

// TestFitInputBoxHidesSlashStatus checks that on a short terminal the status
// box above the input box, with the gap under it, goes whole before the
// slash menu gives up any command, and comes back once the terminal has
// room.
func TestFitInputBoxHidesSlashStatus(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	ui.ShowSlashMenu(ui.service.SlashMenuItems(""))
	full := fitInputBox(ui, 60)
	if !ui.slashStatusBox.Visible() {
		t.Fatal("the status box is hidden on a tall terminal")
	}

	// A row short of the whole box.
	if got := fitInputBox(ui, full); got != full-ui.StatusRows() {
		t.Errorf("input box takes %d rows, want %d without the status box", got, full-ui.StatusRows())
	}
	if ui.slashStatusBox.Visible() {
		t.Error("the status box still shows on a terminal a row short")
	}
	if list, _ := ui.ListPart(); list.Rows != slashMaxRows {
		t.Errorf("menu shows %d rows, want all %d while the status box gives way", list.Rows, slashMaxRows)
	}

	fitInputBox(ui, 60)
	if !ui.slashStatusBox.Visible() {
		t.Error("the status box did not come back once the terminal had room")
	}
}

// TestFitInputBoxGivesRowsBack checks that a list bounded on a short terminal
// shows all its rows again once the terminal has room for them.
func TestFitInputBoxGivesRowsBack(t *testing.T) {
	for _, panel := range fitPanels {
		t.Run(panel.name, func(t *testing.T) {
			ui := newBuiltTestUI(t, true)
			panel.open(ui)
			fitInputBox(ui, 12)
			if ui.service.state.MenuRows == 0 {
				t.Fatal("the list is not bounded on a 12-row terminal")
			}

			fitInputBox(ui, 60)
			if got := ui.service.state.MenuRows; got != 0 {
				t.Errorf("list bound = %d on a 60-row terminal, want it lifted", got)
			}
			list, _ := ui.ListPart()
			if list.Rows != list.Want {
				t.Errorf("list shows %d rows on a 60-row terminal, want all %d", list.Rows, list.Want)
			}
		})
	}
}

// TestFitInputBoxLeavesRoomyMenusAlone checks that a list that fits keeps
// every row, with no bound and no count.
func TestFitInputBoxLeavesRoomyMenusAlone(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	items := numberedItems(3)
	ui.Apply(ui.service.ShowSearchMenu(&SearchMenu{Title: "Models", Items: func() []MenuItem { return items }}))
	fitInputBox(ui, 24)
	if got := ui.service.state.MenuRows; got != 0 {
		t.Errorf("list bound = %d, want none", got)
	}
	if got := shownCount(ui.bannerHeader, ui.bannerCount); got != "" {
		t.Errorf("the menu counts rows that all fit: %q", got)
	}
}

// TestFitInputBoxBoundsSessionPrompts checks that the prompts previewed under
// the session list give their rows up before the list does, never leaving
// their divider without a prompt under it, and take them back once there is
// room.
func TestFitInputBoxBoundsSessionPrompts(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	rows := make([]widget.SelectItem, 4)
	for i := range rows {
		rows[i] = widget.SelectItem{Name: fmt.Sprintf("session %d", i+1), Value: "s"}
	}
	ui.ShowSessionMenu(EventSessionMenuShow{Items: rows})
	ui.sessionPrompts.Set([]string{"one", "two", "three"})
	prompts := ui.sessionPrompts

	full := fitInputBox(ui, 60)
	if got := prompts.Shown(); got != 4 {
		t.Fatalf("preview shows %d rows on a 60-row terminal, want its divider and 3 prompts", got)
	}
	// The terminal shrinks a row at a time, then grows back. The shortest it
	// fits the box in takes the preview's 4 rows, and leaves the list one row
	// of its 4, with its count under it: 6 rows fewer than the box takes, a
	// row short of the terminal.
	var heights []int
	for h := full + 1; h >= full-5; h-- {
		heights = append(heights, h)
	}
	heights = append(heights, full-3, full-1, full, full+1)
	seen := make(map[int]bool)
	for _, height := range heights {
		if got := fitInputBox(ui, height); got > height-1 {
			t.Errorf("%d rows: input box takes %d, want at most %d", height, got, height-1)
		}
		shown := prompts.Shown()
		seen[shown] = true
		if shown == 1 {
			t.Errorf("%d rows: preview shows its divider alone", height)
		}
		if prompts.Box.Visible() != (shown > 0) {
			t.Errorf("%d rows: preview visible = %t, showing %d rows", height, prompts.Box.Visible(), shown)
		}
		if shown > 0 && ui.service.state.MenuRows != 0 {
			t.Errorf("%d rows: list bounded to %d rows while the preview shows %d",
				height, ui.service.state.MenuRows, shown)
		}
	}
	for _, rows := range []int{0, 2, 3, 4} {
		if !seen[rows] {
			t.Errorf("preview never showed %d rows, want every count from 4 rows to hidden but 1", rows)
		}
	}
	if got := prompts.Shown(); got != 4 {
		t.Errorf("preview shows %d rows once there is room again, want 4", got)
	}
}

// TestFitInputBoxSessionPromptsFillTheWindow checks that the session list
// previews as many prompts as the terminal has rows for, the latest last
// after a count of the earlier ones, and every prompt once it has room.
func TestFitInputBoxSessionPromptsFillTheWindow(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	ui.ShowSessionMenu(EventSessionMenuShow{Items: []widget.SelectItem{{Name: "session", Value: "s"}}})
	prompts := make([]string, 40)
	for i := range prompts {
		prompts[i] = fmt.Sprintf("prompt %d", i+1)
	}
	ui.sessionPrompts.Set(prompts)
	preview := ui.sessionPrompts

	if got := fitInputBox(ui, 30); got != 29 {
		t.Errorf("input box takes %d rows of 30, want the 29 that fit", got)
	}
	shown := preview.Shown()
	if shown < promptLeastRows || shown >= preview.Want() {
		t.Fatalf("preview shows %d rows of %d on a 30-row terminal, want some cut", shown, preview.Want())
	}
	lines := strings.Split(preview.Text.PlainText(), "\n")
	// The divider takes a row, and the count another.
	earlier := fmt.Sprintf("  [%d earlier]", len(prompts)-(shown-2))
	if len(lines) != shown-1 || lines[0] != earlier || lines[len(lines)-1] != "  user: prompt 40" {
		t.Errorf("rows = %q, want %d rows from %q to the latest prompt", lines, shown-1, earlier)
	}
	if ui.service.state.MenuRows != 0 {
		t.Errorf("list bounded to %d rows while the preview is cut", ui.service.state.MenuRows)
	}

	fitInputBox(ui, 80)
	if got, want := preview.Shown(), 1+len(prompts); got != want {
		t.Errorf("preview shows %d rows on an 80-row terminal, want all %d", got, want)
	}
}

// TestFitInputBoxBoundsTheInput checks that input text taller than the
// terminal scrolls within the rows that fit, and grows back when they return.
func TestFitInputBoxBoundsTheInput(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	ui.SetInputText(strings.Repeat("line\n", 30) + "last")

	if got := fitInputBox(ui, 12); got > 11 {
		t.Errorf("input box takes %d rows of 12, want at most 11", got)
	}
	bounded := ui.input.LayoutNode().Computed.Height
	if bounded < 2 {
		t.Errorf("input shows %d rows, want at least its two", bounded)
	}

	fitInputBox(ui, 60)
	if got := ui.input.LayoutNode().Computed.Height; got != 31 {
		t.Errorf("input shows %d rows on a 60-row terminal, want all 31", got)
	}

	// A bound the text no longer fills is lifted, so the box closes up.
	fitInputBox(ui, 12)
	ui.SetInputText("short")
	fitInputBox(ui, 12)
	if got := ui.inputContentRow.LayoutNode().Computed.Height; got != 1 {
		t.Errorf("input row is %d rows tall for one line of text, want 1", got)
	}
}

// TestFitInputBoxBoundsTheQueue checks that a queue too long for the terminal
// shows the boxes of the prompts sent next, whole, in the rows that fit, and
// all of them once the terminal has room.
func TestFitInputBoxBoundsTheQueue(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	prompts := make([]string, 30)
	for i := range prompts {
		prompts[i] = fmt.Sprintf("prompt %d", i+1)
	}
	ui.ShowQueue(prompts)
	if got := fitInputBox(ui, 14); got > 13 {
		t.Errorf("input box takes %d rows of 14, want at most 13", got)
	}
	if got := len(ui.queueBox.Children()); got < 1 || got >= len(prompts) {
		t.Errorf("queue shows %d boxes, want the first few", got)
	}
	if got := ui.queueBox.LayoutNode().Computed.Height % queueItemRows; got != 0 {
		t.Errorf("queue is cut %d rows into a box, want whole boxes", got)
	}
	fitInputBox(ui, 200)
	if got := len(ui.queueBox.Children()); got != len(prompts) {
		t.Errorf("queue shows %d boxes on a tall terminal, want all %d", got, len(prompts))
	}
}

// TestFitInputBoxBoundsBannerText checks that a banner whose text is taller
// than the terminal, such as the grapheme report, is cut to the rows that fit.
func TestFitInputBoxBoundsBannerText(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	ui.Apply(ui.service.ShowGraphemeReport())
	if got := fitInputBox(ui, 14); got > 13 {
		t.Errorf("input box takes %d rows of 14, want at most 13", got)
	}
}

// TestFitInputBoxBoundsTheTreeWindow checks that the session tree shows as
// many rows as the bound allows, and pages by them.
func TestFitInputBoxBoundsTheTreeWindow(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	ui.Apply(ui.service.OpenTree(longTree(30)))
	fitInputBox(ui, 14)

	// The bound counts the insert line among the rows.
	rows := ui.service.state.MenuRows - 1
	v, ok := ui.service.TreeView()
	if !ok || len(v.Rows) != rows {
		t.Fatalf("tree draws %d rows, want the bound of %d less the insert line", len(v.Rows), rows)
	}
	if v.Count != "[30/30]" {
		t.Errorf("count = %q, want [30/30]", v.Count)
	}

	ui.Apply(ui.service.PageTree(-1))
	if v, _ := ui.service.TreeView(); v.Rows[v.Selected].Entry.ID != message.ID(fmt.Sprintf("m%02d", 29-rows)) {
		t.Errorf("page up selected %s, want the row %d above", v.Rows[v.Selected].Entry.ID, rows)
	}
}
