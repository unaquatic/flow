package ui_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension/snapshot"
	snapshotui "github.com/unaquatic/flow/extension/snapshot/ui"
	"github.com/unaquatic/flow/terminal/ui"
	"github.com/unaquatic/fluid/layout"
)

// These tests show the widgets of the snapshot extension in the transcript:
// how its rows stack with tool calls, and how its changes expand with them.

// snapshotViews returns the widgets of a snapshot extension that is
// disabled, so that it reads no patch: the tests set the patches they show.
func snapshotViews() ui.Extension {
	return snapshotui.New(context.Background(), &snapshot.Extension{})
}

// interruptedBash returns the call of a reply stopped while it ran.
func interruptedBash() message.ContentPart {
	return message.ToolPart{ID: "a1-bash", CallID: "c1", Name: "bash", State: message.ToolStateError{
		Input: json.RawMessage(`{"command":"go test ./..."}`), Error: message.InterruptedError}}
}

// patchTurn adds reply a1, whose step added two lines to calc.go. The patch
// shows once it is set and the bubble is laid out again, as LoadPatch does
// when the patch arrives, since the bubble spaces only the rows it is told show.
func patchTurn(u *ui.UI) {
	u.AddMessage(ui.AgentText("a1", "Updated the calculator."))
	patchBox(u, "a1").SetPatch(
		"diff --git a/calc.go b/calc.go\n--- a/calc.go\n+++ b/calc.go\n@@ -1,2 +1,3 @@\n package calc\n+\n+import \"errors\"\n")
	u.Bubble("a1").SyncRows()
}

// patchBox returns the changes of message id.
func patchBox(u *ui.UI, id message.ID) *snapshotui.PatchBox {
	return u.Bubble(id).SegmentFor(id).Views[0].(*snapshotui.PatchBox)
}

