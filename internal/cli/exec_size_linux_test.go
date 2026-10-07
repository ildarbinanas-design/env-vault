//go:build linux

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/bundle"
	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore/teststore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

func TestExecLegacyOversizedEnvironmentReportsE2BIG(t *testing.T) {
	newTransferEnv(t)
	store, err := teststore.NewFromEnv("test")
	if err != nil {
		t.Fatal(err)
	}
	seed := testutil.EphemeralValue(t)
	value := []byte(strings.Repeat(seed, 131072/len(seed)+1)[:131072])
	defer bundle.Wipe(value)
	if err := store.Set(context.Background(), secretstore.DefaultService, "legacy", value); err != nil {
		t.Fatal("unable to seed disposable legacy value")
	}
	marker := filepath.Join(t.TempDir(), "started")
	var stdout, stderr bytes.Buffer
	args := []string{"--json", "--config", filepath.Join(t.TempDir(), "config.yaml"), "exec", "--clean-env", "--secret", "legacy:EXAMPLE", "--", os.Args[0], "-test.run=^TestExecMetadataChild$", "--", "silent", marker, "", ""}
	code := Run(args, strings.NewReader(""), &stdout, &stderr)
	output := stdout.String() + stderr.String()
	testutil.AssertNotContains(t, "E2BIG output", output, seed)
	assertExecMetadataError(t, code, stdout.Bytes(), apperrors.ExitRuntimeError, apperrors.CodeRuntimeError)
	if !strings.Contains(output, "Unable to start command") || !strings.Contains(output, "The environment exceeds the operating system limit; reduce the size or number of secrets") {
		t.Fatal("E2BIG response lost its message or remediation")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("child started despite oversized environment")
	}
}
