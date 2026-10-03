package atomicfile

import (
	"errors"

	"golang.org/x/sys/unix"
)

func moveNew(temporaryPath, path string) error {
	err := unix.Renameat2(unix.AT_FDCWD, temporaryPath, unix.AT_FDCWD, path, unix.RENAME_NOREPLACE)
	// ENOSYS covers kernels without renameat2; EINVAL and EOPNOTSUPP cover
	// filesystems that do not implement this valid flag.
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) || errors.Is(err, unix.EOPNOTSUPP) {
		return linkNew(temporaryPath, path)
	}
	return err
}
