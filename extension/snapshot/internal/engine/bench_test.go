package engine

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Sizes of the generated worktree: 100 packages of 100 files in 4
// subdirectories each, 10,000 files in all.
const (
	benchPackages = 100
	benchFiles    = 100
)

// benchWorktree creates a worktree of small files whose modification times
// are an hour old, so staging trusts their index entries.
func benchWorktree(b *testing.B) string {
	b.Helper()
	isolateGitConfig(b)
	dir := filepath.Join(b.TempDir(), "worktree")
	old := time.Now().Add(-time.Hour)
	for p := range benchPackages {
		for f := range benchFiles {
			name := filepath.Join(dir, fmt.Sprintf("pkg%03d", p), fmt.Sprintf("sub%d", f%4), fmt.Sprintf("file%03d.go", f))
			if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
				b.Fatal(err)
			}
			content := fmt.Sprintf("package pkg%03d\n\n// File %d.\nvar V%d = %d\n", p, f, f, p*f)
			if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
				b.Fatal(err)
			}
			if err := os.Chtimes(name, time.Time{}, old); err != nil {
				b.Fatal(err)
			}
		}
	}
	return dir
}

// addIgnoreFiles writes a .gitignore of ten patterns that match nothing into
// every directory below dir, with an old modification time.
func addIgnoreFiles(b *testing.B, dir string) {
	b.Helper()
	old := time.Now().Add(-time.Hour)
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || p == dir {
			return err
		}
		var patterns strings.Builder
		for i := range 10 {
			fmt.Fprintf(&patterns, "*.tmp%d\nbuild%d/\n", i, i)
		}
		name := filepath.Join(p, ".gitignore")
		if err := os.WriteFile(name, []byte(patterns.String()), 0o600); err != nil {
			return err
		}
		return os.Chtimes(name, time.Time{}, old)
	})
	if err != nil {
		b.Fatal(err)
	}
}

// BenchmarkStage measures staging a worktree whose index is already filled,
// and staging one into an empty store, which reads and stores every file.
func BenchmarkStage(b *testing.B) {
	b.Run("first snapshot", func(b *testing.B) {
		w := openWorktree(b, benchWorktree(b))
		for b.Loop() {
			b.StopTimer()
			s := openTestStore(b)
			b.StartTimer()
			stage(b, w, s)
		}
	})
	b.Run("unchanged", func(b *testing.B) {
		dir := benchWorktree(b)
		w, s := openWorktree(b, dir), openTestStore(b)
		stage(b, w, s)
		for b.Loop() {
			stage(b, w, s)
		}
	})
	b.Run("unchanged with a gitignore per directory", func(b *testing.B) {
		dir := benchWorktree(b)
		addIgnoreFiles(b, dir)
		w, s := openWorktree(b, dir), openTestStore(b)
		stage(b, w, s)
		for b.Loop() {
			stage(b, w, s)
		}
	})
	b.Run("one file changed", func(b *testing.B) {
		dir := benchWorktree(b)
		w, s := openWorktree(b, dir), openTestStore(b)
		stage(b, w, s)
		name := filepath.Join(dir, "pkg050", "sub1", "file001.go")
		i := 0
		for b.Loop() {
			i++
			if err := os.WriteFile(name, fmt.Appendf(nil, "package pkg050\n\nvar Edit = %d\n", i), 0o600); err != nil {
				b.Fatal(err)
			}
			stage(b, w, s)
		}
	})
}

// BenchmarkServiceTrack measures Track on an unchanged worktree, including
// opening the worktree for the call.
func BenchmarkServiceTrack(b *testing.B) {
	dir := benchWorktree(b)
	s := NewService(projectStore(b, openTestDB(b), dir), dir)
	ctx := b.Context()
	if _, err := s.Track(ctx); err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := s.Track(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
