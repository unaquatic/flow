//go:build unix

package main

import (
	"fmt"
	"os"
	"syscall"
)

// relaunch replaces the process with flow started again with args, in the
// same terminal, under the same process ID. It returns only when that fails.
func relaunch(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("relaunch: %w", err)
	}
	err = syscall.Exec(exe, append([]string{os.Args[0]}, args...), os.Environ())
	return fmt.Errorf("relaunch %s: %w", exe, err)
}
