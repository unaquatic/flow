package config

import (
	"context"
	"encoding/json"
)

// FileName is the name of the config file in the data directory.
const FileName = "config.json"

// Service reads and writes the configuration of one data directory. It holds
// no settings: every Get reads the config file and the environment anew, so
// a change to either applies to the next one.
type Service struct {
	// Dir is the data directory, which holds the config file.
	Dir string
}

// NewService returns a Service for data directory dir.
func NewService(dir string) *Service {
	return &Service{Dir: dir}
}

// Get returns the configuration as the config file and the environment set
// it now, as LoadDir does. It fails when the config file cannot be read, as
// carrying on with the defaults would quietly use settings, such as the
// model, that the file does not set.
func (s *Service) Get() (*Config, error) {
	return LoadDir(s.Dir)
}

// File returns the config file of the data directory.
func (s *Service) File() File {
	return File{Dir: s.Dir, Name: FileName}
}

// SaveDefaultModel sets provider and model, in the forms
// Config.DefaultProvider and Config.DefaultModel hold, as the default
// provider and model of the config file, in one write, as edit saves it.
// An empty provider removes the default provider.
func (s *Service) SaveDefaultModel(provider, model string) error {
	return s.edit(func(keys map[string]json.RawMessage) error {
		return setDefaultModel(keys, provider, model)
	})
}

// UpdateDefaultModel applies change to the default provider and model of
// the config file, as Get reads them, and saves the provider and model it
// returns, as SaveDefaultModel saves them, in one write under the file's
// lock, so that a change another process makes meanwhile is not lost.
// Nothing is saved when change fails. change must not update the config
// file, which would wait for the lock.
func (s *Service) UpdateDefaultModel(change func(provider, model string) (string, string, error)) error {
	return s.edit(func(keys map[string]json.RawMessage) error {
		provider, model, err := defaultModelOf(keys)
		if err != nil {
			return err
		}
		if provider, model, err = change(provider, model); err != nil {
			return err
		}
		return setDefaultModel(keys, provider, model)
	})
}

// SaveTerminal sets t as the terminal settings of the config file, as save
// sets a setting.
func (s *Service) SaveTerminal(t Terminal) error { return save(s, "terminal", t) }

// SaveDebug sets on as the debug setting of the config file, as save sets a
// setting. The DEBUG environment variable still overrides it.
func (s *Service) SaveDebug(on bool) error { return save(s, "debug", on) }

// save sets v as the setting key of the config file, as edit saves it, or
// removes the setting when v has an IsZero method that reports true, as
// Section.Set does.
func save[T any](s *Service, key string, v T) error {
	return s.edit(func(keys map[string]json.RawMessage) error {
		if z, ok := any(v).(interface{ IsZero() bool }); ok && z.IsZero() {
			delete(keys, key)
			return nil
		}
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		keys[key] = data
		return nil
	})
}

// edit applies change to the top-level keys of the config file, upgraded
// to Version first, as upgrade brings them, and saves them, as File.Edit
// does: it keeps the file's other settings, its permissions, and a symbolic
// link the file is, and replaces the file with a rename. Every write of the
// config file goes through it, so that the file is saved at Version. It
// fails, and saves nothing, when the file is of a newer version.
func (s *Service) edit(change func(keys map[string]json.RawMessage) error) error {
	return s.File().Edit(context.Background(), func(keys map[string]json.RawMessage) error {
		if err := upgrade(keys); err != nil {
			return err
		}
		return change(keys)
	})
}
