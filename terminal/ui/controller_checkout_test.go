package ui

import (
	"strings"
	"testing"

	"github.com/unaquatic/flow/app"
)

func TestBuildDiffToastLines_SummaryAndPerFileStats(t *testing.T) {
	files := []app.FileDiff{
		{File: "/abs/apps/index.tsx", Additions: 3, Deletions: 1, Status: "modified"},
		{File: "/abs/lib/util.go", Additions: 12, Deletions: 4, Status: "added"},
	}
	lines := BuildDiffToastLines(files)
	if len(lines) != 3 {
		t.Fatalf("expected summary + 2 file lines, got %d", len(lines))
	}
	// The summary leads with the files, with no wording before them.
	if got, want := lines[0].PlainText(), "2 files  +15 -5"; got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
	first := lines[1].PlainText()
	if !strings.Contains(first, "+3") || !strings.Contains(first, "-1") {
		t.Fatalf("first file line missing per-file counts: %q", first)
	}
}

func TestBuildDiffToastLines_TruncatesLongList(t *testing.T) {
	var files []app.FileDiff
	for range 10 {
		files = append(files, app.FileDiff{
			File:      "/abs/file.go",
			Additions: 1,
			Deletions: 0,
		})
	}
	lines := BuildDiffToastLines(files)
	// One summary, six files, and one truncation notice.
	if len(lines) != 8 {
		t.Fatalf("expected 8 lines (summary + 6 files + truncation), got %d", len(lines))
	}
	last := lines[len(lines)-1].PlainText()
	if !strings.Contains(last, "[4 more]") {
		t.Fatalf("expected truncation hint, got %q", last)
	}
}

func TestBuildDiffToastLines_SingleFileSingularLabel(t *testing.T) {
	files := []app.FileDiff{
		{File: "/abs/x.go", Additions: 1, Deletions: 0, Status: "modified"},
	}
	lines := BuildDiffToastLines(files)
	if got, want := lines[0].PlainText(), "1 file  +1 -0"; got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}

// TestCompleteUndoRedo checks that undo and redo report the files they
// changed without wording before them, and say when they changed none.
func TestCompleteUndoRedo(t *testing.T) {
	files := []app.FileDiff{{File: "/abs/x.go", Additions: 205, Status: "deleted"}}
	for name, complete := range map[string]func(*Service, []app.FileDiff, error) []UIEvent{
		"undo": (*Service).CompleteUndo,
		"redo": (*Service).CompleteRedo,
	} {
		t.Run(name, func(t *testing.T) {
			s := &Service{state: &UIState{}}
			out := complete(s, files, nil)
			toast, ok := toastOf(out[0])
			if len(out) != 1 || !ok || len(toast.Lines) == 0 {
				t.Fatalf("events = %#v, want one file summary", out)
			}
			if got, want := toast.Lines[0].PlainText(), "1 file  +205 -0"; got != want {
				t.Errorf("summary = %q, want %q", got, want)
			}
			if got := complete(s, nil, nil); !sameEvents(got, []UIEvent{noticeAdded(Notice{Text: NoFileChangesText})}) {
				t.Errorf("no files = %#v, want %q", got, NoFileChangesText)
			}
		})
	}
}
