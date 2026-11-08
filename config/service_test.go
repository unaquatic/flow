package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// TestServiceGetReadsTheFileEachTime checks that Get reads the config file
// anew on each call, so a change to it applies to the next one.
func TestServiceGetReadsTheFileEachTime(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir)
	if cfg, err := s.Get(); err != nil || cfg.DefaultModel != "" {
		t.Fatalf("Get without a file = %q, %v, want no model", cfg.DefaultModel, err)
	}

	write(t, dir, `{"default_model":"lab-a/model-a","default_provider":"provider-a"}`)
	if cfg, err := s.Get(); err != nil || cfg.DefaultProvider != "provider-a" || cfg.DefaultModel != "lab-a/model-a" {
		t.Errorf("Get after a write = %+v, %v, want lab-a/model-a at provider-a", cfg, err)
	}
	write(t, dir, `{"default_model":"lab-b/model-b","default_provider":"provider-b"}`)
	if cfg, err := s.Get(); err != nil || cfg.DefaultProvider != "provider-b" || cfg.DefaultModel != "lab-b/model-b" {
		t.Errorf("Get after a rewrite = %+v, %v, want lab-b/model-b at provider-b", cfg, err)
	}
}

// TestServiceGetReadsTheOlderModelForm checks that Get reads the older
// "model" key, in "provider/model" form, when the config file sets no
// default model, as the model at the provider its first segment
// names, with the provider as the lab of a model that names none, and
// leaves a model without a provider as it is.
func TestServiceGetReadsTheOlderModelForm(t *testing.T) {
	tests := []struct {
		file, provider, model string
	}{
		{`{"model":"provider-a/model-a:fast:high"}`, "provider-a", "provider-a/model-a:fast:high"},
		{`{"model":"provider-b/lab-a/model-b"}`, "provider-b", "lab-a/model-b"},
		{`{"model":"model-x"}`, "", "model-x"},
		{`{"default_model":"lab-a/model-a","default_provider":"provider-b"}`, "provider-b", "lab-a/model-a"},
		{`{"default_model":"lab-a/model-a","default_provider":"provider-b","model":"provider-c/model-c"}`, "provider-b", "lab-a/model-a"},
		{`{"default_provider":"provider-b"}`, "provider-b", ""},
	}
	for _, tt := range tests {
		dir := t.TempDir()
		write(t, dir, tt.file)
		cfg, err := NewService(dir).Get()
		if err != nil || cfg.DefaultProvider != tt.provider || cfg.DefaultModel != tt.model {
			t.Errorf("Get of %s = provider %q, model %q, %v; want %q, %q", tt.file, cfg.DefaultProvider, cfg.DefaultModel, err, tt.provider, tt.model)
		}
	}
}

// TestServiceGetReadsEverySetting checks that Get reads each setting the
// config file can set, the catalog source among them.
func TestServiceGetReadsEverySetting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEBUG", "")
	write(t, dir, `{"default_model":"lab-a/model-a","default_provider":"provider-a","log_level":"warn","extensions":{"snapshot":{"enabled":false}},"catalog_source":"http://catalog.test",
		"provider":{"provider-b":{"base_url":"http://provider-b.test"}},"agent":{"retry_max_attempts":2,"retry_delay":"1s"},
		"debug":true,"terminal":{"fullscreen":true,"key_hints":"footer","tool_marker":true}}`)
	cfg, err := NewService(dir).Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if cfg.DefaultModel != "lab-a/model-a" || cfg.DefaultProvider != "provider-a" || cfg.LogLevel != LogWarn || string(cfg.Extensions["snapshot"]) != `{"enabled":false}` || cfg.CatalogSource != "http://catalog.test" ||
		cfg.Provider["provider-b"].BaseURL != "http://provider-b.test" || cfg.Agent.RetryMaxAttempts != 2 || cfg.Agent.RetryDelay.Seconds() != 1 ||
		!cfg.Debug || !cfg.Terminal.Fullscreen || cfg.Terminal.KeyHints != "footer" || !cfg.Terminal.ToolMarker {
		t.Errorf("config = %+v, want every setting of the file", cfg)
	}
}

// TestServiceGetUnreadableFile checks that Get fails on a config file it
// cannot parse, rather than return settings the file does not set.
func TestServiceGetUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, `not json`)
	if cfg, err := NewService(dir).Get(); err == nil || cfg != nil {
		t.Errorf("Get = %+v, %v, want no configuration and an error", cfg, err)
	}
}

