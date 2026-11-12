package skills

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
)

// TestSplitFrontmatter checks that each form of scalar a SKILL.md writes
// reads as YAML reads it, that mappings read as empty, and that the body is
// what follows the closing fence.
func TestSplitFrontmatter(t *testing.T) {
	text := strings.Join([]string{
		"---",
		"name: review",
		"# a comment",
		"plain: Use when the user asks; for: reviews # trailing comment",
		"plain-lines: first line",
		"  second line",
		"",
		"  after a blank",
		`double: "Say \"hi\"\tthen\u00e9 \\ end"`,
		`single: 'It''s here: yes'`,
		"folded: >-",
		"  Folded text that",
		"  goes on.",
		"",
		"  New paragraph.",
		"literal: |",
		"  Line one",
		"",
		"  - item",
		"keep: |+",
		"  kept",
		"",
		"metadata:",
		"  author: someone",
		"  version: \"1.0\"",
		"disable-model-invocation: true",
		"---",
		"# Body",
		"",
		"Text.",
	}, "\r\n")
	fm, body, err := SplitFrontmatter(text)
	if err != nil {
		t.Fatal(err)
	}
	want := Frontmatter{
		"name":                     "review",
		"plain":                    "Use when the user asks; for: reviews",
		"plain-lines":              "first line second line\nafter a blank",
		"double":                   "Say \"hi\"\tthené \\ end",
		"single":                   "It's here: yes",
		"folded":                   "Folded text that goes on.\nNew paragraph.",
		"literal":                  "Line one\n\n- item\n",
		"keep":                     "kept\n\n",
		"metadata":                 "",
		"disable-model-invocation": "true",
	}
	for k, v := range want {
		if fm[k] != v {
			t.Errorf("%s = %q, want %q", k, fm[k], v)
		}
	}
	if got, want := slices.Sorted(maps.Keys(fm)), slices.Sorted(maps.Keys(want)); !slices.Equal(got, want) {
		t.Errorf("keys = %v, want %v", got, want)
	}
	if !fm.Bool("disable-model-invocation") || fm.Bool("name") {
		t.Error("Bool does not read true as YAML does")
	}
	if want := "# Body\n\nText."; body != want {
		t.Errorf("body = %q, want %q", body, want)
	}
}

// TestSplitFrontmatterNone checks that text without an opening fence has no
// frontmatter, and is the body whole.
func TestSplitFrontmatterNone(t *testing.T) {
	fm, body, err := SplitFrontmatter("# Title\n---\nname: x\n")
	if err != nil || fm != nil || body != "# Title\n---\nname: x\n" {
		t.Errorf("SplitFrontmatter = %v, %q, %v; want no frontmatter and the whole text", fm, body, err)
	}
}

// TestSplitFrontmatterErrors checks that frontmatter that never closes, or
// holds a line that is not a key, or a quote that does not close, fails.
func TestSplitFrontmatterErrors(t *testing.T) {
	if _, _, err := SplitFrontmatter("---\nname: x\n"); !errors.Is(err, ErrUnterminated) {
		t.Errorf("unterminated: err = %v, want ErrUnterminated", err)
	}
	for _, text := range []string{
		"---\njust text\n---\n",
		"---\ndescription: \"open\n---\n",
		"---\ndescription: 'open\n---\n",
		"---\ndescription: \"done\" extra\n---\n",
		"---\ndescription: \"bad \\q\"\n---\n",
	} {
		if _, _, err := SplitFrontmatter(text); err == nil {
			t.Errorf("SplitFrontmatter(%q) = nil error, want one", text)
		}
	}
}
