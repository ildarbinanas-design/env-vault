package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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
	clear(passphrase)
	clear(confirmation)
	for _, value := range values {
		clear(value)
	}
	t.Log("v0.4.2 export imported correctly for default and named services")
}
