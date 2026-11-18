package ui

import (
	"slices"
	"testing"
)

// TestSplitPatch checks that a Git unified diff splits into the files it
// changes, in its order, each with its path and its hunks without Git's
// headers, and with the lines its hunks add and delete counted: a header
// line such as "--- a/x" counts as no deletion, while a deleted line that
// starts with "--" does. A deleted file keeps its path, a renamed one takes
// its new path, and a binary one shows Git's line that it differs.
func TestSplitPatch(t *testing.T) {
	patch := "diff --git a/calc.go b/calc.go\n" +
		"index ec677425..8586c259 100644\n" +
		"--- a/calc.go\n" +
		"+++ b/calc.go\n" +
		"@@ -1,2 +1,3 @@\n" +
		" package calc\n" +
		"+\n" +
		"+import \"errors\"\n" +
		"diff --git a/old.go b/old.go\n" +
		"deleted file mode 100644\n" +
		"index 1111111..0000000\n" +
		"--- a/old.go\n" +
		"+++ /dev/null\n" +
		"@@ -1,2 +0,0 @@\n" +
		"-package calc\n" +
		"--- a dashed line\n" +
		"diff --git a/a.go b/b.go\n" +
		"similarity index 100%\n" +
		"rename from a.go\n" +
		"rename to b.go\n" +
		"diff --git a/logo.png b/logo.png\n" +
		"index 2222222..3333333 100644\n" +
		"Binary files a/logo.png and b/logo.png differ\n"
	want := []FilePatch{
		{Path: "calc.go", Hunks: "@@ -1,2 +1,3 @@\n package calc\n+\n+import \"errors\"", Additions: 2},
		{Path: "old.go", Hunks: "@@ -1,2 +0,0 @@\n-package calc\n--- a dashed line", Deletions: 2},
		{Path: "b.go"},
		{Path: "logo.png", Hunks: "Binary files a/logo.png and b/logo.png differ"},
	}
	if got := SplitPatch(patch); !slices.Equal(got, want) {
		t.Errorf("SplitPatch =\n%+v\nwant\n%+v", got, want)
	}
	if got, want := ParseDiffStat(patch), (DiffStat{Files: 4, Additions: 2, Deletions: 2}); got != want {
		t.Errorf("ParseDiffStat = %+v, want %+v", got, want)
	}
}

// TestSideBySide checks that kept lines show on both sides, that a run of
// deleted lines shows beside the added lines after it, the longer run's
// extra lines beside blank sides, and that lines are numbered from each
// hunk's header.
func TestSideBySide(t *testing.T) {
	hunks := "@@ -3,4 +3,5 @@\n keep\n-old a\n-old b\n+new a\n+new b\n+new c\n tail\n\\ No newline at end of file"
	got := SideBySide(hunks)
	want := []DiffRow{
		{Note: "@@ -3,4 +3,5 @@"},
		{Old: DiffLine{3, "keep", LineContext}, New: DiffLine{3, "keep", LineContext}},
		{Old: DiffLine{4, "old a", LineDeleted}, New: DiffLine{4, "new a", LineAdded}},
		{Old: DiffLine{5, "old b", LineDeleted}, New: DiffLine{5, "new b", LineAdded}},
		{New: DiffLine{6, "new c", LineAdded}},
		{Old: DiffLine{6, "tail", LineContext}, New: DiffLine{7, "tail", LineContext}},
	}
	if !slices.Equal(got, want) {
		t.Errorf("SideBySide =\n%+v\nwant\n%+v", got, want)
	}
	if old, new := HunkStarts("@@ -0,0 +1,2 @@"); old != 1 || new != 1 {
		t.Errorf("HunkStarts of a new file = %d, %d; want 1, 1", old, new)
	}
}
