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
	// what the last check found, as State holds it.
	StateName = "update.json"

	// InstallLockName is the name of the lock file in the data directory
	// that every install of an update holds, as LockInstall takes it, so
	// that one flow installs at a time.
	InstallLockName = "update-install.lock"
)

// LogPath returns the path of the update log of data directory dir, which
// the output of every background install is appended to.
func LogPath(dir string) string { return filepath.Join(dir, "logs", "update.log") }

// State is what the last check for updates found, which the state file
// StateName keeps between runs.
type State struct {
	// CheckedAt is when the registry was last asked for the latest release.
	CheckedAt time.Time `json:"checked_at,omitzero"`
	// Latest is the latest release it named, in Canonical form.
	Latest string `json:"latest,omitempty"`
	// Failed is the release a background install last failed to install,
	// which Auto does not install again.
	Failed string `json:"failed,omitempty"`
	// Installed is the release an update installed last, until its first
	// start tells of it, as TakeInstalled reports it.
	Installed string `json:"installed,omitempty"`
}

// File returns the state file of the data directory.
func (u *Updater) File() config.File { return config.File{Dir: u.Dir, Name: StateName} }

// State returns what the state file keeps, or the zero State when there is
// no state file.
func (u *Updater) State() (State, error) {
	var s State
	keys, err := u.File().Read()
	if err != nil || len(keys) == 0 {
		return s, err
	}
	data, err := json.Marshal(keys)
	if err != nil {
		return s, err
	}
	return s, json.Unmarshal(data, &s)
}

// SaveState applies change to what the state file keeps, and saves it, as
// config.File.Edit saves a file.
func (u *Updater) SaveState(change func(*State)) error {
	return u.File().Edit(context.Background(), func(keys map[string]json.RawMessage) error {
		var s State
		if len(keys) > 0 {
			data, err := json.Marshal(keys)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(data, &s); err != nil {
				return err
			}
		}
		change(&s)
		data, err := json.Marshal(s)
		if err != nil {
			return err
		}
		clear(keys)
		return json.Unmarshal(data, &keys)
	})
}

// RecordInstalled saves version as the release an update installed, which
// TakeInstalled reports at its first start, and clears it as failed.
func (u *Updater) RecordInstalled(version string) error {
	return u.SaveState(func(s *State) { s.Installed, s.Failed = Canonical(version), "" })
}

// TakeInstalled returns the release an update installed, as
// RecordInstalled saved it, when it is the running flow, and clears it in
// the same write, so that only the first start of the release, of all the
// flows that start at once, reports it. It returns "" otherwise, and when
// the state file cannot be read or written, which it logs.
func (u *Updater) TakeInstalled() string {
	current := Canonical(u.Current)
	if s, err := u.State(); err != nil || s.Installed == "" || s.Installed != current {
		return ""
	}
	var taken string
	err := u.SaveState(func(s *State) {
		if s.Installed != "" && s.Installed == current {
			taken, s.Installed = s.Installed, ""
		}
	})
	if err != nil {
		slog.Warn("saving the update state", "err", err)
		return ""
	}
	return taken
}

// Check returns the latest release, as Latest finds it, and saves it with
// the time in the state file; but while the state file holds a release
// found less than Interval ago, it returns that one without asking the
// registry.
func (u *Updater) Check(ctx context.Context) (string, error) {
	interval := u.Interval
	if interval == 0 {
		interval = CheckInterval
	}
	s, err := u.State()
	if err != nil {
		slog.Warn("reading the update state", "err", err)
	}
	if age := time.Since(s.CheckedAt); err == nil && s.Latest != "" && age >= 0 && age < interval {
		return s.Latest, nil
	}
	latest, err := u.Latest(ctx)
	if err != nil {
		return "", err
	}
	if err := u.SaveState(func(s *State) { s.CheckedAt, s.Latest = time.Now().UTC(), latest }); err != nil {
		slog.Warn("saving the update state", "err", err)
	}
	return latest, nil
}

// Status is the outcome that Auto reports.
type Status int

const (
	// StatusAvailable reports a newer release that was not installed.
	StatusAvailable Status = iota + 1
	// StatusInstalled reports a newer release that was installed, which
	// flow runs from its next start, and which RecordInstalled saved for
	// that start to tell of.
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

// Auto checks for a release newer than the running flow, as Check does,
// and installs it as Background does, and reports what came of it, which it
// logs too. It reports false, having done nothing, when the running flow is
// no release build, as IsRelease tells, or is up to date, when the check
// fails, when another flow is installing an update, and on Windows, where
// the executable of a running flow cannot be replaced, without checking.
//
// It reports StatusAvailable, without installing, when Plan finds no
// install that it may run, and when the release failed to install before.
func (u *Updater) Auto(ctx context.Context) (Result, bool) {
	if !IsRelease(u.Current) || runtime.GOOS == "windows" {
		return Result{}, false
	}
	latest, err := u.Check(ctx)
	if err != nil {
		slog.Info("update check", "err", err)
		return Result{}, false
	}
	if !Newer(latest, u.Current) {
		return Result{}, false
	}
	available := Result{Status: StatusAvailable, Version: latest}
	if s, err := u.State(); err != nil || s.Failed == latest {
		return available, true
	}
	inst, err := u.Plan(ctx, latest)
	if err != nil {
		slog.Info("update not installed", "version", latest, "err", err)
		return available, true
	}
	slog.Info("installing update", "version", latest, "command", inst.String())
	switch err := u.Background(inst); {
	case errors.Is(err, ErrBusy):
		return Result{}, false
	case err != nil:
		slog.Warn("update failed", "version", latest, "err", err)
		if err := u.SaveState(func(s *State) { s.Failed = latest }); err != nil {
			slog.Warn("saving the update state", "err", err)
		}
		return Result{Status: StatusFailed, Version: latest, Err: err}, true
	}
	slog.Info("update installed", "version", latest)
	if err := u.RecordInstalled(latest); err != nil {
		slog.Warn("saving the update state", "err", err)
	}
	return Result{Status: StatusInstalled, Version: latest}, true
}

// ErrBusy reports an update that another flow is installing.
var ErrBusy = errors.New("another flow is installing an update")

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
// output appended to the update log, LogPath. It holds the install lock
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
	fmt.Fprintf(log, "%s %s\n", time.Now().UTC().Format(time.RFC3339), inst)
	// The install outlives flow, so no context stops it.
	cmd := inst.Command(context.Background())
	cmd.Stdout, cmd.Stderr = log, log
	detach(cmd, lock)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(log, "failed: %v\n", err)
		return fmt.Errorf("%s: %w, see %s", inst, err, log.Name())
	}
	return nil
}
