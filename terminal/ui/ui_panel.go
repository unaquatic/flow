package ui

import (
	"fmt"
	"iter"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/render"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/textbuf"
	"github.com/unaquatic/fluid/widget"
)

// Colors of the panels that open at the top of the input box, on its fill:
// the slash menu, the session list, banners, and the session tree.
var (
	// titleFg colors a panel's title, such as "Commands", the names in the
	// left column of a menu's rows but the selected one's, such as
	// "/resume" or "Fullscreen", the values of the Usage box, and the label
	// a patch's header starts with, "changes", as TitleChunk gives it.
	titleFg = textFg
	// activeFg colors the selected row of a menu or of the session tree, and
	// the arrow that marks it, or fills a menu's selected row under
	// SelectionFill and SelectionFullWidth, and fills the input's selected
	// text: productFg, the product's primary color.
	activeFg = productFg
)

// Menus yields the menus in the input box: the slash menu, the session list,
// and the banner's menu.
func (ui *UI) Menus() iter.Seq[*widget.Select] {
	return slices.Values([]*widget.Select{ui.slashMenu, ui.sessionMenu, ui.bannerMenu})
}

// MarkSelection returns menu's RenderAfterHook, which marks its selected row
// as the SelectionStyle in use says once menu has drawn itself as
// SelectionArrow says. menu spans the box's whole row, as BuildMenuRow lays it
// out, and the hook blanks its last PaddingX columns, so that its text ends
// PaddingX cells from the box's right edge, as the text of the rows around it
// does. Under a style that fills the row, it then takes the arrow off the
// selected row and fills the row in activeFg, as many cells in from either
// edge as SelectionStyle.FillInset says, under its text in the box's fill.
//
// The hook draws within menu's bounds, which menu repaints whenever its
// selection moves. A frame can repaint less of them: the region it repaints
// is as wide as the cells that changed, such as the worked time and the
// cursor while a turn runs, and can start past the box's left border cell,
// where menu draws the arrow. The hook therefore finds the selected row from
// menu's selection and scroll, and not from the arrow, which such a frame
// leaves as the last frame's hook left it, blank.
func (ui *UI) MarkSelection(menu *widget.Select) func(*buffer.Buffer, time.Duration) {
	return func(buf *buffer.Buffer, _ time.Duration) {
		x, w := menu.X(), menu.Width()
		if w <= 0 {
			return
		}
		end := x + w
		inset, fill := ui.service.state.Selection.FillInset()
		// Every row is one line, as the menus show no description under
		// their names.
		selected := menu.Y() + menu.SelectedIndex() - menu.ScrollOffset()
		for y := menu.Y(); y < menu.Y()+menu.Height(); y++ {
			for cx := max(end-PaddingX, x); cx < end; cx++ {
				cell := buf.Cell(cx, y)
				buf.SetCell(cx, y, ' ', cell.Fg, cell.Bg, 0)
			}
			if !fill || y != selected || menu.ItemCount() == 0 {
				continue
			}
			if arrow := buf.Cell(x, y); arrow.Char == '→' {
				buf.SetCell(x, y, ' ', arrow.Fg, arrow.Bg, 0)
			}
			for cx := x + inset; cx < end-inset; cx++ {
				cell := buf.Cell(cx, y)
				cell.Fg, cell.Bg = ui.BoxBg(), activeFg
				buf.SetCellDirect(cx, y, cell)
			}
		}
	}
}

// KeyHint is one of the keys a panel names: a key and what it does.
type KeyHint struct {
	// Key names the key, such as "esc".
	Key string
	// Action says what the key does, such as "cancel".
	Action string
}

// The keys the panels share. Each panel's keys start with HintCancel, as the
// footer names them; TitleHints moves it last.
var (
	HintCancel = KeyHint{Key: "esc", Action: "cancel"}
	HintMove   = KeyHint{Key: "↑/↓", Action: "move"}
	// HintChange names the arrows that change the value of a menu's
	// selected row, as in /settings.
	HintChange = KeyHint{Key: "←/→", Action: "change"}
	// HintCustom names the key that enters an entry a menu does not list,
	// as a model typed in /model.
	HintCustom = KeyHint{Key: "tab", Action: "custom"}
	// HintComplete and HintSearch name keys that the footer names, and a
	// title leaves out: tab completes a slash command, and typing searches
	// a menu in the input under it.
	HintComplete = KeyHint{Key: "tab", Action: "complete"}
	HintSearch   = KeyHint{Key: "type", Action: "search"}
)

// String returns h in brackets, as a panel's title names it, such as
// "[esc cancel]".
func (h KeyHint) String() string {
	return "[" + h.Key + " " + h.Action + "]"
}

