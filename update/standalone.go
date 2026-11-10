package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/unaquatic/flow/product"
)

// A flow that the install script installed from its GitHub release, apart
// from npm and pnpm, which checks for and installs its updates there too.
const (
	// Repository is the GitHub repository that flow is released from.
	Repository = "unaquatic/flow"

	// DefaultGitHubAPI is the GitHub API that an Updater reads releases from
	// when its GitHubAPI is empty.
	DefaultGitHubAPI = "https://api.github.com"

	// InstallCommand runs the install script, which installs the latest
	// release, and updates a flow that it installed.
	InstallCommand = "curl -fsSL https://aquatic.dev/flow/install | sh"

	// MarkerName is the file that the install script writes beside the flow
	// it installs, which makes that flow MethodStandalone's.
	MarkerName = ".flow-standalone"

	// DownloadTimeout bounds the download of a release.
	DownloadTimeout = 5 * time.Minute

	// maxBinarySize bounds what a download of a release, or a binary in it,
	// may hold.
	maxBinarySize = 1 << 30
)

// IsStandalone reports whether the install script installed the executable
// at path: whether MarkerName is beside it.
func IsStandalone(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(filepath.Dir(path), MarkerName))
	return err == nil
}

// Platform returns the platform of the running flow as its release names
// it: the system as Go names it and the CPU as npm does, such as
// darwin-arm64, linux-x64 or windows-x64.
func Platform() string {
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	return runtime.GOOS + "-" + arch
}

// ReleaseAsset returns the name of the release asset that holds the flow
// binary of the platform: flow-OS-ARCH.tar.gz, as Platform names it, and
// flow-OS-ARCH.zip on Windows.
func ReleaseAsset() string {
	if runtime.GOOS == "windows" {
		return "flow-" + Platform() + ".zip"
	}
	return "flow-" + Platform() + ".tar.gz"
}

// ExecutableName returns the name of the flow binary in a release asset:
// flow, and flow.exe on Windows.
func ExecutableName() string {
	if runtime.GOOS == "windows" {
		return "flow.exe"
	}
	return "flow"
}

// githubRelease is what the GitHub API tells of a release.
type githubRelease struct {
	Tag    string `json:"tag_name"`
	Assets []struct {
		Name   string `json:"name"`
		Digest string `json:"digest"`
		URL    string `json:"browser_download_url"`
	} `json:"assets"`
}

// release returns GitHub's release of tag, or the latest release when tag is
// "latest".
func (u *Updater) release(ctx context.Context, tag string) (githubRelease, error) {
	endpoint := strings.TrimSuffix(cmp.Or(u.GitHubAPI, DefaultGitHubAPI), "/") + "/repos/" + Repository + "/releases/"
	if tag == "latest" {
		endpoint += "latest"
	} else {
		endpoint += "tags/" + url.PathEscape(tag)
	}
	var r githubRelease
	err := u.getJSON(ctx, endpoint, &r)
	if se, ok := errors.AsType[*StatusError](err); ok && se.Code == http.StatusNotFound {
		return r, fmt.Errorf("release not found: %s", tag)
	}
	if err != nil {
		return r, fmt.Errorf("download failed: %w", err)
	}
	return r, nil
}

// latestRelease returns the tag of GitHub's latest release, in Canonical
// form, such as v0.2.0.
func (u *Updater) latestRelease(ctx context.Context) (string, error) {
	r, err := u.release(ctx, "latest")
	if err != nil {
		return "", fmt.Errorf("check failed: %w", err)
	}
	v := Canonical(r.Tag)
	if v == "" {
		return "", fmt.Errorf("check failed: invalid version: %q", r.Tag)
	}
	return v, nil
}

// Replace replaces the executable with the flow binary of release version
// of the platform: its release asset on GitHub, checked against the SHA-256
// digest that GitHub reports for it. It downloads the asset to the system's
// temporary folder, writes the binary beside the executable, named as
// newBinaryPattern, and renames it over it, so that the executable is the
// old binary or the new one, never part of either. It first removes the new
// binaries that a replace which flow exited during left, which the install
// lock, held around every replace, keeps from being in use.
func (u *Updater) Replace(ctx context.Context, version string) error {
	ctx, cancel := context.WithTimeout(ctx, DownloadTimeout)
	defer cancel()
	removeLeftovers(filepath.Dir(u.Executable))
	asset := ReleaseAsset()
	release, err := u.release(ctx, Canonical(version))
	if err != nil {
		return err
	}
	for _, a := range release.Assets {
		if a.Name != asset {
			continue
		}
		hexSum, ok := strings.CutPrefix(a.Digest, "sha256:")
		sum, err := hex.DecodeString(hexSum)
		if !ok || err != nil || len(sum) != sha256.Size {
			return fmt.Errorf("release %s has no digest: %s", Canonical(version), asset)
		}
		f, err := u.download(ctx, a.URL, sha256.New(), sum)
		if err != nil {
			return err
		}
		defer removeTemp(f)
		if u.Installing != nil {
			u.Installing()
		}
		return installFrom(f, strings.HasSuffix(asset, ".zip"), ExecutableName(), u.Executable)
	}
	return fmt.Errorf("release %s has no build: %s", Canonical(version), asset)
}

