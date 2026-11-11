package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/internal/flock"
)

const (
	// StateName is the name of the file in the data directory that keeps
	// the version of flow that ran last, as Updated reads and writes it:
	// {"last_version": "v1.2.3"}.
	StateName = "update.json"

	// InstallLockName is the name of the lock file in the data directory
	// that every install of an update holds, as LockInstall takes it, so
	// that one flow installs at a time.
	InstallLockName = "update-install.lock"
)

// LogPath returns the path of the update log of data directory dir, which
// the output of every background install is appended to.
func LogPath(dir string) string { return filepath.Join(dir, "logs", "update.log") }

// File returns the state file of the data directory.
func (u *Updater) File() config.File { return config.File{Dir: u.Dir, Name: StateName} }

// Updated returns the running flow's version when it is a release newer than
// the one that ran last, as the state file keeps it, which an update, of any
// kind, brought, and "" otherwise. It saves the running release as the one
// that ran last, in the same write, under the file's lock, so that of the
// flows of a release that start at once only the first reports it. A first
// start, with no state file yet, reports none; so does a build that is no
// release, which it does not save. It logs what fails, and reports none then.
func (u *Updater) Updated() string {
	current := Canonical(u.Current)
	if !IsRelease(current) {
		return ""
	}
	var updated string
	err := u.File().Edit(context.Background(), func(keys map[string]json.RawMessage) error {
		var last string
		if raw, ok := keys["last_version"]; ok {
			if err := json.Unmarshal(raw, &last); err != nil {
				return err
			}
		}
		if last != "" && Newer(current, last) {
			updated = current
		}
		// What older builds kept goes with the first write.
		clear(keys)
		data, err := json.Marshal(current)
		keys["last_version"] = data
		return err
	})
	if err != nil {
		slog.Warn("saving the update state", "err", err)
		return ""
	}
	return updated
}

// Status is the outcome that Auto reports.
type Status int

const (
	// StatusAvailable reports a newer release that was not installed.
	StatusAvailable Status = iota + 1
	// StatusInstalled reports a newer release that was installed, which
	// flow runs from its next start, which Updated tells of.
	StatusInstalled
	// StatusFailed reports a newer release that failed to install.
	StatusFailed
)

// Result is what Auto did about a newer release.
type Result struct {
	// Status is what came of it.
	Status Status
	// Version is the release, in Canonical form.
	Version string
	// Err is why it failed to install, with StatusFailed.
	Err error
}

// Auto checks for a release newer than the running flow, as Latest does,
// and installs it as Background does, and reports what came of it, which it
// logs too. It reports false, having done nothing, when the running flow is
// no release build, as IsRelease tells, or is up to date, when the check
// fails, when another flow is installing an update, and on Windows, where
// the executable of a running flow cannot be replaced, without checking.
// A release that failed to install is tried again at the next start.
//
// It reports StatusAvailable, without installing, when Plan finds no
// install that it may run.
func (u *Updater) Auto(ctx context.Context) (Result, bool) {
	if !IsRelease(u.Current) || runtime.GOOS == "windows" {
		return Result{}, false
	}
	latest, err := u.Latest(ctx)
	if err != nil {
		slog.Info("update check", "err", err)
		return Result{}, false
	}
	if !Newer(latest, u.Current) {
		return Result{}, false
	}
	inst, err := u.Plan(ctx, latest)
	if err != nil {
		slog.Info("update not installed", "version", latest, "err", err)
		return Result{Status: StatusAvailable, Version: latest}, true
	}
	slog.Info("installing update", "version", latest, "command", inst.String())
	switch err := u.Background(inst); {
	case errors.Is(err, ErrBusy):
		return Result{}, false
	case err != nil:
		slog.Warn("update failed", "version", latest, "err", err)
		return Result{Status: StatusFailed, Version: latest, Err: err}, true
	}
	slog.Info("update installed", "version", latest)
	return Result{Status: StatusInstalled, Version: latest}, true
}

// ErrBusy reports an update that another flow is installing.
var ErrBusy = errors.New("busy: another flow is installing an update")

// LockInstall takes the lock file InstallLockName, which every install of
// an update holds, so that no two run at once, and returns it, held until
// it is closed, or until whichever process inherited it exits too. It fails
// with ErrBusy, without waiting, when another flow holds it. Where files
// cannot be locked, it returns the file unlocked.
func (u *Updater) LockInstall() (*os.File, error) {
	if err := os.MkdirAll(u.Dir, 0o700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(u.Dir, InstallLockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	switch ok, err := flock.TryLock(lock, true); {
	case errors.Is(err, errors.ErrUnsupported):
	case err != nil:
		return nil, errors.Join(err, lock.Close())
	case !ok:
		return nil, errors.Join(ErrBusy, lock.Close())
	}
	return lock, nil
}

// Background runs inst and waits for it, apart from flow and its terminal,
// so that it outlives flow and no key pressed there stops it, with its
// output appended to the update log, LogPath; for MethodStandalone it
// replaces the executable itself, as Replace does, which flow exiting
// meanwhile stops, leaving the executable as it was. It holds the install lock
// meanwhile, as LockInstall takes it, which inst holds too where it can
// inherit it, so that no other flow installs until inst is done, even once
// this one has exited. It fails with ErrBusy when another holds the lock.
func (u *Updater) Background(inst Install) error {
	lock, err := u.LockInstall()
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := os.MkdirAll(filepath.Dir(LogPath(u.Dir)), 0o700); err != nil {
		return err
	}
	log, err := os.OpenFile(LogPath(u.Dir), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	if inst.Method == MethodStandalone {
		fmt.Fprintf(log, "%s replacing %s with %s\n", time.Now().UTC().Format(time.RFC3339), u.Executable, inst.Version)
		if err := u.Replace(context.Background(), inst.Version); err != nil {
			fmt.Fprintf(log, "failed: %v\n", err)
			return fmt.Errorf("%w; see %s", err, log.Name())
		}
		return nil
	}
	fmt.Fprintf(log, "%s %s\n", time.Now().UTC().Format(time.RFC3339), inst)
	// The install outlives flow, so no context stops it.
	cmd := inst.Command(context.Background())
	cmd.Stdout, cmd.Stderr = log, log
	detach(cmd, lock)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(log, "failed: %v\n", err)
		return fmt.Errorf("install %s: %w; see %s", inst, err, log.Name())
	}
	return nil
}
