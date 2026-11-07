package flock

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

// openLockFile opens the file at path, creating it, and closes it when the
// test ends.
func openLockFile(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func TestLockConflicts(t *testing.T) {
	tests := []struct {
		name     string
		held     bool // whether the held lock is exclusive
		wanted   bool // whether the wanted lock is exclusive
		conflict bool
	}{
		{"shared then shared", false, false, false},
		{"shared then exclusive", false, true, true},
		{"exclusive then shared", true, false, true},
		{"exclusive then exclusive", true, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "x.lock")
				held, wanted := openLockFile(t, path), openLockFile(t, path)
				if err := Lock(t.Context(), held, tt.held); err != nil {
					t.Fatal(err)
				}

				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				err := Lock(ctx, wanted, tt.wanted)
				if tt.conflict {
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatalf("lock while held: error = %v, want deadline exceeded", err)
					}
				} else {
					if err != nil {
						t.Fatalf("lock while held: %v", err)
					}
					if err := Unlock(wanted); err != nil {
						t.Fatal(err)
					}
				}

				if err := Unlock(held); err != nil {
					t.Fatal(err)
				}
				if err := Lock(t.Context(), wanted, tt.wanted); err != nil {
					t.Fatalf("lock after release: %v", err)
				}
				if err := Unlock(wanted); err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

// TestLockWaits checks that a waiter takes the lock once its holder releases
// it.
func TestLockWaits(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "x.lock")
		held, waiter := openLockFile(t, path), openLockFile(t, path)
		if err := Lock(t.Context(), held, true); err != nil {
			t.Fatal(err)
		}
		const holdFor = time.Second
		go func() {
			time.Sleep(holdFor)
			if err := Unlock(held); err != nil {
				t.Error(err)
			}
		}()

		start := time.Now()
		if err := Lock(t.Context(), waiter, true); err != nil {
			t.Fatal(err)
		}
		if waited := time.Since(start); waited < holdFor {
			t.Errorf("took the lock after %v, while it was held for %v", waited, holdFor)
		}
		if err := Unlock(waiter); err != nil {
			t.Fatal(err)
		}
	})
}
