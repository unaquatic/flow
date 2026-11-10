// Package update finds the latest release of flow in the npm registry and
// installs it with the package manager that installed flow: on request, as
// the update command does, and in the background, as the terminal UI does
// once it starts.
package update

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

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

	// CheckInterval is how long Updater.Check trusts the latest release it
	// found last, before it asks the registry again, when the Updater's
	// Interval is zero.
	CheckInterval = 24 * time.Hour

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
	// Registry is the base URL of the npm registry. Empty selects
	// DefaultRegistry.
	Registry string
	// Client sends the requests to the registry. Nil selects
	// http.DefaultClient.
	Client *http.Client
	// Interval is how long Check trusts the latest release it found last.
	// Zero selects CheckInterval.
	Interval time.Duration
	// Output runs a command of a package manager, such as npm root -g, and
	// returns its standard output. Nil runs it with os/exec.
	Output func(ctx context.Context, name string, args ...string) ([]byte, error)
	// Writable reports whether the user running flow can write to the
	// global folder of a package manager. Nil probes it, as the package's
	// Writable does.
	Writable func(dir string) bool
}

// New returns an Updater of the running flow, at product.Version, for data
// directory dir.
func New(dir string) (*Updater, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locating the running flow: %w", err)
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return &Updater{Dir: dir, Current: product.Version(), Executable: exe}, nil
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

// Latest returns the version of PackageName that the registry's "latest"
// dist-tag names, in Canonical form, such as v0.2.0.
func (u *Updater) Latest(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, RequestTimeout)
	defer cancel()
	endpoint := strings.TrimSuffix(cmp.Or(u.Registry, DefaultRegistry), "/") +
		"/" + url.PathEscape(PackageName) + "/latest"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", product.Name+"/"+u.Current)
	client := u.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("checking for updates: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("checking for updates: %s has no release of %s", cmp.Or(u.Registry, DefaultRegistry), PackageName)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checking for updates: %s: %s", endpoint, resp.Status)
	}
	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("checking for updates: %s: %w", endpoint, err)
	}
	v := Canonical(body.Version)
	if v == "" {
		return "", fmt.Errorf("checking for updates: %s: no valid version, got %q", endpoint, body.Version)
	}
	return v, nil
}
