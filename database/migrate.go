package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Name is the ID the migrations table records this package's own
// migrations under, as it records those of package agent/session under
// "session" and those of an extension under the extension's name.
const Name = "database"

// ErrInvalidMigrationID reports a migration ID that is not snake_case.
var ErrInvalidMigrationID = errors.New("migration id is not snake_case")

// ErrNewerSchema reports tables that a newer build of the program has
// upgraded past the migrations this build knows, which it cannot safely read
// or write.
var ErrNewerSchema = errors.New("database schema is newer than this program")

// migrationID matches a snake_case ID: words of lowercase ASCII letters and
// digits, joined by single underscores, the first starting with a letter.
var migrationID = regexp.MustCompile(`^[a-z][a-z0-9]*(_[a-z0-9]+)*$`)

// migrations are the schema upgrades of the package's own tables, in version
// order. An applied migration is never edited: databases have already
// recorded it, so a change to the tables is always a new entry. The other
// tables belong to package agent/session and to the extensions, which create
// and upgrade them with Migrate.
var migrations = []string{
	migrationV1,
}

// migrationV1 creates projects, which holds one row per worktree. The
// sessions and file indexes of other packages belong to a project, so their
// tables reference it.
const migrationV1 = `
CREATE TABLE projects (
	id       INTEGER PRIMARY KEY,
	worktree TEXT    NOT NULL UNIQUE
);
`

// migrationsTable records each migration applied, by the ID of its owner
// and its version, starting at 1. It is created before any migration runs,
// since every migration is recorded in it.
const migrationsTable = `
CREATE TABLE IF NOT EXISTS migrations (
	id         TEXT    NOT NULL,
	version    INTEGER NOT NULL,
	applied_at TEXT    NOT NULL,
	PRIMARY KEY (id, version)
) WITHOUT ROWID;
`

// migrate creates the migrations table and applies the package's own pending
// migrations. Two processes racing to initialize WAL mode can receive
// SQLITE_BUSY; reopening retries migration.
func migrate(db *sql.DB) error {
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, migrationsTable); err != nil {
		return err
	}
	return Migrate(ctx, db, Name, migrations)
}

// Migrate applies the pending schema upgrades of the tables owned by id to
// db, which Open has created: steps holds them in version order, and an
// applied one is never edited. The owner is this package, package
// agent/session, or an extension, under the extension's name; id is
// snake_case, or Migrate fails with [ErrInvalidMigrationID]. Tables that db
// records at a version past the last of steps, as a newer build leaves them,
// fail with [ErrNewerSchema] before any step runs. Each step is applied and
// recorded under id in one transaction, so a process that races another to
// apply one applies it once.
func Migrate(ctx context.Context, db *sql.DB, id string, steps []string) error {
	if !migrationID.MatchString(id) {
		return fmt.Errorf("migrate %q: %w", id, ErrInvalidMigrationID)
	}
	// Recorded versions only grow, so one read before the steps is enough.
	var current int
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM migrations WHERE id = ?`, id,
	).Scan(&current); err != nil {
		return fmt.Errorf("migrate %s: %w", id, err)
	}
	if current > len(steps) {
		return fmt.Errorf("migrate %s: at v%d, this program knows up to v%d: %w",
			id, current, len(steps), ErrNewerSchema)
	}
	for v, step := range steps {
		if err := applyMigration(ctx, db, id, v, step); err != nil {
			return fmt.Errorf("migrate %s to v%d: %w", id, v+1, err)
		}
	}
	return nil
}

// applyMigration applies migration, version v+1 of the tables owned by id,
// and records it, unless it is recorded already. It reads the version in the
// transaction, which holds the write lock from its start.
func applyMigration(ctx context.Context, db *sql.DB, id string, v int, migration string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var current int
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version), 0) FROM migrations WHERE id = ?`, id,
	).Scan(&current); err != nil {
		return err
	}
	if current > v {
		return nil
	}
	if _, err := tx.ExecContext(ctx, migration); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO migrations (id, version, applied_at) VALUES (?, ?, ?)`,
		id, v+1, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}