func TestSnapshotFrames(t *testing.T) {
	tests := []struct {
		name string
		h    int
		add  func(u *ui.UI)
	}{
		// A prompt whose snapshot takes long shows it being taken, as a row
		// like a tool call's, on the canvas a blank row under the block of
		// its text.
		{name: "user_message_indexing", h: 6, add: func(u *ui.UI) {
			msg := ui.UserText("u1", "run the tests")
			msg.Parts = append(msg.Parts, snapshot.StepPart{ID: "u1-step", Pending: true})
			u.AddMessage(msg)
		}},
		// Once the snapshot of a prompt is taken, its row is gone, and the
		// prompt ends in the one blank row under its text's block, which
		// parts it from the reply.
		{name: "user_message_indexed", h: 5, add: func(u *ui.UI) {
			prompt := ui.UserText("u1", "run the tests")
			prompt.Parts = append(prompt.Parts, snapshot.StepPart{ID: "u1-step", Pending: true})
			u.AddMessage(prompt)
			taken := prompt
			taken.Parts = []message.ContentPart{prompt.Parts[0], snapshot.StepPart{ID: "u1-step", Snapshot: ui.ToolTurnTree}}
			u.UpdateMessage(taken)
			u.AddMessage(ui.AgentText("a1", "All tests pass."))
		}},
		// A reply stopped during a call shows its snapshot being taken, as a
		// row right under the interrupted call's, until the snapshot is taken.
		{name: "agent_indexing_stopped", h: 6, add: func(u *ui.UI) {
			u.AddMessage(ui.AgentParts("a1",
				message.TextPart{ID: "a1-text", Text: "Running the tests."},
				interruptedBash(),
				snapshot.StepPart{ID: "a1-step", Pending: true},
			))
		}},
		// A reply with no call shows its snapshot being taken a blank row
		// under its text, as it would show a call, in the white tool name
		// color when /settings picks it.
		{name: "agent_indexing_white", h: 5, add: func(u *ui.UI) {
			u.AddMessage(ui.AgentParts("a1",
				message.TextPart{ID: "a1-text", Text: "All tests pass."},
				snapshot.StepPart{ID: "a1-step", Pending: true},
			))
			u.Apply(u.Service().SetToolColor(ui.ToolNameWhite))
		}},
		// Once the snapshot is taken, the row is gone, and the reply ends
		// where its call does.
		{name: "agent_indexed", h: 6, add: func(u *ui.UI) {
			text := message.TextPart{ID: "a1-text", Text: "Running the tests."}
			u.AddMessage(ui.AgentParts("a1", text, interruptedBash(), snapshot.StepPart{ID: "a1-step", Pending: true}))
			u.UpdateMessage(ui.AgentParts("a1", text, interruptedBash(), snapshot.StepPart{ID: "a1-step", Snapshot: ui.ToolTurnTree}))
		}},
		// Consecutive prompts share a bubble, each its own block a blank row
		// under the one above, with its snapshot under it.
		{name: "user_messages_grouped_debug", h: 11, add: func(u *ui.UI) {
			u.Apply(u.Service().SetDebug(true))
			for _, id := range []message.ID{"u1", "u2"} {
				prompt := ui.UserText(id, "run the tests")
				prompt.ParentID = "u1"
				prompt.Parts = append(prompt.Parts, snapshot.StepPart{ID: message.PartID(id + "-step"), Snapshot: ui.ToolTurnTree})
				u.AddMessage(prompt)
			}
		}},
		// With debug on in /settings, the snapshot stays once taken, as a row
		// that shows its hash, under the prompt and under the reply's call.
		{name: "agent_indexed_debug", h: 12, add: func(u *ui.UI) {
			u.Apply(u.Service().SetDebug(true))
			prompt := ui.UserText("u1", "run the tests")
			prompt.Parts = append(prompt.Parts, snapshot.StepPart{ID: "u1-step", Snapshot: ui.ToolTurnTree})
			u.AddMessage(prompt)
			text := message.TextPart{ID: "a1-text", Text: "Running the tests."}
			u.AddMessage(ui.AgentParts("a1", text, interruptedBash(), snapshot.StepPart{ID: "a1-step", Pending: true}))
			u.UpdateMessage(ui.AgentParts("a1", text, interruptedBash(), snapshot.StepPart{ID: "a1-step", Snapshot: ui.ToolTurnTree}))
		}},
		// A reply that ends its turn shows its snapshot, its cache row, and
		// its footer: the three stack as rows would but for the footer, which
		// is parted from the cache row by a blank row.
		{name: "agent_snapshot_cache_footer", h: 13, add: func(u *ui.UI) {
			u.Apply(u.Service().SetDebug(true))
			u.Apply(u.Service().SetCacheMode(ui.CachePerMessage))
			prompt := ui.UserText("u1", "Hi")
			prompt.Parts = append(prompt.Parts, snapshot.StepPart{ID: "u1-step", Snapshot: ui.ToolTurnTree})
			u.AddMessage(prompt)
			reply := ui.AgentParts("a1",
				message.TextPart{ID: "a1-text", Text: "Hi! How can I help you today?"},
				snapshot.StepPart{ID: "a1-step", Snapshot: ui.ToolTurnTree},
			)
			reply.Usage, reply.Worked = message.Usage{InputTokens: 12}, 3*time.Second
			u.AddMessage(reply)
		}},
		// A step's changes start collapsed to a header like a tool call's: the
		// bold name, and the files counted as an undo notice counts them.
		{name: "agent_patch", h: 8, add: patchTurn},
		{name: "agent_patch_expanded", h: 15, add: func(u *ui.UI) {
			patchTurn(u)
			ui.ExpandTools(u)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := ui.NewInlineTestUI(t, snapshotViews())
			tt.add(u)
			ui.AssertTranscriptFrames(t, u, tt.name, 40, tt.h)
		})
	}
}

