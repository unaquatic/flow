package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/input"
	"github.com/unaquatic/fluid/style"
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
		name    string
		earlier int
		n       int
		want    []string
	}{
		{name: "all fit", n: 4, want: []string{"  User one", "  User two", "  User three", "  User four"}},
		{name: "roomy", n: 5, want: []string{"  User one", "  User two", "  User three", "  User four"}},
		// The count of the earlier prompts takes a row.
		{name: "earlier counted", n: 3, want: []string{"  [2 earlier]", "  User three", "  User four"}},
		{name: "count and latest", n: 2, want: []string{"  [3 earlier]", "  User four"}},
		// A single row shows the latest prompt rather than a count alone.
		{name: "latest alone", n: 1, want: []string{"  User four"}},
		{name: "no rows", n: 0},
		// Prompts not loaded are counted with those that do not fit.
		{name: "unloaded counted", earlier: 5, n: 6, want: []string{"  [5 earlier]", "  User one", "  User two", "  User three", "  User four"}},
		{name: "unloaded and cut", earlier: 5, n: 3, want: []string{"  [7 earlier]", "  User three", "  User four"}},
		{name: "unloaded latest alone", earlier: 5, n: 1, want: []string{"  User four"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, line := range PromptLines(prompts, tt.earlier, tt.n, nil) {
				got = append(got, line.PlainText())
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("lines = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSessionMenuPrompts checks that the session list lists the selected
// session's prompts under its rows, in inline and fullscreen mode alike,
// follows the selection, and clears them as the list closes.
func TestSessionMenuPrompts(t *testing.T) {
	for _, inline := range []bool{true, false} {
		t.Run(fmt.Sprintf("inline=%t", inline), func(t *testing.T) { testSessionMenuPrompts(t, inline) })
	}
}

func testSessionMenuPrompts(t *testing.T, inline bool) {
	ui := newInputTestUI(t)
	ui.inline = inline
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

	loaded := awaitPrompts(t, ui)
	ui.ShowSessionMenu(EventSessionMenuShow{Items: rows})
	loaded()
	check("add a Divide method to the calculator", "also add tests")
	// The next session's prompts load in place of the first's.
	ui.sessionMenu.MoveDown()
	if !prompts.Loading || len(prompts.Prompts) != 0 || prompts.Text.PlainText() != "  "+LoadingText {
		t.Errorf("while loading, preview shows %q, want %q alone", prompts.Text.PlainText(), LoadingText)
	}
	loaded()
	check("fix the flaky login test")
	ui.ShowSessionMenu(EventSessionMenuShow{Text: NoMatchesText, Query: "zzz"})
	check()
	// A session loaded before shows at once.
	ui.ShowSessionMenu(EventSessionMenuShow{Items: rows})
	check("add a Divide method to the calculator", "also add tests")
	if prompts.Loading {
		t.Error("a session loaded before shows as loading")
	}
	ui.HideSessionMenu()
	check()
}

// awaitPrompts makes ui's prompt loader load without delay and hand what it
// loaded to the returned function, which applies it, as the event loop
// would, once it comes.
func awaitPrompts(t *testing.T, ui *UI) func() {
	t.Helper()
	loaded := make(chan func(), 8)
	ui.promptLoader.Delay = 0
	ui.promptLoader.Enqueue = func(fn func()) { loaded <- fn }
	return func() {
		t.Helper()
		select {
		case fn := <-loaded:
			fn()
		case <-time.After(5 * time.Second):
			t.Fatal("the prompts never loaded")
		}
	}
}

// TestPromptLoaderKeepsUpWithTheSelection checks that the prompt loader
// loads only the session selected last when the selection moves on before a
// load starts or ends, cancelling the others, and shows a session it loaded
// before at once.
func TestPromptLoaderKeepsUpWithTheSelection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var (
			started []message.SessionID
			shown   []message.SessionID
			queue   []func()
		)
		block := make(chan struct{})
		l := &PromptLoader{
			Load: func(ctx context.Context, id message.SessionID) []UIEvent {
				started = append(started, id)
				if id == "slow" {
					select {
					case <-block:
					case <-ctx.Done():
					}
				}
				return []UIEvent{EventSessionPrompts{Prompts: []string{string(id)}}}
			},
			Enqueue: func(fn func()) { queue = append(queue, fn) },
			Apply: func(events []UIEvent) {
				shown = append(shown, message.SessionID(events[0].(EventSessionPrompts).Prompts[0]))
			},
			Delay: PromptLoadDelay,
		}
		run := func() {
			synctest.Wait()
			for _, fn := range queue {
				fn()
			}
			queue = nil
		}

		// Moving through a, b and c faster than the delay loads c alone.
		for _, id := range []message.SessionID{"a", "b", "c"} {
			l.Request(id)
			time.Sleep(PromptLoadDelay / 4)
		}
		time.Sleep(PromptLoadDelay)
		run()
		if !slices.Equal(started, []message.SessionID{"c"}) || !slices.Equal(shown, []message.SessionID{"c"}) {
			t.Fatalf("loaded %v and showed %v, want c alone", started, shown)
		}

		// A load in progress is cancelled when the selection moves on, and
		// what it read is dropped.
		l.Request("slow")
		time.Sleep(PromptLoadDelay)
		synctest.Wait()
		l.Request("d")
		time.Sleep(PromptLoadDelay)
		run()
		if want := []message.SessionID{"c", "d"}; !slices.Equal(shown, want) {
			t.Errorf("showed %v, want %v: the slow session dropped", shown, want)
		}

		// A session loaded before shows at once, without a load.
		l.Request("c")
		if want := []message.SessionID{"c", "d", "c"}; !slices.Equal(shown, want) || len(started) != 3 {
			t.Errorf("showed %v after %d loads, want %v after 3", shown, len(started), want)
		}
		l.Reset()
		close(block)
	})
}

// TestSessionMenuTranscript checks that the session list opens like any
// other menu, leaving the transcript on screen in inline and fullscreen mode
// alike.
func TestSessionMenuTranscript(t *testing.T) {
	// A value that is not a message.SessionID previews nothing, so nothing is read.
	rows := []widget.SelectItem{{Name: "0199bbbb-2222  2026-09-27 14:03:11", Value: "s1"}}

	t.Run("inline", func(t *testing.T) {
		ui := newBuiltTestUI(t, true)
		ui.AddMessage(userText("u1", "Run the tests"))
		visible := func() bool {
			return !slices.ContainsFunc(ui.transcriptRows, func(r *widget.Box) bool { return !r.Visible() })
		}

		ui.ShowSessionMenu(EventSessionMenuShow{Items: rows})
		if !visible() {
			t.Error("opening the list hid the inline transcript")
		}
		ui.HideSessionMenu()
		if !visible() || ui.messageBox.Visible() {
			t.Error("closing the list hid the inline transcript or showed the fullscreen container")
		}
	})
	t.Run("fullscreen", func(t *testing.T) {
		ui := newBuiltTestUI(t, false)

		ui.ShowSessionMenu(EventSessionMenuShow{Items: rows})
		if !ui.messageBox.Visible() {
			t.Error("opening the list hid the transcript")
		}
		ui.HideSessionMenu()
		if !ui.messageBox.Visible() {
			t.Error("closing the list hid the transcript")
		}
	})
}

// TestSlashMenuHasNoGaps checks that the slash menu lists every command with
// no blank row between its groups, so that moving down from the last command
// of a group selects the first of the next.
func TestSlashMenuHasNoGaps(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	ui.ShowSlashMenu(ui.service.SlashMenuItems(""))
	if got, want := ui.slashMenu.ItemCount(), len(ui.service.ShownCommands()); got != want {
		t.Fatalf("menu lists %d rows, want one for each of the %d commands", got, want)
	}
	selected := func() Command { return ui.slashMenu.SelectedItem().Value.(Command) }
	for range len(SlashCommandGroups[0]) {
		ui.slashMenu.MoveDown()
	}
	if got := selected(); got != CommandUndo {
		t.Errorf("%d rows down = %s, want %s, the first of the second group", len(SlashCommandGroups[0]), got, CommandUndo)
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

// TestTabCompletesSlashCommand checks that Tab puts the selected command in
// the input with a space after it, ready for what it is given; that the menu
// still offers the command, the space left out of the match; and that it
// closes once something follows the space.
func TestTabCompletesSlashCommand(t *testing.T) {
	ui := newInputTestUI(t)
	ui.input.InsertText("/hel")
	if !ui.service.state.SlashMenuVisible {
		t.Fatal("typing /hel did not open the slash menu")
	}

	ui.HandleKey(&input.KeyEvent{Name: input.KeyTab})

	if got := ui.input.Text(); got != "/help " {
		t.Errorf("input = %q, want %q", got, "/help ")
	}
	if !ui.service.state.SlashMenuVisible || ui.slashMenuRow.Parent() == nil {
		t.Fatal("the slash menu closed when Tab completed a command, want it to offer the command still")
	}
	if item := ui.slashMenu.SelectedItem(); item == nil || item.Value.(Command) != CommandHelp {
		t.Errorf("selected = %v, want /help", item)
	}

	ui.input.InsertText("me")
	if ui.service.state.SlashMenuVisible || ui.slashMenuRow.Parent() != nil {
		t.Error("the slash menu is still open with text after the command")
	}
}

// TestSlashStatusRow checks that the slash menu shows a status row above its
// title, and keeps it up to date while it is open: the model, the token use,
// and the session's worked time, which counts the turn in progress as it
// goes.
func TestSlashStatusRow(t *testing.T) {
	ui := newInputTestUI(t)
	ui.SetModelLabel("provider-a/lab-a/model-h:medium")
	ui.service.state.Messages = []ChatMessage{
		{ID: "u1", Role: message.RoleUser},
		{ID: "a1", Role: message.RoleAgent, Worked: 3 * time.Minute},
	}
	ui.ShowSlashMenu(ui.service.SlashMenuItems(""))
	want := "Unknown [model-h] Medium Context 0K ↓ 0K ↑ 0K Cache 0K $0.00 Worked 3m 00s"
	if got := ui.slashStatus.PlainText(); got != want {
		t.Errorf("opened: status = %q\nwant             %q", got, want)
	}

	ui.SetUsage(SessionUsage{Input: 1_200, Output: 300, CacheRead: 4_000, Cost: 0.051, Context: 5_500, Window: 100_000})
	want = "Unknown [model-h] Medium Context 5.5K 6% ↓ 1.2K ↑ 0.3K Cache 4.0K $0.05 Worked 3m 00s"
	if got := ui.slashStatus.PlainText(); got != want {
		t.Errorf("usage changed: status = %q\nwant                    %q", got, want)
	}

	synctest.Test(t, func(t *testing.T) {
		ui.service.StartTurn(time.Now())
		time.Sleep(12 * time.Second)
		ui.Tick()
		want := "Unknown [model-h] Medium Context 5.5K 6% ↓ 1.2K ↑ 0.3K Cache 4.0K $0.05 Worked 3m 12s"
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
				// The renderer clears the region to its own background first.
				buf.FillRect(damage.X, damage.Y, damage.Width, damage.Height, style.ColorTransparent)
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
