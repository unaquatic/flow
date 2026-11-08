package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent/message"
)

// TestEncodeTime_IsFixedWidth pins the property the column ordering rests on.
// A stored timestamp is compared as text, so every value must be the same
// width: RFC 3339 with nanoseconds omitted the fraction when it was zero,
// which made a whole second sort above every fraction of the same second.
func TestEncodeTime_IsFixedWidth(t *testing.T) {
	whole := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	tests := map[string]time.Time{
		"whole second":   whole,
		"half second":    whole.Add(500 * time.Millisecond),
		"one nanosecond": whole.Add(time.Nanosecond),
		"zero time":      {},
	}
	width := len(encodeTime(whole))
	for name, at := range tests {
		t.Run(name, func(t *testing.T) {
			if got := encodeTime(at); len(got) != width {
				t.Errorf("encodeTime(%v) = %q, want %d bytes like %q", at, got, width, encodeTime(whole))
			}
		})
	}
}

// TestListSessions_OrdersByTimeThenID checks the order the session list is
// read back in, and that a tie inside one second falls back to the id.
func TestListSessions_OrdersByTimeThenID(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()

	early := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	late := early.Add(time.Second)
	for _, s := range []struct {
		id message.SessionID
		at time.Time
	}{
		{"ses_early", early},
		{"ses_late", late},
		{"ses_tied_b", late.Add(500 * time.Millisecond)},
		{"ses_tied_a", late.Add(900 * time.Millisecond)},
	} {
		if _, err := store.db.ExecContext(ctx, `
			INSERT INTO sessions (id, project, title, created_at, updated_at) VALUES (?, ?, '', ?, ?)`,
			s.id, store.project, encodeTime(early), encodeTime(s.at)); err != nil {
			t.Fatal(err)
		}
	}

	sessions, err := store.ListSessions(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := make([]message.SessionID, len(sessions))
	for i, s := range sessions {
		got[i] = s.ID
	}
	want := []message.SessionID{"ses_late", "ses_tied_a", "ses_tied_b", "ses_early"}
	if !slices.Equal(got, want) {
		t.Errorf("newest-first order = %v, want %v", got, want)
	}
	if !sessions[0].UpdatedAt.Equal(late) {
		t.Errorf("round-tripped time = %v, want %v", sessions[0].UpdatedAt, late)
	}
}

// TestListSessions_ScopesToProject checks that projects sharing a database
// each list only the sessions they created, while any of them can still read
// a session by ID.
func TestListSessions_ScopesToProject(t *testing.T) {
	db := openTestDB(t)
	a := newProjectStore(t, db, "/work/a")
	b := newProjectStore(t, db, "/work/b")
	ctx := t.Context()

	appendAt(t, a, "ses_a", "", "msg_1")
	appendAt(t, a, "ses_a_2", "", "msg_1")
	appendAt(t, b, "ses_b", "", "msg_1")

	tests := map[string]struct {
		store *Store
		want  []message.SessionID
	}{
		"project a": {a, []message.SessionID{"ses_a", "ses_a_2"}},
		"project b": {b, []message.SessionID{"ses_b"}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			sessions, err := tt.store.ListSessions(ctx)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			var got []message.SessionID
			for _, s := range sessions {
				got = append(got, s.ID)
			}
			slices.Sort(got)
			if !slices.Equal(got, tt.want) {
				t.Errorf("sessions = %v, want %v", got, tt.want)
			}
		})
	}
	if _, err := b.Session(ctx, "ses_a"); err != nil {
		t.Errorf("reading another project's session by ID: %v", err)
	}
}

// TestFindSession_ScopesToProject checks that FindSession, unlike Session,
// finds only the stored sessions of the store's project.
func TestFindSession_ScopesToProject(t *testing.T) {
	db := openTestDB(t)
	a := newProjectStore(t, db, "/work/a")
	b := newProjectStore(t, db, "/work/b")
	ctx := t.Context()

	appendAt(t, a, "ses_a", "", "msg_1")

	tests := map[string]struct {
		store *Store
		id    message.SessionID
		found bool
	}{
		"own session":   {a, "ses_a", true},
		"other project": {b, "ses_a", false},
		"unknown":       {a, "ses_missing", false},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			sess, err := tt.store.FindSession(ctx, tt.id)
			switch {
			case tt.found && err != nil:
				t.Fatalf("find %q: %v", tt.id, err)
			case tt.found && sess.ID != tt.id:
				t.Errorf("found %q, want %q", sess.ID, tt.id)
			case !tt.found && !errors.Is(err, fs.ErrNotExist):
				t.Errorf("find %q: err = %v, want fs.ErrNotExist", tt.id, err)
			}
		})
	}
}

