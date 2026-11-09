package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/internal/cmdline"
	"github.com/unaquatic/flow/product"
	"github.com/unaquatic/flow/update"
)

// updateOptions are the flags of the update command.
type updateOptions struct {
	// check only reports whether a newer release is out.
	check bool
	// force installs the latest release even when flow is up to date, or
	// is a development build.
	force bool
}

var updateCmd = &cmdline.Command[updateOptions]{
	Name:    "update",
	Aliases: []string{"upgrade"},
	Short:   "Update flow to the latest release",
	Flags: func(fs *flag.FlagSet, o *updateOptions) {
		fs.BoolVar(&o.check, "check", false, "Only check for a newer release")
		fs.BoolVar(&o.force, "force", false, "Install the latest release even when flow is up to date")
	},
	Exclusive: [][]string{{"check", "force"}},
	Run: func(ctx context.Context, o updateOptions, _ []string) error {
		dir, err := config.DataDir()
		if err != nil {
			return err
		}
		u, err := update.New(dir)
		if err != nil {
			return err
		}
		return runUpdate(ctx, os.Stdout, u, o, (*exec.Cmd).Run)
	},
}

// runUpdate asks the registry for the latest release, saves it as u's
// state, and, unless o only checks, installs it with the package manager
// that installed flow, its output to w and standard error, when it is newer
// than the running flow or o forces it. It writes what it found and did to
// w. install runs the install's command, as (*exec.Cmd).Run does, under the
// install lock, and fails with update.ErrBusy, installing nothing, while
// another flow installs an update.
func runUpdate(ctx context.Context, w io.Writer, u *update.Updater, o updateOptions, install func(*exec.Cmd) error) error {
	latest, err := u.Latest(ctx)
	if err != nil {
		return err
	}
	if err := u.SaveState(func(s *update.State) { s.CheckedAt, s.Latest = time.Now().UTC(), latest }); err != nil {
		slog.Warn("saving the update state", "err", err)
	}
	name := product.Name
	switch {
	case o.force:
	case !update.IsRelease(u.Current):
		if o.check {
			fmt.Fprintf(w, "%s %s is a development build, %s is the latest release.\n", name, u.Current, latest)
		} else {
			fmt.Fprintf(w, "%s %s is a development build, run %s update --force to install %s.\n", name, u.Current, name, latest)
		}
		return nil
	case !update.Newer(latest, u.Current):
		fmt.Fprintf(w, "%s %s is up to date.\n", name, u.Current)
		return nil
	case o.check:
		fmt.Fprintf(w, "%s %s is available, and this is %s. Run %s update to install it.\n", name, latest, u.Current, name)
		return nil
	}

	inst, err := u.Plan(ctx, latest)
	if errors.Is(err, update.ErrUnmanaged) {
		return fmt.Errorf("cannot update %s: %w, run %s", name, err, update.Install{Args: update.MethodNPM.InstallArgs("latest")})
	}
	if err != nil {
		return fmt.Errorf("cannot update %s: %w", name, err)
	}
	// No other flow, in the background or not, installs meanwhile.
	lock, err := u.LockInstall()
	if err != nil {
		return fmt.Errorf("cannot update %s: %w", name, err)
	}
	defer lock.Close()
	fmt.Fprintf(w, "%s %s is updating to %s with %s\n", name, u.Current, latest, inst)
	cmd := inst.Command(ctx)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, w, os.Stderr
	if err := install(cmd); err != nil {
		return fmt.Errorf("%s: %w", inst, err)
	}
	// The first start of the release tells of it.
	if err := u.RecordInstalled(latest); err != nil {
		slog.Warn("saving the update state", "err", err)
	}
	fmt.Fprintf(w, "%s updated to %s.\n", name, latest)
	return nil
}

// autoUpdate checks for a newer release of flow in the background, and
// installs it, as update.Updater.Auto does, unless cfg's AutoUpdate is off
// or config.EnvDisableAutoUpdate turns it off. The release runs from the
// next start, which tells of it. An install outlives ctx, and flow; ctx
// stops the check.
func autoUpdate(ctx context.Context, cfg *config.Config) {
	if !cfg.AutoUpdate || config.AutoUpdateDisabled() {
		return
	}
	u, err := update.New(cfg.HomeDir)
	if err != nil {
		slog.Warn("update check", "err", err)
		return
	}
	go u.Auto(ctx)
}

// justUpdated returns the release of flow that an update installed, when
// this is its first start, as update.Updater.TakeInstalled reports it, or
// "".
func justUpdated(cfg *config.Config) string {
	u, err := update.New(cfg.HomeDir)
	if err != nil {
		slog.Warn("update state", "err", err)
		return ""
	}
	return u.TakeInstalled()
}
