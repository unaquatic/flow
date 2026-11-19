package ui

import (
	"strings"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension/snapshot"
	"github.com/unaquatic/flow/terminal/ui"
	"github.com/unaquatic/fluid/input"
	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/widget"
)

// PatchBox is the ui.PartBox of the workspace changes one step made, a reply
// and the tool calls it ran, from the message before it to the reply's end,
// shown under the reply, as a tool call is: a row on the canvas, marked as /config marks
// tool calls, whose header sums them up, as PatchHeader gives it, and which
// Ctrl+O and a click on the header expand and collapse as they do a tool
// call. Collapsed, it lists the files it changed under the header, each by
// its path and counts. Expanded, it shows under the same header the files, each
// by its path and counts and then its diff, side by side; a click on a
// file's row hides its diff, or shows it again. Its rows are not cut to the
// screen as a tool call's are. Its header reads the same expanded or not, and the
// details show the changes of each file under it, as FileView shows them. It stays hidden until a nonempty patch is
// set, which only the box of the reply that ends a turn gets.
type PatchBox struct {
	*ui.PartBox
	// From is the tree the step started from, which the message before its
	// reply ended with.
	From snapshot.Hash
	// To is the tree the step ended with, which its reply ended with.
	To snapshot.Hash
	// Requested reports whether the patch between From and To was requested.
	Requested bool
	// Load, when set, reads the patch between two trees, and shows it with
	// SetPatch. SetMessage calls it once both trees are known. A box without
	// one, as in the session preview, shows no patch.
	Load func(from, to snapshot.Hash)
	// Patch is the unified diff shown, as SetPatch sets it.
	Patch string
	// Files holds a FileView for each file Patch changes, in its order.
	Files *widget.Box
	// Paths lists the files Patch changes, each by its path and counts,
	// right under the header while the box is collapsed, as Files shows
	// them with their diffs once it is expanded.
	Paths *widget.Text
	// Diffs holds the side by side diff of each file that has hunks, which
	// shows under its row while ShowDiff toggles it on.
	Diffs []*widget.Box
	// Theme and Fg are the palette FileView draws with.
	Theme *style.Theme
	Fg    style.Color
	// DiffColor is how the diffs mark changed lines, as SetViewStyle sets it.
	DiffColor ui.DiffColor
}

var (
	_ ui.SegmentView = (*PatchBox)(nil)
	_ ui.BoxContent  = (*PatchBox)(nil)
)

// NewPatchBox creates a hidden, collapsed patch row, which expands into a
// block on the theme's raised surface as a tool call does.
func NewPatchBox(theme *style.Theme, fg style.Color) *PatchBox {
	if theme == nil {
		theme = ui.DefaultTheme()
	}
	surface := theme.ResolveSurfaceRaised()
	b := &PatchBox{
		PartBox: ui.NewPartBox(theme, fg, surface),
		// The files list on rows of their own, and a diff that shows parts
		// its file from the next by a blank row.
		Files: widget.NewBox(widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionColumn),
			layout.WithAlignItems(layout.AlignStretch),
		)),
		// The list sits right under the header, closing the gap the box
		// leaves between its header and what follows it.
		Paths: widget.NewText("", widget.TextWithFg(fg), widget.TextWithBg(style.ColorTransparent),
			widget.TextWithLayout(layout.WithMarginTop(layout.Points(-1)))),
		Theme: theme,
		Fg:    fg,
	}
	// The marker takes the color of the title it heads, as a panel's does.
	b.MarkerFg = ui.TitleChunk(PatchTitle).Fg
	b.SetMarkerShape(b.MarkerShape)
	b.Content = b
	b.Details.Add(b.Files)
	b.Add(b.Paths)
	b.SetGone(true)
	// Collapsed, the row is parted from the rows around it by a blank row,
	// as a block is, rather than stacked with the steps above and below it.
	b.Apart = true
	b.ShowShape()
	return b
}

