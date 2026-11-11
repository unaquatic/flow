package engine

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/unaquatic/flow/extension/snapshot/internal/engine/internal/ignore"
)

// scan is the state of one walk over a worktree. The walk opens each
// directory once and works relative to it, so no path is resolved from the
// worktree root again.
type scan struct {
	// jobs receives the stale files the walk finds.
	jobs chan<- fileJob

	// index holds the entries not yet reached by the walk.
	index map[string]IndexEntry

	// skipped reports whether a directory is one the worktree leaves out.
	skipped func(name string) bool

	// checked is when the walk started, before any file was read.
	checked time.Time

	// files holds the entries of the unchanged files the walk staged.
	files []IndexEntry
}

// folder is a directory the walk is in: open as root, named name within the
// worktree, where the patterns of excludes apply.
type folder struct {
	root *os.Root
	name string

	// excludes holds the worktree-wide patterns and those of each .gitignore
	// from the worktree root down to this directory.
	excludes ignore.Matcher
}

// walk stages the directory dir, open as r, and everything below it, where
// the patterns of excludes apply. dir is "" for the worktree root. A
// directory below the root that holds a .git is a separate repository and is
// skipped, as is one that cannot be read for lack of permission.
func (s *scan) walk(ctx context.Context, r *os.Root, dir string, excludes ignore.Matcher) error {
	entries, err := fs.ReadDir(r.FS(), ".")
	if dir != "" && errors.Is(err, fs.ErrPermission) {
		return nil
	}
	if err != nil {
		return err
	}
	if dir != "" && slices.ContainsFunc(entries, named(".git")) {
		return nil
	}
	// The patterns of this directory's .gitignore apply only below it.
	patterns, err := readIgnoreFile(r, dir, entries)
	if err != nil {
		return err
	}
	f := folder{root: r, name: dir, excludes: excludes.With(patterns...)}
	for _, d := range entries {
		if err := s.visit(ctx, f, d); err != nil {
			return err
		}
	}
	return nil
}

// visit stages d, an entry of the directory f.
func (s *scan) visit(ctx context.Context, f folder, d fs.DirEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	name := path.Join(f.name, d.Name())
	if d.Name() == ".git" || f.excludes.Match(name, d.Type()) {
		return nil
	}
	if !d.IsDir() {
		return s.stageFile(ctx, f.root, name, d)
	}
	if s.skipped(name) {
		return nil
	}
	sub, err := f.root.OpenRoot(d.Name())
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return errors.Join(s.walk(ctx, sub, name, f.excludes), sub.Close())
}

// readIgnoreFile returns the patterns of the .gitignore among entries, the
// contents of the directory dir open as r. Like Git, it reads only a regular
// file and passes over one it cannot read for lack of permission.
func readIgnoreFile(r *os.Root, dir string, entries []fs.DirEntry) (ignore.Matcher, error) {
	i := slices.IndexFunc(entries, named(".gitignore"))
	if i < 0 || !entries[i].Type().IsRegular() {
		return nil, nil
	}
	data, err := r.ReadFile(".gitignore")
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return ignore.Parse(data, dir), nil
}

// named returns a function reporting whether a directory entry is called
// name.
func named(name string) func(fs.DirEntry) bool {
	return func(d fs.DirEntry) bool { return d.Name() == name }
}

// stageFile records the file name, the entry d of a directory open as r,
// when its index entry is up to date, and otherwise sends it to be stored.
func (s *scan) stageFile(ctx context.Context, r *os.Root, name string, d fs.DirEntry) error {
	info, err := d.Info()
	if errors.Is(err, fs.ErrNotExist) {
		return nil // removed during the walk
	}
	if err != nil {
		return err
	}
	mode, ok := ModeOf(info.Mode())
	if !ok {
		return nil
	}
	if info.Size() > MaxFileSize {
		slog.Warn("skip file larger than the size limit", "path", name, "size", info.Size(), "limit", MaxFileSize)
		return nil
	}
	fresh := fileEntry(name, mode, info, s.checked)
	if e, ok := s.index[name]; ok && unchanged(e, fresh) {
		delete(s.index, name)
		s.files = append(s.files, e)
		return nil
	}
	content, err := openContent(r, d.Name(), mode)
	if errors.Is(err, fs.ErrPermission) {
		slog.Warn("skip unreadable file", "path", name, "err", err)
		return nil
	}
	if err != nil {
		return ignoreNotExist(err)
	}
	delete(s.index, name)
	return s.send(ctx, fileJob{entry: fresh, content: content})
}

// send passes j on to be stored, closing its content if the stage is
// canceled first.
func (s *scan) send(ctx context.Context, j fileJob) error {
	select {
	case s.jobs <- j:
		return nil
	case <-ctx.Done():
		return errors.Join(context.Cause(ctx), j.content.Close())
	}
}

// ignoreNotExist returns nil for an error reporting a missing file, which
// means the file was removed during the walk, and err otherwise.
func ignoreNotExist(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// unchanged reports whether the index entry e still describes the file that
// f describes now. As in Git, the mode, size, modification and inode change
// times, and inode number must all match, and e must be settled.
func unchanged(e, f IndexEntry) bool {
	return e.Mode == f.Mode &&
		e.Size == f.Size &&
		e.Inode == f.Inode &&
		e.ModTime.Equal(f.ModTime) &&
		e.ChangeTime.Equal(f.ChangeTime) &&
		settled(e)
}

// settled reports whether e was read at least RacyWindow after its file
// last changed, so that a later change must have moved the file's times.
func settled(e IndexEntry) bool {
	last := e.ModTime
	if e.ChangeTime.After(last) {
		last = e.ChangeTime
	}
	return last.Before(e.Checked.Add(-RacyWindow))
}

// ModeOf returns the tree mode for a file of mode m, reporting false for
// anything but a regular file or a symbolic link. Like Git, it keeps only
// the owner's execute bit.
func ModeOf(m fs.FileMode) (Mode, bool) {
	switch {
	case m.Type() == fs.ModeSymlink:
		return ModeSymlink, true
	case !m.IsRegular():
		return 0, false
	case m.Perm()&0o100 != 0:
		return ModeExec, true
	}
	return ModeFile, true
}

// openContent opens what a blob records for name, a file directly in r: its
// content, or the target of a symbolic link, slash-separated, as Git records
// it on Windows too.
func openContent(r *os.Root, name string, mode Mode) (io.ReadCloser, error) {
	if mode == ModeSymlink {
		target, err := r.Readlink(name)
		if err != nil {
			return nil, err
		}
		return io.NopCloser(strings.NewReader(filepath.ToSlash(target))), nil
	}
	f, err := r.Open(name)
	if err != nil {
		return nil, err
	}
	return f, nil
}
