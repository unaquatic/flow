package ui

import (
	"testing"
	"time"
)

func TestFormatWorked(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "Done [0s]"},
		{-time.Second, "Done [0s]"},
		{400 * time.Millisecond, "Done [0s]"},
		{500 * time.Millisecond, "Done [1s]"},
		{42 * time.Second, "Done [42s]"},
		{59*time.Second + 600*time.Millisecond, "Done [1m 00s]"},
		{65 * time.Second, "Done [1m 05s]"},
		{59*time.Minute + 59*time.Second, "Done [59m 59s]"},
		{time.Hour + 2*time.Minute + 5*time.Second, "Done [1h 02m 05s]"},
		{26 * time.Hour, "Done [26h 00m 00s]"},
	}
	for _, tt := range tests {
		if got := FormatWorked(tt.d); got != tt.want {
			t.Errorf("FormatWorked(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

// TestFooterText checks that a reply's footer shows the time its turn took,
// and then any cache use it is given, all in the row's color, and that a
// reply whose time rounds to zero, as one that does not end a turn, has none.
func TestFooterText(t *testing.T) {
	tests := []struct {
		name   string
		worked time.Duration
		cache  string
		want   string
	}{
		{name: "seconds", worked: 42 * time.Second, want: "Done [42s]"},
		{name: "minutes", worked: 107 * time.Second, want: "Done [1m 47s]"},
		{name: "cache use", worked: 39 * time.Second, cache: "[cache 22K write 0K]", want: "Done [39s] [cache 22K write 0K]"},
		{name: "not the turn's end", cache: "[cache 22K write 0K]"},
		{name: "under half a second", worked: 400 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			text := FooterText(tt.worked, tt.cache)
			if got := text.PlainText(); got != tt.want {
				t.Errorf("FooterText(%v, %q) = %q, want %q", tt.worked, tt.cache, got, tt.want)
			}
			for _, c := range text.Chunks {
				if !c.Fg.IsTransparent() {
					t.Errorf("chunk %q has its own color %v, want the row's", c.Text, c.Fg)
				}
			}
		})
	}
}

// TestFooterBoxShowsOnceTurnEnds checks that a reply's footer stays hidden
// until the reply ends a turn, also with a cache use to show, and shows the
// turn's time from then on, in the color it is given.
func TestFooterBoxShowsOnceTurnEnds(t *testing.T) {
	textFg := mutedFg
	b := NewFooterBox(0, textFg)
	b.SetCache("[cache 1.0K write 0K]")
	if b.Visible() {
		t.Errorf("footer %q shown before the turn ended", b.Text.PlainText())
	}
	b.SetCache("")
	b.SetTurnEnd(42 * time.Second)
	if got, want := b.Text.PlainText(), "Done [42s]"; !b.Visible() || got != want {
		t.Errorf("footer = %q, shown %v; want %q shown", got, b.Visible(), want)
	}
	if got := b.Text.Fg(); got != textFg {
		t.Errorf("footer fg = %v; want %v", got, textFg)
	}
	b.SetTurnEnd(0)
	if b.Visible() {
		t.Error("footer still shown once the reply no longer ends a turn")
	}
}

// TestFooterMarker checks that a reply's footer shows ToolMarker while the
// tool marker is on, though tool calls are expanded: it has nothing to
// expand, as a cache row has not.
func TestFooterMarker(t *testing.T) {
	for _, tt := range []struct {
		name             string
		marker, expanded bool
		want             bool
	}{
		{name: "off", want: false},
		{name: "on", marker: true, want: true},
		{name: "on expanded", marker: true, expanded: true, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			msg := agentText("a1", "done")
			msg.Worked = 3 * time.Second
			cm := NewChatMessageBox(msg, ChatMessageWithToolMarkers(tt.marker))
			cm.SetToolsExpanded(tt.expanded)
			if got := cm.segments[0].Footer.Marker.Visible(); got != tt.want {
				t.Errorf("footer marker shows = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCacheRowMarkerWhenExpanded checks that a cache row, which has nothing
// to expand, keeps ToolMarker while tool calls are expanded.
func TestCacheRowMarkerWhenExpanded(t *testing.T) {
	b := NewCacheBox()
	b.SetMarker(true)
	b.SetExpanded(true)
	if !b.Marker.Visible() {
		t.Error("cache row marker hidden while expanded, want it shown")
	}
}
