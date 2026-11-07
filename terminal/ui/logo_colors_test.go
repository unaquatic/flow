package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/unaquatic/flow/product"
	"github.com/unaquatic/fluid/style"
)

// plainLines returns the text of lines without their styling.
func plainLines(lines []style.StyledText) []string {
	rows := make([]string, len(lines))
	for i, line := range lines {
		rows[i] = line.PlainText()
	}
	return rows
}

// TestJoinColumns checks that each block but the last is padded to its widest
// line and then by the gap, and that a short block ends in blank lines.
func TestJoinColumns(t *testing.T) {
	block := func(lines ...string) []style.StyledText {
		out := make([]style.StyledText, len(lines))
		for i, l := range lines {
			out[i] = style.NewStyledText(style.Plain(l))
		}
		return out
	}
	got := plainLines(JoinColumns([][]style.StyledText{block("ab", "c"), block("d"), block("ef", "g")}, 1))
	if want := []string{"ab d ef", "c    g"}; !slices.Equal(got, want) {
		t.Errorf("joined = %q, want %q", got, want)
	}
}

// TestLogoColorTile checks that a tile is the logo, its "f" in the tile's
// color, over the line of Go that sets that color, its const keyword in the
// color too.
func TestLogoColorTile(t *testing.T) {
	const hex = "#ff8700"
	tile := LogoColorTile(hex)
	want := append(strings.Split(product.Logo, "\n"), `const LogoInitialColor = "#ff8700"`)
	if got := plainLines(tile); !slices.Equal(got, want) {
		t.Fatalf("tile =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	c := style.ColorFromHex(hex)
	for i, line := range tile {
		if fg := line.Chunks[0].Fg; fg != c {
			t.Errorf("line %d starts in %v, want %s", i, fg, hex)
		}
	}
}

// TestColorStep checks that the zero step keeps a color exactly, and that the
// others turn the hue, or lower the saturation or the value, of #ff5f00.
func TestColorStep(t *testing.T) {
	base := style.ColorFromHex("#ff5f00")
	for _, tt := range []struct {
		step ColorStep
		want string
	}{
		{ColorStep{}, "#ff5f00"},
		{ColorStep{Hue: -4}, "#ff4e00"},
		{ColorStep{Hue: 4}, "#ff7000"},
		{ColorStep{Value: -0.2}, "#cc4c00"},
		{ColorStep{Saturation: -0.3}, "#ff8f4c"},
		// Saturation and value stay from 0 through 1.
		{ColorStep{Saturation: 1, Value: 1}, "#ff5f00"},
		{ColorStep{Saturation: -2}, "#ffffff"},
	} {
		if got := tt.step.Apply(base).ToHex(); got != tt.want {
			t.Errorf("%+v applied to #ff5f00 = %s, want %s", tt.step, got, tt.want)
		}
	}
}

// TestLogoColorRows checks that the rows start with the color alone, then
// hold a pair of colors made from it each, all of them different.
func TestLogoColorRows(t *testing.T) {
	rows := LogoColorRows(product.LogoInitialColor)
	if len(rows) != len(LogoColorSteps) {
		t.Fatalf("%d rows, want %d", len(rows), len(LogoColorSteps))
	}
	if !slices.Equal(rows[0], []string{product.LogoInitialColor}) {
		t.Errorf("first row = %q, want the primary color alone", rows[0])
	}
	seen := make(map[string]bool)
	for i, row := range rows {
		if i > 0 && len(row) != 2 {
			t.Errorf("row %d = %q, want a pair", i, row)
		}
		for _, hex := range row {
			if seen[hex] {
				t.Errorf("%s shows twice", hex)
			}
			seen[hex] = true
		}
	}
}

// TestShowStatusComparesLogoColors checks that /status draws a tile for each
// of the LogoColorRows of the primary color on the canvas, with a blank line
// between rows, and the session info under them.
func TestShowStatusComparesLogoColors(t *testing.T) {
	s, _ := newTestService(t)
	s.InitSession(t.Context(), "")

	events := s.ShowStatus()

	if len(events) != 2 {
		t.Fatalf("ShowStatus returned %d events, want the logos and the notice", len(events))
	}
	logos, ok := events[0].(EventCanvasShow)
	if !ok {
		t.Fatalf("first event is %+v, want the logos on the canvas", events[0])
	}
	tileHeight := len(LogoColorTile(product.LogoInitialColor))
	rows := LogoColorRows(product.LogoInitialColor)
	if want := len(rows)*tileHeight + len(rows) - 1; len(logos.Lines) != want {
		t.Errorf("canvas has %d lines, want %d", len(logos.Lines), want)
	}
	text := strings.Join(plainLines(logos.Lines), "\n")
	for _, hex := range slices.Concat(rows...) {
		if line := fmt.Sprintf("const LogoInitialColor = %q", hex); !strings.Contains(text, line) {
			t.Errorf("canvas has no line %s", line)
		}
	}
	if toast, ok := events[1].(EventToastShow); !ok || !toast.System {
		t.Errorf("second event is %+v, want the session info notice", events[1])
	}
}
