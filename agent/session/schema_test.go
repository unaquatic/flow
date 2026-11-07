package session

import (
	"database/sql"
	"slices"
	"testing"

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
		"sessions":      {"id", "project", "title", "created_at", "updated_at", "deleted_at", "head_id", "forward_id"},
		"messages":      {"seq", "session_id", "id", "parent_id", "type", "role", "model", "created_at", "updated_at", "deleted_at"},
		"message_parts": {"seq", "message", "id", "data"},
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
