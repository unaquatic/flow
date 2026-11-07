package engine

import (
	"bytes"
	"context"
	"iter"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/unaquatic/flow/extension/snapshot/internal/engine/internal/diff"
)

// File is one side of a Change: the mode of a file and the hash of its
// content. The zero File stands for a file that is absent.
type File struct {
	Mode Mode
	Hash Hash
}

// Exists reports whether f stands for a file rather than for its absence.
func (f File) Exists() bool {
	return f.Mode != 0
}

// Change describes a file that differs between two trees.
type Change struct {
	// Path is the file's slash-separated path.
	Path string

	// Old and New are the file in the first tree and in the second.
	Old, New File
}

// Status is the kind of a Change.
type Status uint8

const (
	Added    Status = iota + 1 // the file exists only in the second tree
	Deleted                    // the file exists only in the first tree
	Modified                   // the file exists in both, with another mode or content
)

// String returns "added", "deleted", or "modified".
func (s Status) String() string {
	switch s {
	case Added:
		return "added"
	case Deleted:
		return "deleted"
	case Modified:
		return "modified"
	}
	return "status " + strconv.Itoa(int(s))
}

// Status reports the kind of c.
func (c Change) Status() Status {
	switch {
	case !c.Old.Exists():
		return Added
	case !c.New.Exists():
		return Deleted
	}
	return Modified
}

// Changes yields the files that differ between the trees from and to, in
// path order, without detecting renames. It reads only the subtrees that
// differ, and stops at the first error, which it yields.
func (o Objects) Changes(ctx context.Context, from, to Hash) iter.Seq2[Change, error] {
	return func(yield func(Change, error) bool) {
		// walk compares the trees a and b found at dir, where the zero Hash
		// stands for an absent tree, and reports whether to go on.
		var walk func(dir string, a, b Hash) (bool, error)
		walk = func(dir string, a, b Hash) (bool, error) {
			if a == b {
				return true, nil
			}
			olds, err := o.treeByName(ctx, a)
			if err != nil {
				return false, err
			}
			news, err := o.treeByName(ctx, b)
			if err != nil {
				return false, err
			}
			for _, key := range changeKeys(olds, news) {
				name, isDir := strings.CutSuffix(key, "/")
				o, n := olds[name], news[name]
				p := path.Join(dir, name)
				if isDir {
					if more, err := walk(p, dirHash(o), dirHash(n)); !more || err != nil {
						return false, err
					}
					continue
				}
				if c, ok := fileChange(p, o, n); ok && !yield(c, nil) {
					return false, nil
				}
			}
			return true, nil
		}
		if _, err := walk("", from, to); err != nil {
			yield(Change{}, err)
		}
	}
}

// FileDiffs returns, for each file that differs between the trees from and
// to, its status and the number of lines added and deleted. A binary file has
// no line counts.
func (o Objects) FileDiffs(ctx context.Context, from, to Hash) ([]FileDiff, error) {
	var diffs []FileDiff
	for c, err := range o.Changes(ctx, from, to) {
		if err != nil {
			return nil, err
		}
		before, err := o.content(ctx, c.Old)
		if err != nil {
			return nil, err
		}
		after, err := o.content(ctx, c.New)
		if err != nil {
			return nil, err
		}
		fd := FileDiff{Path: c.Path, Status: c.Status()}
		if !IsBinary(before) && !IsBinary(after) {
			fd.Additions, fd.Deletions = diff.Stat(diff.Diff(before, after))
		}
		diffs = append(diffs, fd)
	}
	return diffs, nil
}

// treeByName returns the entries of the tree h keyed by name. The zero Hash
// yields no entries.
func (o Objects) treeByName(ctx context.Context, h Hash) (map[string]TreeEntry, error) {
	if h.IsZero() {
		return nil, nil
	}
	entries, err := o.Tree(ctx, h)
	if err != nil {
		return nil, err
	}
	byName := make(map[string]TreeEntry, len(entries))
	for _, e := range entries {
		byName[e.Name] = e
	}
	return byName, nil
}

// content returns the content of f, which is empty when f is absent.
func (o Objects) content(ctx context.Context, f File) ([]byte, error) {
	if !f.Exists() {
		return nil, nil
	}
	return o.Blob(ctx, f.Hash)
}

// changeKeys returns the names in olds and news that may hold changes, in
// path order. A name that is a file on either side appears as itself, and a
// name that is a directory on either side appears with a trailing slash,
// which sorts it where the paths below it sort.
func changeKeys(olds, news map[string]TreeEntry) []string {
	var keys []string
	for name := range unionKeys(olds, news) {
		o, n := olds[name], news[name]
		if o.File().Exists() || n.File().Exists() {
			keys = append(keys, name)
		}
		if o.Mode == ModeDir || n.Mode == ModeDir {
			keys = append(keys, name+"/")
		}
	}
	slices.Sort(keys)
	return keys
}

// unionKeys returns a set of the keys of a and b.
func unionKeys(a, b map[string]TreeEntry) map[string]bool {
	keys := make(map[string]bool, len(a)+len(b))
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	return keys
}

// dirHash returns the hash of e when it is a directory, and the zero Hash
// otherwise.
func dirHash(e TreeEntry) Hash {
	if e.Mode != ModeDir {
		return Hash{}
	}
	return e.Hash
}

// File returns the file that e stands for, which is absent when e is a
// directory or no entry at all.
func (e TreeEntry) File() File {
	if e.Mode == ModeDir {
		return File{}
	}
	return File{Mode: e.Mode, Hash: e.Hash}
}

// fileChange returns the change to the file p between the entries o and n,
// treating an entry that is a directory as absent. It reports false when the
// file is the same on both sides.
func fileChange(p string, o, n TreeEntry) (Change, bool) {
	c := Change{Path: p, Old: o.File(), New: n.File()}
	return c, c.Old != c.New
}

// IsBinary reports whether data looks binary to Git: it holds a NUL byte in
// its first 8000 bytes.
func IsBinary(data []byte) bool {
	return bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0
}
