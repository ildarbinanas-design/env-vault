package atomicfile

import (
	"errors"

	"golang.org/x/sys/unix"
)

func moveNew(temporaryPath, path string) error {
	err := unix.RenamexNp(temporaryPath, path, unix.RENAME_EXCL)
	if errors.Is(err, unix.ENOTSUP) {
		return linkNew(temporaryPath, path)
	}
	return err
}
