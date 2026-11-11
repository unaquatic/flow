package engine

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// isolateGitConfig points Git and the snapshot code at an empty home, so no
// excludes file of the user's applies.
func isolateGitConfig(t testing.TB) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "gitconfig"))
	return home
}

// openTestWorktree returns an empty worktree directory, the Worktree for it,
// and a store, with Git configuration isolated.
func openTestWorktree(t *testing.T) (string, *Worktree, *Store) {
	t.Helper()
	isolateGitConfig(t)
	dir := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir, openWorktree(t, dir), openTestStore(t)
}

// openWorktree opens dir, leaving out skip, and closes it when the test
// ends.
func openWorktree(t testing.TB, dir string, skip ...string) *Worktree {
	t.Helper()
	w, err := OpenWorktree(dir, skip...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return w
}

// stage stages w into s and commits.
func stage(t testing.TB, w *Worktree, s *Store) Hash {
	t.Helper()
	var h Hash
	inTx(t, s, func(ctx context.Context, tx *Tx) {
		var err error
		if h, err = w.Stage(ctx, tx); err != nil {
			t.Fatalf("stage: %v", err)
		}
	})
	return h
}

// treeNames returns the names in the tree h.
func treeNames(t *testing.T, s *Store, h Hash) []string {
	t.Helper()
	var names []string
	inTx(t, s, func(ctx context.Context, tx *Tx) {
		entries, err := tx.Tree(ctx, h)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			names = append(names, e.Name)
		}
	})
	return names
}

// subtree returns the hash of the directory name in the tree h.
func subtree(t *testing.T, s *Store, h Hash, name string) Hash {
	t.Helper()
	var sub Hash
	inTx(t, s, func(ctx context.Context, tx *Tx) {
		entries, err := tx.Tree(ctx, h)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(entries, func(e TreeEntry) bool { return e.Name == name })
		if i < 0 || entries[i].Mode != ModeDir {
			t.Fatalf("no directory %q in %v", name, entries)
		}
		sub = entries[i].Hash
	})
	return sub
}

// shadowGit initializes a separate Git directory whose worktree is dir, as
// the git-based snapshot service does, and returns a function that runs git
// on it and returns the trimmed output.
func shadowGit(t *testing.T, dir string) func(args ...string) string {
	t.Helper()
	base := []string{
		"--git-dir", filepath.Join(t.TempDir(), "shadow"), "--work-tree", dir,
		"-c", "core.ignorecase=false", "-c", "core.autocrlf=false",
		"-c", "core.symlinks=true", "-c", "core.filemode=true", "-c", "core.quotepath=false",
	}
	git := func(args ...string) string {
		t.Helper()
		return gitOutput(t, dir, nil, slices.Concat(base, args)...)
	}
	git("init", "--quiet")
	return git
}

// gitWriteTree returns the tree Git writes for worktree.
func gitWriteTree(t *testing.T, worktree string) string {
	t.Helper()
	git := shadowGit(t, worktree)
	git("add", "-A", ".")
	return git("write-tree")
}

func TestStage_MatchesGitWriteTree(t *testing.T) {
	requireGit(t)
	dir, w, s := openTestWorktree(t)

	writeFile(t, dir, ".gitignore", "*.log\nbuild/\n!keep.log\n")
	writeFile(t, dir, "sub/.gitignore", "/local\n")
	for _, name := range []string{
		"main.go", "a.log", "keep.log", "build/out", "sub/local", "sub/deep/local",
		"a/x", "a.txt", "a-b", "a0", "space name.txt", "ünïcode.txt",
	} {
		writeFile(t, dir, name, "content of "+name+"\n")
	}
	if err := os.Chmod(writeFile(t, dir, "run.sh", "#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A symlinked .gitignore is not read; the link itself is a file.
	writeFile(t, dir, "patterns", "*.txt\n")
	writeFile(t, dir, "linked/kept.txt", "k")
	for link, target := range map[string]string{
		"link": "main.go", "dirlink": "sub", "dangling": "nowhere", "linked/.gitignore": "../patterns",
	} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "empty", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	// An unreadable directory is skipped.
	if os.Geteuid() != 0 {
		writeFile(t, dir, "locked/secret.txt", "s")
		locked := filepath.Join(dir, "locked")
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(locked, 0o700) })
	}

	want := gitWriteTree(t, dir)
	if got := stage(t, w, s).String(); got != want {
		t.Errorf("tree = %s, git writes %s", got, want)
	}
}

