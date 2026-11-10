// Package config loads and combines application configuration.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/unaquatic/flow/product"
)

// LogLevel controls logging verbosity. The config file names it as one of
// the constants below, in lower case, as "debug".
type LogLevel string

const (
	// LogDebug enables debug logging.
	LogDebug LogLevel = "debug"
	// LogInfo enables informational logging.
	LogInfo LogLevel = "info"
	// LogWarn enables warning and error logging.
	LogWarn LogLevel = "warn"
	// LogError enables error logging only.
	LogError LogLevel = "error"
)

// Valid reports whether l is one of LogDebug, LogInfo, LogWarn and
// LogError.
func (l LogLevel) Valid() bool {
	switch l {
	case LogDebug, LogInfo, LogWarn, LogError:
		return true
	}
	return false
}

// Level returns the slog level that l names, or slog.LevelInfo, the
// default, when l names none.
func (l LogLevel) Level() slog.Level {
	switch l {
	case LogDebug:
		return slog.LevelDebug
	case LogWarn:
		return slog.LevelWarn
	case LogError:
		return slog.LevelError
	}
	return slog.LevelInfo
}

// Version is the format version of the config file that this build reads
// and writes. LoadDir reads a file of an older version as upgrade brings it
// to Version, every write of Service saves the file at Version, and a file
// of a newer version fails with ErrNewerVersion.
const Version = 1

// ErrNewerVersion reports a config file of a format version newer than
// Version, which a newer build wrote.
var ErrNewerVersion = errors.New("config file of a newer version")

// Config contains resolved application configuration.
type Config struct {
	// Version is the format version of the config file, Version once it
	// is read; a file without one is of version 0.
	Version int `json:"version"`

	// DefaultProvider is the provider the default model runs at, by its
	// catalog ID, such as "provider-a".
	DefaultProvider string `json:"default_provider,omitempty"`

	// DefaultModel is the default model, without its provider, in
	// "model:option:effort" form, as agent.Variant.Ref writes it, such as
	// "lab-a/model-a:fast:high", leaving out the option and the effort it
	// does not set.
	DefaultModel string `json:"default_model,omitempty"`

	// LogLevel controls logging verbosity.
	LogLevel LogLevel `json:"log_level,omitempty"`

	// Provider contains settings keyed by provider name.
	Provider map[string]*Provider `json:"provider,omitempty"`

	// Agent contains model and agent settings.
	Agent Agent `json:"agent"`

	// ShowBorder selects terminal border mode.
	ShowBorder bool `json:"show_border,omitempty"`

	// Debug enables additional terminal diagnostics, such as the snapshot
	// each message ends with. The terminal's /config menu sets it.
	Debug bool `json:"debug,omitempty"`

	// AutoUpdate makes the terminal UI check for a newer release of flow
	// when it starts, and install it in the background, which runs from the
	// next start. It is on by default; EnvDisableAutoUpdate turns it off,
	// whatever the file says. flow update checks and installs on request
	// either way.
	AutoUpdate bool `json:"auto_update,omitempty"`

	// Terminal holds the settings of the terminal UI, which its /config
	// menu sets.
	Terminal Terminal `json:"terminal,omitzero"`

	// Extensions holds the settings of each extension, keyed by its name,
	// which the extension reads with Extension, such as
	// {"snapshot": {"enabled": false}}.
	Extensions map[string]json.RawMessage `json:"extensions,omitempty"`

	// CatalogSource is the catalog server flow reads its catalog from, as
	// the catalog repository's cmd/catalog serves it, such as
	// "http://127.0.0.1:4100" for a local one. Empty selects
	// gateway.DefaultSource, the hosted catalog.
	CatalogSource string `json:"catalog_source,omitempty"`

	// HomeDir is the application data directory and is not serialized.
	HomeDir string `json:"-"`
}

