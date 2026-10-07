package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/bundle"
	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/output"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore/teststore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

func legacyValueBytes(t *testing.T, length int) []byte {
	t.Helper()
	seed := []byte(testutil.EphemeralValue(t))
	defer bundle.Wipe(seed)
	value := make([]byte, length)
	for offset := 0; offset < len(value); {
		offset += copy(value[offset:], seed)
	}
	t.Cleanup(func() { bundle.Wipe(value) })
	return value
}

// Seed below secret set so the new-write rule cannot accidentally hide a
// regression in how existing data is checked, injected or exported.
func seedLegacyValue(t *testing.T, name string, value []byte) secretstore.Store {
	t.Helper()
	store, err := teststore.NewFromEnv("legacy-value-test")
	if err != nil {
		t.Fatal("cannot open isolated legacy store")
	}
	if err := store.Set(context.Background(), secretstore.DefaultService, name, value); err != nil {
		t.Fatal("cannot seed isolated legacy value")
	}
	return store
}

type legacyOperationStore struct {
	secretstore.Store
	gets, exists, sets int
	refuseGet          bool
}

func (s *legacyOperationStore) Get(ctx context.Context, service, name string) ([]byte, error) {
	s.gets++
	if s.refuseGet {
		return nil, secretstore.ErrUnreadable
	}
	return s.Store.Get(ctx, service, name)
}

func (s *legacyOperationStore) Exists(ctx context.Context, service, name string) (bool, error) {
	s.exists++
	return s.Store.Exists(ctx, service, name)
}

func (s *legacyOperationStore) Set(ctx context.Context, service, name string, value []byte) error {
	s.sets++
	return s.Store.Set(ctx, service, name, value)
}

func assertLegacyOutputSafe(t *testing.T, data []byte, values ...[]byte) {
	t.Helper()
	for _, value := range values {
		if len(value) > 0 && bytes.Contains(data, value) {
			t.Fatal("legacy operation output disclosed sensitive material")
		}
	}
}

func TestLegacyLargeSecretCheckDoesNotReadValue(t *testing.T) {
	newTransferEnv(t)
	value := legacyValueBytes(t, secretstore.MaxValueBytes+1)
	store := &legacyOperationStore{Store: seedLegacyValue(t, "legacy", value), refuseGet: true}
	var stdout, stderr bytes.Buffer
	app := newApp(strings.NewReader(""), &stdout, &stderr)
	app.wrapStore = func(secretstore.Store) secretstore.Store { return store }
	code := app.run([]string{"--json", "secret", "check", "legacy"})
	assertLegacyOutputSafe(t, stdout.Bytes(), value)
	assertLegacyOutputSafe(t, stderr.Bytes(), value)
	if code != 0 || store.exists != 1 || store.gets != 0 {
		t.Fatal("secret check read or refused an existing oversized value")
	}
}

func TestLegacyLargeSecretExecPreservesValue(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("requires a native OS permitting an environment value over 64 KiB")
	}
	newTransferEnv(t)
	value := legacyValueBytes(t, secretstore.MaxValueBytes+1)
	seedLegacyValue(t, "legacy", value)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--quiet", "exec", "--clean-env", "--secret", "legacy:ENV_VAULT_LEGACY_VALUE", "--",
		os.Args[0], "-test.run=^TestLegacyLargeExecHelper$", "--", "legacy-value-digest"},
		strings.NewReader(""), &stdout, &stderr)
	assertLegacyOutputSafe(t, stdout.Bytes(), value)
	assertLegacyOutputSafe(t, stderr.Bytes(), value)
	want := fmt.Sprintf("%d %x\n", len(value), sha256.Sum256(value))
	if code != 0 || stdout.String() != want || stderr.Len() != 0 {
		t.Fatal("legacy exec length/digest mismatch")
	}
}