func TestStage_EmptyWorktreeIsTheEmptyTree(t *testing.T) {
	_, w, s := openTestWorktree(t)
	if got, want := stage(t, w, s).String(), "4b825dc642cb6eb9a060e54bf8d69288fbee4904"; got != want {
		t.Errorf("tree = %s, want the empty tree %s", got, want)
	}
}

func TestStage_FollowsEditsAndStoresNothingNewWhenUnchanged(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, "a.txt", "v1")
	writeFile(t, dir, "b/c.txt", "c")

	first := stage(t, w, s)
	objects := countObjects(t, s)
	if again := stage(t, w, s); again != first {
		t.Fatalf("unchanged worktree staged as %s, then %s", first, again)
	}
	if n := countObjects(t, s); n != objects {
		t.Errorf("restaging an unchanged worktree stored %d new objects", n-objects)
	}

	writeFile(t, dir, "a.txt", "v2")
	if edited := stage(t, w, s); edited == first {
		t.Fatal("an edit left the tree unchanged")
	}
	writeFile(t, dir, "a.txt", "v1")
	if reverted := stage(t, w, s); reverted != first {
		t.Errorf("reverting the edit gave %s, want %s", reverted, first)
	}
}

// countObjects returns the number of stored objects.
func countObjects(t testing.TB, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM objects`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStage_ForgetsDeletedAndNewlyIgnoredFiles(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, "keep.txt", "k")
	writeFile(t, dir, "gone.txt", "g")
	writeFile(t, dir, "dir/secret.txt", "s")
	stage(t, w, s)

	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, ".gitignore", "secret.txt\n")
	got := stage(t, w, s)

	inTx(t, s, func(ctx context.Context, tx *Tx) {
		var paths []string
		for _, e := range indexEntries(t, tx) {
			paths = append(paths, e.Path)
		}
		if want := []string{".gitignore", "keep.txt"}; !slices.Equal(paths, want) {
			t.Errorf("index = %q, want %q", paths, want)
		}
	})

	// The same files staged from scratch give the same tree.
	fresh, freshWorktree, freshStore := openTestWorktree(t)
	writeFile(t, fresh, "keep.txt", "k")
	writeFile(t, fresh, ".gitignore", "secret.txt\n")
	if want := stage(t, freshWorktree, freshStore); got != want {
		t.Errorf("tree = %s, want %s as staged from scratch", got, want)
	}
}

func TestStage_SkipsGitDirectoriesAndNestedRepositories(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, ".git/config", "[core]\n")
	writeFile(t, dir, "vendor/lib/.git/HEAD", "ref: refs/heads/main\n")
	writeFile(t, dir, "vendor/lib/code.go", "package lib\n")
	writeFile(t, dir, "vendor/linked/.git", "gitdir: ../../.git/modules/linked\n")
	writeFile(t, dir, "vendor/linked/code.go", "package linked\n")
	writeFile(t, dir, "vendor/own.go", "package vendor\n")
	writeFile(t, dir, "main.go", "package main\n")

	root := stage(t, w, s)
	if got, want := treeNames(t, s, root), []string{"main.go", "vendor"}; !slices.Equal(got, want) {
		t.Errorf("root = %q, want %q", got, want)
	}
	vendor := subtree(t, s, root, "vendor")
	if got, want := treeNames(t, s, vendor), []string{"own.go"}; !slices.Equal(got, want) {
		t.Errorf("vendor = %q, want %q", got, want)
	}
}

func TestStage_HonorsExcludeFiles(t *testing.T) {
	tests := []struct {
		name string
		// setup writes the excludes around top and returns the worktree.
		setup func(t *testing.T, home, top string) string
	}{
		{"global excludes file", func(t *testing.T, home, top string) string {
			writeFile(t, home, "xdg/git/ignore", "excluded.txt\n")
			return top
		}},
		{"repository info/exclude", func(t *testing.T, home, top string) string {
			writeFile(t, top, ".git/info/exclude", "excluded.txt\n")
			return top
		}},
		{"enclosing repository", func(t *testing.T, home, top string) string {
			writeFile(t, top, ".git/info/exclude", "excluded.txt\n")
			return filepath.Join(top, "project")
		}},
		{"linked worktree", func(t *testing.T, home, top string) string {
			writeFile(t, top, "main/.git/info/exclude", "excluded.txt\n")
			writeFile(t, top, "main/.git/worktrees/wt/commondir", "../..\n")
			writeFile(t, top, "wt/.git", "gitdir: ../main/.git/worktrees/wt\n")
			return filepath.Join(top, "wt")
		}},
		{"core.excludesFile in GIT_CONFIG_GLOBAL", func(t *testing.T, home, top string) string {
			writeFile(t, home, "gitconfig", "[core]\n\texcludesFile = ~/global-ignore\n")
			writeFile(t, home, "global-ignore", "excluded.txt\n")
			return top
		}},
		{"core.excludesFile in ~/.gitconfig", func(t *testing.T, home, top string) string {
			t.Setenv("GIT_CONFIG_GLOBAL", "")
			writeFile(t, home, ".gitconfig", "[core]\n\texcludesFile = ~/global-ignore\n")
			writeFile(t, home, "global-ignore", "excluded.txt\n")
			return top
		}},
		{"core.excludesFile in the XDG config", func(t *testing.T, home, top string) string {
			t.Setenv("GIT_CONFIG_GLOBAL", "")
			writeFile(t, home, "xdg/git/config", "[core]\n\texcludesFile = "+slashJoin(home, "global-ignore")+"\n")
			writeFile(t, home, "global-ignore", "excluded.txt\n")
			return top
		}},
		{"core.excludesFile in the repository config", func(t *testing.T, home, top string) string {
			writeFile(t, top, ".git/config", "[core]\n\texcludesFile = "+slashJoin(home, "repo-ignore")+"\n")
			writeFile(t, home, "repo-ignore", "excluded.txt\n")
			return top
		}},
		{"core.excludesFile relative to the worktree", func(t *testing.T, home, top string) string {
			writeFile(t, home, "gitconfig", "[core]\n\texcludesFile = rules/ignore\n")
			writeFile(t, top, "rules/ignore", "excluded.txt\n")
			return top
		}},
		{"default excludes file without XDG_CONFIG_HOME", func(t *testing.T, home, top string) string {
			t.Setenv("XDG_CONFIG_HOME", "")
			writeFile(t, home, ".config/git/ignore", "excluded.txt\n")
			return top
		}},
		{"core.excludesFile in ~/.config/git/config without XDG_CONFIG_HOME", func(t *testing.T, home, top string) string {
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("GIT_CONFIG_GLOBAL", "")
			writeFile(t, home, ".config/git/config", "[core]\n\texcludesFile = ~/global-ignore\n")
			writeFile(t, home, "global-ignore", "excluded.txt\n")
			return top
		}},
		{"core.excludesFile replaces the default file", func(t *testing.T, home, top string) string {
			writeFile(t, home, "xdg/git/ignore", "kept.txt\n")
			writeFile(t, home, "gitconfig", "[core]\n\texcludesFile = ~/global-ignore\n")
			writeFile(t, home, "global-ignore", "excluded.txt\n")
			return top
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := isolateGitConfig(t)
			dir := tt.setup(t, home, t.TempDir())
			writeFile(t, dir, "excluded.txt", "x")
			writeFile(t, dir, "kept.txt", "k")

			s := openTestStore(t)
			root := stage(t, openWorktree(t, dir), s)
			names := treeNames(t, s, root)
			if slices.Contains(names, "excluded.txt") || !slices.Contains(names, "kept.txt") {
				t.Errorf("root = %q, want kept.txt without excluded.txt", names)
			}
		})
	}
}

// slashJoin joins path elements with forward slashes, which Git accepts on
// every platform and which a configuration value does not treat as escapes.
func slashJoin(elem ...string) string {
	return filepath.ToSlash(filepath.Join(elem...))
}

func TestStage_CoreExcludesFileMatchesGit(t *testing.T) {
	requireGit(t)
	dir, w, s := openTestWorktree(t)
	home := os.Getenv("HOME")
	writeFile(t, home, "gitconfig", "[core]\n\texcludesFile = ~/global-ignore\n")
	writeFile(t, home, "global-ignore", "*.log\n!keep.log\nbuild/\n")
	// core.excludesFile replaces the default file, so this pattern is unused.
	writeFile(t, home, "xdg/git/ignore", "main.go\n")
	for _, name := range []string{"main.go", "a.log", "keep.log", "build/out", "sub/b.log"} {
		writeFile(t, dir, name, "content of "+name+"\n")
	}

	want := gitWriteTree(t, dir)
	if got := stage(t, w, s).String(); got != want {
		t.Errorf("tree = %s, git writes %s", got, want)
	}
}

func TestOpenWorktree_SkipLeavesADirectoryOutOfStageAndCheckout(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, "a.txt", "v1")
	writeFile(t, dir, "data/state.db", "old")
	before := stage(t, w, s) // recorded without the skip, so it holds data/

	w = openWorktree(t, dir, filepath.Join(dir, "data"))
	root := stage(t, w, s)
	if got, want := treeNames(t, s, root), []string{"a.txt"}; !slices.Equal(got, want) {
		t.Errorf("root = %q, want %q", got, want)
	}

	writeFile(t, dir, "a.txt", "v2")
	writeFile(t, dir, "data/state.db", "new")
	writeFile(t, dir, "data/extra", "e")
	checkout(t, w, s, before)
	wantContent(t, dir, "a.txt", "v1")
	wantContent(t, dir, "data/state.db", "new")
	wantContent(t, dir, "data/extra", "e")
}

func TestOpenWorktree_SkipIgnoresWhatIsNotInsideTheWorktree(t *testing.T) {
	dir, _, s := openTestWorktree(t)
	writeFile(t, dir, "a.txt", "a")
	writeFile(t, dir, "sub/b.txt", "b")
	w := openWorktree(t, dir, "", dir, filepath.Dir(dir), filepath.Join(filepath.Dir(dir), "elsewhere"))
	if got, want := treeNames(t, s, stage(t, w, s)), []string{"a.txt", "sub"}; !slices.Equal(got, want) {
		t.Errorf("root = %q, want %q", got, want)
	}
}

func TestOpenWorktree_SkipResolvesSymbolicLinks(t *testing.T) {
	dir, _, s := openTestWorktree(t)
	writeFile(t, dir, "a.txt", "a")
	writeFile(t, dir, "data/x", "x")
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	w := openWorktree(t, dir, filepath.Join(link, "data"))
	if got, want := treeNames(t, s, stage(t, w, s)), []string{"a.txt"}; !slices.Equal(got, want) {
		t.Errorf("root = %q, want %q", got, want)
	}
}

func TestWorktreeSkipped(t *testing.T) {
	dir := t.TempDir()
	w := openWorktree(t, dir, filepath.Join(dir, "data"), filepath.Join(dir, "a", "b"))
	tests := map[string]bool{
		"data":     true,
		"data/x":   true,
		"data/x/y": true,
		"a/b":      true,
		"a/b/c":    true,
		"database": false,
		"data.txt": false,
		"dat":      false,
		"a":        false,
		"a/bc":     false,
		"b":        false,
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			if got := w.Skipped(name); got != want {
				t.Errorf("Skipped(%q) = %t, want %t", name, got, want)
			}
		})
	}
}

func TestModeOf(t *testing.T) {
	tests := []struct {
		name string
		mode fs.FileMode
		want Mode
		ok   bool
	}{
		{"regular file", 0o644, ModeFile, true},
		{"read-only file", 0o444, ModeFile, true},
		{"owner executable", 0o744, ModeExec, true},
		{"owner-only executable", 0o700, ModeExec, true},
		{"group and others executable only", 0o655, ModeFile, true},
		{"setuid executable", fs.ModeSetuid | 0o755, ModeExec, true},
		{"symbolic link", fs.ModeSymlink | 0o777, ModeSymlink, true},
		{"directory", fs.ModeDir | 0o755, 0, false},
		{"named pipe", fs.ModeNamedPipe | 0o644, 0, false},
		{"socket", fs.ModeSocket | 0o755, 0, false},
		{"device", fs.ModeDevice | 0o644, 0, false},
		{"character device", fs.ModeDevice | fs.ModeCharDevice | 0o644, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ModeOf(tt.mode)
			if got != tt.want || ok != tt.ok {
				t.Errorf("ModeOf(%v) = %o, %t; want %o, %t", tt.mode, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestStage_RereadsFilesChangedRightAfterBeingRead(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	name := writeFile(t, dir, "a.txt", "aaaa")
	info, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	first := stage(t, w, s)

	// Same size and modification time, as when a write lands in the same
	// timestamp tick as the previous read.
	writeFile(t, dir, "a.txt", "bbbb")
	if err := os.Chtimes(name, time.Time{}, info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if second := stage(t, w, s); second == first {
		t.Error("a file changed within the racy window was not read again")
	}
}

// settle makes w read the clock an hour ahead, so that every file looks as
// if it last changed well outside RacyWindow.
func settle(w *Worktree) {
	w.now = func() time.Time { return time.Now().Add(time.Hour) }
}

func TestStage_ReusesEntriesOfSettledFiles(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, "a.txt", "aaaa")
	settle(w)
	stage(t, w, s)
	var first []IndexEntry
	inTx(t, s, func(ctx context.Context, tx *Tx) { first = indexEntries(t, tx) })

	stage(t, w, s)
	inTx(t, s, func(ctx context.Context, tx *Tx) {
		// A file read again would be recorded with a new Checked time.
		if got := indexEntries(t, tx); len(got) != 1 || !sameEntry(got[0], first[0]) {
			t.Errorf("index = %+v, want %+v unchanged", got, first)
		}
	})
}

// TestStage_DetectsRewritesThatKeepSizeAndModTime pins what the inode change
// time adds: a rewrite that restores the size and modification time, as cp -p
// or touch -r can, is still read again.
func TestStage_DetectsRewritesThatKeepSizeAndModTime(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	name := writeFile(t, dir, "a.txt", "aaaa")
	info, err := os.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	if ctime, _ := sysStat(info); ctime.IsZero() {
		t.Skip("the platform reports no inode change time")
	}
	settle(w)
	first := stage(t, w, s)

	writeFile(t, dir, "a.txt", "bbbb")
	if err := os.Chtimes(name, time.Time{}, info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if second := stage(t, w, s); second == first {
		t.Error("a rewrite that kept the size and modification time went unseen")
	}
}

func TestUnchanged(t *testing.T) {
	then := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	e := IndexEntry{
		Path: "a.txt", Mode: ModeFile, Size: 4,
		ModTime: then, ChangeTime: then, Inode: 7,
		Checked: then.Add(time.Minute),
	}
	tests := []struct {
		name   string
		change func(e, f *IndexEntry)
		want   bool
	}{
		{"same file", func(e, f *IndexEntry) {}, true},
		{"mode", func(e, f *IndexEntry) { f.Mode = ModeExec }, false},
		{"size", func(e, f *IndexEntry) { f.Size = 5 }, false},
		{"modification time", func(e, f *IndexEntry) { f.ModTime = then.Add(time.Nanosecond) }, false},
		{"inode change time", func(e, f *IndexEntry) { f.ChangeTime = then.Add(time.Nanosecond) }, false},
		{"inode", func(e, f *IndexEntry) { f.Inode = 8 }, false},
		{"read right after a write", func(e, f *IndexEntry) { e.Checked = then.Add(time.Second) }, false},
		{"read right after a chmod", func(e, f *IndexEntry) {
			e.ChangeTime = e.Checked.Add(-time.Second)
			f.ChangeTime = e.ChangeTime
		}, false},
		{"no change time or inode reported", func(e, f *IndexEntry) {
			e.ChangeTime, e.Inode = time.Time{}, 0
			f.ChangeTime, f.Inode = time.Time{}, 0
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old, now := e, e
			tt.change(&old, &now)
			if got := unchanged(old, now); got != tt.want {
				t.Errorf("unchanged = %t, want %t", got, tt.want)
			}
		})
	}
}

// captureLogs sends the default logger's output to the returned buffer until
// the test ends.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var logs bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &logs
}

// TestStage_SkipsUnreadableFiles checks that a file Stage may not read is
// left out with a warning, rather than failing every snapshot, and is then
// untracked.
func TestStage_SkipsUnreadableFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads files regardless of permissions")
	}
	if onWindows {
		t.Skip("Windows reads a file of mode 0, which it only makes read-only")
	}
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, "ok.txt", "ok")
	secret := writeFile(t, dir, "secret.txt", "s")
	stage(t, w, s) // indexes secret.txt while it is readable
	if err := os.Chmod(secret, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(secret, 0o600) })
	logs := captureLogs(t)

	root := stage(t, w, s)
	if got, want := treeNames(t, s, root), []string{"ok.txt"}; !slices.Equal(got, want) {
		t.Errorf("root = %q, want %q", got, want)
	}
	if !strings.Contains(logs.String(), "secret.txt") {
		t.Errorf("no warning names the unreadable file; log:\n%s", logs.String())
	}
	inTx(t, s, func(ctx context.Context, tx *Tx) {
		for _, e := range indexEntries(t, tx) {
			if e.Path == "secret.txt" {
				t.Errorf("index still holds the unreadable file: %+v", e)
			}
		}
	})

	// The skipped file is untracked, so a checkout leaves it alone.
	writeFile(t, dir, "ok.txt", "changed")
	checkout(t, w, s, root)
	wantContent(t, dir, "ok.txt", "ok")
	if info, err := os.Lstat(secret); err != nil || info.Mode().Perm() != 0 || info.Size() != 1 {
		t.Errorf("checkout touched the unreadable file: %v, %v", info, err)
	}
}

// TestStage_SkipsFilesOverTheSizeLimit uses a sparse file, so the test
// neither writes nor reads a gigabyte.
func TestStage_SkipsFilesOverTheSizeLimit(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, "small.txt", "s")
	huge := filepath.Join(dir, "huge.bin")
	f, err := os.Create(huge)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(f.Truncate(MaxFileSize+1), f.Close()); err != nil {
		t.Fatal(err)
	}
	logs := captureLogs(t)

	root := stage(t, w, s)
	if got, want := treeNames(t, s, root), []string{"small.txt"}; !slices.Equal(got, want) {
		t.Errorf("root = %q, want %q", got, want)
	}
	if !strings.Contains(logs.String(), "huge.bin") {
		t.Errorf("no warning names the skipped file; log:\n%s", logs.String())
	}

	// The skipped file is untracked, so a checkout leaves it alone.
	writeFile(t, dir, "small.txt", "changed")
	checkout(t, w, s, root)
	wantContent(t, dir, "small.txt", "s")
	if info, err := os.Stat(huge); err != nil || info.Size() != MaxFileSize+1 {
		t.Errorf("checkout touched the skipped file: %v, %v", info, err)
	}
}

func TestStage_RecordsSymlinksWithoutFollowingThem(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, "target/file.txt", "t")
	if err := os.Symlink("target", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	root := stage(t, w, s)
	inTx(t, s, func(ctx context.Context, tx *Tx) {
		entries, err := tx.Tree(ctx, root)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(entries, func(e TreeEntry) bool { return e.Name == "link" })
		if i < 0 || entries[i].Mode != ModeSymlink {
			t.Fatalf("link = %+v, want a symlink entry", entries)
		}
		target, err := tx.Blob(ctx, entries[i].Hash)
		if err != nil || string(target) != "target" {
			t.Errorf("link blob = %q, %v; want the target path", target, err)
		}
	})
}
