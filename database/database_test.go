package database

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	_ "github.com/unaquatic/sqlite" // registers the driver that Open uses
)

// openTest opens a database in a temporary directory.
func openTest(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), FileName))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// texts returns the single text column of each row query selects.
func texts(t *testing.T, db *sql.DB, query string, args ...any) []string {
	t.Helper()
	rows, err := db.Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// columns returns the column names of table.
func columns(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	return texts(t, db, `SELECT name FROM pragma_table_info(?)`, table)
}

// versions returns the versions recorded for the tables owned by id, in
// order.
func versions(t *testing.T, db *sql.DB, id string) []int {
	t.Helper()
	rows, err := db.Query(`SELECT version FROM migrations WHERE id = ? ORDER BY version`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// allVersions returns the versions 1 to n.
func allVersions(n int) []int {
	out := make([]int, n)
	for i := range n {
		out[i] = i + 1
	}
	return out
}

// TestOpen_CreatesOnlyItsOwnTables checks that Open creates the migrations
// table and the package's own tables, and leaves those of every other owner
// to its Migrate call.
func TestOpen_CreatesOnlyItsOwnTables(t *testing.T) {
	db := openTest(t)

	if got := texts(t, db, `SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`); !slices.Equal(got, []string{"migrations", "projects"}) {
		t.Errorf("tables = %v, want migrations and projects only", got)
	}
	tests := map[string][]string{
		"projects":   {"id", "worktree"},
		"migrations": {"id", "version", "applied_at"},
	}
	for table, want := range tests {
		t.Run(table, func(t *testing.T) {
			if got := columns(t, db, table); !slices.Equal(got, want) {
				t.Errorf("%s columns = %v, want %v", table, got, want)
			}
		})
	}
	if got, want := versions(t, db, Name), allVersions(len(migrations)); !slices.Equal(got, want) {
		t.Errorf("recorded versions %v under %q, want %v", got, Name, want)
	}
}

func TestMigrate_IsIdempotent(t *testing.T) {
	db := openTest(t)
	for range 3 {
		if err := migrate(db); err != nil {
			t.Fatalf("migrate: %v", err)
		}
	}
	if got, want := versions(t, db, Name), allVersions(len(migrations)); !slices.Equal(got, want) {
		t.Errorf("recorded versions %v, want %v", got, want)
	}
}

// TestMigrate_PerOwner checks that each owner upgrades its own tables once
// per version, apart from the others, and later picks up only the steps it
// has not applied.
func TestMigrate_PerOwner(t *testing.T) {
	db := openTest(t)
	one := []string{`CREATE TABLE one_a (x INTEGER)`}
	for range 2 {
		if err := Migrate(t.Context(), db, "one", one); err != nil {
			t.Fatalf("migrate one: %v", err)
		}
	}
	if err := Migrate(t.Context(), db, "two", []string{`CREATE TABLE two_a (y INTEGER)`}); err != nil {
		t.Fatalf("migrate two: %v", err)
	}
	one = append(one, `ALTER TABLE one_a ADD COLUMN z INTEGER`)
	if err := Migrate(t.Context(), db, "one", one); err != nil {
		t.Fatalf("migrate one to v2: %v", err)
	}

	if got := versions(t, db, "one"); !slices.Equal(got, []int{1, 2}) {
		t.Errorf("one recorded versions %v, want [1 2]", got)
	}
	if got := versions(t, db, "two"); !slices.Equal(got, []int{1}) {
		t.Errorf("two recorded versions %v, want [1]", got)
	}
	if got := columns(t, db, "one_a"); !slices.Equal(got, []string{"x", "z"}) {
		t.Errorf("one_a columns = %v, want x and then z", got)
	}
	if err := Migrate(t.Context(), db, "bad", []string{`NOT SQL`}); err == nil {
		t.Error("a step that fails migrated without an error")
	}
	if got := versions(t, db, "bad"); len(got) != 0 {
		t.Errorf("a failed step was recorded as %v", got)
	}
}

// TestMigrate_TakesSnakeCaseIDs checks that Migrate applies the steps of an
// owner whose ID is snake_case, and refuses any other ID before it applies
// or records anything.
func TestMigrate_TakesSnakeCaseIDs(t *testing.T) {
	db := openTest(t)
	for i, id := range []string{"session", "snapshot", "my_extension", "v2_data", "a1"} {
		if err := Migrate(t.Context(), db, id, []string{fmt.Sprintf(`CREATE TABLE ok_%d (x INTEGER)`, i)}); err != nil {
			t.Errorf("migrate %q: %v", id, err)
		}
	}
	for _, id := range []string{"", "Session", "my-extension", "my extension", "_session", "session_", "my__extension", "1session", "séance"} {
		t.Run(id, func(t *testing.T) {
			err := Migrate(t.Context(), db, id, []string{`CREATE TABLE refused (x INTEGER)`})
			if !errors.Is(err, ErrInvalidMigrationID) {
				t.Errorf("migrate %q: err = %v, want ErrInvalidMigrationID", id, err)
			}
			if got := versions(t, db, id); len(got) != 0 {
				t.Errorf("migrate %q recorded versions %v", id, got)
			}
		})
	}
	if got := columns(t, db, "refused"); len(got) != 0 {
		t.Errorf("a refused ID created a table with columns %v", got)
	}
}

// TestOpen_ReopensWithoutReapplying checks that a database survives being
// closed and opened again, which is the path every run after the first takes.
func TestOpen_ReopensWithoutReapplying(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	first, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	id, err := Project(t.Context(), first, "/work/a")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { second.Close() })
	if got, want := versions(t, second, Name), allVersions(len(migrations)); !slices.Equal(got, want) {
		t.Errorf("recorded versions %v after reopening, want %v", got, want)
	}
	if again, err := Project(t.Context(), second, "/work/a"); err != nil || again != id {
		t.Errorf("project after reopening = %d, %v; want %d", again, err, id)
	}
}

func TestProject_ReturnsOneIDPerWorktree(t *testing.T) {
	db := openTest(t)
	ctx := t.Context()
	project := func(dir string) ProjectID {
		t.Helper()
		id, err := Project(ctx, db, dir)
		if err != nil {
			t.Fatalf("project %s: %v", dir, err)
		}
		return id
	}

	a := project("/work/a")
	if again := project("/work/a"); again != a {
		t.Errorf("same worktree got IDs %d and %d", a, again)
	}
	if b := project("/work/b"); b == a {
		t.Errorf("different worktrees share ID %d", a)
	}
	if cleaned := project("/work/x/../a/"); cleaned != a {
		t.Errorf("unclean path got ID %d, want %d", cleaned, a)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if rel, abs := project("."), project(wd); rel != abs {
		t.Errorf("relative path got ID %d, absolute path %d", rel, abs)
	}
}

// TestOpen_EnforcesForeignKeys checks that the tables of other owners, which
// reference projects, cannot hold rows of a project that does not exist.
func TestOpen_EnforcesForeignKeys(t *testing.T) {
	db := openTest(t)
	if err := Migrate(t.Context(), db, "owner", []string{
		`CREATE TABLE owned (project INTEGER NOT NULL REFERENCES projects (id))`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO owned (project) VALUES (42)`); err == nil {
		t.Error("stored a row for a project that does not exist")
	}
}