// This child is activated by a nonsecret marker, never a secret in argv. Only
// length and digest are emitted; the ordinary test invocation reads no value.
func TestLegacyLargeExecHelper(t *testing.T) {
	if len(os.Args) < 2 || os.Args[len(os.Args)-2] != "--" || os.Args[len(os.Args)-1] != "legacy-value-digest" {
		return
	}
	value := []byte(os.Getenv("ENV_VAULT_LEGACY_VALUE"))
	fmt.Fprintf(os.Stdout, "%d %x\n", len(value), sha256.Sum256(value))
	bundle.Wipe(value)
	os.Exit(0)
}

func TestLegacyLargeSecretExportPreservesCiphertextButImportRefusesWrites(t *testing.T) {
	env := newTransferEnv(t)
	small := legacyValueBytes(t, 32)
	large := legacyValueBytes(t, secretstore.MaxValueBytes+1)
	passphrase := []byte(testutil.EphemeralValue(t))
	defer bundle.Wipe(passphrase)
	seedLegacyValue(t, "a-small", small)
	seedLegacyValue(t, "z-legacy", large)
	container := filepath.Join(t.TempDir(), "legacy.evb")
	var stdout, stderr bytes.Buffer
	app := newApp(strings.NewReader(""), &stdout, &stderr)
	app.passphraseReader = func(string) ([]byte, error) { return append([]byte(nil), passphrase...), nil }
	code := app.run([]string{"--json", "export", "--out", container})
	assertLegacyOutputSafe(t, stdout.Bytes(), small, large, passphrase)
	assertLegacyOutputSafe(t, stderr.Bytes(), small, large, passphrase)
	if code != 0 {
		t.Fatal("export refused an existing oversized value")
	}
	raw, err := os.ReadFile(container)
	if err != nil {
		t.Fatal("cannot read encrypted legacy container")
	}
	assertLegacyOutputSafe(t, raw, small, large, passphrase)
	payload, err := bundle.Open(raw, passphrase)
	if err != nil {
		t.Fatal("cannot authenticate encrypted legacy container")
	}
	defer func() {
		for _, entry := range payload.Secrets {
			bundle.Wipe(entry.Value)
		}
	}()
	if len(payload.Secrets) != 2 {
		t.Fatal("export changed the legacy entry count")
	}
	want := map[string][]byte{"a-small": small, "z-legacy": large}
	for _, entry := range payload.Secrets {
		if entry.Service != secretstore.DefaultService || !bytes.Equal(entry.Value, want[entry.Name]) {
			t.Fatal("export changed a legacy value")
		}
		delete(want, entry.Name)
	}
	if len(want) != 0 {
		t.Fatal("export omitted a legacy value")
	}

	for _, dryRun := range []bool{false, true} {
		t.Run(fmt.Sprintf("dry-run-%t", dryRun), func(t *testing.T) {
			env.useFreshStore(t)
			store := &legacyOperationStore{}
			stdout.Reset()
			stderr.Reset()
			app := newApp(strings.NewReader(""), &stdout, &stderr)
			app.passphraseReader = func(string) ([]byte, error) { return append([]byte(nil), passphrase...), nil }
			app.wrapStore = func(base secretstore.Store) secretstore.Store { store.Store = base; return store }
			args := []string{"--json", "import", container}
			if dryRun {
				args = append(args, "--dry-run")
			}
			code := app.run(args)
			assertLegacyOutputSafe(t, stdout.Bytes(), small, large, passphrase)
			assertLegacyOutputSafe(t, stderr.Bytes(), small, large, passphrase)
			var envelope output.Envelope
			if json.Unmarshal(stdout.Bytes(), &envelope) != nil || envelope.Error == nil ||
				envelope.Error.Code != apperrors.CodeSecretTooLarge || code != apperrors.ExitUsage {
				t.Fatal("import did not refuse a selected legacy value with SECRET_TOO_LARGE")
			}
			if store.sets != 0 {
				t.Fatal("import wrote a smaller value before refusing a legacy value")
			}
		})
	}
}
