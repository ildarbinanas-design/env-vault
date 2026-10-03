package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/bundle"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

// Keep the generated value in memory while using the complete test gate.
type exportPublicationStore struct {
	secretstore.Store
	value []byte
}

func (s exportPublicationStore) List(context.Context, string) ([]secretstore.Metadata, error) {
	return []secretstore.Metadata{{Name: "publication-test"}}, nil
}

func (s exportPublicationStore) Get(context.Context, string, string) ([]byte, error) {
	return bytes.Clone(s.value), nil
}

func TestExportPreservesFileCreatedDuringPassphrasePrompt(t *testing.T) {
	newTransferEnv(t)
	value := []byte(testutil.EphemeralValue(t))
	defer bundle.Wipe(value)
	passphrase := []byte(testutil.EphemeralValue(t))
	defer bundle.Wipe(passphrase)
	path := filepath.Join(t.TempDir(), "vault.evb")
	previous := []byte("another process owns this file")
	var stdout, stderr bytes.Buffer
	app := newApp(strings.NewReader(""), &stdout, &stderr)
	app.wrapStore = func(store secretstore.Store) secretstore.Store {
		return exportPublicationStore{Store: store, value: value}
	}
	prompts := 0
	app.passphraseReader = func(string) ([]byte, error) {
		prompts++
		if prompts == 1 {
			if err := os.WriteFile(path, previous, 0o600); err != nil {
				t.Fatalf("create concurrent file: %v", err)
			}
		}
		return bytes.Clone(passphrase), nil
	}
	code := app.run([]string{"export", "--out", path})
	for _, output := range [][]byte{stdout.Bytes(), stderr.Bytes()} {
		if bytes.Contains(output, value) || bytes.Contains(output, passphrase) {
			t.Fatal("export output contains generated sensitive bytes")
		}
	}
	if prompts != 2 {
		t.Fatalf("prompt calls = %d, want 2", prompts)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read concurrent file: %v", err)
	}
	if !bytes.Equal(got, previous) {
		t.Error("export replaced a file created during the passphrase prompt")
	}
	if code != 2 {
		t.Fatalf("export exit = %d, want 2", code)
	}
	assertErrorCode(t, stderr.String(), "USAGE")
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatalf("read output directory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatal("failed export left a temporary file")
	}
}
