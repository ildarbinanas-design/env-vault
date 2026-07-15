//go:build windows

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"time"
)

func configureProcess(cmd *exec.Cmd) {}

func terminateProcess(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return os.ErrProcessDone
	}
	// A timed-out go test or reporting command can own a test binary and CLI
	// grandchildren. Hosted Windows runners provide taskkill; /T ensures those
	// descendants cannot survive the deadline. Retain Process.Kill as the
	// fallback for minimal Windows installations.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	err := cmd.Process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}
