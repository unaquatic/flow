package engine

// Migrations are the schema upgrades of the tables snapshots are stored in,
// in version order, which the snapshot extension applies with
// database.Migrate as it loads. An applied migration is never edited:
// databases have already recorded it, so a change to the tables is always a
// new entry.
var Migrations = []string{
	migrationV1,
}

// migrationV1 creates the tables.
//
// objects holds Git blobs and trees keyed by their object name, with data
// compressed by DEFLATE and size giving the uncompressed length. It keeps its
// rowid: WITHOUT ROWID tables suit rows much smaller than a page, and file
// contents often are not. Objects belong to no project, so projects share the
// blobs and trees they have in common.
//
// entries holds each project's file index. It records, for each worktree
// file, the hash of its content and the size, modification time, inode change
// time, and inode number the file had when read, so an unchanged file is not
// read again. checked is when the file was read. These times are Unix
// nanoseconds; ctime and inode are 0 where the platform does not report them.
const migrationV1 = `
CREATE TABLE objects (
	hash BLOB    PRIMARY KEY CHECK (length(hash) = 20),
	type INTEGER NOT NULL CHECK (type IN (1, 2)),
	size INTEGER NOT NULL CHECK (size >= 0),
	data BLOB    NOT NULL
);

CREATE TABLE entries (
	project INTEGER NOT NULL REFERENCES projects (id),
	path    TEXT    NOT NULL,
	mode    INTEGER NOT NULL,
	hash    BLOB    NOT NULL CHECK (length(hash) = 20),
	size    INTEGER NOT NULL,
	mtime   INTEGER NOT NULL,
	ctime   INTEGER NOT NULL,
	inode   INTEGER NOT NULL,
	checked INTEGER NOT NULL,
	PRIMARY KEY (project, path)
) WITHOUT ROWID;
`
