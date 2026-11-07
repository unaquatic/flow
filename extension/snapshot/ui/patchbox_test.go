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
