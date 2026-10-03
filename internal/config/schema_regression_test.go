package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
)

func TestLoadRejectsUnknownYAMLFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, document string }{
		{"root", "version: 1\nprofiles: {}\nunrecognized: true\n"},
		{"profile", "version: 1\nprofiles:\n  dev:\n    unrecognized: true\n"},
		{"mapping", "version: 1\nprofiles:\n  dev:\n    secrets:\n      - name: team/token\n        env: TOKEN\n        required: true\n        unrecognized: true\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.document), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			appErr, ok := apperrors.From(err)
			if err == nil || !ok || appErr.Code != apperrors.CodeConfigInvalid || appErr.ExitCode != apperrors.ExitConfigInvalid {
				t.Fatalf("unknown YAML field: error=%v, want CONFIG_INVALID", err)
			}
			if cfg != nil {
				t.Fatal("invalid schema returned a config")
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != tc.document {
				t.Fatal("rejected config changed")
			}
		})
	}
}

func TestRemoveMappingUsesOnlyEnvOrExactPair(t *testing.T) {
	t.Parallel()
	original := []SecretMapping{
		{Name: "different", Env: "SHARED", Required: true},
		{Name: "SHARED", Env: "OTHER", Required: true},
		{Name: "SHARED", Env: "THIRD", Required: true},
		{Name: "keeper", Env: "KEEP", Required: false},
	}
	for _, tc := range []struct {
		name, selector, env string
		removeIndex         int
		wantErr             bool
	}{
		{"ENV selector", "SHARED", "SHARED", 0, false},
		{"exact pair", "SHARED:OTHER", "OTHER", 1, false},
		{"secret name is not a selector", "different", "", -1, false},
		{"wrong pair", "different:OTHER", "", -1, false},
		{"bare non-ENV name", "team/token", "", -1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			profile := Profile{Secrets: append([]SecretMapping(nil), original...)}
			want := append([]SecretMapping(nil), original...)
			if tc.removeIndex >= 0 {
				want = append(want[:tc.removeIndex], want[tc.removeIndex+1:]...)
			}
			got, env, removed, err := RemoveMapping(profile, tc.selector)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v, wantErr=%v", err, tc.wantErr)
			}
			if env != tc.env || removed != (tc.removeIndex >= 0) {
				t.Fatalf("result env=%q removed=%v, want env=%q removed=%v", env, removed, tc.env, tc.removeIndex >= 0)
			}
			if !reflect.DeepEqual(got.Secrets, want) {
				t.Fatalf("remaining mappings=%#v, want %#v", got.Secrets, want)
			}
		})
	}
}
