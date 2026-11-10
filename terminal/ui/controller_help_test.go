package ui

import (
	"strings"
	"testing"
	"time"
)

// TestUsageLines checks the rows /usage shows, and that a model that is not
// set reads as StatusNone.
func TestUsageLines(t *testing.T) {
	info := UsageInfo{
		Model:  "lab-a/model-a:high",
		Usage:  SessionUsage{Input: 120_000, Output: 8_400, CacheRead: 310_000, CacheWrite: 2_000, Cost: 0.42, Context: 20_000, Window: 400_000},
		Worked: 3*time.Minute + 12*time.Second,
	}
	var got []string
	for _, line := range UsageLines(info, nil) {
		got = append(got, line.PlainText())
	}
	want := []string{
		"Model        model-a high",
		"Context      20K / 400K 5%",
		"Input        120K",
		"Output       8.4K",
		"Cache read   310K",
		"Cache write  2.0K",
		"Cost         $0.42",
		"Worked       3m 12s",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("UsageLines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if first := UsageLines(UsageInfo{}, nil)[0].PlainText(); first != "Model        "+StatusNone {
		t.Errorf("first row without a model = %q", first)
	}
}

// TestUsageAndHelpCommands checks that /usage, /context and /help each add
// a system notice under its title.
func TestUsageAndHelpCommands(t *testing.T) {
	for cmd, title := range map[string]string{"/usage": UsageTitle, "/context": UsageTitle, "/help": HelpTitle} {
		s, _ := newTestService(t)
		s.InitSession(t.Context(), "")
		out := s.HandleSubmit(cmd)
		if len(out) == 0 {
			t.Fatalf("%s added nothing", cmd)
		}
		n, ok := toastOf(out[len(out)-1])
		if !ok || n.Title != title || n.Surface != NoticeSystem {
			t.Errorf("%s = %+v, want a system notice titled %s", cmd, out[len(out)-1], title)
		}
	}
}

// TestHelpLines checks that /help lists every command it is given by the
// name the user types, then, under a blank row, every key.
func TestHelpLines(t *testing.T) {
	lines := HelpLines(SlashCommands)
	if len(lines) != len(SlashCommands)+1+len(Keys) {
		t.Fatalf("%d lines, want a row for each command and key and a blank row", len(lines))
	}
	for i, c := range SlashCommands {
		if text := lines[i].PlainText(); !strings.HasPrefix(text, c.Name.String()+" ") || !strings.HasSuffix(text, c.Desc) {
			t.Errorf("row %d = %q, want %s and its description", i, text, c.Name)
		}
	}
	if blank := lines[len(SlashCommands)].PlainText(); blank != "" {
		t.Errorf("row after the commands = %q, want it blank", blank)
	}
	for i, k := range Keys {
		if text := lines[len(SlashCommands)+1+i].PlainText(); !strings.HasPrefix(text, k.Key+" ") {
			t.Errorf("key row %d = %q, want it to start with %s", i, text, k.Key)
		}
	}
}
