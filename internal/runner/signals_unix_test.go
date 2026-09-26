//go:build !windows

package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
)

func TestChildKilledBySignalReportsTheSignal(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code, err := (CommandRunner{Stdout: &stdout, Stderr: &stderr}).Run(context.Background(),
		[]string{"sh", "-c", `kill -TERM $$`}, []string{"PATH=/bin:/usr/bin"})
	var status *apperrors.ExitStatus
	if !errors.As(err, &status) || status.Signal != syscall.SIGTERM {
		t.Fatalf("err = %v, want exit status carrying SIGTERM", err)
	}
	if want := 128 + int(syscall.SIGTERM); code != want || status.Code != want {
		t.Fatalf("code = %d, status code = %d, want %d", code, status.Code, want)
	}
}

// startSignalLogger starts a child that appends the name of every trapped
// signal to a log file and exits on SIGTERM.
func startSignalLogger(t *testing.T) (*exec.Cmd, string) {
	t.Helper()
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	log := filepath.Join(dir, "log")
	child := exec.Command("sh", "-c", `trap 'echo int >> "$2"' INT
trap 'echo quit >> "$2"' QUIT
trap 'echo term >> "$2"; exit 0' TERM
: > "$1"
while :; do sleep 0.05; done`, "sh", ready, log)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { _, err := os.Stat(ready); return err == nil }, "child readiness")
	return child, log
}

func waitFor(t *testing.T, done func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func logged(log string) string {
	data, _ := os.ReadFile(log)
	return strings.TrimSpace(string(data))
}

func withTerminalForeground(t *testing.T, foreground bool) {
	t.Helper()
	old := inTerminalForeground
	inTerminalForeground = func() bool { return foreground }
	t.Cleanup(func() { inTerminalForeground = old })
}

func stopSignalLogger(t *testing.T, child *exec.Cmd, ch chan os.Signal, stop func()) {
	t.Helper()
	ch <- syscall.SIGTERM
	waited := make(chan error, 1)
	go func() { waited <- child.Wait() }()
	select {
	case <-waited:
	case <-time.After(10 * time.Second):
		_ = child.Process.Kill()
		t.Fatal("child did not exit after SIGTERM")
	}
	stop()
}

// In a terminal's foreground group the terminal delivers SIGINT and SIGQUIT to
// the child itself, so forwarding them would deliver each twice.
func TestForwardSignalsSkipsInterruptsTheTerminalAlreadyDelivered(t *testing.T) {
	withTerminalForeground(t, true)
	child, log := startSignalLogger(t)
	ch := make(chan os.Signal, 4)
	stop := forwardSignals(child.Process, ch)
	ch <- os.Interrupt
	ch <- syscall.SIGQUIT
	stopSignalLogger(t, child, ch, stop)
	if got := logged(log); got != "term" {
		t.Fatalf("child received %q, want only term", got)
	}
}

// Without a terminal, SIGINT and SIGQUIT come from a service manager or a
// script, and only env-vault receives them, so they must be forwarded.
func TestForwardSignalsPassesInterruptsWithoutATerminal(t *testing.T) {
	withTerminalForeground(t, false)
	child, log := startSignalLogger(t)
	ch := make(chan os.Signal, 4)
	stop := forwardSignals(child.Process, ch)
	ch <- os.Interrupt
	waitFor(t, func() bool { return strings.Contains(logged(log), "int") }, "forwarded SIGINT")
	ch <- syscall.SIGQUIT
	waitFor(t, func() bool { return strings.Contains(logged(log), "quit") }, "forwarded SIGQUIT")
	stopSignalLogger(t, child, ch, stop)
	if got := logged(log); got != "int\nquit\nterm" {
		t.Fatalf("child received %q, want int, quit, term", got)
	}
}

func TestIgnoredAtStartSeesAnInheritedIgnoredSignal(t *testing.T) {
	if os.Getenv("ENV_VAULT_RUNNER_IGNORED_AT_START") == "1" {
		signalNotifications() // env-vault subscribes before it may exit by signal
		if ignoredAtStart[syscall.SIGHUP] {
			os.Exit(7)
		}
		os.Exit(8)
	}
	for trap, want := range map[string]int{`trap '' HUP; `: 7, ``: 8} {
		cmd := exec.Command("sh", "-c", trap+`exec "$0" -test.run='^TestIgnoredAtStartSeesAnInheritedIgnoredSignal$'`, os.Args[0])
		cmd.Env = append(os.Environ(), "ENV_VAULT_RUNNER_IGNORED_AT_START=1")
		err := cmd.Run()
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != want {
			t.Fatalf("trap %q: helper err = %v, want exit %d", trap, err, want)
		}
	}
}

func TestExitBySignalEndsTheProcessWithThatSignal(t *testing.T) {
	if os.Getenv("ENV_VAULT_RUNNER_EXIT_BY_SIGNAL") == "1" {
		ExitBySignal(syscall.SIGTERM)
		os.Exit(3)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestExitBySignalEndsTheProcessWithThatSignal$")
	cmd.Env = append(os.Environ(), "ENV_VAULT_RUNNER_EXIT_BY_SIGNAL=1")
	err := cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("helper err = %v, want it killed by a signal", err)
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGTERM {
		t.Fatalf("helper status = %v, want killed by SIGTERM", exitErr.ProcessState)
	}
}
