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

// messageTimes returns the stored creation and update times of a message.
func messageTimes(t *testing.T, store *Store, sessionID message.SessionID, id message.ID) (created, updated string) {
	t.Helper()
	if err := store.db.QueryRowContext(t.Context(), `
		SELECT created_at, updated_at FROM messages WHERE session_id = ? AND id = ?`,
		sessionID, string(id)).Scan(&created, &updated); err != nil {
		t.Fatalf("message %s: %v", id, err)
	}
	return created, updated
}

// TestDeleteMessage_RemovesSubtreeAndRepairsHead checks that deleting a
// message removes it and every message below it, and no other, that no read
// or change of the conversation finds them afterwards, and that a head left
// inside the subtree falls back to the deleted message's parent while its
// forward marker is cleared.
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

	if n := countRows(t, store, "messages", id); n != 3 {
		t.Errorf("%d message rows left, want the 3 outside the subtree", n)
	}
	for _, msg := range []message.ID{"msg_3a", "msg_4a"} {
		if _, err := store.Message(ctx, id, msg); err != ErrMessageNotFound {
			t.Errorf("%s survived the delete, err = %v", msg, err)
		}
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

	if all, err := store.Messages(ctx, id); err != nil || !equal(ids(all), []message.ID{"msg_1", "msg_2", "msg_3b"}) {
		t.Errorf("messages = %v, %v; want the ones left", ids(all), err)
	}
	if kids, err := store.Children(ctx, id, "msg_2"); err != nil || !equal(ids(kids), []message.ID{"msg_3b"}) {
		t.Errorf("children of msg_2 = %v, %v; want msg_3b", ids(kids), err)
	}
	if forks, err := store.Forks(ctx, id); err != nil || len(forks) != 0 {
		t.Errorf("forks = %v, %v; want none once one branch is deleted", forks, err)
	}
	if n, err := store.Size(ctx, id, "msg_2"); err != nil || n != 2 {
		t.Errorf("size of msg_2 = %d, %v; want 2", n, err)
	}
	if path, err := store.Path(ctx, id, "msg_4a"); err != nil || len(path) != 0 {
		t.Errorf("path to a deleted message = %v, %v; want none", ids(path), err)
	}
	if _, err := store.Leaf(ctx, id, "msg_3a"); err != ErrMessageNotFound {
		t.Errorf("leaf of a deleted message: err = %v, want ErrMessageNotFound", err)
	}
	if err := store.AppendFork(ctx, id, "msg_3a", &message.Content{ID: "msg_x", Role: message.RoleUser, Type: message.TypeTitle}); !errors.Is(err, ErrMessageNotFound) {
		t.Errorf("fork below a deleted message = %v, want ErrMessageNotFound", err)
	}
	if err := store.DeleteMessage(ctx, id, "msg_3a"); err != ErrMessageNotFound {
		t.Errorf("deleting a deleted message returned %v, want ErrMessageNotFound", err)
	}
	if err := store.DeleteMessage(ctx, id, "msg_missing"); err != ErrMessageNotFound {
		t.Errorf("deleting a missing message returned %v, want ErrMessageNotFound", err)
	}
}

// TestDeleteMessage_OfTheRootLeavesNoHead checks that deleting a session's
// first message leaves its head on nothing, which is stored as NULL, and the
// session showing no messages.
func TestDeleteMessage_OfTheRootLeavesNoHead(t *testing.T) {
	store := newTestStore(t)
	id := fork(t, store)
	ctx := t.Context()
	if err := store.DeleteMessage(ctx, id, "msg_1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if head, forward := headColumns(t, store, id); head.Valid || forward.Valid {
		t.Errorf("head stored as %v, %v; want NULL for both", head, forward)
	}
	if branch, err := store.Branch(ctx, id); err != nil || len(branch) != 0 {
		t.Errorf("branch = %v, %v; want none", ids(branch), err)
	}
	if leaf, err := store.Leaf(ctx, id, ""); err != nil || leaf != "" {
		t.Errorf("leaf = %q, %v; want none", leaf, err)
	}
	appendAt(t, store, id, "", "msg_new")
	if branch, err := store.Branch(ctx, id); err != nil || !equal(ids(branch), []message.ID{"msg_new"}) {
		t.Errorf("branch after a new first message = %v, %v; want msg_new", ids(branch), err)
	}
}

