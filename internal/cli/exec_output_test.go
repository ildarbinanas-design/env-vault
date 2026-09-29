//go:build unix

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/output"
)

func TestExecRecordsAFailedCommandInTheOutputFile(t *testing.T) {
	setupTestBackend(t)
	path := filepath.Join(t.TempDir(), "meta.json")
	// A successful run leaves ok:true behind, and a failed run must replace it.
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"--quiet", "--output", path, "exec", "--", "sh", "-c", "exit 0"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("successful exec code=%d stderr=%s", code, stderr.String())
	}
	if env := readEnvelopeFile(t, path); !env.OK || env.Command != "exec" {
		t.Fatalf("successful exec envelope: %#v", env)
	}
	for _, tc := range []struct {
		name   string
		script string
		code   int
		signal string
	}{
		{name: "non-zero exit", script: "exit 7", code: 7},
		{name: "killed by a signal", script: "kill -TERM $$", code: 128 + int(syscall.SIGTERM), signal: "terminated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Run([]string{"--quiet", "--output", path, "exec", "--", "sh", "-c", tc.script}, strings.NewReader(""), &stdout, &stderr)
			if code != tc.code {
				t.Fatalf("code=%d, want %d; stderr=%s", code, tc.code, stderr.String())
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("stdout=%q stderr=%q, want only the command's own output", stdout.String(), stderr.String())
			}
			env := readEnvelopeFile(t, path)
			data, _ := env.Data.(map[string]any)
			if env.OK || env.Command != "exec" || env.Error == nil || env.Error.Code != apperrors.CodeCommandFailed || data["exit_code"] != float64(tc.code) {
				t.Fatalf("failed exec envelope: %#v", env)
			}
			if got, _ := data["signal"].(string); got != tc.signal {
				t.Fatalf("signal=%q, want %q", got, tc.signal)
			}
		})
	}
}

func readEnvelopeFile(t *testing.T, path string) output.Envelope {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var env output.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return env
}
