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
	"strconv"
	"strings"

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

// runUpdate asks for the latest release and, unless o only checks, installs
// it when it is newer than the running flow or o forces it: from its GitHub
// release for a flow that the install script installed, as
// update.Updater.Replace does, and otherwise with the package manager that
// installed flow, whose output it shows only when the install fails. It
// writes to w what it found and did, versions without their v, each status
// in bold, as introLine draws it, where w shows colors: on a terminal,
// "Checking for updates..." while it asks, the download's URL and progress
// while it downloads, and "Installing update..." while it installs, each on
// a line that what follows replaces.
// install runs the install's command, as (*exec.Cmd).Run does, under the
// install lock, and fails with update.ErrBusy, installing nothing, while
// another flow installs an update.
func runUpdate(ctx context.Context, w io.Writer, u *update.Updater, o updateOptions, install func(*exec.Cmd) error) error {
	name := product.Name
	// fail reports err as an error of the update command, as the command
	// line shows it after its "error:" label, such as "flow update: busy:
	// another flow is installing an update".
	fail := func(err error) error { return fmt.Errorf("%s update: %w", name, err) }
	color := showsColor(w)
	status := newLiveLine(w)
	status.Show(introLine("Checking for updates...", color))
	latest, err := u.Latest(ctx)
	status.Clear()
	if err != nil {
		return fail(err)
	}
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
		return fail(fmt.Errorf("%w; run %s", err, update.InstallCommand))
	}
	if err != nil {
		return fail(err)
	}
	// No other flow, in the background or not, installs meanwhile.
	lock, err := u.LockInstall()
	if err != nil {
		return fail(err)
	}
	defer lock.Close()
	fmt.Fprintf(w, "%s %s → %s\n", introLine("Updating", color), from, to)
	hl := highlighter(w)
	installing := introLine("Installing update...", color)
	if inst.Method == update.MethodStandalone {
		u.Progress = func(url string, done, total int64) {
			status.Show(accountIndent + url + " [" + downloadProgress(done, total) + "]")
		}
		u.Installing = func() { status.Show(installing) }
		if err := u.Replace(ctx, latest); err != nil {
			status.Keep()
			fmt.Fprintln(w)
			return fail(err)
		}
		status.Clear()
	} else {
		fmt.Fprintf(w, "%s%s\n", accountIndent, hl(inst.String()))
		var out bytes.Buffer
		cmd := inst.Command(ctx)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, &out, &out
		status.Show(installing)
		err := install(cmd)
		status.Clear()
		if err != nil {
			fmt.Fprintf(w, "\n%s", out.String())
			return fail(fmt.Errorf("install %s: %w", inst, err))
		}
	}
	fmt.Fprintf(w, "\n%s\n", introLine("Installed successfully", color))
	return nil
}

// displayVersion returns v, a version such as v1.2.3, as flow update shows
// it: without its v, as 1.2.3, and a development build's as it is.
func displayVersion(v string) string {
	return strings.TrimPrefix(v, "v")
}

// downloadProgress returns how far a download of total bytes is when done
// of them are in, as a whole percent, such as "42%", or, when total is
// unknown, as the size done, such as "3.2 MB".
func downloadProgress(done, total int64) string {
	if total > 0 {
		return strconv.FormatInt(min(done*100/total, 100), 10) + "%"
	}
	return fmt.Sprintf("%.1f MB", float64(done)/(1<<20))
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

// justUpdated returns the version of the running flow when an update, of
// any kind, brought it since the last start, as update.Updater.Updated
// tells, or "".
func justUpdated(cfg *config.Config) string {
	u, err := update.New(cfg.HomeDir)
	if err != nil {
		slog.Warn("update state", "err", err)
		return ""
	}
	return u.Updated()
}
