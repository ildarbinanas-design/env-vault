//go:build windows

package e2e_test

import (
	"context"
	"os/exec"
	"strconv"
	"time"
)

func configureProcess(*exec.Cmd) {}

func terminateProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	// taskkill /T terminates descendants as well as the direct process. Fall
	// back to Process.Kill if taskkill is unavailable on a minimal runner.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	return cmd.Process.Kill()
}
