package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/bundle"
	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/output"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore/teststore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

// The CI script fetches and checks the fixed baseline outside go test. Ordinary
// unit tests never depend on the network or on installed historical binaries.
func TestHistoricalTransfer(t *testing.T) {
	if os.Getenv("ENV_VAULT_HISTORICAL_TEST") != "1" {
		t.Skip("requires pinned historical CLI scenario")
	}
	oldCLI, currentCLI := os.Getenv("ENV_VAULT_BASELINE_CLI"), os.Getenv("ENV_VAULT_CURRENT_CLI")
	if oldCLI == "" || currentCLI == "" {
		t.Fatal("historical scenario has no binaries")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	root := t.TempDir()
	newTransferEnv(t)
	passphrase := []byte(testutil.EphemeralValue(t))
	values := [][]byte{[]byte(testutil.EphemeralValue(t)), []byte(testutil.EphemeralValue(t))}
	oldStore, newStore := filepath.Join(root, "old-store"), filepath.Join(root, "new-store")
	t.Setenv(teststore.StoreEnv, newStore)
	run := func(binary, store string, input []byte, args ...string) {
		t.Helper()
		command := exec.CommandContext(ctx, binary, args...)
		command.Env = append(os.Environ(), teststore.StoreEnv+"="+store)
		command.Dir = root
		command.Stdin = bytes.NewReader(input)
		output, err := command.CombinedOutput()
		for _, value := range append(values, passphrase) {
			if bytes.Contains(output, value) {
				t.Fatal("historical CLI output leaked sensitive material")
			}
		}
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				t.Fatalf("historical CLI exit=%d", exit.ExitCode())
			}
			t.Fatal("cannot run historical CLI")
		}
	}
	services := []string{secretstore.DefaultService, "historical-team"}
	for i, service := range services {
		run(oldCLI, oldStore, values[i], "secret", "set", "token", "--service", service, "--stdin")
	}
	container := filepath.Join(root, "transfer.evb")
	// The fully gated stdin reader reuses one input for both prompts.
	confirmation := append(append([]byte{}, passphrase...), '\n')
	run(oldCLI, oldStore, confirmation, "export", "--with-services", services[1], "--out", container)
	run(currentCLI, newStore, append(append([]byte{}, passphrase...), '\n'), "import", container)
	store, err := teststore.NewFromEnv("historical-transfer")
	if err != nil {
		t.Fatal("cannot open isolated import destination")
	}
	for i, service := range services {
		got, err := store.Get(ctx, service, "token")
		if err != nil || !bytes.Equal(got, values[i]) {
			t.Fatal("historical transfer changed a stored value")
		}
		clear(got)
	}
	t.Log("v0.4.2 export imported correctly for default and named services")

	// A historical writer remains capable of creating an otherwise valid v1
	// container with a value that the new write policy intentionally refuses.
	// Seed below either CLI's secret-set boundary, so this does not rely on the
	// removed acceptance behavior of current secret set.
	large := legacyValueBytes(t, secretstore.MaxValueBytes+1)
	values = append(values, large)
	t.Setenv(teststore.StoreEnv, oldStore)
	seedLegacyValue(t, "a-small", values[0])
	seedLegacyValue(t, "z-legacy", large)
	largeContainer := filepath.Join(root, "historical-large.evb")
	run(oldCLI, oldStore, confirmation, "export", "--with-services", services[1], "--out", largeContainer)
	raw, err := os.ReadFile(largeContainer)
	if err != nil {
		t.Fatal("cannot read historical oversized container")
	}
	assertLegacyOutputSafe(t, raw, large, passphrase)
	payload, err := bundle.Open(raw, passphrase)
	if err != nil {
		t.Fatal("historical oversized container is not authentic current-format input")
	}
	found := false
	for _, entry := range payload.Secrets {
		if entry.Service == secretstore.DefaultService && entry.Name == "z-legacy" {
			found = bytes.Equal(entry.Value, large)
		}
		bundle.Wipe(entry.Value)
	}
	if !found {
		t.Fatal("historical export changed or omitted the oversized value")
	}
	for _, dryRun := range []bool{false, true} {
		t.Run(map[bool]string{false: "oversized-import", true: "oversized-dry-run"}[dryRun], func(t *testing.T) {
			destination := filepath.Join(t.TempDir(), "destination")
			args := []string{"--json", "import", largeContainer}
			if dryRun {
				args = append(args, "--dry-run")
			}
			command := exec.CommandContext(ctx, currentCLI, args...)
			command.Env = append(os.Environ(), teststore.StoreEnv+"="+destination)
			command.Dir = root
			command.Stdin = bytes.NewReader(confirmation)
			data, err := command.CombinedOutput()
			assertLegacyOutputSafe(t, data, values[0], values[1], large, passphrase)
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != apperrors.ExitUsage {
				t.Fatal("current CLI did not refuse a historical oversized import")
			}
			var envelope output.Envelope
			if json.Unmarshal(data, &envelope) != nil || envelope.Error == nil || envelope.Error.Code != apperrors.CodeSecretTooLarge {
				t.Fatal("historical oversized import did not report SECRET_TOO_LARGE")
			}
			if _, err := os.Stat(destination); !os.IsNotExist(err) {
				t.Fatal("historical oversized import created a destination store")
			}
		})
	}
	t.Log("v0.4.2 oversized export authenticated; selected import and dry-run refused before creating a store")
	clear(passphrase)
	clear(confirmation)
	for _, value := range values {
		clear(value)
	}
}