// TestPrompts_ReadsTheLatestOnTheActiveBranch checks that Prompts returns
// the newest prompts of the active branch, oldest first, with their parts,
// up to its limit, and counts every prompt on the branch, leaving out the
// replies and the prompts of other branches.
func TestPrompts_ReadsTheLatestOnTheActiveBranch(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	const id = "ses_prompts"
	add := func(parent, msgID message.ID, role message.Role) {
		t.Helper()
		if err := store.SetHead(ctx, id, Head{ID: parent}); err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("set head %q: %v", parent, err)
		}
		msg := message.Content{ID: msgID, Role: role, Parts: []message.ContentPart{message.TextPart{ID: message.PartID("prt_" + msgID), Text: string(msgID)}}}
		if err := store.AppendMessage(ctx, id, &msg); err != nil {
			t.Fatalf("append %q: %v", msgID, err)
		}
	}
	//	u1 -> a1 -> u2 -> a2 -> u3 -> a3
	//	                  \-> u2b (abandoned)
	add("", "u1", message.RoleUser)
	add("u1", "a1", message.RoleAgent)
	add("a1", "u2b", message.RoleUser)
	add("a1", "u2", message.RoleUser)
	add("u2", "a2", message.RoleAgent)
	add("a2", "u3", message.RoleUser)
	add("u3", "a3", message.RoleAgent)

	for _, tt := range []struct {
		limit int
		want  []message.ID
	}{
		{limit: 10, want: []message.ID{"u1", "u2", "u3"}},
		{limit: 2, want: []message.ID{"u2", "u3"}},
		// A limit below one is taken as one.
		{limit: 0, want: []message.ID{"u3"}},
	} {
		got, total, err := store.Prompts(ctx, id, tt.limit)
		if err != nil {
			t.Fatalf("prompts: %v", err)
		}
		if total != 3 || !equal(ids(got), tt.want) {
			t.Errorf("limit %d: prompts = %v of %d, want %v of 3", tt.limit, ids(got), total, tt.want)
		}
		for _, m := range got {
			if len(m.Parts) != 1 {
				t.Errorf("limit %d: prompt %s has %d parts, want its one", tt.limit, m.ID, len(m.Parts))
			}
		}
	}

	if got, total, err := store.Prompts(ctx, "ses_missing", 5); err != nil || total != 0 || len(got) != 0 {
		t.Errorf("unstored session: prompts = %v of %d, %v, want none", ids(got), total, err)
	}
}

