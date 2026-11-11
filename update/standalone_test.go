package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// tarGz returns a gzipped tar that holds content as the file called name.
func tarGz(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// releases serves release v1.2.3 of flow as GitHub would: the release, its
// latest, with the asset of the platform, which holds the binary "github",
// and its digest. With tamper set, the asset holds other bytes than its
// digest names.
func releases(t *testing.T, tamper bool) *httptest.Server {
	t.Helper()
	asset := tarGz(t, ExecutableName(), "github")
	sum := sha256.Sum256(asset)
	if tamper {
		asset = tarGz(t, ExecutableName(), "tampered")
	}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/repos/" + Repository + "/releases/latest", "/repos/" + Repository + "/releases/tags/v1.2.3":
			json.NewEncoder(w).Encode(map[string]any{"tag_name": "v1.2.3", "assets": []map[string]string{{
				"name":                 ReleaseAsset(),
				"digest":               "sha256:" + hex.EncodeToString(sum[:]),
				"browser_download_url": srv.URL + "/download/" + ReleaseAsset(),
			}}})
		case "/download/" + ReleaseAsset():
			w.Write(asset)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// standalone returns an Updater of a flow at v1.2.2 that the install script
// installed, in a new folder with MarkerName beside it, which updates from
// srv.
func standalone(t *testing.T, srv *httptest.Server) *Updater {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, ExecutableName())
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, MarkerName), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return &Updater{Dir: t.TempDir(), Current: "v1.2.2", Executable: exe, GitHubAPI: srv.URL}
}

// binary returns what the flow of u holds, and fails the test when its
// folder holds anything else than it and MarkerName, as a download left.
func binary(t *testing.T, u *Updater) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(u.Executable))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if want := []string{MarkerName, ExecutableName()}; !slices.Equal(names, want) {
		t.Errorf("folder holds %q, want %q", names, want)
	}
	data, err := os.ReadFile(u.Executable)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(u.Executable); err == nil && runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Errorf("binary mode %v, want 0755", info.Mode().Perm())
	}
	return string(data)
}

// TestReplace checks that Replace swaps the flow binary for the release's
// from GitHub.
func TestReplace(t *testing.T) {
	u := standalone(t, releases(t, false))
	if err := u.Replace(t.Context(), "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	if got := binary(t, u); got != "github" {
		t.Errorf("binary = %q, want the GitHub release's", got)
	}
}

// TestReplaceProgress checks that Replace tells Progress how far it has
// downloaded the release asset, from nothing to all of it, never going
// back, and calls Installing once, after the download and before the
// binary is replaced.
func TestReplaceProgress(t *testing.T) {
	u := standalone(t, releases(t, false))
	var urls []string
	var done []int64
	var total int64
	installing := 0
	u.Progress = func(url string, d, n int64) {
		if installing > 0 {
			t.Error("progress told after the install began")
		}
		urls, done, total = append(urls, url), append(done, d), n
	}
	u.Installing = func() {
		if binary(t, u) != "old" {
			t.Error("install began after the binary was replaced")
		}
		installing++
	}
	if err := u.Replace(t.Context(), "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	if installing != 1 {
		t.Errorf("Installing called %d times, want once, between the download and the install", installing)
	}
	if len(done) < 2 || done[0] != 0 || done[len(done)-1] != total || total <= 0 || !slices.IsSorted(done) {
		t.Errorf("progress = %v of %d, want from 0 up to the whole size", done, total)
	}
	other := func(url string) bool { return url != urls[0] }
	if !strings.HasSuffix(urls[0], "/download/"+ReleaseAsset()) || slices.ContainsFunc(urls, other) {
		t.Errorf("progress of %q, want the release asset's URL alone", urls)
	}
}

// TestReplaceLeftovers checks that Replace removes the new binary that a
// replace which flow exited during left beside the flow binary, and leaves
// no download there.
func TestReplaceLeftovers(t *testing.T) {
	u := standalone(t, releases(t, false))
	leftover := filepath.Join(filepath.Dir(u.Executable), ".flow-new-123")
	if err := os.WriteFile(leftover, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := u.Replace(t.Context(), "v1.2.3"); err != nil {
		t.Fatal(err)
	}
	// binary fails the test when the folder holds anything else.
	if got := binary(t, u); got != "github" {
		t.Errorf("binary = %q, want the GitHub release's", got)
	}
}

// TestReplaceTampered checks that a release asset that does not match its
// digest leaves the flow binary as it was.
func TestReplaceTampered(t *testing.T) {
	u := standalone(t, releases(t, true))
	if err := u.Replace(t.Context(), "v1.2.3"); err == nil {
		t.Error("Replace with a tampered asset succeeded")
	}
	if got := binary(t, u); got != "old" {
		t.Errorf("binary = %q, want it left as it was", got)
	}
}

// TestLatestStandalone checks that a flow that the install script installed
// takes the latest release from GitHub, not from npm.
func TestLatestStandalone(t *testing.T) {
	u := standalone(t, releases(t, false))
	u.Registry = "http://127.0.0.1:1"
	if got, err := u.Latest(t.Context()); err != nil || got != "v1.2.3" {
		t.Errorf("Latest = %q, %v; want v1.2.3 from GitHub", got, err)
	}
}

// TestPlanStandalone checks that a flow beside MarkerName updates from its
// GitHub release, as the install script would, unless its folder is not
// writable.
func TestPlanStandalone(t *testing.T) {
	u := standalone(t, releases(t, false))
	inst, err := u.Plan(t.Context(), "1.2.3")
	if err != nil || inst.Method != MethodStandalone || inst.Version != "v1.2.3" || inst.String() != InstallCommand {
		t.Errorf("Plan = %+v %q, %v; want the standalone install of v1.2.3", inst, inst, err)
	}
	u.Writable = func(string) bool { return false }
	if _, err := u.Plan(t.Context(), "1.2.3"); err == nil {
		t.Error("Plan into a folder that is not writable succeeded")
	}
}

// TestAutoStandalone checks that a flow that the install script installed
// updates itself in the background, and that its next start tells of it.
func TestAutoStandalone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Auto does not install on Windows")
	}
	u := standalone(t, releases(t, false))
	if got, ok := u.Auto(t.Context()); !ok || got.Status != StatusInstalled || got.Version != "v1.2.3" {
		t.Fatalf("Auto = %+v, %v; want v1.2.3 installed", got, ok)
	}
	if got := binary(t, u); got != "github" {
		t.Errorf("binary = %q, want the GitHub release's", got)
	}
	if got := u.Updated(); got != "" {
		t.Errorf("Updated at v1.2.2 = %q, want none", got)
	}
	u.Current = "v1.2.3"
	if got := u.Updated(); got != "v1.2.3" {
		t.Errorf("Updated at v1.2.3 = %q, want v1.2.3", got)
	}
}

// down answers every request with 503 Service Unavailable, as GitHub does
// when it does not answer.
func down(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestGitHubDown checks that a flow that the install script installed
// neither finds nor installs a release while GitHub does not answer, and
// leaves its binary as it was.
func TestGitHubDown(t *testing.T) {
	u := standalone(t, down(t))
	if got, err := u.Latest(t.Context()); err == nil {
		t.Errorf("Latest with GitHub down = %q, want an error", got)
	}
	if err := u.Replace(t.Context(), "v1.2.3"); err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("Replace with GitHub down = %v, want the 503 it answered", err)
	}
	if got := binary(t, u); got != "old" {
		t.Errorf("binary = %q, want it left as it was", got)
	}
}
