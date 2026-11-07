package engine

import (
	"context"
	"errors"
	"io/fs"
	"math/rand/v2"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// checkout checks out h in w and commits.
func checkout(t *testing.T, w *Worktree, s *Store, h Hash) {
	t.Helper()
	inTx(t, s, func(ctx context.Context, tx *Tx) {
		if err := w.Checkout(ctx, tx, h); err != nil {
			t.Fatalf("checkout %s: %v", h, err)
		}
	})
}

// wantContent fails the test unless the file name in dir holds content.
func wantContent(t *testing.T, dir, name, content string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if string(got) != content {
		t.Fatalf("%s = %q, want %q", name, got, content)
	}
}

// wantMissing fails the test if name exists in dir.
func wantMissing(t *testing.T, dir, name string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(dir, name)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s should not exist: lstat err = %v", name, err)
	}
}

func TestWorktreeCheckout_RestoresEditsAndDeletesAddedFiles(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, "keep.txt", "kept")
	writeFile(t, dir, "edit.txt", "v1")
	h := stage(t, w, s)

	writeFile(t, dir, "edit.txt", "v2")
	writeFile(t, dir, "sub/new.txt", "made by agent")
	checkout(t, w, s, h)

	wantContent(t, dir, "edit.txt", "v1")
	wantContent(t, dir, "keep.txt", "kept")
	wantMissing(t, dir, "sub")
}

func TestWorktreeCheckout_RoundTrips(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, "a.txt", "v1")
	first := stage(t, w, s)
	writeFile(t, dir, "a.txt", "v2")
	writeFile(t, dir, "added.txt", "only in second")
	second := stage(t, w, s)

	checkout(t, w, s, first)
	checkout(t, w, s, second)
	if got := stage(t, w, s); got != second {
		t.Fatalf("round trip landed on %s, want %s", got, second)
	}
	wantContent(t, dir, "added.txt", "only in second")
}

func TestWorktreeCheckout_IndependentOfStartingPoint(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, "a.txt", "target")
	target := stage(t, w, s)

	for _, name := range []string{"ahead.txt", "behind.txt"} {
		writeFile(t, dir, name, "drift")
		stage(t, w, s)
		checkout(t, w, s, target)
		if got := stage(t, w, s); got != target {
			t.Fatalf("checkout from %s landed on %s, want %s", name, got, target)
		}
	}
}

func TestWorktreeCheckout_KeepsIgnoredFiles(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, ".gitignore", "secret.txt\nbuild/\n")
	writeFile(t, dir, "tracked.txt", "v1")
	h := stage(t, w, s)

	writeFile(t, dir, "tracked.txt", "v2")
	writeFile(t, dir, "secret.txt", "my key")
	writeFile(t, dir, "build/out.bin", "artifact")
	writeFile(t, dir, ".git/info/exclude", "local-notes.md\n")
	writeFile(t, dir, "local-notes.md", "scratch")
	checkout(t, w, s, h)

	wantContent(t, dir, "tracked.txt", "v1")
	wantContent(t, dir, "secret.txt", "my key")
	wantContent(t, dir, "build/out.bin", "artifact")
	wantContent(t, dir, "local-notes.md", "scratch")
}

func TestWorktreeCheckout_RejectsUnknownTreeWithoutChanges(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, "a.txt", "v1")
	stage(t, w, s)
	writeFile(t, dir, "a.txt", "v2")

	unknown := mustParseHash(t, "0123456789abcdef0123456789abcdef01234567")
	inTx(t, s, func(ctx context.Context, tx *Tx) {
		if err := w.Checkout(ctx, tx, unknown); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("checkout of an unknown tree: err = %v, want fs.ErrNotExist", err)
		}
	})
	wantContent(t, dir, "a.txt", "v2")
}

