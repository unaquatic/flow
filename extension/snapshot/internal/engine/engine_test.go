package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unaquatic/flow/database"
	"github.com/unaquatic/flow/product"
)

// requireGit skips a test when Git is unavailable. Only the tests that
// compare results with Git's need it.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git not available: %v", err)
	}
}

// newTestService returns a service with an isolated worktree and database,
// and the worktree's path.
func newTestService(t *testing.T) (*Service, string) {
	t.Helper()
	isolateGitConfig(t)
	root := t.TempDir()
	worktree := filepath.Join(root, "worktree")
	if err := os.MkdirAll(worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	return NewService(projectStore(t, openTestDB(t), worktree), worktree), worktree
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// track calls s.Track or fails the test.
func track(t *testing.T, s *Service) Hash {
	t.Helper()
	h, err := s.Track(t.Context())
	if err != nil {
		t.Fatalf("track: %v", err)
	}
	return h
}

func TestTrack_ProducesStableHashForUnchangedTree(t *testing.T) {
	s, wt := newTestService(t)
	writeFile(t, wt, "a.txt", "hello")
	h1 := track(t, s)
	if h1.IsZero() {
		t.Fatal("expected a non-zero hash")
	}
	if h2 := track(t, s); h1 != h2 {
		t.Fatalf("hash changed without edits: %s vs %s", h1, h2)
	}
}

// TestTrack_WritesOnlyTheDatabase checks that snapshots live in the database
// and leave the worktree as it was.
func TestTrack_WritesOnlyTheDatabase(t *testing.T) {
	s, wt := newTestService(t)
	writeFile(t, wt, "a.txt", "hello")
	track(t, s)

	// One blob and the root tree.
	if n := countObjects(t, s.Store); n != 2 {
		t.Errorf("database holds %d objects after Track, want 2", n)
	}
	entries, err := os.ReadDir(wt)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "a.txt" {
		t.Errorf("worktree holds %v, want only a.txt", entries)
	}
}

// TestTrack_SkipsDataDir puts the data directory inside the worktree, as
// happens when starting in the user's home directory. The snapshot database
// must neither record itself nor be restored.
func TestTrack_SkipsDataDir(t *testing.T) {
	isolateGitConfig(t)
	wt := t.TempDir()
	writeFile(t, wt, "a.txt", "v1")
	home := filepath.Join(wt, "."+product.ID)
	path := filepath.Join(home, database.FileName)
	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := NewService(projectStore(t, db, wt), wt, home)
	first := track(t, s)
	if second := track(t, s); second != first {
		t.Errorf("tracking twice gave %s, then %s", first, second)
	}

	if got, want := treeNames(t, s.Store, first), []string{"a.txt"}; !slices.Equal(got, want) {
		t.Errorf("root = %q, want %q", got, want)
	}

	writeFile(t, wt, "a.txt", "v2")
	if err := s.Checkout(t.Context(), first); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	wantContent(t, wt, "a.txt", "v1")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("checkout removed the database: %v", err)
	}
}

func TestCheckout_RestoresTrackedTree(t *testing.T) {
	s, wt := newTestService(t)
	writeFile(t, wt, "edit.txt", "v1")
	h := track(t, s)

	writeFile(t, wt, "edit.txt", "v2")
	writeFile(t, wt, "sub/new.txt", "made by agent")
	if err := s.Checkout(t.Context(), h); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	wantContent(t, wt, "edit.txt", "v1")
	wantMissing(t, wt, "sub")
	if got := track(t, s); got != h {
		t.Errorf("tree after checkout = %s, want %s", got, h)
	}
}

func TestService_RejectsUnknownTrees(t *testing.T) {
	s, wt := newTestService(t)
	writeFile(t, wt, "a.txt", "v1")
	h := track(t, s)
	ctx := t.Context()

	unknown := mustParseHash(t, "0123456789abcdef0123456789abcdef01234567")
	checks := map[string]func() error{
		"checkout":  func() error { return s.Checkout(ctx, unknown) },
		"diff":      func() error { _, err := s.Diff(ctx, unknown); return err },
		"patch":     func() error { _, err := s.Patch(ctx, h, unknown); return err },
		"fileDiffs": func() error { _, err := s.FileDiffs(ctx, unknown, h); return err },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if err := check(); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("err = %v, want fs.ErrNotExist", err)
			}
		})
	}
	wantContent(t, wt, "a.txt", "v1")
}

