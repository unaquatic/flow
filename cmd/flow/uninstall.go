package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/internal/cmdline"
	"github.com/unaquatic/flow/product"
	"github.com/unaquatic/flow/terminal"
	"github.com/unaquatic/flow/update"
)

// uninstallOptions are the flags of the uninstall command.
type uninstallOptions struct {
	// data also removes the data directory: the config, sessions and logs.
	data bool
}

var uninstallCmd = &cmdline.Command[uninstallOptions]{
	Name:  "uninstall",
	Short: "Remove flow that the install script installed",
	Flags: func(fs *flag.FlagSet, o *uninstallOptions) {
		fs.BoolVar(&o.data, "data", false, "Also remove the config, sessions and logs")
	},
	Run: func(ctx context.Context, o uninstallOptions, _ []string) error {
		dir, err := config.DataDir()
		if err != nil {
			return err
		}
		u, err := update.New(dir)
		if err != nil {
			return err
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		return runUninstall(os.Stdout, cmdline.UseColor(os.Stdout), u, home, os.Getenv("ZDOTDIR"), o)
	},
}

// runUninstall removes the flow of u that the install script installed, as
// update.Updater.Uninstall does in home, and, when o says so, the data
// directory, and writes to w the banner the install script shows, each step
// it took, and that it is done, bold where color is set. For a flow that npm
// or pnpm installed it fails, removing nothing and writing nothing, naming
// the command that removes it.
func runUninstall(w io.Writer, color bool, u *update.Updater, home, zdotdir string, o uninstallOptions) error {
	name := product.Name
	// fail reports err as an error of the uninstall command, as runUpdate's
	// fail reports those of the update command.
	fail := func(err error) error { return fmt.Errorf("%s uninstall: %w", name, err) }
	removed, err := u.Uninstall(home, zdotdir)
	if errors.Is(err, update.ErrNotStandalone) {
		m := u.Method
		if m == update.MethodNone {
			m = update.DetectMethod(u.Executable)
		}
		if args := m.UninstallArgs(); args != nil {
			return fail(fmt.Errorf("installed by %s, run %s", m, strings.Join(args, " ")))
		}
		return fail(fmt.Errorf("%w, remove it yourself", err))
	}
	if len(removed) == 0 {
		return fail(err)
	}

	bold := func(s string) string { return s }
	banner := terminal.LogoBanner().PlainText()
	if color {
		bold = func(s string) string { return "\x1b[1m" + s + "\x1b[22m" }
		banner = terminal.ANSI(terminal.LogoBanner(), colorMode)
	}
	fmt.Fprintf(w, "%s\n\n%s\n\n", banner, bold("Flow Installer"))
	pathRemoved := false
	for _, path := range removed {
		switch {
		case path == u.Executable:
			fmt.Fprintln(w, "■ Binary file removed")
		case path == update.LinkPath(home):
			fmt.Fprintln(w, "■ Symlink removed")
		case !pathRemoved:
			pathRemoved = true
			fmt.Fprintln(w, "■ Shell setup removed")
		}
	}
	if err != nil {
		return fail(err)
	}
	if o.data {
		if err := os.RemoveAll(u.Dir); err != nil {
			return fail(fmt.Errorf("remove failed: %s: %w", tilde(u.Dir, home), err))
		}
		fmt.Fprintln(w, "■ Config and sessions removed")
	}
	fmt.Fprintf(w, "\n%s\n", bold("Uninstall complete"))
	return nil
}

// tilde returns path as people read it, with ~ for home, which it also
// finds in path with its symbolic links resolved.
func tilde(path, home string) string {
	homes := []string{home}
	if real, err := filepath.EvalSymlinks(home); err == nil && real != home {
		homes = append(homes, real)
	}
	for _, h := range homes {
		if rel, err := filepath.Rel(h, path); err == nil && filepath.IsLocal(rel) {
			return filepath.Join("~", rel)
		}
	}
	return path
}
