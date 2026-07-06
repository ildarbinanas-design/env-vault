package runner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

func TestChildReceivesEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell assertion uses sh")
	}
	t.Parallel()
	value := testutil.EphemeralValue(t)
	expectedPath := filepath.Join(t.TempDir(), "expected")
	if err := os.WriteFile(expectedPath, []byte(value), 0o600); err != nil {
		t.Fatalf("write expected value: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code, err := (CommandRunner{Stdout: &stdout, Stderr: &stderr}).Run(context.Background(),
		[]string{"sh", "-c", `expected="$(cat "$1")"; test "$ENV_VAULT_RUNNER_TEST" = "$expected"`, "sh", expectedPath},
		[]string{"PATH=/bin:/usr/bin", "ENV_VAULT_RUNNER_TEST=" + value})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	testutil.AssertNotContains(t, "child stdout", stdout.String(), value)
	testutil.AssertNotContains(t, "child stderr", stderr.String(), value)
}

func TestChildExitCodePropagated(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell assertion uses sh")
	}
	t.Parallel()
	code, err := (CommandRunner{}).Run(context.Background(), []string{"sh", "-c", "exit 7"}, []string{"PATH=/bin:/usr/bin"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 7 {
		t.Fatalf("exit code = %d, want 7", code)
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
