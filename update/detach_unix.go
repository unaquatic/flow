//go:build unix

package update

import (
	"os"
	"os/exec"
	"syscall"
)

// detach makes cmd run in a session of its own, without flow's terminal,
// so that neither the hangup of the terminal nor a key pressed in it stops
// it, and hands it lock, whose flock(2) lock it then holds until it exits.
func detach(cmd *exec.Cmd, lock *os.File) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.ExtraFiles = []*os.File{lock}
}
