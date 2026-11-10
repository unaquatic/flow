package ui

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/config"
)

// greekMenu returns a menu of three rows that selects "beta" while its search
// is empty. Choosing a row records its name in *chosen.
func greekMenu(chosen *string) *SearchMenu {
	item := func(name string, selected bool) MenuItem {
		return MenuItem{Name: name, Selected: selected, Choose: func() []UIEvent {
			*chosen = name
			return nil
		}}
	}
	items := []MenuItem{item("alpha", false), item("beta", true), item("gamma", false)}
	return &SearchMenu{Items: func() []MenuItem { return items }}
}

func TestSearchMenu(t *testing.T) {
	all := []string{"alpha", "beta", "gamma"}
	tests := []struct {
		name, query string
		want        []string
		wantIdx     int
		wantText    string
	}{
		{name: "empty selects Selected", query: "", want: all, wantIdx: 1},
		{name: "case and surrounding spaces ignored", query: " GAM ", want: []string{"gamma"}},
		{name: "best match first and selected", query: "a", want: []string{"alpha", "gamma", "beta"}},
		{name: "blank selects Selected", query: "  ", want: all, wantIdx: 1},
		{name: "no match", query: "zzz", wantText: NoMatchesText},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var chosen string
			s := &Service{state: &UIState{}}
			s.ShowSearchMenu(greekMenu(&chosen))

			show := bannerShown(t, s.HandleBannerSearch(tt.query))
			if got := bannerNames(show); !slices.Equal(got, tt.want) || show.SelectedIdx != tt.wantIdx || show.Text != tt.wantText {
				t.Errorf("search %q = %q selecting %d with text %q, want %q selecting %d with text %q",
					tt.query, got, show.SelectedIdx, show.Text, tt.want, tt.wantIdx, tt.wantText)
			}
			if !show.Search || show.Title != "" {
				t.Errorf("banner = %+v, want a search field and no title", show)
			}
		})
	}
}

func TestSearchMenuChoose(t *testing.T) {
	var chosen string
	s := &Service{state: &UIState{}}
	s.ShowSearchMenu(greekMenu(&chosen))
	show := bannerShown(t, s.HandleBannerSearch("gam"))

	s.HandleBannerSelect(show.Items[0].Value)
	if chosen != "gamma" {
		t.Errorf("choosing the first match ran %q, want gamma", chosen)
	}
	for _, value := range []any{-1, 1, "gamma"} {
		chosen = ""
		if out := s.HandleBannerSelect(value); out != nil || chosen != "" {
			t.Errorf("choosing %v ran %q with %v, want nothing", value, chosen, out)
		}
	}
}

func TestSettingsMenuToggleKeepsSearch(t *testing.T) {
	s := &Service{state: &UIState{Inline: true}}
	show := bannerShown(t, s.HandleSubmit("/config"))
	if show.Items[0].Description != "Off" || show.NameWidth != settingValueColumn {
		t.Fatalf("fullscreen = %q in a %d-wide name column, want Off in %d", show.Items[0].Description, show.NameWidth, settingValueColumn)
	}

	bannerShown(t, s.HandleBannerSearch("full"))
	out := choose(fullscreenSetting)(s)
	show = bannerShown(t, out)
	if !slices.ContainsFunc(out, func(ev UIEvent) bool { _, ok := ev.(EventInlineModeSet); return ok }) {
		t.Error("choosing fullscreen did not switch the mode")
	}
	if len(show.Items) != 1 || show.Items[0].Description != "On" || show.SelectedIdx != 0 {
		t.Errorf("menu after toggle = %+v, want fullscreen on and selected", show.Items)
	}
	if s.state.Banner != MenuBanner || s.state.Menu == nil || s.state.Menu.Query != "full" {
		t.Errorf("after the toggle banner = %v, menu = %+v, want the menu open with its search", s.state.Banner, s.state.Menu)
	}
}

