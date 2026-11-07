package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/input"
	"github.com/unaquatic/fluid/terminal"
	"github.com/unaquatic/fluid/widget"
)

// TestSessionMenuSearchesFromTheInput checks that the session list opens in
// the input box with the input, which holds its query, keeping the keyboard.
func TestSessionMenuSearchesFromTheInput(t *testing.T) {
	// Inline mode shows the list without a preview, so nothing is read.
	ui := newBuiltTestUI(t, true)
	ui.renderer.FocusRenderable(ui.input)

	ui.ShowSessionMenu(EventSessionMenuShow{Items: []widget.SelectItem{{Name: "0199bbbb-2222  2026-09-27 14:03:11"}}})
	if parent := ui.sessionPanel.Parent(); !ui.sessionPanel.Visible() || parent == nil || parent.ID() != ui.inputMiddleCol.ID() {
		t.Fatal("session list is not in the input box")
	}
	if !ui.input.Focused() || !ui.inputContentRow.Visible() {
		t.Error("input is not on screen and focused for the search")
	}
	if got := ui.sessionMenu.Items()[0].Name; got != "0199bbbb-2222  2026-09-27 14:03:11" {
		t.Errorf("row = %q, want it as listed, since the arrow has its own column", got)
	}
	letter := &input.KeyEvent{Name: "p", Text: "p"}
	ui.HandleSessionMenuKey(letter)
	if letter.DefaultPrevented() {
		t.Error("session list kept a letter from the input")
	}

	ui.ShowSessionMenu(EventSessionMenuShow{Text: NoMatchesText, Query: "zzz"})
	if !ui.sessionTextRow.Visible() {
		t.Error("no-match notice is hidden")
	}
	if got := ui.input.Text(); got != "zzz" {
		t.Errorf("input holds %q, want the query", got)
	}

	ui.HideSessionMenu()
	if got := ui.input.Text(); got != "" {
		t.Errorf("input kept the query %q after the list closed", got)
	}
	if !ui.input.Focused() {
		t.Error("input lost the focus")
	}
}

