package session

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"testing"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/database"
	_ "github.com/unaquatic/sqlite" // registers the driver that database.Open uses
)

// openTestDB opens a database in a temporary directory.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), database.FileName))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// newProjectStore opens a store working on behalf of the project whose
// worktree is dir, as the app opens it.
func newProjectStore(t *testing.T, db *sql.DB, dir string) *Store {
	t.Helper()
	project, err := database.Project(t.Context(), db, dir)
	if err != nil {
		t.Fatal(err)
	}
	store, err := Open(t.Context(), db, project)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return newProjectStore(t, openTestDB(t), "/work/project")
}

// appendAt moves the session's head to parent and appends a message there. A
// session that is not stored yet has no head to move.
func appendAt(t *testing.T, store *Store, sessionID message.SessionID, parent, id message.ID) message.Content {
	t.Helper()
	ctx := context.Background()
	err := store.SetHead(ctx, sessionID, Head{ID: parent})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("set head %q: %v", parent, err)
	}
	msg := message.Content{ID: id, Role: message.RoleUser}
	if err := store.AppendMessage(ctx, sessionID, &msg); err != nil {
		t.Fatalf("append %q: %v", id, err)
	}
	return msg
}

func ids(msgs []message.Content) []message.ID {
	out := make([]message.ID, len(msgs))
	for i, m := range msgs {
		out[i] = m.ID
	}
	return out
}

func equal(got, want []message.ID) bool { return slices.Equal(got, want) }

// fork builds a session whose history splits after msg_2:
//
//	msg_1 -> msg_2 -> msg_3a -> msg_4a
//	              \-> msg_3b
func fork(t *testing.T, store *Store) message.SessionID {
	t.Helper()
	const id = "ses_tree"
	appendAt(t, store, id, "", "msg_1")
	appendAt(t, store, id, "msg_1", "msg_2")
	appendAt(t, store, id, "msg_2", "msg_3a")
	appendAt(t, store, id, "msg_3a", "msg_4a")
	appendAt(t, store, id, "msg_2", "msg_3b")
	return id
}

func TestAppendMessage_LinksToHeadAndAdvancesIt(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	first := message.Content{ID: "msg_1", Role: message.RoleUser}
	if err := store.AppendMessage(ctx, "ses_1", &first); err != nil {
		t.Fatalf("append first: %v", err)
	}
	if first.ParentID != nil {
		t.Errorf("first message parent = %v, want nil", *first.ParentID)
	}

	second := message.Content{ID: "msg_2", Role: message.RoleAgent}
	if err := store.AppendMessage(ctx, "ses_1", &second); err != nil {
		t.Fatalf("append second: %v", err)
	}
	if second.ParentID == nil || *second.ParentID != "msg_1" {
		t.Errorf("second message parent = %v, want msg_1", second.ParentID)
	}

	sess, err := store.Session(ctx, "ses_1")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Head.ID != "msg_2" {
		t.Errorf("head = %q, want msg_2", sess.Head.ID)
	}
}

func TestBranch_FollowsTheActiveHeadOnly(t *testing.T) {
	store := newTestStore(t)
	id := fork(t, store)
	ctx := context.Background()

	// The last append left the head on the second branch.
	got, err := store.Branch(ctx, id)
	if err != nil {
		t.Fatalf("branch: %v", err)
	}
	if want := []message.ID{"msg_1", "msg_2", "msg_3b"}; !equal(ids(got), want) {
		t.Errorf("branch = %v, want %v", ids(got), want)
	}

	// The abandoned branch is still stored and reachable from its leaf.
	other, err := store.Path(ctx, id, "msg_4a")
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	if want := []message.ID{"msg_1", "msg_2", "msg_3a", "msg_4a"}; !equal(ids(other), want) {
		t.Errorf("path to msg_4a = %v, want %v", ids(other), want)
	}
}

func TestMessages_ReturnsEveryBranchInCreationOrder(t *testing.T) {
	store := newTestStore(t)
	id := fork(t, store)

	got, err := store.Messages(t.Context(), id)
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if want := []message.ID{"msg_1", "msg_2", "msg_3a", "msg_4a", "msg_3b"}; !equal(ids(got), want) {
		t.Errorf("messages = %v, want %v", ids(got), want)
	}
	if p := got[4].ParentID; p == nil || *p != "msg_2" {
		t.Errorf("msg_3b parent = %v, want msg_2", p)
	}

	if none, err := store.Messages(t.Context(), "ses_empty"); err != nil || len(none) != 0 {
		t.Errorf("messages of an empty session = %v, %v; want none", ids(none), err)
	}
}

func TestChildren_ReturnsSiblingsInCreationOrder(t *testing.T) {
	store := newTestStore(t)
	id := fork(t, store)
	ctx := context.Background()

	got, err := store.Children(ctx, id, "msg_2")
	if err != nil {
		t.Fatalf("children: %v", err)
	}
	if want := []message.ID{"msg_3a", "msg_3b"}; !equal(ids(got), want) {
		t.Errorf("children of msg_2 = %v, want %v", ids(got), want)
	}

	roots, err := store.Children(ctx, id, "")
	if err != nil {
		t.Fatalf("roots: %v", err)
	}
	if want := []message.ID{"msg_1"}; !equal(ids(roots), want) {
		t.Errorf("roots = %v, want %v", ids(roots), want)
	}
}

func TestLeaf(t *testing.T) {
	store := newTestStore(t)
	id := fork(t, store)
	ctx := context.Background()

	tests := []struct {
		name string
		from message.ID
		want message.ID
	}{
		{"descends to the end of a branch", "msg_3a", "msg_4a"},
		{"returns a childless message itself", "msg_3b", "msg_3b"},
		{"prefers the newest branch at a fork", "msg_2", "msg_3b"},
		{"searches the whole session when empty", "", "msg_3b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.Leaf(ctx, id, tt.from)
			if err != nil {
				t.Fatalf("leaf: %v", err)
			}
			if got != tt.want {
				t.Errorf("leaf from %q = %q, want %q", tt.from, got, tt.want)
			}
		})
	}
	t.Run("reports an unknown message", func(t *testing.T) {
		if _, err := store.Leaf(ctx, id, "msg_missing"); !errors.Is(err, ErrMessageNotFound) {
			t.Errorf("err = %v, want ErrMessageNotFound", err)
		}
	})
	t.Run("returns nothing for a session without messages", func(t *testing.T) {
		if got, err := store.Leaf(ctx, "ses_empty", ""); got != "" || err != nil {
			t.Errorf("leaf = %q, %v; want empty", got, err)
		}
	})
}

func TestDeleteMessage_RemovesSubtreeAndRepairsHead(t *testing.T) {
	store := newTestStore(t)
	id := fork(t, store)
	ctx := context.Background()

	if err := store.SetHead(ctx, id, Head{ID: "msg_4a", Forward: "msg_4a"}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteMessage(ctx, id, "msg_3a"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := store.Message(ctx, id, "msg_4a"); err != ErrMessageNotFound {
		t.Errorf("descendant survived the delete, err = %v", err)
	}
	sess, err := store.Session(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Head.ID != "msg_2" {
		t.Errorf("head = %q, want it to fall back to msg_2", sess.Head.ID)
	}
	if sess.Head.Forward != "" {
		t.Errorf("forward = %q, want it cleared", sess.Head.Forward)
	}

	if err := store.DeleteMessage(ctx, id, "msg_3a"); err != ErrMessageNotFound {
		t.Errorf("deleting a missing message returned %v, want ErrMessageNotFound", err)
	}
}