// TestSettingsMenuKeyHints checks that /config names the panels' keys
// after their titles by default, and that choosing the setting moves them
// under the titles, to the footer, and back, keeping the menu open on the
// setting.
func TestSettingsMenuKeyHints(t *testing.T) {
	s := &Service{state: &UIState{Debug: true}}
	settings := bannerShown(t, s.HandleSubmit("/config"))
	row := slices.Index(bannerNames(settings), keyHintsSetting)
	if row < 0 || settings.Items[row].Description != KeyHintsTitle.String() {
		t.Fatalf("settings = %+v, want key hints title", settings.Items)
	}
	for _, want := range []KeyHintsMode{KeyHintsSubtitle, KeyHintsFooter, KeyHintsTitle} {
		out := choose(keyHintsSetting)(s)
		if !slices.Contains(out, UIEvent(EventKeyHintsModeSet{Mode: want})) || s.state.KeyHints != want {
			t.Errorf("events %v with mode %s, want %s", out, s.state.KeyHints, want)
		}
		settings = bannerShown(t, out)
		if settings.SelectedIdx != row || settings.Items[row].Description != want.String() {
			t.Errorf("banner selects %d of %+v, want key hints %s selected", settings.SelectedIdx, settings.Items, want)
		}
	}
}

// TestSettingsMenuListCount checks that /config counts a list's rows below
// them by default, and that choosing the setting moves the count after the
// list's title and back, keeping the menu open on the setting.
func TestSettingsMenuListCount(t *testing.T) {
	s := &Service{state: &UIState{Debug: true}}
	settings := bannerShown(t, s.HandleSubmit("/config"))
	row := slices.Index(bannerNames(settings), listCountSetting)
	if row < 0 || settings.Items[row].Description != CountBelow.String() {
		t.Fatalf("settings = %+v, want list count below", settings.Items)
	}
	for _, want := range []CountPlace{CountInTitle, CountBelow} {
		out := choose(listCountSetting)(s)
		if !slices.Contains(out, UIEvent(EventCountPlaceSet{Place: want})) || s.state.Count != want {
			t.Errorf("events %v with place %s, want %s", out, s.state.Count, want)
		}
		settings = bannerShown(t, out)
		if settings.SelectedIdx != row || settings.Items[row].Description != want.String() {
			t.Errorf("banner selects %d of %+v, want list count %s selected", settings.SelectedIdx, settings.Items, want)
		}
	}
}

// TestSettingsMenuSelection checks that /config fills a menu's selected row
// across the box by default, after the list count, and that choosing the
// setting fills it a cell in from the box's edges instead, then marks it with
// an arrow, and fills it across the box again, keeping the menu open on the
// setting.
func TestSettingsMenuSelection(t *testing.T) {
	s := &Service{state: &UIState{Debug: true}}
	settings := bannerShown(t, s.HandleSubmit("/config"))
	names := bannerNames(settings)
	row := slices.Index(names, selectionSetting)
	if row < 0 || row != slices.Index(names, listCountSetting)+1 || settings.Items[row].Description != SelectionFullWidth.String() {
		t.Fatalf("settings = %+v, want selection full width after the list count", settings.Items)
	}
	for _, want := range []SelectionStyle{SelectionFill, SelectionArrow, SelectionFullWidth} {
		out := choose(selectionSetting)(s)
		if !slices.Contains(out, UIEvent(EventSelectionStyleSet{Style: want})) || s.state.Selection != want {
			t.Errorf("events %v with style %s, want %s", out, s.state.Selection, want)
		}
		settings = bannerShown(t, out)
		if settings.SelectedIdx != row || settings.Items[row].Description != want.String() {
			t.Errorf("banner selects %d of %+v, want selection %s selected", settings.SelectedIdx, settings.Items, want)
		}
	}
}

