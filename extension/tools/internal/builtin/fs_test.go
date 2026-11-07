package builtin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unaquatic/flow/agent"
)

// run runs tool with args, and returns its text and error.
func run(t *testing.T, tool agent.Tool, args map[string]any) (string, error) {
	t.Helper()
	out, err := tool.Execute(t.Context(), args)
	return out.Text, err
}

// writeFile writes content to path, with mode, or fails the test.
func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

// readFile returns the contents of path, or fails the test.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestRead checks that read returns a file whole, from a path relative to
// the working directory too; a window of its lines from offset, at most
// limit of them, with a notice of the lines it shows; and fails for an
// offset past the end, a directory, a missing file, and a missing path.
func TestRead(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeFile(t, "calc.go", "one\ntwo\nthree\nfour\nfive\n", 0o644)
	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{name: "whole", args: map[string]any{"path": "calc.go"}, want: "one\ntwo\nthree\nfour\nfive"},
		{name: "absolute path", args: map[string]any{"path": filepath.Join(dir, "calc.go")}, want: "one\ntwo\nthree\nfour\nfive"},
		{name: "window", args: map[string]any{"path": "calc.go", "offset": float64(2), "limit": float64(2)},
			want: "two\nthree\n\n[showing lines 2-3 of 5]"},
		{name: "from an offset to the end", args: map[string]any{"path": "calc.go", "offset": float64(4)}, want: "four\nfive"},
	}
	for _, tt := range tests {
		if got, err := run(t, Read(), tt.args); err != nil || got != tt.want {
			t.Errorf("%s: read = %q, %v; want %q", tt.name, got, err, tt.want)
		}
	}

	if err := os.Mkdir("pkg", 0o755); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string]map[string]any{
		"offset past the end": {"path": "calc.go", "offset": float64(9)},
		"directory":           {"path": "pkg"},
		"missing file":        {"path": "nope.go"},
		"missing path":        {},
	} {
		if got, err := run(t, Read(), args); err == nil {
			t.Errorf("%s: read = %q, want an error", name, got)
		}
	}
}

// TestWrite checks that write creates a file and the directories above it,
// replaces a file that exists, keeping its mode, and fails for a directory
// and for missing arguments.
func TestWrite(t *testing.T) {
	t.Chdir(t.TempDir())
	got, err := run(t, Write(), map[string]any{"path": "pkg/calc/calc.go", "content": "package calc\n"})
	if err != nil || got != "wrote 13 bytes" {
		t.Fatalf("write = %q, %v; want wrote 13 bytes", got, err)
	}
	if body := readFile(t, "pkg/calc/calc.go"); body != "package calc\n" {
		t.Errorf("file = %q, want what was written", body)
	}

	writeFile(t, "run.sh", "echo old\n", 0o755)
	if _, err := run(t, Write(), map[string]any{"path": "run.sh", "content": "echo new\n"}); err != nil {
		t.Fatal(err)
	}
	if body := readFile(t, "run.sh"); body != "echo new\n" {
		t.Errorf("replaced file = %q, want the new content", body)
	}
	if st, err := os.Stat("run.sh"); err != nil || st.Mode().Perm() != 0o755 {
		t.Errorf("replaced file's mode = %v, %v; want 0755 kept", st.Mode().Perm(), err)
	}

	for name, args := range map[string]map[string]any{
		"directory":       {"path": "pkg", "content": "x"},
		"missing content": {"path": "a.go"},
		"missing path":    {"content": "x"},
	} {
		if got, err := run(t, Write(), args); err == nil {
			t.Errorf("%s: write = %q, want an error", name, got)
		}
	}
}

// TestEdit checks that edit replaces the one occurrence of old_string,
// keeping the file's mode, and leaves the file as it was when old_string is
// missing, found more than once, empty, or new_string equals it, and for a
// directory or a missing file.
func TestEdit(t *testing.T) {
	t.Chdir(t.TempDir())
	const body = "func Add(a, b int) int { return a + b }\nfunc Sub(a, b int) int { return a - b }\n"
	writeFile(t, "calc.go", body, 0o600)

	got, err := run(t, Edit(), map[string]any{"path": "calc.go", "old_string": "a + b", "new_string": "b + a"})
	if err != nil || got != "edited" {
		t.Fatalf("edit = %q, %v; want edited", got, err)
	}
	want := strings.Replace(body, "a + b", "b + a", 1)
	if got := readFile(t, "calc.go"); got != want {
		t.Errorf("file = %q, want %q", got, want)
	}
	if st, err := os.Stat("calc.go"); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("edited file's mode = %v, %v; want 0600 kept", st.Mode().Perm(), err)
	}

	if err := os.Mkdir("pkg", 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name string
		args map[string]any
		want string
	}{
		{name: "missing", args: map[string]any{"path": "calc.go", "old_string": "a * b", "new_string": "x"}, want: "not found"},
		{name: "found twice", args: map[string]any{"path": "calc.go", "old_string": "(a, b int)", "new_string": "(x, y int)"}, want: "found 2 times"},
		{name: "empty", args: map[string]any{"path": "calc.go", "old_string": "", "new_string": "x"}, want: "empty"},
		{name: "unchanged", args: map[string]any{"path": "calc.go", "old_string": "Sub", "new_string": "Sub"}, want: "equals"},
		{name: "directory", args: map[string]any{"path": "pkg", "old_string": "a", "new_string": "b"}, want: "is a directory"},
		{name: "missing file", args: map[string]any{"path": "nope.go", "old_string": "a", "new_string": "b"}, want: "no such file"},
	} {
		got, err := run(t, Edit(), tt.args)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s: edit = %q, %v; want an error saying %q", tt.name, got, err, tt.want)
		}
	}
	if got := readFile(t, "calc.go"); got != want {
		t.Errorf("file after failed edits = %q, want it unchanged: %q", got, want)
	}
}
