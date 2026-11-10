package ui

import (
	"slices"
	"strings"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/input"
	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/widget"
)

// BuildTreePanel creates the session tree panel at the top of the input box:
// its header, which counts the rows when they do not all fit, the rows, and
// their status, above the input that searches them.
func (ui *UI) BuildTreePanel(borderFg, bg style.Color) {
	// Each row is one line, cut to fit, however long its message.
	ui.treeRows = widget.NewStyledText(style.StyledText{},
		widget.TextWithID("tree-rows"),
		widget.TextWithTruncate(true),
	)
	ui.treeRows.RenderAfterHook = ui.MarkTreeSelection
	ui.treeSelected = -1
	ui.treePanel = NewInputPanel("tree-panel")
	ui.treeHeader = AddPanelHeader(ui.treePanel, "tree", "Session tree", borderFg, bg)
	AddDividerRow(ui.treePanel, borderFg, bg)
	// Each row starts with the arrow's two cells, which take the left border
	// cell, as a menu's arrow does. The rows end PaddingX cells from the
	// box's right edge, as BuildArrowRow ends them, in a box that spans the
	// whole row, so that the fill FillTreeSelection draws in it can reach
	// either edge, and is repainted with it whenever the selection moves.
	ui.treeRowsBox = widget.NewBox(
		widget.BoxWithID("tree-rows-box"),
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionColumn),
			layout.WithAlignItems(layout.AlignStretch),
			layout.WithFlexGrow(1),
			layout.WithPaddingRight(layout.Points(PaddingX)),
		),
		widget.Children(ui.treeRows),
	)
	ui.treeRowsBox.RenderAfterHook = ui.FillTreeSelection
	ui.treePanel.Add(widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionRow),
			layout.WithAlignItems(layout.AlignStretch),
		),
		widget.Children(ui.treeRowsBox),
	))
	ui.treeStatus = AddPanelStatus(ui.treePanel, "tree-status", borderFg, bg)
	AddDividerRow(ui.treePanel, borderFg, bg)
	ui.inputMiddleCol.InsertBefore(ui.treePanel, ui.inputContentRow)
}

// TreeHints returns the session tree's keys.
func TreeHints() []KeyHint {
	return []KeyHint{
		HintCancel,
		HintMove,
		{Key: "alt+←/→", Action: "fork"},
		{Key: "ctrl+o", Action: "filter"},
		{Key: "ctrl+t", Action: HiddenLabel},
		HintSearch,
	}
}

// ShowTree draws the session tree at the top of the input box, in place of
// any banner, and puts its search in the input.
func (ui *UI) ShowTree(v TreeView) {
	ui.bannerPanel.SetGone(true)
	ui.DrawTree(v)
	ui.treePanel.SetGone(false)
	ui.SyncFooter()
	ui.SetQuery(v.Query)
	ui.SetInputShown(true)
	ui.renderer.FocusRenderable(ui.input)
}

// DrawTree replaces the session tree's rows, its count, and its status line
// with v's. The count shows after the tree's title, or while CountPlace is
// CountBelow, on the status line, before the status.
func (ui *UI) DrawTree(v TreeView) {
	ui.treeRows.SetStyledContent(JoinStyledLines(TreeLines(v, ui.theme)))
	ui.treeSelected = v.InsertLine()
	ui.treeRowsBox.RequestRender()
	count, status := v.Count, v.Status
	if ui.service.state.Count == CountBelow {
		parts := slices.DeleteFunc([]string{count, status}, func(s string) bool { return s == "" })
		count, status = "", strings.Join(parts, " ")
	}
	ui.treeHeader.SetCount(count)
	ui.treeStatus.Set(status)
}

// TreeLines returns the lines of the session tree's rows: the tree's
// connectors, and each message's text behind a colored label: "User",
// "Agent", or the label of the tool a row's call runs, as the transcript
// names it, such as "Read calc.go". The labels take the colors the
// transcript marks the same rows with: "User" and "Agent" those of their
// markers, as RoleMarkerFg gives them, and a tool's label the theme's
// TextMuted, as are the text after the labels and the connectors. A row of a
// fork off the conversation opens with its type's tag, such as "[title]".
//
// The rows of the transcript, from its root to the head, keep their colors,
// and the rows of other branches are drawn whole in the theme's TextWeak, so
// that the transcript's path stands out without a marker shifting its text.
// Rather than mark the
// selected row, the lines open an empty line under it, where the next
// prompt goes, as TreeView.InsertLine places it, with an arrow in activeFg
// and the selected row's connectors running past it. Under a SelectionStyle
// that fills the selected row, FillTreeSelection and MarkTreeSelection fill
// that line once the lines are drawn.
func TreeLines(v TreeView, theme *style.Theme) []style.StyledText {
	theme = themeOrDefault(theme)
	fg := func(c style.Color) func(string) style.TextChunk { return style.New().WithFg(c).Func() }
	weak := fg(theme.TextWeak)

	lines := make([]style.StyledText, 0, len(v.Rows)+1)
	insert, insertPrefix := v.InsertLine(), ""
	if insert >= 0 {
		insertPrefix = v.Rows[v.Selected].Prefix
	}
	for i, row := range v.Rows {
		if i == insert {
			lines = append(lines, InsertLineText(insertPrefix))
		}
		muted := fg(theme.TextMuted)
		text, dim, tool := muted, muted, muted
		user, agent := fg(RoleMarkerFg(theme, message.RoleUser)), fg(RoleMarkerFg(theme, message.RoleAgent))
		if !row.Active {
			text, dim, muted, user, agent, tool = weak, weak, weak, weak, weak, weak
		}
		chunks := []style.TextChunk{text("  "), dim(row.Prefix)}
		e := row.Entry
		if e.Hidden() {
			chunks = append(chunks, dim(Tag(string(e.Type))+" "))
		}
		switch {
		case e.Role == message.RoleUser:
			chunks = append(chunks, user("User "), text(e.Text))
		case e.Role == message.RoleSystem:
			chunks = append(chunks, muted(ContextLabel+" "), muted(e.Text))
		case e.ToolsOnly():
			call := e.Tools[0]
			if call.Input == "" {
				chunks = append(chunks, tool(ToolLabel(call.Name)))
				break
			}
			chunks = append(chunks, tool(ToolLabel(call.Name)+" "), text(call.Input))
		case e.Role == message.RoleAgent && e.Text == "":
			chunks = append(chunks, agent("Agent "), muted("[no content]"))
		case e.Role == message.RoleAgent:
			chunks = append(chunks, agent("Agent "), text(e.Text))
		default:
			chunks = append(chunks, dim(Tag(e.Role.String())+" "), text(e.Text))
		}
		lines = append(lines, style.NewStyledText(chunks...))
	}
	if insert == len(v.Rows) {
		lines = append(lines, InsertLineText(insertPrefix))
	}
	return lines
}

