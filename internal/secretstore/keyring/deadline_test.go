package keyring

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/99designs/keyring"

	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

// refusingKeyring models the macOS Keychain backend after a denied prompt: the
// record is listed, but Get reports it as not found.
type refusingKeyring struct {
	keys  []string
	block chan struct{}
}

func (k refusingKeyring) wait() {
	if k.block != nil {
		<-k.block
	}
}
func (k refusingKeyring) Get(string) (keyring.Item, error) {
	k.wait()
	return keyring.Item{}, keyring.ErrKeyNotFound
}
func (k refusingKeyring) GetMetadata(string) (keyring.Metadata, error) {
	return keyring.Metadata{}, keyring.ErrMetadataNeedsCredentials
}
func (k refusingKeyring) Set(keyring.Item) error { k.wait(); return nil }
func (k refusingKeyring) Remove(string) error    { k.wait(); return nil }
func (k refusingKeyring) Keys() ([]string, error) {
	k.wait()
	return k.keys, nil
}

func storeWith(kr keyring.Keyring) Store {
	return Store{
		unavailableErr: secretstore.ErrUnavailable,
		openKeyring:    func(keyring.Config) (keyring.Keyring, error) { return kr, nil },
	}
}

func TestGetReportsListedButRefusedRecordAsUnreadable(t *testing.T) {
	_, err := storeWith(refusingKeyring{keys: []string{"nexus-token"}}).Get(context.Background(), secretstore.DefaultService, "nexus-token")
	if errors.Is(err, secretstore.ErrNotFound) || !errors.Is(err, secretstore.ErrUnreadable) || !errors.Is(err, secretstore.ErrUnavailable) {
		t.Fatalf("refused record error = %v, want unreadable backend error", err)
	}
	if got := secretstore.BackendRemediation(err); got != secretstore.UnreadableBackendRemediation {
		t.Fatalf("remediation = %q", got)
	}
}

func TestGetReportsUnlistedRecordAsNotFound(t *testing.T) {
	_, err := storeWith(refusingKeyring{}).Get(context.Background(), secretstore.DefaultService, "nexus-token")
	if !errors.Is(err, secretstore.ErrNotFound) {
		t.Fatalf("missing record error = %v, want not found", err)
	}
}

func TestBackendCallsGiveUpAfterDeadline(t *testing.T) {
	old := backendTimeout
	backendTimeout = 20 * time.Millisecond
	t.Cleanup(func() { backendTimeout = old })
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	store := storeWith(refusingKeyring{keys: []string{"nexus-token"}, block: block})
	ctx := context.Background()
	value := []byte(testutil.EphemeralValue(t))
	calls := map[string]func() error{
		"get":    func() error { _, err := store.Get(ctx, secretstore.DefaultService, "nexus-token"); return err },
		"set":    func() error { return store.Set(ctx, secretstore.DefaultService, "nexus-token", value) },
		"exists": func() error { _, err := store.Exists(ctx, secretstore.DefaultService, "nexus-token"); return err },
		"list":   func() error { _, err := store.List(ctx, secretstore.DefaultService); return err },
		"delete": func() error { return store.Delete(ctx, secretstore.DefaultService, "nexus-token") },
	}
	for name, call := range calls {
		start := time.Now()
		err := call()
		if !errors.Is(err, secretstore.ErrTimeout) || !errors.Is(err, secretstore.ErrUnavailable) {
			t.Fatalf("%s error = %v, want timeout backend error", name, err)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("%s took %s", name, elapsed)
		}
		if got := secretstore.BackendRemediation(err); got != secretstore.TimeoutBackendRemediation {
			t.Fatalf("%s remediation = %q", name, got)
		}
	}
}

func TestOpenGivesUpAfterDeadline(t *testing.T) {
	old := backendTimeout
	backendTimeout = 20 * time.Millisecond
	t.Cleanup(func() { backendTimeout = old })
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	store := Store{
		unavailableErr: secretstore.ErrUnavailable,
		openKeyring: func(keyring.Config) (keyring.Keyring, error) {
			<-block
			return nil, errors.New("unreachable")
		},
	}
	if _, err := store.List(context.Background(), secretstore.DefaultService); !errors.Is(err, secretstore.ErrTimeout) {
		t.Fatalf("open error = %v, want timeout", err)
	}
}
