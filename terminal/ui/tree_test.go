package ui

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/extension/snapshot"
	"github.com/unaquatic/fluid/style"
)

// treeMsg returns a stored message continuing parent, holding text when it
// is not empty and then parts.
func treeMsg(id, parent message.ID, role message.Role, text string, parts ...message.ContentPart) message.Content {
	m := message.Content{ID: id, Role: role}
	if parent != "" {
		m.ParentID = &parent
	}
	if text != "" {
		m.Parts = append(m.Parts, message.TextPart{ID: message.PartID(id + "-text"), Text: text})
	}
	m.Parts = append(m.Parts, parts...)
	return m
}

// forkedTree returns a session that forks after its first reply, whose
// transcript ends at head:
//
//	u1 -> a1 -> u2 -> a2
//	         \-> u3 -> a3 (tools only) -> a4
func forkedTree(head message.ID) app.Tree {
	read := message.ToolPart{CallID: "c1", Name: "read", State: message.ToolStateCompleted{
		Input: json.RawMessage(`{"path":"calc_test.go"}`),
	}}
	edit := message.ToolPart{CallID: "c2", Name: "edit", State: message.ToolStateCompleted{
		Input: json.RawMessage(`{"path":"calc_test.go","old_string":"a","new_string":"b"}`),
	}}
	return app.Tree{Head: head, Messages: []message.Content{
		treeMsg("u1", "", message.RoleUser, "add a Divide method\nto the calculator"),
		treeMsg("a1", "u1", message.RoleAgent, "I'll add Divide with a zero check."),
		treeMsg("u2", "a1", message.RoleUser, "also add tests"),
		treeMsg("a2", "u2", message.RoleAgent, "Added TestDivide."),
		treeMsg("u3", "a1", message.RoleUser, "use table-driven tests instead"),
		treeMsg("a3", "u3", message.RoleAgent, "", read, edit),
		treeMsg("a4", "a3", message.RoleAgent, "Rewrote the tests as a table."),
	}}
}

// drawn returns the tree's rows as plain lines: the prefix, then the message
// ID, starred when the row is on the transcript.
func drawn(t *SessionTree) []string {
	var lines []string
	for _, row := range t.Rows {
		star := ""
		if row.Active {
			star = "*"
		}
		lines = append(lines, row.Prefix+string(row.Entry.ID)+star)
	}
	return lines
}

// selectID selects the row of id or fails the test.
func selectID(tb testing.TB, t *SessionTree, id message.ID) {
	tb.Helper()
	i, ok := t.RowIndex[id]
	if !ok {
		tb.Fatalf("%s has no row", id)
	}
	t.Select(i)
}

// selectedID returns the selected row's message.
func selectedID(t *SessionTree) message.ID {
	row, _ := t.SelectedRow()
	return row.Entry.ID
}

func TestSessionTreeLayout(t *testing.T) {
	tests := []struct {
		name string
		tree app.Tree
		want []string
	}{
		{
			name: "fork with the transcript on the first branch",
			tree: forkedTree("a2"),
			want: []string{
				"u1*",
				"a1*",
				"├─ u2*",
				"│  a2*",
				"└─ u3",
				"   a3",
				"   a3",
				"   a4",
			},
		},
		// The branch holding the transcript comes first, whatever its age.
		{
			name: "fork with the transcript on the second branch",
			tree: forkedTree("a4"),
			want: []string{
				"u1*",
				"a1*",
				"├─ u3*",
				"│  a3*",
				"│  a3*",
				"│  a4*",
				"└─ u2",
				"   a2",
			},
		},
		{
			name: "transcript moved back to the fork",
			tree: forkedTree("a1"),
			want: []string{
				"u1*",
				"a1*",
				"├─ u2",
				"│  a2",
				"└─ u3",
				"   a3",
				"   a3",
				"   a4",
			},
		},
		// Undoing past the first prompt and sending another starts a second
		// root. The roots are the branches of a fork above them all.
		{
			name: "many roots",
			tree: app.Tree{Head: "b1", Messages: []message.Content{
				treeMsg("r1", "", message.RoleUser, "first"),
				treeMsg("a1", "r1", message.RoleAgent, "one"),
				treeMsg("r2", "", message.RoleUser, "second"),
				treeMsg("b1", "r2", message.RoleAgent, "two"),
			}},
			want: []string{
				"├─ r2*",
				"│  b1*",
				"└─ r1",
				"   a1",
			},
		},
		{
			name: "nested fork",
			tree: app.Tree{Head: "c2", Messages: []message.Content{
				treeMsg("u1", "", message.RoleUser, "a"),
				treeMsg("a1", "u1", message.RoleAgent, "b"),
				treeMsg("u2", "a1", message.RoleUser, "c"),
				treeMsg("a2", "u2", message.RoleAgent, "d"),
				treeMsg("c1", "a2", message.RoleUser, "e"),
				treeMsg("c2", "a2", message.RoleUser, "f"),
				treeMsg("u3", "a1", message.RoleUser, "g"),
			}},
			want: []string{
				"u1*",
				"a1*",
				"├─ u2*",
				"│  a2*",
				"│  ├─ c2*",
				"│  └─ c1",
				"└─ u3",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := NewSessionTree(tt.tree)
			if got := drawn(st); !slices.Equal(got, tt.want) {
				t.Errorf("rows:\n%q\nwant:\n%q", got, tt.want)
			}
			if got := selectedID(st); got != tt.tree.Head {
				t.Errorf("selected %s, want the head %s", got, tt.tree.Head)
			}
		})
	}
}