func TestDiff_ReportsWorktreeChangesSinceTree(t *testing.T) {
	s, wt := newTestService(t)
	writeFile(t, wt, "a.txt", "one\n")
	h := track(t, s)
	writeFile(t, wt, "a.txt", "two\n")

	out, err := s.Diff(t.Context(), h)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	for _, want := range []string{"diff --git a/a.txt b/a.txt", "-one", "+two"} {
		if !strings.Contains(out, want) {
			t.Fatalf("diff missing %q:\n%s", want, out)
		}
	}
}

func TestDiff_OfUnchangedWorktreeIsEmpty(t *testing.T) {
	s, wt := newTestService(t)
	writeFile(t, wt, "a.txt", "v1")
	h := track(t, s)
	if out, err := s.Diff(t.Context(), h); out != "" || err != nil {
		t.Errorf("diff = %q, %v; want empty", out, err)
	}
}

// TestDiff_RecordsTheWorktree checks that Diff tracks the worktree, so a
// Track right after it has nothing new to store.
func TestDiff_RecordsTheWorktree(t *testing.T) {
	s, wt := newTestService(t)
	writeFile(t, wt, "a.txt", "one\n")
	from := track(t, s)
	writeFile(t, wt, "a.txt", "two\n")
	if _, err := s.Diff(t.Context(), from); err != nil {
		t.Fatalf("diff: %v", err)
	}
	stored := countObjects(t, s.Store)
	track(t, s)
	if n := countObjects(t, s.Store); n != stored {
		t.Errorf("Track after Diff stored %d new objects, want 0", n-stored)
	}
}

// TestTrack_RollsBackWhenStoringFails makes the database refuse the third
// index entry while workers are still reading files. Track must return the
// error rather than hang, and leave the store as it was.
func TestTrack_RollsBackWhenStoringFails(t *testing.T) {
	s, wt := newTestService(t)
	for i := range 64 {
		writeFile(t, wt, fmt.Sprintf("f%02d.txt", i), strings.Repeat("x", i))
	}
	_, err := s.Store.db.Exec(`
		CREATE TRIGGER refuse BEFORE INSERT ON entries
		WHEN (SELECT COUNT(*) FROM entries) >= 2
		BEGIN SELECT RAISE(ABORT, 'refused by test'); END`)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.Track(t.Context()); err == nil || !strings.Contains(err.Error(), "refused by test") {
		t.Fatalf("track: err = %v, want the refused insert", err)
	}
	if n := countObjects(t, s.Store); n != 0 {
		t.Errorf("a failed Track left %d objects, want 0", n)
	}
	if _, err := s.Store.db.Exec(`DROP TRIGGER refuse`); err != nil {
		t.Fatal(err)
	}
	track(t, s)
}

func TestTrack_FailsWhenCanceled(t *testing.T) {
	s, wt := newTestService(t)
	writeFile(t, wt, "a.txt", "v1")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Track(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	if n := countObjects(t, s.Store); n != 0 {
		t.Errorf("a canceled Track stored %d objects, want 0", n)
	}
}

// TestPatch_KeepsTrailingWhitespace checks that only the final newline is
// dropped: the spaces ending the last line are part of the change.
func TestPatch_KeepsTrailingWhitespace(t *testing.T) {
	s, wt := newTestService(t)
	writeFile(t, wt, "a.txt", "one\n")
	from := track(t, s)
	writeFile(t, wt, "a.txt", "two  \n")
	to := track(t, s)

	out, err := s.Patch(t.Context(), from, to)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if !strings.HasSuffix(out, "\n+two  ") {
		t.Errorf("patch ends %q, want it to end with the whole line %q", out[max(0, len(out)-10):], "+two  ")
	}
}

func TestPatch_ReportsChangesBetweenTrees(t *testing.T) {
	s, wt := newTestService(t)
	writeFile(t, wt, "a.txt", "one\n")
	from := track(t, s)
	writeFile(t, wt, "a.txt", "two\n")
	to := track(t, s)

	out, err := s.Patch(t.Context(), from, to)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	want := "diff --git a/a.txt b/a.txt\nindex 5626abf0..f719efd4 100644\n--- a/a.txt\n+++ b/a.txt\n@@ -1 +1 @@\n-one\n+two"
	if out != want {
		t.Errorf("patch =\n%s\nwant\n%s", out, want)
	}
}

func TestFileDiffs_ReportsFileDiffs(t *testing.T) {
	s, wt := newTestService(t)
	writeFile(t, wt, "a.txt", "one\n")
	from := track(t, s)
	writeFile(t, wt, "a.txt", "one\ntwo\n")
	to := track(t, s)

	diffs, err := s.FileDiffs(t.Context(), from, to)
	if err != nil {
		t.Fatalf("file diffs: %v", err)
	}
	want := FileDiff{Path: "a.txt", Status: Modified, Additions: 1}
	if len(diffs) != 1 || diffs[0] != want {
		t.Errorf("diffs = %+v, want [%+v]", diffs, want)
	}
}

func TestService_RejectsTheZeroHash(t *testing.T) {
	s, wt := newTestService(t)
	writeFile(t, wt, "a.txt", "v1")
	h := track(t, s)
	writeFile(t, wt, "a.txt", "v2")
	ctx := t.Context()

	var zero Hash
	checks := map[string]func() error{
		"diff":            func() error { _, err := s.Diff(ctx, zero); return err },
		"patch from":      func() error { _, err := s.Patch(ctx, zero, h); return err },
		"patch to":        func() error { _, err := s.Patch(ctx, h, zero); return err },
		"file diffs from": func() error { _, err := s.FileDiffs(ctx, zero, h); return err },
		"file diffs to":   func() error { _, err := s.FileDiffs(ctx, h, zero); return err },
		"checkout":        func() error { return s.Checkout(ctx, zero) },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if err := check(); !errors.Is(err, ErrNoTree) {
				t.Errorf("err = %v, want ErrNoTree", err)
			}
		})
	}
	wantContent(t, wt, "a.txt", "v2")
}