// headColumns returns the stored head_id and forward_id of a session.
func headColumns(t *testing.T, store *Store, id message.SessionID) (head, forward sql.Null[string]) {
	t.Helper()
	if err := store.db.QueryRowContext(t.Context(), `
		SELECT head_id, forward_id FROM sessions WHERE id = ?`, id).Scan(&head, &forward); err != nil {
		t.Fatal(err)
	}
	return head, forward
}

// TestSetHead_RoundTripsThroughStore checks the head columns end to end,
// since the write path names them too, and that none is stored as NULL.
func TestSetHead_RoundTripsThroughStore(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	const id = "ses_new"
	appendAt(t, store, id, "", "msg_1")
	appendAt(t, store, id, "msg_1", "msg_2")

	want := Head{ID: "msg_1", Forward: "msg_2"}
	if err := store.SetHead(ctx, id, want); err != nil {
		t.Fatalf("set head: %v", err)
	}
	sess, err := store.Session(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Head != want {
		t.Fatalf("stored head = %+v, want %+v", sess.Head, want)
	}

	if err := store.SetHead(ctx, id, Head{}); err != nil {
		t.Fatalf("clear head: %v", err)
	}
	if sess, err = store.Session(ctx, id); err != nil {
		t.Fatal(err)
	}
	if sess.Head != (Head{}) {
		t.Fatalf("head survived clearing: %+v", sess.Head)
	}
	if head, forward := headColumns(t, store, id); head.Valid || forward.Valid {
		t.Errorf("cleared head stored as %v, %v; want NULL for both", head, forward)
	}
}

// TestSetHead_RefusesMessagesOffTheConversation checks that the head and
// the forward marker rest only on live chat messages of the session, and
// that a refused move leaves both where they were.
func TestSetHead_RefusesMessagesOffTheConversation(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	const id = "ses_head"
	appendAt(t, store, id, "", "msg_1")
	appendAt(t, store, id, "msg_1", "msg_gone")
	appendAt(t, store, id, "msg_1", "msg_2")
	appendAt(t, store, "ses_other", "", "msg_elsewhere")
	if err := store.AppendFork(ctx, id, "msg_2", &message.Content{ID: "msg_fork", Role: message.RoleUser, Type: message.TypeTitle}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteMessage(ctx, id, "msg_gone"); err != nil {
		t.Fatal(err)
	}
	before, err := store.Session(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]struct {
		head Head
		want error
	}{
		"unknown head":        {Head{ID: "msg_missing"}, ErrMessageNotFound},
		"unknown forward":     {Head{ID: "msg_1", Forward: "msg_missing"}, ErrMessageNotFound},
		"another session's":   {Head{ID: "msg_elsewhere"}, ErrMessageNotFound},
		"deleted message":     {Head{ID: "msg_gone"}, ErrMessageNotFound},
		"fork message":        {Head{ID: "msg_fork"}, ErrMessageType},
		"forward onto a fork": {Head{ID: "msg_1", Forward: "msg_fork"}, ErrMessageType},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if err := store.SetHead(ctx, id, tt.head); !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
			if after, err := store.Session(ctx, id); err != nil || after.Head != before.Head {
				t.Errorf("head after a refused move = %+v, %v; want %+v", after.Head, err, before.Head)
			}
		})
	}
}

// TestStore_ReportsUnknownSessions checks the operations that name a session
// rather than create one, and that they leave no session behind: a session is
// stored with its first message and with nothing else.
func TestStore_ReportsUnknownSessions(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	checks := map[string]func() error{
		"set head":    func() error { return store.SetHead(ctx, "ses_missing", Head{}) },
		"set title":   func() error { return store.SetTitle(ctx, "ses_missing", "titled first") },
		"clear title": func() error { return store.SetTitle(ctx, "ses_missing", "") },
		"delete":      func() error { return store.DeleteSession(ctx, "ses_missing") },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if err := check(); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("err = %v, want fs.ErrNotExist", err)
			}
			if sessions, err := store.ListSessions(ctx); err != nil || len(sessions) > 0 {
				t.Errorf("sessions = %v, %v; want none", sessions, err)
			}
		})
	}
}

