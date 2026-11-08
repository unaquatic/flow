package session

import (
	"database/sql"
	"errors"
	"slices"
	"testing"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/database"
)

// columns returns the column names of table.
func columns(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return names
}

// TestOpen_CreatesTheTables checks that Open creates the session tables in a
// database that holds none, records each of Migrations once under Name,
// however often it opens the store, and keeps what the tables store.
func TestOpen_CreatesTheTables(t *testing.T) {
	db := openTestDB(t)
	project, err := database.Project(t.Context(), db, "/work/project")
	if err != nil {
		t.Fatal(err)
	}
	if got := columns(t, db, "sessions"); len(got) != 0 {
		t.Fatalf("database.Open created sessions with columns %v, want it left to Open", got)
	}

	store, err := Open(t.Context(), db, project)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	tests := map[string][]string{
		"sessions":      {"id", "project", "title", "created_at", "updated_at", "head_id", "forward_id"},
		"messages":      {"seq", "session_id", "id", "parent_id", "type", "role", "model", "created_at", "updated_at", "provider"},
		"message_parts": {"seq", "message", "id", "type", "data"},
	}
	for table, want := range tests {
		if got := columns(t, db, table); !slices.Equal(got, want) {
			t.Errorf("%s columns = %v, want %v", table, got, want)
		}
	}

	appendAt(t, store, "ses_kept", "", "msg_1")
	if store, err = Open(t.Context(), db, project); err != nil {
		t.Fatalf("open again: %v", err)
	}
	var versions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM migrations WHERE id = ?`, Name).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if versions != len(Migrations) {
		t.Errorf("recorded %d migrations under %q, want %d", versions, Name, len(Migrations))
	}
	sessions, err := store.ListSessions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != "ses_kept" {
		t.Errorf("sessions after opening again = %+v, want the one stored before", sessions)
	}
}

// insertMessageSQL inserts a message row, taking its session, ID, parent,
// type and role.
const insertMessageSQL = `
	INSERT INTO messages (session_id, id, parent_id, type, role, model, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, '', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`

// insertPartSQL inserts a part row, taking its message's seq, its ID, its
// type and its data.
const insertPartSQL = `INSERT INTO message_parts (message, id, type, data) VALUES (?, ?, ?, ?)`

// TestSchema_RefusesBadRows checks that the tables refuse a row that links
// to what does not exist, a role that names no message.Role, a message or
// part type that is not lowercase letters, digits and underscores, a part
// without an ID, and part data that is not a JSON object, however it is
// written.
func TestSchema_RefusesBadRows(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	appendAt(t, store, "ses_a", "", "msg_a")
	appendAt(t, store, "ses_b", "", "msg_b")

	tests := map[string]struct {
		query string
		args  []any
	}{
		"message of an unknown session": {insertMessageSQL, []any{"ses_missing", "msg_x", nil, "chat", "user"}},
		"parent that does not exist":    {insertMessageSQL, []any{"ses_a", "msg_x", "msg_missing", "chat", "user"}},
		"parent of another session":     {insertMessageSQL, []any{"ses_a", "msg_x", "msg_b", "chat", "user"}},
		"unknown role":                  {insertMessageSQL, []any{"ses_a", "msg_x", "msg_a", "chat", "assistant"}},
		"empty type":                    {insertMessageSQL, []any{"ses_a", "msg_x", "msg_a", "", "user"}},
		"type in capitals":              {insertMessageSQL, []any{"ses_a", "msg_x", "msg_a", "Title", "user"}},
		"type with a dash":              {insertMessageSQL, []any{"ses_a", "msg_x", "msg_a", "title-x", "user"}},
		"type led by a digit":           {insertMessageSQL, []any{"ses_a", "msg_x", "msg_a", "2title", "user"}},
		"head that does not exist":      {`UPDATE sessions SET head_id = 'msg_missing' WHERE id = 'ses_a'`, nil},
		"head of another session":       {`UPDATE sessions SET head_id = 'msg_b' WHERE id = 'ses_a'`, nil},
		"forward of another session":    {`UPDATE sessions SET forward_id = 'msg_b' WHERE id = 'ses_a'`, nil},
		"session left without its head": {`DELETE FROM messages WHERE id = 'msg_a'`, nil},
		"part of an unknown message":    {insertPartSQL, []any{999, "prt_x", "text", `{}`}},
		"part without an ID":            {insertPartSQL, []any{1, "", "text", `{}`}},
		"part without a type":           {insertPartSQL, []any{1, "prt_x", "", `{}`}},
		"part type in capitals":         {insertPartSQL, []any{1, "prt_x", "Text", `{}`}},
		"part data that is an array":    {insertPartSQL, []any{1, "prt_x", "text", `[]`}},
		"part data that is a string":    {insertPartSQL, []any{1, "prt_x", "text", `"hi"`}},
		"part data that is not JSON":    {insertPartSQL, []any{1, "prt_x", "text", `{"text":`}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := store.db.ExecContext(ctx, tt.query, tt.args...); err == nil {
				t.Error("the table took the row")
			}
		})
	}
	if _, err := store.db.ExecContext(ctx, insertMessageSQL, "ses_a", "msg_ok", "msg_a", "title_v2", "agent"); err != nil {
		t.Errorf("a valid fork row: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, insertPartSQL, 1, "prt_ok", "test_tree", `{}`); err != nil {
		t.Errorf("a valid part row: %v", err)
	}
	if rows, err := store.db.QueryContext(ctx, `PRAGMA foreign_key_check`); err != nil {
		t.Fatal(err)
	} else {
		defer rows.Close()
		if rows.Next() {
			t.Error("foreign_key_check reports a violation")
		}
	}
}

// TestStore_FailsOnUnknownRole checks that a message whose role names no
// message.Role is refused as it is appended, and fails the reads that meet
// it, rather than reading back as an agent message.
func TestStore_FailsOnUnknownRole(t *testing.T) {
	store := newTestStore(t)
	ctx := t.Context()
	appendAt(t, store, "ses_a", "", "msg_1")

	bad := message.Content{ID: "msg_bad", Role: message.Role(99)}
	if err := store.AppendMessage(ctx, "ses_a", &bad); !errors.Is(err, message.ErrUnknownRole) {
		t.Errorf("append with an unknown role: err = %v, want ErrUnknownRole", err)
	}
	if err := store.AppendFork(ctx, "ses_a", "msg_1", &message.Content{ID: "msg_fork", Role: message.RoleUser, Type: "Not A Type"}); err == nil {
		t.Error("stored a fork of a type that is not snake_case")
	}

	// A row that slipped past the table's check, as one an older build
	// wrote, must fail the read.
	if _, err := store.db.ExecContext(ctx, `PRAGMA ignore_check_constraints = ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, insertMessageSQL, "ses_a", "msg_old", "msg_1", "chat", "assistant"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `PRAGMA ignore_check_constraints = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Message(ctx, "ses_a", "msg_old"); !errors.Is(err, message.ErrUnknownRole) {
		t.Errorf("read of a stored unknown role: err = %v, want ErrUnknownRole", err)
	}
	if _, err := store.Messages(ctx, "ses_a"); !errors.Is(err, message.ErrUnknownRole) {
		t.Errorf("messages holding a stored unknown role: err = %v, want ErrUnknownRole", err)
	}
}

// TestMigrationV2_MovesTheProvider checks that migrationV2 moves the
// provider that the older form names in front of a message's model to the
// provider column, gives a model the older form wrote without its lab the
// provider as its lab back, and leaves a model that names no provider, or
// none, alone.
func TestMigrationV2_MovesTheProvider(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	project, err := database.Project(ctx, db, "/work/project")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx, db, Name, Migrations[:1]); err != nil {
		t.Fatalf("migrate to v1: %v", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	models := map[message.ID]string{
		"msg_lab":     "provider-a/model-a:fast:high",
		"msg_gateway": "provider-b/lab-a/model-b",
		"msg_bare":    "model-x",
		"msg_none":    "",
	}
	parent := any(nil)
	for _, id := range []message.ID{"msg_lab", "msg_gateway", "msg_bare", "msg_none"} {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO messages (session_id, id, parent_id, type, role, model, created_at, updated_at)
			VALUES ('ses_old', ?, ?, 'chat', 'agent', ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
			string(id), parent, models[id]); err != nil {
			t.Fatal(err)
		}
		parent = string(id)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO sessions (id, project, created_at, updated_at, head_id)
		VALUES ('ses_old', ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 'msg_none')`, project); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	store, err := Open(ctx, db, project)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	msgs, err := store.Messages(ctx, "ses_old")
	if err != nil {
		t.Fatal(err)
	}
	want := map[message.ID][2]string{
		"msg_lab":     {"provider-a", "provider-a/model-a:fast:high"},
		"msg_gateway": {"provider-b", "lab-a/model-b"},
		"msg_bare":    {"", "model-x"},
		"msg_none":    {"", ""},
	}
	if len(msgs) != len(want) {
		t.Fatalf("read %d messages, want %d", len(msgs), len(want))
	}
	for _, m := range msgs {
		if got := [2]string{string(m.Provider), m.Model}; got != want[m.ID] {
			t.Errorf("message %s = provider %q, model %q; want %q", m.ID, got[0], got[1], want[m.ID])
		}
	}
}