// TestServiceSaveDefaultModel checks that SaveDefaultModel creates the
// config file, replaces only its default provider and model, removes the
// older "model" key, keeps its other settings, and leaves no temporary
// file.
func TestServiceSaveDefaultModel(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir)
	if err := s.SaveDefaultModel("provider-a", "lab-a/model-a"); err != nil {
		t.Fatalf("SaveDefaultModel without a file: %v", err)
	}
	if cfg, err := s.Get(); err != nil || cfg.DefaultProvider != "provider-a" || cfg.DefaultModel != "lab-a/model-a" {
		t.Errorf("config = %+v, %v, want lab-a/model-a at provider-a", cfg, err)
	}

	write(t, dir, `{"log_level":"debug","model":"provider-a/model-a"}`)
	if err := s.SaveDefaultModel("provider-b", "lab-b/model-b:high"); err != nil {
		t.Fatalf("SaveDefaultModel: %v", err)
	}
	cfg, err := s.Get()
	if err != nil || cfg.DefaultProvider != "provider-b" || cfg.DefaultModel != "lab-b/model-b:high" || cfg.LogLevel != LogDebug {
		t.Errorf("config = provider %q, model %q, log level %q, %v, want the saved model and the kept log level",
			cfg.DefaultProvider, cfg.DefaultModel, cfg.LogLevel, err)
	}
	if keys, err := s.File().Read(); err != nil || keys["model"] != nil {
		t.Errorf("config file keys = %v, %v, want the older model key gone", keys, err)
	}
	if got, want := names(t, dir), []string{FileName, "config.lock"}; !slices.Equal(got, want) {
		t.Errorf("directory holds %q, want %q: no temporary file left", got, want)
	}
}

// TestServiceSaveTerminalAndDebug checks that SaveTerminal replaces the
// terminal settings of the config file and SaveDebug its debug setting,
// each keeping the other settings, the model among them.
func TestServiceSaveTerminalAndDebug(t *testing.T) {
	t.Setenv("DEBUG", "")
	dir := t.TempDir()
	write(t, dir, `{"default_model":"lab-a/model-a","default_provider":"provider-a","terminal":{"selection":"arrow"}}`)
	s := NewService(dir)
	if err := s.SaveTerminal(Terminal{Fullscreen: true, CacheUsage: "per_turn", ToolMarker: true}); err != nil {
		t.Fatalf("SaveTerminal: %v", err)
	}
	if err := s.SaveDebug(true); err != nil {
		t.Fatalf("SaveDebug: %v", err)
	}
	cfg, err := s.Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	term := cfg.Terminal
	if cfg.DefaultModel != "lab-a/model-a" || cfg.DefaultProvider != "provider-a" || !cfg.Debug || !term.Fullscreen || term.CacheUsage != "per_turn" ||
		!term.ToolMarker || term.Selection != "" {
		t.Errorf("config = model %q, debug %v, terminal %+v; want the model kept, debug on, and the saved terminal settings alone",
			cfg.DefaultModel, cfg.Debug, term)
	}
}

// write writes data as the config file in dir.
func write(t *testing.T, dir, data string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestDataDir checks the data directory that EnvDataDir chooses, with a
// leading ~ for the home directory and a relative path taken from the working
// directory, and the default one when it is unset, and that DataDir creates
// it.
func TestDataDir(t *testing.T) {
	home, wd := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Chdir(wd)
	wd, _ = os.Getwd() // the working directory as the system resolves it

	tests := []struct {
		env  string
		want string
	}{
		{env: "", want: filepath.Join(home, ".flow")},
		{env: "~", want: home},
		{env: "~/flow-data", want: filepath.Join(home, "flow-data")},
		{env: filepath.Join(home, "abs"), want: filepath.Join(home, "abs")},
		{env: "rel/data", want: filepath.Join(wd, "rel", "data")},
	}
	for _, tt := range tests {
		t.Setenv(EnvDataDir, tt.env)
		got, err := DataDir()
		if err != nil || got != tt.want {
			t.Errorf("DataDir with %s=%q = %q, %v, want %q", EnvDataDir, tt.env, got, err, tt.want)
			continue
		}
		if info, err := os.Stat(got); err != nil || !info.IsDir() {
			t.Errorf("DataDir with %s=%q did not create %s: %v", EnvDataDir, tt.env, got, err)
		}
	}
}

// TestServiceSaveDefaultModelKeepsPermissions checks that SaveDefaultModel keeps the
// permissions of the config file, which can hold API keys, and creates a
// new one private to its user.
func TestServiceSaveDefaultModelKeepsPermissions(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir)
	path := filepath.Join(dir, FileName)

	if err := s.SaveDefaultModel("provider-a", "lab-a/model-a"); err != nil {
		t.Fatal(err)
	}
	if mode := perm(t, path); mode != 0o600 {
		t.Errorf("new config file mode = %04o, want 0600", mode)
	}

	for _, mode := range []os.FileMode{0o600, 0o640} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveDefaultModel("provider-b", "lab-b/model-b"); err != nil {
			t.Fatal(err)
		}
		if got := perm(t, path); got != mode {
			t.Errorf("config file mode after SaveDefaultModel = %04o, want %04o kept", got, mode)
		}
	}
}

