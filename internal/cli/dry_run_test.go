package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/config"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore/teststore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

func TestVersionCommandUsesBuildVersion(t *testing.T) {
	oldVersion := Version
	Version = "v-test"
	t.Cleanup(func() { Version = oldVersion })

	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "version"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"version":"v-test"`) {
		t.Fatalf("stdout does not contain build version: %s", stdout.String())
	}
}

func TestSecretSetDryRunDoesNotStore(t *testing.T) {
	storePath := setupTestBackend(t)
	secretValue := testutil.EphemeralValue(t)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--dry-run", "--json", "secret", "set", "nexus-token", "--stdin"}, strings.NewReader(secretValue+"\n"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	if _, err := os.Stat(storePath); !os.IsNotExist(err) {
		t.Fatalf("test store was created during dry-run: %v", err)
	}
	testutil.AssertNotContains(t, "secret set dry-run stdout", stdout.String(), secretValue)
	testutil.AssertNotContains(t, "secret set dry-run stderr", stderr.String(), secretValue)
}

func TestSecretSetOutputModesDoNotLeakGeneratedValue(t *testing.T) {
	tests := []struct {
		name       string
		baseArgs   []string
		outputFile bool
	}{
		{name: "human"},
		{name: "json", baseArgs: []string{"--json"}},
		{name: "jsonl", baseArgs: []string{"--jsonl"}},
		{name: "json output file", baseArgs: []string{"--json"}, outputFile: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setupTestBackend(t)
			secretValue := testutil.EphemeralValue(t)
			args := append([]string{}, tc.baseArgs...)
			var metaPath string
			if tc.outputFile {
				metaPath = filepath.Join(t.TempDir(), "meta.json")
				args = append(args, "--output", metaPath)
			}
			args = append(args, "secret", "set", "nexus-token", "--stdin")
			var stdout, stderr bytes.Buffer
			code := Run(args, strings.NewReader(secretValue+"\n"), &stdout, &stderr)
			if code != 0 {
				t.Fatalf("code=%d", code)
			}
			testutil.AssertNotContains(t, "secret set stdout", stdout.String(), secretValue)
			testutil.AssertNotContains(t, "secret set stderr", stderr.String(), secretValue)
			if tc.outputFile {
				testutil.AssertFileNotContains(t, "secret set metadata", metaPath, secretValue)
			}
		})
	}
}

func TestSecretSetStructuredErrorDoesNotLeakGeneratedValue(t *testing.T) {
	setupBrokenTestBackend(t)
	secretValue := testutil.EphemeralValue(t)
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--json", "secret", "set", "nexus-token", "--stdin"}, strings.NewReader(secretValue+"\n"), &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected non-zero exit")
	}
	testutil.AssertNotContains(t, "structured error stdout", stdout.String(), secretValue)
	testutil.AssertNotContains(t, "structured error stderr", stderr.String(), secretValue)
}

func TestExecDryRunDoesNotRunChild(t *testing.T) {
	storePath := setupTestBackend(t)
	secretValue := testutil.EphemeralValue(t)
	store, err := teststore.NewFromEnv("test")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if err := store.Set(context.Background(), secretstore.DefaultService, "nexus-token", []byte(secretValue)); err != nil {
		t.Fatalf("set: %v", err)
	}
	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Empty()
	cfg.Profiles["dev"] = config.Profile{
		Secrets: []config.SecretMapping{{Name: "nexus-token", Env: "NPM_TOKEN", Required: true}},
	}
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatalf("config save: %v", err)
	}
	marker := filepath.Join(t.TempDir(), "marker")
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--dry-run", "--json", "--config", cfgPath, "exec", "dev", "--", "sh", "-c", "touch " + marker}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("child appears to have run")
	}
	testutil.AssertNotContains(t, "exec dry-run stdout", stdout.String(), secretValue)
	testutil.AssertNotContains(t, "exec dry-run stderr", stderr.String(), secretValue)
	if _, err := os.Stat(storePath); err != nil {
		t.Fatalf("store should exist after setup: %v", err)
	}
}

func setupTestBackend(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "env-vault-cli-test-")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	storePath := filepath.Join(dir, "store.gob")
	t.Setenv(teststore.BackendEnv, "test")
	t.Setenv(teststore.AllowEnv, "1")
	t.Setenv(teststore.StoreEnv, storePath)
	return storePath
}

func setupBrokenTestBackend(t *testing.T) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "env-vault-cli-test-broken-")
	if err != nil {
		t.Fatalf("mktemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv(teststore.BackendEnv, "test")
	t.Setenv(teststore.AllowEnv, "1")
	t.Setenv(teststore.StoreEnv, dir)
}
