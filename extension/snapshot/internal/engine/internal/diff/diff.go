// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package diff computes line differences between two texts.
//
// It is adapted from the Go standard library's internal/diff. The algorithm
// is unchanged; Diff returns structured hunks instead of formatted text, and
// Hunk.String numbers hunks the way Git does.
package diff

import (
	"bytes"
	"slices"
	"strconv"
	"strings"
)

// contextLines is the number of unchanged lines kept around each change.
const contextLines = 3

// noEOL is the marker that follows a line ending its text without a newline,
// as BSD and GNU diff and Git print it.
const noEOL = "\n\\ No newline at end of file\n"

// Op says what a line of a hunk does.
type Op byte

const (
	Equal  Op = ' ' // the line is in both texts
	Delete Op = '-' // the line is only in the old text
	Insert Op = '+' // the line is only in the new text
)

// Line is one line of a hunk.
type Line struct {
	Op Op

	// Text is the line without its newline.
	Text string

	// NoEOL reports whether the line ends its text without a newline.
	NoEOL bool
}

// String returns l as a unified diff shows it: its Op, its text, and a
// newline, followed by the "\ No newline at end of file" marker when NoEOL
// is set.
func (l Line) String() string {
	if l.NoEOL {
		return string(l.Op) + l.Text + noEOL
	}
	return string(l.Op) + l.Text + "\n"
}

// Hunk is a run of changed lines together with the unchanged lines around it.
type Hunk struct {
	// OldStart and NewStart are the 1-based numbers of the hunk's first line
	// in the old and new texts. When a side has no lines in the hunk, its
	// start is the number of the line before, as in Git.
	OldStart, NewStart int

	// OldLines and NewLines count the hunk's lines on each side.
	OldLines, NewLines int

	// Lines holds the hunk's lines in order.
	Lines []Line
}

// String returns h in unified diff format, starting with its "@@" header. A
// count of one is omitted from the header, as Git does.
func (h Hunk) String() string {
	var b strings.Builder
	b.WriteString("@@ -")
	writeRange(&b, h.OldStart, h.OldLines)
	b.WriteString(" +")
	writeRange(&b, h.NewStart, h.NewLines)
	b.WriteString(" @@\n")
	for _, line := range h.Lines {
		b.WriteString(line.String())
	}
	return b.String()
}

// Stat returns the number of inserted and deleted lines in hunks.
func Stat(hunks []Hunk) (inserted, deleted int) {
	for _, h := range hunks {
		for _, line := range h.Lines {
			switch line.Op {
			case Insert:
				inserted++
			case Delete:
				deleted++
			}
		}
	}
	return inserted, deleted
}

// Diff returns the hunks of an anchored diff from old to new. It returns nil
// when old and new are identical.
//
// Unix diff implementations typically look for a diff with
// the smallest number of lines inserted and removed,
// which can in the worst case take time quadratic in the
// number of lines in the texts. As a result, many implementations
// either can be made to run for a long time or cut off the search
// after a predetermined amount of work.
//
// In contrast, this implementation looks for a diff with the
// smallest number of “unique” lines inserted and removed,
// where unique means a line that appears just once in both old and new.
// We call this an “anchored diff” because the unique lines anchor
// the chosen matching regions. An anchored diff is usually clearer
// than a standard diff, because the algorithm does not try to
// reuse unrelated blank lines or closing braces.
// The algorithm also guarantees to run in O(n log n) time
// instead of the standard O(n²) time.
func Diff(old, new []byte) []Hunk {
	if bytes.Equal(old, new) {
		return nil
	}
	x, y := lines(old), lines(new)
	eof := pair{len(x), len(y)}
	runs := commonRuns(x, y)

	// The first run is the common prefix. Its last lines lead into the
	// first hunk.
	h := openHunk(x, runs[0].end, min(runs[0].end.x, contextLines))
	var hunks []Hunk
	done := runs[0].end
	for _, r := range runs[1:] {
		appendLines(&h, Delete, x[done.x:r.start.x])
		appendLines(&h, Insert, y[done.y:r.start.y])
		done = r.end

		// A short run between changes joins the hunks on either side.
		n := r.end.x - r.start.x
		if r.end != eof && n < 2*contextLines {
			appendLines(&h, Equal, x[r.start.x:r.end.x])
			continue
		}
		appendLines(&h, Equal, x[r.start.x:r.start.x+min(n, contextLines)])
		hunks = append(hunks, closeHunk(h))
		if r.end != eof {
			h = openHunk(x, r.end, contextLines)
		}
	}
	return hunks
}

// A pair is a pair of values tracked for both the x and y side of a diff.
// It is typically a pair of line indexes.
type pair struct{ x, y int }

// A run is a maximal stretch of equal lines: x[start.x:end.x] equals
// y[start.y:end.y].
type run struct{ start, end pair }

// commonRuns extends each anchor from tgs to a maximal run of equal lines,
// skipping anchors that an earlier run already covers. The first run starts
// at the top of both texts and the last ends at the bottom of both; either
// may be empty.
func commonRuns(x, y []string) []run {
	eof := pair{len(x), len(y)}
	var (
		runs []run
		done pair // lines before done belong to earlier runs or changes
	)
	for _, m := range tgs(x, y) {
		if m.x < done.x {
			continue
		}
		r := extend(x, y, m, done)
		runs = append(runs, r)
		if r.end == eof {
			break
		}
		done = r.end
	}
	return runs
}

