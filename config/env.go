package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// The environment variables of flow's own, which EnvVars lists. The API keys
// of the providers are the catalog's to name.
const (
	// EnvDataDir sets the folder where flow keeps its config, sessions and
	// log. Without it, flow uses the .flow folder in the home folder.
	EnvDataDir = "FLOW_HOME_DIR"
	// EnvDebug turns debug mode on or off, whatever the config file says.
	EnvDebug = "DEBUG"
	// EnvSnapshotDelay makes each workspace snapshot take that much longer,
	// a duration such as 2s, so that the row showing it being indexed stays
	// on screen long enough to look at.
	EnvSnapshotDelay = "FLOW_SNAPSHOT_DELAY"
	// EnvDisableTerminalTitle stops the terminal UI from changing the
	// terminal title.
	EnvDisableTerminalTitle = "FLOW_DISABLE_TERMINAL_TITLE"
	// EnvWidthProbe makes the terminal UI measure how wide the terminal
	// draws characters when it starts.
	EnvWidthProbe = "WIDTH_PROBE"
	// EnvServerPassword makes flow serve ask every request for this
	// password, by HTTP basic authentication.
	EnvServerPassword = "FLOW_SERVER_PASSWORD"
	// EnvServerUsername sets the user name that goes with
	// EnvServerPassword. Without it, the user name is flow.
	EnvServerUsername = "FLOW_SERVER_USERNAME"
)

// An EnvVar is an environment variable that flow reads.
type EnvVar struct {
	// Name is the variable's name, such as FLOW_HOME_DIR.
	Name string
	// Usage describes what the variable sets.
	Usage string
}

// EnvVars returns flow's own environment variables, as help lists them. The
// slice is new at each call.
func EnvVars() []EnvVar {
	return []EnvVar{
		{Name: EnvDataDir, Usage: "Folder for the config, sessions and log"},
		{Name: EnvDebug, Usage: "Turn debug mode on or off"},
		{Name: EnvSnapshotDelay, Usage: "Slow each snapshot by a duration, such as 2s, to preview indexing"},
		{Name: EnvDisableTerminalTitle, Usage: "Stop changing the terminal title"},
		{Name: EnvWidthProbe, Usage: "Measure character widths at startup"},
		{Name: EnvServerPassword, Usage: "Password that flow serve asks every request for"},
		{Name: EnvServerUsername, Usage: "User name that goes with the server password (default flow)"},
	}
}

// ServerCredentials returns the user name and password that EnvServerUsername
// and EnvServerPassword set for flow serve, each empty when unset.
func ServerCredentials() (username, password string) {
	return os.Getenv(EnvServerUsername), os.Getenv(EnvServerPassword)
}

// TerminalTitleDisabled reports whether EnvDisableTerminalTitle turns off
// terminal title updates.
func TerminalTitleDisabled() bool { return envFlag(EnvDisableTerminalTitle) }

// WidthProbe reports whether EnvWidthProbe asks for the startup width probe.
func WidthProbe() bool { return envFlag(EnvWidthProbe) }

// SnapshotDelay returns the time EnvSnapshotDelay adds to each snapshot. It
// returns zero when the variable is empty or unset, and zero with an error
// when it does not hold a duration of zero or more, such as 500ms or 2s.
func SnapshotDelay() (time.Duration, error) {
	v := os.Getenv(EnvSnapshotDelay)
	if v == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", EnvSnapshotDelay, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("%s: %q is negative", EnvSnapshotDelay, v)
	}
	return d, nil
}

// envFlag reports whether the environment variable name is on: set to
// anything but empty, 0 or false.
func envFlag(name string) bool {
	switch strings.ToLower(os.Getenv(name)) {
	case "", "0", "false":
		return false
	}
	return true
}
