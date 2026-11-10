package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
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
// that installed flow, whose output it shows only when the install fails,
// when it is newer than the running flow or o forces it. It writes to w
// what it found and did, versions without their v, each status in bold, as
// introLine draws it, where w shows colors: on a terminal, "Checking for
// updates..." while it asks, and "Installing update..." while it installs,
// each on a line that what follows replaces. install runs the install's
// command, as (*exec.Cmd).Run does, under the install lock, and fails with
// update.ErrBusy, installing nothing, while another flow installs an
// update.
func runUpdate(ctx context.Context, w io.Writer, u *update.Updater, o updateOptions, install func(*exec.Cmd) error) error {
	color := showsColor(w)
	status := newLiveLine(w)
	status.Show(introLine("Checking for updates...", color))
	latest, err := u.Latest(ctx)
	status.Clear()
	if err != nil {
		return err
	}
	if err := u.SaveState(func(s *update.State) { s.CheckedAt, s.Latest = time.Now().UTC(), latest }); err != nil {
		slog.Warn("saving the update state", "err", err)
	}
	name := product.Name
	from, to := displayVersion(u.Current), displayVersion(latest)
	switch {
	case o.force:
	case !update.IsRelease(u.Current):
		return fmt.Errorf("%s %s is a development build, run %s update --force to install", name, u.Current, name)
	case !update.Newer(latest, u.Current):
		fmt.Fprintf(w, "%s %s\n", introLine("Already up to date:", color), from)
		return nil
	case o.check:
		fmt.Fprintf(w, "%s %s → %s\n\n", introLine("New update available:", color), from, to)
		writeField(w, "Update with:", name+" update")
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
	fmt.Fprintf(w, "%s %s → %s\n", introLine("Updating", color), from, to)
	fmt.Fprintf(w, "%s%s\n", accountIndent, highlighter(w)(inst.String()))
	var out bytes.Buffer
	cmd := inst.Command(ctx)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, &out, &out
	status.Show(introLine("Installing update...", color))
	err = install(cmd)
	status.Clear()
	if err != nil {
		fmt.Fprintf(w, "\n%s", out.String())
		return fmt.Errorf("%s: %w", inst, err)
	}
	// The first start of the release tells of it.
	if err := u.RecordInstalled(latest); err != nil {
		slog.Warn("saving the update state", "err", err)
	}
	fmt.Fprintf(w, "\n%s\n", introLine("Installed successfully", color))
	return nil
}

// displayVersion returns v, a version such as v1.2.3, as flow update shows
// it: without its v, as 1.2.3, and a development build's as it is.
func displayVersion(v string) string {
	return strings.TrimPrefix(v, "v")
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