func TestSessionTreeNarrowing(t *testing.T) {
	tests := []struct {
		name     string
		head     message.ID
		filter   TreeFilter
		query    string
		want     []string
		selected message.ID
		// status is empty while the rows fit and no filter is on.
		status string
	}{
		// A listed message hangs under its nearest listed ancestor. A search
		// that hides the selection and the messages above it selects the
		// first match.
		{name: "search", head: "a2", query: "TABLE", want: []string{"u3", "a4"}, selected: "u3"},
		{name: "search every word", head: "a2", query: "tests table", want: []string{"u3", "a4"}, selected: "u3"},
		{name: "search tool calls", head: "a2", query: "calc_test", want: []string{"a3", "a3"}, selected: "a3"},
		// With their common ancestors unlisted, the matches are roots.
		{name: "search roles", head: "a2", query: "user tests", want: []string{"├─ u2*", "└─ u3"}, selected: "u2"},
		{name: "no match", head: "a2", query: "zzz", status: NoMatchesText},
		{
			name: "no tools", head: "a2", filter: TreeFilterNoTools,
			want:     []string{"u1*", "a1*", "├─ u2*", "│  a2*", "└─ u3", "   a4"},
			selected: "a2", status: "[no-tools]",
		},
		{
			name: "no tools keeps the head", head: "a3", filter: TreeFilterNoTools,
			want:     []string{"u1*", "a1*", "├─ u3*", "│  a3*", "│  a3*", "│  a4", "└─ u2", "   a2"},
			selected: "a3", status: "[no-tools]",
		},
		{
			name: "user only", head: "a2", filter: TreeFilterUserOnly,
			want:     []string{"u1*", "├─ u2*", "└─ u3"},
			selected: "u2", status: "[user]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := NewSessionTree(forkedTree(tt.head))
			st.Filter = tt.filter
			st.Search(tt.query)
			if got := drawn(st); !slices.Equal(got, tt.want) {
				t.Errorf("rows:\n%q\nwant:\n%q", got, tt.want)
			}
			if got := selectedID(st); got != tt.selected {
				t.Errorf("selected %q, want %q", got, tt.selected)
			}
			if got := st.View(treeMaxRows).Status; got != tt.status {
				t.Errorf("status = %q, want %q", got, tt.status)
			}
		})
	}
}

// TestSessionTreeEmptyTranscript checks that a session moved back past its
// first prompt, whose transcript is empty, selects the first row and marks
// none as on the transcript.
func TestSessionTreeEmptyTranscript(t *testing.T) {
	tree := forkedTree("")
	st := NewSessionTree(tree)
	if got := selectedID(st); got != "u1" {
		t.Errorf("selected %s, want the first row, u1", got)
	}
	if slices.ContainsFunc(st.Rows, func(r TreeRow) bool { return r.Active }) {
		t.Error("a row is marked as on an empty transcript")
	}
}

