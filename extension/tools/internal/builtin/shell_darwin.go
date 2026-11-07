package builtin

// fallbackShells are tried in order when $SHELL is not a usable POSIX shell.
// zsh is the default login shell on macOS and is always installed there.
var fallbackShells = []string{"/bin/zsh", "bash"}
