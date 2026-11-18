package ui

import (
	"log/slog"
	"slices"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension/snapshot"
	"github.com/unaquatic/flow/terminal/ui"
	"github.com/unaquatic/fluid/style"
)

// CommandDiff is the slash command that prints what the last turn changed.
const CommandDiff ui.Command = "diff"

var _ ui.CommandExtension = (*Extension)(nil)

// SlashCommands returns /diff, which runs as ShowDiff does.
func (e *Extension) SlashCommands() []ui.SlashCommand {
	return []ui.SlashCommand{{Name: CommandDiff, Desc: "Show what the last turn changed", Run: e.ShowDiff}}
}

// ShowDiff adds a notice to the transcript of what the last turn changed, as
// LastTurnTrees bounds it and DiffNotice sums it up, or says that no turn
// has changed anything yet.
func (e *Extension) ShowDiff(s *ui.Service) []ui.UIEvent {
	from, to, ok := LastTurnTrees(s.Messages())
	if !ok {
		return s.ShowToast("No turn to diff yet")
	}
	var patch string
	if from != to {
		var err error
		if patch, err = e.Patch(from, to); err != nil {
			slog.Warn("diff command", "from", from, "to", to, "err", err)
			return s.ShowToast("Reading the changes failed: " + err.Error())
		}
	}
	return s.AddNotice(ui.Notice{Lines: DiffNotice(SplitPatch(patch))})
}

// Patch returns the patch between two trees, as LoadPatch reads it, from
// Patches when it was read before.
func (e *Extension) Patch(from, to snapshot.Hash) (string, error) {
	key := [2]snapshot.Hash{from, to}
	if patch, ok := e.Patches[key]; ok {
		return patch, nil
	}
	patch, err := e.ReadPatch(e.ctx, from, to)
	if err != nil {
		return "", err
	}
	e.Patches[key] = patch
	return patch, nil
}

// LastTurnTrees returns the trees bounding the last turn of msgs, a
// transcript: the one its prompt ended with, and the last one a message
// after it ended with, or the prompt's own while none has yet. ok is false
// when msgs hold no prompt, or the prompt recorded no tree.
func LastTurnTrees(msgs []ui.ChatMessage) (from, to snapshot.Hash, ok bool) {
	prompt := -1
	for j, m := range slices.Backward(msgs) {
		if m.Notice == nil && m.Role == message.RoleUser {
			prompt = j
			break
		}
	}
	if prompt < 0 {
		return snapshot.Hash{}, snapshot.Hash{}, false
	}
	from = snapshot.TreeIn(msgs[prompt].Parts)
	if from.IsZero() {
		return snapshot.Hash{}, snapshot.Hash{}, false
	}
	to = from
	for _, m := range msgs[prompt+1:] {
		if tree := snapshot.TreeIn(m.Parts); m.Notice == nil && !tree.IsZero() {
			to = tree
		}
	}
	return from, to, true
}

// DiffNotice returns the lines of /diff's notice of files: PatchHeader of
// them, such as "Changed 3 files  +44 -11", or "Changed 0 files", and then a
// line for each file, its path and its counts as ui.DiffCounts colors them.
func DiffNotice(files []FilePatch) []style.StyledText {
	if len(files) == 0 {
		return []style.StyledText{style.NewStyledText(ui.TitleChunk(PatchTitle), style.Plain(" 0 files"))}
	}
	lines := []style.StyledText{PatchHeader(StatOf(files))}
	for _, f := range files {
		lines = append(lines, style.NewStyledText(append([]style.TextChunk{style.Plain(f.Path + "  ")},
			ui.DiffCounts(f.Additions, f.Deletions)...)...))
	}
	return lines
}
