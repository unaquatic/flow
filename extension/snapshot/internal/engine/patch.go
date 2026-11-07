package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/unaquatic/flow/extension/snapshot/internal/engine/internal/diff"
)

// Patch returns the differences between the trees from and to as a patch in
// the format of git diff --no-renames with core.quotePath off. Its hunks come
// from an anchored diff and name no enclosing function, so they can differ
// from Git's.
func (o Objects) Patch(ctx context.Context, from, to Hash) (string, error) {
	var b strings.Builder
	for c, err := range o.Changes(ctx, from, to) {
		if err != nil {
			return "", err
		}
		for _, part := range splitTypeChange(c) {
			if err := o.writePatch(ctx, &b, part); err != nil {
				return "", err
			}
		}
	}
	return b.String(), nil
}

// splitTypeChange returns c as Git shows it in a patch: a change between a
// symbolic link and a file is a deletion followed by an addition.
func splitTypeChange(c Change) []Change {
	if !c.Old.Exists() || !c.New.Exists() || (c.Old.Mode == ModeSymlink) == (c.New.Mode == ModeSymlink) {
		return []Change{c}
	}
	return []Change{
		{Path: c.Path, Old: c.Old},
		{Path: c.Path, New: c.New},
	}
}

// writePatch writes the patch for the change c to b.
func (o Objects) writePatch(ctx context.Context, b *strings.Builder, c Change) error {
	before, err := o.content(ctx, c.Old)
	if err != nil {
		return err
	}
	after, err := o.content(ctx, c.New)
	if err != nil {
		return err
	}
	oldName, newName := quotePath("a/", c.Path), quotePath("b/", c.Path)
	fmt.Fprintf(b, "diff --git %s %s\n", oldName, newName)
	writeModeLines(b, c)
	if c.Old.Hash == c.New.Hash {
		return nil // the mode alone changed
	}
	writeIndexLine(b, c)

	if !c.Old.Exists() {
		oldName = "/dev/null"
	}
	if !c.New.Exists() {
		newName = "/dev/null"
	}
	if IsBinary(before) || IsBinary(after) {
		fmt.Fprintf(b, "Binary files %s and %s differ\n", oldName, newName)
		return nil
	}
	hunks := diff.Diff(before, after)
	if len(hunks) == 0 {
		return nil // an empty file was added or deleted
	}
	fmt.Fprintf(b, "--- %s%s\n+++ %s%s\n", oldName, labelEnd(oldName), newName, labelEnd(newName))
	for _, h := range hunks {
		b.WriteString(h.String())
	}
	return nil
}

// writeModeLines writes the header lines that give the file modes of c.
func writeModeLines(b *strings.Builder, c Change) {
	switch {
	case !c.Old.Exists():
		fmt.Fprintf(b, "new file mode %06o\n", c.New.Mode)
	case !c.New.Exists():
		fmt.Fprintf(b, "deleted file mode %06o\n", c.Old.Mode)
	case c.Old.Mode != c.New.Mode:
		fmt.Fprintf(b, "old mode %06o\nnew mode %06o\n", c.Old.Mode, c.New.Mode)
	}
}

// writeIndexLine writes the line naming the blobs of c, abbreviated to
// ShortHashLen digits, and the mode when it is the same on both sides.
func writeIndexLine(b *strings.Builder, c Change) {
	fmt.Fprintf(b, "index %s..%s", ShortHash(c.Old.Hash), ShortHash(c.New.Hash))
	if c.Old.Mode == c.New.Mode {
		fmt.Fprintf(b, " %06o", c.Old.Mode)
	}
	b.WriteByte('\n')
}

// labelEnd returns what Git appends to a "---" or "+++" label: a tab when
// the label holds a space, so the label's end stays visible.
func labelEnd(label string) string {
	if strings.Contains(label, " ") {
		return "\t"
	}
	return ""
}

// quotePath returns prefix+p as Git shows a path with core.quotePath off: in
// double quotes, with C escapes, when it holds a control character, a double
// quote, or a backslash, and as it is otherwise.
func quotePath(prefix, p string) string {
	s := prefix + p
	if !strings.ContainsFunc(s, needsQuote) {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := range len(s) {
		writeQuoted(&b, s[i])
	}
	b.WriteByte('"')
	return b.String()
}

// needsQuote reports whether r makes Git quote a path.
func needsQuote(r rune) bool {
	return r < ' ' || r == 0x7f || r == '"' || r == '\\'
}

// cEscapes holds the C escapes Git uses in quoted paths.
var cEscapes = map[byte]string{
	'\a': `\a`, '\b': `\b`, '\t': `\t`, '\n': `\n`, '\v': `\v`, '\f': `\f`, '\r': `\r`,
	'"': `\"`, '\\': `\\`,
}

// writeQuoted writes c as it appears inside a quoted path.
func writeQuoted(b *strings.Builder, c byte) {
	if esc, ok := cEscapes[c]; ok {
		b.WriteString(esc)
		return
	}
	if c < ' ' || c == 0x7f {
		fmt.Fprintf(b, `\%03o`, c)
		return
	}
	b.WriteByte(c)
}
