package ui

import (
	"strconv"
	"strings"
	"time"

	"github.com/unaquatic/flow/terminal/ui"
	"github.com/unaquatic/fluid/buffer"
	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/syntax"
	"github.com/unaquatic/fluid/textbuf"
	"github.com/unaquatic/fluid/widget"
)

// LineKind is what a line of a side by side diff is: kept, deleted, or added.
type LineKind int

const (
	// LineContext is a line both sides keep.
	LineContext LineKind = iota
	// LineDeleted is a line the old side has and the new one does not.
	LineDeleted
	// LineAdded is a line the new side has and the old one does not.
	LineAdded
)

// DiffLine is one side of a row of a side by side diff.
type DiffLine struct {
	// Number is the line's number in its file, or zero for a blank side,
	// where the other side adds or deletes a line.
	Number int
	// Text is the line, without the sign the unified diff starts it with.
	Text string
	// Kind is whether the line is kept, deleted, or added.
	Kind LineKind
	// Code is Text highlighted as code of its file's language, as Highlight
	// colors it, or nil when the language is not known.
	Code []style.TextChunk
}

// DiffRow is a row of a side by side diff: the old file's line on the left
// and the new file's on the right, or, when Note is set, a line that spans
// both, such as a hunk's header or Git's note that a binary file differs.
type DiffRow struct {
	// Old and New are the row's two sides.
	Old, New DiffLine
	// Note, when set, spans the row in place of Old and New.
	Note string
}