// TestSettingsMenuExpandedTools checks that /config shows expanded tool
// calls plain by default, after the cache usage, and that choosing the
// setting makes them boxed and plain again, keeping the menu open on the
// setting.
func TestSettingsMenuExpandedTools(t *testing.T) {
	s := &Service{state: &UIState{Debug: true}}
	settings := bannerShown(t, s.HandleSubmit("/config"))
	names := bannerNames(settings)
	row := slices.Index(names, expandedSetting)
	if row < 0 || row != slices.Index(names, cacheUsageSetting)+1 || settings.Items[row].Description != ExpandedPlain.String() {
		t.Fatalf("settings = %+v, want expanded tools plain after the cache usage", settings.Items)
	}
	for _, want := range []ExpandedStyle{ExpandedBoxed, ExpandedPlain} {
		out := choose(expandedSetting)(s)
		if !slices.Contains(out, UIEvent(EventExpandStyleSet{Style: want})) || s.state.ExpandStyle != want {
			t.Errorf("events %v with style %s, want %s", out, s.state.ExpandStyle, want)
		}
		settings = bannerShown(t, out)
		if settings.SelectedIdx != row || settings.Items[row].Description != want.String() {
			t.Errorf("banner selects %d of %+v, want expanded tools %s selected", settings.SelectedIdx, settings.Items, want)
		}
	}
}

// TestSettingsMenuToolMarker checks that /config shows the tool marker on
// by default, after the expanded tools, and that choosing the setting hides it
// and shows it again, keeping the menu open on the setting.
func TestSettingsMenuToolMarker(t *testing.T) {
	s := &Service{state: &UIState{Debug: true}}
	settings := bannerShown(t, s.HandleSubmit("/config"))
	names := bannerNames(settings)
	row := slices.Index(names, toolMarkerSetting)
	if row < 0 || row != slices.Index(names, expandedSetting)+1 || settings.Items[row].Description != "On" {
		t.Fatalf("settings = %+v, want the tool marker on after the expanded tools", settings.Items)
	}
	for _, show := range []bool{false, true} {
		out := choose(toolMarkerSetting)(s)
		if !slices.Contains(out, UIEvent(EventToolMarkersSet{Show: show})) || s.state.HideToolMarkers == show {
			t.Errorf("events %v with markers hidden %v, want them shown %v", out, s.state.HideToolMarkers, show)
		}
		settings = bannerShown(t, out)
		if settings.SelectedIdx != row || settings.Items[row].Description != onOff(show) {
			t.Errorf("banner selects %d of %+v, want the tool marker %s selected", settings.SelectedIdx, settings.Items, onOff(show))
		}
	}
}

// TestSettingsMenuToolColor checks that /config shows the tool name color
// gray by default, after the tool marker, and that choosing the setting
// makes it white and gray again, keeping the menu open on the setting.
func TestSettingsMenuToolColor(t *testing.T) {
	s := &Service{state: &UIState{Debug: true}}
	settings := bannerShown(t, s.HandleSubmit("/config"))
	names := bannerNames(settings)
	row := slices.Index(names, toolColorSetting)
	if row < 0 || row != slices.Index(names, markerShapeSetting)+1 || settings.Items[row].Description != ToolNameGray.String() {
		t.Fatalf("settings = %+v, want the tool name color gray after the marker shape", settings.Items)
	}
	for _, want := range []ToolNameColor{ToolNameWhite, ToolNameGray} {
		out := choose(toolColorSetting)(s)
		if !slices.Contains(out, UIEvent(EventToolColorSet{Color: want})) || s.state.ToolColor != want {
			t.Errorf("events %v with color %s, want %s", out, s.state.ToolColor, want)
		}
		settings = bannerShown(t, out)
		if settings.SelectedIdx != row || settings.Items[row].Description != want.String() {
			t.Errorf("banner selects %d of %+v, want the tool name color %s selected", settings.SelectedIdx, settings.Items, want)
		}
	}
}

