package builtin

import "testing"

// TestDefaultShell_FallsBackToZsh checks that macOS falls back to zsh, its
// default login shell, before bash.
func TestDefaultShell_FallsBackToZsh(t *testing.T) {
	t.Setenv("SHELL", "")
	if got := DefaultShell(); got != "/bin/zsh" {
		t.Errorf("DefaultShell() with SHELL unset = %q; want /bin/zsh", got)
	}
}
