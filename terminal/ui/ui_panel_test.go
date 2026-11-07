package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/input"
	"github.com/unaquatic/fluid/render"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/terminal"
	"github.com/unaquatic/fluid/widget"
)

// TestPanelTitlesNameKeys checks that each panel in the input box names its
// keys after its title and count, esc last, also a banner without a title.
func TestPanelTitlesNameKeys(t *testing.T) {
	tests := []struct {
		name   string
		open   func(ui *UI)
		header func(ui *UI) *PanelHeader
		want   string
	}{
		{name: "slash menu", open: func(ui *UI) { ui.ShowSlashMenu(ui.service.SlashMenuItems("")) },
			header: func(ui *UI) *PanelHeader { return ui.slashHeader },
			want:   "Commands [↑/↓ move] [esc cancel]"},
		{name: "slash menu that fits", open: func(ui *UI) { ui.ShowSlashMenu(ui.service.SlashMenuItems("/st")) },
			header: func(ui *UI) *PanelHeader { return ui.slashHeader },
			want:   "Commands [↑/↓ move] [esc cancel]"},
		{name: "session list", open: func(ui *UI) {
			ui.ShowSessionMenu(EventSessionMenuShow{Items: []widget.SelectItem{{Name: "s", Value: "s"}}})
		}, header: func(ui *UI) *PanelHeader { return ui.sessionHeader },
			want: "Sessions [↑/↓ move] [esc cancel]"},
		{name: "loading banner", open: func(ui *UI) {
			ui.ShowBanner(EventBannerShow{Title: "Models", Text: "Loading models..."})
		}, header: func(ui *UI) *PanelHeader { return ui.bannerHeader },
			want: "Models [esc cancel]"},
		{name: "settings", open: func(ui *UI) { ui.Apply(ui.service.ShowSettingsMenu()) },
			header: func(ui *UI) *PanelHeader { return ui.bannerHeader },
			want:   "Settings [↑/↓ move] [←/→ change] [esc cancel]"},
		{name: "banner without a title", open: func(ui *UI) {
			ui.ShowBanner(EventBannerShow{Items: []widget.SelectItem{{Name: "provider-a"}}})
		}, header: func(ui *UI) *PanelHeader { return ui.bannerHeader },
			want: "[↑/↓ move] [esc cancel]"},
		{name: "session tree", open: func(ui *UI) { ui.Apply(ui.service.OpenTree(forkedTree("a2"))) },
			header: func(ui *UI) *PanelHeader { return ui.treeHeader },
			want:   "Session tree [↑/↓ move] [alt+←/→ fork] [ctrl+o filter] [ctrl+t hidden] [esc cancel]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInputTestUI(t)
			tt.open(ui)
			h := tt.header(ui)
			if got := h.PlainText(); got != tt.want || !h.TitleRow.Visible() {
				t.Errorf("title = %q, shown %v; want %q shown", got, h.TitleRow.Visible(), tt.want)
			}
		})
	}
}

