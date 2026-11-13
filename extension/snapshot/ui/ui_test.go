package ui

import (
	"context"
	"errors"
	"testing"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/extension/snapshot"
	"github.com/unaquatic/flow/extension/snapshot/internal/engine"
	"github.com/unaquatic/flow/terminal/ui"
	"github.com/unaquatic/fluid/style"
)

// The trees a turn starts from and ends with.
var (
	base = mustHash("7a36b04ce429eb3326713c5689665b792179b206")
	tree = mustHash("c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0")
)

// mustHash parses s, a valid tree hash.
func mustHash(s string) snapshot.Hash {
	h, err := engine.ParseHash(s)
	if err != nil {
		panic(err)
	}
	return h
}

// TestNewPartView checks that the extension shows snapshot parts, and leaves
// other parts to the transcript.
func TestNewPartView(t *testing.T) {
	e := New(t.Context(), nil)
	host := ui.ViewHost{Style: ui.ViewStyle{Snapshots: true, NameFg: ui.DefaultTheme().TextMuted}}
	box, ok := e.NewPartView(snapshot.SnapshotPart{ID: "s", Tree: tree}, host).(*SnapshotBox)
	if !ok || !box.Visible() {
		t.Fatalf("view of a snapshot part = %T, want a SnapshotBox shown, as snapshots are on", box)
	}
	if view := e.NewPartView(message.TextPart{Text: "hi"}, host); view != nil {
		t.Errorf("view of a text part = %T, want none", view)
	}
}

// TestSnapshotBox_ShownWhilePending checks that a snapshot part shows as a
// row while the snapshot is being taken, and is gone before and once it holds
// its tree.
func TestSnapshotBox_ShownWhilePending(t *testing.T) {
	box := NewSnapshotBox(snapshot.SnapshotPart{ID: "s"})
	if box.Visible() {
		t.Fatal("a snapshot part with its tree is visible, want it gone")
	}
	for _, step := range []struct {
		part snapshot.SnapshotPart
		want bool
	}{
		{snapshot.SnapshotPart{ID: "s", Pending: true}, true},
		{snapshot.SnapshotPart{ID: "s", Tree: tree}, false},
	} {
		ui.UpdateContentPart(box, step.part)
		if box.Visible() != step.want {
			t.Errorf("after %+v visible = %v, want %v", step.part, box.Visible(), step.want)
		}
	}
	if !ui.IsToolRow(box) {
		t.Error("a snapshot row does not stack as a tool call row")
	}
}

// TestSnapshotBox_ShowDone checks that a snapshot part kept once taken stays
// a row, reads as being indexed while pending, and then shows its tree's
// short hash in brackets, as a history move's notice names what it checked
// out, or that recording it failed.
func TestSnapshotBox_ShowDone(t *testing.T) {
	box := NewSnapshotBox(snapshot.SnapshotPart{ID: "s", Pending: true})
	box.SetShowDone(true)
	for _, step := range []struct {
		part snapshot.SnapshotPart
		want string
	}{
		{snapshot.SnapshotPart{ID: "s", Pending: true}, "Snapshot " + IndexingStatus},
		{snapshot.SnapshotPart{ID: "s", Tree: tree}, "Snapshot [" + tree.String()[:engine.ShortHashLen] + "]"},
		{snapshot.SnapshotPart{ID: "s"}, "Snapshot " + FailedStatus},
	} {
		ui.UpdateContentPart(box, step.part)
		if !box.Visible() || box.Header.PlainText() != step.want {
			t.Errorf("after %+v visible %v with %q, want visible with %q", step.part, box.Visible(), box.Header.PlainText(), step.want)
		}
	}
	box.SetShowDone(false)
	if box.Visible() {
		t.Error("a snapshot part taken is visible once not kept, want it gone")
	}
}

// TestPatchBox_LoadsOnceBothTreesAreKnown checks that a turn's changes are
// fetched once, under the reply that ends it, between the tree its prompt
// ended with and the tree of that reply's own snapshot part, once that
// arrives, and that neither a reply that does not end its turn nor a prompt
// fetches any.
func TestPatchBox_LoadsOnceBothTreesAreKnown(t *testing.T) {
	var loads [][2]snapshot.Hash
	box := NewPatchBox(nil, style.Color{})
	box.Load = func(from, to snapshot.Hash) { loads = append(loads, [2]snapshot.Hash{from, to}) }
	reply := ui.ChatMessage{ID: "a2", Role: message.RoleAgent,
		Prev:     []message.ContentPart{snapshot.SnapshotPart{Tree: mustHash("1111111111111111111111111111111111111111")}},
		TurnPrev: []message.ContentPart{snapshot.SnapshotPart{Tree: base}}}
	box.SetMessage(reply)
	reply.Parts = []message.ContentPart{snapshot.SnapshotPart{Tree: tree}}
	box.SetMessage(reply)
	if len(loads) != 0 {
		t.Fatalf("loaded %v before the turn ended", loads)
	}
	reply.EndsTurn = true
	box.SetMessage(reply)
	box.SetMessage(reply)
	if want := [][2]snapshot.Hash{{base, tree}}; len(loads) != 1 || loads[0] != want[0] {
		t.Errorf("loads = %v, want %v once", loads, want)
	}

	prompt := NewPatchBox(nil, style.Color{})
	prompt.Load = func(from, to snapshot.Hash) { t.Errorf("a prompt loaded %s..%s", from, to) }
	prompt.SetMessage(ui.ChatMessage{ID: "u2", Role: message.RoleUser, EndsTurn: true,
		Prev:  []message.ContentPart{snapshot.SnapshotPart{Tree: base}},
		Parts: []message.ContentPart{snapshot.SnapshotPart{Tree: tree}}})
}

