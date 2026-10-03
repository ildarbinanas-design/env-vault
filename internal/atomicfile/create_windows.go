package atomicfile

import (
	"errors"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

func moveNew(temporaryPath, path string) error {
	from, err := movePath(temporaryPath)
	if err != nil {
		return err
	}
	to, err := movePath(path)
	if err != nil {
		return err
	}
	clock := retryClock{now: time.Now, sleep: time.Sleep}
	err = retryReplace(replaceTimeout, replaceDelay, clock, isTransientReplaceError, func() error {
		if err := ValidateTarget(path); err != nil {
			return err
		}
		// Neither replacement nor copying is allowed. Both names are in
		// the same directory, so this moves the completed file in place.
		return windows.MoveFileEx(from, to, 0)
	})
	if errors.Is(err, windows.ERROR_NOT_SUPPORTED) || errors.Is(err, windows.ERROR_INVALID_FUNCTION) {
		return linkNew(temporaryPath, path)
	}
	return err
}

// Preserve os package support for long local and UNC paths when calling the
// Windows API directly. Short paths retain ordinary Win32 normalization.
func movePath(path string) (*uint16, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if len(path) >= 248 && !strings.HasPrefix(path, `\\?\`) &&
		!strings.HasPrefix(path, `\\.\`) && !strings.HasPrefix(path, `\??\`) {
		if strings.HasPrefix(path, `\\`) {
			path = `\\?\UNC\` + path[2:]
		} else {
			path = `\\?\` + path
		}
	}
	return windows.UTF16PtrFromString(path)
}
