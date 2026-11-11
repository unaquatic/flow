package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/unaquatic/fluid/style"
)

// TestStatusLinesDimTags checks that the session info's Account and Model
// rows show the bracketed IDs of a provider, lab or model the catalog does
// not list in the labels' dim color, and the rest of the name in the text's.
func TestStatusLinesDimTags(t *testing.T) {
	dim := DefaultTheme().TextMuted
	lines := StatusLines(StatusInfo{Account: "Unknown [provider-z]", Model: "Unknown [lab-z] Unknown [model-z] Medium"}, nil)
	for _, tt := range []struct {
		row  int
		want []string
	}{
		{1, []string{"Unknown ", "[provider-z]"}},
		{2, []string{"Unknown ", "[lab-z]", " Unknown ", "[model-z]", " Medium"}},
	} {
		chunks := lines[tt.row].Chunks[1:]
		var got []string
		for _, c := range chunks {
			got = append(got, c.Text)
			if tagged := strings.HasPrefix(c.Text, "["); tagged != (c.Fg == dim) {
				t.Errorf("row %d chunk %q in %v, want the bracketed IDs alone dim", tt.row, c.Text, c.Fg)
			}
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("row %d chunks = %q, want %q", tt.row, got, tt.want)
		}
	}
}

// TestStatusLinesContext checks that the session info's Context row reads
// the latest request's tokens, the window and the share of it they fill, or
// the tokens alone when the window is not known.
func TestStatusLinesContext(t *testing.T) {
	for _, tt := range []struct {
		usage SessionUsage
		want  string
	}{
		{SessionUsage{Context: 20_000, Window: 400_000}, "Context   20K / 400K 5%"},
		{SessionUsage{Window: 400_000}, "Context   0K / 400K 0%"},
		{SessionUsage{Context: 18_000}, "Context   18K"},
	} {
		lines := StatusLines(StatusInfo{Usage: tt.usage}, nil)
		i := slices.IndexFunc(lines, func(l style.StyledText) bool { return strings.HasPrefix(l.String(), "Context") })
		if i < 0 || lines[i].String() != tt.want {
			t.Errorf("usage %+v: context row = %q, want %q", tt.usage, rowText(lines, i), tt.want)
		}
	}
}

// TestStatusNoModel checks that a terminal started with no model holds none,
// not "/", and that /status reads its account and model as StatusNone,
// while a model that names no provider reads as none for the account alone,
// and a provider, lab or model the catalog does not list reads as unknown.
func TestStatusNoModel(t *testing.T) {
	for _, tt := range []struct {
		start, model   string
		account, value string
	}{
		{start: "", model: "", account: "Account   None", value: "Model     None"},
		{start: "model-x", model: "/model-x", account: "Account   None", value: "Model     Unknown [model-x]"},
		// A provider and lab the catalog does not list read as unknown.
		{start: "provider-z/lab-z/model-z", model: "provider-z/lab-z/model-z",
			account: "Account   Unknown [provider-z]", value: "Model     Unknown [lab-z] Unknown [model-z]"},
	} {
		_, a := newTestService(t)
		state := &UIState{Model: tt.start}
		s := NewService(t.Context(), a, state)
		if state.Model != tt.model || state.ModelLabel != tt.model {
			t.Errorf("start %q: model, label = %q, %q, want %q", tt.start, state.Model, state.ModelLabel, tt.model)
		}
		s.InitSession(t.Context(), "")
		events := s.ShowInfo()
		if len(events) != 1 {
			t.Fatalf("start %q: ShowInfo returned %d events, want the notice alone", tt.start, len(events))
		}
		notice, _ := toastOf(events[0])
		row := func(text string) int {
			return slices.IndexFunc(notice.Lines, func(l style.StyledText) bool { return l.PlainText() == text })
		}
		if p, m := row(tt.account), row(tt.value); p < 0 || m != p+1 {
			t.Errorf("start %q: /status lines = %q, want %q and then %q", tt.start, plainLines(notice.Lines), tt.account, tt.value)
		}
	}
}

// plainLines returns the plain text of each of lines.
func plainLines(lines []style.StyledText) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.PlainText()
	}
	return out
}

// rowText returns the text of lines[i], or "" when i is out of range.
func rowText(lines []style.StyledText, i int) string {
	if i < 0 || i >= len(lines) {
		return ""
	}
	return lines[i].String()
}
