package tests

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestMain builds the strict GitHub transport and release checker once for the
// integration-test process. Mutation script tests receive the same genuine,
// digest-bound operational projection pair as release workflows.
func TestMain(m *testing.M) {
	directory, err := os.MkdirTemp("", "env-vault-test-transport.")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if os.Getenv("RELEASE_TRANSPORT_BIN") == "" {
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
	}
	projectionFile := os.Getenv("RELEASE_CONTRACT_PROJECTION_FILE")
	versionFile := os.Getenv("RELEASE_CONTRACT_VERSION_FILE")
	if (projectionFile == "") != (versionFile == "") {
		fmt.Fprintln(os.Stderr, "RELEASE_CONTRACT_PROJECTION_FILE and RELEASE_CONTRACT_VERSION_FILE must be supplied together")
		os.Exit(1)
	}
	checker := os.Getenv("RELEASECHECK_BIN")
	if checker == "" {
		checker = filepath.Join(directory, "releasecheck")
		build := exec.Command("go", "build", "-trimpath", "-o", checker, "../cmd/releasecheck")
		build.Stdout = os.Stderr
		build.Stderr = os.Stderr
		if err := build.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "build shared test releasecheck: %v\n", err)
			os.Exit(1)
		}
		if err := os.Setenv("RELEASECHECK_BIN", checker); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if os.Getenv("RELEASE_CONTRACT_CHECKER") == "" {
		if err := os.Setenv("RELEASE_CONTRACT_CHECKER", checker); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if projectionFile == "" {
		versionPath := filepath.Join(directory, "releasecheck-version.json")
		projectionPath := filepath.Join(directory, "release-contract-operational.json")
		commands := []struct {
			path string
			args []string
		}{
			{versionPath, []string{"--contract", "../release/contract.v2.json", "--version", "--json"}},
			{projectionPath, []string{"contract", "operational", "--contract", "../release/contract.v2.json", "--json"}},
		}
		for _, command := range commands {
			output, runErr := exec.Command(checker, command.args...).Output()
			if runErr != nil {
				fmt.Fprintf(os.Stderr, "generate shared typed release contract fixture: %v\n", runErr)
				os.Exit(1)
			}
			if err := os.WriteFile(command.path, output, 0o600); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
		if err := os.Setenv("RELEASE_CONTRACT_VERSION_FILE", versionPath); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := os.Setenv("RELEASE_CONTRACT_PROJECTION_FILE", projectionPath); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	code := m.Run()
	if err := os.RemoveAll(directory); err != nil && code == 0 {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}