// TitleHints returns the keys of hints that a panel's title names: all but
// HintComplete and HintSearch, with the keys of esc last.
func TitleHints(hints []KeyHint) []KeyHint {
	var keys, esc []KeyHint
	for _, h := range hints {
		switch {
		case h == HintComplete || h == HintSearch:
		case h.Key == HintCancel.Key:
			esc = append(esc, h)
		default:
			keys = append(keys, h)
		}
	}
	return append(keys, esc...)
}

// keyHintGap is the space between two key hints on a line of the footer.
const keyHintGap = 2

// KeyHintsView draws key hints, wrapping between hints and never inside
// one, so that a key is never parted from what it does, and measures its own
// height for the width it is given, as a text does. A Bracketed view draws
// them as a panel's title names them, such as "[esc cancel]", a space apart,
// in mutedFg; otherwise it draws them as the footer does, such as "esc
// cancel", keyHintGap apart, in mutedFg too.
type KeyHintsView struct {
	*render.BaseRenderable
	// Hints are the key hints drawn.
	Hints []KeyHint
	// Bracketed draws the hints as a panel's title names them.
	Bracketed bool
}

// NewKeyHintsView returns an empty view of key hints with widget ID id, in
// the style bracketed picks.
func NewKeyHintsView(id string, bracketed bool) *KeyHintsView {
	v := &KeyHintsView{BaseRenderable: render.NewBaseRenderable(render.WithID(id)), Bracketed: bracketed}
	v.LayoutNode().Measure = func(width int, widthMode layout.MeasureMode, _ int, _ layout.MeasureMode) layout.MeasureResult {
		if widthMode == layout.MeasureUndefined {
			width = math.MaxInt
		}
		lines := v.Lines(width)
		measured := 0
		for _, line := range lines {
			measured = max(measured, v.LineWidth(line))
		}
		if widthMode == layout.MeasureExactly {
			measured = width
		}
		return layout.MeasureResult{Width: measured, Height: len(lines)}
	}
	v.SetGone(true)
	return v
}

// SetHints replaces the hints drawn, and hides the view while there are
// none.
func (v *KeyHintsView) SetHints(hints []KeyHint) {
	if slices.Equal(v.Hints, hints) {
		return
	}
	v.Hints = slices.Clone(hints)
	v.SetGone(len(hints) == 0)
	v.LayoutNode().MarkDirty()
	v.RequestRender()
}

// Text returns h as the view draws it.
func (v *KeyHintsView) Text(h KeyHint) string {
	if v.Bracketed {
		return h.String()
	}
	return h.Key + " " + h.Action
}

// Gap returns the cells between two hints on a line.
func (v *KeyHintsView) Gap() int {
	if v.Bracketed {
		return 1
	}
	return keyHintGap
}

// Lines packs the hints into lines at most width cells wide, in order. A
// hint wider than width takes a line of its own.
func (v *KeyHintsView) Lines(width int) [][]KeyHint {
	var lines [][]KeyHint
	for _, h := range v.Hints {
		if n := len(lines); n > 0 && v.LineWidth(lines[n-1])+v.Gap()+textbuf.StringWidth(v.Text(h)) <= width {
			lines[n-1] = append(lines[n-1], h)
			continue
		}
		lines = append(lines, []KeyHint{h})
	}
	return lines
}

// LineWidth returns the cells a line of hints takes, Gap apart.
func (v *KeyHintsView) LineWidth(line []KeyHint) int {
	w := max(len(line)-1, 0) * v.Gap()
	for _, h := range line {
		w += textbuf.StringWidth(v.Text(h))
	}
	return w
}

// PlainText returns the hints as they read on one line.
func (v *KeyHintsView) PlainText() string {
	texts := make([]string, len(v.Hints))
	for i, h := range v.Hints {
		texts[i] = v.Text(h)
	}
	return strings.Join(texts, strings.Repeat(" ", v.Gap()))
}

// RenderSelf draws the hints, packed into lines of the view's width.
func (v *KeyHintsView) RenderSelf(buf *buffer.Buffer, _ time.Duration) {
	w, h := v.Width(), v.Height()
	if w <= 0 || h <= 0 {
		return
	}
	for row, line := range v.Lines(w) {
		if row >= h {
			return
		}
		x, y := v.X(), v.Y()+row
		for i, hint := range line {
			if i > 0 {
				x += v.Gap()
			}
			if v.Bracketed {
				buf.DrawText(hint.String(), x, y, mutedFg)
				x += textbuf.StringWidth(hint.String())
				continue
			}
			buf.DrawText(hint.Key+" "+hint.Action, x, y, mutedFg)
			x += textbuf.StringWidth(hint.Key) + 1 + textbuf.StringWidth(hint.Action)
		}
	}
}

