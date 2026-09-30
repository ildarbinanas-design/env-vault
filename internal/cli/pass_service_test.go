package cli

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/bundle"
	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/output"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore/teststore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

func TestPassBackendRefusesServiceWithSlash(t *testing.T) {
	t.Setenv(teststore.BackendEnv, "pass")
	for _, args := range [][]string{
		{"--dry-run", "--json", "secret", "set", "tok", "--service", "team/ci"},
		{"--json", "secret", "check", "tok", "--service", "team/ci"},
		{"--dry-run", "--json", "secret", "delete", "tok", "--confirm", "tok", "--service", "team/ci"},
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

func TestPassImportRefusesSlashedServiceBeforeStoreAccess(t *testing.T) {
	passphrase := []byte(testutil.EphemeralValue(t))
	value := []byte(testutil.EphemeralValue(t))
	defer clear(passphrase)
	defer clear(value)
	name := "token-" + rand.Text()
	service := "team/ci-" + rand.Text()
	raw, err := bundle.Seal(bundle.Payload{Secrets: []bundle.SecretEntry{
		{Service: secretstore.DefaultService, Name: name, Value: value},
		{Service: service, Name: name, Value: value},
	}}, passphrase, bundle.Options{})
	if err != nil {
		t.Fatal("seal synthetic container failed")
	}
	container := filepath.Join(t.TempDir(), "vault.evb")
	if err := os.WriteFile(container, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(teststore.BackendEnv, "pass")
	for _, dryRun := range []bool{false, true} {
		t.Run(map[bool]string{false: "write", true: "dry-run"}[dryRun], func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			app := newApp(strings.NewReader(""), &stdout, &stderr)
			app.passphraseReader = func(string) ([]byte, error) {
				return append([]byte(nil), passphrase...), nil
			}
			app.wrapStore = func(secretstore.Store) secretstore.Store {
				t.Fatal("import opened the store before refusing a slashed service")
				return nil
			}
			args := []string{"--json", "import", container}
			if dryRun {
				args = append(args, "--dry-run")
			}
			code := app.run(args)
			for _, sensitive := range [][]byte{passphrase, value} {
				testutil.AssertNotContains(t, "import stdout", stdout.String(), string(sensitive))
				testutil.AssertNotContains(t, "import stderr", stderr.String(), string(sensitive))
			}
			var env output.Envelope
			if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
				t.Fatal("import did not render a JSON envelope")
			}
			if code != apperrors.ExitUsage || env.OK || env.Error == nil || env.Error.Code != apperrors.CodeUsage || env.Error.Remediation != secretstore.PassServiceRemediation || !strings.Contains(env.Error.Message, service) {
				t.Fatal("import did not report the slashed service as a usage error with pass remediation")
			}
		})
	}
}