// TestKeyHintsInFooter checks that with KeyHintsMode set to KeyHintsFooter,
// each panel names all its keys in the footer, esc first, and none after its
// title, that closing it leaves the footer blank, and that the default mode
// brings the keys back to the titles.
func TestKeyHintsInFooter(t *testing.T) {
	tests := []struct {
		name   string
		open   func(ui *UI)
		close  func(ui *UI)
		header func(ui *UI) *PanelHeader
		want   []KeyHint
	}{
		// The Usage box shows with the slash menu, and its key follows the
		// menu's, naming what it pins.
		{name: "slash menu", want: slices.Concat(SlashHints(), UsageFooterHints(false)),
			open:   func(ui *UI) { ui.ShowSlashMenu(ui.service.SlashMenuItems("")) },
			close:  func(ui *UI) { ui.HideSlashMenu() },
			header: func(ui *UI) *PanelHeader { return ui.slashHeader }},
		{name: "session list", want: SessionHints(),
			open: func(ui *UI) {
				ui.ShowSessionMenu(EventSessionMenuShow{Items: []widget.SelectItem{{Name: "s", Value: "s"}}})
			},
			close:  func(ui *UI) { ui.HideSessionMenu() },
			header: func(ui *UI) *PanelHeader { return ui.sessionHeader }},
		{name: "banner", want: []KeyHint{HintCancel},
			open:   func(ui *UI) { ui.ShowBanner(EventBannerShow{Title: "Models", Text: "Loading models..."}) },
			close:  func(ui *UI) { ui.HideBanner() },
			header: func(ui *UI) *PanelHeader { return ui.bannerHeader }},
		{name: "session tree", want: TreeHints(),
			open:   func(ui *UI) { ui.Apply(ui.service.OpenTree(forkedTree("a2"))) },
			close:  func(ui *UI) { ui.HideBanner() },
			header: func(ui *UI) *PanelHeader { return ui.treeHeader }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInputTestUI(t)
			ui.Apply(ui.service.SetKeyHintsMode(KeyHintsFooter))
			tt.open(ui)
			if got := ui.footerHints.Hints; !slices.Equal(got, tt.want) || !ui.footerHints.Visible() {
				t.Errorf("footer: hints %v, shown %v; want %v shown", got, ui.footerHints.Visible(), tt.want)
			}
			if got := tt.header(ui).Keys.Hints; len(got) > 0 {
				t.Errorf("footer: title names %v, want no keys", got)
			}

			ui.Apply(ui.service.SetKeyHintsMode(KeyHintsTitle))
			own := slices.DeleteFunc(slices.Clone(tt.want), func(h KeyHint) bool { return slices.Contains(UsageFooterHints(false), h) })
			if got, want := tt.header(ui).Keys.Hints, TitleHints(own); !slices.Equal(got, want) {
				t.Errorf("title: title names %v, want %v", got, want)
			}
			if ui.footerHints.Visible() {
				t.Errorf("title: footer shows %v, want no hints", ui.footerHints.Hints)
			}

			ui.Apply(ui.service.SetKeyHintsMode(KeyHintsFooter))
			tt.close(ui)
			if ui.footerHints.Visible() {
				t.Errorf("closed: footer shows %v, want no hints", ui.footerHints.Hints)
			}
		})
	}
}

// TestKeyHintsInSubtitle checks that with KeyHintsMode set to
// KeyHintsSubtitle, each panel names all its keys on the row under its title,
// esc first, as the footer would, and none after its title or in the footer,
// and that the default mode takes the row away.
func TestKeyHintsInSubtitle(t *testing.T) {
	tests := []struct {
		name   string
		open   func(ui *UI)
		header func(ui *UI) *PanelHeader
		want   []KeyHint
	}{
		{name: "slash menu", want: SlashHints(),
			open:   func(ui *UI) { ui.ShowSlashMenu(ui.service.SlashMenuItems("")) },
			header: func(ui *UI) *PanelHeader { return ui.slashHeader }},
		{name: "session list", want: SessionHints(),
			open: func(ui *UI) {
				ui.ShowSessionMenu(EventSessionMenuShow{Items: []widget.SelectItem{{Name: "s", Value: "s"}}})
			},
			header: func(ui *UI) *PanelHeader { return ui.sessionHeader }},
		{name: "banner", want: []KeyHint{HintCancel},
			open:   func(ui *UI) { ui.ShowBanner(EventBannerShow{Title: "Models", Text: "Loading models..."}) },
			header: func(ui *UI) *PanelHeader { return ui.bannerHeader }},
		{name: "session tree", want: TreeHints(),
			open:   func(ui *UI) { ui.Apply(ui.service.OpenTree(forkedTree("a2"))) },
			header: func(ui *UI) *PanelHeader { return ui.treeHeader }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInputTestUI(t)
			ui.Apply(ui.service.SetKeyHintsMode(KeyHintsSubtitle))
			tt.open(ui)
			h := tt.header(ui)
			if got := h.SubtitleKeys.Hints; !slices.Equal(got, tt.want) || !h.SubtitleKeysRow.Visible() {
				t.Errorf("subtitle: row names %v, shown %v; want %v shown", got, h.SubtitleKeysRow.Visible(), tt.want)
			}
			if got := h.Keys.Hints; len(got) > 0 {
				t.Errorf("subtitle: title names %v, want no keys", got)
			}
			if ui.footerHints.Visible() {
				t.Errorf("subtitle: footer shows %v, want no hints", ui.footerHints.Hints)
			}

			ui.Apply(ui.service.SetKeyHintsMode(KeyHintsTitle))
			if h.SubtitleKeysRow.Visible() || len(h.SubtitleKeys.Hints) > 0 {
				t.Errorf("title: row under the title names %v, want it gone", h.SubtitleKeys.Hints)
			}
			if got, want := h.Keys.Hints, TitleHints(tt.want); !slices.Equal(got, want) {
				t.Errorf("title: title names %v, want %v", got, want)
			}
		})
	}
}

