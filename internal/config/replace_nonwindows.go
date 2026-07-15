//go:build !windows

package config

import "os"

func replaceConfigFile(temporaryPath, path string) (unsafeTarget bool, err error) {
	if err := validateSaveTarget(path); err != nil {
		return isUnsafeConfigTargetError(err), err
	}
	return false, os.Rename(temporaryPath, path)
}

func validateConfigTarget(path string) error {
	return validateSaveTarget(path)
}
