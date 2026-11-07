package diff

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStdlibCases runs the standard library's internal/diff test cases. Each
// testdata file is a txtar archive holding the old text, the new text, and
// the expected diff in the standard library's output format.
func TestStdlibCases(t *testing.T) {
	files, err := filepath.Glob("testdata/*.txt")
	if err != nil || len(files) == 0 {
		t.Fatalf("no testdata: %v", err)
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			a := parseTxtar(string(data))
			if len(a) != 3 || a[2].name != "diff" {
				t.Fatalf("want three files, third named \"diff\"")
			}
			hunks := Diff(clean(a[0].data), clean(a[1].data))
			got := stdlibFormat(a[0].name, a[1].name, hunks)
			if want := clean(a[2].data); !bytes.Equal(got, want) {
				t.Fatalf("have:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestHunkString(t *testing.T) {
	tests := []struct {
		name     string
		old, new string
		want     string
	}{
		{"one line changed", "a\n", "b\n", "@@ -1 +1 @@\n-a\n+b\n"},
		{"file added", "", "a\nb\n", "@@ -0,0 +1,2 @@\n+a\n+b\n"},
		{"file deleted", "a\n", "", "@@ -1 +0,0 @@\n-a\n"},
		{"line inserted", "a\nb\n", "a\nx\nb\n", "@@ -1,2 +1,3 @@\n a\n+x\n b\n"},
		{"line deleted", "a\nb\nc\nd\ne\nf\ng\nh\n", "a\nb\nc\ne\nf\ng\nh\n", "@@ -1,7 +1,6 @@\n a\n b\n c\n-d\n e\n f\n g\n"},
		{
			"newline added at end",
			"a", "a\n",
			"@@ -1 +1 @@\n-a\n\\ No newline at end of file\n+a\n",
		},
		{
			"far apart changes split",
			"1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n", "x\n2\n3\n4\n5\n6\n7\n8\n9\ny\n",
			"@@ -1,4 +1,4 @@\n-1\n+x\n 2\n 3\n 4\n@@ -7,4 +7,4 @@\n 7\n 8\n 9\n-10\n+y\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			for _, h := range Diff([]byte(tt.old), []byte(tt.new)) {
				b.WriteString(h.String())
			}
			if got := b.String(); got != tt.want {
				t.Errorf("diff:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

func TestStat(t *testing.T) {
	tests := []struct {
		name              string
		old, new          string
		inserted, deleted int
	}{
		{"identical", "a\n", "a\n", 0, 0},
		{"added file", "", "a\nb\n", 2, 0},
		{"deleted file", "a\nb\nc\n", "", 0, 3},
		{"replaced and appended", "a\nb\nc\n", "a\nB\nc\nd\n", 2, 1},
		{"newline added at end", "a", "a\n", 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ins, del := Stat(Diff([]byte(tt.old), []byte(tt.new)))
			if ins != tt.inserted || del != tt.deleted {
				t.Errorf("Stat = +%d -%d; want +%d -%d", ins, del, tt.inserted, tt.deleted)
			}
		})
	}
}

// FuzzDiff checks that applying the hunks to old yields new.
func FuzzDiff(f *testing.F) {
	f.Add("a\nb\nc\n", "a\nc\nd\n")
	f.Add("", "x")
	f.Add("x", "")
	f.Add("1\n2\n3\n4\n5\n6\n7\n8\n9\n", "1\n2\nX\n4\n5\n6\n7\n8\nY\n")
	f.Add("}\n}\n\n}\n", "}\n\n}\n}\n}")
	f.Fuzz(func(t *testing.T, old, new string) {
		hunks := Diff([]byte(old), []byte(new))
		got, err := apply(old, hunks)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if got != new {
			t.Fatalf("apply(%q) = %q; want %q", old, got, new)
		}
	})
}

// apply patches old with hunks, checking that context and deleted lines
// agree with old.
func apply(old string, hunks []Hunk) (string, error) {
	src := lines([]byte(old))
	var out strings.Builder
	pos := 0
	for _, h := range hunks {
		start := h.OldStart
		if h.OldLines > 0 {
			start--
		}
		if start < pos || start > len(src) {
			return "", fmt.Errorf("hunk %+v out of order", h)
		}
		out.WriteString(strings.Join(src[pos:start], ""))
		pos = start
		for _, line := range h.Lines {
			// The line as lines splits the text, marker included.
			text := line.Text + "\n"
			if line.NoEOL {
				text = line.Text + noEOL
			}
			if line.Op != Insert {
				if pos >= len(src) || src[pos] != text {
					return "", fmt.Errorf("line %d: have %q, hunk says %q", pos+1, src[pos:], text)
				}
				pos++
			}
			if line.Op != Delete {
				out.WriteString(text)
			}
		}
	}
	out.WriteString(strings.Join(src[pos:], ""))
	return strings.TrimSuffix(out.String(), noEOL), nil
}

// stdlibFormat renders hunks as the standard library's internal/diff does,
// so that its test cases apply unchanged.
func stdlibFormat(oldName, newName string, hunks []Hunk) []byte {
	if hunks == nil {
		return nil
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "diff %s %s\n--- %s\n+++ %s\n", oldName, newName, oldName, newName)
	for _, h := range hunks {
		fmt.Fprintf(&b, "@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
		for _, line := range h.Lines {
			b.WriteString(line.String())
		}
	}
	return b.Bytes()
}

// clean decodes the testdata conventions: "$" marks a line end and a final
// "^D" marks a text without a trailing newline.
func clean(text string) []byte {
	text = strings.ReplaceAll(text, "$\n", "\n")
	text = strings.TrimSuffix(text, "^D\n")
	return []byte(text)
}

// archiveFile is one file of a txtar archive.
type archiveFile struct {
	name string
	data string
}

// parseTxtar splits a txtar archive into its files. Text before the first
// "-- name --" marker is a comment and is dropped.
func parseTxtar(text string) []archiveFile {
	var files []archiveFile
	for line := range strings.Lines(text) {
		if name, ok := txtarMarker(line); ok {
			files = append(files, archiveFile{name: name})
			continue
		}
		if len(files) > 0 {
			files[len(files)-1].data += line
		}
	}
	return files
}

// txtarMarker returns the file name from a "-- name --" marker line.
func txtarMarker(line string) (string, bool) {
	name, ok := strings.CutPrefix(strings.TrimSuffix(line, "\n"), "-- ")
	if !ok {
		return "", false
	}
	name, ok = strings.CutSuffix(name, " --")
	return strings.TrimSpace(name), ok
}
