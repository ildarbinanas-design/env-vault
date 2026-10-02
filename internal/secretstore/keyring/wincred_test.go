package keyring

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"testing"

	"github.com/99designs/keyring"

	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
)

// memoryKeyring keeps items in memory and counts writes.
type memoryKeyring struct {
	items map[string][]byte
	sets  int
}

func (k *memoryKeyring) Get(key string) (keyring.Item, error) {
	data, ok := k.items[key]
	if !ok {
		return keyring.Item{}, keyring.ErrKeyNotFound
	}
	return keyring.Item{Key: key, Data: data}, nil
}
func (k *memoryKeyring) GetMetadata(string) (keyring.Metadata, error) {
	return keyring.Metadata{}, keyring.ErrMetadataNeedsCredentials
}
func (k *memoryKeyring) Set(item keyring.Item) error {
	k.sets++
	k.items[item.Key] = item.Data
	return nil
}
func (k *memoryKeyring) Remove(key string) error {
	delete(k.items, key)
	return nil
}
func (k *memoryKeyring) Keys() ([]string, error) {
	keys := make([]string, 0, len(k.items))
	for key := range k.items {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys, nil
}

func winCredStore(kr keyring.Keyring, winCred bool) Store {
	return Store{
		unavailableErr: secretstore.ErrUnavailable,
		openKeyring:    func(keyring.Config) (keyring.Keyring, error) { return kr, nil },
		winCredCheck:   &winCred,
	}
}

func TestWinCredRefusesAValueLargerThanItStores(t *testing.T) {
	ctx := context.Background()
	kr := &memoryKeyring{items: map[string][]byte{}}
	store := winCredStore(kr, true)
	if got := store.MaxValueBytes(); got != 2560 {
		t.Fatalf("MaxValueBytes()=%d, want 2560", got)
	}
	err := store.Set(ctx, secretstore.DefaultService, "large", make([]byte, winCredMaxValueBytes+1))
	if !errors.Is(err, secretstore.ErrValueTooLarge) || errors.Is(err, secretstore.ErrUnavailable) {
		t.Fatalf("Set of %d bytes: err=%v, want ErrValueTooLarge", winCredMaxValueBytes+1, err)
	}
	if kr.sets != 0 {
		t.Fatalf("the backend was written %d times", kr.sets)
	}
	if err := store.Set(ctx, secretstore.DefaultService, "largest", make([]byte, winCredMaxValueBytes)); err != nil || kr.sets != 1 {
		t.Fatalf("Set of %d bytes: err=%v, writes=%d", winCredMaxValueBytes, err, kr.sets)
	}
	// Other backends keep no such limit.
	other := winCredStore(kr, false)
	if got := other.MaxValueBytes(); got != 0 {
		t.Fatalf("MaxValueBytes()=%d without WinCred, want 0", got)
	}
	if err := other.Set(ctx, secretstore.DefaultService, "large", make([]byte, winCredMaxValueBytes+1)); err != nil {
		t.Fatalf("Set without WinCred: %v", err)
	}
}

// Only the default store on Windows writes to Credential Manager; the limit
// and the case rule must never reach macOS Keychain or pass.
func TestUsesWinCredOnlyForTheDefaultStoreOnWindows(t *testing.T) {
	windows := runtime.GOOS == "windows"
	for name, store := range map[string]Store{"default": New(), "zero": {}} {
		if got := store.usesWinCred(); got != windows {
			t.Fatalf("%s store: usesWinCred()=%v on %s", name, got, runtime.GOOS)
		}
	}
	if NewPass().usesWinCred() {
		t.Fatal("the pass store uses Credential Manager")
	}
}
