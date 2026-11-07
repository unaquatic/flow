package ui

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/unaquatic/fluid/style"
)

// TestShowStatusOmitsLogo checks that /status shows the session info alone,
// while a new session opens under the standard product logo.
func TestShowStatusOmitsLogo(t *testing.T) {
	s, _ := newTestService(t)
	s.InitSession(t.Context(), "")

	events := s.ShowStatus()
	if len(events) != 1 {
		t.Fatalf("ShowStatus returned %d events, want the notice alone", len(events))
	}
	if notice, ok := toastOf(events[0]); !ok || notice.Surface != NoticeSystem {
		t.Errorf("event is %+v, want the session info notice", events[0])
	}

	events = s.ShowInfo()
	if len(events) != 2 {
		t.Fatalf("ShowInfo returned %d events, want the logo and the notice", len(events))
	}
	logo, ok := canvasOf(events[0])
	if !ok {
		t.Fatalf("first event is %+v, want the logo on the canvas", events[0])
	}
	if want := slices.Collect(LogoLines()); !reflect.DeepEqual(logo.Lines, want) {
		t.Errorf("logo = %+v, want standard logo %+v", logo.Lines, want)
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

// rowText returns the text of lines[i], or "" when i is out of range.
func rowText(lines []style.StyledText, i int) string {
	if i < 0 || i >= len(lines) {
		return ""
	}
	return lines[i].String()
}