// PanelHeader is the top of a panel in the input box: its title, with the
// panel's count and keys after it, the panel's keys under it as the footer
// names them, and a subtitle. Every panel opens with one, so that their
// titles and rows line up. KeyHintsMode picks which of the two rows names
// the keys, if either does.
type PanelHeader struct {
	// Title shows Heading, not bold, and Count after it.
	Title *widget.Text
	// Keys draws the keys the panel names after Title, bracketed, which wrap
	// under themselves rather than under Title on a narrow terminal.
	Keys *KeyHintsView
	// SubtitleKeys draws the keys the panel names under its title, as the
	// footer names them.
	SubtitleKeys *KeyHintsView
	// Subtitle shows a line about the panel under its title, dimmed.
	Subtitle *widget.Text
	// TitleRow, SubtitleKeysRow, and SubtitleRow are the rows holding Title
	// and Keys, SubtitleKeys, and Subtitle, which collapse while they are
	// empty.
	TitleRow, SubtitleKeysRow, SubtitleRow *widget.Box
	// Heading is the panel's title.
	Heading string
	// Count is a menu's count after its title, such as "[3/20]", as
	// TitleCount gives it, or "".
	Count string
}

// AddPanelHeader appends a header showing title, without a subtitle, to
// panel, whose widget IDs start with id.
func AddPanelHeader(panel *widget.Box, id, title string, borderFg, bg style.Color) *PanelHeader {
	h := &PanelHeader{
		// The heading and the count are separate chunks, so that each has
		// its own color.
		Title: widget.NewText("",
			widget.TextWithID(id+"-title"),
			widget.TextWithFg(titleFg),
			widget.TextWithLayout(layout.WithFlexShrink(0)),
		),
		Keys:         NewKeyHintsView(id+"-keys", true),
		SubtitleKeys: NewKeyHintsView(id+"-subtitle-keys", false),
		Subtitle: widget.NewText("",
			widget.TextWithID(id+"-subtitle"),
			widget.TextWithFg(mutedFg),
		),
	}
	h.Keys.LayoutNode().FlexGrow = 1
	line := widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionRow),
			layout.WithColumnGap(1),
		),
		widget.Children(h.Title, h.Keys),
	)
	h.TitleRow = AddContentRow(panel, line, borderFg, bg)
	h.SubtitleKeysRow = AddContentRow(panel, h.SubtitleKeys, borderFg, bg)
	h.SubtitleKeysRow.SetGone(true)
	h.SubtitleRow = AddContentRow(panel, h.Subtitle, borderFg, bg)
	h.Set(title, "")
	return h
}

// Set replaces the title and the subtitle, collapsing an empty row.
func (h *PanelHeader) Set(title, subtitle string) {
	h.Heading = title
	h.ShowTitle()
	h.Subtitle.SetContent(subtitle)
	h.SubtitleRow.SetGone(subtitle == "")
}

// SetCount shows count after the title, or the title alone for an empty
// count.
func (h *PanelHeader) SetCount(count string) {
	if count == h.Count {
		return
	}
	h.Count = count
	h.ShowTitle()
}

// SetKeys names hints after the title and its count, or none for no hints.
func (h *PanelHeader) SetKeys(hints []KeyHint) {
	if slices.Equal(h.Keys.Hints, hints) {
		return
	}
	h.Keys.SetHints(hints)
	h.ShowTitle()
}

// SetSubtitleKeys names hints on the row under the title, as the footer
// names them, and collapses the row for no hints.
func (h *PanelHeader) SetSubtitleKeys(hints []KeyHint) {
	h.SubtitleKeys.SetHints(hints)
	h.SubtitleKeysRow.SetGone(len(hints) == 0)
}

// ShowTitle shows Heading, not bold, and then, a space apart, Count in
// mutedFg, before the keys, collapsing the row while it shows none of
// them.
func (h *PanelHeader) ShowTitle() {
	var text style.StyledText
	if h.Heading != "" {
		text.Chunks = append(text.Chunks, style.Plain(h.Heading))
	}
	if h.Count != "" {
		count, fg := h.Count, mutedFg
		if len(text.Chunks) > 0 {
			count = " " + count
		}
		text.Chunks = append(text.Chunks, style.TextChunk{Text: count, Fg: fg})
	}
	h.Title.SetStyledContent(text)
	h.Title.SetGone(len(text.Chunks) == 0)
	h.TitleRow.SetGone(len(text.Chunks) == 0 && len(h.Keys.Hints) == 0)
}

// PlainText returns the title row as it reads on one line: Heading, Count,
// and the keys, a space apart.
func (h *PanelHeader) PlainText() string {
	parts := slices.DeleteFunc([]string{h.Title.PlainText(), h.Keys.PlainText()}, func(s string) bool { return s == "" })
	return strings.Join(parts, " ")
}

