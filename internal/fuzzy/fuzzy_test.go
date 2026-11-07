package fuzzy

import (
	"slices"
	"testing"
)

func TestMatch(t *testing.T) {
	tests := []struct {
		name     string
		query, s string
		want     Result
		wantOK   bool
	}{
		{name: "empty", query: "", s: "status", want: Result{Tier: Prefix}, wantOK: true},
		{name: "prefix", query: "st", s: "status", want: Result{Tier: Prefix}, wantOK: true},
		{name: "exact", query: "status", s: "status", want: Result{Tier: Prefix}, wantOK: true},
		{name: "subsequence", query: "ck", s: "checkout", want: Result{Tier: Subsequence, Cost: 3}, wantOK: true},
		{name: "subsequence fewer skips", query: "sts", s: "status", want: Result{Tier: Subsequence, Cost: 3}, wantOK: true},
		{name: "subsequence more skips", query: "sts", s: "settings", want: Result{Tier: Subsequence, Cost: 5}, wantOK: true},
		{name: "missing letter", query: "chekout", s: "checkout", want: Result{Tier: Subsequence, Cost: 1}, wantOK: true},
		{name: "swapped letters", query: "lgoin", s: "login", want: Result{Tier: Typo, Cost: editCost}, wantOK: true},
		{name: "extra letter", query: "quiit", s: "quit", want: Result{Tier: Typo, Cost: editCost}, wantOK: true},
		{name: "neighboring key", query: "stqtus", s: "status", want: Result{Tier: Typo, Cost: neighborCost}, wantOK: true},
		{name: "typo in partial input", query: "lgo", s: "login", want: Result{Tier: Typo, Cost: editCost}, wantOK: true},
		{name: "too short for a typo", query: "gl", s: "login"},
		{name: "too many typos", query: "lxgx", s: "login"},
		{name: "case-sensitive", query: "S", s: "status"},
		{name: "first word prefix", query: "prov", s: "provider-a/model-a-5", want: Result{Tier: Prefix}, wantOK: true},
		{name: "later word prefix", query: "small", s: "provider-b/model-small-4-6", want: Result{Tier: Prefix, Cost: 3}, wantOK: true},
		{name: "run of separators", query: "b", s: "a--b", want: Result{Tier: Prefix, Cost: 1}, wantOK: true},
		{name: "subsequence from word start", query: "a5", s: "provider-a/model-a-5", want: Result{Tier: Subsequence, Cost: 1}, wantOK: true},
		{name: "subsequence across words", query: "a4m", s: "provider-a/model-a4-mini", want: Result{Tier: Subsequence, Cost: 1}, wantOK: true},
		{name: "typo in later word", query: "smlal", s: "provider-b/model-small-4-6", want: Result{Tier: Typo, Cost: editCost}, wantOK: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Match(tt.query, tt.s)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("Match(%q, %q) = %+v, %v; want %+v, %v", tt.query, tt.s, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestRank(t *testing.T) {
	items := []string{"resume", "model", "status", "redo", "settings", "quit"}
	tests := []struct {
		name, query string
		want        []string
	}{
		{name: "empty keeps order", query: "", want: items},
		{name: "prefix before subsequence", query: "st", want: []string{"status", "settings"}},
		{name: "ties keep order", query: "re", want: []string{"resume", "redo"}},
		{name: "typo", query: "stauts", want: []string{"status"}},
		{name: "no match", query: "xyz", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := slices.Collect(Rank(tt.query, items, func(s string) string { return s }))
			if !slices.Equal(got, tt.want) {
				t.Errorf("Rank(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

func TestRankWords(t *testing.T) {
	items := []string{"provider-a/model-a-5", "provider-a/model-a4-mini", "provider-b/model-3-7-small", "provider-b/model-small-4-6"}
	tests := []struct {
		name, query string
		want        []string
	}{
		{name: "provider before its near misses", query: "provider-b", want: []string{"provider-b/model-3-7-small", "provider-b/model-small-4-6", "provider-a/model-a-5", "provider-a/model-a4-mini"}},
		{name: "earlier word first", query: "small", want: []string{"provider-b/model-small-4-6", "provider-b/model-3-7-small"}},
		{name: "subsequence", query: "a5", want: []string{"provider-a/model-a-5"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := slices.Collect(Rank(tt.query, items, func(s string) string { return s }))
			if !slices.Equal(got, tt.want) {
				t.Errorf("Rank(%q) = %q, want %q", tt.query, got, tt.want)
			}
		})
	}
}

func TestRankStopsEarly(t *testing.T) {
	var got []string
	for s := range Rank("", []string{"a", "b", "c"}, func(s string) string { return s }) {
		got = append(got, s)
		break
	}
	if want := []string{"a"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestNeighbors(t *testing.T) {
	tests := []struct {
		a, b rune
		want bool
	}{
		{'q', 'w', true},
		{'q', 'a', true},
		{'w', 'a', true},
		{'a', 'z', true},
		{'s', 'x', true},
		{'s', 'e', true},
		{'m', 'k', true},
		{'q', 's', false},
		{'a', 'x', false},
		{'r', 'u', false},
		{'e', 'n', false},
		{'a', 'a', false},
		{'1', 'q', false},
	}
	for _, tt := range tests {
		if got := Neighbors(tt.a, tt.b); got != tt.want {
			t.Errorf("Neighbors(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
		if got := Neighbors(tt.b, tt.a); got != tt.want {
			t.Errorf("Neighbors(%q, %q) = %v, want %v", tt.b, tt.a, got, tt.want)
		}
	}
}
