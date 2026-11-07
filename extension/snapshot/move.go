package snapshot

import (
	"context"
	"fmt"
	"log/slog"
	"slices"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension"
)

// OnMove resets the workspace to the tree recorded for m.Head, and reports the
// files the move changed. The tree comes from the transcript, so the result
// depends only on the destination and never on the point being left: undo,
// redo, and a branch switch are this one operation.
//
// It refuses the move with ErrDisabled while snapshots are disabled, and
// with ErrNoSnapshot when no tree was recorded for m.Head.
func (e *Extension) OnMove(ctx context.Context, m extension.Move) (extension.MoveResult, error) {
	if e.Disabled() {
		return extension.MoveResult{}, ErrDisabled
	}
	tree, err := TreeAt(m.Branch, m.Head)
	if err != nil {
		return extension.MoveResult{}, err
	}
	before, err := e.Service.Track(ctx)
	if err != nil {
		slog.Warn("track snapshot before checkout", "err", err)
	}
	if err := e.Service.Checkout(ctx, tree); err != nil {
		return extension.MoveResult{}, fmt.Errorf("checkout %s: %w", tree, err)
	}
	after, err := e.Service.Track(ctx)
	if err != nil {
		slog.Warn("track snapshot after checkout", "err", err)
	}
	return extension.MoveResult{Files: e.fileDiffs(ctx, before, after)}, nil
}

// TreeAt returns the workspace tree recorded for head, a message of branch.
// An empty head resolves to the tree the branch started from. It refuses
// with ErrNoSnapshot when no tree was recorded.
func TreeAt(branch []message.Content, head message.ID) (Hash, error) {
	visible := 0
	if head != "" {
		idx := slices.IndexFunc(branch, func(m message.Content) bool { return m.ID == head })
		if idx < 0 {
			return Hash{}, fmt.Errorf("snapshot: no message %s on the branch", head)
		}
		visible = idx + 1
	}
	tree := TreeAfter(branch, visible)
	if tree.IsZero() {
		return Hash{}, ErrNoSnapshot
	}
	return tree, nil
}

// TreeAfter returns the workspace as it stood once the first n messages of a
// branch had run: the newest tree they ended with. When none of them recorded
// one, as before the first message, it is the tree of the next message if
// that is a prompt, which changes no files, so that its tree is the workspace
// before it. It returns the zero Hash when there is no such tree.
func TreeAfter(branch []message.Content, n int) Hash {
	for _, m := range slices.Backward(branch[:n]) {
		if tree := TreeIn(m.Parts); !tree.IsZero() {
			return tree
		}
	}
	if n < len(branch) && branch[n].Role == message.RoleUser {
		return TreeIn(branch[n].Parts)
	}
	return Hash{}
}

// fileDiffs returns the files that differ between two trees, with their line
// counts. A failed Track leaves the zero Hash, which has been logged already
// and gives none.
func (e *Extension) fileDiffs(ctx context.Context, from, to Hash) []extension.FileDiff {
	if from.IsZero() || to.IsZero() || from == to {
		return nil
	}
	full, err := e.Service.FileDiffs(ctx, from, to)
	if err != nil {
		slog.Warn("snapshot diff", "err", err)
		return nil
	}
	out := make([]extension.FileDiff, 0, len(full))
	for _, fd := range full {
		out = append(out, extension.FileDiff{
			File:      fd.Path,
			Additions: fd.Additions,
			Deletions: fd.Deletions,
			Status:    fd.Status.String(),
		})
	}
	return out
}