// readPatches returns an Extension whose patches are patch, or err, and the
// number of patches it read so far.
func readPatches(patch string, err error) (*Extension, *int) {
	reads := 0
	e := &Extension{
		ReadPatch: func(context.Context, snapshot.Hash, snapshot.Hash) (string, error) {
			reads++
			return patch, err
		},
		Patches: make(map[[2]snapshot.Hash]string),
		ctx:     context.Background(),
	}
	return e, &reads
}

// stepPatch is the patch of a step that changed one line of one file.
const stepPatch = "diff --git a/calc.go b/calc.go\n--- a/calc.go\n+++ b/calc.go\n@@ -1 +1 @@\n-a\n+b\n"

// TestNewSegmentView_ShowsThePatchAtOnce checks that the changes of a turn
// show as the view of the reply that ends it is made, rather than once a
// patch read later arrives, which could change a row already in the
// scrollback.
func TestNewSegmentView_ShowsThePatchAtOnce(t *testing.T) {
	e, _ := readPatches(stepPatch, nil)
	relayouts := 0
	host := ui.ViewHost{Enqueue: func(func()) {}, Relayout: func() { relayouts++ }}
	reply := ui.ChatMessage{ID: "a1", Role: message.RoleAgent, EndsTurn: true,
		TurnPrev: []message.ContentPart{snapshot.SnapshotPart{Tree: base}},
		Parts:    []message.ContentPart{snapshot.SnapshotPart{Tree: tree}}}

	box := e.NewSegmentView(reply, host).(*PatchBox)

	if box.Patch != stepPatch || !box.Visible() {
		t.Errorf("the view shows patch %q, visible %v, want the turn's patch shown", box.Patch, box.Visible())
	}
	if relayouts != 1 {
		t.Errorf("the bubble was laid out again %d times, want once", relayouts)
	}
}

// TestLoadPatch_ReadsOnce checks that a patch read once shows again without
// being read again, as when the transcript is built again, and that one that
// fails to read keeps its box hidden and is read again next time.
func TestLoadPatch_ReadsOnce(t *testing.T) {
	host := ui.ViewHost{Relayout: func() {}}
	e, reads := readPatches(stepPatch, nil)
	for range 2 {
		box := NewPatchBox(nil, style.Color{})
		e.LoadPatch(box, base, tree, host)
		if box.Patch != stepPatch {
			t.Errorf("the box shows %q, want the patch", box.Patch)
		}
	}
	if *reads != 1 {
		t.Errorf("read %d patches, want 1", *reads)
	}

	failing, reads := readPatches("", errors.New("disk full"))
	for range 2 {
		box := NewPatchBox(nil, style.Color{})
		failing.LoadPatch(box, base, tree, host)
		if box.Visible() {
			t.Error("a box whose patch failed to read shows")
		}
	}
	if *reads != 2 {
		t.Errorf("read %d patches, want a failed one read again", *reads)
	}
}

// TestPatchHeader checks that a step's changes are headed like a tool call,
// by "Diff", not bold, in the color of a panel's title, without brackets,
// and counted as the notice of a history move counts them, in the same
// colors.
func TestPatchHeader(t *testing.T) {
	header := PatchHeader(DiffStat{Files: 1, Additions: 0, Deletions: 205})
	if got, want := header.PlainText(), "Diff 1 file  +0 -205"; got != want {
		t.Fatalf("header = %q, want %q", got, want)
	}
	name := header.Chunks[0]
	if title := ui.TitleChunk(PatchTitle); name.Text != "Diff" || name.Attributes&style.AttrBold != 0 || name.Fg != title.Fg {
		t.Errorf("name = %+v, want \"Diff\", not bold, in the title color", name)
	}
	notice := ui.BuildDiffToastLines([]app.FileDiff{{File: "/abs/x.go", Deletions: 205}})[0]
	fgs := func(chunks []style.TextChunk) []style.Color {
		var out []style.Color
		for _, c := range chunks {
			if !c.Fg.IsTransparent() {
				out = append(out, c.Fg)
			}
		}
		return out
	}
	got, want := fgs(header.Chunks[1:]), fgs(notice.Chunks)
	if len(got) != 2 || len(want) != 2 || got[0] != ui.DiffAddFg || got[1] != ui.DiffDelFg || want[0] != ui.DiffAddFg || want[1] != ui.DiffDelFg {
		t.Errorf("count colors: header %v, notice %v; want both %v then %v", got, want, ui.DiffAddFg, ui.DiffDelFg)
	}
}
