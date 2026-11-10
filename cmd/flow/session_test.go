package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/terminal/ui"
)

// TestSessionList checks what flow session list writes: that there are no
// sessions, an empty JSON list, and then a row for each session, newest
// first, with its folder only with --all, and the sessions as JSON.
func TestSessionList(t *testing.T) {
	a, _ := newRunTestApp(t, "All green.")
	var out strings.Builder
	if err := runSessionList(t.Context(), &out, a, SessionListOptions{Format: OutputText}, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "No sessions found\n"; got != want {
		t.Errorf("no sessions = %q, want %q", got, want)
	}
	out.Reset()
	if err := runSessionList(t.Context(), &out, a, SessionListOptions{Format: OutputJSON}, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "[]\n"; got != want {
		t.Errorf("no sessions as JSON = %q, want %q", got, want)
	}

	for _, prompt := range []string{"fix the tests", "and the lint"} {
		if err := runPrompt(t.Context(), new(strings.Builder), a, RunOptions{Format: OutputText}, prompt); err != nil {
			t.Fatal(err)
		}
	}
	sessions, err := a.Sessions().ListSessions(t.Context())
	if err != nil || len(sessions) != 2 {
		t.Fatalf("sessions = %d, %v; want 2", len(sessions), err)
	}

	for _, all := range []bool{false, true} {
		out.Reset()
		if err := runSessionList(t.Context(), &out, a, SessionListOptions{All: all, Format: OutputText}, nil); err != nil {
			t.Fatal(err)
		}
		rows := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
		if len(rows) != 2 {
			t.Fatalf("all %v: rows =\n%s\nwant 2", all, out.String())
		}
		// The column cuts a long folder from the left, keeping its name.
		dir := filepath.Base(sessions[0].Worktree)
		for i, row := range rows {
			if !strings.HasPrefix(row, ui.UntitledName) || !strings.HasSuffix(row, string(sessions[i].ID)) {
				t.Errorf("all %v: row %d = %q, want %s first and %s last", all, i, row, ui.UntitledName, sessions[i].ID)
			}
			if strings.Contains(row, dir) != all {
				t.Errorf("all %v: row %d = %q, want the folder %s shown only with --all", all, i, row, dir)
			}
		}
	}

	out.Reset()
	if err := runSessionList(t.Context(), &out, a, SessionListOptions{Format: OutputJSON}, nil); err != nil {
		t.Fatal(err)
	}
	var got []session.Session
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("JSON = %s: %v", out.String(), err)
	}
	if len(got) != 2 || got[0].ID != sessions[0].ID || got[1].ID != sessions[1].ID || got[0].Worktree == "" {
		t.Errorf("JSON = %+v, want %+v with their folders", got, sessions)
	}
}
