package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/output"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore/teststore"
)

func TestPassBackendRefusesServiceWithSlash(t *testing.T) {
	t.Setenv(teststore.BackendEnv, "pass")
	for _, args := range [][]string{
		{"--dry-run", "--json", "secret", "set", "tok", "--service", "team/ci"},
		{"--json", "secret", "check", "tok", "--service", "team/ci"},
		{"--dry-run", "--json", "export", "--out", "unused.evb", "--with-services", "team/ci"},
	} {
		var stdout, stderr bytes.Buffer
		code := Run(args, strings.NewReader(""), &stdout, &stderr)
		var env output.Envelope
		if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
			t.Fatalf("%v: json: %v (stdout=%s)", args, err, stdout.String())
		}
		if code != apperrors.ExitUsage || env.Error == nil || env.Error.Code != apperrors.CodeUsage || env.Error.Remediation != secretstore.PassServiceRemediation {
			t.Fatalf("%v: code=%d envelope=%#v", args, code, env)
		}
	}
}