// TestWorktreeCheckout_RejectsMissingSubtreeWithoutChanges gives Checkout a
// tree whose root is stored but whose subdirectory is not. The root alone
// already differs from the worktree, so a checkout that acted before reading
// every tree would change a.txt.
func TestWorktreeCheckout_RejectsMissingSubtreeWithoutChanges(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, "a.txt", "v1")
	writeFile(t, dir, "sub/b.txt", "b")
	stage(t, w, s)
	writeFile(t, dir, "a.txt", "v2")

	var h Hash
	inTx(t, s, func(ctx context.Context, tx *Tx) {
		blob, err := tx.PutBlob(ctx, []byte("v1"))
		if err != nil {
			t.Fatal(err)
		}
		h, err = tx.PutTree(ctx, []TreeEntry{
			{Name: "a.txt", Mode: ModeFile, Hash: blob},
			{Name: "sub", Mode: ModeDir, Hash: HashObject(TreeType, []byte("never stored"))},
		})
		if err != nil {
			t.Fatal(err)
		}
	})
	inTx(t, s, func(ctx context.Context, tx *Tx) {
		if err := w.Checkout(ctx, tx, h); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("checkout of a tree with a missing subtree: err = %v, want fs.ErrNotExist", err)
		}
	})
	wantContent(t, dir, "a.txt", "v2")
	wantContent(t, dir, "sub/b.txt", "b")
}

func TestWorktreeCheckout_RestoresModes(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	script := writeFile(t, dir, "run.sh", "#!/bin/sh\necho hi\n")
	if err := os.Chmod(script, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "plain.txt", "p")
	if err := os.Symlink("plain.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	h := stage(t, w, s)

	if err := os.Chmod(script, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "link", "now a regular file")
	checkout(t, w, s, h)

	if info, err := os.Stat(script); err != nil || info.Mode()&0o100 == 0 {
		t.Errorf("run.sh lost its executable bit: %v, %v", info, err)
	}
	if target, err := os.Readlink(filepath.Join(dir, "link")); err != nil || target != "plain.txt" {
		t.Errorf("link = %q, %v; want a symlink to plain.txt", target, err)
	}
}

func TestWorktreeCheckout_SwapsFilesAndDirectories(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, "x", "file x")
	writeFile(t, dir, "y/z", "file y/z")
	a := stage(t, w, s)

	for _, name := range []string{"x", "y"} {
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, dir, "x/inner", "file x/inner")
	writeFile(t, dir, "y", "file y")
	b := stage(t, w, s)

	checkout(t, w, s, a)
	wantContent(t, dir, "x", "file x")
	wantContent(t, dir, "y/z", "file y/z")
	checkout(t, w, s, b)
	wantContent(t, dir, "x/inner", "file x/inner")
	wantContent(t, dir, "y", "file y")
}

// TestWorktreeCheckout_ReplacesWhatStandsInTheWay pins the behavior of git
// read-tree --reset -u: untracked and ignored entries give way where the tree
// puts a file or a directory.
func TestWorktreeCheckout_ReplacesWhatStandsInTheWay(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, "out", "file")
	writeFile(t, dir, "d/f", "1")
	writeFile(t, dir, "z.ign", "tracked")
	h := stage(t, w, s)

	writeFile(t, dir, ".git/info/exclude", "*.ign\nd\n")
	for _, name := range []string{"out", "d"} {
		if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, dir, "out/a.ign", "ignored artifact")
	writeFile(t, dir, "d", "ignored file")
	writeFile(t, dir, "z.ign", "ignored edit")
	checkout(t, w, s, h)

	wantContent(t, dir, "out", "file")
	wantContent(t, dir, "d/f", "1")
	wantContent(t, dir, "z.ign", "tracked")
}

func TestWorktreeCheckout_DoesNotWriteThroughSymlinks(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, "a", "A")
	writeFile(t, dir, "dir/f", "F")
	h := stage(t, w, s)

	writeFile(t, dir, ".git/info/exclude", "c\nother/\n")
	writeFile(t, dir, "c", "C")
	writeFile(t, dir, "other/f", "O")
	for link, target := range map[string]string{"a": "c", "dir": "other"} {
		if err := os.RemoveAll(filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}
	checkout(t, w, s, h)

	wantContent(t, dir, "a", "A")
	wantContent(t, dir, "dir/f", "F")
	wantContent(t, dir, "c", "C")
	wantContent(t, dir, "other/f", "O")
	if info, err := os.Lstat(filepath.Join(dir, "dir")); err != nil || !info.IsDir() {
		t.Errorf("dir is not a directory again: %v, %v", info, err)
	}
}

