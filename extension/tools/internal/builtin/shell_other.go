//go:build !unix && !windows

package builtin

import "os/exec"

// lastResortShell is the shell used when no other is found.
const lastResortShell = "/bin/sh"

// usableShell reports true: every POSIX shell found runs commands.
func usableShell(string) bool { return true }

// killProcessGroupOnCancel keeps c's default cancellation, which kills only
// the shell. Processes it started are cut off by bashWaitDelay instead.
func killProcessGroupOnCancel(*exec.Cmd) {}