// TestAppendFork checks that a fork is stored below its parent, each
// message below the one before it, of its type, without moving the head or
// the session's update time, and that every read of the conversation leaves
// it out, so that a prompt whose only children are forks is still a leaf;
// that ForkChildren and Path read it, and chat messages read back with the
// zero Type; that neither append takes the other's messages; and that
// deleting its parent deletes it.
func TestAppendFork(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(t)
	const id = "ses_fork"
	appendAt(t, store, id, "", "msg_1")
	appendAt(t, store, id, "msg_1", "msg_2")
	before, err := store.Session(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	ask := message.Content{ID: "msg_ask", Role: message.RoleUser, Type: message.TypeTitle, Parts: []message.ContentPart{message.TextPart{Text: "summarize"}}}
	reply := message.Content{ID: "msg_reply", Role: message.RoleAgent, Type: message.TypeTitle, Parts: []message.ContentPart{message.TextPart{Text: "A title"}}}
	if err := store.AppendFork(ctx, id, "msg_2", &ask, &reply); err != nil {
		t.Fatal(err)
	}
	if ask.ParentID == nil || *ask.ParentID != "msg_2" || reply.ParentID == nil || *reply.ParentID != "msg_ask" {
		t.Errorf("parents = %v, %v; want msg_2 then msg_ask", ask.ParentID, reply.ParentID)
	}

	after, err := store.Session(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Head != before.Head || !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("session = %+v, want its head and update time as before: %+v", after, before)
	}
	branch, err := store.Branch(ctx, id)
	if err != nil || !equal(ids(branch), []message.ID{"msg_1", "msg_2"}) {
		t.Errorf("branch = %v, %v; want msg_1, msg_2", ids(branch), err)
	}
	all, err := store.Messages(ctx, id)
	if err != nil || !equal(ids(all), []message.ID{"msg_1", "msg_2"}) {
		t.Errorf("messages = %v, %v; want the visible ones", ids(all), err)
	}
	if kids, err := store.Children(ctx, id, "msg_2"); err != nil || len(kids) != 0 {
		t.Errorf("children of msg_2 = %v, %v; want none", ids(kids), err)
	}
	if forks, err := store.Forks(ctx, id); err != nil || len(forks) != 0 {
		t.Errorf("forks = %v, %v; want none", forks, err)
	}
	for _, from := range []message.ID{"msg_1", ""} {
		if leaf, err := store.Leaf(ctx, id, from); err != nil || leaf != "msg_2" {
			t.Errorf("leaf from %q = %q, %v; want msg_2", from, leaf, err)
		}
	}
	if n, err := store.Size(ctx, id, "msg_2"); err != nil || n != 1 {
		t.Errorf("size of msg_2 = %d, %v; want 1", n, err)
	}

	forked, err := store.ForkChildren(ctx, id, "msg_2", message.TypeTitle)
	if err != nil || !equal(ids(forked), []message.ID{"msg_ask"}) {
		t.Fatalf("title children = %v, %v; want msg_ask", ids(forked), err)
	}
	if other, err := store.ForkChildren(ctx, id, "msg_2", "other"); err != nil || len(other) != 0 {
		t.Errorf("children of another type = %v, %v; want none", ids(other), err)
	}
	if all, err := store.ForkMessages(ctx, id); err != nil || !equal(ids(all), []message.ID{"msg_ask", "msg_reply"}) {
		t.Errorf("fork messages = %v, %v; want the fork's, in order", ids(all), err)
	}
	path, err := store.Path(ctx, id, "msg_reply")
	if err != nil || !equal(ids(path), []message.ID{"msg_1", "msg_2", "msg_ask", "msg_reply"}) {
		t.Errorf("path to the reply = %v, %v; want the fork after the branch", ids(path), err)
	}
	if got := path[len(path)-1].Parts; len(got) != 1 || got[0].(message.TextPart).Text != "A title" {
		t.Errorf("reply parts = %+v, want the stored text", got)
	}
	types := make([]message.Type, len(path))
	for i, m := range path {
		types[i] = m.Type
	}
	if !slices.Equal(types, []message.Type{"", "", message.TypeTitle, message.TypeTitle}) {
		t.Errorf("types along the path = %q, want chat as the zero Type, then the fork's", types)
	}

	if err := store.AppendFork(ctx, id, "msg_missing", &message.Content{ID: "msg_x", Role: message.RoleUser, Type: message.TypeTitle}); !errors.Is(err, ErrMessageNotFound) {
		t.Errorf("fork below a missing message = %v, want ErrMessageNotFound", err)
	}
	if err := store.AppendFork(ctx, id, "msg_2", &message.Content{ID: "msg_y", Role: message.RoleUser, Type: message.TypeChat}); !errors.Is(err, ErrMessageType) {
		t.Errorf("fork of a chat message = %v, want ErrMessageType", err)
	}
	if err := store.AppendMessage(ctx, id, &message.Content{ID: "msg_z", Role: message.RoleUser, Type: message.TypeTitle}); !errors.Is(err, ErrMessageType) {
		t.Errorf("conversation message of a title type = %v, want ErrMessageType", err)
	}
	if err := store.DeleteMessage(ctx, id, "msg_2"); err != nil {
		t.Fatal(err)
	}
	if path, err := store.Path(ctx, id, "msg_reply"); err != nil || len(path) != 0 {
		t.Errorf("path to the reply after deleting its parent = %v, %v; want none", ids(path), err)
	}
}
