package ui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/unaquatic/flow/agent/message"
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
// changed without wording before them, titled by their own name and the
// short form of what they checked out, and say when they changed none: under that title when they
// checked something out, and on a line of its own otherwise.
func TestCompleteUndoRedo(t *testing.T) {
	const tree = "c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0"
	files := []app.FileDiff{{File: "/abs/x.go", Additions: 205, Status: "deleted"}}
	for _, tt := range []struct {
		name     string
		complete func(*Service, app.MoveResult, error) []UIEvent
		title    string
	}{
		{"undo", (*Service).CompleteUndo, "Undo [c1d2e3f4]"},
		{"redo", (*Service).CompleteRedo, "Redo [c1d2e3f4]"},
	} {
		complete := tt.complete
		t.Run(tt.name, func(t *testing.T) {
			s := &Service{state: &UIState{}}
			out := complete(s, app.MoveResult{Files: files, Checkout: tree}, nil)
			toast, ok := toastOf(out[0])
			if len(out) != 1 || !ok || len(toast.Lines) == 0 {
				t.Fatalf("events = %#v, want one file summary", out)
			}
			if got, want := toast.Lines[0].PlainText(), "1 file  +205 -0"; got != want {
				t.Errorf("summary = %q, want %q", got, want)
			}
			if got, want := toast.Title, tt.title; got != want {
				t.Errorf("title = %q, want %q", got, want)
			}
			want := []UIEvent{noticeAdded(Notice{Text: NoFileChangesText, Title: tt.title})}
			if got := complete(s, app.MoveResult{Checkout: tree}, nil); !sameEvents(got, want) {
				t.Errorf("no files = %#v, want %q under the title", got, NoFileChangesText)
			}
			if got := complete(s, app.MoveResult{}, nil); !sameEvents(got, []UIEvent{noticeAdded(Notice{Text: NoFileChangesText})}) {
				t.Errorf("nothing checked out = %#v, want %q", got, NoFileChangesText)
			}
		})
	}
}

// TestUndoPutsPromptBack checks that an undo puts the text of the latest
// prompt, which it moves back over, in the input once it succeeds, its text
// parts a blank line apart and without its files, passing over the reply and
// the notices after it; that a failed undo leaves the input as it is; and
// that an undo with no prompt to take back sets none.
func TestUndoPutsPromptBack(t *testing.T) {
	prompt := userText("u2", "fix the tests")
	prompt.Parts = append(prompt.Parts,
		message.FilePart{ID: "u2-file", MediaType: "text/plain", Filename: "a.txt"},
		message.TextPart{ID: "u2-more", Text: "and run them"})
	msgs := []ChatMessage{
		userText("u1", "first"), agentText("a1", "done"),
		prompt, agentText("a2", "fixed"),
		{ID: "n1", Notice: &Notice{Text: "a notice"}},
	}
	moved := app.MoveResult{}
	s := &Service{state: &UIState{Messages: msgs}}

	if out := s.HandleUndo(); !slices.Contains(out, UIEvent(EventUndoRequested{})) {
		t.Fatalf("/undo = %#v, want a move back", out)
	}
	want := EventInputSet{Text: "fix the tests\n\nand run them"}
	if out := s.CompleteUndo(moved, nil); !slices.Contains(out, UIEvent(want)) {
		t.Errorf("undo done = %#v, want the prompt back in the input, %q", out, want.Text)
	}

	s.HandleUndo()
	if out := s.CompleteUndo(moved, errors.New("session busy")); slices.ContainsFunc(out, isInputSet) {
		t.Errorf("failed undo = %#v, want the input left as it is", out)
	}

	s = &Service{state: &UIState{Messages: []ChatMessage{agentText("a1", "hello")}}}
	s.HandleUndo()
	if out := s.CompleteUndo(moved, nil); slices.ContainsFunc(out, isInputSet) {
		t.Errorf("undo without a prompt = %#v, want the input left as it is", out)
	}
}

// isInputSet reports whether e sets the input's text.
func isInputSet(e UIEvent) bool {
	_, ok := e.(EventInputSet)
	return ok
}

// TestMoveTitle checks that a history move's notice names the move and what
// it checked out by its first ShortRefLen characters, whole when shorter,
// and is titled ChangesTitle when it checked out nothing.
func TestMoveTitle(t *testing.T) {
	for _, tt := range []struct{ verb, ref, want string }{
		{CheckoutVerb, "c1d2e3f4a5b6c7d8", "Checkout [c1d2e3f4]"},
		{UndoVerb, "c1d2e3f4a5b6c7d8", "Undo [c1d2e3f4]"},
		{RedoVerb, "abc", "Redo [abc]"},
		{UndoVerb, "", ChangesTitle},
	} {
		if got := MoveTitle(tt.verb, tt.ref); got != tt.want {
			t.Errorf("MoveTitle(%q, %q) = %q, want %q", tt.verb, tt.ref, got, tt.want)
		}
	}
}
