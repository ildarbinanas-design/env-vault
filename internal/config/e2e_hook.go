package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	e2eSaveReadyEnv    = "ENV_VAULT_E2E_CONFIG_SAVE_READY"
	e2eSaveContinueEnv = "ENV_VAULT_E2E_CONFIG_SAVE_CONTINUE"
)

// runE2ESaveCrashHook exposes one deterministic crash window after a temporary
// config has been synced and closed but before it replaces the prior file. It is
// unreachable unless the full insecure test-backend gate is also active, both
// hook paths are explicitly supplied, and every supplied path is below the
// child process's own temporary directory. Normal production environments take
// the first return without filesystem access.
func runE2ESaveCrashHook() error {
	if os.Getenv("ENV_VAULT_BACKEND") != "test" ||
		os.Getenv("ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND") != "1" ||
		os.Getenv("ENV_VAULT_TEST_STORE") == "" {
		return nil
	}
	ready := os.Getenv(e2eSaveReadyEnv)
	continuePath := os.Getenv(e2eSaveContinueEnv)
	if ready == "" && continuePath == "" {
		return nil
	}
	if ready == "" || continuePath == "" {
		return errors.New("both E2E save hook paths are required")
	}
	for label, path := range map[string]string{
		"test store":      os.Getenv("ENV_VAULT_TEST_STORE"),
		"ready marker":    ready,
		"continue marker": continuePath,
	} {
		if err := validateE2ETempPath(path); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
	}

	marker, err := os.OpenFile(ready, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create ready marker: %w", err)
	}
	if _, err := marker.WriteString("ready\n"); err != nil {
		_ = marker.Close()
		return fmt.Errorf("write ready marker: %w", err)
	}
	if err := marker.Sync(); err != nil {
		_ = marker.Close()
		return fmt.Errorf("sync ready marker: %w", err)
	}
	if err := marker.Close(); err != nil {
		return fmt.Errorf("close ready marker: %w", err)
	}

	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		info, err := os.Lstat(continuePath)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
				return errors.New("continue marker must be a regular non-symlink file")
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect continue marker: %w", err)
		}
		select {
		case <-deadline.C:
			return errors.New("timed out waiting for E2E continue marker")
		case <-ticker.C:
		}
	}
}

func validateE2ETempPath(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return errors.New("path must be absolute")
	}
	temp, err := filepath.Abs(filepath.Clean(os.TempDir()))
	if err != nil {
		return err
	}
	candidate, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(temp, candidate)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("path must be a descendant of the process temporary directory")
	}
	resolvedTemp, err := filepath.EvalSymlinks(temp)
	if err != nil {
		return fmt.Errorf("resolve temporary directory: %w", err)
	}
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(candidate))
	if err != nil {
		return fmt.Errorf("resolve path parent: %w", err)
	}
	resolvedCandidate := filepath.Join(resolvedParent, filepath.Base(candidate))
	resolvedRelative, err := filepath.Rel(resolvedTemp, resolvedCandidate)
	if err != nil || resolvedRelative == "." || resolvedRelative == ".." || strings.HasPrefix(resolvedRelative, ".."+string(filepath.Separator)) {
		return errors.New("resolved path must remain below the process temporary directory")
	}
	return nil
}