// Provider holds settings for a single LLM provider.
type Provider struct {
	// APIKey is the configured provider API key.
	APIKey string `json:"api_key,omitempty"`

	// BaseURL moves the provider's endpoints: a host alone, such as
	// https://proxy.example, takes each there with its own path, as
	// https://proxy.example/v1 for https://api.provider-a.example/v1; a base URL with a path
	// replaces the provider's own.
	BaseURL string `json:"base_url,omitempty"`

	// Disabled prevents this provider from being registered.
	Disabled bool `json:"disabled,omitempty"`
}

// Agent holds model tuning and agent behavior parameters.
type Agent struct {
	// Temperature controls sampling randomness.
	Temperature *float64 `json:"temperature,omitempty"`

	// TopP controls nucleus sampling probability mass.
	TopP *float64 `json:"top_p,omitempty"`

	// MaxSteps is the maximum number of agentic iterations per run.
	MaxSteps int `json:"max_steps,omitempty"`

	// RetryMaxAttempts includes the initial provider attempt.
	RetryMaxAttempts int `json:"retry_max_attempts,omitempty"`

	// RetryDelay is the delay between provider attempts.
	RetryDelay Duration `json:"retry_delay"`

	// SystemPrompt is additional text appended to the system prompt, after
	// the AGENTS.md files, as app.SystemPrompt joins them. A session stores
	// its system prompt with its first prompt, so a change applies to the
	// sessions opened after it.
	SystemPrompt string `json:"system_prompt,omitempty"`
}

// Terminal holds the settings of the terminal UI, which its /config
// menu sets and saves. The terminal's style settings, which /config shows
// while debug is on, are not saved: each terminal starts with their
// defaults.
type Terminal struct {
	// Fullscreen starts the terminal on the alternate screen rather than
	// inline.
	Fullscreen bool `json:"fullscreen,omitempty"`
}

// versionKey is the key of the config file that holds its format version.
const versionKey = "version"

// legacyModelKey is the key that config files of version 0 name the
// default model by, in "provider/model:option:effort" form.
const legacyModelKey = "model"

// upgrade brings keys, the top-level keys of a config file, from their
// format version to Version, in place, and sets the version. It fails with
// ErrNewerVersion when keys are of a newer version, and changes nothing
// then.
//
// Version 1 replaces the "model" key of version 0 with "default_provider"
// and "default_model", unless the file sets "default_model" already. The
// older form left out the model's lab when the lab was the provider, so its
// first segment becomes the default provider, and also the lab of a model
// left without one.
func upgrade(keys map[string]json.RawMessage) error {
	var v int
	if raw, ok := keys[versionKey]; ok {
		if err := json.Unmarshal(raw, &v); err != nil {
			return fmt.Errorf("%s: %w", versionKey, err)
		}
	}
	if v > Version {
		return fmt.Errorf("%w: version %d, and this build reads up to %d", ErrNewerVersion, v, Version)
	}
	if v < 1 {
		if err := upgradeV1(keys); err != nil {
			return err
		}
	}
	keys[versionKey] = json.RawMessage(strconv.Itoa(Version))
	return nil
}

// upgradeV1 brings keys of version 0 to version 1, as upgrade describes.
func upgradeV1(keys map[string]json.RawMessage) error {
	raw, ok := keys[legacyModelKey]
	if !ok {
		return nil
	}
	delete(keys, legacyModelKey)
	if _, ok := keys["default_model"]; ok {
		return nil
	}
	var model string
	if err := json.Unmarshal(raw, &model); err != nil {
		return fmt.Errorf("%s: %w", legacyModelKey, err)
	}
	provider, rest, ok := strings.Cut(model, "/")
	if !ok {
		return setDefaultModel(keys, "", model)
	}
	if !strings.Contains(rest, "/") {
		rest = provider + "/" + rest
	}
	return setDefaultModel(keys, provider, rest)
}

