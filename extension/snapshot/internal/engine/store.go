package engine

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"time"

	"github.com/unaquatic/flow/database"
)

// Store holds snapshot objects and one project's file index in the
// database. Every project shares the objects; the index is the project's own.
//
// Objects never change once stored, so the Store reads them without a
// transaction. Writes, and reads that must see them, go through a [Tx].
type Store struct {
	Objects

	db      *sql.DB
	project database.ProjectID
}

// NewStore returns the store of project in db, which must have been opened
// by [database.Open]. Closing db is up to the caller. The store compresses
// objects with [Deflate].
func NewStore(db *sql.DB, project database.ProjectID) *Store {
	return &Store{q: db, codec: Deflate{}, db: db, project: project}
}

// Objects reads the objects of a Store, either directly or within a [Tx],
// and compares the trees among them.
type Objects struct {
	q     Querier
	codec Codec
}

// NewObjects returns the objects read through q and decompressed with codec.
func NewObjects(q Querier, codec Codec) Objects {
	return Objects{q: q, codec: codec}
}

// Begin starts a transaction. A database opened by [database.Open] has one
// connection, which the transaction holds until it ends, so every other use
// of the database waits for it.
func (s *Store) Begin(ctx context.Context) (*Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("snapshot: begin: %w", err)
	}
	t := &Tx{q: tx, codec: s.codec, tx: tx, project: s.project}
	if err := t.prepare(ctx); err != nil {
		return nil, errors.Join(fmt.Errorf("snapshot: begin: %w", err), tx.Rollback())
	}
	return t, nil
}

// Tx is a transaction on a Store. It must end with Commit or Rollback. Its
// Objects see the objects the transaction stored.
type Tx struct {
	Objects

	tx      *sql.Tx
	project database.ProjectID

	// hasObject, putObject, and setEntry are the statements a snapshot runs
	// for every file, prepared once for the transaction, which closes them.
	hasObject, putObject, setEntry *sql.Stmt
}

