// Package update finds the latest release of flow in the npm registry and
// installs it with the package manager that installed flow: on request, as
// the update command does, and in the background, as the terminal UI does
// once it starts.
package update

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/product"
	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

const (
	// PackageName is the npm package that flow is released as.
	PackageName = "@unaquatic/flow"

	// DefaultRegistry is the npm registry that an Updater reads releases
	// from when its Registry is empty.
	DefaultRegistry = "https://registry.npmjs.org"

	// RequestTimeout bounds a request to the registry.
	RequestTimeout = 10 * time.Second
)

// Updater checks for releases of the running flow, and installs them, for
// one data directory.
type Updater struct {
	// Dir is the data directory, which holds the state file StateName, the
	// lock file InstallLockName, and the update log, LogPath.
	Dir string
	// Current is the version of the running flow, as product.Version
	// returns it.
	Current string
	// Executable is the path of the running flow, its symbolic links
	// resolved.
	Executable string
	// Method is the package manager that installed flow, as the launcher
	// of the npm package tells it, which Plan takes over the one the
	// folders of Executable tell. MethodNone leaves it to them.
	Method Method
	// Registry is the base URL of the npm registry. Empty selects
	// DefaultRegistry.
	Registry string
	// GitHubAPI is the base URL of the GitHub API, which a flow that the
	// install script installed updates from. Empty selects DefaultGitHubAPI.
	GitHubAPI string
	// Mirror is the base URL of the mirror of the releases, which such a
	// flow updates from when GitHub does not answer. Empty selects
	// DefaultMirror.
	Mirror string
	// Client sends the requests to the registry. Nil selects
	// http.DefaultClient.
	Client *http.Client
	// Output runs a command of a package manager, such as npm root -g, and
	// returns its standard output. Nil runs it with os/exec.
	Output func(ctx context.Context, name string, args ...string) ([]byte, error)
	// Writable reports whether the user running flow can write to the
	// global folder of a package manager. Nil probes it, as the package's
	// Writable does.
	Writable func(dir string) bool
	// Progress, when set, is told how far Replace has downloaded the
	// release asset at url: done bytes of total, or of an unknown size when
	// total is -1. It is called as the bytes arrive, from the goroutine that
	// calls Replace.
	Progress func(url string, done, total int64)
	// Installing, when set, is called by Replace once it has downloaded the
	// release asset and checked its digest, before it writes the new binary.
	Installing func()
}

// New returns an Updater of the running flow, at product.Version, for data
// directory dir, installed by the package manager that
// config.EnvManagedBy names, when it names npm or pnpm.
func New(dir string) (*Updater, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate the running flow: %w", err)
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	m := Method(config.ManagedBy())
	if m != MethodNPM && m != MethodPNPM {
		m = MethodNone
	}
	return &Updater{Dir: dir, Current: product.Version(), Executable: exe, Method: m}, nil
}

// Canonical returns v as a semantic version with its leading "v", which Go
// writes and the npm registry leaves out, such as v0.2.0 of 0.2.0, or ""
// when v is no semantic version.
func Canonical(v string) string {
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return ""
	}
	return v
}

// IsRelease reports whether v, a version as product.Version returns it, is
// that of a release build, which updates apply to: a semantic version that
// is no pseudo-version, as a build of an untagged commit records, and has
// no build metadata, as the +dirty of a build with uncommitted changes.
// The "(devel)" of go run and of builds outside a repository is none.
func IsRelease(v string) bool {
	return semver.IsValid(v) && semver.Build(v) == "" && !module.IsPseudoVersion(v)
}

// Newer reports whether latest is a later version than current, either
// with or without its leading "v". It is false when either is no semantic
// version.
func Newer(latest, current string) bool {
	l, c := Canonical(latest), Canonical(current)
	return l != "" && c != "" && semver.Compare(l, c) > 0
}

// Latest returns the latest release, in Canonical form, such as v0.2.0, as
// the way flow was installed tells of it: the latest GitHub release for a
// flow that the install script installed, as IsStandalone tells, and the
// version of PackageName that the npm registry's "latest" dist-tag names
// otherwise.
func (u *Updater) Latest(ctx context.Context) (string, error) {
	if IsStandalone(u.Executable) {
		return u.latestRelease(ctx)
	}
	endpoint := strings.TrimSuffix(cmp.Or(u.Registry, DefaultRegistry), "/") +
		"/" + url.PathEscape(PackageName) + "/latest"
	var body struct {
		Version string `json:"version"`
	}
	if err := u.getJSON(ctx, endpoint, &body); err != nil {
		if se, ok := errors.AsType[*StatusError](err); ok && se.Code == http.StatusNotFound {
			return "", fmt.Errorf("check for updates: not published: %s", PackageName)
		}
		return "", fmt.Errorf("check for updates: %w", err)
	}
	v := Canonical(body.Version)
	if v == "" {
		return "", fmt.Errorf("check for updates: invalid version: %s", body.Version)
	}
	return v, nil
}

// getJSON decodes the JSON that a GET of endpoint answers with, within
// RequestTimeout, into v.
func (u *Updater) getJSON(ctx context.Context, endpoint string, v any) error {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	resp, err := u.get(ctx, endpoint)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v); err != nil {
		return fmt.Errorf("%s: %w", endpoint, err)
	}
	return nil
}
