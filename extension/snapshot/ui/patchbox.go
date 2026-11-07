package ui

import (
	"strings"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension/snapshot"
	"github.com/unaquatic/flow/terminal/ui"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/widget"
)

// PatchBox is the ui.PartBox of the workspace changes one agent step made: a
// block whose header sums them up, as PatchHeader gives it, and whose details
// show them as a unified diff, which Ctrl+O expands with the tool calls. It
// stays hidden until a nonempty patch is set.
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
	// Body displays the unified diff.
	Body *widget.Markdown
}

var (
	_ ui.SegmentView = (*PatchBox)(nil)
	_ ui.BoxContent  = (*PatchBox)(nil)
)

// NewPatchBox creates a hidden, collapsed patch block on the theme's raised
// surface.
func NewPatchBox(theme *style.Theme, fg style.Color) *PatchBox {
	if theme == nil {
		theme = ui.DefaultTheme()
	}
	surface := theme.ResolveSurfaceRaised()
	b := &PatchBox{
		PartBox: ui.NewPartBox(theme, fg, surface),
		Body:    ui.NewToolBody(theme, fg, surface),
	}
	b.Content = b
	b.Shape = ui.ShapeBlock
	b.Marked = false
	b.Details.Add(b.Body)
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

// SetPatch displays patch, hiding the box when it is empty.
func (b *PatchBox) SetPatch(patch string) {
	b.Patch = patch
	b.ShowHeader()
	b.Body.SetContent(ui.ToolFence("diff", patch))
	b.SetGone(patch == "")
}

// ShowHeader writes the summary of Patch, as PatchHeader gives it, once a
// patch is set.
func (b *PatchBox) ShowHeader() {
	if b.Patch != "" {
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

// ParseDiffStat counts the files and changed lines in a Git unified diff. Only
// lines inside hunks are counted, so file headers such as "--- a/x" are not
// mistaken for deletions.
func ParseDiffStat(patch string) DiffStat {
	var st DiffStat
	inHunk := false
	for line := range strings.Lines(patch) {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			st.Files++
			inHunk = false
		case strings.HasPrefix(line, "@@"):
			inHunk = true
		case !inHunk:
		case strings.HasPrefix(line, "+"):
			st.Additions++
		case strings.HasPrefix(line, "-"):
			st.Deletions++
		}
	}
	return st
}

// PatchHeader summarizes st for a collapsed patch block, as a tool call's
// header names its call: "changes" as ui.TitleChunk gives it, and then the
// files changed as a history move's notice counts them, such as
// "changes 2 files  +15 -5".
func PatchHeader(st DiffStat) style.StyledText {
	chunks := append([]style.TextChunk{ui.TitleChunk("changes"), style.Plain(" ")}, ui.DiffSummary(st.Files, st.Additions, st.Deletions)...)
	return style.NewStyledText(chunks...)
}
