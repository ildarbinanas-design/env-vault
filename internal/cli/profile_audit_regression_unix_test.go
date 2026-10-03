//go:build darwin || linux

package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestProfileCreateDryRunRejectsFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.fifo")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	// Run in a bounded subprocess: a regression must fail instead of leaving
	// a goroutine blocked forever opening a FIFO with no writer.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestProfileDryRunFIFOHelper$", "--", path)
	child.Env = append(os.Environ(), "ENV_VAULT_PROFILE_FIFO_HELPER=1")
	output, err := child.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("dry-run blocked on a FIFO; child was killed and reaped")
	}
	if err != nil {
		t.Fatalf("FIFO child: %v\n%s", err, output)
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("FIFO changed: info=%v err=%v", info, err)
	}
}

func TestProfileDryRunFIFOHelper(t *testing.T) {
	if os.Getenv("ENV_VAULT_PROFILE_FIFO_HELPER") != "1" {
		return
	}
	if len(os.Args) < 3 || os.Args[len(os.Args)-2] != "--" {
		t.Fatal("missing FIFO argument")
	}
	assertProfileDryRunInvalidTarget(t, os.Args[len(os.Args)-1])
}
