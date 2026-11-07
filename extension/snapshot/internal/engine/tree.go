package engine

import (
	"cmp"
	"context"
	"maps"
	"path"
	"slices"
	"strings"
)

// loadIndex returns the file index keyed by path.
func loadIndex(ctx context.Context, tx *Tx) (map[string]IndexEntry, error) {
	index := make(map[string]IndexEntry)
	for e, err := range tx.Entries(ctx) {
		if err != nil {
			return nil, err
		}
		index[e.Path] = e
	}
	return index, nil
}

// writeTrees stores one tree per directory holding files, deepest first, and
// returns the hash of the root tree.
func writeTrees(ctx context.Context, tx *Tx, files []IndexEntry) (Hash, error) {
	dirs := map[string][]TreeEntry{"": nil}
	for _, f := range files {
		dir, name := splitPath(f.Path)
		dirs[dir] = append(dirs[dir], TreeEntry{Name: name, Mode: f.Mode, Hash: f.Hash})
		addParents(dirs, dir)
	}
	for _, dir := range slices.SortedFunc(maps.Keys(dirs), deeperFirst) {
		if dir == "" {
			continue
		}
		h, err := tx.PutTree(ctx, dirs[dir])
		if err != nil {
			return Hash{}, err
		}
		parent, name := splitPath(dir)
		dirs[parent] = append(dirs[parent], TreeEntry{Name: name, Mode: ModeDir, Hash: h})
	}
	return tx.PutTree(ctx, dirs[""])
}

// addParents adds the directories above dir to dirs.
func addParents(dirs map[string][]TreeEntry, dir string) {
	for dir != "" {
		dir, _ = splitPath(dir)
		if _, ok := dirs[dir]; ok {
			return
		}
		dirs[dir] = nil
	}
}

// deeperFirst orders slash-separated paths by decreasing depth, so a
// directory comes after everything below it.
func deeperFirst(a, b string) int {
	return cmp.Compare(strings.Count(b, "/"), strings.Count(a, "/"))
}

// splitPath splits a slash-separated path into its directory, which is ""
// at the top, and its last element.
func splitPath(p string) (dir, name string) {
	dir, name = path.Split(p)
	return strings.TrimSuffix(dir, "/"), name
}