// TestSettingsMenuMarkerShape checks that /config shows the marker shape
// as a filled square by default, after the tool marker, and that choosing
// the setting goes through every shape and back, keeping the menu open on
// the setting.
func TestSettingsMenuMarkerShape(t *testing.T) {
	s := &Service{state: &UIState{Debug: true}}
	settings := bannerShown(t, s.HandleSubmit("/config"))
	names := bannerNames(settings)
	row := slices.Index(names, markerShapeSetting)
	if row < 0 || row != slices.Index(names, toolMarkerSetting)+1 || settings.Items[row].Description != "■ Square" {
		t.Fatalf("settings = %+v, want the marker shape \"■ Square\" after the tool marker", settings.Items)
	}
	for _, want := range append(MarkerShapes[1:], MarkerSquare) {
		out := choose(markerShapeSetting)(s)
		if !slices.Contains(out, UIEvent(EventMarkerShapeSet{Shape: want})) || s.state.MarkerShape != want {
			t.Errorf("events %v with shape %s, want %s", out, s.state.MarkerShape, want)
		}
		settings = bannerShown(t, out)
		if settings.SelectedIdx != row || settings.Items[row].Description != want.String() {
			t.Errorf("banner selects %d of %+v, want the marker shape %s selected", settings.SelectedIdx, settings.Items, want)
		}
	}
}

// TestSettingsMenuPromptStyle checks that /config shows prompts by a bar by
// default, after the selection, and that choosing the setting fills them and
// shows them by a bar again, keeping the menu open on the setting.
func TestSettingsMenuPromptStyle(t *testing.T) {
	s := &Service{state: &UIState{Debug: true}}
	settings := bannerShown(t, s.HandleSubmit("/config"))
	names := bannerNames(settings)
	row := slices.Index(names, promptSetting)
	if row < 0 || row != slices.Index(names, selectionSetting)+1 || settings.Items[row].Description != PromptBar.String() {
		t.Fatalf("settings = %+v, want the prompt style a bar after the selection", settings.Items)
	}
	for _, want := range []PromptStyle{PromptFilled, PromptBar} {
		out := choose(promptSetting)(s)
		if !slices.Contains(out, UIEvent(EventPromptStyleSet{Style: want})) || s.state.PromptStyle != want {
			t.Errorf("events %v with style %s, want %s", out, s.state.PromptStyle, want)
		}
		settings = bannerShown(t, out)
		if settings.SelectedIdx != row || settings.Items[row].Description != want.String() {
			t.Errorf("banner selects %d of %+v, want the prompt style %s selected", settings.SelectedIdx, settings.Items, want)
		}
	}
}

// TestSettingsMenuInputStyle checks that /config shows the input by a bar
// by default, after the prompt style, and that choosing the setting fills it
// and shows it by a bar again, keeping the menu open on the setting.
func TestSettingsMenuInputStyle(t *testing.T) {
	s := &Service{state: &UIState{Debug: true}}
	settings := bannerShown(t, s.HandleSubmit("/config"))
	names := bannerNames(settings)
	row := slices.Index(names, inputSetting)
	if row < 0 || row != slices.Index(names, promptSetting)+1 || settings.Items[row].Description != InputBar.String() {
		t.Fatalf("settings = %+v, want the input style a bar after the prompt style", settings.Items)
	}
	for _, want := range []InputStyle{InputFilled, InputBar} {
		out := choose(inputSetting)(s)
		if !slices.Contains(out, UIEvent(EventInputStyleSet{Style: want})) || s.state.InputStyle != want {
			t.Errorf("events %v with style %s, want %s", out, s.state.InputStyle, want)
		}
		settings = bannerShown(t, out)
		if settings.SelectedIdx != row || settings.Items[row].Description != want.String() {
			t.Errorf("banner selects %d of %+v, want the input style %s selected", settings.SelectedIdx, settings.Items, want)
		}
	}
}

