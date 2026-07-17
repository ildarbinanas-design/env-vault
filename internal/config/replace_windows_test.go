//go:build windows

package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestTransientConfigFilesystemErrorsOnWindows(t *testing.T) {
	for _, err := range []error{
		windows.ERROR_ACCESS_DENIED,
		windows.ERROR_SHARING_VIOLATION,
		windows.ERROR_LOCK_VIOLATION,
		&os.PathError{Op: "open", Path: "target", Err: windows.ERROR_SHARING_VIOLATION},
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

func TestWindowsConfigReadHandleAllowsAtomicReplacement(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "config.yaml")
	temporary := filepath.Join(root, ".config.yaml.tmp-test")
	oldConfig := []byte("version: 1\nprofiles:\n  dev:\n    description: old-complete-config\n")
	newConfig := []byte("version: 1\nprofiles:\n  dev:\n    description: new-complete-config\n")
	if err := os.WriteFile(target, oldConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(temporary, newConfig, 0o600); err != nil {
		t.Fatal(err)
	}

	heldReader, err := openConfigFileForRead(target)
	if err != nil {
		t.Fatal(err)
	}
	defer heldReader.Close()

	unsafeTarget, err := replaceConfigFileWithRetry(
		temporary,
		target,
		configFilesystemRetryTimeout,
		configFilesystemRetryDelay,
		defaultConfigReplaceOperations(),
	)
	if err != nil || unsafeTarget {
		t.Fatalf("replacement while config read handle is open: unsafe=%v err=%v", unsafeTarget, err)
	}

	heldData, err := io.ReadAll(heldReader)
	if err != nil {
		t.Fatalf("read held config handle: %v", err)
	}
	if string(heldData) != string(oldConfig) {
		t.Fatalf("held handle data=%q, want prior complete config %q", heldData, oldConfig)
	}
	currentData, err := readConfigFileOnce(target)
	if err != nil {
		t.Fatalf("read replaced config: %v", err)
	}
	if string(currentData) != string(newConfig) {
		t.Fatalf("replaced path data=%q, want new complete config %q", currentData, newConfig)
	}
	if _, err := os.Lstat(temporary); !os.IsNotExist(err) {
		t.Fatalf("temporary file still exists after replacement: %v", err)
	}
}

func TestWindowsReplacementRetriesHeldNonDeleteSharedReadHandle(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "config.yaml")
	temporary := filepath.Join(root, ".config.yaml.tmp-test")
	oldConfig := []byte("version: 1\nprofiles:\n  dev:\n    description: old-complete-config\n")
	newConfig := []byte("version: 1\nprofiles:\n  dev:\n    description: new-complete-config\n")
	if err := os.WriteFile(target, oldConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(temporary, newConfig, 0o600); err != nil {
		t.Fatal(err)
	}

	blockingReader, err := openWindowsConfigFileForReadWithShareMode(target, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE)
	if err != nil {
		t.Fatal(err)
	}

	type replacementResult struct {
		unsafeTarget bool
		err          error
		attempts     int
		elapsed      time.Duration
	}
	firstFailure := make(chan error, 1)
	result := make(chan replacementResult, 1)
	started := time.Now()
	go func() {
		operations := defaultConfigReplaceOperations()
		rename := operations.rename
		attempts := 0
		operations.rename = func(temporaryPath, path string) error {
			attempts++
			err := rename(temporaryPath, path)
			if err != nil {
				select {
				case firstFailure <- err:
				default:
				}
			}
			return err
		}
		unsafeTarget, err := replaceConfigFileWithRetry(
			temporary,
			target,
			configFilesystemRetryTimeout,
			configFilesystemRetryDelay,
			operations,
		)
		result <- replacementResult{
			unsafeTarget: unsafeTarget,
			err:          err,
			attempts:     attempts,
			elapsed:      time.Since(started),
		}
	}()

	var firstErr error
	select {
	case firstErr = <-firstFailure:
	case early := <-result:
		_ = blockingReader.Close()
		t.Fatalf("replacement finished before the blocking handle was released: %+v", early)
	case <-time.After(2 * configFilesystemRetryTimeout):
		_ = blockingReader.Close()
		t.Fatal("replacement did not report the first blocked attempt within the bounded retry window")
	}
	closeErr := blockingReader.Close()

	var completed replacementResult
	select {
	case completed = <-result:
	case <-time.After(2 * configFilesystemRetryTimeout):
		t.Fatal("replacement did not complete after the blocking handle was released")
	}
	if closeErr != nil {
		t.Fatalf("release blocking config handle: %v", closeErr)
	}

	var errno syscall.Errno
	if !errors.As(firstErr, &errno) {
		t.Fatalf("first blocked replacement error has no Win32 errno: %T %v", firstErr, firstErr)
	}
	t.Logf(
		"Windows replacement retry evidence: errno=%d (%v) attempts=%d elapsed=%s",
		uintptr(errno),
		errno,
		completed.attempts,
		completed.elapsed,
	)
	if !isTransientConfigFilesystemError(firstErr) {
		t.Fatalf("first blocked replacement error is outside the retry whitelist: errno=%d err=%v", uintptr(errno), firstErr)
	}
	if completed.err != nil || completed.unsafeTarget {
		t.Fatalf("replacement after releasing blocking handle: unsafe=%v err=%v", completed.unsafeTarget, completed.err)
	}
	if completed.attempts < 2 {
		t.Fatalf("replacement attempts=%d, want at least 2", completed.attempts)
	}
	currentData, err := readConfigFileOnce(target)
	if err != nil {
		t.Fatalf("read replaced config: %v", err)
	}
	if string(currentData) != string(newConfig) {
		t.Fatalf("replaced path data=%q, want new complete config %q", currentData, newConfig)
	}
	if _, err := os.Lstat(temporary); !os.IsNotExist(err) {
		t.Fatalf("temporary file still exists after replacement: %v", err)
	}
}

func TestWindowsConfigReadPreservesMissingFileSemantics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	data, err := readConfigFileOnce(path)
	if data != nil {
		t.Fatalf("missing config data=%q, want nil", data)
	}
	if !os.IsNotExist(err) {
		t.Fatalf("read missing config error=%v, want os.IsNotExist", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load missing config: %v", err)
	}
	if cfg.Version != Version || len(cfg.Profiles) != 0 {
		t.Fatalf("Load missing config=%#v, want empty config", cfg)
	}
}

func openWindowsConfigFileForReadWithShareMode(path string, shareMode uint32) (*os.File, error) {
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathPointer,
		windows.GENERIC_READ,
		shareMode,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, windows.ERROR_INVALID_HANDLE
	}
	return file, nil
}
