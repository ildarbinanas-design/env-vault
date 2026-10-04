//go:build unix

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
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
		name       string
		script     string
		code       int
		signal     os.Signal
		signalName string
		message    string
	}{
		{name: "non-zero exit", script: "exit 7", code: 7, message: "Command exited with status 7"},
		{name: "killed by a signal", script: "kill -TERM $$", code: 128 + int(syscall.SIGTERM), signal: syscall.SIGTERM, signalName: "SIGTERM", message: "Command was killed by SIGTERM (status 143)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code, sig := newApp(strings.NewReader(""), &stdout, &stderr).runStatus([]string{"--quiet", "--output", path, "exec", "--", "sh", "-c", tc.script})
			if code != tc.code {
				t.Fatalf("code=%d, want %d; stderr=%s", code, tc.code, stderr.String())
			}
			// RunAndExit ends env-vault with this signal, so the caller sees
			// the same death as without env-vault.
			if sig != tc.signal {
				t.Fatalf("signal=%v, want %v", sig, tc.signal)
			}
			if stdout.Len() != 0 || stderr.Len() != 0 {
				t.Fatalf("stdout=%q stderr=%q, want only the command's own output", stdout.String(), stderr.String())
			}
			env := readEnvelopeFile(t, path)
			data, _ := env.Data.(map[string]any)
			if env.OK || env.Command != "exec" || env.Error == nil || env.Error.Code != apperrors.CodeCommandFailed || env.Error.Message != tc.message || data["exit_code"] != float64(tc.code) {
				t.Fatalf("failed exec envelope: %#v", env)
			}
			if got, _ := data["signal"].(string); got != tc.signalName {
				t.Fatalf("data.signal=%q, want %q", got, tc.signalName)
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

func TestExecMetadataFailurePreservesChildSignal(t *testing.T) {
	setupExecMetadata(t)
	for _, verbose := range []bool{false, true} {
		t.Run(fmt.Sprintf("verbose=%v", verbose), func(t *testing.T) {
			args := []string{"--json", "--quiet", "--output", t.TempDir()}
			if verbose {
				args = append(args, "--verbose")
			}
			args = append(args, "exec", "--", "sh", "-c", "kill -TERM $$")
			var stdout, stderr bytes.Buffer
			code, sig := newApp(strings.NewReader(""), &stdout, &stderr).runStatus(args)
			if code != 128+int(syscall.SIGTERM) || sig != syscall.SIGTERM {
				t.Fatalf("status=%d signal=%v, want SIGTERM", code, sig)
			}
			if stdout.Len() != 0 {
				t.Fatal("metadata failure added stdout after child signal")
			}
			assertExecMetadataStderr(t, stderr.String(), "", verbose)
		})
	}
}
