package ui

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/testutil"
)

// commitRows commits the first n InlineRows of ui, or all of them when n is
// negative, as terminal.Renderer's CommitInline commits those that scrolled
// into the scrollback, taking them out of their parent, and returns their
// IDs.
func commitRows(ui *UI, n int) []string {
	rows := ui.InlineRows()
	if n >= 0 {
		rows = rows[:min(len(rows), n)]
	}
	var ids []string
	for _, row := range rows {
		ids = append(ids, row.ID())
		row.Parent().Remove(row.ID())
	}
	ui.DropCommittedRows(len(rows))
	return ids
}

// addEnded adds msg to the transcript of ui through its service, and ends it,
// as endMessage does.
func addEnded(ui *UI, msg ChatMessage) {
	ui.Apply(ui.service.AddMessage(msg))
	endMessage(ui, msg)
}

// endMessage ends msg in the transcript of ui through its service, as the app
// ends a message it stores.
func endMessage(ui *UI, msg ChatMessage) {
	ui.Apply(ui.service.ApplyAgentEvent(app.EventMessageEnded{Message: app.Message{Content: message.Content{
		ID: msg.ID, Role: msg.Role, Parts: msg.Parts}}}))
}

// newCommitTestUI returns an inline UI whose transcript holds, in order: the
// logo, prompt u1, reply steps a1 and a2 with a notice added between them,
// which a2 takes a row above, prompt u2, and reply a3, which still streams.
// Every message but a3 has ended.
func newCommitTestUI(t *testing.T) *UI {
	t.Helper()
	ui := newInlineTestUI(t)
	ui.inline = true
	ui.Apply(ui.service.AddNotice(Notice{Lines: []style.StyledText{style.NewStyledText(style.Plain("logo"))}, Surface: NoticeCanvas}))
	addEnded(ui, userText("u1", "first prompt"))
	addEnded(ui, agentText("a1", "first step"))
	ui.Apply(ui.service.ShowToast("a notice"))
	addEnded(ui, agentText("a2", "second step"))
	addEnded(ui, userText("u2", "second prompt"))
	ui.Apply(ui.service.AddMessage(agentText("a3", "streaming")))
	return ui
}

// transcriptText returns the text of the rows the inline root of ui lays out.
func transcriptText(ui *UI) []string {
	buf, rows := DrawInline(ui)
	lines := make([]string, rows)
	for y := range rows {
		lines[y] = testutil.RowText(buf, y)
	}
	return lines
}

// TestCommitScrolledRows checks that the rows committed to the terminal
// leave the transcript, and that a bubble is dropped once none of its rows
// stays, and kept until then.
func TestCommitScrolledRows(t *testing.T) {
	ids := func(m map[message.ID]*ChatMessageBox) []message.ID {
		return slices.Sorted(maps.Keys(m))
	}
	// The rows are the logo, u1's text and the row under it, a1 and a2, the
	// notice, u2's two rows, and a3.
	for _, tt := range []struct {
		name string
		// rows is how many rows go to the terminal.
		rows int
		// shown are the messages whose bubbles the UI still holds.
		shown []message.ID
	}{
		{name: "none", rows: 0, shown: []message.ID{"a1", "a2", "a3", "u1", "u2"}},
		{name: "part of a prompt", rows: 2, shown: []message.ID{"a1", "a2", "a3", "u1", "u2"}},
		{name: "a prompt", rows: 3, shown: []message.ID{"a1", "a2", "a3", "u2"}},
		{name: "part of a reply", rows: 4, shown: []message.ID{"a1", "a2", "a3", "u2"}},
		{name: "a reply", rows: 5, shown: []message.ID{"a3", "u2"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ui := newCommitTestUI(t)
			all := len(ui.transcriptRows)

			commitRows(ui, tt.rows)

			if got := len(ui.transcriptRows); got != all-tt.rows {
				t.Errorf("the transcript holds %d rows, want %d", got, all-tt.rows)
			}
			if got := ids(ui.messageWidgets); !slices.Equal(got, tt.shown) {
				t.Errorf("the UI holds the bubbles of %v, want %v", got, tt.shown)
			}
			if ui.committed != (tt.rows > 0) {
				t.Errorf("committed = %v, want %v", ui.committed, tt.rows > 0)
			}
		})
	}
}

