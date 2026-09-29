//go:build windows

package atomicfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// Windows refuses to replace a file that another process holds open without
// delete sharing, such as an antivirus scanner or an indexer reading the
// previous file. Such holds are short, so the replacement is retried for up
// to a second, as the config writer does.
const (
	replaceTimeout = time.Second
	replaceDelay   = 25 * time.Millisecond
)

func replace(temporaryPath, path string) error {
	clock := retryClock{now: time.Now, sleep: time.Sleep}
	return retryReplace(replaceTimeout, replaceDelay, clock, isTransientReplaceError, func() error {
		return replaceOnce(temporaryPath, path)
	})
}

// replaceOnce renames under one directory handle. os.Root.Rename uses POSIX
// rename semantics where the file system supports them, so a reader that
// opened the previous file with delete sharing does not block it.
func replaceOnce(temporaryPath, path string) error {
	if err := ValidateTarget(path); err != nil {
		return err
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("replace target: %w", err)
	}
	defer root.Close()
	if err := root.Rename(filepath.Base(temporaryPath), filepath.Base(path)); err != nil {
		return fmt.Errorf("replace target: %w", err)
	}
	return nil
}

func isTransientReplaceError(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
