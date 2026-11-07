package ui

import (
	"strings"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension/snapshot"
	"github.com/unaquatic/flow/terminal/ui"
	"github.com/unaquatic/fluid/layout"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/widget"
)

// PatchBox is the ui.PartBox of the workspace changes one agent step made,
// shown as a tool call is: a row on the canvas, marked as /settings marks
// tool calls, whose header sums them up, as PatchHeader gives it, and which
// Ctrl+O expands with the tool calls. Expanded, the header is PatchTitle
// alone, and the details show the summary under it, then the changes of each
// file, as FileView shows them. It stays hidden until a nonempty patch is
// set.
type PatchBox struct {
	*ui.PartBox
	// From is the tree the step started from, which the message before it
	// ended with.
	From snapshot.Hash
	// To is the tree the step ended with.
	To snapshot.Hash
	// Requested reports whether the patch between From and To was requested.
	Requested bool
	// Load, when set, reads the patch between two trees, and shows it with
	// SetPatch. SetMessage calls it once both trees are known. A box without
	// one, as in the session preview, shows no patch.
	Load func(from, to snapshot.Hash)
	// Patch is the unified diff shown, as SetPatch sets it.
	Patch string
	// Summary counts the changed files and lines, as ui.DiffSummary does,
	// over Files while the box is expanded.
	Summary *widget.Text
	// Files holds a FileView for each file Patch changes, in its order.
	Files *widget.Box
	// Theme and Fg are the palette FileView draws with.
	Theme *style.Theme
	Fg    style.Color
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
		// The summary sits right under the header, closing the gap the box
		// leaves between its header and its details.
		Summary: widget.NewText("", widget.TextWithFg(fg), widget.TextWithLayout(layout.WithMarginTop(layout.Points(-1)))),
		// A blank row parts the files, as it parts the box's details.
		Files: widget.NewBox(widget.BoxWithLayout(
			layout.WithFlexDirection(layout.DirectionColumn),
			layout.WithAlignItems(layout.AlignStretch),
			layout.WithGap(1),
		)),
		Theme: theme,
		Fg:    fg,
	}
	b.Content = b
	b.Details.Add(b.Summary)
	b.Details.Add(b.Files)
	b.SetGone(true)
	b.ShowShape()
	return b
}

// SetMessage records the trees bounding the step that msg ends, as SetTrees
// does, and loads its patch once both are known: for a reply, the tree the
// message before it ended with, and the one it ended with itself. A prompt
// changes no files, so it has no patch.
func (b *PatchBox) SetMessage(msg ui.ChatMessage) {
	var from snapshot.Hash
	if msg.Role == message.RoleAgent {
		from = snapshot.TreeIn(msg.Prev)
	}
	b.SetTrees(from, snapshot.TreeIn(msg.Parts))
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
	st := StatOf(files)
	b.Summary.SetStyledContent(style.NewStyledText(ui.DiffSummary(st.Files, st.Additions, st.Deletions)...))
	b.ShowHeader()
	for _, child := range b.Files.Children() {
		b.Files.Remove(child.ID())
	}
	for _, f := range files {
		b.Files.Add(b.FileView(f))
	}
	b.SetGone(patch == "")
}

// FileView returns the changes of f as a tool call's diff shows an edit:
// its path in the box's color and its counts as ui.DiffCounts colors them,
// and right under them its hunks, without Git's headers.
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
		// The hunks fill no background of their own, as a tool call's body
		// does, so that the box's shows through them: its surface when
		// boxed, and the canvas when plain.
		body := ui.NewToolBody(b.Theme, b.Fg, style.ColorTransparent)
		body.SetContent(ui.ToolFence("diff", f.Hunks))
		view.Add(body)
	}
	return view
}

// ShowHeader writes the header once a patch is set: PatchTitle alone while
// the box is open, where Summary counts the changes under it, and otherwise
// PatchHeader of Patch.
func (b *PatchBox) ShowHeader() {
	switch {
	case b.Patch == "":
	case b.Open():
		b.Header.SetStyledContent(style.NewStyledText(ui.TitleChunk(PatchTitle)))
	default:
		b.Header.SetStyledContent(PatchHeader(ParseDiffStat(b.Patch)))
	}
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

// PatchTitle names the changes of a step during a run. The notice of a
// history move names its changes ui.ChangesTitle instead.
const PatchTitle = "Diff"

// PatchHeader summarizes st for a collapsed patch row, as a tool call's
// header names its call: PatchTitle as ui.TitleChunk gives it, and then the
// files changed as a history move's notice counts them, such as
// "Diff 2 files  +15 -5".
func PatchHeader(st DiffStat) style.StyledText {
	chunks := append([]style.TextChunk{ui.TitleChunk(PatchTitle), style.Plain(" ")}, ui.DiffSummary(st.Files, st.Additions, st.Deletions)...)
	return style.NewStyledText(chunks...)
}
