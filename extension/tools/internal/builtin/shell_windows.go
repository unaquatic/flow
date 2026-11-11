//go:build windows

package builtin

import (
	"cmp"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// lastResortShell is "", as Windows has no POSIX shell of its own.
const lastResortShell = ""

// fallbackShells returns the shells tried in order when $SHELL is not a
// usable POSIX shell: Git Bash where Git for Windows installs it, for all
// users or for one, and then bash from PATH, as MSYS2 or Cygwin put it there.
func fallbackShells() []string {
	var dirs []string
	for _, dir := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)")} {
		if dir != "" {
			dirs = append(dirs, dir)
		}
	}
	if dir := os.Getenv("LocalAppData"); dir != "" {
		dirs = append(dirs, filepath.Join(dir, "Programs"))
	}
	shells := make([]string, 0, len(dirs)+1)
	for _, dir := range dirs {
		shells = append(shells, filepath.Join(dir, "Git", "bin", "bash.exe"))
	}
	return append(shells, "bash")
}

// usableShell reports whether the shell at path runs commands on Windows:
// whether it is not the bash.exe of WSL, in System32 or WindowsApps, which
// runs them in Linux, where Windows paths do not resolve.
func usableShell(path string) bool {
	switch strings.ToLower(filepath.Base(filepath.Dir(path))) {
	case "system32", "sysnative", "windowsapps":
		return false
	}
	return true
}

// killProcessGroupOnCancel makes cancellation kill c's whole process tree
// with taskkill, so that the processes the shell started do not outlive it.
// These include the bash that Git Bash's bash.exe launches and waits for.
// When taskkill fails, the shell alone is killed.
func killProcessGroupOnCancel(c *exec.Cmd) {
	c.Cancel = func() error {
		taskkill := filepath.Join(cmp.Or(os.Getenv("SystemRoot"), `C:\Windows`), "System32", "taskkill.exe")
		if exec.Command(taskkill, "/F", "/T", "/PID", strconv.Itoa(c.Process.Pid)).Run() == nil {
			return nil
		}
		return c.Process.Kill()
	}
}
