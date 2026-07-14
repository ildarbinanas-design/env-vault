package runner

import (
	"context"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/config"
	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

type fakeStore struct {
	values map[string][]byte
}

func (s fakeStore) Set(context.Context, string, string, []byte) error { return nil }

func (s fakeStore) Get(_ context.Context, _ string, name string) ([]byte, error) {
	value, ok := s.values[name]
	if !ok {
		return nil, secretstore.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s fakeStore) Exists(_ context.Context, _ string, name string) (bool, error) {
	_, ok := s.values[name]
	return ok, nil
}

func (s fakeStore) Delete(context.Context, string, string) error { return nil }

func (s fakeStore) List(context.Context, string) ([]secretstore.Metadata, error) { return nil, nil }

func TestResolveProfileMapping(t *testing.T) {
	t.Parallel()
	secretName := "nexus-token"
	envName := "NPM_TOKEN"
	value := testutil.EphemeralValue(t)
	result, err := Resolve(context.Background(), fakeStore{values: map[string][]byte{secretName: []byte(value)}},
		[]config.SecretMapping{{Name: secretName, Env: envName, Required: true}}, nil,
		ResolveOptions{CurrentEnv: []string{"PATH=/bin"}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !containsEnvValue(result.Env, value) {
		t.Fatalf("env missing generated value")
	}
}

func TestResolveDirectSecret(t *testing.T) {
	t.Parallel()
	secretName := "nexus-token"
	envName := "NPM_TOKEN"
	value := testutil.EphemeralValue(t)
	result, err := Resolve(context.Background(), fakeStore{values: map[string][]byte{secretName: []byte(value)}},
		nil, []config.SecretMapping{{Name: secretName, Env: envName, Required: true}},
		ResolveOptions{CurrentEnv: []string{"PATH=/bin"}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(result.Secrets) != 1 || result.Secrets[0].Env != envName {
		t.Fatalf("unexpected resolved metadata")
	}
	if !containsEnvValue(result.Env, value) {
		t.Fatalf("env missing generated value")
	}
}

func TestResolveCombinedMappings(t *testing.T) {
	t.Parallel()
	firstName := "nexus-token"
	secondName := "service-token"
	firstValue := testutil.EphemeralValue(t)
	secondValue := testutil.EphemeralValue(t)
	store := fakeStore{values: map[string][]byte{firstName: []byte(firstValue), secondName: []byte(secondValue)}}
	result, err := Resolve(context.Background(), store,
		[]config.SecretMapping{{Name: firstName, Env: "NPM_TOKEN", Required: true}},
		[]config.SecretMapping{{Name: secondName, Env: "SERVICE_TOKEN", Required: true}},
		ResolveOptions{CurrentEnv: []string{"PATH=/bin"}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !containsEnvValue(result.Env, firstValue) || !containsEnvValue(result.Env, secondValue) {
		t.Fatalf("env missing generated values")
	}
}

func TestResolveMissingSecret(t *testing.T) {
	t.Parallel()
	_, err := Resolve(context.Background(), fakeStore{values: map[string][]byte{}},
		[]config.SecretMapping{{Name: "missing", Env: "MISSING", Required: true}}, nil,
		ResolveOptions{CurrentEnv: []string{"PATH=/bin"}})
	if err == nil {
		t.Fatalf("expected error")
	}
	var appErr *apperrors.AppError
	if !stderrors.As(err, &appErr) || appErr.Code != apperrors.CodeMissingSecret {
		t.Fatalf("unexpected error: %T %v", err, err)
	}
}

func TestResolveEnvCollision(t *testing.T) {
	t.Parallel()
	secretName := "nexus-token"
	envName := "NPM_TOKEN"
	value := testutil.EphemeralValue(t)
	_, err := Resolve(context.Background(), fakeStore{values: map[string][]byte{secretName: []byte(value)}},
		[]config.SecretMapping{{Name: secretName, Env: envName, Required: true}}, nil,
		ResolveOptions{CurrentEnv: []string{envName + "=" + testutil.EphemeralValue(t)}})
	if err == nil {
		t.Fatalf("expected collision")
	}
	var appErr *apperrors.AppError
	if !stderrors.As(err, &appErr) || appErr.Code != apperrors.CodeEnvCollision {
		t.Fatalf("unexpected error: %T %v", err, err)
	}
}

func TestResolveEnvCollisionIsCaseInsensitive(t *testing.T) {
	t.Parallel()
	secretName := "nexus-token"
	value := testutil.EphemeralValue(t)
	_, err := Resolve(context.Background(), fakeStore{values: map[string][]byte{secretName: []byte(value)}},
		[]config.SecretMapping{{Name: secretName, Env: "PATH", Required: true}}, nil,
		ResolveOptions{CurrentEnv: []string{"Path=/windows/system32"}})
	if err == nil {
		t.Fatal("expected case-insensitive collision")
	}
	var appErr *apperrors.AppError
	if !stderrors.As(err, &appErr) || appErr.Code != apperrors.CodeEnvCollision {
		t.Fatalf("unexpected error: %T %v", err, err)
	}
}

func TestResolveRejectsCaseInsensitiveDuplicateMappings(t *testing.T) {
	t.Parallel()
	firstName := "first-token"
	secondName := "second-token"
	store := fakeStore{values: map[string][]byte{
		firstName:  []byte(testutil.EphemeralValue(t)),
		secondName: []byte(testutil.EphemeralValue(t)),
	}}
	_, err := Resolve(context.Background(), store,
		[]config.SecretMapping{{Name: firstName, Env: "TOKEN", Required: true}},
		[]config.SecretMapping{{Name: secondName, Env: "token", Required: true}},
		ResolveOptions{CurrentEnv: []string{"PATH=/bin"}})
	if err == nil {
		t.Fatal("expected case-insensitive duplicate mapping failure")
	}
	var appErr *apperrors.AppError
	if !stderrors.As(err, &appErr) || appErr.Code != apperrors.CodeConfigInvalid {
		t.Fatalf("unexpected error: %T %v", err, err)
	}
}

func TestResolveOverrideEnv(t *testing.T) {
	t.Parallel()
	secretName := "nexus-token"
	envName := "NPM_TOKEN"
	value := testutil.EphemeralValue(t)
	result, err := Resolve(context.Background(), fakeStore{values: map[string][]byte{secretName: []byte(value)}},
		[]config.SecretMapping{{Name: secretName, Env: envName, Required: true}}, nil,
		ResolveOptions{CurrentEnv: []string{envName + "=" + testutil.EphemeralValue(t)}, OverrideEnv: true})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !containsEnvValue(result.Env, value) {
		t.Fatalf("override missing generated value")
	}
}

func TestResolveOverrideReplacesAllCaseVariants(t *testing.T) {
	t.Parallel()
	secretName := "nexus-token"
	value := testutil.EphemeralValue(t)
	result, err := Resolve(context.Background(), fakeStore{values: map[string][]byte{secretName: []byte(value)}},
		[]config.SecretMapping{{Name: secretName, Env: "PATH", Required: true}}, nil,
		ResolveOptions{
			CurrentEnv:  []string{"Path=/windows/system32", "PATH=/bin", "HOME=/home/test"},
			OverrideEnv: true,
		})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	pathEntries := 0
	for _, item := range result.Env {
		name, gotValue, ok := strings.Cut(item, "=")
		if ok && strings.EqualFold(name, "PATH") {
			pathEntries++
			if gotValue != value {
				t.Fatalf("PATH value was not replaced")
			}
		}
	}
	if pathEntries != 1 {
		t.Fatalf("case-variant PATH entries=%d, want 1: %v", pathEntries, result.Env)
	}
}

func TestDryRunDoesNotInjectSecretValue(t *testing.T) {
	t.Parallel()
	secretName := "nexus-token"
	value := testutil.EphemeralValue(t)
	result, err := Resolve(context.Background(), fakeStore{values: map[string][]byte{secretName: []byte(value)}},
		[]config.SecretMapping{{Name: secretName, Env: "NPM_TOKEN", Required: true}}, nil,
		ResolveOptions{CurrentEnv: []string{"PATH=/bin"}, DryRun: true})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if strings.Join(result.Env, "\n") != "PATH=/bin" {
		t.Fatalf("dry-run env changed")
	}
	testutil.AssertNotContains(t, "dry-run env", strings.Join(result.Env, "\n"), value)
}

func containsEnvValue(env []string, value string) bool {
	suffix := "=" + value
	for _, got := range env {
		if strings.HasSuffix(got, suffix) {
			return true
		}
	}
	return false
}
