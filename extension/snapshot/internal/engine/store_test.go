package engine

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"math/rand/v2"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/unaquatic/flow/database"
	_ "github.com/unaquatic/sqlite" // registers the driver that database.Open uses
)

// openTestDB opens a database in a temporary directory.
func openTestDB(t testing.TB) *sql.DB {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), database.FileName))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// projectStore returns the store in db of the project whose worktree is
// worktree, with the tables of Migrations applied, under the snapshot
// extension's name, as the extension applies them as it loads.
func projectStore(t testing.TB, db *sql.DB, worktree string) *Store {
	t.Helper()
	if err := database.Migrate(t.Context(), db, "snapshot", Migrations); err != nil {
		t.Fatal(err)
	}
	project, err := database.Project(t.Context(), db, worktree)
	if err != nil {
		t.Fatal(err)
	}
	return NewStore(db, project)
}

// openTestStore opens a store in a temporary database.
func openTestStore(t testing.TB) *Store {
	t.Helper()
	return projectStore(t, openTestDB(t), "/work/project")
}

// inTx runs fn in a transaction and commits it.
func inTx(t testing.TB, s *Store, fn func(ctx context.Context, tx *Tx)) {
	t.Helper()
	ctx := t.Context()
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	fn(ctx, tx)
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// randomBytes returns n reproducible bytes that do not compress.
func randomBytes(n int) []byte {
	rng := rand.New(rand.NewChaCha8([32]byte{1}))
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(rng.Uint32())
	}
	return b
}

func TestStore_BlobRoundTrips(t *testing.T) {
	tests := map[string][]byte{
		"empty":        {},
		"text":         []byte("hello\n"),
		"compressible": bytes.Repeat([]byte("func main() {}\n"), 1<<16),
		"random":       randomBytes(1 << 18),
	}
	s := openTestStore(t)
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			var h Hash
			inTx(t, s, func(ctx context.Context, tx *Tx) {
				var err error
				if h, err = tx.PutBlob(ctx, data); err != nil {
					t.Fatalf("put: %v", err)
				}
			})
			if want := HashObject(BlobType, data); h != want {
				t.Errorf("hash = %s, want %s", h, want)
			}
			inTx(t, s, func(ctx context.Context, tx *Tx) {
				got, err := tx.Blob(ctx, h)
				if err != nil {
					t.Fatalf("get: %v", err)
				}
				if !bytes.Equal(got, data) {
					t.Errorf("read back %d bytes that differ from the %d stored", len(got), len(data))
				}
			})
		})
	}
}

