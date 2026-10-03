package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/bundle"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore/teststore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

type unreadInput struct{ reads int }

func (input *unreadInput) Read([]byte) (int, error) {
	input.reads++
	return 0, errors.New("input must not be read during validation")
}

type backendGateCase struct {
	name, backend, allow, path string
}

func invalidBackendGates(storePath string) []backendGateCase {
	outsideTemp := filepath.Join(filepath.VolumeName(storePath)+string(os.PathSeparator), "env-vault-outside-temp", "store")
	return []backendGateCase{
		{"backend-only", "test", "", ""},
		{"allow-only", "", "1", ""},
		{"path-only", "", "", storePath},
		{"missing-path", "test", "1", ""},
		{"missing-allow", "test", "", storePath},
		{"missing-backend", "", "1", storePath},
		{"wrong-allow", "test", "true", storePath},
		{"relative-path", "test", "1", "relative-store"},
		{"outside-temp", "test", "1", outsideTemp},
		{"production-with-test-allow", "keyring", "1", storePath},
	}
}

func setBackendGate(t *testing.T, gate backendGateCase) {
	t.Helper()
	t.Setenv(teststore.BackendEnv, gate.backend)
	t.Setenv(teststore.AllowEnv, gate.allow)
	t.Setenv(teststore.StoreEnv, gate.path)
}

func TestSecretSetDryRunValidatesBackendWithoutReadingInput(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "absent", "store")
	cases := append(invalidBackendGates(storePath), backendGateCase{name: "unsupported", backend: "unsupported"})
	for _, gate := range cases {
		t.Run(gate.name, func(t *testing.T) {
			setBackendGate(t, gate)
			var stdout, stderr bytes.Buffer
			input := &unreadInput{}
			app := newApp(input, &stdout, &stderr)
			app.wrapStore = func(secretstore.Store) secretstore.Store {
				t.Fatal("validation opened a command store")
				return nil
			}
			code := app.run([]string{"--json", "--dry-run", "secret", "set", "gate-check", "--stdin"})
			if code != 4 {
				t.Errorf("exit = %d, want 4", code)
			} else {
				assertErrorCode(t, stdout.String(), "BACKEND_UNAVAILABLE")
			}
			if input.reads != 0 {
				t.Errorf("validation read input %d times", input.reads)
			}
			if _, err := os.Stat(filepath.Dir(storePath)); !os.IsNotExist(err) {
				t.Fatal("validation created the store directory")
			}
		})
	}
}

func TestSecretSetDryRunAcceptsSelectionWithoutOpeningBackend(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "absent", "store")
	for _, gate := range []backendGateCase{
		{name: "default"},
		{name: "keyring", backend: "keyring"},
		{name: "pass", backend: "pass"},
		{name: "test", backend: "test", allow: "1", path: storePath},
	} {
		t.Run(gate.name, func(t *testing.T) {
			setBackendGate(t, gate)
			var stdout, stderr bytes.Buffer
			input := &unreadInput{}
			app := newApp(input, &stdout, &stderr)
			app.wrapStore = func(secretstore.Store) secretstore.Store {
				t.Fatal("dry-run opened a command store")
				return nil
			}
			if code := app.run([]string{"--json", "--dry-run", "secret", "set", "gate-check", "--stdin"}); code != 0 {
				t.Fatalf("exit = %d, want 0", code)
			}
			if data := decodeEnvelope(t, stdout.String()); !data.OK || data.Data["dry_run"] != true {
				t.Fatal("expected dry-run success metadata")
			}
			if input.reads != 0 {
				t.Fatalf("dry-run read input %d times", input.reads)
			}
			if _, err := os.Stat(filepath.Dir(storePath)); !os.IsNotExist(err) {
				t.Fatal("dry-run created the store directory")
			}
		})
	}
}

func TestImportRejectsInvalidTestGateBeforeReadingPassphrase(t *testing.T) {
	// Use a valid container. This test checks input-channel authorization,
	// independently of parsing, authentication, and any keychain operation.
	value := []byte(testutil.EphemeralValue(t))
	passphrase := []byte(testutil.EphemeralValue(t))
	defer bundle.Wipe(value)
	defer bundle.Wipe(passphrase)
	raw, err := bundle.Seal(bundle.Payload{Secrets: []bundle.SecretEntry{{Service: secretstore.DefaultService, Name: "gate-check", Value: value}}}, passphrase, bundle.Options{})
	if err != nil {
		t.Fatal("could not create the test container")
	}
	containerPath := filepath.Join(t.TempDir(), "valid.evb")
	if err := os.WriteFile(containerPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(t.TempDir(), "absent", "store")
	for _, gate := range invalidBackendGates(storePath) {
		t.Run(gate.name, func(t *testing.T) {
			setBackendGate(t, gate)
			input := &unreadInput{}
			var stdout, stderr bytes.Buffer
			app := newApp(input, &stdout, &stderr)
			app.wrapStore = func(secretstore.Store) secretstore.Store {
				t.Fatal("invalid gate opened a command store")
				return nil
			}
			code := app.run([]string{"--json", "import", containerPath})
			if code != 4 {
				t.Errorf("exit = %d, want 4", code)
			} else {
				assertErrorCode(t, stdout.String(), "BACKEND_UNAVAILABLE")
			}
			if input.reads != 0 {
				t.Errorf("invalid gate read stdin %d times", input.reads)
			}
			if _, err := os.Stat(filepath.Dir(storePath)); !os.IsNotExist(err) {
				t.Fatal("invalid gate created the store directory")
			}
		})
	}
}
