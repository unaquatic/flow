package ui

import (
	"fmt"
	"iter"
	"slices"
	"strings"

	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/internal/fuzzy"
	"github.com/unaquatic/fluid/widget"
)

// settingValueColumn is the column the /settings values start at, which
// keeps them well to the right of the setting names.
const settingValueColumn = 24

// MenuItem is a row of a SearchMenu.
type MenuItem struct {
	// Name is the row's text, which the search matches.
	Name string
	// Aliases are other names the search finds the row by, such as a
	// model's whole ID.
	Aliases []string
	// Hint is shown right after the name in PanelDimFg, on the selected row
	// too, such as "[default]", or is "".
	Hint string
	// Desc is shown in a column after the names.
	Desc string
	// Selected marks the row to select while the search is empty.
	Selected bool
	// Choose runs when the row is chosen.
	Choose func() []UIEvent
	// Change, when set, moves the row's value step places through its
	// values, back for a negative step, as ←/→ do.
	Change func(step int) []UIEvent
}

// SearchMenu is a banner menu that the input narrows as the user types.
type SearchMenu struct {
	// Title heads the menu.
	Title string
	// Items returns the menu's rows in their unsearched order. It runs on
	// every search, so the rows can show state that changed while the menu
	// is open.
	Items func() []MenuItem
	// NameWidth is the least width of the name column that descriptions
	// follow, or zero to fit the column to the names.
	NameWidth int
	// Custom, when set, runs on Tab with Query, for an entry the menu does
	// not list, as the model menu asks for a model typed in, and the banner
	// names Tab with HintCustom.
	Custom func(query string) []UIEvent
	// Query is the text of the input, which searches the menu.
	Query string
	// Shown are the rows the banner shows, best first.
	Shown []MenuItem
}

// Names yields the row's name and then its aliases.
func (item MenuItem) Names() iter.Seq[string] {
	return slices.Values(slices.Concat([]string{item.Name}, item.Aliases))
}

// RankByName returns the items whose name matches query, best first as
// ranked by [fuzzy.Rank], ignoring case and the spaces around query.
func RankByName[T any](query string, items []T, name func(T) string) []T {
	query = strings.ToLower(strings.TrimSpace(query))
	key := func(item T) string { return strings.ToLower(name(item)) }
	return slices.Collect(fuzzy.Rank(query, items, key))
}

// RankByNames returns the items that query matches by any of their names,
// best first, each ranked by its best match as [RankByName] ranks names, and
// on a tie by its place in items.
func RankByNames[T any](query string, items []T, names func(T) iter.Seq[string]) []T {
	type named struct {
		i    int
		name string
	}
	var all []named
	for i, item := range items {
		for name := range names(item) {
			all = append(all, named{i, name})
		}
	}
	seen := make(map[int]bool, len(items))
	var out []T
	for _, n := range RankByName(query, all, func(n named) string { return n.name }) {
		if !seen[n.i] {
			seen[n.i] = true
			out = append(out, items[n.i])
		}
	}
	return out
}

// Search sets m's query and returns the event that shows the matching rows.
func (m *SearchMenu) Search(query string) []UIEvent {
	m.Query = query
	return m.Banner()
}

// Banner returns the event that shows the rows matching m's query, best
// first. While the query is empty it selects the Selected row, and otherwise
// the best match.
func (m *SearchMenu) Banner() []UIEvent {
	return m.BannerOn("")
}

// BannerOn returns the event that shows the rows matching m's query, best
// first, as Banner does, but with the row named name selected while it
// matches, as after the row changed its value. The banner names the arrows
// that change values when any row has values to change.
func (m *SearchMenu) BannerOn(name string) []UIEvent {
	items := m.Items()
	m.Shown = RankByNames(m.Query, items, MenuItem.Names)
	searching := strings.TrimSpace(m.Query) != ""

	menu := EventBannerShow{
		Title:     m.Title,
		Items:     make([]widget.SelectItem, len(m.Shown)),
		NameWidth: m.NameWidth,
		Search:    true,
		Query:     m.Query,
		Change:    slices.ContainsFunc(items, func(item MenuItem) bool { return item.Change != nil }),
		Custom:    m.Custom != nil,
	}
	for i, item := range m.Shown {
		menu.Items[i] = widget.SelectItem{Name: item.Name, Hint: item.Hint, Description: item.Desc, Value: i}
		if item.Selected && !searching || name != "" && item.Name == name {
			menu.SelectedIdx = i
		}
	}
	if len(m.Shown) == 0 {
		menu.Text = NoMatchesText
	}
	return []UIEvent{menu}
}