// TestSnapshotFullscreenFrames draws the scrolling transcript of fullscreen
// mode, scrolled past a prompt: its text stays pinned at the top while the
// reply under it scrolls, and its snapshot scrolls away with the rows
// around it, as the rows of a reply do.
func TestSnapshotFullscreenFrames(t *testing.T) {
	tests := []struct {
		name   string
		scroll int
	}{
		// At the top, nothing is pinned yet.
		{name: "fullscreen_snapshot_top"},
		// The prompt's text is scrolled past and pinned, and its snapshot
		// scrolls up under it, as a row of the reply would.
		{name: "fullscreen_snapshot_scrolling", scroll: 1},
		// The snapshot has scrolled off, and the text is still pinned.
		{name: "fullscreen_snapshot_scrolled", scroll: 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := ui.NewInputTestUI(t, snapshotViews())
			prompt := ui.UserText("u1", "run the tests")
			prompt.Parts = append(prompt.Parts, snapshot.StepPart{ID: "u1-step", Pending: true})
			u.AddMessage(prompt)
			u.AddMessage(ui.AgentText("a1", "one\n\ntwo\n\nthree\n\nfour\n\nfive\n\nsix\n\nseven\n\neight"))
			ui.AssertFullscreenFrames(t, u, tt.name, 40, 12, tt.scroll)
		})
	}
}

// TestPromptPinsWithoutItsSnapshot checks that of the two rows a prompt takes
// in the fullscreen transcript, the one that holds its text pins to the top
// of the viewport once scrolled past, and the one under it that holds its
// snapshot does not.
func TestPromptPinsWithoutItsSnapshot(t *testing.T) {
	u := ui.NewInputTestUI(t, snapshotViews())
	for _, n := range []string{"1", "2"} {
		prompt := ui.UserText(message.ID("u"+n), "run the tests")
		prompt.Parts = append(prompt.Parts, snapshot.StepPart{ID: message.PartID("u" + n + "-step"), Pending: true})
		u.AddMessage(prompt)
		u.AddMessage(ui.AgentText(message.ID("a"+n), "one\n\ntwo\n\nthree\n\nfour\n\nfive\n\nsix"))
	}
	for _, id := range []message.ID{"u1", "u2"} {
		cm := u.Bubble(id)
		if got := cm.Box.LayoutNode().Sticky; got != layout.StickyTop {
			t.Errorf("%s: row of the text sticks to %v, want the top", id, got)
		}
		if got := cm.TailRow.LayoutNode().Sticky; got != layout.StickyNone {
			t.Errorf("%s: row of the snapshot sticks to %v, want none", id, got)
		}
	}

	// Scrolled past the first prompt and its reply, but not into the second.
	ui.DrawFullscreen(u, 40, 12, 8)
	first := u.Bubble("u1")
	if first.Box.StickyOffsetY() <= 0 {
		t.Errorf("text of the first prompt is offset by %d, want it pinned below its place", first.Box.StickyOffsetY())
	}
	if got := first.TailRow.StickyOffsetY(); got != 0 {
		t.Errorf("snapshot of the first prompt is offset by %d, want it to scroll", got)
	}
	if got := u.Bubble("u2").Box.StickyOffsetY(); got != 0 {
		t.Errorf("text of the second prompt is offset by %d before it is scrolled to", got)
	}

	// Scrolled to the end, the second prompt has taken the first's place.
	ui.DrawFullscreen(u, 40, 12, 1000)
	second := u.Bubble("u2")
	if second.Box.StickyOffsetY() <= 0 {
		t.Errorf("text of the second prompt is offset by %d, want it pinned below its place", second.Box.StickyOffsetY())
	}
	if got := second.TailRow.StickyOffsetY(); got != 0 {
		t.Errorf("snapshot of the second prompt is offset by %d, want it to scroll", got)
	}
}