// TestCtrlUPinsUsage checks that the Usage box names ctrl+u after its title
// while the slash menu is open, that pressing it pins the box, which then
// names ctrl+u as what unpins it, and stays above the input box once the
// menu closes, naming no key, and that pressing it again unpins the box,
// which goes with the menu closed. Pinned, the box shows with the menu
// closed from the start too.
func TestCtrlUPinsUsage(t *testing.T) {
	ui := newInputTestUI(t)
	ctrlU := func() {
		key := &input.KeyEvent{Name: "u", Mods: input.ModCtrl}
		ui.HandleKey(key)
		if !key.DefaultPrevented() {
			t.Error("Ctrl+U went on to the input")
		}
	}
	ui.ShowSlashMenu(ui.service.SlashMenuItems(""))
	if got, want := ui.statusHeader.PlainText(), "Usage [ctrl+u pin]"; got != want || !ui.slashStatusBox.Visible() {
		t.Errorf("menu open: Usage title %q, shown %v; want %q shown", got, ui.slashStatusBox.Visible(), want)
	}

	ctrlU()
	if got, want := ui.statusHeader.PlainText(), "Usage [ctrl+u unpin]"; got != want {
		t.Errorf("pinned, menu open: Usage title %q, want %q", got, want)
	}
	ui.service.state.SlashMenuVisible = false
	ui.HideSlashMenu()
	if got, want := ui.statusHeader.PlainText(), "Usage"; got != want || !ui.slashStatusBox.Visible() {
		t.Errorf("pinned, menu closed: Usage title %q, shown %v; want %q shown", got, ui.slashStatusBox.Visible(), want)
	}
	if got := ui.slashStatus.PlainText(); got == "" {
		t.Error("pinned, menu closed: the Usage box's line is empty")
	}

	ctrlU()
	if ui.slashStatusBox.Visible() || ui.service.state.UsagePinned {
		t.Error("unpinned, menu closed: the Usage box still shows")
	}

	ctrlU()
	if !ui.slashStatusBox.Visible() {
		t.Error("pinned with the menu closed from the start: the Usage box does not show")
	}
}

// TestFooterNamesUsagePin checks that in the footer, away from the Usage
// box, the key that pins the box names what it pins, "pin usage" or "unpin
// usage", while the slash menu is open, and that the footer names no key of
// the pinned box once the menu closes.
func TestFooterNamesUsagePin(t *testing.T) {
	ui := newInputTestUI(t)
	ui.Apply(ui.service.SetKeyHintsMode(KeyHintsFooter))
	ui.ShowSlashMenu(ui.service.SlashMenuItems(""))
	last := func() KeyHint { return ui.footerHints.Hints[len(ui.footerHints.Hints)-1] }
	if got, want := last(), (KeyHint{Key: "ctrl+u", Action: "pin usage"}); got != want {
		t.Errorf("unpinned: footer ends with %v, want %v", got, want)
	}
	ui.Apply(ui.service.HandleCtrlU())
	if got, want := last(), (KeyHint{Key: "ctrl+u", Action: "unpin usage"}); got != want {
		t.Errorf("pinned: footer ends with %v, want %v", got, want)
	}
	ui.service.state.SlashMenuVisible = false
	ui.HideSlashMenu()
	if ui.footerHints.Visible() {
		t.Errorf("pinned, menu closed: footer shows %v, want no hints", ui.footerHints.Hints)
	}
}

