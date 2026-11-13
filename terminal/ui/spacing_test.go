package ui

import (
	"encoding/json"
	"fmt"
	"iter"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/render"
	"github.com/unaquatic/fluid/style"
	"github.com/unaquatic/fluid/widget"
)

// TestGap checks the one spacing rule for every pair of kinds: rows stack under
// rows, and any other two rows are parted by a blank row. In particular nothing
// stacks under a footer, and a footer does not stack above a row.
func TestGap(t *testing.T) {
	kinds := []RowKind{KindBlock, KindRow, KindFooter}
	for _, above := range kinds {
		for _, below := range kinds {
			want := 1
			if above == KindRow && below == KindRow {
				want = 0
			}
			if got := Gap(above, below); got != want {
				t.Errorf("Gap(%d, %d) = %d, want %d", above, below, got, want)
			}
		}
	}
}

// TestKindOf checks the kind each widget of a bubble reports: a PartBox that
// shows as a row is a row, unless it is Apart, as the cache row is, which is a
// block, as is one that shows as a block; a footer is a footer, even while
// hidden, and anything else, nil too, is a block.
func TestKindOf(t *testing.T) {
	block := NewPartBox(nil, style.Color{}, style.ColorTransparent)
	block.Shape = ShapeBlock
	block.ShowShape()
	row := NewPartBox(nil, style.Color{}, style.ColorTransparent)
	row.Shape = ShapeRow
	row.ShowShape()
	tests := []struct {
		name string
		r    render.Renderable
		want RowKind
	}{
		{name: "row box", r: row, want: KindRow},
		{name: "cache row", r: NewCacheBox(), want: KindBlock},
		{name: "block box", r: block, want: KindBlock},
		{name: "footer", r: NewFooterBox(0, textFg), want: KindFooter},
		{name: "text", r: widget.NewText("x"), want: KindBlock},
	}
	for _, tt := range tests {
		if got := KindOf(tt.r); got != tt.want {
			t.Errorf("%s: kind = %d, want %d", tt.name, got, tt.want)
		}
	}
	if got := KindOf(nil); got != KindBlock {
		t.Errorf("nil: kind = %d, want %d", got, KindBlock)
	}
}

// sequences yields every string of at most maxLen letters of alphabet, the
// empty one first.
func sequences(alphabet string, maxLen int) iter.Seq[string] {
	return func(yield func(string) bool) {
		var walk func(prefix string) bool
		walk = func(prefix string) bool {
			if !yield(prefix) {
				return false
			}
			if len(prefix) == maxLen {
				return true
			}
			for _, c := range alphabet {
				if !walk(prefix + string(c)) {
					return false
				}
			}
			return true
		}
		walk("")
	}
}

// spacedRow is a row a bubble is expected to draw, found in the frame by the
// text it holds.
type spacedRow struct {
	// Marker is text that only this row draws.
	Marker string
	// Stacks reports that the row is a collapsed call: the kind of row that
	// stacks under another. A cache row is parted from the rows around it.
	Stacks bool
}

// TestBubbleSpacing draws bubbles of one or two replies, whose parts, cache
// rows, and footers vary, and checks the blank rows between the rows that show
// in the frame, not the margins that put them there: two rows touch when both
// are rows that stack, and are parted by one blank row otherwise. It holds for
// every row a reply can show, so a new kind of row that is wrong shows here.
func TestBubbleSpacing(t *testing.T) {
	usage := message.Usage{InputTokens: 1_200, CachedInputTokens: 800}
	for _, mode := range []CacheMode{CacheOff, CachePerMessage} {
		for _, worked := range []bool{false, true} {
			for first := range sequences("TR", 2) {
				for _, second := range append([]string{"-"}, slices.Collect(sequences("TR", 1))...) {
					name := fmt.Sprintf("%s/worked=%v/%s+%s", mode, worked, first, second)
					t.Run(name, func(t *testing.T) {
						checkBubbleSpacing(t, mode, worked, usage, first, second)
					})
				}
			}
		}
	}
}

// checkBubbleSpacing draws the reply of the parts that first spells, T for a
// text and R for a collapsed call, and the reply under it that second spells,
// unless it is "-", which adds none. Each reply reports usage, and the last
// ends a turn that took three seconds when worked is set.
func checkBubbleSpacing(t *testing.T, mode CacheMode, worked bool, usage message.Usage, first, second string) {
	t.Helper()
	ui := newInputTestUI(t)
	ui.inline = true
	ui.Apply(ui.service.SetCacheMode(mode))

	var want []spacedRow
	n := 0
	reply := func(id message.ID, seq string, last bool) ChatMessage {
		var parts []message.ContentPart
		for _, c := range seq {
			n++
			switch c {
			case 'T':
				parts = append(parts, message.TextPart{ID: message.PartID(fmt.Sprintf("%s-text-%d", id, n)), Text: fmt.Sprintf("text-%d", n)})
				want = append(want, spacedRow{Marker: fmt.Sprintf("text-%d", n)})
			case 'R':
				input := json.RawMessage(fmt.Sprintf(`{"command":"echo-%d"}`, n))
				parts = append(parts, message.ToolPart{ID: message.PartID(fmt.Sprintf("%s-tool-%d", id, n)), CallID: fmt.Sprintf("c%d", n), Name: "bash",
					State: message.ToolStateCompleted{Input: input, Output: "ok"}})
				want = append(want, spacedRow{Marker: fmt.Sprintf("echo-%d", n), Stacks: true})
			}
		}
		msg := agentParts(id, parts...)
		msg.Usage = usage
		if mode == CachePerMessage {
			want = append(want, spacedRow{Marker: "Cache read"})
		}
		if last && worked {
			msg.Worked = 3 * time.Second
			want = append(want, spacedRow{Marker: "Done ["})
		}
		return msg
	}
	a1 := reply("a1", first, second == "-")
	ui.AddMessage(a1)
	if second != "-" {
		a2 := reply("a2", second, true)
		a2.ParentID = "a1"
		ui.AddMessage(a2)
	}

	frame := renderFrames(60, 40, detachTranscript(ui)...).String()
	lines := strings.Split(frame, "\n")
	at := make([]int, len(want))
	cursor := 0
	for i, row := range want {
		found := -1
		for y := cursor; y < len(lines); y++ {
			if strings.Contains(lines[y], row.Marker) {
				found = y
				break
			}
		}
		if found < 0 {
			t.Fatalf("no row with %q, in order, in the frame:\n%s", row.Marker, frame)
		}
		at[i], cursor = found, found+1
	}

	shown := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			shown++
		}
	}
	if shown != len(want) {
		t.Errorf("frame draws %d rows, want %d:\n%s", shown, len(want), frame)
	}
	if len(want) > 0 && at[0] != 0 {
		t.Errorf("first row %q is at row %d, want the first:\n%s", want[0].Marker, at[0], frame)
	}
	for i := 1; i < len(want); i++ {
		wantGap := 1
		if want[i-1].Stacks && want[i].Stacks {
			wantGap = 0
		}
		if gap := at[i] - at[i-1] - 1; gap != wantGap {
			t.Errorf("%q is %d blank rows under %q, want %d:\n%s", want[i].Marker, gap, want[i-1].Marker, wantGap, frame)
		}
	}
}
