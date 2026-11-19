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
// files the move changed and the hash of that tree, as the Checkout of its
// result. The tree comes from the transcript, so the result
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
	return extension.MoveResult{Files: e.fileDiffs(ctx, before, after), Checkout: tree.String()}, nil
}

// TreeAt returns the workspace tree recorded for head, a message of branch,
// as TreeAfter finds it once head has run. An empty head resolves to the
// tree the branch started from. branch runs past head to a leaf, as
// extension.Move holds it, so that a move back to the message before a
// prompt restores the tree that prompt was sent with. It refuses with
// ErrNoSnapshot when no tree was recorded.
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
// branch had run. When the next message is a prompt that recorded a tree, it
// is that tree: a prompt changes no files, so its tree is the workspace just
// before it, including what changed after the messages before it ended,
// such as edits made outside the session between turns. Otherwise it is the
// newest tree the first n messages ended with. It returns the zero Hash when
// there is no such tree.
func TreeAfter(branch []message.Content, n int) Hash {
	if n < len(branch) && branch[n].Role == message.RoleUser {
		if tree := TreeIn(branch[n].Parts); !tree.IsZero() {
			return tree
		}
	}
	for _, m := range slices.Backward(branch[:n]) {
		if tree := TreeIn(m.Parts); !tree.IsZero() {
			return tree
		}
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
