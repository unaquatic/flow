//go:build !unix

package main

import (
	"fmt"
	"os"
	"os/exec"
)

// relaunch starts flow again with args in the same terminal, where a process
// cannot replace itself, and waits for it to end.
func relaunch(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("relaunch: %w", err)
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
