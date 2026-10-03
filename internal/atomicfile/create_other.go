//go:build !darwin && !linux && !windows

package atomicfile

func moveNew(temporaryPath, path string) error {
	return linkNew(temporaryPath, path)
}
