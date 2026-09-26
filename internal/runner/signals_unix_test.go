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

// A terminal sends SIGINT and SIGQUIT to the whole foreground process group,
// so forwarding them would deliver each twice. Other signals are forwarded.
func TestForwardSignalsSkipsTerminalInterrupts(t *testing.T) {
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
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = child.Process.Kill()
			t.Fatal("child never became ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	ch := make(chan os.Signal, 4)
	stop := forwardSignals(child.Process, ch)
	ch <- os.Interrupt
	ch <- syscall.SIGQUIT
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
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != "term" {
		t.Fatalf("child received %q, want only term", got)
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
