package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/flock"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
)

const (
	transactionHelperEnv     = "ENV_VAULT_TRANSACTION_HELPER"
	transactionHelperPathEnv = "ENV_VAULT_TRANSACTION_CONFIG"
	transactionHelperNameEnv = "ENV_VAULT_TRANSACTION_PROFILE"
	transactionStartedEnv    = "ENV_VAULT_TRANSACTION_STARTED"
	transactionEnteredEnv    = "ENV_VAULT_TRANSACTION_ENTERED"
	transactionReleaseEnv    = "ENV_VAULT_TRANSACTION_RELEASE"
)

func TestTransactionSerializesInterprocessUpdates(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.yaml")
	if err := Save(path, Empty()); err != nil {
		t.Fatalf("initial Save: %v", err)
	}

	firstStarted := filepath.Join(root, "first-started")
	firstEntered := filepath.Join(root, "first-entered")
	firstRelease := filepath.Join(root, "first-release")
	secondStarted := filepath.Join(root, "second-started")
	secondEntered := filepath.Join(root, "second-entered")

	var firstOutput bytes.Buffer
	first := transactionHelperCommand(path, "worker-one", firstStarted, firstEntered, firstRelease)
	first.Stdout = &firstOutput
	first.Stderr = &firstOutput
	if err := first.Start(); err != nil {
		t.Fatalf("start first helper: %v", err)
	}

	var secondOutput bytes.Buffer
	second := transactionHelperCommand(path, "worker-two", secondStarted, secondEntered, "")
	second.Stdout = &secondOutput
	second.Stderr = &secondOutput
	t.Cleanup(func() {
		_ = os.WriteFile(firstRelease, []byte("release\n"), 0o600)
		for _, command := range []*exec.Cmd{first, second} {
			if command.Process != nil && command.ProcessState == nil {
				_ = command.Process.Kill()
			}
		}
	})

	if err := waitForTransactionSignal(firstEntered, 3*time.Second); err != nil {
		t.Fatalf("first helper did not enter transaction: %v\n%s", err, firstOutput.String())
	}
	if err := second.Start(); err != nil {
		t.Fatalf("start second helper: %v", err)
	}
	if err := waitForTransactionSignal(secondStarted, 3*time.Second); err != nil {
		t.Fatalf("second helper did not start: %v\n%s", err, secondOutput.String())
	}

	blockedUntil := time.Now().Add(750 * time.Millisecond)
	for time.Now().Before(blockedUntil) {
		if _, err := os.Stat(secondEntered); err == nil {
			t.Fatalf("second process entered while first held the transaction lock\nfirst: %s\nsecond: %s", firstOutput.String(), secondOutput.String())
		} else if !os.IsNotExist(err) {
			t.Fatalf("inspect second transaction signal: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := os.WriteFile(firstRelease, []byte("release\n"), 0o600); err != nil {
		t.Fatalf("release first helper: %v", err)
	}
	if err := first.Wait(); err != nil {
		t.Fatalf("first helper: %v\n%s", err, firstOutput.String())
	}
	if err := second.Wait(); err != nil {
		t.Fatalf("second helper: %v\n%s", err, secondOutput.String())
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("final Load: %v", err)
	}
	for _, profile := range []string{"worker-one", "worker-two"} {
		if _, ok := loaded.Profiles[profile]; !ok {
			t.Fatalf("lost interprocess update for %q: %#v", profile, loaded.Profiles)
		}
	}
}

func TestTransactionHelperProcess(t *testing.T) {
	if os.Getenv(transactionHelperEnv) != "1" {
		return
	}
	path := os.Getenv(transactionHelperPathEnv)
	profile := os.Getenv(transactionHelperNameEnv)
	started := os.Getenv(transactionStartedEnv)
	entered := os.Getenv(transactionEnteredEnv)
	release := os.Getenv(transactionReleaseEnv)
	if path == "" || profile == "" || started == "" || entered == "" {
		t.Fatal("transaction helper environment is incomplete")
	}
	if err := os.WriteFile(started, []byte("started\n"), 0o600); err != nil {
		t.Fatalf("write started signal: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	if err := Transaction(ctx, path, func(cfg *File) (bool, error) {
		if err := os.WriteFile(entered, []byte("entered\n"), 0o600); err != nil {
			return false, err
		}
		if release != "" {
			if err := waitForTransactionSignal(release, 3*time.Second); err != nil {
				return false, err
			}
		}
		cfg.Profiles[profile] = Profile{}
		return true, nil
	}); err != nil {
		t.Fatalf("transaction helper: %v", err)
	}
}

func TestTransactionTimeoutReturnsStructuredLockedError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	lock := flock.New(transactionLockPath(path), flock.SetPermissions(0o600))
	locked, err := lock.TryLock()
	if err != nil || !locked {
		t.Fatalf("hold lock: locked=%v err=%v", locked, err)
	}
	t.Cleanup(func() { _ = lock.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	called := false
	started := time.Now()
	err = Transaction(ctx, path, func(cfg *File) (bool, error) {
		called = true
		return true, nil
	})
	if called {
		t.Fatal("mutation callback ran without acquiring the lock")
	}
	if err == nil {
		t.Fatal("Transaction unexpectedly acquired a held lock")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("lock timeout was not bounded: %s", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error does not preserve context deadline: %v", err)
	}
	appErr, ok := apperrors.From(err)
	if !ok {
		t.Fatalf("error type=%T, want AppError", err)
	}
	if appErr.Code != apperrors.CodeConfigLocked || appErr.ExitCode != apperrors.ExitConfigInvalid {
		t.Fatalf("lock error code=%q exit=%d", appErr.Code, appErr.ExitCode)
	}
	if appErr.Message != "Timed out waiting for config lock" || !strings.Contains(appErr.Remediation, "Retry") {
		t.Fatalf("lock error contract=%#v", appErr)
	}
}

func TestTransactionKeepsStablePrivateLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := Transaction(context.Background(), path, func(cfg *File) (bool, error) {
		cfg.Profiles["dev"] = Profile{}
		return true, nil
	}); err != nil {
		t.Fatalf("first Transaction: %v", err)
	}
	lockPath := transactionLockPath(path)
	firstInfo, err := os.Lstat(lockPath)
	if err != nil {
		t.Fatalf("stat persistent lock: %v", err)
	}
	if !firstInfo.Mode().IsRegular() {
		t.Fatalf("lock mode=%v, want regular", firstInfo.Mode())
	}
	if runtime.GOOS != "windows" && firstInfo.Mode().Perm() != 0o600 {
		t.Fatalf("lock permissions=%#o, want 0600", firstInfo.Mode().Perm())
	}

	if err := Transaction(context.Background(), path, func(cfg *File) (bool, error) {
		return false, nil
	}); err != nil {
		t.Fatalf("second Transaction: %v", err)
	}
	secondInfo, err := os.Lstat(lockPath)
	if err != nil {
		t.Fatalf("stat reused lock: %v", err)
	}
	if !os.SameFile(firstInfo, secondInfo) {
		t.Fatal("transaction replaced the stable lock inode")
	}
}

func TestTransactionRepairsLockPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows file modes do not model POSIX 0600 permissions")
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	lockPath := transactionLockPath(path)
	if err := os.WriteFile(lockPath, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lockPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Transaction(context.Background(), path, func(cfg *File) (bool, error) {
		return false, nil
	}); err != nil {
		t.Fatalf("Transaction: %v", err)
	}
	info, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("lock permissions=%#o, want 0600", info.Mode().Perm())
	}
}

func TestTransactionRejectsUnsafeLockTargets(t *testing.T) {
	t.Run("symlink", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "config.yaml")
		outside := filepath.Join(root, "outside.lock")
		const sentinel = "outside lock must not change\n"
		if err := os.WriteFile(outside, []byte(sentinel), 0o600); err != nil {
			t.Fatal(err)
		}
		lockPath := transactionLockPath(path)
		if err := os.Symlink(outside, lockPath); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		called := false
		err := Transaction(context.Background(), path, func(cfg *File) (bool, error) {
			called = true
			return true, nil
		})
		assertUnsafeLockError(t, err, called)
		data, readErr := os.ReadFile(outside)
		if readErr != nil || string(data) != sentinel {
			t.Fatalf("outside lock changed: data=%q err=%v", data, readErr)
		}
	})

	t.Run("directory", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.Mkdir(transactionLockPath(path), 0o700); err != nil {
			t.Fatal(err)
		}
		called := false
		err := Transaction(context.Background(), path, func(cfg *File) (bool, error) {
			called = true
			return true, nil
		})
		assertUnsafeLockError(t, err, called)
	})
}

func transactionHelperCommand(path, profile, started, entered, release string) *exec.Cmd {
	cmd := exec.Command(os.Args[0], "-test.run=^TestTransactionHelperProcess$")
	cmd.Env = append(os.Environ(),
		transactionHelperEnv+"=1",
		transactionHelperPathEnv+"="+path,
		transactionHelperNameEnv+"="+profile,
		transactionStartedEnv+"="+started,
		transactionEnteredEnv+"="+entered,
		transactionReleaseEnv+"="+release,
	)
	return cmd
}

func waitForTransactionSignal(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s", filepath.Base(path))
}

func assertUnsafeLockError(t *testing.T, err error, called bool) {
	t.Helper()
	if called {
		t.Fatal("mutation callback ran for unsafe lock target")
	}
	if err == nil {
		t.Fatal("unsafe lock target unexpectedly accepted")
	}
	appErr, ok := apperrors.From(err)
	if !ok || appErr.Code != apperrors.CodeConfigInvalid || !strings.Contains(appErr.Message, "Unsafe config lock target") {
		t.Fatalf("unsafe lock error=%#v", err)
	}
}
