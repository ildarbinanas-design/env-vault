package redact_test

import (
	"bytes"
	"testing"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/output"
	"github.com/ildarbinanas-design/env-vault/internal/redact"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

func TestRedactionRemovesSecretFromFormattedOutput(t *testing.T) {
	t.Parallel()
	value := testutil.EphemeralValue(t)
	var stdout bytes.Buffer
	renderer := output.New(&stdout, &bytes.Buffer{}, output.Options{JSON: true}, redact.New(value))
	if err := renderer.Success("doctor", map[string]any{
		"nested": []map[string]string{{"value": value}},
	}, []string{"warning " + value}); err != nil {
		t.Fatalf("success: %v", err)
	}
	testutil.AssertNotContains(t, "formatted output", stdout.String(), value)
}

func TestRedactionRemovesSecretFromStructuredError(t *testing.T) {
	t.Parallel()
	value := testutil.EphemeralValue(t)
	var stdout bytes.Buffer
	renderer := output.New(&stdout, &bytes.Buffer{}, output.Options{JSON: true}, redact.New(value))
	err := apperrors.New("exec", apperrors.CodeRuntimeError, "failed "+value, "remove "+value, apperrors.ExitRuntimeError)
	if writeErr := renderer.Error("exec", err); writeErr != nil {
		t.Fatalf("error: %v", writeErr)
	}
	testutil.AssertNotContains(t, "structured error output", stdout.String(), value)
}
