// Package database opens the database, a SQLite file holding the
// sessions and workspace snapshots of every project.
//
// Its own table is projects. Every other table belongs to the package that
// stores in it: package agent/session, or an extension, such as the snapshot
// extension. Each creates and upgrades its tables with [Migrate], under an ID
// of its own, and the migrations table records what every owner applied.
//
// The package does not register the SQLite driver itself. Packages that only
// use its types, such as [ProjectID], then do not link SQLite. A program that
// calls [Open] registers the driver by importing it:
//
//	import _ "github.com/unaquatic/sqlite"
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// FileName is the name of the database file in the data directory.
const FileName = "state.db"

// ProjectID identifies a project: one worktree whose sessions and snapshots
// the database holds.
type ProjectID int64

// Open opens the database at path, creating it and its directory if needed,
// and applies the pending migrations of the package's own tables, which
// leaves the tables of other owners to their Migrate calls. It fails unless
// the program has registered the driver of github.com/unaquatic/sqlite.
//
// The returned DB has a single connection, so its transactions run one at a
// time within the process. Other processes sharing the file wait up to 30
// seconds for its write lock.
//
// A new database gets 16 KiB pages, which store the large blobs of snapshots
// in fewer pages than the default 4 KiB. An existing database keeps its page
// size.
func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	// The driver runs the _pragma values sorted by name and _journal_mode
	// after them, so page_size applies before WAL mode fixes the page size.
	db, err := sql.Open("sqlite", "file:"+path+
		"?_pragma=busy_timeout(30000)&_pragma=page_size(16384)&_pragma=synchronous(NORMAL)"+
		"&_pragma=foreign_keys(1)&_journal_mode=WAL&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	// Serialize this process's transactions to avoid competing SQLite writers.
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		return nil, errors.Join(fmt.Errorf("init database: %w", err), db.Close())
	}
	return db, nil
}

// Project returns the ID of the project rooted at worktree, adding the
// project if the database holds none. A relative worktree is resolved against
// the working directory.
func Project(ctx context.Context, db *sql.DB, worktree string) (ProjectID, error) {
	abs, err := filepath.Abs(worktree)
	if err != nil {
		return 0, fmt.Errorf("database: project %s: %w", worktree, err)
	}
	// The no-op update makes RETURNING yield the row that already exists.
	var id ProjectID
	err = db.QueryRowContext(ctx, `
		INSERT INTO projects (worktree) VALUES (?)
		ON CONFLICT (worktree) DO UPDATE SET worktree = excluded.worktree
		RETURNING id`, abs).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("database: project %s: %w", abs, err)
	}
	return id, nil
}
