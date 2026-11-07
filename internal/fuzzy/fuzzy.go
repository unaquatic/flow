// Package fuzzy matches short queries against strings as a user types,
// tolerating skipped letters and typos.
//
// A query matches a string in one of three tiers, tried in order: a word of
// the string starts with the query, the query's letters appear in the string
// in order, or the query is a few edits away from the start of a word. Words
// begin at the start of the string and after each separator: '/', '-', '.',
// '_' or a space. Matching is case-sensitive.
package fuzzy

import (
	"cmp"
	"iter"
	"slices"
	"strings"
)

// Tier is the way a query matched a string. Lower tiers rank first.
type Tier int

const (
	// Prefix means a word of the string starts with the query.
	Prefix Tier = iota
	// Subsequence means the query's letters appear in the string in order,
	// with gaps.
	Subsequence
	// Typo means the query is a few edits away from the start of a word.
	Typo
)

// Result describes how well a query matched a string.
type Result struct {
	// Tier is the way the query matched.
	Tier Tier
	// Cost ranks results within a tier; lower is better. For a prefix it
	// is the position of the word, counting from zero; for a subsequence,
	// the letters skipped after the nearest word start; for a typo, the edit
	// cost.
	Cost int
}

// Compare returns -1, 0 or +1 as r ranks before, with or after o.
func (r Result) Compare(o Result) int {
	return cmp.Or(cmp.Compare(r.Tier, o.Tier), cmp.Compare(r.Cost, o.Cost))
}

// Match reports whether query matches s, and how well.
func Match(query, s string) (Result, bool) {
	for i, word := range words(s) {
		if strings.HasPrefix(word, query) {
			return Result{Tier: Prefix, Cost: i}, true
		}
	}
	if cost, ok := lowest(query, s, subsequenceCost); ok {
		return Result{Tier: Subsequence, Cost: cost}, true
	}
	if cost, ok := lowest(query, s, typoCost); ok {
		return Result{Tier: Typo, Cost: cost}, true
	}
	return Result{}, false
}

// words yields the position and remainder of s at each word start: s itself,
// then the rest of s after each run of separators.
func words(s string) iter.Seq2[int, string] {
	return func(yield func(int, string) bool) {
		if !yield(0, s) {
			return
		}
		n := 1
		for i := range len(s) - 1 {
			if !isSeparator(s[i]) || isSeparator(s[i+1]) {
				continue
			}
			if !yield(n, s[i+1:]) {
				return
			}
			n++
		}
	}
}

// isSeparator reports whether b ends a word. Separators are ASCII, so a byte
// that is one always stands for itself.
func isSeparator(b byte) bool {
	return strings.IndexByte("/-._ ", b) >= 0
}

// lowest returns the lowest cost that cost reports for query against s from
// any of its word starts, and false if it matches from none.
func lowest(query, s string, cost func(query, s string) (int, bool)) (int, bool) {
	best, found := 0, false
	for _, word := range words(s) {
		if c, ok := cost(query, word); ok && (!found || c < best) {
			best, found = c, true
		}
	}
	return best, found
}

// Rank returns the items whose key matches query, best first. Items that rank
// equally keep their order in items.
func Rank[T any](query string, items []T, key func(T) string) iter.Seq[T] {
	return func(yield func(T) bool) {
		type ranked struct {
			item   T
			result Result
		}
		var matches []ranked
		for _, item := range items {
			if r, ok := Match(query, key(item)); ok {
				matches = append(matches, ranked{item, r})
			}
		}
		slices.SortStableFunc(matches, func(a, b ranked) int { return a.result.Compare(b.result) })
		for _, m := range matches {
			if !yield(m.item) {
				return
			}
		}
	}
}

// subsequenceCost reports whether the letters of query appear in s in order,
// and how many letters of s are skipped before the last one matches. Matching
// each letter at its earliest position gives the fewest skips.
func subsequenceCost(query, s string) (int, bool) {
	q := []rune(query)
	if len(q) == 0 {
		return 0, true
	}
	matched := 0
	for i, r := range []rune(s) {
		if r != q[matched] {
			continue
		}
		matched++
		if matched == len(q) {
			return i + 1 - len(q), true
		}
	}
	return 0, false
}

// Edit costs for typo matching. Hitting a neighbouring key costs less than
// any other edit.
const (
	editCost     = 2
	neighborCost = 1
)

// typoCost returns the lowest cost of editing query into a prefix of s, and
// whether that cost is within the typos allowed for a query of its length.
// Insertions, deletions, substitutions and swaps of adjacent letters cost
// editCost each, and a substitution with a neighbouring key costs
// neighborCost.
func typoCost(query, s string) (int, bool) {
	q, t := []rune(query), []rune(s)
	limit := editCost * allowedTypos(len(q))
	if limit == 0 {
		return 0, false
	}

	// d[i][j] is the cost of editing q[:i] into t[:j].
	d := make([][]int, len(q)+1)
	for i := range d {
		d[i] = make([]int, len(t)+1)
		d[i][0] = i * editCost
	}
	for j := range d[0] {
		d[0][j] = j * editCost
	}
	for i := range q {
		for j := range t {
			c := min(
				d[i][j+1]+editCost,
				d[i+1][j]+editCost,
				d[i][j]+substitutionCost(q[i], t[j]),
			)
			if i > 0 && j > 0 && q[i] == t[j-1] && q[i-1] == t[j] {
				c = min(c, d[i-1][j-1]+editCost)
			}
			d[i+1][j+1] = c
		}
	}

	cost := slices.Min(d[len(q)])
	return cost, cost <= limit
}

// allowedTypos returns the number of typos tolerated in a query of n
// letters. Short queries tolerate none, since a typo in one of two letters
// would match almost everything.
func allowedTypos(n int) int {
	switch {
	case n < 3:
		return 0
	case n <= 4:
		return 1
	default:
		return 2
	}
}

// substitutionCost returns the cost of typing got where want was meant.
func substitutionCost(got, want rune) int {
	switch {
	case got == want:
		return 0
	case Neighbors(got, want):
		return neighborCost
	default:
		return editCost
	}
}

// qwertyRows are the letter rows of a QWERTY keyboard, each with its offset
// from the left edge of the top row in quarter keys.
var qwertyRows = [...]struct {
	keys   string
	offset int
}{
	{"qwertyuiop", 0},
	{"asdfghjkl", 1},
	{"zxcvbnm", 3},
}

// keyPosition returns the row of letter r on a QWERTY keyboard and its
// horizontal position in quarter keys, or false if r is not a letter key.
func keyPosition(r rune) (row, x int, ok bool) {
	for row, line := range qwertyRows {
		if i := strings.IndexRune(line.keys, r); i >= 0 {
			return row, line.offset + 4*i, true
		}
	}
	return 0, 0, false
}

// Neighbors reports whether a and b are different, touching letter keys on a
// QWERTY keyboard: beside each other in a row, or overlapping in the row
// above or below.
func Neighbors(a, b rune) bool {
	rowA, xA, okA := keyPosition(a)
	rowB, xB, okB := keyPosition(b)
	if !okA || !okB {
		return false
	}
	dx := max(xA-xB, xB-xA)
	switch max(rowA-rowB, rowB-rowA) {
	case 0:
		return dx == 4
	case 1:
		return dx <= 3
	}
	return false
}