// Choose runs the shown row that value, an item value from
// [SearchMenu.Banner], identifies.
func (m *SearchMenu) Choose(value any) []UIEvent {
	i, ok := value.(int)
	if !ok || i < 0 || i >= len(m.Shown) {
		return nil
	}
	return m.Shown[i].Choose()
}

// Change moves the value of the shown row that value, an item value from
// [SearchMenu.Banner], identifies step places, as ←/→ do, and reports whether
// the row has values to change.
func (m *SearchMenu) Change(value any, step int) (bool, []UIEvent) {
	i, ok := value.(int)
	if !ok || i < 0 || i >= len(m.Shown) || m.Shown[i].Change == nil {
		return false, nil
	}
	return true, m.Shown[i].Change(step)
}

// ShowSearchMenu opens m in the banner, searched from the emptied input.
func (s *Service) ShowSearchMenu(m *SearchMenu) []UIEvent {
	s.OpenBanner(MenuBanner)
	s.state.Menu = m
	return m.Search("")
}

// OpenBanner records that a banner of kind is open, and drops the menu or
// session tree of the banner it replaces.
func (s *Service) OpenBanner(kind Banner) {
	s.state.Banner = kind
	s.state.Menu = nil
	s.state.Tree = nil
	s.state.Login = nil
	s.state.LoginCompleting = false
}

// CancelBanner hides the active banner. It does not cancel app operations.
func (s *Service) CancelBanner() []UIEvent {
	if s.state.Banner == NoBanner {
		return nil
	}
	login := s.state.Login != nil
	s.OpenBanner(NoBanner)
	out := []UIEvent{EventBannerHide{}}
	if login {
		out = append(out, EventInputHintChanged{})
	}
	return out
}

// Closing returns an action that hides the banner and then runs run.
func (s *Service) Closing(run func() []UIEvent) func() []UIEvent {
	return func() []UIEvent {
		return append(s.CancelBanner(), run()...)
	}
}

// HandleBannerSelect runs the banner row that value identifies.
func (s *Service) HandleBannerSelect(value any) []UIEvent {
	if m := s.state.Menu; m != nil {
		return m.Choose(value)
	}
	return nil
}

// HandleBannerSearch narrows the banner's menu or session tree to the text of
// its search field.
func (s *Service) HandleBannerSearch(query string) []UIEvent {
	if m := s.state.Menu; m != nil {
		return m.Search(query)
	}
	return s.UpdateTree(func(t *SessionTree) { t.Search(query) })
}

// HandleBannerChange moves the value of the banner row that value identifies
// step places, as ←/→ do, and reports whether the row has values to change.
func (s *Service) HandleBannerChange(value any, step int) (bool, []UIEvent) {
	if m := s.state.Menu; m != nil {
		return m.Change(value, step)
	}
	return false, nil
}

// HandleBannerCustom runs the open menu's Custom with its search, as Tab
// does, and reports whether the menu has one.
func (s *Service) HandleBannerCustom() (bool, []UIEvent) {
	m := s.state.Menu
	if m == nil || m.Custom == nil {
		return false, nil
	}
	return true, m.Custom(m.Query)
}

// NoMatchesText takes the place of the rows of a menu, the session list, or
// the session tree when its search matches none.
const NoMatchesText = "[no matches]"

// The names of the /settings rows, capitalized as a menu's words are:
// fullscreenSetting names the row that switches fullscreen and inline mode,
// cacheUsageSetting the row that sets the CacheMode, expandedSetting the row
// that sets the ExpandedStyle of expanded tool calls, toolMarkerSetting the row that shows or hides
// ToolMarker, markerShapeSetting the row that sets its MarkerShape,
// toolColorSetting the row that sets the ToolNameColor,
// keyHintsSetting the row that sets the KeyHintsMode,
// listCountSetting the row that sets the CountPlace, selectionSetting the
// row that sets the SelectionStyle, promptSetting the row that sets the
// PromptStyle, inputSetting the row that sets the InputStyle, and
// debugSetting the row that switches
// Debug.
const (
	fullscreenSetting  = "Fullscreen"
	cacheUsageSetting  = "Cache usage"
	expandedSetting    = "Expanded tools"
	toolMarkerSetting  = "Tool marker"
	markerShapeSetting = "Marker shape"
	toolColorSetting   = "Tool name color"
	keyHintsSetting    = "Key hints"
	listCountSetting   = "List count"
	selectionSetting   = "Selection"
	promptSetting      = "Prompt style"
	inputSetting       = "Input style"
	debugSetting       = "Debug"
)

