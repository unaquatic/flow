//go:build !unix

package builtin

import "os/exec"

// killProcessGroupOnCancel keeps c's default cancellation, which kills only
// the shell. Processes it started are cut off by bashWaitDelay instead.
func killProcessGroupOnCancel(*exec.Cmd) {}
