//go:build !darwin

package builtin

// fallbackShells are tried in order when $SHELL is not a usable POSIX shell.
var fallbackShells = []string{"bash"}