// TestCountBelow checks that with CountPlace set to CountBelow, the default,
// each list whose rows do not all fit counts them on the line under them,
// the tree before its filter, and not after its title, and that CountInTitle
// moves the count after the title. The slash menu counts none, as
// TestSlashMenuCountsNoRows checks.
func TestCountBelow(t *testing.T) {
	tests := []struct {
		name   string
		open   func(ui *UI)
		header func(ui *UI) *PanelHeader
		below  func(ui *UI) *PanelStatus
		count  string
	}{
		{name: "session list", count: "[1/10]",
			open: func(ui *UI) {
				rows := make([]widget.SelectItem, 10)
				for i := range rows {
					rows[i] = widget.SelectItem{Name: fmt.Sprintf("session %d", i+1), Value: "s"}
				}
				ui.ShowSessionMenu(EventSessionMenuShow{Items: rows})
			},
			header: func(ui *UI) *PanelHeader { return ui.sessionHeader },
			below:  func(ui *UI) *PanelStatus { return ui.sessionCount }},
		{name: "banner menu", count: "[1/15]",
			open: func(ui *UI) {
				items := numberedItems(15)
				ui.Apply(ui.service.ShowSearchMenu(&SearchMenu{Title: "Models", Items: func() []MenuItem { return items }}))
			},
			header: func(ui *UI) *PanelHeader { return ui.bannerHeader },
			below:  func(ui *UI) *PanelStatus { return ui.bannerCount }},
		{name: "session tree", count: "[30/30]",
			open:   func(ui *UI) { ui.Apply(ui.service.OpenTree(longTree(30))) },
			header: func(ui *UI) *PanelHeader { return ui.treeHeader },
			below:  func(ui *UI) *PanelStatus { return ui.treeStatus }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInputTestUI(t)
			ui.Apply(ui.service.SetCountPlace(CountBelow))
			tt.open(ui)
			if got := tt.header(ui).Count; got != "" {
				t.Errorf("below: title counts %q, want no count", got)
			}
			if got := tt.below(ui).Text.PlainText(); got != tt.count || !tt.below(ui).Row.Visible() {
				t.Errorf("below: line under the rows %q, shown %v; want %q shown", got, tt.below(ui).Row.Visible(), tt.count)
			}

			ui.Apply(ui.service.SetCountPlace(CountInTitle))
			if got := tt.header(ui).Count; got != tt.count {
				t.Errorf("title: title counts %q, want %q", got, tt.count)
			}
			if got := tt.below(ui).Text.PlainText(); got != "" {
				t.Errorf("title: line under the rows %q, want none", got)
			}
		})
	}
}

// TestQueueBoxes checks that each queued prompt shows in a box of its own
// above the input box, the first line of its prompt and then its hints:
// "[queued]", and after the first, sent next, the key that sends it now.
func TestQueueBoxes(t *testing.T) {
	ui := newInputTestUI(t)
	ui.ShowQueue([]string{"also update the README", "then run go vet\nand fix what it finds"})
	if got := len(ui.queueBox.Children()); got != 2 || !ui.queueBox.Visible() {
		t.Fatalf("queue shows %d boxes, shown %v; want 2 shown", got, ui.queueBox.Visible())
	}
	buf := renderFrames(60, 12, ui.inputWrapper)
	text := buf.String()
	for _, want := range []string{
		"also update the README [queued] [alt+enter send now]",
		"then run go vet [queued]",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("frame %q lacks %q", text, want)
		}
	}
	if strings.Contains(text, "fix what it finds") {
		t.Error("a queued prompt shows past its first line")
	}
	ui.ShowQueue(nil)
	if ui.queueBox.Visible() || len(ui.queueBox.Children()) > 0 {
		t.Error("the queue's boxes stay once nothing is queued")
	}
}

// TestTitleHints checks that a panel's title leaves out the keys that tab
// and typing serve, and names esc last, keeping the order of the rest.
func TestTitleHints(t *testing.T) {
	escClose := KeyHint{Key: "esc", Action: "close"}
	fork := KeyHint{Key: "alt+←/→", Action: "fork"}
	tests := []struct {
		hints, want []KeyHint
	}{
		{hints: SlashHints(), want: []KeyHint{HintMove, HintCancel}},
		{hints: []KeyHint{HintCancel, HintMove, fork, HintSearch}, want: []KeyHint{HintMove, fork, HintCancel}},
		{hints: []KeyHint{escClose}, want: []KeyHint{escClose}},
		{hints: []KeyHint{HintSearch}, want: nil},
	}
	for _, tt := range tests {
		if got := TitleHints(tt.hints); !slices.Equal(got, tt.want) {
			t.Errorf("TitleHints(%v) = %v, want %v", tt.hints, got, tt.want)
		}
	}
}