// SetViewStyle marks the changed lines of the diffs as st's DiffColor says,
// drawing them again when that changes.
func (b *PatchBox) SetViewStyle(st ui.ViewStyle) {
	if st.DiffColor == b.DiffColor {
		return
	}
	b.DiffColor = st.DiffColor
	if b.Patch != "" {
		b.SetPatch(b.Patch)
	}
}

// SetMessage records the trees bounding the step that msg, a reply, took, as
// SetTrees does, and loads its patch once both are known: the tree the
// message before it ended with, and the one msg ended with itself. Each reply
// so shows the changes of its own step, its tool calls, under it, and one
// that changed nothing shows none, as a prompt, which changes no files,
// never does.
func (b *PatchBox) SetMessage(msg ui.ChatMessage) {
	if msg.Role != message.RoleAgent {
		return
	}
	b.SetTrees(snapshot.TreeIn(msg.Prev), snapshot.TreeIn(msg.Parts))
	if b.Load != nil && b.Pending() {
		b.Requested = true
		b.Load(b.From, b.To)
	}
}

// SetTrees records the trees bounding the step. The zero Hash leaves the
// recorded tree unchanged.
func (b *PatchBox) SetTrees(from, to snapshot.Hash) {
	if !from.IsZero() {
		b.From = from
	}
	if !to.IsZero() {
		b.To = to
	}
}

// Pending reports whether both trees are known and their patch has not been
// requested yet.
func (b *PatchBox) Pending() bool {
	return !b.From.IsZero() && !b.To.IsZero() && !b.Requested
}

// SetPatch displays patch, a FileView for each file it changes, hiding the
// box when it is empty.
func (b *PatchBox) SetPatch(patch string) {
	b.Patch = patch
	files := SplitPatch(patch)
	b.Paths.SetStyledContent(ui.JoinStyledLines(DiffNotice(files)[1:]))
	b.ShowHeader()
	for _, child := range b.Files.Children() {
		b.Files.Remove(child.ID())
	}
	b.Diffs = b.Diffs[:0]
	for _, f := range files {
		b.Files.Add(b.FileView(f))
	}
	// The last diff needs no row under it: the turn's footer is parted from
	// the box anyway.
	if n := len(b.Diffs); n > 0 && files[len(files)-1].Hunks != "" {
		b.Diffs[n-1].LayoutNode().MarginBottom = layout.Points(0)
	}
	b.SetGone(patch == "")
}

// FileView returns the changes of f: its path in the box's color and its
// counts as ui.DiffCounts colors them, and under them, until a click on
// that row hides it, its hunks, without Git's headers, side by side, as
// SideBySideView shows them, followed by a blank row.
func (b *PatchBox) FileView(f FilePatch) *widget.Box {
	heading := widget.NewStyledText(style.NewStyledText(
		append([]style.TextChunk{{Text: f.Path, Fg: b.Fg}, style.Plain("  ")}, ui.DiffCounts(f.Additions, f.Deletions)...)...))
	view := widget.NewBox(
		widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionColumn),
			layout.WithAlignItems(layout.AlignStretch),
		),
		widget.Children(heading),
	)
	if f.Hunks != "" {
		// The hunks fill no background of their own, so that the box's
		// shows through them: its surface when boxed, and the canvas when
		// plain.
		diff := b.SideBySideView(SideBySide(f.Hunks))
		diff.LayoutNode().MarginBottom = layout.Points(1)
		b.Diffs = append(b.Diffs, diff)
		heading.OnMouseDownHandler = func(e *input.MouseEvent) {
			if e.Button == input.ButtonLeft {
				diff.SetGone(diff.Visible())
				e.StopPropagation()
			}
		}
		view.Add(diff)
	}
	return view
}

// ShowHeader writes PatchHeader of Patch once it is set, the same whether
// the box is open or not, and shows Paths under it while the box is
// collapsed.
func (b *PatchBox) ShowHeader() {
	if b.Patch != "" {
		b.Header.SetStyledContent(PatchHeader(ParseDiffStat(b.Patch)))
	}
	// Collapsed, the box lists its files under the header; expanded, Files
	// shows them with their diffs instead.
	b.Paths.SetGone(b.Patch == "" || b.Open())
}

