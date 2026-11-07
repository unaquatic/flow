package app

import (
	"errors"
	"slices"
	"testing"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/extension/snapshot"
)

// treeIDs returns the IDs of the session's messages in the order Tree lists
// them, failing the test when the tree cannot be read.
func treeIDs(t *testing.T, w Session) ([]message.ID, Tree) {
	t.Helper()
	tree, err := w.Tree()
	if err != nil {
		t.Fatalf("tree: %v", err)
	}
	ids := make([]message.ID, len(tree.Messages))
	for i, m := range tree.Messages {
		ids[i] = m.ID
	}
	return ids, tree
}

// forkedSession returns a session whose second turn was undone and replaced,
// and the IDs of its six messages in creation order: the first turn, the
// abandoned second turn, and the turn that replaced it, each a prompt and its
// reply. The workspace holds the replacement's a.txt, "v3".
func forkedSession(t *testing.T, name message.SessionID) (Session, string, []message.ID) {
	t.Helper()
	w, worktree := newTestSession(t, name)
	seedTurn(t, w, worktree, "first", map[string]string{"a.txt": "v1"})
	seedTurn(t, w, worktree, "second", map[string]string{"a.txt": "v2"})
	if _, err := w.undo(); err != nil {
		t.Fatalf("undo: %v", err)
	}
	seedTurn(t, w, worktree, "instead", map[string]string{"a.txt": "v3"})
	ids, _ := treeIDs(t, w)
	if len(ids) != 6 {
		t.Fatalf("setup: tree holds %d messages, want 6", len(ids))
	}
	return w, worktree, ids
}

func TestTree_ListsEveryBranchAndTheHead(t *testing.T) {
	w, _, ids := forkedSession(t, "ses_tree")

	_, tree := treeIDs(t, w)
	if tree.Head != ids[5] {
		t.Errorf("head = %q, want the replacement's reply %q", tree.Head, ids[5])
	}
	// The abandoned turn and its replacement both continue the first reply.
	for _, i := range []int{2, 4} {
		if p := tree.Messages[i].ParentID; p == nil || *p != ids[1] {
			t.Errorf("message %d parent = %v, want the first reply", i, p)
		}
	}
}

func TestTree_EmptySession(t *testing.T) {
	w, _ := newTestSession(t, "ses_tree_empty")
	ids, tree := treeIDs(t, w)
	if len(ids) != 0 || tree.Head != "" {
		t.Errorf("tree = %v at %q, want no messages and no head", ids, tree.Head)
	}
}

// TestNavigate_AlongTheBranchKeepsTheRestAhead checks that moving back to a
// reply on the session's own branch restores the files after it and leaves
// the later turns for redo.
func TestNavigate_AlongTheBranchKeepsTheRestAhead(t *testing.T) {
	w, worktree := newTestSession(t, "ses_nav_back")
	seedTurn(t, w, worktree, "first", map[string]string{"a.txt": "v1"})
	seedTurn(t, w, worktree, "second", map[string]string{"a.txt": "v2"})
	ids, _ := treeIDs(t, w)

	if _, err := w.navigate(ids[1]); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if got := readFile(t, worktree, "a.txt"); got != "v1" {
		t.Fatalf("after navigating to the first reply: a.txt = %q, want v1", got)
	}
	sess := storedSession(t, w.app.sessions, w.id)
	if want := (session.Head{ID: ids[1], Forward: ids[3]}); sess.Head != want {
		t.Fatalf("head = %+v, want %+v", sess.Head, want)
	}

	if _, err := w.redo(); err != nil {
		t.Fatalf("redo: %v", err)
	}
	if got := readFile(t, worktree, "a.txt"); got != "v2" {
		t.Fatalf("after redo: a.txt = %q, want v2", got)
	}
}

// TestNavigate_OntoAnotherBranch checks that moving to a reply on an
// abandoned branch restores that branch's files and ends the transcript at
// the reply.
func TestNavigate_OntoAnotherBranch(t *testing.T) {
	w, worktree, ids := forkedSession(t, "ses_nav_other")

	if _, err := w.navigate(ids[3]); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if got := readFile(t, worktree, "a.txt"); got != "v2" {
		t.Fatalf("after navigating to the abandoned reply: a.txt = %q, want v2", got)
	}
	branch := branchOf(t, w.app.sessions, w.id)
	got := make([]message.ID, len(branch))
	for i, m := range branch {
		got[i] = m.ID
	}
	if want := ids[:4]; !slices.Equal(got, want) {
		t.Fatalf("transcript = %v, want %v", got, want)
	}
	if sess := storedSession(t, w.app.sessions, w.id); sess.Head.MovedBack() {
		t.Errorf("a sideways move left %q ahead to redo", sess.Head.Forward)
	}
}