func TestSessionTreeKeepsSelection(t *testing.T) {
	st := NewSessionTree(forkedTree("a2"))
	selectID(t, st, "u3")

	st.Search("tests")
	if got := selectedID(st); got != "u3" {
		t.Fatalf("search moved the selection to %s, want u3", got)
	}
	st.Search("zzz")
	if _, ok := st.SelectedRow(); ok {
		t.Fatal("a search listing nothing selects a row")
	}
	st.Search("")
	if got := selectedID(st); got != "u3" {
		t.Fatalf("clearing the search selected %s, want u3", got)
	}

	// A filter that hides the selection selects its nearest listed ancestor.
	selectID(t, st, "a3")
	st.CycleFilter()
	if got := selectedID(st); got != "u3" {
		t.Errorf("the no-tools filter selected %s, want u3", got)
	}
}

func TestSessionTreeNavigation(t *testing.T) {
	tests := []struct {
		name string
		from message.ID
		move func(*SessionTree)
		want message.ID
	}{
		{name: "down", from: "a1", move: func(t *SessionTree) { t.Move(1) }, want: "u2"},
		{name: "up wraps", from: "u1", move: func(t *SessionTree) { t.Move(-1) }, want: "a4"},
		{name: "down wraps", from: "a4", move: func(t *SessionTree) { t.Move(1) }, want: "u1"},
		{name: "page down stops at the end", from: "u1", move: func(t *SessionTree) { t.Page(1, treeMaxRows) }, want: "a4"},
		{name: "page up stops at the start", from: "a2", move: func(t *SessionTree) { t.Page(-1, treeMaxRows) }, want: "u1"},
		{name: "fork down to the first branch", from: "u1", move: func(t *SessionTree) { t.JumpFork(1) }, want: "u2"},
		{name: "fork down to the end", from: "u2", move: func(t *SessionTree) { t.JumpFork(1) }, want: "a2"},
		{name: "fork up to the branch start", from: "a4", move: func(t *SessionTree) { t.JumpFork(-1) }, want: "u3"},
		{name: "fork up from a branch start", from: "u3", move: func(t *SessionTree) { t.JumpFork(-1) }, want: "u1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := NewSessionTree(forkedTree("a2"))
			selectID(t, st, tt.from)
			tt.move(st)
			if got := selectedID(st); got != tt.want {
				t.Errorf("selected %s, want %s", got, tt.want)
			}
		})
	}
}

func TestSessionTreeWindow(t *testing.T) {
	msgs := make([]message.Content, 30)
	parent := message.ID("")
	for i := range msgs {
		id := message.ID(fmt.Sprintf("m%02d", i))
		msgs[i] = treeMsg(id, parent, message.RoleUser, string(id))
		parent = id
	}
	tests := []struct {
		selected, start, end int
	}{
		{selected: 2, start: 0, end: 12},
		{selected: 20, start: 14, end: 26},
		{selected: 29, start: 18, end: 30},
	}
	for _, tt := range tests {
		st := NewSessionTree(app.Tree{Messages: msgs})
		st.Select(tt.selected)
		start, end := st.Window(treeMaxRows)
		if start != tt.start || end != tt.end {
			t.Errorf("window around %d = [%d, %d), want [%d, %d)", tt.selected, start, end, tt.start, tt.end)
		}
		v := st.View(treeMaxRows)
		if v.Rows[v.Selected].Entry.ID != msgs[tt.selected].ID {
			t.Errorf("view around %d selects %s", tt.selected, v.Rows[v.Selected].Entry.ID)
		}
		// The rows do not all fit, so the title counts them.
		if want := fmt.Sprintf("[%d/30]", tt.selected+1); v.Count != want || v.Status != "" {
			t.Errorf("count around %d = %q, status %q, want %q and no status", tt.selected, v.Count, v.Status, want)
		}
	}
}

func TestTitleCount(t *testing.T) {
	tests := []struct {
		selected, total, shown int
		want                   string
	}{
		{selected: 0, total: 3, shown: 3, want: ""},
		{selected: 0, total: 0, shown: 0, want: ""},
		{selected: 2, total: 20, shown: 12, want: "[3/20]"},
		{selected: 19, total: 20, shown: 12, want: "[20/20]"},
	}
	for _, tt := range tests {
		if got := TitleCount(tt.selected, tt.total, tt.shown); got != tt.want {
			t.Errorf("TitleCount(%d, %d, %d) = %q, want %q", tt.selected, tt.total, tt.shown, got, tt.want)
		}
	}
}

