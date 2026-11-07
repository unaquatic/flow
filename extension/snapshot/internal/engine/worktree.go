package engine

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// RacyWindow is how long after a file last changed the index must have read
// it before trusting its times. A write within the file system's timestamp
// granularity of a read can leave the times unchanged, so an entry read that
// soon after the last change is checked again.
const RacyWindow = 2 * time.Second

// MaxFileSize is the size of the largest file a snapshot records, just under
// 1 GB. SQLite holds no value over 1,000,000,000 bytes, and the limit leaves
// room for DEFLATE to grow content that does not compress.
const MaxFileSize = 1_000_000_000 - 1<<20

// Worktree is a directory whose files snapshots record.
type Worktree struct {
	dir  string
	root *os.Root

	// skip holds the slash-separated directories that Stage and Checkout
	// leave out.
	skip []string

	// now reports the current time. Tests replace it to make files look
	// settled without waiting out RacyWindow.
	now func() time.Time
}

// OpenWorktree opens the directory dir for snapshots. All file access stays
// inside dir.
//
// Stage and Checkout leave out each directory in skip that lies inside dir:
// Stage records nothing below it, and Checkout neither writes nor deletes
// anything there. An empty path, dir itself, and a directory outside dir have
// no effect.
func OpenWorktree(dir string, skip ...string) (*Worktree, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	inside, err := skipDirs(abs, skip)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("snapshot: open worktree: %w", err)
	}
	return &Worktree{dir: abs, root: root, skip: inside, now: time.Now}, nil
}

// Close releases the worktree's directory handle.
func (w *Worktree) Close() error {
	return w.root.Close()
}

// skipDirs returns the directories in skip that lie inside the worktree dir,
// slash-separated and relative to it.
func skipDirs(dir string, skip []string) ([]string, error) {
	var inside []string
	for _, s := range skip {
		if s == "" {
			continue
		}
		abs, err := filepath.Abs(s)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(realPath(dir), realPath(abs))
		if err != nil {
			continue // on another volume, so outside the worktree
		}
		if rel != "." && filepath.IsLocal(rel) {
			inside = append(inside, filepath.ToSlash(rel))
		}
	}
	return inside, nil
}

// Skipped reports whether the slash-separated path name lies in a directory
// the worktree leaves out.
func (w *Worktree) Skipped(name string) bool {
	return slices.ContainsFunc(w.skip, func(dir string) bool {
		return name == dir || strings.HasPrefix(name, dir+"/")
	})
}

// realPath returns the absolute path p with its symbolic links resolved. The
// part of p that does not exist yet is kept as it is, below the resolved
// part that does.
func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	parent := filepath.Dir(p)
	if parent == p {
		return p
	}
	return filepath.Join(realPath(parent), filepath.Base(p))
}

// Stage records the worktree in tx and returns the hash of its tree, which
// equals the tree Git would write for the same files. It stores the content
// of each file whose index entry is stale, brings the file index up to date,
// and stores one tree per directory holding a file.
//
// Stage leaves out what Git would not add: paths matched by the global
// excludes file that core.excludesFile names or Git's default one, the
// enclosing repository's info/exclude, or a .gitignore; anything named .git;
// directories holding a .git, which are separate repositories; and files that
// are neither regular files nor symbolic links. It also leaves out the
// directories OpenWorktree was told to skip. A directory that cannot be read
// for lack of permission is skipped, and so, with a logged warning, are a
// file that cannot be read for lack of permission and a file larger than
// MaxFileSize. A skipped file is untracked: a later Checkout leaves it alone
// unless the tree puts a file or directory in its place.
//
// Like Git, Stage trusts a file unchanged while its mode, size, modification
// and inode change times, and inode number match its index entry.
func (w *Worktree) Stage(ctx context.Context, tx *Tx) (Hash, error) {
	excludes, err := loadExcludes(w.dir)
	if err != nil {
		return Hash{}, err
	}
	index, err := loadIndex(ctx, tx)
	if err != nil {
		return Hash{}, err
	}
	s := &scan{index: index, skipped: w.Skipped, checked: w.now()}
	stored, err := s.stageFiles(ctx, tx, w.root, excludes)
	if err != nil {
		return Hash{}, fmt.Errorf("snapshot: stage: %w", err)
	}
	// What the walk did not reach was deleted or is now ignored.
	for name := range s.index {
		if err := tx.DeleteEntry(ctx, name); err != nil {
			return Hash{}, err
		}
	}
	return writeTrees(ctx, tx, append(s.files, stored...))
}

// fileEntry returns the index entry, without its hash, for the file name of
// the given mode as info, which must come from Lstat, describes it at time
// checked.
func fileEntry(name string, mode Mode, info fs.FileInfo, checked time.Time) IndexEntry {
	ctime, inode := sysStat(info)
	return IndexEntry{
		Path:       name,
		Mode:       mode,
		Size:       info.Size(),
		ModTime:    info.ModTime(),
		ChangeTime: ctime,
		Inode:      inode,
		Checked:    checked,
	}
}
