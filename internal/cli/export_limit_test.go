package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/bundle"
	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

// The complete test-backend gate remains active. This wrapper keeps the large
// generated values in memory; none is written to the test backend's files.
type exportSizeStore struct {
	secretstore.Store
	value []byte
}

func (s exportSizeStore) List(context.Context, string) ([]secretstore.Metadata, error) {
	return []secretstore.Metadata{{Name: "first"}, {Name: "second"}}, nil
}

func (s exportSizeStore) Get(context.Context, string, string) ([]byte, error) {
	return bytes.Clone(s.value), nil
}

func TestExportRefusesOversizedContainerWithoutWriting(t *testing.T) {
	newTransferEnv(t)
	value := make([]byte, 13<<19) // Two values exceed the ciphertext cap together.
	if _, err := rand.Read(value); err != nil {
		t.Fatalf("generate ephemeral value: %v", err)
	}
	defer bundle.Wipe(value)
	encodedPrefix := []byte(base64.StdEncoding.EncodeToString(value[:24]))
	defer bundle.Wipe(encodedPrefix)
	passphrase := []byte(testutil.EphemeralValue(t))
	defer bundle.Wipe(passphrase)

	for _, test := range []struct {
		name     string
		existing bool
	}{
		{"new destination", false},
		{"force existing destination", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "vault.evb")
			previous := []byte("existing destination")
			if test.existing {
				if err := os.WriteFile(path, previous, 0o600); err != nil {
					t.Fatalf("write existing destination: %v", err)
				}
			}
			var stdout, stderr bytes.Buffer
			app := newApp(strings.NewReader(""), &stdout, &stderr)
			app.passphraseReader = func(string) ([]byte, error) { return bytes.Clone(passphrase), nil }
			app.wrapStore = func(store secretstore.Store) secretstore.Store {
				return exportSizeStore{Store: store, value: value}
			}
			args := []string{"--json", "--verbose", "export", "--out", path}
			if test.existing {
				args = append(args, "--force")
			}
			if code := app.run(args); code != apperrors.ExitConfigInvalid {
				t.Fatalf("export exit = %d, want %d", code, apperrors.ExitConfigInvalid)
			}
			var response struct {
				OK      bool   `json:"ok"`
				Command string `json:"command"`
				Error   struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
				t.Fatal("export did not return a JSON error envelope")
			}
			if response.OK || response.Command != "export" || response.Error.Code != apperrors.CodeBundleInvalid {
				t.Fatal("export did not return BUNDLE_INVALID for the oversized container")
			}
			for _, output := range [][]byte{stdout.Bytes(), stderr.Bytes()} {
				if bytes.Contains(output, passphrase) || bytes.Contains(output, value[:32]) || bytes.Contains(output, encodedPrefix) {
					t.Fatal("export output contains generated sensitive bytes")
				}
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatalf("read output directory: %v", err)
			}
			if !test.existing {
				if len(entries) != 0 {
					t.Fatal("failed export created an output or temporary file")
				}
				return
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read existing destination: %v", err)
			}
			if len(entries) != 1 || !bytes.Equal(got, previous) {
				t.Fatal("failed export replaced the existing destination or left a temporary file")
			}
		})
	}
}
