package update

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The lines that the install script writes around the line of a shell
// profile that adds flow to PATH.
const (
	pathBlockBegin = "# >>> flow >>>"
	pathBlockEnd   = "# <<< flow <<<"
)

// LinkPath returns the link that the install script makes to flow, in
// ~/.local/bin of home.
func LinkPath(home string) string { return filepath.Join(home, ".local", "bin", "flow") }

// Profiles returns the shell profiles of home that the install script may
// add flow to PATH in, with that of zdotdir, ZDOTDIR, when it is set.
func Profiles(home, zdotdir string) []string {
	profiles := []string{
		filepath.Join(home, ".zprofile"),
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".bash_profile"),
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".profile"),
		filepath.Join(home, ".config", "fish", "config.fish"),
	}
	if zdotdir != "" && zdotdir != home {
		profiles = append(profiles, filepath.Join(zdotdir, ".zshrc"))
	}
	return profiles
}

// UninstallArgs returns the command line with which m removes PackageName,
// such as npm uninstall -g @unaquatic/flow, or nil for MethodNone and
// MethodStandalone.
func (m Method) UninstallArgs() []string {
	switch m {
	case MethodNPM:
		return []string{"npm", "uninstall", "-g", PackageName}
	case MethodPNPM:
		return []string{"pnpm", "remove", "-g", PackageName}
	}
	return nil
}

// ErrNotStandalone reports a flow that the install script did not install,
// which flow uninstall leaves to the way it was installed.
var ErrNotStandalone = errors.New("not installed by the install script")

// Uninstall removes the flow that the install script installed, as
// IsStandalone tells, from home: the executable, the marker beside it and
// what an update left there, the link to it in ~/.local/bin, and the lines
// that add flow to PATH in the shell profiles of home, as Profiles lists
// them with zdotdir. It leaves the data directory. It returns what it
// removed, in that order, and fails with ErrNotStandalone, removing nothing,
// for any other flow, and with ErrBusy while another flow installs an
// update.
func (u *Updater) Uninstall(home, zdotdir string) ([]string, error) {
	if !IsStandalone(u.Executable) {
		return nil, fmt.Errorf("%w: %s", ErrNotStandalone, u.Executable)
	}
	lock, err := u.LockInstall()
	if err != nil {
		return nil, err
	}
	defer lock.Close()

	// The link is to this flow when it resolves to it, which it can only
	// while this flow is there.
	link := LinkPath(home)
	ours := false
	if info, err := os.Lstat(link); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(link)
		exe, exeErr := filepath.EvalSymlinks(u.Executable)
		ours = err == nil && exeErr == nil && target == exe
	}

	var removed []string
	dir := filepath.Dir(u.Executable)
	removeLeftovers(dir)
	os.Remove(u.Executable + ".old")
	if err := os.Remove(u.Executable); err != nil {
		return nil, err
	}
	removed = append(removed, u.Executable)
	os.Remove(filepath.Join(dir, MarkerName))
	// The folder goes once it is empty, as the install script made it.
	os.Remove(dir)

	if ours {
		if err := os.Remove(link); err != nil {
			return removed, err
		}
		removed = append(removed, link)
	}

	var errs []error
	for _, profile := range Profiles(home, zdotdir) {
		ok, err := RemovePathBlock(profile)
		if err != nil {
			errs = append(errs, err)
		}
		if ok {
			removed = append(removed, profile)
		}
	}
	return removed, errors.Join(errs...)
}

// RemovePathBlock removes from the shell profile at path the lines that the
// install script wrote there to add flow to PATH, between and with the
// lines that mark them, and the blank line that it wrote before them, and
// reports whether it found them. It leaves a profile without them, a
// missing one, and one whose block has no end, as they are.
func RemovePathBlock(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var out []string
	in, found := false, false
	for _, line := range strings.SplitAfter(string(data), "\n") {
		switch text := strings.TrimRight(line, "\r\n"); {
		case !in && text == pathBlockBegin:
			in, found = true, true
			if n := len(out); n > 0 && strings.TrimSpace(out[n-1]) == "" {
				out = out[:n-1]
			}
		case in:
			in = text != pathBlockEnd
		default:
			out = append(out, line)
		}
	}
	if !found || in {
		return false, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	// A profile that is a link stays one, as WriteFile follows it.
	return true, os.WriteFile(path, []byte(strings.Join(out, "")), info.Mode().Perm())
}
