package app

import "github.com/unaquatic/flow/config"

// Config returns the configuration as the config file and the environment
// set it now. It fails when the config file cannot be read.
func (a *App) Config() (*config.Config, error) {
	return a.config.Get()
}

// SaveTerminal saves t as the terminal's settings in the config file,
// keeping its other settings.
func (a *App) SaveTerminal(t config.Terminal) error {
	return a.config.SaveTerminal(t)
}

// SaveDebug saves on as the debug setting of the config file, keeping its
// other settings.
func (a *App) SaveDebug(on bool) error {
	return a.config.SaveDebug(on)
}