// DiffStat counts the changes in a unified diff.
type DiffStat struct {
	// Files is the number of changed files.
	Files int
	// Additions is the number of added lines.
	Additions int
	// Deletions is the number of deleted lines.
	Deletions int
}

// ParseDiffStat counts the files and changed lines in a Git unified diff, as
// StatOf counts those SplitPatch finds.
func ParseDiffStat(patch string) DiffStat {
	return StatOf(SplitPatch(patch))
}

// StatOf counts files and the lines they add and delete.
func StatOf(files []FilePatch) DiffStat {
	st := DiffStat{Files: len(files)}
	for _, f := range files {
		st.Additions += f.Additions
		st.Deletions += f.Deletions
	}
	return st
}

// FilePatch is the part of a Git unified diff that changes one file.
type FilePatch struct {
	// Path is the file's path in the tree the diff ends with, or for a
	// deleted file in the one it starts from.
	Path string
	// Hunks are the file's hunks, from its first "@@" line, or Git's line
	// that says a binary file differs. Git's headers, such as "diff --git",
	// "index", "---" and "+++", are left out.
	Hunks string
	// Additions and Deletions count the lines the hunks add and delete.
	Additions int
	Deletions int
}

// SplitPatch returns the files a Git unified diff changes, in its order. Only
// lines inside hunks are counted, so headers such as "--- a/x" are not
// mistaken for deletions.
func SplitPatch(patch string) []FilePatch {
	var files []FilePatch
	var hunks strings.Builder
	inHunk := false
	end := func() {
		if len(files) > 0 {
			files[len(files)-1].Hunks = strings.TrimSuffix(hunks.String(), "\n")
		}
		hunks.Reset()
	}
	for line := range strings.Lines(patch) {
		if rest, ok := strings.CutPrefix(line, "diff --git "); ok {
			end()
			files = append(files, FilePatch{Path: GitDiffPath(rest)})
			inHunk = false
			continue
		}
		if len(files) == 0 {
			continue
		}
		f := &files[len(files)-1]
		switch {
		case strings.HasPrefix(line, "@@"):
			inHunk = true
		case inHunk:
			switch {
			case strings.HasPrefix(line, "+"):
				f.Additions++
			case strings.HasPrefix(line, "-"):
				f.Deletions++
			}
		case strings.HasPrefix(line, "Binary files "):
		case strings.HasPrefix(line, "+++ b/"):
			f.Path = strings.TrimSpace(strings.TrimPrefix(line, "+++ b/"))
			continue
		case strings.HasPrefix(line, "rename to "):
			f.Path = strings.TrimSpace(strings.TrimPrefix(line, "rename to "))
			continue
		default:
			continue
		}
		hunks.WriteString(line)
	}
	end()
	return files
}

// GitDiffPath returns the path a "diff --git" line names, given the rest of
// the line after "diff --git ": that after " b/", the path in the tree the
// diff ends with.
func GitDiffPath(rest string) string {
	rest = strings.TrimSpace(rest)
	if i := strings.LastIndex(rest, " b/"); i >= 0 {
		return rest[i+len(" b/"):]
	}
	return strings.TrimPrefix(rest, "a/")
}

// PatchTitle names the changes of a turn. The notice of a history move is
// headed by the move's name instead, as ui.MoveLines gives it.
const PatchTitle = "Changed"

// PatchHeader summarizes st for a collapsed patch row, as a tool call's
// header names its call: PatchTitle as ui.TitleChunk gives it, and then the
// files changed as a history move's notice counts them, such as
// "Changed 2 files  +15 -5".
func PatchHeader(st DiffStat) style.StyledText {
	chunks := append([]style.TextChunk{ui.TitleChunk(PatchTitle), style.Plain(" ")}, ui.DiffSummary(st.Files, st.Additions, st.Deletions)...)
	return style.NewStyledText(chunks...)
}
