package tests

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestMain builds the strict GitHub transport once for the integration-test
// process. Individual scripts still exercise their real launcher and fake gh
// boundary, without paying a Go cold-build for every read.
func TestMain(m *testing.M) {
	if os.Getenv("RELEASE_TRANSPORT_BIN") != "" {
		os.Exit(m.Run())
	}
	directory, err := os.MkdirTemp("", "env-vault-test-transport.")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binary := filepath.Join(directory, "releasetransport")
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "../cmd/releasetransport")
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "build shared test releasetransport: %v\n", err)
		os.Exit(1)
	}
	if err := os.Setenv("RELEASE_TRANSPORT_BIN", binary); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	if err := os.RemoveAll(directory); err != nil && code == 0 {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}