// TestKeyHintsViewLines checks that key hints wrap between hints and never
// inside one, in both styles: bracketed, a space apart, as a title names
// them, and as the footer names them, two spaces apart.
func TestKeyHintsViewLines(t *testing.T) {
	for _, tt := range []struct {
		bracketed bool
		width     int
		want      []string
	}{
		{bracketed: false, width: 80, want: []string{"esc cancel  ↑/↓ move  alt+←/→ fork  ctrl+o filter  ctrl+t hidden  type search"}},
		{bracketed: false, width: 40, want: []string{"esc cancel  ↑/↓ move  alt+←/→ fork", "ctrl+o filter  ctrl+t hidden", "type search"}},
		{bracketed: true, width: 80, want: []string{"[esc cancel] [↑/↓ move] [alt+←/→ fork] [ctrl+o filter] [ctrl+t hidden]", "[type search]"}},
		{bracketed: true, width: 40, want: []string{"[esc cancel] [↑/↓ move] [alt+←/→ fork]", "[ctrl+o filter] [ctrl+t hidden]", "[type search]"}},
		// A hint wider than the row takes a row of its own.
		{bracketed: true, width: 4, want: []string{"[esc cancel]", "[↑/↓ move]", "[alt+←/→ fork]", "[ctrl+o filter]", "[ctrl+t hidden]", "[type search]"}},
	} {
		v := NewKeyHintsView("hints", tt.bracketed)
		v.SetHints(TreeHints())
		var got []string
		for _, line := range v.Lines(tt.width) {
			texts := make([]string, len(line))
			for i, h := range line {
				texts[i] = v.Text(h)
			}
			got = append(got, strings.Join(texts, strings.Repeat(" ", v.Gap())))
			if w := v.LineWidth(line); w > tt.width && len(line) > 1 {
				t.Errorf("bracketed %v, width %d: line %q is %d wide", tt.bracketed, tt.width, got[len(got)-1], w)
			}
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("bracketed %v, width %d: lines %q, want %q", tt.bracketed, tt.width, got, tt.want)
		}
	}
}

// TestPanelHeaderCount checks that a header shows a menu's count after its
// title, in the grey of what a key does, and neither of them bold, and the
// title alone once the count is gone.
func TestPanelHeaderCount(t *testing.T) {
	ui := newBannerTestUI(t)
	h := ui.bannerHeader
	h.Set("Commands", "")
	h.SetCount("[3/13]")
	chunks := h.Title.Content().Chunks
	if got := h.Title.PlainText(); got != "Commands [3/13]" || len(chunks) != 2 {
		t.Fatalf("title = %q in %d chunks, want \"Commands [3/13]\" in 2", got, len(chunks))
	}
	if chunks[0].Attributes&style.AttrBold != 0 {
		t.Error("the title is bold")
	}
	if count := chunks[1]; count.Attributes&style.AttrBold != 0 || count.Fg != PanelDimFg {
		t.Errorf("count = %+v, want it in PanelDimFg, not bold", count)
	}
	h.SetCount("")
	if got := h.Title.PlainText(); got != "Commands" {
		t.Errorf("title without a count = %q, want Commands", got)
	}
}

// TestPanelHeaderSubtitle checks that a header's subtitle row shows only
// while it has text.
func TestPanelHeaderSubtitle(t *testing.T) {
	ui := newBannerTestUI(t)
	h := ui.bannerHeader
	h.Set("Models", "")
	if h.SubtitleRow.Visible() {
		t.Error("empty subtitle row is shown")
	}
	h.Set("Models", "provider-a account 01a0e1b4")
	if !h.SubtitleRow.Visible() || h.Subtitle.PlainText() != "provider-a account 01a0e1b4" {
		t.Errorf("subtitle = %q, shown %v; want it shown", h.Subtitle.PlainText(), h.SubtitleRow.Visible())
	}
	h.Set("", "")
	if h.Visible() {
		t.Error("header without title or subtitle is shown")
	}
}

// TestIndicatorShowsRunKeys checks that the keys that act on a run show in
// the indicator after the run's time, with ctrl+o named for what it does
// next as soon as it is pressed.
func TestIndicatorShowsRunKeys(t *testing.T) {
	ui := newInputTestUI(t)
	ui.Apply(ui.service.ShowIndicator(""))
	const expand = "Working [0s] [esc cancel] [ctrl+o expand]"
	if got := ui.indicatorLabel.PlainText(); got != expand {
		t.Errorf("working: indicator = %q, want %q", got, expand)
	}

	ui.Apply(ui.service.SetToolsExpanded(true))
	const collapse = "Working [0s] [esc cancel] [ctrl+o collapse]"
	if got := ui.indicatorLabel.PlainText(); got != collapse {
		t.Errorf("tools expanded: indicator = %q, want %q", got, collapse)
	}
}

