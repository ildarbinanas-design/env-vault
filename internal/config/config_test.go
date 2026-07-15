package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

func TestParseMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		spec    string
		wantErr bool
	}{
		{name: "valid", spec: "nexus-token:NPM_TOKEN"},
		{name: "missing colon", spec: "nexus-token", wantErr: true},
		{name: "empty name", spec: ":NPM_TOKEN", wantErr: true},
		{name: "empty env", spec: "nexus-token:", wantErr: true},
		{name: "invalid env name", spec: "nexus-token:1TOKEN", wantErr: true},
		{name: "extra colon", spec: "nexus-token:NPM:TOKEN", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseMapping(tc.spec)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateSecretNameRejectsTraversalAndKeepsSafeSlashNames(t *testing.T) {
	t.Parallel()
	if err := ValidateSecretName("team/registry/token"); err != nil {
		t.Fatalf("safe slash name: %v", err)
	}
	for _, name := range []string{"../token", "team/../../token", "/token", "team//token", "team/./token", "team/token/"} {
		if err := ValidateSecretName(name); err == nil {
			t.Fatalf("unsafe secret name %q unexpectedly accepted", name)
		}
	}
}

func TestValidateRejectsCaseInsensitiveDuplicateEnvVars(t *testing.T) {
	t.Parallel()
	cfg := Empty()
	cfg.Profiles["dev"] = Profile{Secrets: []SecretMapping{
		{Name: "first", Env: "TOKEN", Required: true},
		{Name: "second", Env: "token", Required: true},
	}}
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "differ only by case") {
		t.Fatalf("Validate error=%v, want case-insensitive duplicate failure", err)
	}
}

func TestLoadSaveRoundTripHasNoSecretValues(t *testing.T) {
	t.Parallel()
	secretValue := testutil.EphemeralValue(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	cfg := Empty()
	cfg.Profiles["dev"] = Profile{
		Description: "local development",
		Secrets: []SecretMapping{{
			Name:     "nexus-token",
			Env:      "NPM_TOKEN",
			Required: true,
		}},
	}
	if err := Save(path, cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	testutil.AssertNotContains(t, "config yaml", string(data), secretValue)
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	mappings := loaded.Profiles["dev"].Secrets
	if len(mappings) != 1 || mappings[0].Name != "nexus-token" || mappings[0].Env != "NPM_TOKEN" || !mappings[0].Required {
		t.Fatalf("unexpected roundtrip: %#v", loaded)
	}
}

func TestInvalidYAMLProducesConfigInvalid(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("version: ["), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatalf("expected error")
	}
	appErr, ok := apperrors.From(err)
	if !ok {
		t.Fatalf("expected AppError, got %T", err)
	}
	if appErr.Code != apperrors.CodeConfigInvalid {
		t.Fatalf("code = %s, want %s", appErr.Code, apperrors.CodeConfigInvalid)
	}
}

func TestSaveRejectsExistingAndDanglingSymlinks(t *testing.T) {
	for _, dangling := range []bool{false, true} {
		name := "existing target"
		if dangling {
			name = "dangling target"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			target := filepath.Join(root, "outside.yaml")
			const sentinel = "outside must not change\n"
			if !dangling {
				if err := os.WriteFile(target, []byte(sentinel), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(root, "config.yaml")
			if err := os.Symlink(target, path); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			cfg := Empty()
			cfg.Profiles["dev"] = Profile{}
			if err := Save(path, cfg); err == nil {
				t.Fatal("Save unexpectedly followed a symlink")
			}
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("config symlink was replaced: info=%v err=%v", info, err)
			}
			if dangling {
				if _, err := os.Stat(target); !os.IsNotExist(err) {
					t.Fatalf("dangling symlink target was created: %v", err)
				}
				return
			}
			data, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != sentinel {
				t.Fatalf("outside target changed: %q", data)
			}
		})
	}
}

func TestConcurrentSavePublishesOnlyCompleteConfigs(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	expected := make(map[string]bool)
	initial := completeConfig("writer-initial")
	expected[initial.Profiles["dev"].Description] = true
	if err := Save(path, initial); err != nil {
		t.Fatalf("initial Save: %v", err)
	}

	const writers = 12
	const writesPerWriter = 4
	for writer := 0; writer < writers; writer++ {
		for iteration := 0; iteration < writesPerWriter; iteration++ {
			expected[fmt.Sprintf("writer-%02d-iteration-%02d", writer, iteration)] = true
		}
	}

	errors := make(chan error, writers*writesPerWriter+1)
	stopReader := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stopReader:
				return
			default:
			}
			loaded, err := Load(path)
			if err != nil {
				errors <- fmt.Errorf("concurrent Load: %w", err)
				return
			}
			description := loaded.Profiles["dev"].Description
			if !expected[description] {
				errors <- fmt.Errorf("observed partial config description %q", description)
				return
			}
		}
	}()

	var wait sync.WaitGroup
	for writer := 0; writer < writers; writer++ {
		writer := writer
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < writesPerWriter; iteration++ {
				description := fmt.Sprintf("writer-%02d-iteration-%02d", writer, iteration)
				if err := Save(path, completeConfig(description)); err != nil {
					errors <- fmt.Errorf("Save(%s): %w", description, err)
					return
				}
			}
		}()
	}
	wait.Wait()
	close(stopReader)
	<-readerDone
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("final Load: %v", err)
	}
	if !expected[loaded.Profiles["dev"].Description] {
		t.Fatalf("final config is incomplete: %#v", loaded)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("config mode=%#o, want 0600", got)
	}
	matches, err := filepath.Glob(filepath.Join(root, ".config.yaml.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary configs remain: %v", matches)
	}
}

func completeConfig(description string) *File {
	cfg := Empty()
	cfg.Profiles["dev"] = Profile{
		Description: description,
		Secrets: []SecretMapping{{
			Name:     "team/registry/token",
			Env:      "REGISTRY_TOKEN",
			Required: true,
		}},
	}
	return cfg
}
