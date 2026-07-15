//go:build windows

package config

import (
	"errors"
	"testing"
	"time"
)

func replaceConfigFileWithRetry(
	temporaryPath, path string,
	retryTimeout, retryDelay time.Duration,
	operations configReplaceOperations,
) (unsafeTarget bool, err error) {
	err = retryConfigFilesystemOperation(
		retryTimeout,
		retryDelay,
		operations,
		func() error {
			if err := operations.validate(path); err != nil {
				return err
			}
			return operations.rename(temporaryPath, path)
		},
	)
	return operations.unsafe(err), err
}

func validateConfigTargetWithRetry(
	path string,
	retryTimeout, retryDelay time.Duration,
	operations configReplaceOperations,
) error {
	return retryConfigFilesystemOperation(
		retryTimeout,
		retryDelay,
		operations,
		func() error { return operations.validate(path) },
	)
}

func TestReplaceConfigFileRetriesOnlyRetryableFailures(t *testing.T) {
	transientErr := errors.New("transient rename failure")
	attempts := 0
	validations := 0
	unsafeTarget, err := replaceConfigFileWithRetry(
		"temporary",
		"target",
		time.Second,
		0,
		configReplaceOperations{
			validate: func(string) error {
				validations++
				return nil
			},
			rename: func(string, string) error {
				attempts++
				if attempts < 3 {
					return transientErr
				}
				return nil
			},
			retryable: func(err error) bool {
				return errors.Is(err, transientErr)
			},
			unsafe: func(error) bool { return false },
			now:    time.Now,
			wait:   func(time.Duration) {},
		},
	)
	if err != nil || unsafeTarget {
		t.Fatalf("replace result unsafe=%v err=%v", unsafeTarget, err)
	}
	if attempts != 3 || validations != 3 {
		t.Fatalf("attempts=%d validations=%d, want 3 each", attempts, validations)
	}
}

func TestReplaceConfigFileDoesNotRetryPermanentFailure(t *testing.T) {
	permanentErr := errors.New("permanent rename failure")
	attempts := 0
	unsafeTarget, err := replaceConfigFileWithRetry(
		"temporary",
		"target",
		time.Second,
		0,
		configReplaceOperations{
			validate: func(string) error { return nil },
			rename: func(string, string) error {
				attempts++
				return permanentErr
			},
			retryable: func(error) bool { return false },
			unsafe:    func(error) bool { return false },
			now:       time.Now,
			wait:      func(time.Duration) {},
		},
	)
	if !errors.Is(err, permanentErr) || unsafeTarget {
		t.Fatalf("replace result unsafe=%v err=%v", unsafeTarget, err)
	}
	if attempts != 1 {
		t.Fatalf("rename attempts=%d, want 1", attempts)
	}
}

func TestReplaceConfigFileHonorsRetryDeadline(t *testing.T) {
	transientErrors := []error{
		errors.New("first transient rename failure"),
		errors.New("second transient rename failure"),
		errors.New("last transient rename failure"),
	}
	now := time.Unix(1_700_000_000, 0)
	waits := make([]time.Duration, 0, 3)
	attempts := 0
	unsafeTarget, err := replaceConfigFileWithRetry(
		"temporary",
		"target",
		70*time.Millisecond,
		25*time.Millisecond,
		configReplaceOperations{
			validate: func(string) error { return nil },
			rename: func(string, string) error {
				err := transientErrors[attempts]
				attempts++
				return err
			},
			retryable: func(error) bool { return true },
			unsafe:    func(error) bool { return false },
			now:       func() time.Time { return now },
			wait: func(delay time.Duration) {
				waits = append(waits, delay)
				now = now.Add(delay)
			},
		},
	)
	if !errors.Is(err, transientErrors[2]) || unsafeTarget {
		t.Fatalf("replace result unsafe=%v err=%v", unsafeTarget, err)
	}
	if attempts != 3 {
		t.Fatalf("rename attempts=%d, want 3", attempts)
	}
	wantWaits := []time.Duration{25 * time.Millisecond, 25 * time.Millisecond, 20 * time.Millisecond}
	if len(waits) != len(wantWaits) {
		t.Fatalf("waits=%v, want %v", waits, wantWaits)
	}
	for index := range wantWaits {
		if waits[index] != wantWaits[index] {
			t.Fatalf("waits=%v, want %v", waits, wantWaits)
		}
	}
}