// extend grows the anchor m backward, no further than done, and forward
// while the lines of x and y agree.
func extend(x, y []string, m, done pair) run {
	start := m
	for start.x > done.x && start.y > done.y && x[start.x-1] == y[start.y-1] {
		start.x--
		start.y--
	}
	end := m
	for end.x < len(x) && end.y < len(y) && x[end.x] == y[end.y] {
		end.x++
		end.y++
	}
	return run{start, end}
}

// openHunk starts a hunk whose leading context is the lead lines of x before
// end.
func openHunk(x []string, end pair, lead int) Hunk {
	h := Hunk{OldStart: end.x - lead + 1, NewStart: end.y - lead + 1}
	appendLines(&h, Equal, x[end.x-lead:end.x])
	return h
}

// closeHunk finishes h. A side without lines is numbered by the line before
// the hunk.
func closeHunk(h Hunk) Hunk {
	if h.OldLines == 0 {
		h.OldStart--
	}
	if h.NewLines == 0 {
		h.NewStart--
	}
	return h
}

// appendLines adds lines, as the lines function splits them, to h with op,
// and counts them on the sides op touches.
func appendLines(h *Hunk, op Op, lines []string) {
	for _, s := range lines {
		text, missing := strings.CutSuffix(s, noEOL)
		if !missing {
			text = strings.TrimSuffix(s, "\n")
		}
		h.Lines = append(h.Lines, Line{Op: op, Text: text, NoEOL: missing})
	}
	if op != Insert {
		h.OldLines += len(lines)
	}
	if op != Delete {
		h.NewLines += len(lines)
	}
}

// writeRange writes one side of a hunk header.
func writeRange(b *strings.Builder, start, count int) {
	b.WriteString(strconv.Itoa(start))
	if count != 1 {
		b.WriteByte(',')
		b.WriteString(strconv.Itoa(count))
	}
}

// lines returns the lines in the file x, including newlines.
// If the file does not end in a newline, one is supplied
// along with a warning about the missing newline.
func lines(x []byte) []string {
	l := strings.SplitAfter(string(x), "\n")
	if l[len(l)-1] == "" {
		return l[:len(l)-1]
	}
	// Treat last line as having a message about the missing newline attached,
	// using the same text as BSD/GNU diff (including the leading backslash).
	l[len(l)-1] += noEOL
	return l
}

// tgs returns the pairs of indexes of the longest common subsequence
// of unique lines in x and y, where a unique line is one that appears
// once in x and once in y. The sequence starts with {0, 0} and ends with
// {len(x), len(y)}.
//
// The longest common subsequence algorithm is as described in
// Thomas G. Szymanski, “A Special Case of the Maximal Common
// Subsequence Problem,” Princeton TR #170 (January 1975),
// available at https://research.swtch.com/tgs170.pdf.
func tgs(x, y []string) []pair {
	xi, yi, inv := uniqueLines(x, y)

	// Apply Algorithm A from Szymanski's paper.
	// In those terms, A = J = inv and B = [0, n).
	n := len(xi)
	tops := make([]int, n)
	lengths := make([]int, n)
	for i := range tops {
		tops[i] = n + 1
	}
	for i, j := range inv {
		k, _ := slices.BinarySearch(tops, j)
		tops[k] = j
		lengths[i] = k + 1
	}
	k := 0
	for _, v := range lengths {
		k = max(k, v)
	}

	// Walk back through the subsequence. The last index with each length
	// before the previous pick always extends it.
	seq := make([]pair, 2+k)
	seq[1+k] = pair{len(x), len(y)} // sentinel at end
	for i, length := range slices.Backward(lengths) {
		if length == k {
			seq[k] = pair{xi[i], yi[inv[i]]}
			k--
		}
	}
	seq[0] = pair{0, 0} // sentinel at start
	return seq
}

// uniqueLines returns the indexes of the lines that appear exactly once in
// both x and y: xi and yi list them in increasing order, and x[xi[i]] equals
// y[yi[inv[i]]].
func uniqueLines(x, y []string) (xi, yi, inv []int) {
	// Count the number of times each string appears in a and b.
	// We only care about 0, 1, many, counted as 0, -1, -2
	// for the x side and 0, -4, -8 for the y side.
	// Using negative numbers now lets us distinguish positive line numbers later.
	m := make(map[string]int)
	for _, s := range x {
		if c := m[s]; c > -2 {
			m[s] = c - 1
		}
	}
	for _, s := range y {
		if c := m[s]; c > -8 {
			m[s] = c - 4
		}
	}

	// Now unique strings can be identified by m[s] = -1+-4.
	for i, s := range y {
		if m[s] == -1+-4 {
			m[s] = len(yi)
			yi = append(yi, i)
		}
	}
	for i, s := range x {
		if j, ok := m[s]; ok && j >= 0 {
			xi = append(xi, i)
			inv = append(inv, j)
		}
	}
	return xi, yi, inv
}
