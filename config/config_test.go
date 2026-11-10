package config

import (
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// TestLogLevel checks that the config file's log level reads as the slog
// level it names, and that a level it does not name, upper-case names
// among them, fails loading.
func TestLogLevel(t *testing.T) {
	tests := []struct {
		file  string
		want  LogLevel
		level slog.Level
	}{
		{file: "debug", want: LogDebug, level: slog.LevelDebug},
		{file: "info", want: LogInfo, level: slog.LevelInfo},
		{file: "warn", want: LogWarn, level: slog.LevelWarn},
		{file: "error", want: LogError, level: slog.LevelError},
	}
	for _, tt := range tests {
		dir := t.TempDir()
		write(t, dir, `{"log_level":"`+tt.file+`"}`)
		cfg, err := LoadDir(dir)
		if err != nil {
			t.Fatalf("LoadDir with %q: %v", tt.file, err)
		}
		if cfg.LogLevel != tt.want || cfg.LogLevel.Level() != tt.level {
			t.Errorf("log level %q = %q at %v, want %q at %v", tt.file, cfg.LogLevel, cfg.LogLevel.Level(), tt.want, tt.level)
		}
	}

	for _, file := range []string{"DEBUG", "Warn", "verbose", ""} {
		dir := t.TempDir()
		write(t, dir, `{"log_level":"`+file+`"}`)
		if _, err := LoadDir(dir); err == nil || !strings.Contains(err.Error(), "log_level") {
			t.Errorf("LoadDir with %q = %v, want an error naming log_level", file, err)
		}
	}
}

// TestExtension checks that an extension reads its settings from its entry
// in the extensions section, or from the top-level key of an older config
// file that held them, which its own entry overrides, and that settings the
// file does not hold leave the extension's defaults.
func TestExtension(t *testing.T) {
	type settings struct {
		Enabled *bool `json:"enabled"`
	}
	tests := []struct {
		name string
		file string
		// want is the raw settings the extension is given, or "" for none.
		want string
	}{
		{name: "section", file: `{"extensions":{"snapshot":{"enabled":false}}}`, want: `{"enabled":false}`},
		{name: "legacy key", file: `{"snapshot":false}`, want: `false`},
		{name: "section over legacy key", file: `{"snapshot":true,"extensions":{"snapshot":{"enabled":false}}}`, want: `{"enabled":false}`},
		{name: "none", file: `{"default_model":"lab-a/model-a"}`, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, tt.file)
			cfg, err := LoadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := string(cfg.Extensions["snapshot"]); got != tt.want {
				t.Errorf("snapshot settings = %q, want %q", got, tt.want)
			}
		})
	}

	on := true
	s := settings{Enabled: &on}
	cfg := &Config{Extensions: map[string]json.RawMessage{"other": json.RawMessage(`{"enabled":false}`)}}
	if err := cfg.Extension("snapshot", &s); err != nil || s.Enabled != &on {
		t.Errorf("Extension without settings = %v, changed %+v; want the defaults kept", err, s)
	}
	if err := cfg.Extension("other", &s); err != nil || *s.Enabled {
		t.Errorf("Extension = %v, %+v; want enabled false", err, s)
	}
	cfg.Extensions["bad"] = json.RawMessage(`{"enabled":"yes"}`)
	if err := cfg.Extension("bad", &s); err == nil || !strings.Contains(err.Error(), "extension bad") {
		t.Errorf("Extension of malformed settings = %v, want an error naming the extension", err)
	}
}

// TestAutoUpdate checks that updates install in the background unless the
// config file turns auto_update off.
func TestAutoUpdate(t *testing.T) {
	for file, want := range map[string]bool{
		`{}`:                    true,
		`{"auto_update":true}`:  true,
		`{"auto_update":false}`: false,
	} {
		dir := t.TempDir()
		write(t, dir, file)
		cfg, err := LoadDir(dir)
		if err != nil {
			t.Fatalf("LoadDir with %s: %v", file, err)
		}
		if cfg.AutoUpdate != want {
			t.Errorf("auto update with %s = %v, want %v", file, cfg.AutoUpdate, want)
		}
	}
}
