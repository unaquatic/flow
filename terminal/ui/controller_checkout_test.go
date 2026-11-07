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
// changed without wording before them, titled by the short form of what they
// checked out, and say when they changed none: under that title when they
// checked something out, and on a line of its own otherwise.
func TestCompleteUndoRedo(t *testing.T) {
	const tree = "c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0"
	files := []app.FileDiff{{File: "/abs/x.go", Additions: 205, Status: "deleted"}}
	for name, complete := range map[string]func(*Service, app.MoveResult, error) []UIEvent{
		"undo": (*Service).CompleteUndo,
		"redo": (*Service).CompleteRedo,
	} {
		t.Run(name, func(t *testing.T) {
			s := &Service{state: &UIState{}}
			out := complete(s, app.MoveResult{Files: files, Checkout: tree}, nil)
			toast, ok := toastOf(out[0])
			if len(out) != 1 || !ok || len(toast.Lines) == 0 {
				t.Fatalf("events = %#v, want one file summary", out)
			}
			if got, want := toast.Lines[0].PlainText(), "1 file  +205 -0"; got != want {
				t.Errorf("summary = %q, want %q", got, want)
			}
			if got, want := toast.Title, "Checkout [c1d2e3f4]"; got != want {
				t.Errorf("title = %q, want %q", got, want)
			}
			want := []UIEvent{noticeAdded(Notice{Text: NoFileChangesText, Title: "Checkout [c1d2e3f4]"})}
			if got := complete(s, app.MoveResult{Checkout: tree}, nil); !sameEvents(got, want) {
				t.Errorf("no files = %#v, want %q under the title", got, NoFileChangesText)
			}
			if got := complete(s, app.MoveResult{}, nil); !sameEvents(got, []UIEvent{noticeAdded(Notice{Text: NoFileChangesText})}) {
				t.Errorf("nothing checked out = %#v, want %q", got, NoFileChangesText)
			}
		})
	}
}

// TestCheckoutTitle checks that a history move's notice names what it
// checked out by its first ShortRefLen characters, whole when shorter, and
// is titled ChangesTitle when it checked out nothing.
func TestCheckoutTitle(t *testing.T) {
	for ref, want := range map[string]string{
		"c1d2e3f4a5b6c7d8": "Checkout [c1d2e3f4]",
		"abc":              "Checkout [abc]",
		"":                 ChangesTitle,
	} {
		if got := CheckoutTitle(ref); got != want {
			t.Errorf("CheckoutTitle(%q) = %q, want %q", ref, got, want)
		}
	}
}
