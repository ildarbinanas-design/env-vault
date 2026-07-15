package keyring

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/99designs/keyring"

	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

func TestProductionAllowedBackends(t *testing.T) {
	allowed := productionAllowedBackends()

	for _, backend := range []keyring.BackendType{
		keyring.KeychainBackend,
		keyring.SecretServiceBackend,
		keyring.KWalletBackend,
		keyring.WinCredBackend,
		keyring.PassBackend,
	} {
		if !containsBackend(allowed, backend) {
			t.Fatalf("production allowlist missing %q", backend)
		}
	}

	for _, backend := range []keyring.BackendType{
		keyring.FileBackend,
		keyring.BackendType("test"),
		keyring.BackendType("passwork"),
	} {
		if containsBackend(allowed, backend) {
			t.Fatalf("production allowlist contains disallowed backend %q", backend)
		}
	}
}

func TestPassBackendScopesSafeSlashNames(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pass backend is unavailable on Windows")
	}
	root := t.TempDir()
	passDir := filepath.Join(root, "password-store")
	if err := os.Mkdir(passDir, 0o700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "pass.log")
	passPath := filepath.Join(root, "pass")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_PASS_LOG"
while IFS= read -r line; do :; done
`
	if err := os.WriteFile(passPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_PASS_LOG", logPath)
	store := Store{
		allowedBackends: []keyring.BackendType{keyring.PassBackend},
		passCmd:         passPath,
		passDir:         passDir,
	}
	if err := store.Set(context.Background(), "team/dev", "registry/token", []byte(testutil.EphemeralValue(t))); err != nil {
		t.Fatalf("Set: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(data)), "insert -m -f env-vault/team/dev/registry/token"; got != want {
		t.Fatalf("pass argv=%q, want %q", got, want)
	}
}

func TestKeyringAdapterRejectsTraversalBeforeOpeningBackend(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pass backend is unavailable on Windows")
	}
	root := t.TempDir()
	logPath := filepath.Join(root, "pass.log")
	passPath := filepath.Join(root, "pass")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_PASS_LOG"
exit 99
`
	if err := os.WriteFile(passPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_PASS_LOG", logPath)
	store := Store{
		allowedBackends: []keyring.BackendType{keyring.PassBackend},
		passCmd:         passPath,
		passDir:         root,
	}
	ctx := context.Background()
	fixture := []byte(testutil.EphemeralValue(t))
	checks := []struct {
		name string
		run  func() error
	}{
		{name: "set name", run: func() error { return store.Set(ctx, secretstore.DefaultService, "../../outside", fixture) }},
		{name: "get name", run: func() error { _, err := store.Get(ctx, secretstore.DefaultService, "../../outside"); return err }},
		{name: "delete name", run: func() error { return store.Delete(ctx, secretstore.DefaultService, "../../outside") }},
		{name: "set service", run: func() error { return store.Set(ctx, "../outside", "token", fixture) }},
		{name: "list service", run: func() error { _, err := store.List(ctx, "../outside"); return err }},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.run(); err == nil {
				t.Fatal("unsafe identifier unexpectedly accepted")
			}
		})
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("backend was invoked for an unsafe identifier: %v", err)
	}
}

func TestProductionAllowedBackendsOrder(t *testing.T) {
	allowed := productionAllowedBackends()
	if got := allowed[len(allowed)-1]; got != keyring.PassBackend {
		t.Fatalf("pass backend should be last in production allowlist, got %q", got)
	}
}

func containsBackend(backends []keyring.BackendType, backend keyring.BackendType) bool {
	for _, candidate := range backends {
		if candidate == backend {
			return true
		}
	}
	return false
}
