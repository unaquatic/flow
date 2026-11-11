//go:build unix

package builtin

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// lastResortShell is the shell used when no other is found.
const lastResortShell = "/bin/sh"

// usableShell reports true: every POSIX shell found runs commands.
func usableShell(string) bool { return true }

// killProcessGroupOnCancel starts c in a new process group and makes
// cancellation kill the whole group, so processes started by the shell do not
// outlive it.
func killProcessGroupOnCancel(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		err := syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
