package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/bundle"
	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

type identityTestStore struct {
	secretstore.Store
	validate     func([]secretstore.Metadata) error
	sets, checks int
	failCheck    int
}

func (s *identityTestStore) ValidateIdentities(records []secretstore.Metadata) error {
	return s.validate(records)
}
func (s *identityTestStore) Set(ctx context.Context, service, name string, value []byte) error {
	s.sets++
	return s.Store.Set(ctx, service, name, value)
}
func (s *identityTestStore) Exists(ctx context.Context, service, name string) (bool, error) {
	s.checks++
	if s.checks == s.failCheck {
		return false, secretstore.ErrUnavailable
	}
	return s.Store.Exists(ctx, service, name)
}
func checkImportPreflight(t *testing.T, identities []secretstore.Metadata, validate func([]secretstore.Metadata) error, failCheck int, wantCode string) {
	t.Helper()
	for _, policy := range []string{"fail", "skip", "overwrite"} {
		t.Run(policy, func(t *testing.T) {
			newTransferEnv(t)
			value := []byte(testutil.EphemeralValue(t))
			passphrase := []byte(testutil.EphemeralValue(t))
			payload := bundle.Payload{}
			for _, identity := range identities {
				payload.Secrets = append(payload.Secrets, bundle.SecretEntry{Service: identity.Service, Name: identity.Name, Value: value})
			}
			raw, err := bundle.Seal(payload, passphrase, bundle.Options{})
			if err != nil {
				t.Fatal("cannot create preflight container")
			}
			path := filepath.Join(t.TempDir(), "transfer.evb")
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer
			app := newApp(strings.NewReader(""), &stdout, &stderr)
			app.passphraseReader = func(string) ([]byte, error) { return bytes.Clone(passphrase), nil }
			store := &identityTestStore{validate: validate, failCheck: failCheck}
			app.wrapStore = func(base secretstore.Store) secretstore.Store { store.Store = base; return store }
			code := app.run([]string{"--json", "import", path, "--on-conflict", policy})
			for _, secret := range [][]byte{value, passphrase} {
				if bytes.Contains(stdout.Bytes(), secret) || bytes.Contains(stderr.Bytes(), secret) {
					t.Fatal("preflight output leaked sensitive material")
				}
			}
			if code == 0 {
				t.Fatal("preflight unexpectedly succeeded")
			}
			assertErrorCode(t, stdout.String(), wantCode)
			if store.sets != 0 {
				t.Fatalf("preflight failure wrote %d records", store.sets)
			}
		})
	}
}
func TestImportPreflightRejectsIdentityAndMetadataFailuresBeforeWrites(t *testing.T) {
	records := []secretstore.Metadata{{Service: "team", Name: "one"}, {Service: "team", Name: "two"}}
	for _, failure := range []error{secretstore.ErrIdentityCollision, secretstore.ErrUnavailable} {
		code := apperrors.CodeBackendUnavailable
		if errors.Is(failure, secretstore.ErrIdentityCollision) {
			code = apperrors.CodeBundleInvalid
		}
		checkImportPreflight(t, records, func([]secretstore.Metadata) error { return failure }, 0, code)
	}
	checkImportPreflight(t, records, func([]secretstore.Metadata) error { return nil }, 2, apperrors.CodeBackendUnavailable)
}
