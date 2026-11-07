package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestServiceGetReadsTheFileEachTime checks that Get reads the config file
// anew on each call, so a change to it applies to the next one.
func TestServiceGetReadsTheFileEachTime(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir)
	if cfg, err := s.Get(); err != nil || cfg.Model != DefaultModel {
		t.Fatalf("Get without a file = %q, %v, want the default model", cfg.Model, err)
	}

	write(t, dir, `{"model":"openai/gpt-x"}`)
	if cfg, err := s.Get(); err != nil || cfg.Model != "openai/gpt-x" {
		t.Errorf("Get after a write = %q, %v, want openai/gpt-x", cfg.Model, err)
	}
	write(t, dir, `{"model":"anthropic/claude-x"}`)
	if cfg, err := s.Get(); err != nil || cfg.Model != "anthropic/claude-x" {
		t.Errorf("Get after a rewrite = %q, %v, want anthropic/claude-x", cfg.Model, err)
	}
}

// TestServiceGetReadsEverySetting checks that Get reads each setting the
// config file can set, the catalog source among them.
func TestServiceGetReadsEverySetting(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DEBUG", "")
	write(t, dir, `{"model":"openai/gpt-x","log_level":"warn","extensions":{"snapshot":{"enabled":false}},"catalog_source":"http://catalog.test",
		"provider":{"anthropic":{"base_url":"http://anthropic.test"}},"agent":{"retry_max_attempts":2,"retry_delay":"1s"},
		"debug":true,"terminal":{"fullscreen":true,"key_hints":"footer","tool_marker":true}}`)
	cfg, err := NewService(dir).Get()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if cfg.Model != "openai/gpt-x" || cfg.LogLevel != LogWarn || string(cfg.Extensions["snapshot"]) != `{"enabled":false}` || cfg.CatalogSource != "http://catalog.test" ||
		cfg.Provider["anthropic"].BaseURL != "http://anthropic.test" || cfg.Agent.RetryMaxAttempts != 2 || cfg.Agent.RetryDelay.Seconds() != 1 ||
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

// TestServiceSaveModel checks that SaveModel creates the config file,
// replaces only its model, keeps its other settings, and leaves no temporary
// file.
func TestServiceSaveModel(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir)
	if err := s.SaveModel("openai/gpt-x"); err != nil {
		t.Fatalf("SaveModel without a file: %v", err)
	}
	if cfg, err := s.Get(); err != nil || cfg.Model != "openai/gpt-x" {
		t.Errorf("model = %q, %v, want openai/gpt-x", cfg.Model, err)
	}

	write(t, dir, `{"log_level":"debug","model":"openai/gpt-x"}`)
	if err := s.SaveModel("anthropic/claude-x"); err != nil {
		t.Fatalf("SaveModel: %v", err)
	}
	cfg, err := s.Get()
	if err != nil || cfg.Model != "anthropic/claude-x" || cfg.LogLevel != LogDebug {
		t.Errorf("config = model %q, log level %q, %v, want the saved model and the kept log level", cfg.Model, cfg.LogLevel, err)
	}
	if got, want := names(t, dir), []string{FileName, FileName + ".lock"}; !slices.Equal(got, want) {
		t.Errorf("directory holds %q, want %q: no temporary file left", got, want)
	}
}

// TestServiceSaveTerminalAndDebug checks that SaveTerminal replaces the
// terminal settings of the config file and SaveDebug its debug setting,
// each keeping the other settings, the model among them.
func TestServiceSaveTerminalAndDebug(t *testing.T) {
	t.Setenv("DEBUG", "")
	dir := t.TempDir()
	write(t, dir, `{"model":"openai/gpt-x","terminal":{"selection":"arrow"}}`)
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
	if cfg.Model != "openai/gpt-x" || !cfg.Debug || !term.Fullscreen || term.CacheUsage != "per_turn" ||
		!term.ToolMarker || term.Selection != "" {
		t.Errorf("config = model %q, debug %v, terminal %+v; want the model kept, debug on, and the saved terminal settings alone",
			cfg.Model, cfg.Debug, term)
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

// TestServiceSaveModelKeepsPermissions checks that SaveModel keeps the
// permissions of the config file, which can hold API keys, and creates a
// new one private to its user.
func TestServiceSaveModelKeepsPermissions(t *testing.T) {
	dir := t.TempDir()
	s := NewService(dir)
	path := filepath.Join(dir, FileName)

	if err := s.SaveModel("openai/gpt-x"); err != nil {
		t.Fatal(err)
	}
	if mode := perm(t, path); mode != 0o600 {
		t.Errorf("new config file mode = %04o, want 0600", mode)
	}

	for _, mode := range []os.FileMode{0o600, 0o640} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		if err := s.SaveModel("anthropic/claude-x"); err != nil {
			t.Fatal(err)
		}
		if got := perm(t, path); got != mode {
			t.Errorf("config file mode after SaveModel = %04o, want %04o kept", got, mode)
		}
	}
}

// TestServiceSaveModelThroughSymlink checks that SaveModel writes the file a
// symbolically linked config file names, keeping the link, as for a config
// kept in a dotfiles repository, and the target when it does not exist yet.
func TestServiceSaveModelThroughSymlink(t *testing.T) {
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
			if err := s.SaveModel("openai/gpt-x"); err != nil {
				t.Fatal(err)
			}
			if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("config file after SaveModel is not the link: %v, %v", info, err)
			}
			cfg, err := s.Get()
			if err != nil || cfg.Model != "openai/gpt-x" {
				t.Errorf("model read through the link = %q, %v, want openai/gpt-x", cfg.Model, err)
			}
			if existing && cfg.LogLevel != LogDebug {
				t.Errorf("log level = %q, want the target's kept", cfg.LogLevel)
			}
			if got, want := names(t, dir), []string{FileName, FileName + ".lock"}; !slices.Equal(got, want) {
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