// download writes what a GET of rawURL answers with to a new file in the
// system's temporary folder, and returns it, rewound, once it has checked
// that h hashes it to want, telling u.Progress how far it is. The caller
// removes it with removeTemp.
func (u *Updater) download(ctx context.Context, rawURL string, h hash.Hash, want []byte) (*os.File, error) {
	resp, err := u.get(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	f, err := os.CreateTemp("", "flow-download-*")
	if err != nil {
		return nil, err
	}
	var body io.Reader = resp.Body
	if u.Progress != nil {
		u.Progress(rawURL, 0, resp.ContentLength)
		body = &progressReader{r: resp.Body, url: rawURL, total: resp.ContentLength, report: u.Progress}
	}
	if _, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(body, maxBinarySize)); err != nil {
		removeTemp(f)
		return nil, fmt.Errorf("download failed: %s: %w", rawURL, err)
	}
	if got := h.Sum(nil); !bytes.Equal(got, want) {
		removeTemp(f)
		return nil, fmt.Errorf("digest mismatch: %s", path.Base(rawURL))
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		removeTemp(f)
		return nil, err
	}
	return f, nil
}

// progressReader reads r, the body of a download of url of total bytes, or
// -1 when its size is unknown, and reports how much it has read to report
// after each read that reads any.
type progressReader struct {
	r      io.Reader
	url    string
	done   int64
	total  int64
	report func(url string, done, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 {
		p.done += int64(n)
		p.report(p.url, p.done, p.total)
	}
	return n, err
}

// newBinaryPattern names the new binary that Replace writes beside the
// executable before it renames it over it.
const newBinaryPattern = ".flow-new-*"

// removeLeftovers removes the new binaries, named as newBinaryPattern, that
// a replace left in dir when flow exited during it.
func removeLeftovers(dir string) {
	leftovers, _ := filepath.Glob(filepath.Join(dir, newBinaryPattern))
	for _, name := range leftovers {
		os.Remove(name)
	}
}

// removeTemp closes and removes f, a temporary file.
func removeTemp(f *os.File) {
	f.Close()
	os.Remove(f.Name())
}

// installFrom writes the file called name in the archive f, a gzipped tar,
// or a zip when zipped is set, to a new file beside dst, executable, and
// renames it over dst. On Windows, which cannot replace a running
// executable, it first renames dst aside, to dst.old.
func installFrom(f *os.File, zipped bool, name, dst string) error {
	r, err := archiveFile(f, zipped, name)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), newBinaryPattern)
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmp, io.LimitReader(r, maxBinarySize)); err != nil {
		removeTemp(tmp)
		return err
	}
	if err := errors.Join(tmp.Chmod(0o755), tmp.Close()); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if runtime.GOOS == "windows" {
		os.Remove(dst + ".old")
		if err := os.Rename(dst, dst+".old"); err != nil && !errors.Is(err, os.ErrNotExist) {
			os.Remove(tmp.Name())
			return err
		}
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// archiveFile returns the content of the file called name in the archive f,
// a gzipped tar, or a zip when zipped is set.
func archiveFile(f *os.File, zipped bool, name string) (io.Reader, error) {
	if zipped {
		info, err := f.Stat()
		if err != nil {
			return nil, err
		}
		zr, err := zip.NewReader(f, info.Size())
		if err != nil {
			return nil, err
		}
		return zr.Open(name)
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("archive has no file: %s", name)
		}
		if err != nil {
			return nil, err
		}
		if hdr.Name == name && hdr.Typeflag == tar.TypeReg {
			return tr, nil
		}
	}
}

// StatusError reports a response of a status other than 200 OK.
type StatusError struct {
	// URL is the URL requested.
	URL string
	// Code is the status code of the response, such as 404.
	Code int
	// Status is the status of the response, such as "404 Not Found".
	Status string
}

func (e *StatusError) Error() string { return e.URL + ": " + e.Status }

// get sends a GET of rawURL, as flow at the version it runs, and returns the
// response, which the caller closes, or a StatusError when its status is
// other than 200 OK.
func (u *Updater) get(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", product.Name+"/"+u.Current)
	client := u.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, &StatusError{URL: rawURL, Code: resp.StatusCode, Status: resp.Status}
	}
	return resp, nil
}
