package config

import (
	"os"
	"path/filepath"
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