// TestCtrlOWithSlashMenuOpen checks that ctrl+o, which the slash menu does
// not name, still expands the tool calls while the menu is open and leaves
// the keys its title names as they are.
func TestCtrlOWithSlashMenuOpen(t *testing.T) {
	ui := newInputTestUI(t)
	ui.ShowSlashMenu(ui.service.SlashMenuItems(""))
	want := ui.slashHeader.PlainText()

	handled, events := ui.service.HandleCtrlO()
	if !handled {
		t.Fatal("Ctrl+O with the slash menu open was not handled")
	}
	ui.Apply(events)
	if !ui.service.state.ToolsExpanded {
		t.Error("Ctrl+O with the slash menu open did not expand the tool calls")
	}
	if got := ui.slashHeader.PlainText(); got != want {
		t.Errorf("tools expanded: title = %q, want %q", got, want)
	}
}

// cursorRecorder is a render host that keeps the last cursor a widget
// placed.
type cursorRecorder struct {
	render.NoopHost
	// X, Y, and Visible are the last cursor placed.
	X, Y    int
	Visible bool
}

// SetCursorPosition records the cursor.
func (c *cursorRecorder) SetCursorPosition(x, y int, visible bool) {
	c.X, c.Y, c.Visible = x, y, visible
}

// TestQuitHintHidesTheCursor checks that the focused input hides its cursor
// while it shows the quit hint, whose first character the cursor block would
// draw inverted, and shows it again once the hint is gone.
func TestQuitHintHidesTheCursor(t *testing.T) {
	ui := newInputTestUI(t)
	ui.renderer.FocusRenderable(ui.input)
	root := terminal.NewRootRenderable(60, 5)
	root.Add(ui.inputWrapper)
	cursor := &cursorRecorder{}
	ui.input.SetHost(cursor)
	draw := func() {
		root.Render(buffer.New(60, 5), 0)
	}

	draw()
	if !cursor.Visible {
		t.Fatal("the empty input without a hint hides its cursor")
	}
	ui.service.state.LastCtrlCTime = time.Now()
	ui.SyncInputHint()
	draw()
	if cursor.Visible {
		t.Errorf("the cursor shows at %d,%d over the quit hint, want it hidden", cursor.X, cursor.Y)
	}
	ui.SetInputHint("")
	draw()
	if !cursor.Visible {
		t.Error("the cursor stayed hidden once the hint was gone")
	}
}

// TestInputShowsQuitHint checks that an idle Ctrl+C shows the key that quits
// in the empty input, in the grey of the other bracketed hints, while a
// second press quits, and that the input drops it once QuitWindow has
// passed.
func TestInputShowsQuitHint(t *testing.T) {
	ui := newBuiltTestUI(t, true)
	synctest.Test(t, func(t *testing.T) {
		// work stands in for the event loop, which runs what the timer
		// enqueues.
		work := make(chan func())
		enqueue := func(fn func()) { work <- fn }

		_, out := ui.service.HandleCtrlC(false)
		if !slices.Contains(out, UIEvent(EventQuitArmed{})) {
			t.Fatalf("idle Ctrl+C = %v, want it to arm the quit", out)
		}
		ui.ShowQuitHint(enqueue)
		if hint, fg := ui.input.EditSurface().PlaceholderText(); hint != QuitHint || fg != PanelDimFg {
			t.Errorf("armed: input hint %q in %v, want %q in PanelDimFg", hint, fg, QuitHint)
		}

		time.Sleep(QuitWindow - time.Nanosecond)
		synctest.Wait()
		select {
		case <-work:
			t.Error("the hint went before QuitWindow passed")
		default:
		}

		time.Sleep(time.Nanosecond)
		(<-work)()
		if hint, _ := ui.input.EditSurface().PlaceholderText(); hint != "" {
			t.Errorf("lapsed: input hint %q still shown, want none", hint)
		}
	})
}