func TestNewTreeEntry(t *testing.T) {
	bash := message.ToolPart{Name: "bash", State: message.ToolStatePending{
		Input: json.RawMessage(`{"command":"go test\n  ./...","description":"Run tests"}`),
	}}
	tests := []struct {
		name string
		msg  message.Content
		want TreeEntry
	}{
		{
			name: "prompt",
			msg:  treeMsg("u1", "", message.RoleUser, "  fix the\n\ttests  "),
			want: TreeEntry{ID: "u1", Role: message.RoleUser, Text: "fix the tests", Prompt: "  fix the\n\ttests  "},
		},
		{
			name: "reply with tools",
			msg:  treeMsg("a1", "u1", message.RoleAgent, "", bash, message.ToolPart{Name: "todo"}),
			want: TreeEntry{ID: "a1", Parent: "u1", Role: message.RoleAgent, Tools: []TreeTool{
				{Name: "bash", Input: "go test ./..."},
				{Name: "todo"},
			}},
		},
		// A long input is kept whole, like a message's text; the row cuts it
		// at the panel's edge.
		{
			name: "reply with a long tool input",
			msg: treeMsg("a3", "u1", message.RoleAgent, "", message.ToolPart{Name: "bash", State: message.ToolStatePending{
				Input: json.RawMessage(`{"command":"go test ./calculator/... -run 'TestDivide|TestDivideByZero' -count=1 -v"}`),
			}}),
			want: TreeEntry{ID: "a3", Parent: "u1", Role: message.RoleAgent, Tools: []TreeTool{
				{Name: "bash", Input: "go test ./calculator/... -run 'TestDivide|TestDivideByZero' -count=1 -v"},
			}},
		},
		{
			name: "reply with text skips reasoning",
			msg: treeMsg("a2", "u1", message.RoleAgent, "Done.",
				message.ReasoningPart{Text: "thinking"}),
			want: TreeEntry{ID: "a2", Parent: "u1", Role: message.RoleAgent, Text: "Done."},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NewTreeEntry(tt.msg); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("entry = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestTreeLinesDimOtherBranches checks that the transcript's rows keep their
// colors, "User" and "Agent" in those of their markers in the transcript,
// and other branches' rows are in TextWeak, the selected one too, which the
// insert line marks rather than its colors.
func TestTreeLinesDimOtherBranches(t *testing.T) {
	theme := DefaultTheme()
	st := NewSessionTree(forkedTree("a2"))
	selectID(t, st, "u3")
	lines := TreeLines(st.View(treeMaxRows), theme)

	// labelFg returns the color of the role label of the row of id, which
	// follows the cursor and the prefix, past the insert line above it.
	v := st.View(treeMaxRows)
	labelFg := func(id message.ID) style.Color {
		line := st.RowIndex[id]
		if line >= v.InsertLine() {
			line++
		}
		return lines[line].Chunks[2].Fg
	}
	tests := []struct {
		id   message.ID
		want style.Color
	}{
		{id: "u2", want: theme.ResolveTextBase()},
		{id: "a2", want: theme.Accent},
		{id: "u3", want: theme.TextWeak},
		{id: "a4", want: theme.TextWeak},
	}
	for _, tt := range tests {
		if got := labelFg(tt.id); got != tt.want {
			t.Errorf("label of %s drawn in %v, want %v", tt.id, got, tt.want)
		}
	}
}

// sameEvents reports whether got and want hold equal events in order. It
// compares them as printed, since some events hold slices.
func sameEvents(got, want []UIEvent) bool {
	return slices.EqualFunc(got, want, func(a, b UIEvent) bool { return eventString(a) == eventString(b) })
}

// eventString formats ev for sameEvents: a notice by what it shows, without
// the ID it takes.
func eventString(ev UIEvent) string {
	if n, ok := noticeOf(ev); ok {
		return fmt.Sprintf("notice %#v", n)
	}
	return fmt.Sprintf("%#v", ev)
}

// TestChooseTreeRow checks that choosing any row closes the tree and asks
// for a move to it, which for a prompt is a move before it that carries the
// prompt's whole text to the input, and that none fills the input at once.
// The app resolves the move when it runs.
func TestChooseTreeRow(t *testing.T) {
	edit := func(id message.ID, prompt string) EventTreeMoveRequested {
		return EventTreeMoveRequested{MessageID: id, Edit: true, Prompt: prompt}
	}
	tests := []struct {
		name         string
		head, choose message.ID
		want         EventTreeMoveRequested
	}{
		{name: "prompt on another branch", head: "a2", choose: "u3", want: edit("u3", "use table-driven tests instead")},
		{name: "prompt on the transcript", head: "a2", choose: "u2", want: edit("u2", "also add tests")},
		// The input gets the prompt as sent, line breaks and all.
		{name: "first prompt", head: "a2", choose: "u1", want: edit("u1", "add a Divide method\nto the calculator")},
		// A failed run leaves the transcript ending at its prompt.
		{name: "prompt the transcript ends at", head: "u3", choose: "u3", want: edit("u3", "use table-driven tests instead")},
		// A prompt sent from the transcript's end is moved before all the
		// same, since a run may have moved the end since the tree was read.
		{name: "prompt sent from the transcript's end", head: "a1", choose: "u3", want: edit("u3", "use table-driven tests instead")},
		{name: "reply", head: "a2", choose: "a4", want: EventTreeMoveRequested{MessageID: "a4"}},
		{name: "tool calls", head: "a2", choose: "a3", want: EventTreeMoveRequested{MessageID: "a3"}},
		{name: "the transcript's end", head: "a2", choose: "a2", want: EventTreeMoveRequested{MessageID: "a2"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Service{state: &UIState{}}
			s.OpenTree(forkedTree(tt.head))
			selectID(t, s.state.Tree, tt.choose)
			if got, want := s.ChooseTreeRow(), []UIEvent{EventBannerHide{}, tt.want}; !sameEvents(got, want) {
				t.Errorf("events = %#v, want %#v", got, want)
			}
			if s.state.Banner != NoBanner || s.state.Tree != nil {
				t.Error("choosing a row left the tree open")
			}
		})
	}
}

func TestShowTree(t *testing.T) {
	s, a := newTestService(t)
	s.NewSession()
	out := s.ShowTree()
	if toast, ok := toastOf(out[0]); len(out) != 1 || !ok || toast.Text != "No messages yet" {
		t.Fatalf("tree of a new session = %#v, want a notice", out)
	}

	for _, m := range []message.Content{
		treeMsg("u1", "", message.RoleUser, "hello"),
		treeMsg("a1", "", message.RoleAgent, "hi"),
	} {
		if err := a.Sessions().AppendMessage(t.Context(), s.SessionID(), &m); err != nil {
			t.Fatal(err)
		}
	}
	out = s.HandleSubmit("/tree")
	show, ok := out[len(out)-1].(EventTreeShow)
	if !ok {
		t.Fatalf("/tree = %#v, want the tree drawn last", out)
	}
	if got := drawn(s.state.Tree); !slices.Equal(got, []string{"u1*", "a1*"}) {
		t.Errorf("rows = %q, want the stored prompt and reply", got)
	}
	if len(show.Tree.Rows) != 2 || show.Tree.Status != "" || s.state.Banner != TreeBanner {
		t.Errorf("%d rows, status %q in banner %v; want 2 rows that fit, uncounted, in the tree banner",
			len(show.Tree.Rows), show.Tree.Status, s.state.Banner)
	}
}

func TestTreeSearchFromInput(t *testing.T) {
	s := &Service{state: &UIState{}}
	s.OpenTree(forkedTree("a2"))

	out := s.HandleInputChange("table")
	if len(out) != 1 {
		t.Fatalf("events = %#v, want one tree redraw", out)
	}
	show, ok := out[0].(EventTreeShow)
	if !ok {
		t.Fatalf("event = %#v, want EventTreeShow", out[0])
	}
	if n := len(show.Tree.Rows); n != 2 || show.Tree.Query != "table" {
		t.Errorf("search %q drew %d rows, want 2 for \"table\"", show.Tree.Query, n)
	}

	if out := s.CancelBanner(); !sameEvents(out, []UIEvent{EventBannerHide{}}) || s.state.Tree != nil {
		t.Errorf("cancel = %#v with tree %v, want the banner hidden and the tree gone", out, s.state.Tree)
	}
}

// TestCompleteTreeMove checks that a move before a prompt puts the prompt in
// the input and starts no run, that a move to a reply points to /continue,
// and that a failed move leaves the input alone.
func TestCompleteTreeMove(t *testing.T) {
	edit := EventTreeMoveRequested{MessageID: "u3", Edit: true, Prompt: "use tables\nplease"}
	reply := EventTreeMoveRequested{MessageID: "a4"}
	tests := []struct {
		name  string
		event EventTreeMoveRequested
		err   error
		want  []UIEvent
	}{
		{
			name: "prompt", event: edit,
			want: []UIEvent{
				noticeAdded(Notice{Text: NoFileChangesText}),
				EventInputSet{Text: "use tables\nplease"},
			},
		},
		{
			name: "reply", event: reply,
			want: []UIEvent{noticeAdded(Notice{Text: NoFileChangesText})},
		},
		{name: "reply the transcript ended at", event: reply, err: app.ErrAlreadyThere},
		// The prompt stays out of the input, which would send it from the
		// wrong place.
		{
			name: "failed", event: edit, err: snapshot.ErrDisabled,
			want: []UIEvent{noticeAdded(Notice{Text: "tree move failed: snapshots are disabled"})},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Service{state: &UIState{}}
			if got := s.CompleteTreeMove(tt.event, app.MoveResult{}, tt.err); !sameEvents(got, tt.want) {
				t.Errorf("events = %#v, want %#v", got, tt.want)
			}
			if s.Streaming() {
				t.Error("the move started a run")
			}
		})
	}
}

// TestHandleContinue checks that /continue starts a run whatever the
// transcript ends at, a prompt or a reply, and reports instead when a run is
// in progress or the transcript is empty.
func TestHandleContinue(t *testing.T) {
	// toast returns the text of the notice out shows, after any clearing of
	// the input.
	toast := func(out []UIEvent) string {
		for _, ev := range out {
			if e, ok := toastOf(ev); ok {
				return e.Text
			}
		}
		return fmt.Sprintf("no notice in %#v", out)
	}
	synctest.Test(t, func(t *testing.T) {
		s, a := newTestService(t)
		s.NewSession()
		if got := toast(s.HandleSubmit("/continue")); got != "Nothing to continue" {
			t.Errorf("/continue on a new session = %q, want a notice", got)
		}

		prompt := treeMsg("u1", "", message.RoleUser, "hello")
		if err := a.Sessions().AppendMessage(t.Context(), s.SessionID(), &prompt); err != nil {
			t.Fatal(err)
		}
		sess := a.Session(s.SessionID())
		out := s.HandleSubmit("/continue")
		if !s.Streaming() || !slices.ContainsFunc(out, func(e UIEvent) bool { return e == WorkingIndicator(0, false) }) {
			t.Fatalf("/continue = %#v with running %v, want a run started", out, s.Streaming())
		}
		if got := toast(s.HandleContinue()); got != "A run is already in progress" {
			t.Errorf("/continue during a run = %q, want a notice", got)
		}
		sess.Stop()
		synctest.Wait()
		s.ApplyAgentEvent(app.EventAgentEnded{EventBase: app.EventBase{SessionID: s.SessionID()}})

		reply := treeMsg("a1", "", message.RoleAgent, "Done.")
		if err := a.Sessions().AppendMessage(t.Context(), s.SessionID(), &reply); err != nil {
			t.Fatal(err)
		}
		if out := s.HandleContinue(); !s.Streaming() {
			t.Errorf("/continue after a reply = %#v, want a run started", out)
		}
		sess.Stop()
		synctest.Wait()
	})
}

// TestTreeInsertLine checks that the empty line that marks where the next
// prompt goes opens under the selected row, a reply's, a tool call's, or a
// prompt's, with the selected row's connectors running past it.
func TestTreeInsertLine(t *testing.T) {
	for _, tt := range []struct {
		id   message.ID
		want []string
	}{
		{id: "a1", want: []string{"  Agent I'll add Divide with a zero check.", "→", "  ├─ User also add tests"}},
		{id: "u2", want: []string{"  ├─ User also add tests", "→ │", "  │  Agent Added TestDivide."}},
		{id: "a2", want: []string{"  │  Agent Added TestDivide.", "→ │", "  └─ User use table-driven tests instead"}},
	} {
		t.Run(string(tt.id), func(t *testing.T) {
			st := NewSessionTree(forkedTree("a2"))
			selectID(t, st, tt.id)
			v := st.View(treeMaxRows)
			var lines []string
			for _, line := range TreeLines(v, DefaultTheme()) {
				lines = append(lines, strings.TrimRight(line.PlainText(), " "))
			}
			at := v.InsertLine()
			if got := lines[at-1 : at+2]; !slices.Equal(got, tt.want) {
				t.Errorf("lines around the insert line:\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}

// TestTreeLinesLabels checks that the tree labels its rows as the transcript
// names them, "User", "Agent", or a tool's label, with no colon, and gives
// each tool call of a message of calls alone a row of its own.
func TestTreeLinesLabels(t *testing.T) {
	st := NewSessionTree(forkedTree("a4"))
	var got []string
	for _, line := range TreeLines(st.View(treeMaxRows), DefaultTheme()) {
		got = append(got, strings.TrimSpace(strings.TrimLeft(line.PlainText(), " →│├└─")))
	}
	// The insert line, under the head, is empty.
	if i := slices.Index(got, ""); i >= 0 {
		got = slices.Delete(got, i, i+1)
	}
	want := []string{
		"User add a Divide method to the calculator",
		"Agent I'll add Divide with a zero check.",
		"User use table-driven tests instead",
		"Read calc_test.go",
		"Edit calc_test.go",
		"Agent Rewrote the tests as a table.",
		"User also add tests",
		"Agent Added TestDivide.",
	}
	if !slices.Equal(got, want) {
		t.Errorf("rows:\n%q\nwant:\n%q", got, want)
	}
}

// titledTree returns forkedTree with the exchange that titled the session
// after its prompt u2 forked below it: t1, the request, and t2, the title.
func titledTree(head message.ID) app.Tree {
	tree := forkedTree(head)
	t1 := treeMsg("t1", "u2", message.RoleUser, "Summarize the user request in this session")
	t2 := treeMsg("t2", "t1", message.RoleAgent, "Divide with tests")
	t1.Type, t2.Type = message.TypeTitle, message.TypeTitle
	// A fork read just after its prompt was stored has no parent in the tree.
	orphan := treeMsg("t9", "u9", message.RoleUser, "orphan")
	orphan.Type = message.TypeTitle
	tree.Forks = []message.Content{t1, t2, orphan}
	return tree
}

// TestSessionTreeHidden checks that the tree hides the forks off the
// conversation until they are toggled on, then draws them as asides off the
// transcript, under the prompt they forked from and ahead of the reply,
// which stays at the prompt's indent, names them in the status line, and
// finds them by their type; that it leaves out a fork with no parent; and
// that toggling them off again keeps the selection on the nearest listed
// message above it.
func TestSessionTreeHidden(t *testing.T) {
	st := NewSessionTree(titledTree("a2"))
	plain := []string{"u1*", "a1*", "├─ u2*", "│  a2*", "└─ u3", "   a3", "   a3", "   a4"}
	if got := drawn(st); !slices.Equal(got, plain) {
		t.Errorf("rows = %q, want the conversation alone: %q", got, plain)
	}
	if _, ok := st.Index["t9"]; ok {
		t.Error("the tree holds a fork message whose parent it does not")
	}

	st.ToggleHidden()
	want := []string{"u1*", "a1*", "├─ u2*", "│  └─ t1", "│     t2", "│  a2*", "└─ u3", "   a3", "   a3", "   a4"}
	if got := drawn(st); !slices.Equal(got, want) {
		t.Errorf("rows with hidden = %q, want %q", got, want)
	}
	if v := st.View(20); v.Status != "[hidden]" {
		t.Errorf("status = %q, want [hidden]", v.Status)
	}
	selectID(t, st, "t2")

	st.Search("title")
	if got := drawn(st); !slices.Equal(got, []string{"t1", "t2"}) {
		t.Errorf("rows matching title = %q, want the fork", got)
	}
	st.Search("")

	st.ToggleHidden()
	if got := drawn(st); !slices.Equal(got, plain) {
		t.Errorf("rows after hiding again = %q, want %q", got, plain)
	}
	if got := selectedID(st); got != "u2" {
		t.Errorf("selected %s, want u2, the nearest listed message above t2", got)
	}
	if v := st.View(20); v.Status != "" {
		t.Errorf("status = %q, want none", v.Status)
	}
}

// TestChooseHiddenTreeRow checks that choosing a fork's row says it is not
// part of the conversation, asks for no move, and leaves the tree open.
func TestChooseHiddenTreeRow(t *testing.T) {
	s := &Service{state: &UIState{}}
	s.OpenTree(titledTree("a2"))
	s.ToggleTreeHidden()
	selectID(t, s.state.Tree, "t2")
	out := s.ChooseTreeRow()
	for _, ev := range out {
		if _, ok := ev.(EventTreeMoveRequested); ok {
			t.Fatalf("choosing a fork asked for a move: %#v", out)
		}
	}
	if !slices.ContainsFunc(out, func(ev UIEvent) bool {
		toast, ok := toastOf(ev)
		return ok && toast.Text == "A title message is not part of the conversation"
	}) {
		t.Errorf("events = %#v, want a notice that the fork is not part of the conversation", out)
	}
	if s.state.Banner != TreeBanner || s.state.Tree == nil {
		t.Error("choosing a fork closed the tree")
	}
}

// TestTreeLinesTagHidden checks that a fork's row opens with its type's tag,
// before its role.
func TestTreeLinesTagHidden(t *testing.T) {
	st := NewSessionTree(titledTree("a2"))
	st.ToggleHidden()
	lines := TreeLines(st.View(20), nil)
	var plain []string
	for _, l := range lines {
		plain = append(plain, l.PlainText())
	}
	for _, want := range []string{"│  └─ [title] User Summarize the user request", "│     [title] Agent Divide with tests", "│  Agent Added TestDivide."} {
		if !slices.ContainsFunc(plain, func(l string) bool { return strings.Contains(l, want) }) {
			t.Errorf("lines = %q, want one holding %q", plain, want)
		}
	}
}

// TestSessionTreeAsides checks where a fork off the conversation is drawn
// beside what goes on from its prompt: as an aside over a single reply,
// which keeps the prompt's indent; as a branch of the fork among several
// replies; and as an aside under a prompt with no reply yet. The jumps
// between branches pass asides by.
func TestSessionTreeAsides(t *testing.T) {
	title := func(id, parent message.ID) message.Content {
		m := treeMsg(id, parent, message.RoleUser, "summarize")
		m.Type = message.TypeTitle
		return m
	}
	tests := []struct {
		name string
		tree app.Tree
		want []string
	}{
		{name: "one reply", tree: app.Tree{Head: "a1", Messages: []message.Content{
			treeMsg("u1", "", message.RoleUser, "hi"),
			treeMsg("a1", "u1", message.RoleAgent, "hello"),
		}, Forks: []message.Content{title("t1", "u1")}},
			want: []string{"u1*", "└─ t1", "a1*"}},
		{name: "several replies", tree: app.Tree{Head: "a1", Messages: []message.Content{
			treeMsg("u1", "", message.RoleUser, "hi"),
			treeMsg("a1", "u1", message.RoleAgent, "hello"),
			treeMsg("a2", "u1", message.RoleAgent, "hey"),
		}, Forks: []message.Content{title("t1", "u1")}},
			want: []string{"u1*", "├─ t1", "├─ a1*", "└─ a2"}},
		{name: "no reply yet", tree: app.Tree{Head: "u1", Messages: []message.Content{
			treeMsg("u1", "", message.RoleUser, "hi"),
		}, Forks: []message.Content{title("t1", "u1")}},
			want: []string{"u1*", "└─ t1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := NewSessionTree(tt.tree)
			st.ToggleHidden()
			if got := drawn(st); !slices.Equal(got, tt.want) {
				t.Errorf("rows = %q, want %q", got, tt.want)
			}
		})
	}

	st := NewSessionTree(titledTree("a2"))
	st.ToggleHidden()
	selectID(t, st, "u2")
	st.JumpFork(1)
	if got := selectedID(st); got != "a2" {
		t.Errorf("jump down from u2 selected %s, want a2, past its aside", got)
	}
	st.JumpFork(-1)
	if got := selectedID(st); got != "u2" {
		t.Errorf("jump up from a2 selected %s, want u2, the start of its branch", got)
	}
}