// TestInlineRows_Fullscreen checks that no row of the transcript goes to the
// terminal outside inline mode.
func TestInlineRows_Fullscreen(t *testing.T) {
	ui := newCommitTestUI(t)
	ui.inline = false

	if rows := ui.InlineRows(); len(rows) != 0 {
		t.Errorf("%d rows would go to the terminal in fullscreen mode, want none", len(rows))
	}
}

// TestRebuildTranscript checks that the transcript built again after rows
// were committed shows what it showed before, its notices among the messages
// where they were added.
func TestRebuildTranscript(t *testing.T) {
	ui := newCommitTestUI(t)
	want := transcriptText(ui)
	commitRows(ui, -1)

	ui.RebuildTranscript()

	if got := transcriptText(ui); !slices.Equal(got, want) {
		t.Errorf("the transcript built again shows\n%q\nwant\n%q", got, want)
	}
	if ui.committed {
		t.Error("committed = true once every row is in the transcript again")
	}
	if got := len(ui.service.state.Messages) - ui.service.MessageCount(); got != 2 {
		t.Errorf("the transcript records %d notices, want the logo and the notice", got)
	}
}

// TestReprintTranscript checks that the reprint the renderer asks for builds
// the transcript again only while rows of it are committed.
func TestReprintTranscript(t *testing.T) {
	ui := newCommitTestUI(t)
	rows := slices.Clone(ui.transcriptRows)
	ui.ReprintTranscript()
	if !slices.Equal(ui.transcriptRows, rows) {
		t.Error("the reprint built the transcript again with no row committed")
	}

	commitRows(ui, -1)
	ui.ReprintTranscript()
	if got := len(ui.transcriptRows); got != len(rows) {
		t.Errorf("the transcript holds %d rows after the reprint, want all %d", got, len(rows))
	}
}

// TestSetInlineMode_RebuildsCommittedRows checks that the alternate screen
// shows the whole transcript once rows of it were committed.
func TestSetInlineMode_RebuildsCommittedRows(t *testing.T) {
	ui := newCommitTestUI(t)
	all := len(ui.transcriptRows)
	commitRows(ui, -1)

	ui.SetInlineMode(EventInlineModeSet{Inline: false})

	if got := ui.messageBox.ChildrenCount(); got != all {
		t.Errorf("the alternate screen shows %d rows, want all %d", got, all)
	}
}

// TestTranscriptResetsDropNotices checks that what replaces the transcript, a
// session that opens, a history move, and clearing the chat, drops the
// notices of the transcript it replaces, which a rebuild would show again.
func TestTranscriptResetsDropNotices(t *testing.T) {
	s, a := newTestService(t)
	id := storePrompts(t, a, "Run the tests")
	for _, tt := range []struct {
		name  string
		reset func()
	}{
		{name: "session opens", reset: func() {
			if err := s.OpenSession(id); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "history move", reset: func() { s.LoadMessages(nil) }},
		{name: "chat cleared", reset: func() { s.ClearMessages() }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s.ShowToast("old")
			tt.reset()
			if slices.ContainsFunc(s.state.Messages, func(m ChatMessage) bool { return m.Notice != nil && m.Notice.Text == "old" }) {
				t.Errorf("transcript = %+v, want the old notice dropped", s.state.Messages)
			}
		})
	}
}