// TestSettingsMenuDebug checks that /config shows fullscreen and debug
// alone while debug is off, debug off by default as the last row, that
// choosing it switches debug on and shows the style settings between the
// two, and that choosing it again hides them, keeping the menu open on the
// setting.
func TestSettingsMenuDebug(t *testing.T) {
	s := &Service{state: &UIState{}}
	settings := bannerShown(t, s.HandleSubmit("/config"))
	plain := []string{fullscreenSetting, debugSetting}
	if names := bannerNames(settings); !slices.Equal(names, plain) || settings.Items[1].Description != "Off" {
		t.Fatalf("settings = %+v, want %q with debug off", settings.Items, plain)
	}
	debugged := []string{fullscreenSetting, cacheUsageSetting, expandedSetting, toolMarkerSetting, markerShapeSetting,
		toolColorSetting, keyHintsSetting, listCountSetting, selectionSetting, promptSetting, inputSetting, debugSetting}
	for _, tt := range []struct {
		on    bool
		names []string
	}{{true, debugged}, {false, plain}} {
		out := choose(debugSetting)(s)
		if !slices.Contains(out, UIEvent(EventDebugSet{On: tt.on})) || s.state.Debug != tt.on {
			t.Errorf("events %v with debug %v, want %v", out, s.state.Debug, tt.on)
		}
		settings = bannerShown(t, out)
		row := len(tt.names) - 1
		if names := bannerNames(settings); !slices.Equal(names, tt.names) ||
			settings.SelectedIdx != row || settings.Items[row].Description != onOff(tt.on) {
			t.Errorf("banner selects %d of %+v, want %q with debug %s selected", settings.SelectedIdx, settings.Items, tt.names, onOff(tt.on))
		}
	}
}

// TestSettingsPersist checks that fullscreen and debug are saved in the
// config file as /config changes them, that the style settings are not,
// and that the next terminal starts with the saved settings and the style
// settings' defaults.
func TestSettingsPersist(t *testing.T) {
	t.Setenv("DEBUG", "")
	s, a := newTestService(t)
	// The terminal starts inline when nothing is saved.
	s.state.Inline = true
	for _, change := range []func() []UIEvent{
		s.ToggleInlineMode,
		func() []UIEvent { return s.SetDebug(true) },
		func() []UIEvent { return s.SetCacheMode(CachePerMessage) },
		func() []UIEvent { return s.SetExpandStyle(ExpandedBoxed) },
		func() []UIEvent { return s.SetToolMarkers(false) },
		func() []UIEvent { return s.SetMarkerShape(MarkerCircleOutline) },
		func() []UIEvent { return s.SetToolColor(ToolNameWhite) },
		func() []UIEvent { return s.SetKeyHintsMode(KeyHintsFooter) },
		func() []UIEvent { return s.SetCountPlace(CountInTitle) },
		func() []UIEvent { return s.SetSelectionStyle(SelectionArrow) },
		func() []UIEvent { return s.SetPromptStyle(PromptFilled) },
		func() []UIEvent { return s.SetInputStyle(InputFilled) },
	} {
		for _, ev := range change() {
			if toast, ok := toastOf(ev); ok {
				t.Fatalf("change shows %q, want it saved", toast.Text)
			}
		}
	}
	conf, err := a.Config()
	if err != nil {
		t.Fatal(err)
	}
	var next UIState
	next.ApplyConfig(conf.Terminal, conf.Debug)
	if want := (UIState{Inline: false, Debug: true}); !reflect.DeepEqual(next, want) {
		t.Errorf("next terminal starts with %+v, want fullscreen, debug on, and the style defaults", next)
	}
}

// failingSettings fails to save any setting.
type failingSettings struct{}

func (failingSettings) SaveTerminal(config.Terminal) error { return errors.New("disk full") }
func (failingSettings) SaveDebug(bool) error               { return errors.New("disk full") }

// TestSettingsPersistFailure checks that a setting that cannot be saved
// applies all the same, and that a toast reports the failure.
func TestSettingsPersistFailure(t *testing.T) {
	s := &Service{state: &UIState{}, Settings: failingSettings{}}
	for name, out := range map[string][]UIEvent{
		"fullscreen": s.ToggleInlineMode(),
		"debug":      s.SetDebug(true),
	} {
		toasts := 0
		for _, ev := range out {
			if toast, ok := toastOf(ev); ok && toast.Text == "Saving settings failed: disk full" {
				toasts++
			}
		}
		if toasts != 1 || len(out) != 2 {
			t.Errorf("%s: events %v, want the change and one toast that reports the failure", name, out)
		}
	}
	if !s.state.Inline || !s.state.Debug {
		t.Errorf("inline %v, debug %v, want both changed", s.state.Inline, s.state.Debug)
	}
}

