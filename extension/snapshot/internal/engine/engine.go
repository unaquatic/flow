// Package engine records the files of a worktree in a SQLite database and
// restores them, for the snapshot extension.
//
// Blobs and trees use Git's object format, so the hash of a snapshot equals
// the tree hash Git would write for the same files. No Git executable is run,
// and the Git repository holding the worktree, if any, is consulted only for
// its info/exclude file.
package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

var (
	// ErrDisabled is returned by the operations of a disabled Service.
	ErrDisabled = errors.New("snapshot: disabled")

	// ErrNoTree is returned when an operation is given the zero Hash, which
	// names no tree.
	ErrNoTree = errors.New("snapshot: no tree")
)

// Service records snapshots for one worktree. Track and Checkout open the
// worktree for the call and work in write transactions on the store, which
// run one at a time on the same database. Patch and FileDiffs
// read only recorded objects, which never change, and take no transaction.
//
// Track, Diff, Patch, FileDiffs, and Checkout fail with [ErrDisabled] when
// the Service is disabled, and with [ErrNoTree] when given the zero Hash.
type Service struct {
	// Disabled makes the operations on snapshots fail.
	Disabled bool

	// Worktree is the project directory that snapshots record.
	Worktree string

	// Store holds the snapshots and the worktree's file index.
	Store *Store

	// Skip holds the directories that snapshots leave out.
	Skip []string
}

// NewService returns a Service recording worktree into store. Snapshots leave
// out every directory in skip that lies inside the worktree, as
// [OpenWorktree] does.
func NewService(store *Store, worktree string, skip ...string) *Service {
	return &Service{Worktree: worktree, Store: store, Skip: slices.Clone(skip)}
}

// Track records the current worktree and returns the hash of its tree.
func (s *Service) Track(ctx context.Context) (Hash, error) {
	if err := s.ready(); err != nil {
		return Hash{}, err
	}
	var h Hash
	err := s.transactWorktree(ctx, func(tx *Tx, w *Worktree) error {
		var err error
		h, err = w.Stage(ctx, tx)
		return err
	})
	if err != nil {
		return Hash{}, err
	}
	return h, nil
}

// Diff returns the unified diff between the tree from and the current
// worktree, without its final newline. It records the worktree first, as
// Track does.
func (s *Service) Diff(ctx context.Context, from Hash) (string, error) {
	if err := s.ready(from); err != nil {
		return "", err
	}
	to, err := s.Track(ctx)
	if err != nil {
		return "", err
	}
	patch, err := s.Store.Patch(ctx, from, to)
	return strings.TrimSuffix(patch, "\n"), err
}

// Patch returns the unified diff between two trees, without its final
// newline. It reads only recorded trees, so it neither touches the worktree
// nor the file index. It returns an empty patch when the trees are equal.
func (s *Service) Patch(ctx context.Context, from, to Hash) (string, error) {
	if err := s.ready(from, to); err != nil {
		return "", err
	}
	if from == to {
		return "", nil
	}
	patch, err := s.Store.Patch(ctx, from, to)
	return strings.TrimSuffix(patch, "\n"), err
}

// FileDiff summarizes one file's change between two snapshots.
type FileDiff struct {
	// Path is the file's slash-separated path.
	Path string

	// Status is the kind of change.
	Status Status

	// Additions and Deletions count the lines added and deleted. Both are
	// zero for a binary file.
	Additions, Deletions int
}

// FileDiffs returns the status and line counts of each file that differs
// between two trees. A binary file has no line counts.
func (s *Service) FileDiffs(ctx context.Context, from, to Hash) ([]FileDiff, error) {
	if err := s.ready(from, to); err != nil {
		return nil, err
	}
	return s.Store.FileDiffs(ctx, from, to)
}

// Checkout resets the worktree to tree, in both directions. Files the tree does
// not hold are deleted, so the result depends only on tree and never on the
// state it replaces. Anything the exclude rules ignore is untracked and
// survives unless the tree puts a file where it stands.
//
// Checkout first records the worktree, as Track does, and commits the record
// before it changes any file, so the state it replaces stays in the store
// even when the checkout fails partway.
func (s *Service) Checkout(ctx context.Context, tree Hash) error {
	if err := s.ready(tree); err != nil {
		return err
	}
	if _, err := s.Track(ctx); err != nil {
		return err
	}
	return s.transactWorktree(ctx, func(tx *Tx, w *Worktree) error {
		return w.Checkout(ctx, tx, tree)
	})
}

// Cleanup is meant to drop stale snapshot data. Pruning is not implemented
// yet, so it fails with an error wrapping [errors.ErrUnsupported].
func (s *Service) Cleanup(ctx context.Context) error {
	if err := s.ready(); err != nil {
		return err
	}
	return fmt.Errorf("snapshot: cleanup: %w", errors.ErrUnsupported)
}

// SchedulePrune is meant to start running Cleanup every interval until ctx
// is canceled. Pruning is not implemented yet, so it starts nothing and fails
// with an error wrapping [errors.ErrUnsupported].
func (s *Service) SchedulePrune(ctx context.Context, every time.Duration) error {
	if err := s.ready(); err != nil {
		return err
	}
	return fmt.Errorf("snapshot: schedule pruning: %w", errors.ErrUnsupported)
}

// ready reports why the Service cannot operate on trees: [ErrDisabled] when
// it is disabled, and [ErrNoTree] when one of trees is the zero Hash.
func (s *Service) ready(trees ...Hash) error {
	if s.Disabled {
		return ErrDisabled
	}
	if slices.ContainsFunc(trees, Hash.IsZero) {
		return ErrNoTree
	}
	return nil
}

// transactWorktree runs fn in a transaction on the store, with the worktree
// open and its skipped directories left out, and commits when fn succeeds.
func (s *Service) transactWorktree(ctx context.Context, fn func(*Tx, *Worktree) error) error {
	tx, err := s.Store.Begin(ctx)
	if err != nil {
		return err
	}
	w, err := OpenWorktree(s.Worktree, s.Skip...)
	if err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if err := errors.Join(fn(tx, w), w.Close()); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	return tx.Commit()
}
