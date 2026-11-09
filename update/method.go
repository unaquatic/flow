package update

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Method is the package manager that installed flow, which installs its
// updates.
type Method string

const (
	// MethodNone is a flow that no package manager of the others
	// installed, such as one built from source, which updates the way it
	// was installed.
	MethodNone Method = ""
	// MethodNPM is a flow that npm installed.
	MethodNPM Method = "npm"
	// MethodPNPM is a flow that pnpm installed.
	MethodPNPM Method = "pnpm"
)

// DetectMethod returns the package manager that installed the executable at
// path, as the folders it is in tell: an executable in a node_modules folder
// is pnpm's in pnpm's store or folders, and npm's otherwise, as that of
// another package manager reads too, which Plan then finds outside npm's
// global folder. Any other is MethodNone's.
func DetectMethod(path string) Method {
	p := "/" + strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
	if !strings.Contains(p, "/node_modules/") {
		return MethodNone
	}
	if strings.Contains(p, "/.pnpm/") || strings.Contains(p, "/pnpm/") {
		return MethodPNPM
	}
	return MethodNPM
}

// InstallArgs returns the command line with which m installs version of
// PackageName globally, such as npm install -g @unaquatic/flow@0.2.0, or
// nil for MethodNone. version may have the leading "v" that npm versions
// leave out.
func (m Method) InstallArgs(version string) []string {
	spec := PackageName + "@" + strings.TrimPrefix(version, "v")
	switch m {
	case MethodNPM:
		return []string{"npm", "install", "-g", spec}
	case MethodPNPM:
		return []string{"pnpm", "add", "-g", spec}
	}
	return nil
}

// Install is how the running flow updates to a release: the command line
// of its package manager that installs it.
type Install struct {
	// Method is the package manager that installed flow.
	Method Method
	// Version is the release it installs, in Canonical form.
	Version string
	// Args is the command line, as Method.InstallArgs returns it.
	Args []string
}

// String returns the command line of i, as a shell takes it.
func (i Install) String() string { return strings.Join(i.Args, " ") }

// Command returns the command that runs i, which ctx kills when it is done
// first.
func (i Install) Command(ctx context.Context) *exec.Cmd {
	return exec.CommandContext(ctx, i.Args[0], i.Args[1:]...)
}

// ErrUnmanaged reports a flow that no package manager Method names
// installed globally, which updates the way it was installed.
var ErrUnmanaged = errors.New("not a global npm or pnpm install")

// NotWritableError reports a global install that the user running flow
// cannot write to, such as one under a prefix that root owns, which the
// user updates with Install on their own, with the rights it takes.
type NotWritableError struct {
	// Dir is the global folder of the package manager.
	Dir string
	// Install is the install that would update flow.
	Install Install
}

func (e *NotWritableError) Error() string {
	return fmt.Sprintf("%s is not writable, run %s", e.Dir, e.Install)
}

// Plan returns the Install that updates the running flow to version. It
// fails with ErrUnmanaged when no package manager installed flow globally:
// when the executable is in no package manager's folders, or not in the
// global folder that the package manager whose folders it is in reports,
// as a project's node_modules is not. It fails with a NotWritableError when
// the global folder is not writable.
func (u *Updater) Plan(ctx context.Context, version string) (Install, error) {
	m := DetectMethod(u.Executable)
	if m == MethodNone {
		return Install{}, fmt.Errorf("%s is %w", u.Executable, ErrUnmanaged)
	}
	inst := Install{Method: m, Version: Canonical(version), Args: m.InstallArgs(version)}
	root, err := u.GlobalRoot(ctx, m)
	if err != nil {
		return Install{}, fmt.Errorf("%s is %w: %w", u.Executable, ErrUnmanaged, err)
	}
	if rel, err := filepath.Rel(root, u.Executable); err != nil || !filepath.IsLocal(rel) {
		return Install{}, fmt.Errorf("%s is %w (outside %s)", u.Executable, ErrUnmanaged, root)
	}
	writable := u.Writable
	if writable == nil {
		writable = Writable
	}
	if !writable(root) {
		return Install{}, &NotWritableError{Dir: root, Install: inst}
	}
	return inst, nil
}

// GlobalRoot returns the folder that m installs global packages in, its
// symbolic links resolved, as m root -g reports it, such as
// /usr/local/lib/node_modules of npm. It fails when m is not on the path.
func (u *Updater) GlobalRoot(ctx context.Context, m Method) (string, error) {
	if m != MethodNPM && m != MethodPNPM {
		return "", ErrUnmanaged
	}
	dir, err := u.output(ctx, string(m), "root", "-g")
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return dir, nil
}

// output runs name with args, as Updater.Output does, and returns its
// standard output without the white space around it.
func (u *Updater) output(ctx context.Context, name string, args ...string) (string, error) {
	run := u.Output
	if run == nil {
		run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).Output()
		}
	}
	out, err := run(ctx, name, args...)
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(bytes.TrimSpace(out)), nil
}

// Writable reports whether the user running flow can create files in dir.
func Writable(dir string) bool {
	f, err := os.CreateTemp(dir, ".flow-update-*")
	if err != nil {
		return false
	}
	name := f.Name()
	return errors.Join(f.Close(), os.Remove(name)) == nil
}