// TestAppendMessage_KeepsCreatedAt checks that a session is created with its
// first message, and that a later message moves its update time but not its
// creation time.
func TestAppendMessage_KeepsCreatedAt(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	before := time.Now().Truncate(time.Second)
	appendAt(t, store, "ses_a", "", "msg_1")
	sess, err := store.Session(ctx, "ses_a")
	if err != nil {
		t.Fatal(err)
	}
	if sess.CreatedAt.Before(before) || !sess.CreatedAt.Equal(sess.UpdatedAt) {
		t.Errorf("after the first message: created %v, updated %v; want both from %v on", sess.CreatedAt, sess.UpdatedAt, before)
	}

	// Times are whole seconds, so the session is made an hour older to tell
	// the two apart.
	old := before.Add(-time.Hour).UTC()
	if _, err := store.db.ExecContext(ctx, `
		UPDATE sessions SET created_at = ?, updated_at = ? WHERE id = 'ses_a'`,
		encodeTime(old), encodeTime(old)); err != nil {
		t.Fatal(err)
	}
	appendAt(t, store, "ses_a", "msg_1", "msg_2")
	if sess, err = store.Session(ctx, "ses_a"); err != nil {
		t.Fatal(err)
	}
	if !sess.CreatedAt.Equal(old) || !sess.UpdatedAt.After(old) {
		t.Errorf("after the second message: created %v, updated %v; want created %v and updated later", sess.CreatedAt, sess.UpdatedAt, old)
	}
}