func TestReplaceConfigFileRevalidatesTargetBeforeRetry(t *testing.T) {
	transientErr := errors.New("transient rename failure")
	unsafeErr := errors.New("target became a symlink")
	validations := 0
	attempts := 0
	unsafeTarget, err := replaceConfigFileWithRetry(
		"temporary",
		"target",
		time.Second,
		0,
		configReplaceOperations{
			validate: func(string) error {
				validations++
				if validations > 1 {
					return unsafeErr
				}
				return nil
			},
			rename: func(string, string) error {
				attempts++
				return transientErr
			},
			retryable: func(error) bool { return true },
			unsafe: func(err error) bool {
				return errors.Is(err, unsafeErr)
			},
			now:  time.Now,
			wait: func(time.Duration) {},
		},
	)
	if !errors.Is(err, unsafeErr) || !unsafeTarget {
		t.Fatalf("replace result unsafe=%v err=%v", unsafeTarget, err)
	}
	if attempts != 1 || validations != 2 {
		t.Fatalf("attempts=%d validations=%d, want 1 and 2", attempts, validations)
	}
}

func TestReplaceConfigFileRetriesTransientValidationFailure(t *testing.T) {
	transientErr := errors.New("transient target inspection failure")
	validations := 0
	attempts := 0
	unsafeTarget, err := replaceConfigFileWithRetry(
		"temporary",
		"target",
		time.Second,
		0,
		configReplaceOperations{
			validate: func(string) error {
				validations++
				if validations == 1 {
					return transientErr
				}
				return nil
			},
			rename: func(string, string) error {
				attempts++
				return nil
			},
			retryable: func(err error) bool { return errors.Is(err, transientErr) },
			unsafe:    func(error) bool { return false },
			now:       time.Now,
			wait:      func(time.Duration) {},
		},
	)
	if err != nil || unsafeTarget {
		t.Fatalf("replace result unsafe=%v err=%v", unsafeTarget, err)
	}
	if validations != 2 || attempts != 1 {
		t.Fatalf("validations=%d attempts=%d, want 2 and 1", validations, attempts)
	}
}

func TestValidateConfigTargetRetriesTransientInspectionFailure(t *testing.T) {
	transientErr := errors.New("transient target inspection failure")
	validations := 0
	operations := configReplaceOperations{
		validate: func(string) error {
			validations++
			if validations < 3 {
				return transientErr
			}
			return nil
		},
		retryable: func(err error) bool { return errors.Is(err, transientErr) },
		unsafe:    func(error) bool { return false },
		now:       time.Now,
		wait:      func(time.Duration) {},
	}
	if err := validateConfigTargetWithRetry("target", time.Second, 0, operations); err != nil {
		t.Fatalf("validate target: %v", err)
	}
	if validations != 3 {
		t.Fatalf("validations=%d, want 3", validations)
	}
}

func TestReadConfigFileRetriesTransientFailure(t *testing.T) {
	transientErr := errors.New("transient read failure")
	attempts := 0
	data, err := readConfigFileWithRetry(
		"target",
		time.Second,
		0,
		configReplaceOperations{
			retryable: func(err error) bool { return errors.Is(err, transientErr) },
			unsafe:    func(error) bool { return false },
			now:       time.Now,
			wait:      func(time.Duration) {},
		},
		func(string) ([]byte, error) {
			attempts++
			if attempts < 3 {
				return nil, transientErr
			}
			return []byte("complete config"), nil
		},
	)
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("read attempts=%d, want 3", attempts)
	}
	if string(data) != "complete config" {
		t.Fatalf("read data=%q, want complete config", data)
	}
}

func TestReadConfigFileDoesNotRetryPermanentFailure(t *testing.T) {
	permanentErr := errors.New("permanent read failure")
	attempts := 0
	data, err := readConfigFileWithRetry(
		"target",
		time.Second,
		0,
		configReplaceOperations{
			retryable: func(error) bool { return false },
			unsafe:    func(error) bool { return false },
			now:       time.Now,
			wait:      func(time.Duration) {},
		},
		func(string) ([]byte, error) {
			attempts++
			return nil, permanentErr
		},
	)
	if !errors.Is(err, permanentErr) {
		t.Fatalf("read error=%v, want permanent failure", err)
	}
	if data != nil {
		t.Fatalf("read data=%q, want nil", data)
	}
	if attempts != 1 {
		t.Fatalf("read attempts=%d, want 1", attempts)
	}
}
