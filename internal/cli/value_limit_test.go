package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/bundle"
	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

// limitedStore gives a store the value limit of Windows Credential Manager:
// it reports the limit and, like the real backend, refuses a larger value.
type limitedStore struct {
	secretstore.Store
	limit int
}

func (s limitedStore) MaxValueBytes() int { return s.limit }

func (s limitedStore) Set(ctx context.Context, service, name string, value []byte) error {
	if s.limit > 0 && len(value) > s.limit {
		return secretstore.ErrValueTooLarge
	}
	return s.Store.Set(ctx, service, name, value)
}

func TestRefuseOversizedValues(t *testing.T) {
	entries := []bundle.SecretEntry{
		{Service: secretstore.DefaultService, Name: "small", Value: make([]byte, 4)},
		{Service: secretstore.DefaultService, Name: "large", Value: make([]byte, 5)},
	}
	err := refuseOversizedValues(limitedStore{limit: 4}, entries)
	appErr, ok := apperrors.From(err)
	if !ok || appErr.Code != apperrors.CodeSecretTooLarge || appErr.ExitCode != apperrors.ExitUsage || appErr.Message != "Secret large is larger than the backend stores" {
		t.Fatalf("err=%v, want SECRET_TOO_LARGE for large", err)
	}
	for name, store := range map[string]secretstore.Store{
		"a limit every value fits": limitedStore{limit: 5},
		"no known limit":           limitedStore{limit: 0},
		"a store without limits":   struct{ secretstore.Store }{},
	} {
		if err := refuseOversizedValues(store, entries); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// import must refuse a value the backend cannot store before it writes any
// secret, and --dry-run must not report success for such a container. A value
// that --on-conflict skip leaves alone is not a reason to refuse.
func TestImportRefusesAnOversizedValueBeforeAnyWrite(t *testing.T) {
	env := newTransferEnv(t)
	small := testutil.EphemeralValue(t)
	large := testutil.EphemeralValue(t) + testutil.EphemeralValue(t)
	limit := len(small) + 1
	if len(large) <= limit {
		t.Fatalf("the large value must exceed the limit")
	}
	setSecret(t, "a-small", small)
	setSecret(t, "b-large", large)
	container := filepath.Join(t.TempDir(), "vault.evb")
	mustRunCLI(t, "", transferPassphrase, "export", "--out", container)
	env.useFreshStore(t)

	runLimited := func(args ...string) (int, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		app := newApp(strings.NewReader(""), &stdout, &stderr)
		app.passphraseReader = func(string) ([]byte, error) { return []byte(transferPassphrase), nil }
		app.wrapStore = func(store secretstore.Store) secretstore.Store {
			return limitedStore{Store: store, limit: limit}
		}
		return app.run(args), stdout.String() + stderr.String()
	}

	for _, args := range [][]string{
		{"--json", "--dry-run", "import", container},
		{"--json", "import", container},
	} {
		code, output := runLimited(args...)
		if code != apperrors.ExitUsage {
			t.Fatalf("%v: exit %d, want %d", args, code, apperrors.ExitUsage)
		}
		assertErrorCode(t, output, apperrors.CodeSecretTooLarge)
		if _, ok := storedValue(t, secretstore.DefaultService, "a-small"); ok {
			t.Fatalf("%v wrote a-small before refusing b-large", args)
		}
	}

	// With b-large already stored, skip leaves it alone, so the import fits.
	kept := testutil.EphemeralValue(t)
	setSecret(t, "b-large", kept)
	if code, output := runLimited("--json", "import", container, "--on-conflict", "skip"); code != 0 {
		t.Fatalf("import --on-conflict skip: exit %d, output %s", code, output)
	}
	if got, ok := storedValue(t, secretstore.DefaultService, "a-small"); !ok || !bytes.Equal(got, []byte(small)) {
		t.Fatal("import --on-conflict skip did not restore a-small")
	}
	if got, _ := storedValue(t, secretstore.DefaultService, "b-large"); !bytes.Equal(got, []byte(kept)) {
		t.Fatal("import --on-conflict skip replaced b-large")
	}
}