// TestSettingsMenuCacheUsage checks that /config shows the cache usage off
// by default, that choosing it moves it to the next of off, per turn, and
// per message, after the last back to the first, that ←/→ move it back and
// forth, and that the menu stays open on the cache usage row, which shows
// the mode in use.
func TestSettingsMenuCacheUsage(t *testing.T) {
	s := &Service{state: &UIState{Debug: true}}
	settings := bannerShown(t, s.HandleSubmit("/config"))
	row := slices.Index(bannerNames(settings), cacheUsageSetting)
	if row < 0 || settings.Items[row].Description != CacheOff.String() {
		t.Fatalf("settings = %+v, want cache usage off", settings.Items)
	}
	if !settings.Change {
		t.Error("settings do not name the arrows that change a setting")
	}
	right := func(s *Service) []UIEvent { _, out := s.HandleBannerChange(row, 1); return out }
	left := func(s *Service) []UIEvent { _, out := s.HandleBannerChange(row, -1); return out }
	tests := []struct {
		name string
		step step
		want CacheMode
	}{
		{name: "choose", step: choose(cacheUsageSetting), want: CachePerTurn},
		{name: "choose again", step: choose(cacheUsageSetting), want: CachePerMessage},
		{name: "choose after the last", step: choose(cacheUsageSetting), want: CacheOff},
		{name: "right", step: right, want: CachePerTurn},
		{name: "left", step: left, want: CacheOff},
		{name: "left before the first", step: left, want: CachePerMessage},
		{name: "right after the last", step: right, want: CacheOff},
	}
	for _, tt := range tests {
		out := tt.step(s)
		if !slices.Contains(out, UIEvent(EventCacheModeSet{Mode: tt.want})) || s.state.CacheMode != tt.want {
			t.Errorf("%s: events %v with mode %s, want %s", tt.name, out, s.state.CacheMode, tt.want)
		}
		settings = bannerShown(t, out)
		if settings.Title != "Settings" || settings.SelectedIdx != row || settings.Items[row].Description != tt.want.String() {
			t.Errorf("%s: banner %q selects %d of %+v, want cache usage %s selected",
				tt.name, settings.Title, settings.SelectedIdx, settings.Items, tt.want)
		}
	}
	if changed, out := s.HandleBannerChange(len(settings.Items), 1); changed || out != nil {
		t.Errorf("changing a row past the menu = %v, %v; want nothing", changed, out)
	}
}

