package builtin

// fallbackShells returns the shells tried in order when $SHELL is not a
// usable POSIX shell. zsh is the default login shell on macOS and is always
// installed there.
func fallbackShells() []string { return []string{"/bin/zsh", "bash"} }