// TestServiceSaveDefaultModelThroughSymlink checks that SaveDefaultModel writes the file a
// symbolically linked config file names, keeping the link, as for a config
// kept in a dotfiles repository, and the target when it does not exist yet.
func TestServiceSaveDefaultModelThroughSymlink(t *testing.T) {
	for name, existing := range map[string]bool{"existing target": true, "missing target": false} {
		t.Run(name, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "dotfiles-config.json")
			if existing {
				if err := os.WriteFile(target, []byte(`{"log_level":"debug"}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			dir := t.TempDir()
			link := filepath.Join(dir, FileName)
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("cannot create a symbolic link: %v", err)
			}

			s := NewService(dir)
			if err := s.SaveDefaultModel("provider-a", "lab-a/model-a"); err != nil {
				t.Fatal(err)
			}
			if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("config file after SaveDefaultModel is not the link: %v, %v", info, err)
			}
			cfg, err := s.Get()
			if err != nil || cfg.DefaultProvider != "provider-a" || cfg.DefaultModel != "lab-a/model-a" {
				t.Errorf("model read through the link = %+v, %v, want lab-a/model-a at provider-a", cfg, err)
			}
			if existing && cfg.LogLevel != LogDebug {
				t.Errorf("log level = %q, want the target's kept", cfg.LogLevel)
			}
			if got, want := names(t, dir), []string{FileName, "config.lock"}; !slices.Equal(got, want) {
				t.Errorf("data directory holds %q, want %q: the link and its lock file alone", got, want)
			}
			if got, want := names(t, filepath.Dir(target)), []string{filepath.Base(target)}; !slices.Equal(got, want) {
				t.Errorf("target directory holds %q, want %q: no temporary file left", got, want)
			}
		})
	}
}

// perm returns the permissions of the file at path.
func perm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

// names returns the names of the entries of dir, sorted.
func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// TestServiceUpdateDefaultModel checks that UpdateDefaultModel gives change
// the default provider and model as Get reads them, the older "model" key
// among them, saves what it returns in their place, saves nothing when
// change fails, and loses no update that processes make at once.
func TestServiceUpdateDefaultModel(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir)
	write(t, dir, `{"log_level":"debug","model":"provider-a/model-a"}`)
	err := s.UpdateDefaultModel(func(provider, model string) (string, string, error) {
		if provider != "provider-a" || model != "provider-a/model-a" {
			t.Errorf("change got provider %q, model %q, want provider-a/model-a at provider-a", provider, model)
		}
		return "provider-b", model, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := s.Get()
	if err != nil || cfg.DefaultProvider != "provider-b" || cfg.DefaultModel != "provider-a/model-a" || cfg.LogLevel != LogDebug {
		t.Errorf("config = %+v, %v, want provider-a/model-a at provider-b and the kept log level", cfg, err)
	}

	before, _ := os.ReadFile(filepath.Join(dir, FileName))
	refused := errors.New("refused")
	if err := s.UpdateDefaultModel(func(string, string) (string, string, error) { return "", "", refused }); !errors.Is(err, refused) {
		t.Errorf("failed change = %v, want its error", err)
	}
	if after, _ := os.ReadFile(filepath.Join(dir, FileName)); !bytes.Equal(before, after) {
		t.Errorf("config file after a failed change = %s, want it unchanged", after)
	}

	// Each update adds a letter to the model; none may be lost.
	const n = 20
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			if err := s.UpdateDefaultModel(func(provider, model string) (string, string, error) {
				return provider, model + "x", nil
			}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if cfg, err := s.Get(); err != nil || cfg.DefaultModel != "provider-a/model-a"+strings.Repeat("x", n) {
		t.Errorf("model after %d updates at once = %q, %v, want every update", n, cfg.DefaultModel, err)
	}
}

// TestServiceVersion checks that every write saves the config file at
// Version, upgrading the settings of an older file in the same write, and
// that a file of a newer version is neither read nor written.
func TestServiceVersion(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir)
	write(t, dir, `{"model":"provider-a/model-a","log_level":"debug"}`)
	if err := s.SaveDebug(true); err != nil {
		t.Fatal(err)
	}
	keys, err := s.File().Read()
	if err != nil {
		t.Fatal(err)
	}
	if string(keys["version"]) != strconv.Itoa(Version) || keys["model"] != nil ||
		string(keys["default_provider"]) != `"provider-a"` || string(keys["default_model"]) != `"provider-a/model-a"` {
		t.Errorf("config file after a write = %v, want it at version %d with the model upgraded", keys, Version)
	}
	if cfg, err := s.Get(); err != nil || cfg.Version != Version || !cfg.Debug || cfg.LogLevel != LogDebug {
		t.Errorf("config = %+v, %v, want version %d and the settings kept", cfg, err, Version)
	}

	newer := fmt.Sprintf(`{"version":%d,"default_model":"lab-a/model-a"}`, Version+1)
	write(t, dir, newer)
	if _, err := s.Get(); !errors.Is(err, ErrNewerVersion) {
		t.Errorf("Get of a newer file = %v, want ErrNewerVersion", err)
	}
	if err := s.SaveDebug(false); !errors.Is(err, ErrNewerVersion) {
		t.Errorf("write of a newer file = %v, want ErrNewerVersion", err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, FileName)); string(data) != newer {
		t.Errorf("newer file after a refused write = %s, want it unchanged", data)
	}
}
