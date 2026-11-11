package product

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// TestLogoShape checks that the logo keeps the size its documentation gives,
// leaves its top pixel row blank, uses only full blocks, half blocks and
// spaces, and has no trailing spaces or newline.
func TestLogoShape(t *testing.T) {
	lines := strings.Split(Logo, "\n")
	if len(lines) != 8 {
		t.Errorf("logo has %d lines, want 8", len(lines))
	}
	if strings.ContainsAny(lines[0], "█▀") {
		t.Errorf("first line fills the top pixel row: %q", lines[0])
	}
	width := 0
	for i, line := range lines {
		width = max(width, utf8.RuneCountInString(line))
		if strings.TrimRight(line, " ") != line {
			t.Errorf("line %d has trailing spaces: %q", i+1, line)
		}
		if strings.Trim(line, "█▀▄ ") != "" {
			t.Errorf("line %d holds a character other than a block or a space: %q", i+1, line)
		}
	}
	if width != 30 {
		t.Errorf("logo is %d columns wide, want 30", width)
	}
}

// TestIconShape checks that the icon is a square of 16 by 16 pixels, 16
// columns on each of its 8 lines, with blank top and bottom pixel rows, and
// uses only full blocks, half blocks and spaces.
func TestIconShape(t *testing.T) {
	lines := strings.Split(Icon, "\n")
	if len(lines) != 8 {
		t.Fatalf("icon has %d lines, want 8", len(lines))
	}
	for i, line := range lines {
		if n := utf8.RuneCountInString(line); n != 16 {
			t.Errorf("line %d is %d columns wide, want 16: %q", i+1, n, line)
		}
		if strings.Trim(line, "█▀▄ ") != "" {
			t.Errorf("line %d holds a character other than a block or a space: %q", i+1, line)
		}
	}
	if strings.ContainsAny(lines[0], "█▀") {
		t.Errorf("first line fills the top pixel row: %q", lines[0])
	}
	if last := lines[len(lines)-1]; strings.ContainsAny(last, "█▄") {
		t.Errorf("last line fills the bottom pixel row: %q", last)
	}
}

// TestLogoParts checks that LogoParts cuts every line of Logo in order, on
// the blank column before the "l", and that joining the parts gives the line
// back.
func TestLogoParts(t *testing.T) {
	var got []string
	for f, rest := range LogoParts() {
		if utf8.RuneCountInString(f) > logoInitialWidth {
			t.Errorf("the f part %q is wider than %d columns", f, logoInitialWidth)
		}
		if rest != "" && rest[0] != ' ' {
			t.Errorf("line %d is cut inside a letter: %q, %q", len(got)+1, f, rest)
		}
		got = append(got, f+rest)
	}
	if want := strings.Split(Logo, "\n"); !slices.Equal(got, want) {
		t.Errorf("joined parts are %q, want %q", got, want)
	}
	for f, rest := range LogoParts() {
		if f != "    ▄▄▄" || rest != " ▄▄▄" {
			t.Errorf("first line cut into %q and %q, want %q and %q", f, rest, "    ▄▄▄", " ▄▄▄")
		}
		break
	}
}
