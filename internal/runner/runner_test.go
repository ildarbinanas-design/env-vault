package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

// TestRunnerHelper is re-executed directly so portable tests need no shell.
func TestRunnerHelper(t *testing.T) {
	switch os.Getenv("ENV_VAULT_RUNNER_HELPER") {
	case "digest":
		digest := sha256.Sum256([]byte(os.Getenv("ENV_VAULT_RUNNER_TEST")))
		if hex.EncodeToString(digest[:]) != os.Getenv("ENV_VAULT_RUNNER_DIGEST") {
			os.Exit(8)
		}
		os.Exit(0)
	case "exit":
		os.Exit(7)
	}
}
func TestChildReceivesEnv(t *testing.T) {
	t.Parallel()
	value := testutil.EphemeralValue(t)
	digest := sha256.Sum256([]byte(value))
	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code, err := (CommandRunner{Stdout: &stdout, Stderr: &stderr}).Run(ctx,
		[]string{os.Args[0], "-test.run=^TestRunnerHelper$"},
		append(os.Environ(), "ENV_VAULT_RUNNER_HELPER=digest", "ENV_VAULT_RUNNER_TEST="+value, "ENV_VAULT_RUNNER_DIGEST="+hex.EncodeToString(digest[:])))
	testutil.AssertNotContains(t, "child output", stdout.String()+stderr.String(), value)
	if err != nil || code != 0 {
		t.Fatalf("child exit=%d, error=%v", code, err)
	}
}
func TestChildExitCodePropagated(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var output strings.Builder
	code, err := (CommandRunner{Stdout: &output, Stderr: &output}).Run(ctx, []string{os.Args[0], "-test.run=^TestRunnerHelper$"}, append(os.Environ(), "ENV_VAULT_RUNNER_HELPER=exit"))
	if err != nil || code != 7 {
		t.Fatalf("child exit=%d, error=%v; want 7", code, err)
	}
}

func TestMissingCommandReturns127StructuredError(t *testing.T) {
	t.Parallel()
	err := (CommandRunner{}).Validate([]string{"env-vault-command-that-should-not-exist"})
	if err == nil {
		t.Fatalf("expected error")
	}
	var appErr *apperrors.AppError
	if !errors.As(err, &appErr) {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.ExitCode != apperrors.ExitCommandNotFound {
		t.Fatalf("exit = %d, want %d", appErr.ExitCode, apperrors.ExitCommandNotFound)
	}
}
