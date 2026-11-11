package engine

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// chmod sets the permissions of name in dir.
func chmod(t *testing.T, dir, name string, perm os.FileMode) {
	t.Helper()
	if err := os.Chmod(filepath.Join(dir, name), perm); err != nil {
		t.Fatal(err)
	}
}

// symlink replaces name in dir with a symbolic link to target.
func symlink(t *testing.T, dir, target, name string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, p); err != nil {
		t.Fatal(err)
	}
}

// remove deletes name, and everything below it, from dir.
func remove(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.RemoveAll(filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
}

// changes returns the changes between from and to.
func changes(t *testing.T, s *Store, from, to Hash) []Change {
	t.Helper()
	var got []Change
	for c, err := range s.Changes(t.Context(), from, to) {
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, c)
	}
	return got
}

func TestStoreChanges_ListsChangedFilesInPathOrder(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, "a.txt", "a1")
	writeFile(t, dir, "same/deep/file.txt", "unchanged")
	writeFile(t, dir, "dir/x", "x")
	writeFile(t, dir, "swap/inner", "i")
	writeFile(t, dir, "mode.sh", "m")
	from := stage(t, w, s)

	writeFile(t, dir, "a.txt", "a2")
	remove(t, dir, "dir/x")
	writeFile(t, dir, "dir/y", "y")
	remove(t, dir, "swap")
	writeFile(t, dir, "swap", "now a file")
	chmod(t, dir, "mode.sh", 0o755)
	writeFile(t, dir, "new.txt", "n")
	to := stage(t, w, s)

	type summary struct {
		path             string
		oldMode, newMode Mode
	}
	var got []summary
	for _, c := range changes(t, s, from, to) {
		got = append(got, summary{c.Path, c.Old.Mode, c.New.Mode})
	}
	want := []summary{
		{"a.txt", ModeFile, ModeFile},
		{"dir/x", ModeFile, 0},
		{"dir/y", 0, ModeFile},
		{"mode.sh", ModeFile, ModeExec},
		{"new.txt", 0, ModeFile},
		{"swap", 0, ModeFile},
		{"swap/inner", ModeFile, 0},
	}
	if onWindows {
		// chmod sets no executable bit to change mode.sh's mode.
		want = slices.DeleteFunc(want, func(s summary) bool { return s.path == "mode.sh" })
	}
	if !slices.Equal(got, want) {
		t.Errorf("changes =\n%v\nwant\n%v", got, want)
	}
	if back := changes(t, s, to, to); len(back) != 0 {
		t.Errorf("a tree differs from itself: %v", back)
	}
	for c, err := range s.Changes(t.Context(), from, to) {
		if err != nil || c.Path != "a.txt" {
			t.Errorf("first change = %+v, %v; want a.txt", c, err)
		}
		break
	}
}

func TestStoreFileDiffs_ReportsStatusAndLineCounts(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, "edited.txt", "one\ntwo\n")
	writeFile(t, dir, "removed.txt", "gone\n")
	from := stage(t, w, s)

	writeFile(t, dir, "edited.txt", "one\ntwo\nthree\n")
	remove(t, dir, "removed.txt")
	writeFile(t, dir, "created.txt", "brand new\n")
	to := stage(t, w, s)

	diffs, err := s.FileDiffs(t.Context(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	want := []FileDiff{
		{Path: "created.txt", Status: Added, Additions: 1},
		{Path: "edited.txt", Status: Modified, Additions: 1},
		{Path: "removed.txt", Status: Deleted, Deletions: 1},
	}
	if !slices.Equal(diffs, want) {
		t.Errorf("diffs =\n%+v\nwant\n%+v", diffs, want)
	}
}

func TestStoreFileDiffs_ReportsBinaryWithoutCounts(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, "blob.bin", "\x00\x01\x02")
	from := stage(t, w, s)
	writeFile(t, dir, "blob.bin", "\x00\xff\x02\x03")
	to := stage(t, w, s)

	diffs, err := s.FileDiffs(t.Context(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	want := []FileDiff{{Path: "blob.bin", Status: Modified}}
	if !slices.Equal(diffs, want) {
		t.Errorf("diffs = %+v, want %+v", diffs, want)
	}
}

func TestStoreFileDiffs_ReportsModeAndLinkChanges(t *testing.T) {
	dir, w, s := openTestWorktree(t)
	writeFile(t, dir, "run.sh", "#!/bin/sh\n")
	symlink(t, dir, "one", "link")
	from := stage(t, w, s)
	chmod(t, dir, "run.sh", 0o755)
	symlink(t, dir, "two", "link")
	to := stage(t, w, s)

	diffs, err := s.FileDiffs(t.Context(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	want := []FileDiff{
		{Path: "link", Status: Modified, Additions: 1, Deletions: 1},
		{Path: "run.sh", Status: Modified},
	}
	if onWindows {
		// chmod sets no executable bit to change run.sh's mode.
		want = slices.DeleteFunc(want, func(d FileDiff) bool { return d.Path == "run.sh" })
	}
	if !slices.Equal(diffs, want) {
		t.Errorf("diffs =\n%+v\nwant\n%+v", diffs, want)
	}
}

func TestChangeStatus(t *testing.T) {
	file := File{Mode: ModeFile, Hash: HashObject(BlobType, []byte("a"))}
	exec := File{Mode: ModeExec, Hash: file.Hash}
	tests := []struct {
		change Change
		want   Status
		text   string
	}{
		{Change{Path: "a", New: file}, Added, "added"},
		{Change{Path: "a", Old: file}, Deleted, "deleted"},
		{Change{Path: "a", Old: file, New: exec}, Modified, "modified"},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			got := tt.change.Status()
			if got != tt.want || got.String() != tt.text {
				t.Errorf("Status() = %d %q, want %d %q", got, got.String(), tt.want, tt.text)
			}
		})
	}
	if got, want := Status(0).String(), "status 0"; got != want {
		t.Errorf("Status(0) = %q, want %q", got, want)
	}
}

func TestIsBinary(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want bool
	}{
		{"empty", nil, false},
		{"text", []byte("hello\n"), false},
		{"high bytes", []byte{0xff, 0xfe, 0x80}, false},
		{"NUL", []byte("a\x00b"), true},
		{"NUL as the 8000th byte", append(bytes.Repeat([]byte("a"), 7999), 0), true},
		{"NUL as the 8001st byte", append(bytes.Repeat([]byte("a"), 8000), 0), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsBinary(tt.data); got != tt.want {
				t.Errorf("IsBinary = %t, want %t", got, tt.want)
			}
		})
	}
}