// defaultModelOf returns the default provider and model that keys, the
// top-level keys of a config file of Version, set. Either is empty when
// keys do not set it.
func defaultModelOf(keys map[string]json.RawMessage) (provider, model string, err error) {
	if raw, ok := keys["default_provider"]; ok {
		if err := json.Unmarshal(raw, &provider); err != nil {
			return "", "", fmt.Errorf("default_provider: %w", err)
		}
	}
	if raw, ok := keys["default_model"]; ok {
		if err := json.Unmarshal(raw, &model); err != nil {
			return "", "", fmt.Errorf("default_model: %w", err)
		}
	}
	return provider, model, nil
}

// setDefaultModel sets provider and model as the default provider and
// model in keys, the top-level keys of a config file. An empty provider
// removes the default provider.
func setDefaultModel(keys map[string]json.RawMessage, provider, model string) error {
	m, err := json.Marshal(model)
	if err != nil {
		return err
	}
	keys["default_model"] = m
	if provider == "" {
		delete(keys, "default_provider")
		return nil
	}
	p, err := json.Marshal(provider)
	if err != nil {
		return err
	}
	keys["default_provider"] = p
	return nil
}

// legacyExtensions maps each top-level key of config files written before
// extensions had settings of their own to the extension that now reads it.
// The extension is given the key's value as it is, unless its own section
// sets it, and reads both forms.
var legacyExtensions = map[string]string{
	"snapshot": "snapshot",
}

// Extension decodes the settings of the extension called name, from its
// entry in the extensions section of the config file, into v. It leaves v as
// it is when the file has no settings for the extension.
func (c *Config) Extension(name string, v any) error {
	if c == nil {
		return nil
	}
	data, ok := c.Extensions[name]
	if !ok {
		return nil
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("settings of extension %s: %w", name, err)
	}
	return nil
}

// DataDir returns the application data directory, which holds the config
// file, and creates it when it is missing: the directory EnvDataDir names,
// where a leading ~ stands for the home directory and a relative path is
// taken from the working directory, or else ~/.flow.
func DataDir() (string, error) {
	dir, err := dataDirPath(os.Getenv(EnvDataDir))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("creating data directory: %w", err)
	}
	return dir, nil
}

// dataDirPath returns the absolute path of the data directory that name
// chooses, as DataDir reads it, or of the default one when name is empty.
func dataDirPath(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil && (name == "" || strings.HasPrefix(name, "~")) {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	switch {
	case name == "":
		return filepath.Join(home, "."+product.ID), nil
	case name == "~":
		return home, nil
	case strings.HasPrefix(name, "~/"):
		name = filepath.Join(home, name[2:])
	}
	return filepath.Abs(name)
}

// Defaults returns the configuration of data directory dir when its config
// file and the environment set nothing.
func Defaults(dir string) *Config {
	return &Config{
		LogLevel:   LogInfo,
		HomeDir:    dir,
		ShowBorder: true,
		AutoUpdate: true,
		Agent: Agent{
			MaxSteps:         50,
			RetryMaxAttempts: 5,
			RetryDelay:       Duration{3 * time.Second},
		},
	}
}

// LoadDir returns the configuration of data directory dir: its defaults,
// with the config file in dir merged in selectively, and then the
// environment variables, of which EnvDebug overrides the file while API-key
// variables fill empty keys. It does not create dir.
func LoadDir(dir string) (*Config, error) {
	cfg := Defaults(dir)

	if err := mergeFromFile(cfg, File{Dir: dir, Name: FileName}); err != nil {
		return nil, fmt.Errorf("loading global config: %w", err)
	}

	applyEnvOverrides(cfg)

	return cfg, nil
}

// applyEnvOverrides overrides the debug setting and fills empty API keys.
func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv(EnvDebug); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "on":
			cfg.Debug = true
		case "0", "false", "no", "off":
			cfg.Debug = false
		}
	}

	// Use NAME_API_KEY for the configured providers. The app reads the
	// environment variables the catalog names for each provider besides.
	for name, pc := range cfg.Provider {
		if pc.APIKey == "" {
			envKey := strings.ToUpper(name) + "_API_KEY"
			pc.APIKey = os.Getenv(envKey)
		}
	}
}

