package ignore

import (
	"io/fs"
	"slices"
	"testing"
)

const (
	file = fs.FileMode(0)
	dir  = fs.ModeDir
)

func TestParsePattern(t *testing.T) {
	tests := []struct {
		name string
		line string
		want Pattern
		ok   bool
	}{
		{name: "empty", line: ""},
		{name: "spaces only", line: "   "},
		{name: "comment", line: "# note"},
		{name: "bare negation", line: "!"},
		{name: "bare slash", line: "/"},
		{name: "plain", line: "*.log", want: Pattern{Glob: "*.log"}, ok: true},
		{name: "negated", line: "!keep.log", want: Pattern{Glob: "keep.log", Negate: true}, ok: true},
		{name: "directory only", line: "build/", want: Pattern{Glob: "build", DirOnly: true}, ok: true},
		{name: "anchored", line: "/root", want: Pattern{Glob: "/root"}, ok: true},
		{name: "quoted hash", line: `\#hash`, want: Pattern{Glob: `\#hash`}, ok: true},
		{name: "quoted bang", line: `\!bang`, want: Pattern{Glob: `\!bang`}, ok: true},
		{name: "leading space before hash", line: " #x", want: Pattern{Glob: " #x"}, ok: true},
		{name: "trailing spaces", line: "trail   ", want: Pattern{Glob: "trail"}, ok: true},
		{name: "quoted trailing space", line: `space\ `, want: Pattern{Glob: `space\ `}, ok: true},
		{name: "quoted then unquoted space", line: `space\  `, want: Pattern{Glob: `space\ `}, ok: true},
		{name: "trailing backslash", line: `end\`, want: Pattern{Glob: `end\`}, ok: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParsePattern(tt.line, "")
			if ok != tt.ok || got != tt.want {
				t.Errorf("ParsePattern(%q) = %+v, %t; want %+v, %t", tt.line, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestParse(t *testing.T) {
	data := []byte("\ufeff*.log\r\n# comment\r\n\r\n!keep.log\nbuild/")
	want := []Pattern{
		{Dir: "sub", Glob: "*.log"},
		{Dir: "sub", Glob: "keep.log", Negate: true},
		{Dir: "sub", Glob: "build", DirOnly: true},
	}
	if got := Parse(data, "sub"); !slices.Equal(got, want) {
		t.Errorf("Parse = %+v; want %+v", got, want)
	}
}

func TestMatchSegment(t *testing.T) {
	tests := []struct {
		glob, name string
		want       bool
	}{
		{"*.go", "a.go", true},
		{"*.go", "a.go.txt", false},
		{"a*b*c", "aXbYc", true},
		{"a*b*c", "aXbY", false},
		{"**", "anything", true},
		{"?", "a", true},
		{"?", "ab", false},
		{"[abc]", "b", true},
		{"[abc]", "d", false},
		{"[!abc]", "d", true},
		{"[^abc]", "a", false},
		{"[a-c]x", "bx", true},
		{"[a-c]x", "dx", false},
		{"[]]", "]", true},
		{"[]a]", "a", true},
		{"[-a]", "-", true},
		{"[a-]", "-", true},
		{`[\]]`, "]", true},
		{`[a\-z]`, "-", true},
		{`[a\-z]`, "b", false},
		{"[[:digit:]]*", "7up", true},
		{"[[:digit:]]*", "up", false},
		{"[[:space:]]", "\v", false},
		{"[[:bogus:]]", "b", false},
		{"[abc", "a", false},
		{`\*`, "*", true},
		{`\*`, "a", false},
		{`\`, `\`, false},
	}
	for _, tt := range tests {
		t.Run(tt.glob+" "+tt.name, func(t *testing.T) {
			if got := matchSegment(tt.glob, tt.name); got != tt.want {
				t.Errorf("matchSegment(%q, %q) = %t; want %t", tt.glob, tt.name, got, tt.want)
			}
		})
	}
}

func TestMatchPath(t *testing.T) {
	tests := []struct {
		glob, name string
		want       bool
	}{
		{"a/**", "a/b/c", true},
		{"a/**", "a", false},
		{"a/**/b", "a/b", true},
		{"a/**/b", "a/x/y/b", true},
		{"a/**/b", "a/x/c", false},
		{"**/b", "b", true},
		{"**/b", "x/y/b", true},
		{"a/*/c", "a/b/c", true},
		{"a/*/c", "a/b/x/c", false},
		{"a/b", "a/b/c", false},
	}
	for _, tt := range tests {
		t.Run(tt.glob+" "+tt.name, func(t *testing.T) {
			if got := matchPath(tt.glob, tt.name); got != tt.want {
				t.Errorf("matchPath(%q, %q) = %t; want %t", tt.glob, tt.name, got, tt.want)
			}
		})
	}
}

func TestPatternMatch(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		dir     string
		path    string
		typ     fs.FileMode
		matches bool
	}{
		{"base name at root", "*.log", "", "x.log", file, true},
		{"base name at depth", "*.log", "", "a/b/x.log", file, true},
		{"anchored at root", "/build", "", "build", dir, true},
		{"anchored not deeper", "/build", "", "a/build", dir, false},
		{"directory only skips file", "tmp/", "", "tmp", file, false},
		{"directory only takes dir", "tmp/", "", "tmp", dir, true},
		{"inner slash anchors", "docs/*.md", "", "docs/x.md", file, true},
		{"inner slash not deeper", "docs/*.md", "", "a/docs/x.md", file, false},
		{"star stays in segment", "docs/*.md", "", "docs/a/x.md", file, false},
		{"nested file applies below", "*.gen", "sub", "sub/x.gen", file, true},
		{"nested file not above", "*.gen", "sub", "x.gen", file, false},
		{"nested file not in sibling prefix", "*.gen", "sub", "subx/y.gen", file, false},
		{"nested file not its own dir", "sub", "sub", "sub", dir, false},
		{"nested anchor", "/local", "sub", "sub/local", file, true},
		{"nested anchor not deeper", "/local", "sub", "sub/a/local", file, false},
		{"negation ignored by Match", "!keep", "", "keep", file, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := ParsePattern(tt.line, tt.dir)
			if !ok {
				t.Fatalf("ParsePattern(%q) failed", tt.line)
			}
			if got := p.Match(tt.path, tt.typ); got != tt.matches {
				t.Errorf("%+v.Match(%q) = %t; want %t", p, tt.path, got, tt.matches)
			}
		})
	}
}

func TestMatcherMatch(t *testing.T) {
	tests := []struct {
		name    string
		m       Matcher
		path    string
		ignored bool
	}{
		{"empty", nil, "a.txt", false},
		{"excluded", Parse([]byte("*.txt\n!keep.txt"), ""), "a.txt", true},
		{"re-included", Parse([]byte("*.txt\n!keep.txt"), ""), "keep.txt", false},
		{"last pattern wins", Parse([]byte("!keep.txt\n*.txt"), ""), "keep.txt", true},
		{"deeper file overrides", nested(), "sub/x.gen", false},
		{"deeper file leaves others", nested(), "sub/y.gen", true},
		{"deeper file not above", nested(), "x.gen", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.m.Match(tt.path, file); got != tt.ignored {
				t.Errorf("Match(%q) = %t; want %t", tt.path, got, tt.ignored)
			}
		})
	}
}

// nested returns a root .gitignore followed by one in "sub".
func nested() Matcher {
	return Parse([]byte("*.gen"), "").With(Parse([]byte("!x.gen"), "sub")...)
}

// TestMatcherWith_LeavesTheReceiverAlone checks that sibling directories
// extending one matcher do not see each other's patterns.
func TestMatcherWith_LeavesTheReceiverAlone(t *testing.T) {
	// Spare capacity, so a With that appended in place would share it.
	base := append(make(Matcher, 0, 8), Parse([]byte("*.gen"), "")...)
	a := base.With(Parse([]byte("!x.gen"), "a")...)
	b := base.With(Parse([]byte("*.txt"), "b")...)

	if a.Match("b/x.txt", file) || !b.Match("b/x.txt", file) {
		t.Error("b's pattern leaked into a, or is missing from b")
	}
	if !b.Match("a/x.gen", file) || a.Match("a/x.gen", file) {
		t.Error("a's pattern leaked into b, or is missing from a")
	}
	if len(base) != 1 {
		t.Errorf("base holds %d patterns after With, want 1", len(base))
	}
}
