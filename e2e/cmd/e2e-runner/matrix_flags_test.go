package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

func TestParseMatrixFlagsDerivesExactReleaseContractPlatforms(t *testing.T) {
	repositoryRoot, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	contractPath := filepath.Join(repositoryRoot, filepath.FromSlash(releasecontract.CanonicalPath))
	opts, err := parseMatrixFlags([]string{
		"--contract", contractPath,
		"--reports", t.TempDir(),
		"--phase", "candidate",
		"--expected-commit", strings.Repeat("a", 40),
		"--expected-run-id", "42",
		"--expected-run-url", "https://github.com/example/env-vault/actions/runs/42",
		"--expected-run-attempt", "1",
		"--expected-repository", "example/env-vault",
	})
	if err != nil {
		t.Fatal(err)
	}
	contract, err := releasecontract.LoadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	var expected []string
	for _, platform := range contract.Platforms {
		expected = append(expected, platform.ID)
	}
	if opts.required != strings.Join(expected, ",") {
		t.Fatalf("matrix platforms=%q, want release contract %q", opts.required, strings.Join(expected, ","))
	}

	if _, err := parseMatrixFlags([]string{"--required-platforms", "linux-amd64"}); err == nil {
		t.Fatal("removed caller-controlled matrix override was accepted")
	}
}
