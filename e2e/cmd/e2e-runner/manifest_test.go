package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCanonicalSuiteBytesNormalizesCheckoutLineEndings(t *testing.T) {
	lf := []byte("package e2e\n\nvar value = 1\n")
	crlf := []byte("package e2e\r\n\r\nvar value = 1\r\n")
	if got := canonicalSuiteBytes(crlf); !bytes.Equal(got, lf) {
		t.Fatalf("canonical CRLF bytes = %q, want %q", got, lf)
	}
	if got := canonicalSuiteBytes(lf); !bytes.Equal(got, lf) {
		t.Fatalf("canonical LF bytes changed: %q", got)
	}
}

func TestSuiteHashCoversSemanticRunnerButNotReporterPin(t *testing.T) {
	repository := t.TempDir()
	write := func(relative, value string) {
		t.Helper()
		filename := filepath.Join(repository, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("e2e/scenarios.json", "{}\n")
	write("e2e/cmd/e2e-runner/semantic.go", "package main\nconst semantic = 1\n")
	write("e2e/cmd/e2e-runner/tooling.go", "package main\nconst (\n\tgotestsumModuleVersion = \"gotest.tools/gotestsum@v1.12.2\"\n\tgotestsumVersion       = \"v1.12.2\"\n)\n")
	baseline, err := suiteHash(repository)
	if err != nil {
		t.Fatal(err)
	}
	write("e2e/cmd/e2e-runner/tooling.go", "package main\nconst (\n\tgotestsumModuleVersion = \"gotest.tools/gotestsum@v9.9.9\"\n\tgotestsumVersion       = \"v9.9.9\"\n)\n")
	toolingChanged, err := suiteHash(repository)
	if err != nil {
		t.Fatal(err)
	}
	if toolingChanged != baseline {
		t.Fatal("reporter-only pin changed semantic suite hash")
	}
	write("e2e/cmd/e2e-runner/tooling.go", "package main\nconst (\n\tgotestsumModuleVersion = \"gotest.tools/gotestsum@v9.9.9\"\n\tgotestsumVersion       = \"v9.9.9\"\n)\nfunc init() { println(\"semantic\") }\n")
	toolingSemanticChanged, err := suiteHash(repository)
	if err != nil {
		t.Fatal(err)
	}
	if toolingSemanticChanged == baseline {
		t.Fatal("semantic code in reporter pin file retained old suite hash")
	}
	write("e2e/cmd/e2e-runner/semantic.go", "package main\nconst semantic = 2\n")
	semanticChanged, err := suiteHash(repository)
	if err != nil {
		t.Fatal(err)
	}
	if semanticChanged == baseline {
		t.Fatal("semantic runner change retained old suite hash")
	}
}

func TestLoadManifestRejectsInvalidIdentityAndPlatforms(t *testing.T) {
	valid := `{"schema_version":1,"sentinel_prefix":"ENV_VAULT_E2E_SENTINEL_","scenarios":[{"id":"A","feature":"f","requirement":"r","go_test":"TestE2E/A","platforms":["linux-amd64"],"critical":true}]}`
	cases := map[string]string{
		"schema":             `{"schema_version":2,"sentinel_prefix":"ENV_VAULT_E2E_SENTINEL_","scenarios":[{"id":"A","feature":"f","requirement":"r","go_test":"TestE2E/A","platforms":["linux-amd64"],"critical":true}]}`,
		"unknown field":      `{"schema_version":1,"sentinel_prefix":"ENV_VAULT_E2E_SENTINEL_","extra":true,"scenarios":[{"id":"A","feature":"f","requirement":"r","go_test":"TestE2E/A","platforms":["linux-amd64"],"critical":true}]}`,
		"unknown platform":   `{"schema_version":1,"sentinel_prefix":"ENV_VAULT_E2E_SENTINEL_","scenarios":[{"id":"A","feature":"f","requirement":"r","go_test":"TestE2E/A","platforms":["plan9-amd64"],"critical":true}]}`,
		"duplicate platform": `{"schema_version":1,"sentinel_prefix":"ENV_VAULT_E2E_SENTINEL_","scenarios":[{"id":"A","feature":"f","requirement":"r","go_test":"TestE2E/A","platforms":["linux-amd64","linux-amd64"],"critical":true}]}`,
		"unknown skip":       `{"schema_version":1,"sentinel_prefix":"ENV_VAULT_E2E_SENTINEL_","scenarios":[{"id":"A","feature":"f","requirement":"r","go_test":"TestE2E/A","platforms":["linux-amd64"],"critical":true,"expected_platform_skips":["plan9-amd64"]}]}`,
	}
	write := func(name, data string) string {
		t.Helper()
		filename := filepath.Join(t.TempDir(), name+".json")
		if err := os.WriteFile(filename, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		return filename
	}
	if _, err := loadManifest(write("valid", valid)); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadManifest(write(name, data)); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
}