func TestPatch_OfEqualTreesIsEmpty(t *testing.T) {
	s, wt := newTestService(t)
	writeFile(t, wt, "a.txt", "v1")
	h := track(t, s)
	if out, err := s.Patch(t.Context(), h, h); out != "" || err != nil {
		t.Errorf("patch = %q, %v; want empty", out, err)
	}
}

func TestDisabled_RejectsEveryOperation(t *testing.T) {
	s, wt := newTestService(t)
	writeFile(t, wt, "a.txt", "v1")
	from := track(t, s)
	writeFile(t, wt, "a.txt", "v2")
	to := track(t, s)
	stored := countObjects(t, s.Store)
	writeFile(t, wt, "a.txt", "v3")
	s.Disabled = true
	ctx := t.Context()

	checks := map[string]func() error{
		"track":      func() error { _, err := s.Track(ctx); return err },
		"diff":       func() error { _, err := s.Diff(ctx, from); return err },
		"patch":      func() error { _, err := s.Patch(ctx, from, to); return err },
		"file diffs": func() error { _, err := s.FileDiffs(ctx, from, to); return err },
		"checkout":   func() error { return s.Checkout(ctx, from) },
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			if err := check(); !errors.Is(err, ErrDisabled) {
				t.Errorf("err = %v, want ErrDisabled", err)
			}
		})
	}
	wantContent(t, wt, "a.txt", "v3")
	if n := countObjects(t, s.Store); n != stored {
		t.Errorf("a disabled service stored objects: %d, want %d", n, stored)
	}
}

func TestSchedulePrune_IsUnsupported(t *testing.T) {
	s, _ := newTestService(t)
	if err := s.SchedulePrune(t.Context(), time.Hour); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("err = %v, want errors.ErrUnsupported", err)
	}
}

func TestCleanup_LeavesSnapshotsUsable(t *testing.T) {
	s, wt := newTestService(t)
	writeFile(t, wt, "a.txt", "v1")
	h := track(t, s)

	if err := s.Cleanup(t.Context()); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("cleanup: err = %v, want errors.ErrUnsupported", err)
	}
	writeFile(t, wt, "a.txt", "v2")
	if err := s.Checkout(t.Context(), h); err != nil {
		t.Fatalf("checkout after cleanup: %v", err)
	}
	wantContent(t, wt, "a.txt", "v1")
}

// TestService_SerializesConcurrentCalls runs Track from many goroutines, which
// all share the service's worktree and store.
func TestService_SerializesConcurrentCalls(t *testing.T) {
	s, wt := newTestService(t)
	writeFile(t, wt, "a.txt", "shared")
	want := track(t, s)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			got, err := s.Track(t.Context())
			if err != nil || got != want {
				t.Errorf("concurrent track = %s, %v; want %s", got, err, want)
			}
		})
	}
	wg.Wait()
}