// TestCtrlCClosesMenus checks that Ctrl+C closes every page that opens in the
// input box, as Esc does, with or without a search: the menus, the session
// tree, the grapheme report, and the session list. Closing one does not
// count toward quitting, so the next press only arms the quit.
func TestCtrlCClosesMenus(t *testing.T) {
	tests := []struct {
		name string
		open func(t *testing.T, s *Service, a *app.App)
		// hide is the event that closes the page.
		hide UIEvent
	}{
		{name: "settings", open: func(_ *testing.T, s *Service, _ *app.App) { s.HandleSubmit("/config") }, hide: EventBannerHide{}},
		{name: "settings searched", open: func(_ *testing.T, s *Service, _ *app.App) {
			s.HandleSubmit("/config")
			s.HandleBannerSearch("cache")
		}, hide: EventBannerHide{}},
		{name: "model", open: func(_ *testing.T, s *Service, _ *app.App) { s.HandleSubmit("/model") }, hide: EventBannerHide{}},
		{name: "login", open: func(_ *testing.T, s *Service, _ *app.App) { s.HandleSubmit("/login") }, hide: EventBannerHide{}},
		{name: "demo", open: func(_ *testing.T, s *Service, _ *app.App) { s.HandleSubmit("/demo") }, hide: EventBannerHide{}},
		{name: "grapheme report", open: func(_ *testing.T, s *Service, _ *app.App) { s.ShowGraphemeReport() }, hide: EventBannerHide{}},
		{name: "tree", open: func(_ *testing.T, s *Service, _ *app.App) {
			s.OpenTree(app.Tree{Messages: []message.Content{{ID: message.NewID(), Role: message.RoleUser}}})
		}, hide: EventBannerHide{}},
		{name: "resume", open: func(t *testing.T, s *Service, a *app.App) {
			storePrompts(t, a, "Run the tests")
			s.HandleSubmit("/resume")
			if !s.state.SessionMenuVisible {
				t.Fatal("setup: /resume did not open the session list")
			}
		}, hide: EventSessionMenuHide{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, a := newTestService(t)
			tt.open(t, s, a)
			handled, out := s.HandleCtrlC(s.state.Menu != nil && s.state.Menu.Query != "")
			if !handled || !slices.Contains(out, tt.hide) {
				t.Errorf("Ctrl+C = %v, %v; want %T", handled, out, tt.hide)
			}
			if s.state.Banner != NoBanner || s.state.Menu != nil || s.state.SessionMenuVisible {
				t.Errorf("after Ctrl+C banner %v, menu %v, session list %v; want all closed",
					s.state.Banner, s.state.Menu, s.state.SessionMenuVisible)
			}
			if slices.Contains(out, UIEvent(EventQuit{})) || !s.state.LastCtrlCTime.IsZero() {
				t.Error("closing the page counted toward quitting")
			}
			if _, out := s.HandleCtrlC(false); !slices.Contains(out, UIEvent(EventQuitArmed{})) {
				t.Error("the press after the page closed quit, want it to arm the quit")
			}
		})
	}
}

// TestMenusSearchUnderTitle checks that every search menu opens under its
// title, with the key hints of a search: esc, the arrows, and typing, and in
// the settings, between them, the arrows that change a setting.
func TestMenusSearchUnderTitle(t *testing.T) {
	demo := func(s *Service) []UIEvent { return runCommand(t, s, "demo") }
	tests := []struct {
		name  string
		show  func(s *Service) []UIEvent
		title string
		// change marks a menu whose rows ←/→ change.
		change bool
	}{
		{name: "settings", show: (*Service).ShowSettingsMenu, title: "Settings", change: true},
		{name: "demo", show: demo, title: "Demos"},
		{name: "providers", show: (*Service).ShowLoginMenu, title: "Login"},
		{name: "accounts", show: func(s *Service) []UIEvent { return s.ShowAccountMenu(providerA) },
			title: "Provider A accounts"},
		{name: "login methods", show: func(s *Service) []UIEvent { return s.ShowLoginMethodMenu(providerA) },
			title: "New Provider A account"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := newTestService(t)
			show := bannerShown(t, tt.show(s))
			if !show.Search || show.Title != tt.title || len(show.Items) == 0 {
				t.Errorf("banner = %+v, want rows under a search field titled %q", show, tt.title)
			}
			want := []KeyHint{HintCancel, HintMove, HintSearch}
			title := []KeyHint{HintMove, HintCancel}
			if tt.change {
				want = []KeyHint{HintCancel, HintMove, HintChange, HintSearch}
				title = []KeyHint{HintMove, HintChange, HintCancel}
			}
			if got := show.Hints(); !slices.Equal(got, want) {
				t.Errorf("keys = %v, want %v", got, want)
			}
			if got := TitleHints(show.Hints()); !slices.Equal(got, title) {
				t.Errorf("title keys = %v, want %v", got, title)
			}
		})
	}
}

func TestDemoMenuGrapheme(t *testing.T) {
	s := &Service{state: &UIState{}}
	runCommand(t, s, "demo")

	show := bannerShown(t, choose("Grapheme")(s))
	if s.state.Banner != GraphemeBanner || s.state.Menu != nil || show.Title != "Grapheme width" {
		t.Errorf("banner = %v with menu %+v showing %q, want the grapheme report", s.state.Banner, s.state.Menu, show.Title)
	}
}