// Visible reports whether the header shows a title, keys under it, or a
// subtitle.
func (h *PanelHeader) Visible() bool {
	return h.TitleRow.Visible() || h.SubtitleKeysRow.Visible() || h.SubtitleRow.Visible()
}

// SessionHints returns the session list's keys.
func SessionHints() []KeyHint {
	return []KeyHint{HintCancel, HintMove, HintSearch}
}

// PanelHints returns the keys of the panel open at the top of the input box,
// or nil when none is open.
func (ui *UI) PanelHints() []KeyHint {
	switch {
	case ui.treePanel.Visible():
		return TreeHints()
	case ui.sessionPanel.Visible():
		return SessionHints()
	case ui.bannerPanel.Visible():
		return ui.bannerHints
	case ui.slashMenuRow.Parent() != nil:
		return SlashHints()
	}
	return nil
}

// SetHeaderKeys names hints, a panel's keys, in its header h where
// KeyHintsMode says: after its title, as TitleHints gives them, on the row
// under its title, as the footer names them, or nowhere in h while the
// footer names them.
func (ui *UI) SetHeaderKeys(h *PanelHeader, hints []KeyHint) {
	var title, subtitle []KeyHint
	switch ui.service.state.KeyHints {
	case KeyHintsTitle:
		title = TitleHints(hints)
	case KeyHintsSubtitle:
		subtitle = hints
	}
	h.SetKeys(title)
	h.SetSubtitleKeys(subtitle)
}

// SyncKeyHints names every panel's keys where KeyHintsMode says: after its
// title, under it, or for the open panel, in the footer under the input box.
func (ui *UI) SyncKeyHints() {
	ui.SetHeaderKeys(ui.slashHeader, SlashHints())
	ui.SetHeaderKeys(ui.sessionHeader, SessionHints())
	ui.SetHeaderKeys(ui.treeHeader, TreeHints())
	ui.SetHeaderKeys(ui.bannerHeader, ui.bannerHints)
	ui.SyncFooter()
}

// SyncFooter shows the keys of the open panel in the footer under the input
// box while KeyHintsMode is KeyHintsFooter, then the Usage box's while the
// box names them, as UsageKeys reports, which the footer names as
// UsageFooterHints gives them. It shows or hides the Usage box, as
// SyncStatusBox does. The footer's row stays, blank, while there are no
// hints.
func (ui *UI) SyncFooter() {
	ui.SyncStatusBox()
	var hints []KeyHint
	if ui.service.state.KeyHints == KeyHintsFooter {
		hints = ui.PanelHints()
		if len(ui.UsageKeys()) > 0 {
			hints = slices.Concat(hints, UsageFooterHints(ui.service.state.UsagePinned))
		}
	}
	ui.footerHints.SetHints(hints)
}

// ShowCount shows count, a list's count as TitleCount gives it, where
// CountPlace says: after the list's title in h, or on the line under its
// rows, below.
func (ui *UI) ShowCount(h *PanelHeader, below *PanelStatus, count string) {
	if ui.service.state.Count == CountBelow {
		h.SetCount("")
		below.Set(count)
		return
	}
	h.SetCount(count)
	below.Set("")
}

// SyncCounts counts the rows of every list where CountPlace says, but for
// the slash menu, which counts none.
func (ui *UI) SyncCounts() {
	ui.SyncSessionCount()
	ui.SyncBannerCount()
	if v, ok := ui.service.TreeView(); ok && ui.treePanel.Visible() {
		ui.DrawTree(v)
	}
}

// TitleCount returns a menu's count while its shown rows do not all fit,
// such as "[3/20]" for the third of twenty, which shows under its rows or
// after its title, as CountPlace says, and "" when all total fit in the shown
// rows.
func TitleCount(selected, total, shown int) string {
	if total <= shown {
		return ""
	}
	return fmt.Sprintf("[%d/%d]", selected+1, total)
}

// PanelStatus is the dim line at the bottom of a panel, under its rows, such
// as the session tree's filter, or a list's count while CountPlace is
// CountBelow. Its row collapses while it is empty.
type PanelStatus struct {
	// Text shows the status.
	Text *widget.Text
	// Row is the row holding Text.
	Row *widget.Box
}

// AddPanelStatus appends an empty status line to panel, whose widget ID is
// id.
func AddPanelStatus(panel *widget.Box, id string, borderFg, bg style.Color) *PanelStatus {
	s := &PanelStatus{Text: widget.NewText("", widget.TextWithID(id), widget.TextWithFg(mutedFg))}
	s.Row = AddContentRow(panel, s.Text, borderFg, bg)
	s.Set("")
	return s
}

// Set shows text, collapsing the row when text is empty.
func (s *PanelStatus) Set(text string) {
	s.Text.SetContent(text)
	s.Row.SetGone(text == "")
}