// TestNoticesStayOutOfTheConversation checks that the notices the transcript
// holds among its messages change none of what follows from them: a reply
// after a notice joins the bubble of the reply before it, with that reply's
// parts as the step before it, the turn's time shows under the reply that
// ends it, a turn that adds a notice alone adds no message, and /status
// counts messages alone.
func TestNoticesStayOutOfTheConversation(t *testing.T) {
	s := &Service{state: &UIState{}}
	start := time.Now()
	s.StartTurn(start)
	s.AddMessage(agentText("a1", "first"))
	s.ShowToast("a notice")
	added := s.AddMessage(agentText("a2", "second"))[0].(EventMessageAdded).Msg
	if added.ParentID != "a1" {
		t.Errorf("the reply after the notice has parent %q, want a1", added.ParentID)
	}
	if len(added.Prev) != 1 || added.Prev[0].PartID() != "a1-text" {
		t.Errorf("the reply after the notice follows %+v, want the parts of a1", added.Prev)
	}
	s.ShowToast("another notice")
	if ended := s.EndTurn(start.Add(time.Second)); len(ended) != 1 || ended[0].(EventTurnEnded).MessageID != "a2" {
		t.Errorf("the turn ended with %+v, want its time under a2", ended)
	}

	s.StartTurn(start)
	s.ShowToast("a notice alone")
	if ended := s.EndTurn(start.Add(time.Second)); ended != nil {
		t.Errorf("a turn that added a notice alone ended with %+v, want nothing", ended)
	}
	if got := s.MessageCount(); got != 2 {
		t.Errorf("the transcript counts %d messages, want 2", got)
	}
}

// TestShowInfoCountsMessagesAlone checks that /status counts the messages of
// the transcript, and not the notices shown among them.
func TestShowInfoCountsMessagesAlone(t *testing.T) {
	s, _ := newTestService(t)
	s.InitSession(t.Context(), "")
	s.ShowToast("a notice")

	status, ok := toastOf(s.ShowInfo()[1])
	want := fmt.Sprintf("%-10s%d", "Messages", 0)
	if !ok || !slices.ContainsFunc(status.Lines, func(l style.StyledText) bool { return l.PlainText() == want }) {
		t.Errorf("/status shows %+v, want %q", status.Lines, want)
	}
}

// longSession returns the messages of a session as one loads them: turns
// turns, prompt "u<i>" "prompt <i>" each, and a reply of steps steps,
// "a<i>-<j>" "turn <i>, step <j>" each.
func longSession(turns, steps int) []ChatMessage {
	var msgs []ChatMessage
	for i := range turns {
		msgs = append(msgs, userText(message.ID(fmt.Sprintf("u%d", i)), fmt.Sprintf("prompt %d", i)))
		for j := range steps {
			msgs = append(msgs, agentText(message.ID(fmt.Sprintf("a%d-%d", i, j)), fmt.Sprintf("turn %d, step %d", i, j)))
		}
	}
	AssignParents(msgs)
	AssignPrev(msgs)
	return msgs
}

// loadSession returns a UI, inline as inline says, that loaded msgs as the
// active session's transcript, as a session that opens loads them.
func loadSession(t *testing.T, inline bool, msgs []ChatMessage) *UI {
	t.Helper()
	ui := newInlineTestUI(t)
	ui.inline = inline
	ui.messageBox.SetGone(inline)
	ui.service.state.Messages = msgs
	ui.Apply([]UIEvent{EventSessionLoad{Messages: msgs}})
	return ui
}

// segmentCounter counts the segments of the bubbles built, as an extension's
// view under each one is made.
type segmentCounter struct{ Built int }

func (c *segmentCounter) NewPartView(message.ContentPart, ViewHost) PartView { return nil }

func (c *segmentCounter) NewSegmentView(ChatMessage, ViewHost) SegmentView {
	c.Built++
	return nil
}