// TestPromptsDoNotShareABubble checks that consecutive prompts take a bubble
// each, since each pins on its own, and that each message keeps its widgets.
func TestPromptsDoNotShareABubble(t *testing.T) {
	u := ui.NewInlineTestUI(t, snapshotViews())
	for _, id := range []message.ID{"u1", "u2"} {
		prompt := ui.UserText(id, "run the tests")
		prompt.ParentID = "u1"
		u.AddMessage(prompt)
	}
	first, second := u.Bubble("u1"), u.Bubble("u2")
	if first == nil || second == nil || first == second {
		t.Fatalf("prompts show in bubbles %p and %p, want one each", first, second)
	}
	if got := len(first.SegmentFor("u1").ContentBox.ChildrenRaw()); got != 1 {
		t.Errorf("first prompt shows %d part widgets, want its text", got)
	}
}

// TestSnapshotIndicatorFrames draws the indicator of a run whose prompt's
// snapshot is still being taken before the reply shows anything: a blank row
// parts the indicator from the snapshot's row, as it parts the indicator from
// a reply.
func TestSnapshotIndicatorFrames(t *testing.T) {
	u := ui.NewInputTestUI(t, snapshotViews())
	msg := ui.UserText("u1", "explore the repo")
	msg.Parts = append(msg.Parts, snapshot.StepPart{ID: "u1-step", Pending: true})
	u.AddMessage(msg)
	ui.AssertIndicatorFrames(t, u, "user_message_indexing_indicator", 48, 8)
}

// TestSnapshotPatchBounds checks that a reply's changes are bounded by the
// tree the message before it ended with and the tree of its own step part,
// once it arrives.
func TestSnapshotPatchBounds(t *testing.T) {
	u := ui.NewInlineTestUI(t, snapshotViews())
	ui.StreamToolTurn(u)
	if box := patchBox(u, "a1"); box.From != ui.ToolTurnBase || box.To != ui.ToolTurnTree {
		t.Errorf("patch bounds = %s..%s, want the prompt's tree and the reply's", box.From, box.To)
	}
}

// TestSnapshotDebugSet checks that debug shows the snapshot of every prompt
// and reply once taken, of those shown and of those added later, and hides it
// again.
func TestSnapshotDebugSet(t *testing.T) {
	u := ui.NewInlineTestUI(t, snapshotViews())
	step := func(id message.PartID) message.ContentPart {
		return snapshot.StepPart{ID: id, Snapshot: ui.ToolTurnTree}
	}
	prompt := ui.UserText("u1", "run the tests")
	prompt.Parts = append(prompt.Parts, step("u1-step"))
	u.AddMessage(prompt)
	u.AddMessage(ui.AgentParts("a1", message.TextPart{ID: "a1-text", Text: "Done."}, step("a1-step")))
	check := func(when string, want ...bool) {
		t.Helper()
		var got []bool
		for _, id := range []message.ID{"u1", "a1", "a2"} {
			if cm := u.Bubble(id); cm != nil {
				for v := range cm.StyledViews() {
					if box, ok := v.(*snapshotui.StepBox); ok {
						got = append(got, box.Visible())
					}
				}
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s: snapshots shown %v, want %v", when, got, want)
		}
	}

	check("off", false, false)
	u.Apply(u.Service().SetDebug(true))
	check("on", true, true)
	u.AddMessage(ui.AgentParts("a2", message.TextPart{ID: "a2-text", Text: "Again."}, step("a2-step")))
	check("added on", true, true, true)
	u.Apply(u.Service().SetDebug(false))
	check("off again", false, false, false)
}

// TestSnapshotTogglesToolsCountsShownChanges checks that a step's changes
// count toward what Ctrl+O would toggle once they are shown, and not while
// the step has none, since toggling a hidden block changes nothing on screen.
func TestSnapshotTogglesToolsCountsShownChanges(t *testing.T) {
	u := ui.NewInputTestUI(t, snapshotViews())
	u.AddMessage(ui.AgentText("a1", "Done."))
	if u.TogglesTools(true) {
		t.Error("TogglesTools(true) = true for a reply without tool calls or changes")
	}
	patchTurn(u)
	if !u.TogglesTools(true) {
		t.Error("TogglesTools(true) = false for a reply whose changes are shown collapsed")
	}
}