// prepare prepares the statements a snapshot runs for every file.
func (t *Tx) prepare(ctx context.Context) error {
	var err error
	if t.hasObject, err = t.tx.PrepareContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM objects WHERE hash = ?)`); err != nil {
		return err
	}
	if t.putObject, err = t.tx.PrepareContext(ctx, `
		INSERT INTO objects (hash, type, size, data) VALUES (?, ?, ?, ?)
		ON CONFLICT (hash) DO NOTHING`); err != nil {
		return err
	}
	t.setEntry, err = t.tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO entries (project, path, mode, hash, size, mtime, ctime, inode, checked)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	return err
}

// Commit commits the transaction.
func (t *Tx) Commit() error {
	return t.tx.Commit()
}

// Rollback aborts the transaction. It returns [sql.ErrTxDone] after Commit.
func (t *Tx) Rollback() error {
	return t.tx.Rollback()
}

// PutBlob stores data as a blob and returns its hash. Storing a blob that
// exists already is a no-op.
func (t *Tx) PutBlob(ctx context.Context, data []byte) (Hash, error) {
	return t.put(ctx, BlobType, data)
}

// PutTree stores a tree holding entries, which may be in any order, and
// returns its hash. The objects the entries name need not be stored yet.
func (t *Tx) PutTree(ctx context.Context, entries []TreeEntry) (Hash, error) {
	data, err := EncodeTree(entries)
	if err != nil {
		return Hash{}, err
	}
	return t.put(ctx, TreeType, data)
}

// Has reports whether the store holds the object h, including an object the
// transaction stored.
func (t *Tx) Has(ctx context.Context, h Hash) (bool, error) {
	var ok bool
	err := t.hasObject.QueryRowContext(ctx, h).Scan(&ok)
	return ok, err
}

// Blob returns the content of the blob h. The error wraps [fs.ErrNotExist]
// when the store holds no object h.
func (o Objects) Blob(ctx context.Context, h Hash) ([]byte, error) {
	return o.Object(ctx, h, BlobType)
}

// Tree returns the entries of the tree h in Git order. The error wraps
// [fs.ErrNotExist] when the store holds no object h.
func (o Objects) Tree(ctx context.Context, h Hash) ([]TreeEntry, error) {
	data, err := o.Object(ctx, h, TreeType)
	if err != nil {
		return nil, err
	}
	return DecodeTree(data)
}

// IndexEntry is the recorded state of one worktree file.
type IndexEntry struct {
	// Path is the file's slash-separated path relative to the worktree
	// root.
	Path string

	// Mode is ModeFile, ModeExec, or ModeSymlink.
	Mode Mode

	// Hash names the blob holding the file's content.
	Hash Hash

	// Size, ModTime, ChangeTime, and Inode describe the file when its
	// content was read: its size, modification time, inode change time, and
	// inode number. ChangeTime and Inode are zero where the platform does not
	// report them.
	Size       int64
	ModTime    time.Time
	ChangeTime time.Time
	Inode      uint64

	// Checked is when the content was read. A change made within the file
	// system's timestamp granularity of Checked may leave the file's times
	// as they were.
	Checked time.Time
}

// Entries returns the project's file index in path order.
func (t *Tx) Entries(ctx context.Context) iter.Seq2[IndexEntry, error] {
	return func(yield func(IndexEntry, error) bool) {
		rows, err := t.tx.QueryContext(ctx, `
			SELECT path, mode, hash, size, mtime, ctime, inode, checked FROM entries
			WHERE project = ? ORDER BY path`, t.project)
		if err != nil {
			yield(IndexEntry{}, err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			e, err := scanEntry(rows)
			if !yield(e, err) || err != nil {
				return
			}
		}
		if err := rows.Err(); err != nil {
			yield(IndexEntry{}, err)
		}
	}
}

// SetEntry records e in the file index, replacing any entry for e.Path.
func (t *Tx) SetEntry(ctx context.Context, e IndexEntry) error {
	if err := checkIndexEntry(e); err != nil {
		return err
	}
	_, err := t.setEntry.ExecContext(ctx,
		t.project, e.Path, int64(e.Mode), e.Hash, e.Size, e.ModTime.UnixNano(),
		unixNano(e.ChangeTime), int64(e.Inode), e.Checked.UnixNano())
	return err
}

// DeleteEntry removes path from the file index. Removing a path the index
// does not hold is a no-op.
func (t *Tx) DeleteEntry(ctx context.Context, path string) error {
	_, err := t.tx.ExecContext(ctx, `DELETE FROM entries WHERE project = ? AND path = ?`, t.project, path)
	return err
}

// put stores data as an object of type typ and returns its hash.
func (t *Tx) put(ctx context.Context, typ ObjectType, data []byte) (Hash, error) {
	h := HashObject(typ, data)
	exists, err := t.Has(ctx, h)
	if err != nil {
		return Hash{}, fmt.Errorf("snapshot: look up %s %s: %w", typ, h, err)
	}
	if exists {
		return h, nil
	}
	var z bytes.Buffer
	if err := t.codec.Compress(&z, data); err != nil {
		return Hash{}, fmt.Errorf("snapshot: compress %s %s: %w", typ, h, err)
	}
	return h, t.insert(ctx, h, typ, len(data), z.Bytes())
}

// insert stores z, the content of size bytes of the object h of type typ
// compressed by t's codec. Storing an object that exists already is a no-op.
func (t *Tx) insert(ctx context.Context, h Hash, typ ObjectType, size int, z []byte) error {
	_, err := t.putObject.ExecContext(ctx, h, int64(typ), size, z)
	if err != nil {
		return fmt.Errorf("snapshot: store %s %s: %w", typ, h, err)
	}
	return nil
}

// Querier runs the queries that read objects. [sql.DB] and [sql.Tx]
// implement it.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Object returns the content of the object h, which must be of type want. The
// error wraps [fs.ErrNotExist] when the store holds no object h.
func (o Objects) Object(ctx context.Context, h Hash, want ObjectType) ([]byte, error) {
	var (
		typ  ObjectType
		size int64
		z    []byte
	)
	err := o.q.QueryRowContext(ctx, `SELECT type, size, data FROM objects WHERE hash = ?`, h).
		Scan(&typ, &size, &z)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("snapshot: %s %s: %w", want, h, fs.ErrNotExist)
	}
	if err != nil {
		return nil, err
	}
	if typ != want {
		return nil, fmt.Errorf("snapshot: object %s is a %s, not a %s", h, typ, want)
	}
	data, err := o.codec.Decompress(z, size)
	if err != nil {
		return nil, fmt.Errorf("snapshot: read %s %s: %w", typ, h, err)
	}
	return data, nil
}

// scanEntry reads one row of the entries table.
func scanEntry(rows *sql.Rows) (IndexEntry, error) {
	var (
		e                     IndexEntry
		mode, inode           int64
		mtime, ctime, checked int64
	)
	if err := rows.Scan(&e.Path, &mode, &e.Hash, &e.Size, &mtime, &ctime, &inode, &checked); err != nil {
		return IndexEntry{}, err
	}
	e.Mode = Mode(mode)
	e.ModTime = time.Unix(0, mtime)
	e.ChangeTime = fromUnixNano(ctime)
	e.Inode = uint64(inode)
	e.Checked = time.Unix(0, checked)
	return e, checkIndexEntry(e)
}

// unixNano returns t in Unix nanoseconds, and 0 for the zero Time, which
// stands for a time the platform does not report.
func unixNano(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixNano()
}

// fromUnixNano returns the Time of Unix nanoseconds ns, reversing unixNano.
func fromUnixNano(ns int64) time.Time {
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}

// checkIndexEntry reports whether e names a file by a valid path.
func checkIndexEntry(e IndexEntry) error {
	if !fs.ValidPath(e.Path) || e.Path == "." {
		return fmt.Errorf("snapshot: invalid index path %q", e.Path)
	}
	switch e.Mode {
	case ModeFile, ModeExec, ModeSymlink:
		return nil
	}
	return fmt.Errorf("snapshot: index entry %q has invalid mode %o", e.Path, e.Mode)
}