// InsertLineText returns the empty line of the session tree that marks where
// the next prompt goes: an arrow in activeFg, then the connectors of prefix,
// the selected row's, as the lines under that row draw them, dim.
func InsertLineText(prefix string) style.StyledText {
	return style.NewStyledText(
		style.TextChunk{Text: "→ ", Fg: activeFg},
		style.TextChunk{Text: ContinuationPrefix(prefix), Fg: mutedFg},
	)
}

// ContinuationPrefix returns prefix, a row's connectors, as the lines under
// the row draw them: a connector that opens the row's branch becomes the
// line that runs on past it, "├─ " a "│  ", and the last branch's "└─ "
// blank.
func ContinuationPrefix(prefix string) string {
	return strings.NewReplacer("├─ ", "│  ", "└─ ", "   ").Replace(prefix)
}

// FillTreeSelection is the RenderAfterHook of the box around the session
// tree's rows, which the box runs before the rows draw on it. Under a
// SelectionStyle that fills the selected row, it fills the selected row's
// line in activeFg, as many cells in from either edge of the box as
// SelectionStyle.FillInset says, under the text MarkTreeSelection colors.
func (ui *UI) FillTreeSelection(buf *buffer.Buffer, _ time.Duration) {
	inset, fill := ui.service.state.Selection.FillInset()
	box := ui.treeRowsBox
	if !fill || ui.treeSelected < 0 || ui.treeSelected >= box.Height() {
		return
	}
	buf.FillRect(box.X()+inset, box.Y()+ui.treeSelected, box.Width()-2*inset, 1, activeFg)
}

// MarkTreeSelection is the RenderAfterHook of the session tree's rows. Under
// a SelectionStyle that fills the selected row, it takes the arrow off the
// selected row and draws the row's text, its labels and connectors included,
// in the box's fill on the fill FillTreeSelection drew, as a menu's selected
// row is drawn.
func (ui *UI) MarkTreeSelection(buf *buffer.Buffer, _ time.Duration) {
	inset, fill := ui.service.state.Selection.FillInset()
	rows := ui.treeRows
	if !fill || ui.treeSelected < 0 || ui.treeSelected >= rows.Height() {
		return
	}
	x, y := rows.X(), rows.Y()+ui.treeSelected
	if arrow := buf.Cell(x, y); arrow.Char == '→' {
		buf.SetCell(x, y, ' ', arrow.Fg, arrow.Bg, 0)
	}
	// The rows start at the box's left edge, where the fill starts inset
	// cells in.
	for cx := x + inset; cx < x+rows.Width(); cx++ {
		cell := buf.Cell(cx, y)
		cell.Fg, cell.Bg = ui.BoxBg(), activeFg
		buf.SetCellDirect(cx, y, cell)
	}
}

// HandleTreeKey handles navigation in the session tree. Other keys go to the
// input, which searches the tree.
func (ui *UI) HandleTreeKey(k *input.KeyEvent) {
	switch {
	case k.Name == input.KeyUp:
		ui.Apply(ui.service.MoveTree(-1))
	case k.Name == input.KeyDown:
		ui.Apply(ui.service.MoveTree(1))
	case k.Name == input.KeyPageUp:
		ui.Apply(ui.service.PageTree(-1))
	case k.Name == input.KeyPageDown:
		ui.Apply(ui.service.PageTree(1))
	case k.Mods.Has(input.ModAlt) && k.Name == input.KeyLeft:
		ui.Apply(ui.service.JumpTreeFork(-1))
	case k.Mods.Has(input.ModAlt) && k.Name == input.KeyRight:
		ui.Apply(ui.service.JumpTreeFork(1))
	case k.Mods.Has(input.ModCtrl) && k.Name == "o":
		ui.Apply(ui.service.CycleTreeFilter())
	case k.Mods.Has(input.ModCtrl) && k.Name == "t":
		ui.Apply(ui.service.ToggleTreeHidden())
	case k.Name == input.KeyEscape:
		ui.Apply(ui.service.CancelBanner())
	default:
		return
	}
	k.PreventDefault()
}
