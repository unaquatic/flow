package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lines returns the lines of the named file, or none when it does not exist.
func lines(t *testing.T, name string) []string {
	t.Helper()
	data, err := os.ReadFile(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// TestInit_CopiesSessionRecords checks that every record reaches the log,
// and that a record with a session attribute, of its own or of its logger,
// reaches that session's file too, unless the attribute is in a group or the
// session is unsafe as a file name.
func TestInit_CopiesSessionRecords(t *testing.T) {
	prev := slog.Default()
	dir := filepath.Join(t.TempDir(), "logs")
	f, err := Init(filepath.Join(dir, "app.log"), slog.LevelInfo)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		slog.SetDefault(prev)
		f.Close()
	})

	slog.Info("plain")
	slog.Info("own", "session", "ses_a")
	slog.Default().With("session", "ses_a").Info("logger")
	slog.Default().WithGroup("g").Info("grouped", "session", "ses_g")
	slog.Info("unsafe", "session", "../escape")

	if got := lines(t, filepath.Join(dir, "app.log")); len(got) != 5 {
		t.Errorf("app.log has %d lines, want all 5:\n%s", len(got), strings.Join(got, "\n"))
	}
	got := lines(t, filepath.Join(dir, "ses_a.log"))
	if len(got) != 2 || !strings.Contains(got[0], "msg=own") || !strings.Contains(got[1], "msg=logger") {
		t.Errorf("ses_a.log = %q, want the own and logger records", got)
	}
	for _, line := range got {
		if !strings.Contains(line, "pid=") || !strings.Contains(line, "component=logging") {
			t.Errorf("session line %q lacks the pid or component of the log's lines", line)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("log dir holds %d files, want app.log and ses_a.log", len(entries))
	}
}
