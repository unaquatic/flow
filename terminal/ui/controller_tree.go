package ui

import (
	"errors"
	"fmt"
	"slices"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
)

// ShowTree opens the active session's history as a tree at the top of the
// input box, searched from the input, with the transcript's end selected.
func (s *Service) ShowTree() []UIEvent {
	sess, ok := s.ActiveSession()
	if !ok {
		return s.ShowToast("No active session")
	}
	tree, err := sess.Tree()
	if err != nil {
		return s.ShowToast(fmt.Sprintf("Loading the session tree failed: %v", err))
	}
	if len(tree.Messages) == 0 {
		return s.ShowToast("No messages yet")
	}
	return s.OpenTree(tree)
}

// OpenTree opens tree in the banner, searched from the emptied input.
func (s *Service) OpenTree(tree app.Tree) []UIEvent {
	s.OpenBanner(TreeBanner)
	s.state.Tree = NewSessionTree(tree)
	return s.TreeShown()
}

// TreeShown returns the event that draws the open session tree as it is, or
// nothing when no tree is open.
func (s *Service) TreeShown() []UIEvent {
	v, ok := s.TreeView()
	if !ok {
		return nil
	}
	return []UIEvent{EventTreeShow{Tree: v}}
}

// TreeView returns what the open session tree draws, and false when no tree
// is open.
func (s *Service) TreeView() (TreeView, bool) {
	if s.state.Tree == nil {
		return TreeView{}, false
	}
	return s.state.Tree.View(s.TreeRows()), true
}

// TreeRows returns the number of lines the session tree shows at once:
// treeMaxRows, within the input box's bound. One of them is the insert line,
// so a page of rows is a line shorter.
func (s *Service) TreeRows() int {
	return BoundRows(treeMaxRows, s.state.MenuRows)
}

// UpdateTree applies change to the open session tree and redraws it. It does
// nothing when no tree is open.
func (s *Service) UpdateTree(change func(*SessionTree)) []UIEvent {
	if s.state.Tree == nil {
		return nil
	}
	change(s.state.Tree)
	return s.TreeShown()
}

// MoveTree selects the row delta rows below the selected one, or above it
// for a negative delta, wrapping at both ends.
func (s *Service) MoveTree(delta int) []UIEvent {
	return s.UpdateTree(func(t *SessionTree) { t.Move(delta) })
}

// PageTree moves the selection delta pages of shown rows down, or up for a
// negative delta.
func (s *Service) PageTree(delta int) []UIEvent {
	return s.UpdateTree(func(t *SessionTree) { t.Page(delta, max(s.TreeRows()-1, 1)) })
}

// JumpTreeFork moves the selection to the next branch below it, or for a
// negative dir to the start of its own branch, as [SessionTree.JumpFork] does.
func (s *Service) JumpTreeFork(dir int) []UIEvent {
	return s.UpdateTree(func(t *SessionTree) { t.JumpFork(dir) })
}

// CycleTreeFilter moves the open session tree to its next filter.
func (s *Service) CycleTreeFilter() []UIEvent {
	return s.UpdateTree((*SessionTree).CycleFilter)
}

// ChooseTreeRow closes the session tree and moves the session to the
// selected message, as pi's /tree does:
//
//   - A prompt ends the transcript before it, at the message it continues,
//     and puts its text in the input, to edit and send again. The prompt
//     sent opens a branch beside the chosen one, so that the two prompts
//     share a parent.
//   - A reply or its tool calls end the transcript at them, for /continue to
//     pick the work up from there.
//
// The move stops the active run first and runs off the event loop, so its
// result arrives later. The app resolves the move at that point, so a run
// that moved the transcript since the tree was read cannot mislead it.
func (s *Service) ChooseTreeRow() []UIEvent {
	t := s.state.Tree
	if t == nil {
		return nil
	}
	row, ok := t.SelectedRow()
	if !ok {
		return nil
	}
	closed := s.CancelBanner()
	s.Stop()
	e := row.Entry
	return append(closed, EventTreeMoveRequested{
		MessageID: e.ID,
		Edit:      e.Role == message.RoleUser,
		Prompt:    e.Prompt,
	})
}

// CompleteTreeMove reports the files a finished move to a message chosen in
// the session tree changed. A move before a prompt also puts the prompt's
// text in the input; a move to a reply the transcript already ended at needs
// no report.
//
// A failed move reports why and leaves the input alone, so that a prompt
// sent from it does not land where it was not meant to.
func (s *Service) CompleteTreeMove(e EventTreeMoveRequested, files []app.FileDiff, err error) []UIEvent {
	switch {
	case err != nil && !errors.Is(err, app.ErrAlreadyThere):
		return s.ShowToast(checkoutErrorMessage("tree move", err))
	case e.Edit:
		return slices.Concat(s.ShowDiffToast(files), s.SetInput(e.Prompt))
	case err != nil:
		return nil
	}
	return s.ShowDiffToast(files)
}
