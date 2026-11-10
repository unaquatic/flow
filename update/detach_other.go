//go:build !unix

package update

import (
	"os"
	"os/exec"
)

// detach leaves cmd as it is where processes cannot be detached from the
// terminal, or inherit lock, as they are on Unix.
func detach(*exec.Cmd, *os.File) {}