// mergeFromFile selectively merges the config file f into cfg. A missing or
// empty file is ignored.
func mergeFromFile(cfg *Config, f File) error {
	// raw preserves the distinction between absent and zero-valued fields.
	raw, err := f.Read()
	if err != nil || len(raw) == 0 {
		return err
	}
	// Read an older file as its upgrade to Version, which a write saves.
	if err := upgrade(raw); err != nil {
		path, _ := f.Path()
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	// Decode the overlay from the keys of the one read, so that both see
	// the same file even when a write replaces it meanwhile.
	data, err := json.Marshal(raw)
	if err != nil {
		return err
	}
	var overlay Config
	if err := json.Unmarshal(data, &overlay); err != nil {
		path, _ := f.Path()
		return fmt.Errorf("parsing %s: %w", path, err)
	}

	cfg.Version = overlay.Version
	if cfg.DefaultProvider, cfg.DefaultModel, err = defaultModelOf(raw); err != nil {
		path, _ := f.Path()
		return fmt.Errorf("parsing %s: %w", path, err)
	}
	if _, ok := raw["log_level"]; ok {
		if !overlay.LogLevel.Valid() {
			path, _ := f.Path()
			return fmt.Errorf("parsing %s: log_level %q is none of %q, %q, %q and %q",
				path, overlay.LogLevel, LogDebug, LogInfo, LogWarn, LogError)
		}
		cfg.LogLevel = overlay.LogLevel
	}

	if _, ok := raw["provider"]; ok && overlay.Provider != nil {
		if cfg.Provider == nil {
			cfg.Provider = make(map[string]*Provider)
		}
		for name, pc := range overlay.Provider {
			if existing, ok := cfg.Provider[name]; ok {
				if pc.APIKey != "" {
					existing.APIKey = pc.APIKey
				}
				if pc.BaseURL != "" {
					existing.BaseURL = pc.BaseURL
				}
				if pc.Disabled {
					existing.Disabled = true
				}
			} else {
				cfg.Provider[name] = pc
			}
		}
	}

	if _, ok := raw["agent"]; ok {
		if overlay.Agent.Temperature != nil {
			cfg.Agent.Temperature = overlay.Agent.Temperature
		}
		if overlay.Agent.TopP != nil {
			cfg.Agent.TopP = overlay.Agent.TopP
		}
		if overlay.Agent.MaxSteps > 0 {
			cfg.Agent.MaxSteps = overlay.Agent.MaxSteps
		}
		if overlay.Agent.RetryMaxAttempts > 0 {
			cfg.Agent.RetryMaxAttempts = overlay.Agent.RetryMaxAttempts
		}
		if overlay.Agent.RetryDelay.Duration > 0 {
			cfg.Agent.RetryDelay = overlay.Agent.RetryDelay
		}
		if overlay.Agent.SystemPrompt != "" {
			cfg.Agent.SystemPrompt = overlay.Agent.SystemPrompt
		}
	}

	if _, ok := raw["show_border"]; ok {
		cfg.ShowBorder = overlay.ShowBorder
	}

	if _, ok := raw["debug"]; ok {
		cfg.Debug = overlay.Debug
	}

	if _, ok := raw["auto_update"]; ok {
		cfg.AutoUpdate = overlay.AutoUpdate
	}

	if _, ok := raw["terminal"]; ok {
		cfg.Terminal = overlay.Terminal
	}

	if _, ok := raw["extensions"]; ok && overlay.Extensions != nil {
		if cfg.Extensions == nil {
			cfg.Extensions = make(map[string]json.RawMessage)
		}
		maps.Copy(cfg.Extensions, overlay.Extensions)
	}
	for key, name := range legacyExtensions {
		if v, ok := raw[key]; ok {
			if _, set := cfg.Extensions[name]; !set {
				if cfg.Extensions == nil {
					cfg.Extensions = make(map[string]json.RawMessage)
				}
				cfg.Extensions[name] = v
			}
		}
	}

	if _, ok := raw["catalog_source"]; ok {
		cfg.CatalogSource = overlay.CatalogSource
	}

	return nil
}
