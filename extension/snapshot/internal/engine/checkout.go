package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
)

// Checkout makes the worktree match the tree h and records the result in
// tx. It works in both directions: the outcome depends only on h, never on
// the state it replaces.
//
// Checkout first stages the worktree, so the file index lists every file a
// snapshot would hold, and compares the staged tree with h, reading only the
// subtrees that differ. It checks that the store holds every blob it will
// write, then deletes the files h lacks, removes the directories this leaves
// empty, and writes each file whose content or mode differs in h. Ignored
// and untracked files survive, except where h puts a file or directory in
// their place: there h wins, as with git read-tree --reset -u. The
// directories OpenWorktree was told to skip are never touched, even when h
// holds files there. Nothing changes when the store lacks h, a tree below
// it, or a blob Checkout would write.
func (w *Worktree) Checkout(ctx context.Context, tx *Tx, h Hash) error {
	current, err := w.Stage(ctx, tx)
	if err != nil {
		return err
	}
	changes, err := w.Changes(ctx, tx, current, h)
	if err != nil {
		return err
	}
	if err := checkBlobs(ctx, tx, changes); err != nil {
		return err
	}
	var deleted []string
	for _, c := range changes {
		if !c.New.Exists() {
			deleted = append(deleted, c.Path)
		}
	}
	if err := w.removeFiles(ctx, tx, deleted); err != nil {
		return err
	}
	return w.writeFiles(ctx, tx, changes)
}

// Changes returns the changes from the tree from to the tree to in tx, in
// path order, leaving out the paths the worktree skips. Unlike
// [Objects.Changes] it reads every change before it returns, so Checkout
// touches no file when a tree is missing.
func (w *Worktree) Changes(ctx context.Context, tx *Tx, from, to Hash) ([]Change, error) {
	var changes []Change
	for c, err := range tx.Changes(ctx, from, to) {
		if err != nil {
			return nil, err
		}
		if !w.Skipped(c.Path) {
			changes = append(changes, c)
		}
	}
	return changes, nil
}

// checkBlobs reports an error wrapping [fs.ErrNotExist] when tx lacks the
// blob of a file that changes would write.
func checkBlobs(ctx context.Context, tx *Tx, changes []Change) error {
	for _, c := range changes {
		if !c.New.Exists() {
			continue
		}
		ok, err := tx.Has(ctx, c.New.Hash)
		if err != nil {
			return fmt.Errorf("snapshot: checkout %s: %w", c.Path, err)
		}
		if !ok {
			return fmt.Errorf("snapshot: checkout %s: blob %s: %w", c.Path, c.New.Hash, fs.ErrNotExist)
		}
	}
	return nil
}

// writeFiles writes the new side of each change that leaves a file.
func (w *Worktree) writeFiles(ctx context.Context, tx *Tx, changes []Change) error {
	checked := w.now()
	for _, c := range changes {
		if !c.New.Exists() {
			continue
		}
		f := IndexEntry{Path: c.Path, Mode: c.New.Mode, Hash: c.New.Hash, Checked: checked}
		if err := w.checkoutFile(ctx, tx, f); err != nil {
			return fmt.Errorf("snapshot: checkout %s: %w", c.Path, err)
		}
	}
	return nil
}

// removeFiles deletes the files names from the worktree and the index, then
// removes the directories this leaves empty.
func (w *Worktree) removeFiles(ctx context.Context, tx *Tx, names []string) error {
	dirs := make(map[string]bool)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.root.Remove(filepath.FromSlash(name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("snapshot: remove %s: %w", name, err)
		}
		if err := tx.DeleteEntry(ctx, name); err != nil {
			return err
		}
		for dir, _ := splitPath(name); dir != ""; dir, _ = splitPath(dir) {
			dirs[dir] = true
		}
	}
	for _, dir := range slices.SortedFunc(maps.Keys(dirs), deeperFirst) {
		if err := w.removeIfEmpty(dir); err != nil {
			return err
		}
	}
	return nil
}

// removeIfEmpty removes dir if it is a directory holding nothing.
func (w *Worktree) removeIfEmpty(dir string) error {
	info, err := w.root.Lstat(filepath.FromSlash(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return nil
	}
	entries, err := fs.ReadDir(w.root.FS(), dir)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return nil
	}
	return w.root.Remove(filepath.FromSlash(dir))
}

// checkoutFile writes the file f describes and records it in the index.
func (w *Worktree) checkoutFile(ctx context.Context, tx *Tx, f IndexEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := tx.Blob(ctx, f.Hash)
	if err != nil {
		return err
	}
	parent, _ := splitPath(f.Path)
	if err := w.makeDir(parent); err != nil {
		return err
	}
	if err := w.clear(f.Path); err != nil {
		return err
	}
	if err := w.writeFile(f.Path, f.Mode, data); err != nil {
		return err
	}
	info, err := w.root.Lstat(filepath.FromSlash(f.Path))
	if err != nil {
		return err
	}
	e := fileEntry(f.Path, f.Mode, info, f.Checked)
	e.Hash = f.Hash
	return tx.SetEntry(ctx, e)
}

// makeDir makes dir a directory, creating it and its parents as needed. A
// file or symbolic link where dir or a parent belongs is removed first.
func (w *Worktree) makeDir(dir string) error {
	if dir == "" {
		return nil
	}
	parent, _ := splitPath(dir)
	if err := w.makeDir(parent); err != nil {
		return err
	}
	name := filepath.FromSlash(dir)
	info, err := w.root.Lstat(name)
	switch {
	case err == nil && info.IsDir():
		return nil
	case err == nil:
		if err := w.root.Remove(name); err != nil {
			return err
		}
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	return w.root.Mkdir(name, 0o777)
}

// clear removes whatever is at name, including a whole directory.
func (w *Worktree) clear(name string) error {
	err := w.root.RemoveAll(filepath.FromSlash(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// writeFile creates name holding data, as a symbolic link to data when mode
// is ModeSymlink. As in Git, the process umask applies to the permissions.
func (w *Worktree) writeFile(name string, mode Mode, data []byte) error {
	osName := filepath.FromSlash(name)
	switch mode {
	case ModeSymlink:
		return w.root.Symlink(string(data), osName)
	case ModeExec:
		return w.root.WriteFile(osName, data, 0o777)
	}
	return w.root.WriteFile(osName, data, 0o666)
}
