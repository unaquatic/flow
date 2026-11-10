package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/terminal/ui"
)

// TestExport checks that flow export writes the latest session of the
// project as Markdown, a session it names as JSON, the session and then each
// message on a line of its own as JSON Lines, and a transcript to the file
// it names, saying where.
func TestExport(t *testing.T) {
	a, _ := newRunTestApp(t, "All green.")
	if err := runExport(t.Context(), new(strings.Builder), a, ExportOptions{Format: ExportMarkdown}, nil); !errors.Is(err, errNoSessions) {
		t.Errorf("export with no sessions: err = %v, want %v", err, errNoSessions)
	}
	if err := runPrompt(t.Context(), new(strings.Builder), a, RunOptions{Format: OutputText}, "fix the tests"); err != nil {
		t.Fatal(err)
	}
	sessions, err := a.Sessions().ListSessions(t.Context())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %d, %v; want 1", len(sessions), err)
	}
	id := sessions[0].ID

	var out strings.Builder
	if err := runExport(t.Context(), &out, a, ExportOptions{Format: ExportMarkdown}, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "# Untitled\n\n## User\n\nfix the tests\n\n## Agent\n\nAll green.\n"; got != want {
		t.Errorf("Markdown = %q, want %q", got, want)
	}

	out.Reset()
	if err := runExport(t.Context(), &out, a, ExportOptions{Format: ExportJSON}, []string{string(id)}); err != nil {
		t.Fatal(err)
	}
	var got struct {
		Session struct {
			ID message.SessionID `json:"id"`
		} `json:"session"`
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("JSON = %s: %v", out.String(), err)
	}
	if got.Session.ID != id || len(got.Messages) != 2 || got.Messages[0].Role != "user" || got.Messages[1].Role != "agent" {
		t.Errorf("JSON = %+v, want session %s with the prompt and the answer", got, id)
	}

	out.Reset()
	if err := runExport(t.Context(), &out, a, ExportOptions{Format: ExportJSONL}, nil); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("JSON Lines =\n%s\nwant the session and its two messages", out.String())
	}
	for i, want := range []struct{ typ, role string }{{"session", ""}, {"message", "user"}, {"message", "agent"}} {
		var line struct {
			Type string            `json:"type"`
			ID   message.SessionID `json:"id"`
			Role string            `json:"role"`
		}
		if err := json.Unmarshal([]byte(lines[i]), &line); err != nil {
			t.Fatalf("line %d = %s: %v", i, lines[i], err)
		}
		if !strings.HasPrefix(lines[i], `{"type":"`+want.typ+`",`) || line.Role != want.role || (i == 0 && line.ID != id) {
			t.Errorf("line %d = %s, want type %s first, role %q", i, lines[i], want.typ, want.role)
		}
	}

	out.Reset()
	file := filepath.Join(t.TempDir(), "session.md")
	if err := runExport(t.Context(), &out, a, ExportOptions{Format: ExportMarkdown, Output: file}, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "Exported the session to:\n  "+file+"\n"; got != want {
		t.Errorf("output with a file = %q, want %q", got, want)
	}
	if data, err := os.ReadFile(file); err != nil || !strings.HasPrefix(string(data), "# Untitled\n") {
		t.Errorf("file = %q, %v; want the Markdown", data, err)
	}

	err = runExport(t.Context(), new(strings.Builder), a, ExportOptions{Format: ExportMarkdown}, []string{"ses_missing"})
	if err == nil || err.Error() != `no stored session matches "ses_missing"` {
		t.Errorf("export of a missing session: err = %v", err)
	}
}

// TestExportFormat checks the names --format takes, in any case, and that
// it refuses others.
func TestExportFormat(t *testing.T) {
	for in, want := range map[string]ExportFormat{"markdown": ExportMarkdown, "JSON": ExportJSON, "jsonl": ExportJSONL} {
		var f ExportFormat
		if err := f.UnmarshalText([]byte(in)); err != nil || f != want {
			t.Errorf("format %q = %q, %v; want %q", in, f, err, want)
		}
	}
	var f ExportFormat
	if err := f.UnmarshalText([]byte("text")); err == nil {
		t.Errorf("format text = %q, want an error", f)
	}
}

// TestExportMatchesTheTerminal checks that flow export writes a session in
// Markdown as the terminal's /export saves it, tool calls named alike, byte
// for byte.
func TestExportMatchesTheTerminal(t *testing.T) {
	reply := func() (agent.StreamResponse, error) { return nil, errors.ErrUnsupported }
	err := withRunDemoApp(t.Context(), reply, func(a *app.App) error {
		if err := storeDemoSessions(t.Context(), a); err != nil {
			return err
		}
		var cli strings.Builder
		if err := runExport(t.Context(), &cli, a, ExportOptions{Format: ExportMarkdown}, []string{string(demoSession)}); err != nil {
			return err
		}

		svc := ui.NewService(t.Context(), a, &ui.UIState{})
		svc.Extensions = views(t.Context(), a)
		svc.InitSession(t.Context(), demoSession)
		svc.HandleExport()
		saved, err := os.ReadFile(filepath.Join(a.WorkingDir(), ui.ExportName(demoSession)))
		if err != nil {
			return err
		}
		if cli.String() != string(saved) {
			t.Errorf("flow export =\n%s\n/export saved\n%s", cli.String(), saved)
		}
		if !strings.Contains(cli.String(), "\n### Read\n") {
			t.Errorf("flow export =\n%s\nwant the read call under Read", cli.String())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestTypedLine checks that a line of JSON Lines is the object with its type
// first, an empty object too, and that a value that is no object fails.
func TestTypedLine(t *testing.T) {
	for _, tt := range []struct {
		v    any
		want string
	}{
		{struct {
			ID string `json:"id"`
		}{"x"}, `{"type":"t","id":"x"}` + "\n"},
		{struct{}{}, `{"type":"t"}` + "\n"},
	} {
		if got, err := typedLine("t", tt.v); err != nil || string(got) != tt.want {
			t.Errorf("typedLine(%+v) = %q, %v; want %q", tt.v, got, err, tt.want)
		}
	}
	if got, err := typedLine("t", []int{1}); err == nil {
		t.Errorf("typedLine of a list = %q, want an error", got)
	}
}
