package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/redact"
)

func TestJSONSuccess(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	renderer := New(&stdout, &stderr, Options{JSON: true}, redact.New())
	if err := renderer.Success("secret_set", map[string]any{"name": "nexus-token"}, nil); err != nil {
		t.Fatalf("success: %v", err)
	}
	var env Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("json: %v", err)
	}
	if !env.OK || env.Command != "secret_set" || env.Error != nil {
		t.Fatalf("unexpected envelope: %#v", env)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestJSONError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	renderer := New(&stdout, &stderr, Options{JSON: true}, redact.New())
	appErr := apperrors.New("exec", apperrors.CodeMissingSecret, "Missing secret: nexus-token", "Run: env-vault secret set nexus-token", apperrors.ExitMissingSecret)
	if err := renderer.Error("exec", appErr); err != nil {
		t.Fatalf("error: %v", err)
	}
	var env Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("json: %v", err)
	}
	if env.OK || env.Error == nil || env.Error.Code != apperrors.CodeMissingSecret {
		t.Fatalf("unexpected envelope: %#v", env)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestJSONLEvent(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	renderer := New(&stdout, &bytes.Buffer{}, Options{JSONL: true}, redact.New())
	if err := renderer.Success("doctor", map[string]any{"backend": "test"}, []string{"warning"}); err != nil {
		t.Fatalf("success: %v", err)
	}
	if got := stdout.String(); !strings.HasSuffix(got, "\n") || strings.Count(got, "\n") != 1 {
		t.Fatalf("expected one jsonl line, got %q", got)
	}
}

func TestJSONAndJSONLSingleEnvelopeCompatibility(t *testing.T) {
	t.Parallel()
	fixed := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	var jsonOutput, jsonlOutput bytes.Buffer
	jsonRenderer := New(&jsonOutput, &bytes.Buffer{}, Options{JSON: true}, redact.New())
	jsonlRenderer := New(&jsonlOutput, &bytes.Buffer{}, Options{JSONL: true}, redact.New())
	jsonRenderer.now = func() time.Time { return fixed }
	jsonlRenderer.now = func() time.Time { return fixed }
	data := map[string]any{"version": "v1.2.3"}
	if err := jsonRenderer.Success("version", data, nil); err != nil {
		t.Fatal(err)
	}
	if err := jsonlRenderer.Success("version", data, nil); err != nil {
		t.Fatal(err)
	}
	if jsonOutput.String() != jsonlOutput.String() || strings.Count(jsonOutput.String(), "\n") != 1 {
		t.Fatalf("single-envelope JSON/JSONL contract diverged: json=%q jsonl=%q", jsonOutput.String(), jsonlOutput.String())
	}
}

func TestQuietSuppressesHumanSuccess(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	renderer := New(&stdout, &bytes.Buffer{}, Options{Quiet: true}, redact.New())
	if err := renderer.Success("version", map[string]any{"version": "test"}, nil); err != nil {
		t.Fatalf("success: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