func TestStore_PutIsIdempotent(t *testing.T) {
	s := openTestStore(t)
	inTx(t, s, func(ctx context.Context, tx *Tx) {
		first, err := tx.PutBlob(ctx, []byte("same"))
		if err != nil {
			t.Fatal(err)
		}
		second, err := tx.PutBlob(ctx, []byte("same"))
		if err != nil {
			t.Fatal(err)
		}
		if first != second {
			t.Errorf("hashes differ: %s, %s", first, second)
		}
	})
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM objects`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("stored %d objects, want 1", n)
	}
}

func TestStore_TreeRoundTripsInGitOrder(t *testing.T) {
	s := openTestStore(t)
	inTx(t, s, func(ctx context.Context, tx *Tx) {
		blob, err := tx.PutBlob(ctx, []byte("x"))
		if err != nil {
			t.Fatal(err)
		}
		sub, err := tx.PutTree(ctx, []TreeEntry{{Name: "inner", Mode: ModeFile, Hash: blob}})
		if err != nil {
			t.Fatal(err)
		}
		want := []TreeEntry{
			{Name: "a.txt", Mode: ModeFile, Hash: blob},
			{Name: "a", Mode: ModeDir, Hash: sub},
			{Name: "run.sh", Mode: ModeExec, Hash: blob},
		}
		root, err := tx.PutTree(ctx, []TreeEntry{want[2], want[1], want[0]})
		if err != nil {
			t.Fatal(err)
		}
		got, err := tx.Tree(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, want) {
			t.Errorf("tree = %+v, want %+v", got, want)
		}
	})
}

func TestStore_ReportsMissingAndMistypedObjects(t *testing.T) {
	s := openTestStore(t)
	inTx(t, s, func(ctx context.Context, tx *Tx) {
		blob, err := tx.PutBlob(ctx, []byte("x"))
		if err != nil {
			t.Fatal(err)
		}
		tree, err := tx.PutTree(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		missing := HashObject(BlobType, []byte("never stored"))

		if _, err := tx.Blob(ctx, missing); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("missing blob: err = %v, want fs.ErrNotExist", err)
		}
		if _, err := tx.Tree(ctx, missing); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("missing tree: err = %v, want fs.ErrNotExist", err)
		}
		if _, err := tx.Blob(ctx, tree); err == nil || errors.Is(err, fs.ErrNotExist) {
			t.Errorf("tree read as blob: err = %v, want a type error", err)
		}
		if _, err := tx.Tree(ctx, blob); err == nil || errors.Is(err, fs.ErrNotExist) {
			t.Errorf("blob read as tree: err = %v, want a type error", err)
		}
	})
}

func TestStore_RollbackDiscardsWrites(t *testing.T) {
	s := openTestStore(t)
	ctx := t.Context()
	tx, err := s.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	h, err := tx.PutBlob(ctx, []byte("discarded"))
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	inTx(t, s, func(ctx context.Context, tx *Tx) {
		if _, err := tx.Blob(ctx, h); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("blob survived rollback: err = %v", err)
		}
	})
}

func TestStore_RejectsCorruptSize(t *testing.T) {
	s := openTestStore(t)
	var h Hash
	inTx(t, s, func(ctx context.Context, tx *Tx) {
		var err error
		if h, err = tx.PutBlob(ctx, []byte("twelve bytes")); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := s.db.Exec(`UPDATE objects SET size = 11 WHERE hash = ?`, h); err != nil {
		t.Fatal(err)
	}
	inTx(t, s, func(ctx context.Context, tx *Tx) {
		if _, err := tx.Blob(ctx, h); err == nil {
			t.Error("read a blob whose recorded size is wrong")
		}
	})
}

// TestNewObjects_ReadsWithTheStoresCodec stores a blob with the Stored codec
// and reads it back through NewObjects on the bare database.
func TestNewObjects_ReadsWithTheStoresCodec(t *testing.T) {
	s := openTestStore(t)
	s.codec = Stored{}
	data := []byte("kept as it is\n")
	var h Hash
	inTx(t, s, func(ctx context.Context, tx *Tx) {
		var err error
		if h, err = tx.PutBlob(ctx, data); err != nil {
			t.Fatal(err)
		}
	})

	var raw []byte
	if err := s.db.QueryRow(`SELECT data FROM objects WHERE hash = ?`, h).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, data) {
		t.Errorf("Stored wrote %q, want the content %q unchanged", raw, data)
	}
	got, err := NewObjects(s.db, Stored{}).Blob(t.Context(), h)
	if err != nil || !bytes.Equal(got, data) {
		t.Errorf("blob = %q, %v; want %q", got, err, data)
	}
}

func TestStore_EntriesRejectsCorruptRows(t *testing.T) {
	tests := map[string]struct {
		path string
		mode Mode
	}{
		"directory mode": {"a.txt", ModeDir},
		"zero mode":      {"a.txt", 0},
		"absolute path":  {"/a.txt", ModeFile},
		"dot dot":        {"a/../b", ModeFile},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			s := openTestStore(t)
			_, err := s.db.Exec(`
				INSERT INTO entries (project, path, mode, hash, size, mtime, ctime, inode, checked)
				VALUES (?, ?, ?, ?, 0, 0, 0, 0, 0)`, s.project, tt.path, int64(tt.mode), Hash{})
			if err != nil {
				t.Fatal(err)
			}
			inTx(t, s, func(ctx context.Context, tx *Tx) {
				var last error
				for _, err := range tx.Entries(ctx) {
					last = err
				}
				if last == nil {
					t.Errorf("read the entry %q with mode %o without an error", tt.path, tt.mode)
				}
			})
		})
	}
}

// indexEntries collects the file index.
func indexEntries(t *testing.T, tx *Tx) []IndexEntry {
	t.Helper()
	var all []IndexEntry
	for e, err := range tx.Entries(t.Context()) {
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, e)
	}
	return all
}

// sameEntry reports whether a and b hold the same values.
func sameEntry(a, b IndexEntry) bool {
	return a.Path == b.Path && a.Mode == b.Mode && a.Hash == b.Hash && a.Size == b.Size &&
		a.ModTime.Equal(b.ModTime) && a.ChangeTime.Equal(b.ChangeTime) && a.Inode == b.Inode &&
		a.Checked.Equal(b.Checked)
}

func TestStore_IndexSetsReplacesAndDeletes(t *testing.T) {
	s := openTestStore(t)
	at := time.Date(2026, 9, 24, 12, 0, 0, 123456789, time.UTC)
	a := IndexEntry{
		Path: "dir/a.go", Mode: ModeFile, Hash: HashObject(BlobType, []byte("a")), Size: 1,
		ModTime: at, ChangeTime: at.Add(time.Millisecond), Inode: 1 << 63, Checked: at.Add(time.Second),
	}
	b := IndexEntry{Path: "b.sh", Mode: ModeExec, Hash: HashObject(BlobType, []byte("b")), Size: 1, ModTime: at, Checked: at}
	replaced := a
	replaced.Hash, replaced.Size, replaced.ModTime = HashObject(BlobType, []byte("a2")), 2, at.Add(time.Minute)

	inTx(t, s, func(ctx context.Context, tx *Tx) {
		for _, e := range []IndexEntry{a, b, replaced} {
			if err := tx.SetEntry(ctx, e); err != nil {
				t.Fatalf("set %s: %v", e.Path, err)
			}
		}
		if err := tx.DeleteEntry(ctx, b.Path); err != nil {
			t.Fatal(err)
		}
		if err := tx.DeleteEntry(ctx, "never/indexed"); err != nil {
			t.Fatalf("delete of an unknown path: %v", err)
		}
	})
	inTx(t, s, func(ctx context.Context, tx *Tx) {
		got := indexEntries(t, tx)
		if len(got) != 1 || !sameEntry(got[0], replaced) {
			t.Errorf("index = %+v, want only %+v", got, replaced)
		}
	})
}

func TestStore_SetEntryRejectsInvalidEntries(t *testing.T) {
	valid := IndexEntry{Path: "a.txt", Mode: ModeFile, ModTime: time.Now(), Checked: time.Now()}
	tests := map[string]func(*IndexEntry){
		"empty path":      func(e *IndexEntry) { e.Path = "" },
		"dot":             func(e *IndexEntry) { e.Path = "." },
		"absolute path":   func(e *IndexEntry) { e.Path = "/a.txt" },
		"dot dot":         func(e *IndexEntry) { e.Path = "a/../b" },
		"trailing slash":  func(e *IndexEntry) { e.Path = "a/" },
		"directory mode":  func(e *IndexEntry) { e.Mode = ModeDir },
		"unknown mode":    func(e *IndexEntry) { e.Mode = 0o100600 },
		"zero-value mode": func(e *IndexEntry) { e.Mode = 0 },
	}
	s := openTestStore(t)
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			e := valid
			mutate(&e)
			inTx(t, s, func(ctx context.Context, tx *Tx) {
				if err := tx.SetEntry(ctx, e); err == nil {
					t.Errorf("SetEntry(%+v) succeeded", e)
				}
			})
		})
	}
}

// TestStore_SharesObjectsButNotIndexes checks that two projects in one
// database read each other's objects but keep separate file indexes.
func TestStore_SharesObjectsButNotIndexes(t *testing.T) {
	db := openTestDB(t)
	a, b := projectStore(t, db, "/work/a"), projectStore(t, db, "/work/b")
	at := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)

	var h Hash
	inTx(t, a, func(ctx context.Context, tx *Tx) {
		var err error
		if h, err = tx.PutBlob(ctx, []byte("shared")); err != nil {
			t.Fatal(err)
		}
		e := IndexEntry{Path: "a.txt", Mode: ModeFile, Hash: h, Size: 6, ModTime: at, Checked: at}
		if err := tx.SetEntry(ctx, e); err != nil {
			t.Fatal(err)
		}
	})
	inTx(t, b, func(ctx context.Context, tx *Tx) {
		if got, err := tx.Blob(ctx, h); err != nil || string(got) != "shared" {
			t.Errorf("blob stored by another project = %q, %v", got, err)
		}
		if got := indexEntries(t, tx); len(got) != 0 {
			t.Errorf("index of another project = %+v, want none", got)
		}
		if err := tx.DeleteEntry(ctx, "a.txt"); err != nil {
			t.Fatal(err)
		}
	})
	inTx(t, a, func(ctx context.Context, tx *Tx) {
		if got := indexEntries(t, tx); len(got) != 1 || got[0].Path != "a.txt" {
			t.Errorf("index after another project deleted a.txt = %+v, want a.txt", got)
		}
	})
}