// TestStorePatch_MatchesGitDiff compares Patch with git diff on the same two
// trees. The edits are ones where the anchored diff and Git's agree, in
// lines that give Git no function name for the hunk header.
func TestStorePatch_MatchesGitDiff(t *testing.T) {
	requireGit(t)
	dir, w, s := openTestWorktree(t)
	git := shadowGit(t, dir)
	stageBoth := func() Hash {
		t.Helper()
		h := stage(t, w, s)
		git("add", "-A", ".")
		if gh := git("write-tree"); gh != h.String() {
			t.Fatalf("tree = %s, git writes %s", h, gh)
		}
		return h
	}

	numbers := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n"
	// Git quotes these names in a patch, but Windows cannot name a file so.
	quoted := map[string]string{"tab\tname.txt": "t", `quote"back\slash`: "q"}
	if onWindows {
		clear(quoted)
	}
	files := map[string]string{
		"edit.txt":       numbers,
		"deleted.txt":    "gone\n",
		"empty-deleted":  "",
		"mode.sh":        "#!/bin/sh\n",
		"mode-edit.sh":   "a\n",
		"bin.dat":        "\x00one",
		"typechange":     "x\n",
		"space name.txt": "s1\n",
		"ünïcode.txt":    "u1\n",
		"noeol.txt":      "a",
		"swap/inner.txt": "i\n",
	}
	for name, prefix := range quoted {
		files[name] = prefix + "1\n"
	}
	for name, content := range files {
		writeFile(t, dir, name, content)
	}
	symlink(t, dir, "t1", "link")
	from := stageBoth()

	writeFile(t, dir, "edit.txt", strings.Replace(numbers, "9\n", "nine\n", 1))
	writeFile(t, dir, "added.txt", "new\n")
	writeFile(t, dir, "empty-new", "")
	remove(t, dir, "deleted.txt")
	remove(t, dir, "empty-deleted")
	chmod(t, dir, "mode.sh", 0o755)
	writeFile(t, dir, "mode-edit.sh", "b\n")
	chmod(t, dir, "mode-edit.sh", 0o755)
	writeFile(t, dir, "bin.dat", "\x00two")
	writeFile(t, dir, "bin-new.dat", "\x00new")
	symlink(t, dir, "t2", "link")
	symlink(t, dir, "x", "typechange")
	writeFile(t, dir, "space name.txt", "s2\n")
	for name, prefix := range quoted {
		writeFile(t, dir, name, prefix+"2\n")
	}
	writeFile(t, dir, "ünïcode.txt", "u2\n")
	writeFile(t, dir, "noeol.txt", "a\n")
	remove(t, dir, "swap")
	writeFile(t, dir, "swap", "file\n")
	to := stageBoth()

	want := git("diff", "--no-renames", "--no-color", "--no-ext-diff", "--abbrev="+strconv.Itoa(ShortHashLen), from.String(), to.String())
	got, err := s.Patch(t.Context(), from, to)
	if err != nil {
		t.Fatal(err)
	}
	if got = strings.TrimSpace(got); got != want {
		t.Errorf("patch differs from git diff\n--- got\n%s\n--- git\n%s", got, want)
	}
}

func TestQuotePath(t *testing.T) {
	tests := []struct {
		path, want string
	}{
		{"plain.txt", "a/plain.txt"},
		{"space name", "a/space name"},
		{"ünïcode", "a/ünïcode"},
		{"tab\tname", `"a/tab\tname"`},
		{"new\nline", `"a/new\nline"`},
		{`quote"d`, `"a/quote\"d"`},
		{`back\slash`, `"a/back\\slash"`},
		{"bell\a", `"a/bell\a"`},
		{"esc\x1b", `"a/esc\033"`},
		{"del\x7f", `"a/del\177"`},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := quotePath("a/", tt.path); got != tt.want {
				t.Errorf("quotePath(%q) = %s, want %s", tt.path, got, tt.want)
			}
		})
	}
}
