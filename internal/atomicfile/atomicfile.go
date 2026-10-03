// Package atomicfile writes a file so readers never observe a truncated or
// partially written result, and so a symlink planted at the target cannot
// redirect the write.
//
// It deliberately does not carry the cooperative-transaction machinery in
// internal/config. That machinery exists because several env-vault processes
// coordinate on one config pathname; a transfer container is written once to a
// path the user names, so it needs target safety and atomic publication only.
package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// UnsafeTargetError reports a target that must not be written through, as
// opposed to an ordinary filesystem failure. Callers map it to a distinct
// remediation.
type UnsafeTargetError struct {
	Reason string
}

func (err *UnsafeTargetError) Error() string {
	return err.Reason
}

// IsUnsafeTarget reports whether err was caused by an unsafe target.
func IsUnsafeTarget(err error) bool {
	var unsafeTarget *UnsafeTargetError
	return errors.As(err, &unsafeTarget)
}

// ValidateTarget rejects a target that exists but is not a regular file. A
// missing target is valid: it is the ordinary create case.
func ValidateTarget(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return &UnsafeTargetError{Reason: "path is a symlink"}
	}
	if !info.Mode().IsRegular() {
		return &UnsafeTargetError{Reason: "path is not a regular file"}
	}
	return nil
}

// Write publishes data at path with mode 0600.
//
// The data is written to a temporary sibling, synced, and closed before it
// replaces the target, so a crash cannot leave a half-written file at the
// target pathname. The target is validated immediately before replacement:
// replacement replaces a symlink raced in at the last moment rather than
// following it, and the check turns an already-present symlink into a clear
// failure instead of a write through it.
func Write(path string, data []byte) error {
	return write(path, data, replace)
}

// WriteNew publishes data at path with mode 0600 only if the target is absent.
// The final publication atomically refuses every existing directory entry,
// including a file created after the initial validation. It returns an error
// wrapping os.ErrExist for a regular file that already exists.
//
// Publication uses a native exclusive rename, with a hard-link fallback when
// the operation is unsupported. Filesystems that support neither fail without
// a fallback that could overwrite a concurrent file or expose a partial result.
func WriteNew(path string, data []byte) error {
	return write(path, data, publishNew)
}

func publishNew(temporaryPath, path string) error {
	if err := ValidateTarget(path); err != nil {
		return err
	}
	if err := moveNew(temporaryPath, path); err != nil {
		return fmt.Errorf("publish new target: %w", err)
	}
	return nil
}

// linkNew retains exclusive publication when native exclusive rename is not
// available. Both names refer to the complete file before the temporary name
// is removed; no copy or replacing rename is permitted here.
func linkNew(temporaryPath, path string) error {
	if err := os.Link(temporaryPath, path); err != nil {
		return err
	}
	if err := os.Remove(temporaryPath); err != nil {
		return fmt.Errorf("remove temporary file: %w", err)
	}
	return nil
}

func write(path string, data []byte, publish func(string, string) error) error {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	if err := ValidateTarget(path); err != nil {
		return err
	}

	temporary, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure temporary file: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := publish(temporaryPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}
