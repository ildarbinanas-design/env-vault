package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
)

func TestJSONSuccess(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	renderer := New(&stdout, &stderr, Options{JSON: true})
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
	renderer := New(&stdout, &stderr, Options{JSON: true})
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
	renderer := New(&stdout, &bytes.Buffer{}, Options{JSONL: true})
	if err := renderer.Success("doctor", map[string]any{"backend": "test"}, []string{"warning"}); err != nil {
		t.Fatalf("success: %v", err)
	}
	if got := stdout.String(); !strings.HasSuffix(got, "\n") || strings.Count(got, "\n") != 1 {
		t.Fatalf("expected one jsonl line, got %q", got)
	}
}

func TestQuietSuppressesHumanSuccess(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	renderer := New(&stdout, &bytes.Buffer{}, Options{Quiet: true})
	if err := renderer.Success("version", map[string]any{"version": "test"}, nil); err != nil {
		t.Fatalf("success: %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestHumanVersionLine(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		data map[string]any
		want string
	}{
		{"release", map[string]any{"version": "v0.4.0", "commit": "1fd6638295fb616189e66da7cc110cf4831a3d94", "commit_time": "2026-09-27T10:16:51Z"}, "v0.4.0 (1fd6638, 2026-09-27)\n"},
		{"commit time in another zone", map[string]any{"version": "v0.4.0", "commit": "1fd6638295fb", "commit_time": "2026-09-27T23:30:00-02:00"}, "v0.4.0 (1fd6638, 2026-09-28)\n"},
		{"no commit time", map[string]any{"version": "v0.4.0", "commit": "1fd6638295fb"}, "v0.4.0 (1fd6638)\n"},
		{"no commit", map[string]any{"version": "dev"}, "dev\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			if err := New(&stdout, &bytes.Buffer{}, Options{}).Success("version", tc.data, nil); err != nil {
				t.Fatalf("success: %v", err)
			}
			if stdout.String() != tc.want {
				t.Fatalf("version line=%q, want %q", stdout.String(), tc.want)
			}
		})
	}
}