// countRows returns the rows of table that belong to session id: its own
// row in sessions, its messages, and its messages' parts.
func countRows(t *testing.T, store *Store, table string, id message.SessionID) int {
	t.Helper()
	query := map[string]string{
		"sessions":      `SELECT COUNT(*) FROM sessions WHERE id = ?`,
		"messages":      `SELECT COUNT(*) FROM messages WHERE session_id = ?`,
		"message_parts": `SELECT COUNT(*) FROM message_parts WHERE message IN (SELECT seq FROM messages WHERE session_id = ?)`,
	}[table]
	var n int
	if err := store.db.QueryRowContext(t.Context(), query, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestDeleteSession_RemovesItsRows checks that deleting a session removes
// it with its messages and their parts, and no other session's, that every
// read and change then treats it as a session the store does not hold, and
// that appending under its ID stores a new session.
func TestDeleteSession_RemovesItsRows(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	for _, sess := range []message.SessionID{"ses_a", "ses_kept"} {
		for i, parent := range []message.ID{"", "msg_1", "msg_1"} {
			if err := store.SetHead(ctx, sess, Head{ID: parent}); err != nil && !errors.Is(err, fs.ErrNotExist) {
				t.Fatal(err)
			}
			msg := message.Content{ID: message.ID(fmt.Sprintf("msg_%d", i+1)), Role: message.RoleUser,
				Parts: []message.ContentPart{message.TextPart{Text: "hi"}}}
			if err := store.AppendMessage(ctx, sess, &msg); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Each read counts what it returns, which is something before the
	// session is deleted and nothing after.
	reads := map[string]func() (int, error){
		"list": func() (int, error) {
			s, err := store.ListSessions(ctx)
			return len(s) - 1, err // ses_kept stays listed
		},
		"branch":   func() (int, error) { m, err := store.Branch(ctx, "ses_a"); return len(m), err },
		"path":     func() (int, error) { m, err := store.Path(ctx, "ses_a", "msg_2"); return len(m), err },
		"messages": func() (int, error) { m, err := store.Messages(ctx, "ses_a"); return len(m), err },
		"children": func() (int, error) { m, err := store.Children(ctx, "ses_a", ""); return len(m), err },
		"forks":    func() (int, error) { f, err := store.Forks(ctx, "ses_a"); return len(f), err },
		"size":     func() (int, error) { return store.Size(ctx, "ses_a", "msg_1") },
		"leaf":     func() (int, error) { id, err := store.Leaf(ctx, "ses_a", ""); return len(id), err },
	}
	for name, read := range reads {
		if n, err := read(); n == 0 || err != nil {
			t.Fatalf("%s before deleting = %d, %v; want something", name, n, err)
		}
	}

	if err := store.DeleteSession(ctx, "ses_a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	for table, want := range map[string]int{"sessions": 1, "messages": 3, "message_parts": 3} {
		if n := countRows(t, store, table, "ses_a"); n != 0 {
			t.Errorf("%d %s rows of the deleted session, want none", n, table)
		}
		if n := countRows(t, store, table, "ses_kept"); n != want {
			t.Errorf("%d %s rows of the other session, want its %d", n, table, want)
		}
	}

	for name, read := range reads {
		t.Run(name, func(t *testing.T) {
			if n, err := read(); n != 0 || err != nil {
				t.Errorf("after deleting = %d, %v; want nothing", n, err)
			}
		})
	}
	unknown := map[string]struct {
		run  func() error
		want error
	}{
		"session":        {func() error { _, err := store.Session(ctx, "ses_a"); return err }, fs.ErrNotExist},
		"find":           {func() error { _, err := store.FindSession(ctx, "ses_a"); return err }, fs.ErrNotExist},
		"set title":      {func() error { return store.SetTitle(ctx, "ses_a", "back") }, fs.ErrNotExist},
		"set head":       {func() error { return store.SetHead(ctx, "ses_a", Head{ID: "msg_1"}) }, fs.ErrNotExist},
		"delete":         {func() error { return store.DeleteSession(ctx, "ses_a") }, fs.ErrNotExist},
		"message":        {func() error { _, err := store.Message(ctx, "ses_a", "msg_1"); return err }, ErrMessageNotFound},
		"leaf of":        {func() error { _, err := store.Leaf(ctx, "ses_a", "msg_1"); return err }, ErrMessageNotFound},
		"delete message": {func() error { return store.DeleteMessage(ctx, "ses_a", "msg_1") }, ErrMessageNotFound},
		"fork": {func() error {
			return store.AppendFork(ctx, "ses_a", "msg_1", &message.Content{ID: "msg_x", Role: message.RoleUser, Type: message.TypeTitle})
		}, ErrMessageNotFound},
	}
	for name, tt := range unknown {
		t.Run(name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}

	again := message.Content{ID: "msg_new", Role: message.RoleUser}
	if err := store.AppendMessage(ctx, "ses_a", &again); err != nil {
		t.Fatalf("append under the deleted ID: %v", err)
	}
	if branch, err := store.Branch(ctx, "ses_a"); err != nil || !equal(ids(branch), []message.ID{"msg_new"}) {
		t.Errorf("branch of the new session = %v, %v; want msg_new alone", ids(branch), err)
	}
}

// TestSetTitle_RoundTrips checks that a stored session takes a title and
// gives it up.
func TestSetTitle_RoundTrips(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	appendAt(t, store, "ses_a", "", "msg_1")
	for _, title := range []string{"first", "second", ""} {
		if err := store.SetTitle(ctx, "ses_a", title); err != nil {
			t.Fatalf("set title %q: %v", title, err)
		}
		sess, err := store.Session(ctx, "ses_a")
		if err != nil {
			t.Fatal(err)
		}
		if sess.Title != title {
			t.Errorf("title = %q, want %q", sess.Title, title)
		}
	}
}

// TestAppendMessage_StampsItsTimes checks that a stored message, of the
// conversation or a fork, is updated when it is created, since it does not
// change.
func TestAppendMessage_StampsItsTimes(t *testing.T) {
	ctx := t.Context()
	store := newTestStore(t)
	const id = "ses_times"
	appendAt(t, store, id, "", "msg_1")
	if err := store.AppendFork(ctx, id, "msg_1", &message.Content{ID: "msg_fork", Role: message.RoleUser, Type: message.TypeTitle}); err != nil {
		t.Fatal(err)
	}
	for _, msg := range []message.ID{"msg_1", "msg_fork"} {
		if created, updated := messageTimes(t, store, id, msg); updated != created {
			t.Errorf("%s: created %s, updated %s; want updated as created", msg, created, updated)
		}
	}
}
