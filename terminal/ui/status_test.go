package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/unaquatic/fluid/style"
)

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
// not "/", and that /status reads its provider and model as StatusNone,
// while a model that names no provider reads as none for the provider alone.
func TestStatusNoModel(t *testing.T) {
	for _, tt := range []struct {
		start, model    string
		provider, value string
	}{
		{start: "", model: "", provider: "Provider  none", value: "Model     none"},
		{start: "model-x", model: "/model-x", provider: "Provider  none", value: "Model     model-x"},
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
		if p, m := row(tt.provider), row(tt.value); p < 0 || m != p+1 {
			t.Errorf("start %q: /status lines = %q, want %q and then %q", tt.start, plainLines(notice.Lines), tt.provider, tt.value)
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
