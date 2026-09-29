//go:build !windows

package atomicfile

import (
	"fmt"
	"os"
)

// replace publishes the temporary file at path. A rename replaces the
// directory entry even while other processes hold the previous file open.
func replace(temporaryPath, path string) error {
	if err := ValidateTarget(path); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace target: %w", err)
	}
	return nil
}
