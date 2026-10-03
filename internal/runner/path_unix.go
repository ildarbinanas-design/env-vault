//go:build !windows

package runner

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// LookPath discards permission errors while searching PATH. After that
// search fails, recover only the distinction between absent and unusable.
// This never chooses an executable or bypasses exec.ErrDot.
func pathHasNonExecutable(name string) bool {
	if name == "" {
		return false
	}
	for _, directory := range filepath.SplitList(os.Getenv("PATH")) {
		candidate, err := filepath.Abs(filepath.Join(directory, name))
		if err != nil {
			continue
		}
		_, err = exec.LookPath(candidate)
		if errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EISDIR) {
			return true
		}
	}
	return false
}
