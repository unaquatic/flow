//go:build !darwin && !windows

package builtin

// fallbackShells returns the shells tried in order when $SHELL is not a
// usable POSIX shell.
func fallbackShells() []string { return []string{"bash"} }