// TestBuildTranscript_KeepsTheLastRows checks that a long session loaded
// inline shows the rows of its last messages alone: TailRows of them at
// least, and no row that the rows under it leave out of them.
func TestBuildTranscript_KeepsTheLastRows(t *testing.T) {
	msgs := longSession(200, 3)
	ui := loadSession(t, true, msgs)

	height := ui.TranscriptHeight()
	if height < TailRows {
		t.Errorf("the transcript takes %d rows, want %d at least", height, TailRows)
	}
	rows := ui.transcriptRows
	if under := RowBottom(rows[len(rows)-1]) - RowBottom(rows[0]); under >= TailRows {
		t.Errorf("the rows under the first take %d rows, want fewer than %d", under, TailRows)
	}
	if _, ok := ui.messageWidgets["u0"]; ok {
		t.Error("the transcript shows the first prompt, want it left out")
	}
	if _, ok := ui.messageWidgets[msgs[len(msgs)-1].ID]; !ok {
		t.Error("the transcript does not show the last reply")
	}
	if !ui.trimmed || ui.committed {
		t.Errorf("trimmed, committed = %v, %v, want true, false", ui.trimmed, ui.committed)
	}
}

// TestBuildTranscript_Whole checks that a transcript is built whole outside
// inline mode, and inline when its rows add up to fewer than TailRows.
func TestBuildTranscript_Whole(t *testing.T) {
	for _, tt := range []struct {
		name   string
		inline bool
		turns  int
	}{
		{name: "fullscreen", turns: 200},
		{name: "short session", inline: true, turns: 5},
	} {
		t.Run(tt.name, func(t *testing.T) {
			msgs := longSession(tt.turns, 3)
			ui := loadSession(t, tt.inline, msgs)

			for _, m := range msgs {
				if _, ok := ui.messageWidgets[m.ID]; !ok {
					t.Fatalf("the transcript does not show %s, want every message", m.ID)
				}
			}
			if ui.trimmed {
				t.Error("trimmed = true for a transcript built whole")
			}
		})
	}
}

// TestBuildTranscript_BuildsTheLastMessages checks that a long session
// loaded inline builds the bubbles of about as many messages as it shows,
// rather than of every message.
func TestBuildTranscript_BuildsTheLastMessages(t *testing.T) {
	msgs := longSession(2000, 3)
	ui := newInlineTestUI(t)
	ui.inline = true
	counter := &segmentCounter{}
	ui.extensions = append(ui.extensions, counter)

	ui.LoadTranscript(msgs)

	shown := 0
	for _, m := range msgs {
		if _, ok := ui.messageWidgets[m.ID]; ok {
			shown++
		}
	}
	if counter.Built > 3*shown {
		t.Errorf("the load built %d messages of %d to show %d, want %d at most", counter.Built, len(msgs), shown, 3*shown)
	}
}

// TestBuildTranscript_ReplyContinues checks that a reply whose first rows a
// long session loaded inline leaves out takes the steps it goes on with.
func TestBuildTranscript_ReplyContinues(t *testing.T) {
	msgs := longSession(1, 600)
	ui := loadSession(t, true, msgs)
	reply := ui.messageWidgets[msgs[1].ID]
	if reply == nil {
		t.Fatal("the transcript does not show the reply")
	}
	if slices.Contains(ui.transcriptRows, reply.segments[1].Box) {
		t.Fatal("the transcript shows the second step of the reply, want its first steps left out")
	}

	ui.Apply(ui.service.AddMessage(agentText("next", "one more step")))

	if ui.messageWidgets["next"] != reply {
		t.Error("the step the reply went on with took a bubble of its own, want the reply's")
	}
	if last := reply.segments[len(reply.segments)-1]; last.MsgID != "next" || ui.transcriptRows[len(ui.transcriptRows)-1] != last.Box {
		t.Error("the step the reply went on with is not the last row of the transcript")
	}
}

