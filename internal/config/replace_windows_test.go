//go:build windows

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestTransientConfigFilesystemErrorsOnWindows(t *testing.T) {
	for _, err := range []error{
		windows.ERROR_ACCESS_DENIED,
		windows.ERROR_SHARING_VIOLATION,
		windows.ERROR_LOCK_VIOLATION,
		&os.LinkError{Op: "rename", Old: "temporary", New: "target", Err: windows.ERROR_SHARING_VIOLATION},
	} {
		if !isTransientConfigFilesystemError(err) {
			t.Fatalf("error %v should be retryable", err)
		}
	}
	if isTransientConfigFilesystemError(fmt.Errorf("unrelated failure")) {
		t.Fatal("unrelated failure should not be retryable")
	}
}

func TestE2EWindowsReplaceFailureInjectionRequiresEveryGate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nprofiles: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gates := map[string]string{
		"ENV_VAULT_BACKEND":                     "test",
		"ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND": "1",
		"ENV_VAULT_TEST_STORE":                  filepath.Join(t.TempDir(), "store.gob"),
		"ENV_VAULT_E2E_CHILD_MARKER":            "public-marker",
		"GOCOVERDIR":                            t.TempDir(),
	}
	for missing := range gates {
		t.Run("missing_"+missing, func(t *testing.T) {
			for name, value := range gates {
				t.Setenv(name, value)
			}
			t.Setenv(missing, "")
			inject, err := e2eWindowsReplaceFailureMode(path, false)
			if err != nil || inject {
				t.Fatalf("injection enabled without %s", missing)
			}
		})
	}
	for name, value := range gates {
		t.Setenv(name, value)
	}
	if inject, err := e2eWindowsReplaceFailureMode(path, false); inject || !errors.Is(err, errE2EWindowsCoverageIdentity) {
		t.Fatalf("uninstrumented full gate inject=%v err=%v", inject, err)
	}
	if inject, err := e2eWindowsReplaceFailureMode(path, true); !inject || err != nil {
		t.Fatalf("instrumented full gate inject=%v err=%v", inject, err)
	}
	if inject, err := e2eWindowsReplaceFailureMode(filepath.Join(t.TempDir(), "missing.yaml"), true); inject || err != nil {
		t.Fatalf("missing config inject=%v err=%v", inject, err)
	}
	t.Setenv("ENV_VAULT_TEST_STORE", filepath.Join(filepath.Dir(os.TempDir()), "outside-store.gob"))
	if inject, err := e2eWindowsReplaceFailureMode(path, true); inject || err != nil {
		t.Fatalf("out-of-scope store inject=%v err=%v", inject, err)
	}
}

func TestReplaceConfigFileExercisesGatedE2EWindowsRetry(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "config.yaml")
	temporary := filepath.Join(root, ".config.yaml.tmp-test")
	if err := os.WriteFile(target, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(temporary, []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"ENV_VAULT_BACKEND":                     "test",
		"ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND": "1",
		"ENV_VAULT_TEST_STORE":                  filepath.Join(root, "store.gob"),
		"ENV_VAULT_E2E_CHILD_MARKER":            "public-marker",
		"GOCOVERDIR":                            filepath.Join(root, "coverage"),
	} {
		t.Setenv(name, value)
	}
	unsafeTarget, err := replaceConfigFileForBuild(temporary, target, false)
	if unsafeTarget || !errors.Is(err, errE2EWindowsCoverageIdentity) {
		t.Fatalf("uninstrumented replace unsafe=%v err=%v", unsafeTarget, err)
	}
	oldData, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(oldData) != "old\n" {
		t.Fatalf("uninstrumented gate changed target content=%q", oldData)
	}
	if _, err := os.Lstat(temporary); err != nil {
		t.Fatalf("uninstrumented gate removed temporary source: %v", err)
	}
	unsafeTarget, err = replaceConfigFileForBuild(temporary, target, true)
	if err != nil || unsafeTarget {
		t.Fatalf("replace result unsafe=%v err=%v", unsafeTarget, err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new\n" {
		t.Fatalf("target content=%q, want new config", data)
	}
	if _, err := os.Lstat(temporary); !os.IsNotExist(err) {
		t.Fatalf("temporary file still exists after replacement: %v", err)
	}
}
