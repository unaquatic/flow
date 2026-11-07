package main

import (
	"context"
	"fmt"
	"io"

	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/extension/snapshot"
	"github.com/unaquatic/flow/internal/cmdline"
)

// withSnapshots returns a Command.Run that starts the app and passes run its
// snapshot extension, with standard output as w. It fails with
// [snapshot.ErrDisabled] when snapshots are disabled.
func withSnapshots(run func(ctx context.Context, w io.Writer, s *snapshot.Extension, args []string) error) func(context.Context, cmdline.NoOptions, []string) error {
	return withApp(func(ctx context.Context, w io.Writer, a *app.App, _ cmdline.NoOptions, args []string) error {
		s, ok := extension.Find[*snapshot.Extension](a.Extensions())
		if !ok || s.Disabled() {
			return snapshot.ErrDisabled
		}
		return run(ctx, w, s, args)
	})
}

var debugCmd = &cmdline.Command[cmdline.NoOptions]{
	Name:     "debug",
	Short:    "Debug utilities",
	Commands: []cmdline.Node{snapshotCmd},
}

var snapshotCmd = &cmdline.Command[cmdline.NoOptions]{
	Name:  "snapshot",
	Short: "Workspace snapshot debugging utilities",
	Commands: []cmdline.Node{
		snapshotTrackCmd,
		snapshotDiffCmd,
		snapshotCheckoutCmd,
		snapshotCleanupCmd,
	},
}

var snapshotTrackCmd = &cmdline.Command[cmdline.NoOptions]{
	Name:  "track",
	Short: "Capture the current workspace as a snapshot tree and print its hash",
	Run: withSnapshots(func(ctx context.Context, w io.Writer, s *snapshot.Extension, _ []string) error {
		hash, err := s.Service.Track(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintln(w, hash)
		return nil
	}),
}

var snapshotDiffCmd = &cmdline.Command[cmdline.NoOptions]{
	Name:  "diff",
	Usage: "<hash>",
	Short: "Print a unified diff between the given snapshot and the current worktree",
	Args:  cmdline.ExactArgs(1),
	Run: withSnapshots(func(ctx context.Context, w io.Writer, s *snapshot.Extension, args []string) error {
		from, err := snapshot.ParseHash(args[0])
		if err != nil {
			return err
		}
		out, err := s.Service.Diff(ctx, from)
		if err != nil {
			return err
		}
		fmt.Fprintln(w, out)
		return nil
	}),
}

var snapshotCheckoutCmd = &cmdline.Command[cmdline.NoOptions]{
	Name:  "checkout",
	Usage: "<hash>",
	Short: "Reset the worktree to the given snapshot tree, deleting files it omits",
	Args:  cmdline.ExactArgs(1),
	Run: withSnapshots(func(ctx context.Context, _ io.Writer, s *snapshot.Extension, args []string) error {
		tree, err := snapshot.ParseHash(args[0])
		if err != nil {
			return err
		}
		return s.Service.Checkout(ctx, tree)
	}),
}

var snapshotCleanupCmd = &cmdline.Command[cmdline.NoOptions]{
	Name:  "cleanup",
	Short: "Drop stale snapshot data (not implemented yet; fails)",
	Run: withSnapshots(func(ctx context.Context, _ io.Writer, s *snapshot.Extension, _ []string) error {
		return s.Service.Cleanup(ctx)
	}),
}
