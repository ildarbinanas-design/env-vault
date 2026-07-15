//go:build !windows

package e2e_test

import (
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func testExecSignalForwarding(sc *scenario) {
	ready := filepath.Join(sc.root, "signal-ready")
	received := filepath.Join(sc.root, "signal-received")
	command, err := launchEnvVault(sc, runOptions{}, "exec", "--", sc.suite.helper, "wait-signal", "--ready", ready, "--received", received)
	if err != nil {
		sc.t.Fatalf("start signal-forwarding command: %v", err)
	}
	waitForFile(sc.t, ready, 5*time.Second)
	if err := command.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		sc.t.Fatalf("signal env-vault process: %v", err)
	}
	result := finishLaunched(sc, command, 5*time.Second, true)
	wantExit(sc.t, result, 0)
	wantEmpty(sc.t, result.Stdout, "signal exec stdout")
	wantEmpty(sc.t, result.Stderr, "signal exec stderr")
	waitForFile(sc.t, received, time.Second)
	data, err := os.ReadFile(received)
	if err != nil {
		sc.t.Fatalf("read received signal marker: %v", err)
	}
	if string(data) != "terminated\n" {
		sc.t.Fatalf("forwarded signal=%q, want terminated", data)
	}
}
