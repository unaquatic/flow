package config

import "context"

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

// SaveModel sets model as the model of the config file, as save sets a
// setting.
func (s *Service) SaveModel(model string) error { return save(s, "model", model) }

// SaveTerminal sets t as the terminal settings of the config file, as save
// sets a setting.
func (s *Service) SaveTerminal(t Terminal) error { return save(s, "terminal", t) }

// SaveDebug sets on as the debug setting of the config file, as save sets a
// setting. The DEBUG environment variable still overrides it.
func (s *Service) SaveDebug(on bool) error { return save(s, "debug", on) }

// save sets v as the setting key of the config file, as Section.Set does:
// it keeps the file's other settings, its permissions, and a symbolic link
// the file is, and replaces the file with a rename.
func save[T any](s *Service, key string, v T) error {
	return Section[T]{File: s.File(), Key: key}.Set(context.Background(), v)
}
