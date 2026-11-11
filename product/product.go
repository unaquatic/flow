// Package product names the application and its version. It imports no
// package of this module, so every package can use it.
package product

import (
	"runtime"
	"runtime/debug"
)

// Platform returns the system and CPU the binary runs on, as Go names them,
// such as darwin/arm64.
func Platform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

const (
	// Name is the application's name, which is also the name of its command.
	Name = "flow"

	// ID identifies the application in file and directory names, such as its
	// default data directory ~/.flow, and in environment variable names, such
	// as FLOW_HOME_DIR.
	ID = "flow"

	// PrimaryColor is the application's orange. It draws the square of Icon, the
	// selected row of a menu, and the application's name where a notice or
	// the command line names it.
	PrimaryColor = "#ff5f00"
)

// Version returns the application version: the main module version that the
// go command recorded in the binary from version control. It is the tag, such
// as v0.1.0, for a build of a tagged commit, and a pseudo-version, such as
// v0.1.1-0.20261010091914-54eabbb75a2d, for a build of a later commit, with
// +dirty appended when the build had uncommitted changes. It is "(devel)"
// when the binary records no version, as for go run, go test, and builds
// outside a repository.
func Version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}
