//go:build linux

package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

func TestAggregateEnvironmentReportsE2BIG(t *testing.T) {
	seed := testutil.EphemeralValue(t)
	value := strings.Repeat(seed, 32768/len(seed)+1)[:32768]
	// Each string fits Linux's per-string limit; together these exceed even
	// its largest aggregate allowance (6 MiB with an unlimited stack).
	env := make([]string, 512)
	for i := range env {
		env[i] = fmt.Sprintf("ENV_VAULT_SIZE_%d=%s", i, value)
	}
	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	code, err := (CommandRunner{Stdout: &stdout, Stderr: &stderr}).Run(ctx, []string{os.Args[0], "-test.run=^TestRunnerHelper$"}, env)
	testutil.AssertNotContains(t, "aggregate start output", stdout.String()+stderr.String(), seed)
	appErr, ok := apperrors.From(err)
	if !ok || code != apperrors.ExitRuntimeError || appErr.Code != apperrors.CodeRuntimeError || appErr.Message != "Unable to start command" || appErr.Remediation != "The environment exceeds the operating system limit; reduce the size or number of secrets" {
		t.Fatal("aggregate environment limit lost its structured error mapping")
	}
	if !errors.Is(err, syscall.E2BIG) {
		t.Fatal("aggregate environment did not produce a wrapped E2BIG")
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatal("child emitted output despite start failure")
	}
}