// ShowSettingsMenu lists the settings with their values, searched from the
// input. Choosing one moves it to its next value, after the last back to the
// first, and ←/→ move it back and forth; the menu stays open with the same
// search, on the setting. Fullscreen is on or off, and debug, the last row,
// on or off; each is saved, as PersistTerminal and PersistDebug save it, so
// that the terminal starts with it next time. While debug is on, the style
// settings show between them, for this terminal alone: cache usage one of
// CacheModes, expanded tools one of ExpandedStyles, the tool marker on or
// off, the marker shape one of MarkerShapes, the tool name color one of
// ToolNameColors, key hints one of KeyHintsModes, list count one of
// CountPlaces, selection one of SelectionStyles, the prompt style one of
// PromptStyles, and the input style one of InputStyles, in their order.
func (s *Service) ShowSettingsMenu() []UIEvent {
	menu := &SearchMenu{Title: "Settings", NameWidth: settingValueColumn}
	setting := func(name, value string, change func(step int) []UIEvent) MenuItem {
		move := func(step int) []UIEvent { return slices.Concat(change(step), menu.BannerOn(name)) }
		return MenuItem{Name: name, Desc: value, Choose: func() []UIEvent { return move(1) }, Change: move}
	}
	menu.Items = func() []MenuItem {
		fullscreen := setting(fullscreenSetting, onOff(!s.state.Inline), func(int) []UIEvent { return s.ToggleInlineMode() })
		debug := setting(debugSetting, onOff(s.state.Debug), func(int) []UIEvent {
			return s.SetDebug(!s.state.Debug)
		})
		if !s.state.Debug {
			return []MenuItem{fullscreen, debug}
		}
		return []MenuItem{
			fullscreen,
			setting(cacheUsageSetting, s.state.CacheMode.String(), func(step int) []UIEvent {
				return s.SetCacheMode(s.state.CacheMode.Next(step))
			}),
			setting(expandedSetting, s.state.ExpandStyle.String(), func(step int) []UIEvent {
				return s.SetExpandStyle(s.state.ExpandStyle.Next(step))
			}),
			setting(toolMarkerSetting, onOff(!s.state.HideToolMarkers), func(int) []UIEvent {
				return s.SetToolMarkers(s.state.HideToolMarkers)
			}),
			setting(markerShapeSetting, s.state.MarkerShape.String(), func(step int) []UIEvent {
				return s.SetMarkerShape(s.state.MarkerShape.Next(step))
			}),
			setting(toolColorSetting, s.state.ToolColor.String(), func(step int) []UIEvent {
				return s.SetToolColor(s.state.ToolColor.Next(step))
			}),
			setting(keyHintsSetting, s.state.KeyHints.String(), func(step int) []UIEvent {
				return s.SetKeyHintsMode(s.state.KeyHints.Next(step))
			}),
			setting(listCountSetting, s.state.Count.String(), func(step int) []UIEvent {
				return s.SetCountPlace(s.state.Count.Next(step))
			}),
			setting(selectionSetting, s.state.Selection.String(), func(step int) []UIEvent {
				return s.SetSelectionStyle(s.state.Selection.Next(step))
			}),
			setting(promptSetting, s.state.PromptStyle.String(), func(step int) []UIEvent {
				return s.SetPromptStyle(s.state.PromptStyle.Next(step))
			}),
			setting(inputSetting, s.state.InputStyle.String(), func(step int) []UIEvent {
				return s.SetInputStyle(s.state.InputStyle.Next(step))
			}),
			debug,
		}
	}
	return s.ShowSearchMenu(menu)
}

// SetCacheMode shows the cache use of every reply in the transcript, and of
// those added later, where mode says.
func (s *Service) SetCacheMode(mode CacheMode) []UIEvent {
	s.state.CacheMode = mode
	return []UIEvent{EventCacheModeSet{Mode: mode}}
}

// SetExpandStyle shows every expanded tool call in the transcript, and those
// added later, as ts says.
func (s *Service) SetExpandStyle(ts ExpandedStyle) []UIEvent {
	s.state.ExpandStyle = ts
	return []UIEvent{EventExpandStyleSet{Style: ts}}
}

