package ui

import (
	"io"
	"slices"
	"testing"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/input"
	"github.com/unaquatic/fluid/render"
	"github.com/unaquatic/fluid/terminal"
	"github.com/unaquatic/fluid/widget"
)

// newBuiltTestUI returns a UI built by BuildUI over a service with empty
// state. Inline sets the transcript mode. What the renderer writes, such as
// the progress on the terminal tab, is discarded rather than reaching the
// test's output.
func newBuiltTestUI(t *testing.T, inline bool) *UI {
	t.Helper()
	r, err := terminal.NewRenderer(terminal.Config{Stdout: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	ui := &UI{
		renderer:       r,
		theme:          DefaultTheme(),
		service:        &Service{state: &UIState{}, Commands: builtinCommands(t)},
		extensions:     builtinExtensions(t),
		inline:         inline,
		messageWidgets: make(map[message.ID]*ChatMessageBox),
	}
	ui.BuildUI()
	return ui
}

func newBannerTestUI(t *testing.T) *UI {
	t.Helper()
	return newBuiltTestUI(t, false)
}

// TestBannerOpensInTheInputBox checks that a banner opens at the top of the
// input box, and that a banner without a search hides the input, which takes
// no keys, until it closes.
func TestBannerOpensInTheInputBox(t *testing.T) {
	ui := newBannerTestUI(t)
	ui.renderer.FocusRenderable(ui.input)

	ui.OnBannerShow(EventBannerShow{Title: "Login", Items: []widget.SelectItem{{Name: "provider-a"}}})
	rows := ui.inputMiddleCol.Children()
	if slices.Index(rows, render.Renderable(ui.bannerPanel)) > slices.Index(rows, render.Renderable(ui.inputContentRow)) {
		t.Error("banner is not above the input row of the input box")
	}
	if ui.inputContentRow.Visible() || ui.input.Focused() {
		t.Error("input row is on screen or focused under a banner without a search")
	}
	if got, want := ui.bannerHeader.PlainText(), "Login [↑/↓ move] [esc cancel]"; got != want {
		t.Errorf("title = %q, want %q", got, want)
	}

	ui.OnBannerHide(EventBannerHide{})
	if ui.bannerPanel.Visible() || !ui.inputContentRow.Visible() {
		t.Error("banner closed without restoring the input row")
	}
	if !ui.input.Focused() {
		t.Error("input did not regain the focus")
	}
}

func TestBannerMenuAfterEmptyModels(t *testing.T) {
	ui := newBannerTestUI(t)
	root := ui.renderer.Root().LayoutNode()
	providers := func() []widget.SelectItem {
		return []widget.SelectItem{{Name: "provider-a"}, {Name: "provider-b"}}
	}
	ui.OnBannerShow(EventBannerShow{Title: "Login", Items: providers()})
	root.ComputeLayout(80, 24)
	ui.OnBannerHide(EventBannerHide{})
	root.ComputeLayout(80, 24)
	ui.OnBannerShow(EventBannerShow{Title: "Loading models..."})
	root.ComputeLayout(80, 24)
	if ui.bannerMenu.SelectedItem() != nil {
		t.Error("empty model menu retained a selected option")
	}
	if node := ui.bannerMenu.LayoutNode(); !ui.bannerMenu.Visible() || node.Computed.Height != 0 {
		t.Error("empty model menu must stay in the layout at zero height")
	}
	ui.OnBannerHide(EventBannerHide{})
	root.ComputeLayout(80, 24)
	ui.OnBannerShow(EventBannerShow{Title: "Login", Items: providers()})
	root.ComputeLayout(80, 24)
	if got := ui.bannerMenu.LayoutNode().Computed.Height; got != 2 {
		t.Errorf("reopened provider menu height = %d, want 2", got)
	}
	if !ui.bannerMenu.Visible() {
		t.Error("reopened provider menu is not visible")
	}
}

// TestBannerSearchesFromTheInput checks that the input's text narrows a
// banner menu, and that the query leaves the input with the banner.
func TestBannerSearchesFromTheInput(t *testing.T) {
	ui := newBannerTestUI(t)
	ui.renderer.FocusRenderable(ui.input)
	items := []MenuItem{{Name: "provider-a/lab-a/model-a"}, {Name: "provider-b/lab-b/model-b"}}
	ui.Apply(ui.service.ShowSearchMenu(&SearchMenu{Items: func() []MenuItem { return items }}))
	if !ui.inputContentRow.Visible() || !ui.input.Focused() {
		t.Fatal("input is not on screen and focused for the search")
	}

	letter := &input.KeyEvent{Name: "p", Text: "p"}
	ui.HandleBannerKey(letter)
	if letter.DefaultPrevented() {
		t.Error("banner kept a letter from the input")
	}
	down := &input.KeyEvent{Name: input.KeyDown}
	ui.HandleBannerKey(down)
	if !down.DefaultPrevented() {
		t.Error("banner passed the down arrow to the input")
	}

	ui.input.InsertText("ma")
	if got := ui.bannerMenu.ItemCount(); got != 1 {
		t.Errorf("menu shows %d rows for \"ma\", want 1", got)
	}
	// Emptying the input reports no change, so clearing it widens the menu.
	ui.Apply(ui.service.ClearInput())
	if got := ui.bannerMenu.ItemCount(); got != len(items) {
		t.Errorf("menu shows %d rows after the input was cleared, want %d", got, len(items))
	}

	ui.input.InsertText("pa")
	ui.Apply(ui.service.CancelBanner())
	if got := ui.input.Text(); got != "" {
		t.Errorf("input kept the query %q after the banner closed", got)
	}
	if !ui.input.Focused() {
		t.Error("input lost the focus")
	}

	ui.OnBannerShow(EventBannerShow{Title: "Login", Items: []widget.SelectItem{{Name: "provider-a"}}})
	letter = &input.KeyEvent{Name: "g", Text: "g"}
	ui.HandleBannerKey(letter)
	if !letter.DefaultPrevented() {
		t.Error("banner without search passed a letter to the hidden input")
	}
}

// TestBannerArrowsChangeSetting checks that ←/→ without modifiers change the
// value of the selected setting and keep it selected, and that they go to the
// input, which moves its cursor, with a modifier and in a menu whose rows have
// no values.
func TestBannerArrowsChangeSetting(t *testing.T) {
	ui := newBannerTestUI(t)
	// The cache usage row shows while debug is on.
	ui.service.state.Debug = true
	ui.Apply(ui.service.ShowSettingsMenu())
	ui.bannerMenu.MoveDown()
	if got := ui.bannerHeader.Keys.Hints; !slices.Contains(got, HintChange) {
		t.Errorf("title keys = %v, want %s among them", got, HintChange)
	}

	for _, tt := range []struct {
		key  *input.KeyEvent
		want CacheMode
	}{
		{key: &input.KeyEvent{Name: input.KeyRight}, want: CachePerTurn},
		{key: &input.KeyEvent{Name: input.KeyLeft}, want: CacheOff},
		{key: &input.KeyEvent{Name: input.KeyLeft}, want: CachePerMessage},
	} {
		ui.HandleBannerKey(tt.key)
		if !tt.key.DefaultPrevented() || ui.service.state.CacheMode != tt.want {
			t.Errorf("%s: mode %s, kept from the input %v; want %s, kept", tt.key.Name, ui.service.state.CacheMode, tt.key.DefaultPrevented(), tt.want)
		}
		if item := ui.bannerMenu.SelectedItem(); item == nil || item.Name != cacheUsageSetting || item.Description != tt.want.String() {
			t.Errorf("%s: selected %+v, want cache usage %s", tt.key.Name, item, tt.want)
		}
	}

	shifted := &input.KeyEvent{Name: input.KeyRight, Mods: input.ModShift}
	ui.HandleBannerKey(shifted)
	if shifted.DefaultPrevented() || ui.service.state.CacheMode != CachePerMessage {
		t.Errorf("shift+right changed the mode to %s or was kept from the input", ui.service.state.CacheMode)
	}

	items := []MenuItem{{Name: "provider-a/lab-a/model-a"}}
	ui.Apply(ui.service.ShowSearchMenu(&SearchMenu{Items: func() []MenuItem { return items }}))
	left := &input.KeyEvent{Name: input.KeyLeft}
	ui.HandleBannerKey(left)
	if left.DefaultPrevented() {
		t.Error("a menu without values kept the left arrow from the input")
	}
	if got := ui.bannerHeader.Keys.Hints; slices.Contains(got, HintChange) {
		t.Errorf("title keys = %v for a menu without values, want no %s", got, HintChange)
	}
}

// TestBannerTabAsksCustomModel checks that Tab in a menu that takes a custom
// entry, as the model menu does, closes the menu and leaves the input empty
// with CustomModelPlaceholder as its hint, and that Escape then cancels the
// question and takes the hint away. Tab in a menu without one goes to the
// input.
func TestBannerTabAsksCustomModel(t *testing.T) {
	ui := newBannerTestUI(t)
	items := []MenuItem{{Name: "model-a", Hint: "[provider-a]"}}
	ui.Apply(ui.service.ShowSearchMenu(&SearchMenu{Title: modelsTitle, Items: func() []MenuItem { return items }, Custom: ui.service.AskCustomModel}))
	if got := ui.bannerHeader.Keys.Hints; !slices.Contains(got, HintCustom) {
		t.Errorf("title keys = %v, want %s among them", got, HintCustom)
	}

	tab := &input.KeyEvent{Name: input.KeyTab}
	ui.HandleKey(tab)
	if !tab.DefaultPrevented() || ui.bannerPanel.Visible() || ui.service.state.Prompt == nil {
		t.Fatalf("tab kept %v, banner shown %v, prompt %v; want kept, the menu closed, and a question open", tab.DefaultPrevented(), ui.bannerPanel.Visible(), ui.service.state.Prompt)
	}
	if hint, _ := ui.input.EditSurface().PlaceholderText(); hint != CustomModelPlaceholder || ui.input.Text() != "" {
		t.Errorf("input %q with hint %q, want it empty with %q", ui.input.Text(), hint, CustomModelPlaceholder)
	}

	ui.HandleKey(&input.KeyEvent{Name: input.KeyEscape})
	if hint, _ := ui.input.EditSurface().PlaceholderText(); hint != "" || ui.service.state.Prompt != nil {
		t.Errorf("after escape: hint %q, prompt %v; want neither", hint, ui.service.state.Prompt)
	}

	ui.Apply(ui.service.ShowSearchMenu(&SearchMenu{Items: func() []MenuItem { return items }}))
	tab = &input.KeyEvent{Name: input.KeyTab}
	ui.HandleBannerKey(tab)
	if tab.DefaultPrevented() || !ui.bannerPanel.Visible() {
		t.Errorf("tab in a menu without a custom entry was kept %v, banner shown %v; want it left to the input", tab.DefaultPrevented(), ui.bannerPanel.Visible())
	}
}

// TestCtrlCClosesPages checks that Ctrl+C closes the page open in the input
// box, also with a search typed: the settings, and the session list with its
// preview. The input is emptied and takes the focus back, and the footer
// drops the page's key hints.
func TestCtrlCClosesPages(t *testing.T) {
	tests := []struct {
		name  string
		open  func(t *testing.T, ui *UI)
		panel func(ui *UI) render.Renderable
	}{
		{name: "settings", open: func(_ *testing.T, ui *UI) {
			ui.Apply(ui.service.ShowSettingsMenu())
		}, panel: func(ui *UI) render.Renderable { return ui.bannerPanel }},
		{name: "resume", open: func(t *testing.T, ui *UI) {
			storePrompts(t, ui.service.app, "Run the tests")
			ui.Apply(ui.service.HandleSubmit("/resume"))
		}, panel: func(ui *UI) render.Renderable { return ui.sessionPanel }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ui := newInputTestUI(t)
			tt.open(t, ui)
			ui.input.InsertText("e")
			if !tt.panel(ui).Visible() {
				t.Fatal("setup: the page is not open")
			}

			key := &input.KeyEvent{Name: "c", Mods: input.ModCtrl}
			ui.HandleKey(key)

			st := ui.service.state
			if !key.DefaultPrevented() || tt.panel(ui).Visible() || st.Banner != NoBanner || st.SessionMenuVisible {
				t.Error("the page is still open after Ctrl+C")
			}
			if ui.input.Text() != "" || !ui.input.Focused() {
				t.Errorf("input = %q, focused %v; want it emptied and focused", ui.input.Text(), ui.input.Focused())
			}
		})
	}
}
