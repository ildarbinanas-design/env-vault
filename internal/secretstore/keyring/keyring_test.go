package keyring

import (
	"testing"

	"github.com/99designs/keyring"
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