func TestPromptLines(t *testing.T) {
	prompts := []string{"one", "two", "three", "four"}
	tests := []struct {
		name string
		n    int
		want []string
	}{
		{name: "all fit", n: 4, want: []string{"  user: one", "  user: two", "  user: three", "  user: four"}},
		{name: "roomy", n: 5, want: []string{"  user: one", "  user: two", "  user: three", "  user: four"}},
		// The count of the earlier prompts takes a row.
		{name: "earlier counted", n: 3, want: []string{"  [2 earlier]", "  user: three", "  user: four"}},
		{name: "count and latest", n: 2, want: []string{"  [3 earlier]", "  user: four"}},
		// A single row shows the latest prompt rather than a count alone.
		{name: "latest alone", n: 1, want: []string{"  user: four"}},
		{name: "no rows", n: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, line := range PromptLines(prompts, tt.n, nil) {
				got = append(got, line.PlainText())
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("lines = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSessionMenuPrompts checks that inline mode lists the selected session's
// prompts under the session list, follows the selection, and clears them as
// the list closes, and that fullscreen mode previews the transcript instead.
func TestSessionMenuPrompts(t *testing.T) {
	ui := newInputTestUI(t)
	ui.inline = true
	a := ui.service.app
	first := storePrompts(t, a, "add a Divide method to the calculator", "also add tests")
	second := storePrompts(t, a, "fix the flaky login test")
	rows := []widget.SelectItem{
		{Name: "first", Value: first},
		{Name: "second", Value: second},
	}
	prompts := ui.sessionPrompts
	check := func(want ...string) {
		t.Helper()
		if !slices.Equal(prompts.Prompts, want) {
			t.Errorf("prompts = %q, want %q", prompts.Prompts, want)
		}
		if shown := prompts.Box.Visible(); shown != (len(want) > 0) {
			t.Errorf("preview shown = %t with prompts %q", shown, want)
		}
	}

	ui.ShowSessionMenu(EventSessionMenuShow{Items: rows})
	check("add a Divide method to the calculator", "also add tests")
	ui.sessionMenu.MoveDown()
	check("fix the flaky login test")
	ui.ShowSessionMenu(EventSessionMenuShow{Text: NoMatchesText, Query: "zzz"})
	check()
	ui.ShowSessionMenu(EventSessionMenuShow{Items: rows})
	ui.HideSessionMenu()
	check()

	ui.inline = false
	ui.ShowSessionMenu(EventSessionMenuShow{Items: rows})
	check()
	if len(ui.previewMessageBox.Children()) == 0 {
		t.Error("fullscreen mode previews no transcript")
	}
}

// TestSessionMenuTranscript checks that inline mode opens the session list
// like any other menu, leaving the transcript on screen, and that fullscreen
// mode shows the preview in the transcript's place until the list closes.
func TestSessionMenuTranscript(t *testing.T) {
	// A value that is not a message.SessionID previews nothing, so nothing is read.
	rows := []widget.SelectItem{{Name: "0199bbbb-2222  2026-09-27 14:03:11", Value: "s1"}}

	t.Run("inline", func(t *testing.T) {
		ui := newBuiltTestUI(t, true)
		ui.AddMessage(userText("u1", "run the tests"))
		visible := func() bool {
			return !slices.ContainsFunc(ui.transcriptRows, func(r *widget.Box) bool { return !r.Visible() })
		}

		ui.ShowSessionMenu(EventSessionMenuShow{Items: rows})
		if !visible() || ui.previewMessageBox.Visible() {
			t.Error("opening the list hid the inline transcript or showed a preview")
		}
		ui.HideSessionMenu()
		if !visible() || ui.messageBox.Visible() {
			t.Error("closing the list hid the inline transcript or showed the fullscreen container")
		}
	})
	t.Run("fullscreen", func(t *testing.T) {
		ui := newBuiltTestUI(t, false)

		ui.ShowSessionMenu(EventSessionMenuShow{Items: rows})
		if ui.messageBox.Visible() || !ui.previewMessageBox.Visible() {
			t.Error("the preview did not take the transcript's place")
		}
		ui.HideSessionMenu()
		if !ui.messageBox.Visible() || ui.previewMessageBox.Visible() {
			t.Error("closing the list did not put the transcript back")
		}
	})
}

// TestSlashMenuHasNoGaps checks that the slash menu lists every command with
// no blank row between its groups, so that moving down from the last command
// of a group selects the first of the next.
func TestSlashMenuHasNoGaps(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	ui.ShowSlashMenu(ui.service.SlashMenuItems(""))
	if got, want := ui.slashMenu.ItemCount(), len(ui.service.CommandList()); got != want {
		t.Fatalf("menu lists %d rows, want one for each of the %d commands", got, want)
	}
	selected := func() Command { return ui.slashMenu.SelectedItem().Value.(Command) }
	for range 5 {
		ui.slashMenu.MoveDown()
	}
	if got := selected(); got != CommandUndo {
		t.Errorf("five rows down = %s, want %s, the first of the second group", got, CommandUndo)
	}
}

// TestSlashMenuCountsNoRows checks that the slash menu counts no rows, after
// its title or under them, wherever CountPlace puts other lists' counts,
// though its rows do not all fit.
func TestSlashMenuCountsNoRows(t *testing.T) {
	for _, place := range CountPlaces {
		t.Run(place.String(), func(t *testing.T) {
			ui := newBuiltTestUI(t, true)
			ui.Apply(ui.service.SetCountPlace(place))
			ui.ShowSlashMenu(ui.service.SlashMenuItems(""))
			if rows := ui.SlashRows(); rows >= len(ui.service.CommandList()) {
				t.Fatalf("menu shows %d rows, want fewer than its %d commands", rows, len(ui.service.CommandList()))
			}
			ui.slashMenu.MoveDown()
			if got := ui.slashHeader.Count; got != "" {
				t.Errorf("title counts %q, want no count", got)
			}
			count := fmt.Sprintf("[2/%d]", len(ui.service.CommandList()))
			if text := renderFrames(60, 24, ui.inputWrapper).String(); strings.Contains(text, count) {
				t.Errorf("frame %q shows the count %s", text, count)
			}
		})
	}
}

// TestCtrlCClosesSlashMenu checks that Ctrl+C, which empties the input,
// closes the slash menu that the input's leading slash opened, as deleting
// the slash does.
func TestCtrlCClosesSlashMenu(t *testing.T) {
	ui := newInputTestUI(t)
	ui.input.InsertText("/")
	if !ui.service.state.SlashMenuVisible || ui.slashMenuRow.Parent() == nil {
		t.Fatal("typing a slash did not open the slash menu")
	}

	ui.HandleKey(&input.KeyEvent{Name: "c", Mods: input.ModCtrl})

	if ui.input.Text() != "" {
		t.Errorf("input = %q, want it emptied", ui.input.Text())
	}
	if ui.service.state.SlashMenuVisible || ui.slashMenuRow.Parent() != nil {
		t.Error("the slash menu is still open after Ctrl+C emptied the input")
	}
}

// TestSlashStatusRow checks that the slash menu shows a status row above its
// title, and keeps it up to date while it is open: the model, the token use,
// and the session's worked time, which counts the turn in progress as it
// goes.
func TestSlashStatusRow(t *testing.T) {
	ui := newInputTestUI(t)
	ui.SetModelLabel("provider-a/model-h:medium")
	ui.service.state.Messages = []ChatMessage{
		{ID: "u1", Role: message.RoleUser},
		{ID: "a1", Role: message.RoleAgent, Worked: 3 * time.Minute},
	}
	ui.ShowSlashMenu(ui.service.SlashMenuItems(""))
	want := "model-h medium context 0K in 0K out 0K cache 0K $0.00 worked 3m 00s"
	if got := ui.slashStatus.PlainText(); got != want {
		t.Errorf("opened: status = %q\nwant             %q", got, want)
	}

	ui.SetUsage(SessionUsage{Input: 1_200, Output: 300, CacheRead: 4_000, Cost: 0.051, Context: 5_500, Window: 100_000})
	want = "model-h medium context 5.5K 6% in 1.2K out 0.3K cache 4.0K $0.05 worked 3m 00s"
	if got := ui.slashStatus.PlainText(); got != want {
		t.Errorf("usage changed: status = %q\nwant                    %q", got, want)
	}

	synctest.Test(t, func(t *testing.T) {
		ui.service.StartTurn(time.Now())
		time.Sleep(12 * time.Second)
		ui.Tick()
		want := "model-h medium context 5.5K 6% in 1.2K out 0.3K cache 4.0K $0.05 worked 3m 12s"
		if got := ui.slashStatus.PlainText(); got != want {
			t.Errorf("turn under way: status = %q\nwant                     %q", got, want)
		}
	})
}

// TestMenuSelectionSurvivesPartialRepaint checks that a frame repainting only
// part of a menu leaves its selected row marked as a frame of the whole menu
// does. While a turn runs, the worked time and the cursor change every second,
// and the region those frames repaint, which is as wide as the cells that
// changed, spans the menu's rows without reaching the box's left border cell,
// where the selected row's arrow, or its fill, starts.
func TestMenuSelectionSurvivesPartialRepaint(t *testing.T) {
	// Seven rows down leaves the menu scrolled, its selected row not the
	// seventh of the menu's rows.
	for _, moves := range []int{1, 7} {
		for _, selection := range SelectionStyles {
			t.Run(fmt.Sprintf("%s/%d down", selection, moves), func(t *testing.T) {
				ui := newInputTestUI(t)
				ui.Apply(ui.service.SetSelectionStyle(selection))
				ui.ShowSlashMenu(ui.service.SlashMenuItems(""))

				const w, h = 60, 19
				root := terminal.NewRootRenderable(w, h)
				root.Add(ui.inputWrapper)
				// The menu scrolls by the height it was last drawn at.
				root.Render(buffer.New(w, h), 0)
				for range moves {
					ui.slashMenu.MoveDown()
				}
				buf := buffer.New(w, h)
				root.Render(buf, 0)
				full := buffer.New(w, h)
				full.CopyFrom(buf)

				// Repaint the menu's rows from the second cell of the box on.
				menu := ui.slashMenu
				damage := buffer.Rect{X: menu.X() + 1, Y: menu.Y(), Width: menu.Width() - 1, Height: menu.Height()}
				root.PrepareLayout(0)
				root.SyncPositions(0)
				buf.FillRect(damage.X, damage.Y, damage.Width, damage.Height, ui.BoxBg())
				root.Draw(buf, 0, damage)

				for y := range h {
					for x := range w {
						if got, want := buf.Cell(x, y), full.Cell(x, y); got != want {
							t.Fatalf("cell (%d, %d) = %+v after a partial repaint, want %+v as drawn in full\nframe:\n%s",
								x, y, got, want, full.String())
						}
					}
				}
			})
		}
	}
}
