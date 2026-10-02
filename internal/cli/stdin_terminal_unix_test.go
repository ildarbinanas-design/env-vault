//go:build darwin || linux

package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/output"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

func TestSecretSetStdinRefusesATerminal(t *testing.T) {
	storePath := setupTestBackend(t)
	controller, terminal := openPseudoTerminal(t)
	// Type a value and end of input (^D), so a regression that reads the
	// terminal stores the value and fails below instead of waiting forever.
	secretValue := testutil.EphemeralValue(t)
	if _, err := controller.Write([]byte(secretValue + "\n\x04")); err != nil {
		t.Fatalf("type into the pseudo-terminal: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "secret", "set", "nexus-token", "--stdin"}, terminal, &stdout, &stderr)
	testutil.AssertNotContains(t, "stdout", stdout.String(), secretValue)
	testutil.AssertNotContains(t, "stderr", stderr.String(), secretValue)
	if code != apperrors.ExitUsage {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var env output.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("json: %v", err)
	}
	if env.OK || env.Error == nil || env.Error.Code != apperrors.CodeUsage || !strings.Contains(env.Error.Message, "stdin is a terminal") {
		t.Fatalf("unexpected envelope: %#v", env)
	}
	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Fatalf("the backend was written: %v", err)
	}
}