// SetToolMarkers shows ToolMarker before the name of every tool call in the
// transcript, and of those added later, or hides it,
// as show says.
func (s *Service) SetToolMarkers(show bool) []UIEvent {
	s.state.HideToolMarkers = !show
	return []UIEvent{EventToolMarkersSet{Show: show}}
}

// SetMarkerShape shows the marker of every tool call in the transcript, and
// of those added later, in m.
func (s *Service) SetMarkerShape(m MarkerShape) []UIEvent {
	s.state.MarkerShape = m
	return []UIEvent{EventMarkerShapeSet{Shape: m}}
}

// SetToolColor shows the marker and the name of every tool call in the
// transcript, and of those added later, in the color
// c names.
func (s *Service) SetToolColor(c ToolNameColor) []UIEvent {
	s.state.ToolColor = c
	return []UIEvent{EventToolColorSet{Color: c}}
}

// SetKeyHintsMode names the keys of every panel where mode says: after its
// title, under it, or in the footer under the input box.
func (s *Service) SetKeyHintsMode(mode KeyHintsMode) []UIEvent {
	s.state.KeyHints = mode
	return []UIEvent{EventKeyHintsModeSet{Mode: mode}}
}

// SetCountPlace counts the rows of every list that does not all fit where
// place says: after its title, or on a line under its rows.
func (s *Service) SetCountPlace(place CountPlace) []UIEvent {
	s.state.Count = place
	return []UIEvent{EventCountPlaceSet{Place: place}}
}

// SetPromptStyle shows every prompt in the transcript, and those added
// later, as style says: on the box background, or with a bar of the
// primary color at its left edge.
func (s *Service) SetPromptStyle(style PromptStyle) []UIEvent {
	s.state.PromptStyle = style
	return []UIEvent{EventPromptStyleSet{Style: style}}
}

// SetInputStyle shows the input box and the panels that open in it as style
// says: on the box background, or on the canvas with a marker at the left
// edge of the input and of each panel's title.
func (s *Service) SetInputStyle(style InputStyle) []UIEvent {
	s.state.InputStyle = style
	return []UIEvent{EventInputStyleSet{Style: style}}
}

// SetSelectionStyle marks the selected row of every menu as style says: with
// a fill across the box, with a fill a cell in from the box's edges, or with
// an arrow.
func (s *Service) SetSelectionStyle(style SelectionStyle) []UIEvent {
	s.state.Selection = style
	return []UIEvent{EventSelectionStyleSet{Style: style}}
}

// SetDebug shows what helps debug flow, in the transcript and in what is
// added to it later, or hides it, as on says: the
// snapshot each prompt and reply ends with, by its hash, once taken.
func (s *Service) SetDebug(on bool) []UIEvent {
	s.state.Debug = on
	return append([]UIEvent{EventDebugSet{On: on}}, s.PersistDebug()...)
}

// onOff renders a setting's value: "On" or "Off".
func onOff(on bool) string {
	if on {
		return "On"
	}
	return "Off"
}

// StartScript starts script, which name names in a toast when it cannot start,
// as when the session has an operation in progress. The run is stored and
// shown like any other: the indicator shows the time it has taken, and the
// keys that act on it.
func (s *Service) StartScript(name string, script app.Script) []UIEvent {
	if err := s.StartRun(func(sess app.Session) error { return sess.Play(script) }); err != nil {
		return s.ShowToast(fmt.Sprintf("%s failed: %v", name, err))
	}
	return slices.Concat(s.ResetError(), s.ShowIndicator(""))
}

// ShowGraphemeReport displays the width diagnostic in the modal banner, which
// esc or enter closes.
func (s *Service) ShowGraphemeReport() []UIEvent {
	s.OpenBanner(GraphemeBanner)
	return []UIEvent{EventBannerShow{
		Title: "Grapheme width",
		Keys:  []KeyHint{{Key: "esc", Action: "close"}},
		Text:  GraphemeReport(s.state.ProbedWidths, s.state.ProbeErr),
	}}
}

// ShowPalette appends the fixed entries of the xterm 256-color palette to the
// transcript, each labeled with its index.
func (s *Service) ShowPalette() []UIEvent {
	return s.AddNotice(Notice{Lines: PaletteLines()})
}
