package ui

import (
	"strings"
	"testing"

	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/fluid/terminal"
	"github.com/unaquatic/fluid/textbuf"
)

// agreeingProbe returns measurements that match fluid widths.
func agreeingProbe() []int {
	probed := make([]int, len(GraphemeSamples))
	for i, s := range GraphemeSamples {
		probed[i] = textbuf.StringWidth(s.Text)
	}
	return probed
}

// sampleIndex returns the index of the nonempty sample with label.
func sampleIndex(t *testing.T, label string) int {
	t.Helper()
	for i, s := range GraphemeSamples {
		if s.Label == label && s.Text != "" {
			return i
		}
	}
	t.Fatalf("no sample labelled %q", label)
	return -1
}

func TestGraphemeReport_Agreement(t *testing.T) {
	got := GraphemeReport(agreeingProbe(), nil)
	if !strings.Contains(got, "agrees with fluid") {
		t.Errorf("report does not report agreement:\n%s", got)
	}
	if strings.Contains(got, "MISMATCH") {
		t.Errorf("report flags a mismatch where there is none:\n%s", got)
	}
}

func TestGraphemeReport_Disagreement(t *testing.T) {
	probed := agreeingProbe()
	probed[sampleIndex(t, "hangul jamo")] = 4

	got := GraphemeReport(probed, nil)
	if !strings.Contains(got, "1 of") {
		t.Errorf("report does not count the disagreement:\n%s", got)
	}
	if strings.Count(got, "MISMATCH") != 1 {
		t.Errorf("want exactly one MISMATCH flag:\n%s", got)
	}
	// A mismatch clips a glyph but does not shift the row.
	if !strings.Contains(got, "clip") {
		t.Errorf("report does not explain the consequence:\n%s", got)
	}
}

func TestGraphemeReport_NotMeasured(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		want     string
		wantHint bool
	}{
		// A missing result without an error means the probe was not requested.
		{name: "probe not requested", want: "fluid's widths only", wantHint: true},
		{name: "timeout", err: terminal.ErrProbeTimeout, want: "timed out"},
		{name: "unsupported", err: terminal.ErrProbeUnsupported, want: "unsupported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GraphemeReport(nil, tt.err)
			if !strings.Contains(got, tt.want) {
				t.Errorf("report missing %q:\n%s", tt.want, got)
			}
			if hasHint := strings.Contains(got, config.EnvWidthProbe); hasHint != tt.wantHint {
				t.Errorf("hint present = %v, want %v:\n%s", hasHint, tt.wantHint, got)
			}
			if strings.Contains(got, "MISMATCH") {
				t.Error("unmeasured report must not claim a mismatch")
			}
		})
	}
}

// TestGraphemeReport_ColumnsAlign checks that sample rows align numeric columns.
func TestGraphemeReport_ColumnsAlign(t *testing.T) {
	probed := agreeingProbe()
	want := -1
	for i, s := range GraphemeSamples {
		if s.Text == "" {
			continue
		}
		row := strings.TrimRight(GraphemeReportRow(i, s, probed), "\n")
		got := fluidFigureColumn(t, row)
		if want < 0 {
			want = got
			continue
		}
		if got != want {
			t.Errorf("%s: fluid figure ends at column %d, want %d\n  row: %q",
				s.Label, got, want, row)
		}
	}
}

// fluidFigureColumn returns the display column containing the fluid width.
// It skips the sample field because a sample can begin with a digit.
func fluidFigureColumn(t *testing.T, row string) int {
	t.Helper()
	const fieldsEnd = 2 + graphemeLabelWidth + graphemeSampleWidth

	// Count grapheme widths rather than code points.
	col, rest := 0, row
	for rest != "" {
		cluster, next, w := textbuf.NextGraphemeCluster(rest)
		rest = next
		col += w
		if col > fieldsEnd && len(cluster) == 1 && cluster[0] >= '0' && cluster[0] <= '9' {
			return col
		}
	}
	t.Fatalf("no fluid figure in row: %q", row)
	return -1
}

// TestRenderedText_MatchesWhatTheFlushEmits checks the text produced from
// rendered cells, including omitted zero-width code points.
func TestRenderedText_MatchesWhatTheFlushEmits(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"plain ascii", "A", "A"},
		{"wide", "\u4e16", "\u4e16"},
		{"zero width space dropped", "A\u200bA", "AA"},
		{"bom dropped", "A\ufeffA", "AA"},
		{"combining mark kept", "e\u0301", "e\u0301"},
		{"spacing mark kept", "\u0E01\u0E33", "\u0E01\u0E33"},
		{"jamo kept", "\u1100\u1161\u11a8", "\u1100\u1161\u11a8"},
		{"zwj sequence kept", "\U0001F468\u200D\U0001F469", "\U0001F468\u200D\U0001F469"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RenderedText(tt.text); got != tt.want {
				t.Errorf("RenderedText(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

// TestGraphemeProbeSamples_WidthMatchesFluid checks that probe strings retain
// the widths assigned during rendering.
func TestGraphemeProbeSamples_WidthMatchesFluid(t *testing.T) {
	for i, s := range GraphemeProbeSamples() {
		want := textbuf.StringWidth(GraphemeSamples[i].Text)
		if got := textbuf.StringWidth(s); got != want {
			t.Errorf("%s: probe sample %q measures %d, but fluid draws %d columns",
				GraphemeSamples[i].Label, s, got, want)
		}
	}
}

func TestGraphemeProbeSamples_IndexAligned(t *testing.T) {
	samples := GraphemeProbeSamples()
	if len(samples) != len(GraphemeSamples) {
		t.Fatalf("got %d samples, want %d", len(samples), len(GraphemeSamples))
	}
	for i, s := range GraphemeSamples {
		// Emitted text can differ, but empty entries must remain aligned.
		if (samples[i] == "") != (s.Text == "") {
			t.Errorf("sample %d = %q, does not correspond to %q", i, samples[i], s.Text)
		}
	}
}

func TestGraphemeSamplesFitField(t *testing.T) {
	for _, s := range GraphemeSamples {
		if w := textbuf.StringWidth(s.Text); w > graphemeSampleWidth {
			t.Errorf("%s: sample width %d exceeds field width %d", s.Label, w, graphemeSampleWidth)
		}
	}
}
