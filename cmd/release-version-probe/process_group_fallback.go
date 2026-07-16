//go:build !linux && !darwin

package main

import "os/exec"

// Go's portable process API can terminate the direct process. WaitDelay still
// bounds inherited output handles on platforms without the Unix process-group
// implementation above.
func configureProcessTree(_ *exec.Cmd) {}

func terminateProcessTree(command *exec.Cmd) {
	if command.Process != nil {
		_ = command.Process.Kill()
	}
}
