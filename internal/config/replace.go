package config

import "errors"

type unsafeConfigTargetError struct {
	reason string
}

func (err *unsafeConfigTargetError) Error() string {
	return err.reason
}

func isUnsafeConfigTargetError(err error) bool {
	var unsafeTarget *unsafeConfigTargetError
	return errors.As(err, &unsafeTarget)
}