// TestTrimmedTranscript_Rebuilds checks what a transcript loaded inline
// without its first rows rebuilds: nothing for a reprint, which puts back
// committed rows alone, the last TailRows rows when rows were committed too,
// and every row in fullscreen mode.
func TestTrimmedTranscript_Rebuilds(t *testing.T) {
	msgs := longSession(200, 3)

	t.Run("reprint", func(t *testing.T) {
		ui := loadSession(t, true, msgs)
		rows := slices.Clone(ui.transcriptRows)
		ui.ReprintTranscript()
		if !slices.Equal(ui.transcriptRows, rows) {
			t.Error("the reprint built the transcript again with no row committed")
		}
	})
	t.Run("reprint after a commit", func(t *testing.T) {
		ui := loadSession(t, true, msgs)
		commitRows(ui, 10)
		ui.ReprintTranscript()
		if !ui.trimmed || ui.TranscriptHeight() < TailRows {
			t.Errorf("trimmed = %v with %d rows, want the last %d rows", ui.trimmed, ui.TranscriptHeight(), TailRows)
		}
		if _, ok := ui.messageWidgets["u0"]; ok {
			t.Error("the reprint shows the first prompt, want the last rows alone")
		}
	})
	t.Run("fullscreen", func(t *testing.T) {
		ui := loadSession(t, true, msgs)
		ui.SetInlineMode(EventInlineModeSet{Inline: false})
		if _, ok := ui.messageWidgets["u0"]; !ok || ui.trimmed {
			t.Errorf("fullscreen shows the first prompt = %v, trimmed = %v, want every row", ok, ui.trimmed)
		}
	})
}

// TestSetInlineMode_TrimsTheTranscript checks that a switch from fullscreen
// to inline keeps the last TailRows rows of the transcript, as a load does.
func TestSetInlineMode_TrimsTheTranscript(t *testing.T) {
	ui := loadSession(t, false, longSession(200, 3))

	ui.SetInlineMode(EventInlineModeSet{Inline: true})

	if _, ok := ui.messageWidgets["u0"]; ok || !ui.trimmed {
		t.Errorf("inline shows the first prompt = %v, trimmed = %v, want the last rows alone", ok, ui.trimmed)
	}
	if height := ui.TranscriptHeight(); height < TailRows {
		t.Errorf("the transcript takes %d rows, want %d at least", height, TailRows)
	}
}

// TestBubbleStart checks where a transcript of the last messages from an
// index on starts: there, unless the message there, past notices, joins the
// bubble of one before it.
func TestBubbleStart(t *testing.T) {
	notice := ChatMessage{ID: "n", Notice: &Notice{Text: "a notice"}}
	msgs := []ChatMessage{userText("u1", "p"), agentText("a1", "x"), agentText("a2", "y"), notice, agentText("a3", "z"), userText("u2", "q"), agentText("a4", "w")}
	AssignParents(msgs)
	// The notice keeps the reply together, as the service records it.
	msgs[4].ParentID = "a1"
	for _, tt := range []struct {
		i, want int
	}{
		{i: -1, want: 0},
		{i: 0, want: 0},
		{i: 1, want: 1},
		{i: 2, want: 1},
		{i: 3, want: 1},
		{i: 4, want: 1},
		{i: 5, want: 5},
		{i: 6, want: 6},
		{i: 7, want: 7},
	} {
		if got := BubbleStart(msgs, tt.i); got != tt.want {
			t.Errorf("BubbleStart(%d) = %d, want %d", tt.i, got, tt.want)
		}
	}
}

// TestDropCommittedRows_StopsTheShimmer checks that the tool calls of a
// bubble dropped once all its rows are committed no longer shimmer.
func TestDropCommittedRows_StopsTheShimmer(t *testing.T) {
	ui := newInlineTestUI(t)
	ui.inline = true
	running := func(in json.RawMessage) message.ToolState { return message.ToolStateRunning{Input: in} }
	ui.AddMessage(agentParts("a1", bashCall("a1-bash", "sleep 1", running)))
	if len(ui.shimmer.Rows) == 0 {
		t.Fatal("the running call does not shimmer")
	}

	commitRows(ui, -1)

	if len(ui.shimmer.Rows) != 0 {
		t.Errorf("%d calls of the dropped bubble still shimmer, want none", len(ui.shimmer.Rows))
	}
}
