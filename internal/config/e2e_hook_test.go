package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestE2ESaveCrashHookRequiresFullGateAndTempPaths(t *testing.T) {
	temp := t.TempDir()
	setProcessTempForTest(t, temp)
	store := filepath.Join(temp, "backend", "store.gob")
	if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(temp, "ready")
	continuePath := filepath.Join(temp, "continue")
	if err := os.WriteFile(continuePath, []byte("continue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ENV_VAULT_BACKEND", "test")
	t.Setenv("ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND", "")
	t.Setenv("ENV_VAULT_TEST_STORE", store)
	t.Setenv(e2eSaveReadyEnv, ready)
	t.Setenv(e2eSaveContinueEnv, continuePath)
	if err := runE2ESaveCrashHook(); err != nil {
		t.Fatalf("partial gate should be a no-op: %v", err)
	}
	if _, err := os.Stat(ready); !os.IsNotExist(err) {
		t.Fatalf("partial gate created marker: %v", err)
	}

	t.Setenv("ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND", "1")
	if err := runE2ESaveCrashHook(); err != nil {
		t.Fatalf("fully gated hook failed: %v", err)
	}
	if info, err := os.Lstat(ready); err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		var mode os.FileMode
		if info != nil {
			mode = info.Mode()
		}
		t.Fatalf("ready marker is unsafe: mode=%v err=%v", mode, err)
	}
}

func TestE2ESaveCrashHookRejectsPathOutsideProcessTemp(t *testing.T) {
	temp := t.TempDir()
	setProcessTempForTest(t, temp)
	store := filepath.Join(temp, "backend", "store.gob")
	if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ENV_VAULT_BACKEND", "test")
	t.Setenv("ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND", "1")
	t.Setenv("ENV_VAULT_TEST_STORE", store)
	volumeRoot := filepath.VolumeName(temp) + string(os.PathSeparator)
	t.Setenv(e2eSaveReadyEnv, filepath.Join(volumeRoot, "env-vault-e2e-outside-ready"))
	t.Setenv(e2eSaveContinueEnv, filepath.Join(temp, "continue"))
	if err := runE2ESaveCrashHook(); err == nil {
		t.Fatal("hook accepted a marker outside the process temporary directory")
	}
}

func setProcessTempForTest(t *testing.T, path string) {
	t.Helper()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, path)
	}
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", path)
	}
}
