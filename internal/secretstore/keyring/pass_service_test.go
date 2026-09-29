package keyring

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/99designs/keyring"

	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

func TestPassStoreRefusesServiceWithSlash(t *testing.T) {
	store := Store{
		allowedBackends: []keyring.BackendType{keyring.PassBackend},
		openKeyring: func(cfg keyring.Config) (keyring.Keyring, error) {
			t.Errorf("a backend was opened with %v", cfg.AllowedBackends)
			return nil, keyring.ErrNoAvailImpl
		},
	}
	// "team/ci" + "tok" and "team" + "ci/tok" would both be env-vault/team/ci/tok.
	err := store.Set(context.Background(), "team/ci", "tok", []byte(testutil.EphemeralValue(t)))
	if !errors.Is(err, secretstore.ErrPassServiceSlash) {
		t.Fatalf("Set with service team/ci: err=%v, want ErrPassServiceSlash", err)
	}
}

func TestSlashedServiceNeverFallsBackToPass(t *testing.T) {
	ctx := context.Background()
	// production is the Linux order in which only pass is available.
	production := []keyring.BackendType{keyring.SecretServiceBackend, keyring.KWalletBackend, keyring.PassBackend}
	var calls [][]keyring.BackendType
	store := Store{
		allowedBackends: production,
		unavailableErr:  secretstore.ErrUnavailable,
		openKeyring: func(cfg keyring.Config) (keyring.Keyring, error) {
			calls = append(calls, slices.Clone(cfg.AllowedBackends))
			if slices.Contains(cfg.AllowedBackends, keyring.PassBackend) {
				return &memoryKeyring{items: map[string][]byte{}}, nil
			}
			return nil, keyring.ErrNoAvailImpl
		},
	}

	if _, err := store.Exists(ctx, "team", "ci/tok"); err != nil || len(calls) != 1 || !slices.Equal(calls[0], production) {
		t.Fatalf("service without a slash: err=%v calls=%v, want one open with pass allowed", err, calls)
	}

	calls = nil
	_, err := store.Exists(ctx, "team/ci", "tok")
	if !errors.Is(err, secretstore.ErrPassServiceSlash) {
		t.Fatalf("service with a slash when only pass is available: err=%v, want ErrPassServiceSlash", err)
	}
	if got := secretstore.BackendRemediation(err); got != secretstore.PassServiceRemediation {
		t.Fatalf("remediation=%q", got)
	}
	if len(calls) == 0 || slices.Contains(calls[0], keyring.PassBackend) {
		t.Fatalf("the first open for team/ci allowed pass: %v", calls)
	}

	// When no backend opens at all, the error stays an unavailable backend.
	store.openKeyring = func(keyring.Config) (keyring.Keyring, error) { return nil, keyring.ErrNoAvailImpl }
	_, err = store.Exists(ctx, "team/ci", "tok")
	if errors.Is(err, secretstore.ErrPassServiceSlash) || !errors.Is(err, secretstore.ErrUnavailable) {
		t.Fatalf("no backend at all: err=%v, want an unavailable backend", err)
	}
}
