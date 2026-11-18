package ui

import (
	"testing"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension/snapshot"
	"github.com/unaquatic/flow/terminal/ui"
)

// TestLastTurnTrees checks that the last turn runs from the tree its prompt
// ended with to the last tree a message after it ended with, passing over
// notices and replies without a tree, and that a transcript without a
// prompt, or whose prompt has no tree, has no turn to diff.
func TestLastTurnTrees(t *testing.T) {
	mid := mustHash("1111111111111111111111111111111111111111")
	snap := func(h snapshot.Hash) []message.ContentPart {
		return []message.ContentPart{snapshot.SnapshotPart{Tree: h}}
	}
	msgs := []ui.ChatMessage{
		{ID: "u1", Role: message.RoleUser, Parts: snap(mid)},
		{ID: "a1", Role: message.RoleAgent, Parts: snap(base)},
		{ID: "u2", Role: message.RoleUser, Parts: snap(base)},
		{ID: "a2", Role: message.RoleAgent, Parts: snap(mid)},
		{ID: "a3", Role: message.RoleAgent, Parts: snap(tree)},
		{ID: "a4", Role: message.RoleAgent},
		{ID: "n1", Notice: &ui.Notice{Text: "Chat cleared"}},
	}
	if from, to, ok := LastTurnTrees(msgs); !ok || from != base || to != tree {
		t.Errorf("LastTurnTrees = %s..%s, %v; want %s..%s", from, to, ok, base, tree)
	}
	if from, to, ok := LastTurnTrees(msgs[:3]); !ok || from != base || to != base {
		t.Errorf("a prompt without replies: %s..%s, %v; want its own tree on both ends", from, to, ok)
	}
	if _, _, ok := LastTurnTrees(msgs[1:2]); ok {
		t.Error("a transcript without a prompt has a turn to diff")
	}
	if _, _, ok := LastTurnTrees([]ui.ChatMessage{{ID: "u1", Role: message.RoleUser}}); ok {
		t.Error("a prompt without a tree has a turn to diff")
	}
}

// TestDiffNotice checks that /diff's notice heads its files with their
// summary, and then lists each with its counts, and reads "Changed 0 files"
// for none.
func TestDiffNotice(t *testing.T) {
	lines := DiffNotice(SplitPatch(stepPatch))
	var got []string
	for _, l := range lines {
		got = append(got, l.PlainText())
	}
	if want := []string{"Changed 1 file  +1 -1", "calc.go  +1 -1"}; len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("lines = %q, want %q", got, want)
	}
	if got := DiffNotice(nil)[0].PlainText(); got != "Changed 0 files" {
		t.Errorf("no files = %q, want Changed 0 files", got)
	}
}