// SideBySide returns the rows of hunks, a file's hunks of a unified diff, as
// FilePatch holds them, side by side: a kept line on both sides, and each run
// of deleted lines beside the run of added lines that follows it, a line of
// one beside a line of the other, the longer run's extra lines beside blank
// sides. Each hunk starts with a Note of its header, and lines are numbered
// from it.
func SideBySide(hunks string) []DiffRow {
	var rows []DiffRow
	var oldLine, newLine int
	var deleted, added []DiffLine
	flush := func() {
		for i := range max(len(deleted), len(added)) {
			var row DiffRow
			if i < len(deleted) {
				row.Old = deleted[i]
			}
			if i < len(added) {
				row.New = added[i]
			}
			rows = append(rows, row)
		}
		deleted, added = deleted[:0], added[:0]
	}
	for line := range strings.Lines(hunks) {
		line = strings.TrimSuffix(line, "\n")
		switch {
		case strings.HasPrefix(line, "@@"):
			flush()
			oldLine, newLine = HunkStarts(line)
			rows = append(rows, DiffRow{Note: line})
		case strings.HasPrefix(line, "-"):
			deleted = append(deleted, DiffLine{Number: oldLine, Text: line[1:], Kind: LineDeleted})
			oldLine++
		case strings.HasPrefix(line, "+"):
			added = append(added, DiffLine{Number: newLine, Text: line[1:], Kind: LineAdded})
			newLine++
		case strings.HasPrefix(line, " "), line == "" && oldLine > 0:
			flush()
			text := strings.TrimPrefix(line, " ")
			rows = append(rows, DiffRow{
				Old: DiffLine{Number: oldLine, Text: text},
				New: DiffLine{Number: newLine, Text: text},
			})
			oldLine++
			newLine++
		case strings.HasPrefix(line, `\`):
			// Git's "\ No newline at end of file" says nothing a reader needs.
		default:
			flush()
			rows = append(rows, DiffRow{Note: line})
		}
	}
	flush()
	return rows
}

// HunkStarts returns the first line numbers of the old and new sides of a
// hunk, as its header, such as "@@ -12,5 +12,7 @@", gives them, or 1 for a
// side it does not give.
func HunkStarts(header string) (old, new int) {
	old, new = 1, 1
	for field := range strings.FieldsSeq(header) {
		sign := field[0]
		if sign != '-' && sign != '+' {
			continue
		}
		start, _, _ := strings.Cut(field[1:], ",")
		n, err := strconv.Atoi(start)
		if err != nil {
			continue
		}
		// An empty side, as of a new file, starts at 0 and has no lines.
		n = max(n, 1)
		if sign == '-' {
			old = n
		} else {
			new = n
		}
	}
	return old, new
}

// Highlight colors the text of each side of rows as code of lang, a language
// as syntax.Lookup finds it, such as a file's path, with h, setting each
// line's Code: the old side's lines in order, and the new side's apart, each
// a file of its own as far as its hunk goes, so that a string or comment
// that spans lines keeps its color. A hunk's Note starts both sides afresh.
// Text the highlighter leaves uncolored takes fg. An unknown lang leaves
// rows as they are.
func Highlight(rows []DiffRow, lang string, h *widget.Highlighter, fg style.Color) {
	l := syntax.Lookup(lang)
	if l == nil || h == nil {
		return
	}
	var oldSt, newSt syntax.State
	var toks []syntax.Token
	color := func(line *DiffLine, st *syntax.State) {
		if line.Number == 0 {
			return
		}
		toks, *st = l.AppendLine(toks[:0], line.Text, *st)
		line.Code = ui.AppendSpans(nil, h.Line(line.Text, toks, fg, style.ColorTransparent))
	}
	for i := range rows {
		if rows[i].Note != "" {
			oldSt, newSt = "", ""
			continue
		}
		color(&rows[i].Old, &oldSt)
		color(&rows[i].New, &newSt)
	}
}

// NumberWidth returns the columns the widest line number of rows takes.
func NumberWidth(rows []DiffRow) int {
	n := 0
	for _, r := range rows {
		n = max(n, r.Old.Number, r.New.Number)
	}
	return len(strconv.Itoa(n))
}

// SideBySideView returns rows as two columns of equal width, the old file on
// the left and the new on the right, each line after its number in the
// theme's TextMuted, a deleted or added line as DiffText marks it, and a
// kept one in the box's color. A line too long for its
// column wraps within it, and the row takes the rows of its taller side, so
// that the two sides stay aligned. A Note spans both columns, muted.
func (b *PatchBox) SideBySideView(rows []DiffRow) *widget.Box {
	view := widget.NewBox(widget.BoxWithLayout(
		layout.WithFlexDirection(layout.DirectionColumn),
		layout.WithAlignItems(layout.AlignStretch),
	))
	muted := b.Theme.TextMuted
	width := NumberWidth(rows)
	for _, r := range rows {
		if r.Note != "" {
			view.Add(widget.NewText(r.Note, widget.TextWithFg(muted), widget.TextWithBg(style.ColorTransparent),
				widget.TextWithWrapMode(textbuf.WrapWord)))
			continue
		}
		// The four cells of a row sit in one row, as a wrapping text takes
		// the width its row leaves it only there: each side's text takes half
		// the row, which shrinks both alike to what the numbers leave. A
		// space in the line's tint parts a number from its text, so that the
		// tint runs on unbroken, the number's own a shade darker.
		row := widget.NewBox(widget.BoxWithLayout(layout.WithFlexDirection(layout.DirectionRow)))
		for i, line := range []DiffLine{r.Old, r.New} {
			number := b.DiffNumber(line, width)
			if i == 1 {
				// Two blank columns part the sides.
				number.LayoutNode().MarginLeft = layout.Points(2)
			}
			row.Add(number)
			row.Add(b.DiffSpace(line))
			row.Add(b.DiffText(line))
		}
		view.Add(row)
	}
	return view
}

// DiffColors returns the text color and the tint of line as DiffColor marks
// it: the box's color and none for a kept line; for a deleted or added one
// ui.DiffDelFg or ui.DiffAddFg and none, or, in ui.DiffColorBackground, the
// box's color on ui.DiffDelBg or ui.DiffAddBg.
func (b *PatchBox) DiffColors(line DiffLine) (fg, bg style.Color) {
	fg, bg = b.Fg, style.ColorTransparent
	changed := map[LineKind][2]style.Color{
		LineDeleted: {ui.DiffDelFg, ui.DiffDelBg},
		LineAdded:   {ui.DiffAddFg, ui.DiffAddBg},
	}
	if c, ok := changed[line.Kind]; ok {
		if b.DiffColor == ui.DiffColorBackground {
			bg = c[1]
		} else {
			fg = c[0]
		}
	}
	return fg, bg
}

// Tint fills the whole of text with bg before it draws, as the text paints
// only the cells it writes, so that a tint covers its wrapped rows and the
// ends of its lines too. A transparent bg leaves it as it is.
func Tint(text *widget.Text, bg style.Color) {
	if bg.IsTransparent() {
		return
	}
	text.RenderBeforeHook = func(buf *buffer.Buffer, _ time.Duration) {
		buf.FillRect(text.X(), text.Y(), text.Width(), text.Height(), bg)
	}
}

// NumberShade is how much darker, in OKLCH lightness, a changed line's
// number is tinted than its text.
const NumberShade = 0.04

// DiffNumber returns line's number, right-aligned in width columns, in the
// theme's TextMuted, on the tint DiffColors gives line, a shade darker by
// NumberShade, or blank columns for a blank side.
func (b *PatchBox) DiffNumber(line DiffLine, width int) *widget.Text {
	number := ""
	if line.Number > 0 {
		number = strconv.Itoa(line.Number)
	}
	_, bg := b.DiffColors(line)
	if !bg.IsTransparent() {
		bg = bg.Darken(NumberShade)
	}
	text := widget.NewText(strings.Repeat(" ", width-len(number))+number,
		widget.TextWithFg(b.Theme.TextMuted), widget.TextWithBg(bg),
		widget.TextWithLayout(layout.WithFlexShrink(0)))
	Tint(text, bg)
	return text
}

// DiffSpace returns the column that parts line's number from its text, on
// the tint DiffColors gives line.
func (b *PatchBox) DiffSpace(line DiffLine) *widget.Text {
	_, bg := b.DiffColors(line)
	text := widget.NewText(" ", widget.TextWithBg(bg), widget.TextWithLayout(layout.WithFlexShrink(0)))
	Tint(text, bg)
	return text
}

// DiffText returns line's text, half its row wide and wrapping there, in the
// colors DiffColors gives it, its tint filling its half of the row. A line
// with Code shows it highlighted instead, but for a changed line in
// ui.DiffColorText, whose color marks the change.
func (b *PatchBox) DiffText(line DiffLine) *widget.Text {
	fg, bg := b.DiffColors(line)
	content := style.NewStyledText(style.Plain(line.Text))
	if line.Code != nil && (line.Kind == LineContext || b.DiffColor == ui.DiffColorBackground) {
		content = style.NewStyledText(line.Code...)
	}
	text := widget.NewStyledText(content,
		widget.TextWithFg(fg), widget.TextWithBg(bg),
		widget.TextWithWrapMode(textbuf.WrapWord),
		widget.TextWithLayout(layout.WithWidth(layout.Percent(50)), layout.WithFlexShrink(1)))
	Tint(text, bg)
	return text
}

// RowByRow returns rows, a diff side by side as SideBySide gives it, as the
// lines of one column, each in a DiffRow's Old, in the order of a unified
// diff: a kept line once, numbered as in the new file, and each run of
// changes as its deleted lines and then its added ones. Notes stay as they
// are, and so does the Code Highlight gave each side.
func RowByRow(rows []DiffRow) []DiffRow {
	var out, added []DiffRow
	flush := func() {
		out = append(out, added...)
		added = added[:0]
	}
	for _, r := range rows {
		switch {
		case r.Note != "":
			flush()
			out = append(out, r)
		case r.Old.Kind == LineContext && r.New.Kind == LineContext && r.Old.Number > 0:
			flush()
			out = append(out, DiffRow{Old: r.New})
		default:
			if r.Old.Number > 0 {
				out = append(out, DiffRow{Old: r.Old})
			}
			if r.New.Number > 0 {
				added = append(added, DiffRow{Old: r.New})
			}
		}
	}
	flush()
	return out
}

// RowByRowView returns rows, as RowByRow gives them, as one column the width
// of the box: each line after its number, as SideBySideView shows a side,
// and each Note across it, muted.
func (b *PatchBox) RowByRowView(rows []DiffRow) *widget.Box {
	view := widget.NewBox(widget.BoxWithLayout(
		layout.WithFlexDirection(layout.DirectionColumn),
		layout.WithAlignItems(layout.AlignStretch),
	))
	width := NumberWidth(rows)
	for _, r := range rows {
		if r.Note != "" {
			view.Add(widget.NewText(r.Note, widget.TextWithFg(b.Theme.TextMuted), widget.TextWithBg(style.ColorTransparent),
				widget.TextWithWrapMode(textbuf.WrapWord)))
			continue
		}
		text := b.DiffText(r.Old)
		text.LayoutNode().Width = layout.Percent(100)
		view.Add(widget.NewBox(
			widget.BoxWithLayout(layout.WithFlexDirection(layout.DirectionRow)),
			widget.Children(b.DiffNumber(r.Old, width), b.DiffSpace(r.Old), text),
		))
	}
	return view
}