func TestNavigate_ToTheHeadChangesNothing(t *testing.T) {
	w, worktree, ids := forkedSession(t, "ses_nav_here")

	files, err := w.navigate(ids[5])
	if !errors.Is(err, ErrAlreadyThere) {
		t.Fatalf("navigate to the head = %v, want ErrAlreadyThere", err)
	}
	if files != nil {
		t.Errorf("staying put changed files: %+v", files)
	}
	if got := readFile(t, worktree, "a.txt"); got != "v3" {
		t.Errorf("a.txt = %q, want v3", got)
	}
}

// TestCheckout_BeforeAPromptForksBesideIt checks the move the session tree
// makes for a chosen prompt: the transcript ends at the message the prompt
// continues, with that point's files, so that a prompt sent next is the
// chosen prompt's sibling rather than a second reply below it. It holds for
// a prompt on another branch, and for one on the session's own.
func TestCheckout_BeforeAPromptForksBesideIt(t *testing.T) {
	tests := []struct {
		name   string
		choose int // index of the chosen prompt in creation order
		head   func(ids []message.ID) session.Head
	}{
		// A move onto another branch is sideways, and leaves nothing ahead.
		{name: "another branch", choose: 2, head: func(ids []message.ID) session.Head {
			return session.Head{ID: ids[1]}
		}},
		{name: "own branch", choose: 4, head: func(ids []message.ID) session.Head {
			return session.Head{ID: ids[1], Forward: ids[5]}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, worktree, ids := forkedSession(t, "ses_edit_prompt")
			if _, err := w.Checkout(ids[tt.choose]); err != nil {
				t.Fatalf("checkout: %v", err)
			}
			if got := readFile(t, worktree, "a.txt"); got != "v1" {
				t.Errorf("a.txt = %q, want the first turn's v1", got)
			}
			if sess, want := storedSession(t, w.app.sessions, w.id), tt.head(ids); sess.Head != want {
				t.Errorf("head = %+v, want %+v", sess.Head, want)
			}

			edited := &message.Content{
				ID:    message.NewID(),
				Role:  message.RoleUser,
				Parts: []message.ContentPart{message.TextPart{Text: "edited"}},
			}
			appendMessage(t, w.app.sessions, w.id, edited)
			if p := edited.ParentID; p == nil || *p != ids[1] {
				t.Fatalf("edited prompt's parent = %v, want the chosen prompt's parent %q", p, ids[1])
			}
			siblings, err := w.app.sessions.Children(t.Context(), w.id, ids[1])
			if err != nil {
				t.Fatal(err)
			}
			if len(siblings) != 3 || siblings[2].ID != edited.ID {
				t.Errorf("the first reply has %d children, want both prompts and the edited one last", len(siblings))
			}
		})
	}
}

func TestNavigate_Rejects(t *testing.T) {
	t.Run("unknown message", func(t *testing.T) {
		w, _, _ := forkedSession(t, "ses_nav_unknown")
		if _, err := w.Navigate("msg_nonexistent"); !errors.Is(err, session.ErrMessageNotFound) {
			t.Fatalf("Navigate = %v, want ErrMessageNotFound", err)
		}
	})
	t.Run("empty message", func(t *testing.T) {
		w, _, _ := forkedSession(t, "ses_nav_empty")
		if _, err := w.Navigate(""); !errors.Is(err, session.ErrMessageNotFound) {
			t.Fatalf("Navigate = %v, want ErrMessageNotFound", err)
		}
	})
	t.Run("disabled snapshots", func(t *testing.T) {
		w, _, ids := forkedSession(t, "ses_nav_disabled")
		snapshotsOf(t, w).Service.Disabled = true
		if _, err := w.Navigate(ids[1]); !errors.Is(err, snapshot.ErrDisabled) {
			t.Fatalf("Navigate = %v, want ErrSnapshotDisabled", err)
		}
	})
}
