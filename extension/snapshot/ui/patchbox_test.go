package ui

import (
	"github.com/unaquatic/flow/terminal/ui"
	"github.com/unaquatic/fluid/widget"
	"reflect"
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
		{Old: DiffLine{Number: 3, Text: "keep", Kind: LineContext}, New: DiffLine{Number: 3, Text: "keep", Kind: LineContext}},
		{Old: DiffLine{Number: 4, Text: "old a", Kind: LineDeleted}, New: DiffLine{Number: 4, Text: "new a", Kind: LineAdded}},
		{Old: DiffLine{Number: 5, Text: "old b", Kind: LineDeleted}, New: DiffLine{Number: 5, Text: "new b", Kind: LineAdded}},
		{New: DiffLine{Number: 6, Text: "new c", Kind: LineAdded}},
		{Old: DiffLine{Number: 6, Text: "tail", Kind: LineContext}, New: DiffLine{Number: 7, Text: "tail", Kind: LineContext}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SideBySide =\n%+v\nwant\n%+v", got, want)
	}
	if old, new := HunkStarts("@@ -0,0 +1,2 @@"); old != 1 || new != 1 {
		t.Errorf("HunkStarts of a new file = %d, %d; want 1, 1", old, new)
	}
}

// TestHighlight checks that each side of a diff is colored as code of its
// file's language, a string that spans lines keeping its color on its side
// alone, and that a file of a language not known stays plain.
func TestHighlight(t *testing.T) {
	rows := SideBySide("@@ -1,2 +1,2 @@\n-x := `a\n-b`\n+y := 1\n+z := 2")
	h := widget.NewHighlighter(ui.DefaultTheme())
	fg := ui.DefaultTheme().TextBase
	Highlight(rows, "calc.go", h, fg)
	if rows[1].Old.Code == nil || rows[1].New.Code == nil {
		t.Fatalf("rows = %+v, want both sides highlighted", rows)
	}
	// The raw string the old side opens on its first line colors its second
	// whole; the new side's second line is not in it.
	second := rows[2].Old.Code
	if len(second) != 1 || second[0].Fg == fg {
		t.Errorf("old second line = %+v, want it all in the string's color", second)
	}
	if n := rows[2].New.Code; len(n) == 0 || n[0].Fg == second[0].Fg {
		t.Errorf("new second line = %+v, want it out of the old side's string", n)
	}
	plain := SideBySide("@@ -1 +1 @@\n-a\n+b")
	Highlight(plain, "notes.unknownext", h, fg)
	if plain[1].Old.Code != nil {
		t.Errorf("unknown language highlighted: %+v", plain[1].Old.Code)
	}
}
