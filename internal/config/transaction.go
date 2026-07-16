package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
)

const (
	transactionLockTimeout = 5 * time.Second
	transactionRetryDelay  = 25 * time.Millisecond
)

// TransactionFunc mutates a loaded config and reports whether it changed.
// Returning changed=false skips the same-directory replacement save.
type TransactionFunc func(*File) (changed bool, err error)

// Transaction serializes one complete config read-modify-write operation.
// The adjacent lock file is intentionally persistent: removing it after
// unlock would let concurrent processes lock different inodes for one config.
func Transaction(ctx context.Context, path string, mutate TransactionFunc) (resultErr error) {
	if mutate == nil {
		return apperrors.ConfigInvalid("config", "Config transaction has no mutation", "Report this bug", nil)
	}
	if ctx == nil {
		ctx = context.Background()
	}

	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return apperrors.ConfigInvalid("config", "Unable to create config directory", "Check directory permissions", err)
	}
	if err := validateConfigTarget(path); err != nil {
		return configTargetValidationError(err)
	}

	lockPath := transactionLockPath(path)
	if err := validateLockTarget(lockPath); err != nil {
		return apperrors.ConfigInvalid("config", "Unsafe config lock target", "Remove the non-regular lock target and retry", err)
	}

	lock := flock.New(lockPath, flock.SetPermissions(0o600))
	defer func() {
		if err := lock.Close(); resultErr == nil && err != nil {
			resultErr = apperrors.ConfigInvalid("config", "Unable to release config lock", "Check filesystem health before retrying", err)
		}
	}()
	lockCtx, cancel := context.WithTimeout(ctx, transactionLockTimeout)
	defer cancel()
	locked, err := lock.TryLockContext(lockCtx, transactionRetryDelay)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return configLockWaitError(err)
		}
		return apperrors.ConfigInvalid("config", "Unable to acquire config lock", "Check the config directory and lock-file permissions", err)
	}
	if !locked {
		return apperrors.Wrap("config", apperrors.CodeConfigLocked, "Config is locked by another process", "Retry after the other profile command finishes", apperrors.ExitConfigInvalid, nil)
	}

	if err := secureLockedTarget(lockPath); err != nil {
		return apperrors.ConfigInvalid("config", "Unsafe config lock target", "Use a private regular lock file beside the config", err)
	}
	// Recheck the config path under the lock before Load so an existing
	// symlink or non-regular target is rejected before it can be read.
	if err := validateConfigTarget(path); err != nil {
		return configTargetValidationError(err)
	}

	cfg, err := Load(path)
	if err != nil {
		return err
	}
	changed, err := mutate(cfg)
	if err != nil {
		return err
	}
	if err := Validate(cfg); err != nil {
		return apperrors.ConfigInvalid("config", "Invalid config transaction", "Fix profile mappings before saving", err)
	}
	if !changed {
		return nil
	}
	return Save(path, cfg)
}

func transactionLockPath(path string) string {
	return path + ".lock"
}

func validateLockTarget(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("config lock path is a symlink")
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("config lock path is not a regular file")
	}
	return nil
}

func secureLockedTarget(path string) error {
	if err := validateLockedTarget(path); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("set config lock permissions: %w", err)
	}
	if err := validateLockedTarget(path); err != nil {
		return err
	}
	return nil
}

func validateLockedTarget(path string) error {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("config lock path is a symlink")
	}
	if !pathInfo.Mode().IsRegular() {
		return fmt.Errorf("config lock path is not a regular file")
	}
	return nil
}

func configLockWaitError(err error) error {
	message := "Config lock wait was cancelled"
	if errors.Is(err, context.DeadlineExceeded) {
		message = "Timed out waiting for config lock"
	}
	return apperrors.Wrap(
		"config",
		apperrors.CodeConfigLocked,
		message,
		"Retry after the other profile command finishes",
		apperrors.ExitConfigInvalid,
		err,
	)
}