func TestWorktreeCheckout_RemovesOnlyDirectoriesItEmptied(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, ".git/info/exclude", "*.ign\n")
	writeFile(t, dir, "keep.txt", "k")
	h := stage(t, w, s)

	writeFile(t, dir, "new/deep/file.txt", "n")
	writeFile(t, dir, "mixed/file.txt", "m")
	writeFile(t, dir, "mixed/x.ign", "ignored")
	if err := os.MkdirAll(filepath.Join(dir, "untouched-empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	checkout(t, w, s, h)

	wantMissing(t, dir, "new")
	wantMissing(t, dir, "mixed/file.txt")
	wantContent(t, dir, "mixed/x.ign", "ignored")
	if _, err := os.Stat(filepath.Join(dir, "untouched-empty")); err != nil {
		t.Errorf("an empty directory checkout never held was removed: %v", err)
	}
}

func TestWorktreeCheckout_LeavesMatchingFilesAlone(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	same := writeFile(t, dir, "same.txt", "unchanged")
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(same, time.Time{}, old); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "a.txt", "v1")
	h := stage(t, w, s)

	writeFile(t, dir, "a.txt", "v2")
	checkout(t, w, s, h)

	info, err := os.Stat(same)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(old) {
		t.Errorf("same.txt was rewritten: modified %v, want %v", info.ModTime(), old)
	}
}

// TestWorktreeCheckout_RandomStatesRoundTrip checks out random snapshots in
// random order. Each must come back exactly, with no empty directory left
// behind.
func TestWorktreeCheckout_RandomStatesRoundTrip(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	rng := rand.New(rand.NewChaCha8([32]byte{5}))
	var states []Hash
	for range 8 {
		writeRandomState(t, rng, dir)
		states = append(states, stage(t, w, s))
	}
	for i := range 40 {
		h := states[rng.IntN(len(states))]
		checkout(t, w, s, h)
		if got := stage(t, w, s); got != h {
			t.Fatalf("step %d: checkout of %s staged as %s", i, h, got)
		}
		wantNoEmptyDirs(t, dir)
	}
}

// writeRandomState replaces the contents of dir with random files, links,
// and directories drawn from few names, so that states swap files for
// directories.
func writeRandomState(t *testing.T, rng *rand.Rand, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
	files := make(map[string]bool)
	for range 12 {
		name := randomPath(rng)
		if conflicts(files, name) {
			continue
		}
		files[name] = true
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := writeRandomFile(rng, p); err != nil {
			t.Fatal(err)
		}
	}
}

// randomPath returns a path of one to three elements from a small set.
func randomPath(rng *rand.Rand) string {
	elems := make([]string, 1+rng.IntN(3))
	for i := range elems {
		elems[i] = []string{"a", "b", "c"}[rng.IntN(3)]
	}
	return path.Join(elems...)
}

// conflicts reports whether name cannot join files: it or one of its
// parents is a file, or it is a parent of a file.
func conflicts(files map[string]bool, name string) bool {
	for f := range files {
		if f == name || strings.HasPrefix(name, f+"/") || strings.HasPrefix(f, name+"/") {
			return true
		}
	}
	return false
}

// writeRandomFile creates p as a regular file, an executable, or a symbolic
// link, with random content.
func writeRandomFile(rng *rand.Rand, p string) error {
	content := []string{"alpha", "beta", "gamma"}[rng.IntN(3)]
	switch rng.IntN(3) {
	case 0:
		return os.Symlink(content, p)
	case 1:
		return os.WriteFile(p, []byte(content), 0o755)
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

// wantNoEmptyDirs fails the test if a directory below dir is empty.
func wantNoEmptyDirs(t *testing.T, dir string) {
	t.Helper()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || p == dir {
			return err
		}
		entries, err := os.ReadDir(p)
		if err == nil && len(entries) == 0 {
			t.Errorf("empty directory left behind: %s", p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
