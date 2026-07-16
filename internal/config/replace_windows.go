//go:build windows

package config

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime/coverage"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

const (
	configFilesystemRetryTimeout = time.Second
	configFilesystemRetryDelay   = 25 * time.Millisecond
)

var e2eCoverageBuildIdentity struct {
	once    sync.Once
	enabled bool
}

var errE2EWindowsCoverageIdentity = errors.New("E2E Windows replacement fault requires a coverage-instrumented binary")

type configReplaceOperations struct {
	validate  func(string) error
	rename    func(string, string) error
	retryable func(error) bool
	unsafe    func(error) bool
	now       func() time.Time
	wait      func(time.Duration)
}

func replaceConfigFile(temporaryPath, path string) (unsafeTarget bool, err error) {
	coverageBuild := false
	if isE2EWindowsReplaceFailureRequested(path) {
		coverageBuild = isCoverageInstrumentedBuild()
	}
	return replaceConfigFileForBuild(temporaryPath, path, coverageBuild)
}

func replaceConfigFileForBuild(temporaryPath, path string, coverageBuild bool) (unsafeTarget bool, err error) {
	operations := defaultConfigReplaceOperations()
	injectTransient, err := e2eWindowsReplaceFailureMode(path, coverageBuild)
	if err != nil {
		return false, err
	}
	injected := false
	err = retryConfigFilesystemOperation(
		configFilesystemRetryTimeout,
		configFilesystemRetryDelay,
		operations,
		func() error {
			if err := operations.validate(path); err != nil {
				return err
			}
			if injectTransient && !injected {
				injected = true
				return windows.ERROR_SHARING_VIOLATION
			}
			return operations.rename(temporaryPath, path)
		},
	)
	return operations.unsafe(err), err
}

func readConfigFile(path string) ([]byte, error) {
	return readConfigFileWithRetry(
		path,
		configFilesystemRetryTimeout,
		configFilesystemRetryDelay,
		defaultConfigReplaceOperations(),
		os.ReadFile,
	)
}

func readConfigFileWithRetry(
	path string,
	retryTimeout, retryDelay time.Duration,
	operations configReplaceOperations,
	readFile func(string) ([]byte, error),
) ([]byte, error) {
	var data []byte
	err := retryConfigFilesystemOperation(
		retryTimeout,
		retryDelay,
		operations,
		func() error {
			var err error
			data, err = readFile(path)
			return err
		},
	)
	return data, err
}

func validateConfigTarget(path string) error {
	operations := defaultConfigReplaceOperations()
	return retryConfigFilesystemOperation(
		configFilesystemRetryTimeout,
		configFilesystemRetryDelay,
		operations,
		func() error { return operations.validate(path) },
	)
}

func defaultConfigReplaceOperations() configReplaceOperations {
	return configReplaceOperations{
		validate:  validateSaveTarget,
		rename:    os.Rename,
		retryable: isTransientConfigFilesystemError,
		unsafe:    isUnsafeConfigTargetError,
		now:       time.Now,
		wait:      waitForConfigReplaceRetry,
	}
}

func waitForConfigReplaceRetry(delay time.Duration) {
	timer := time.NewTimer(delay)
	<-timer.C
}

func retryConfigFilesystemOperation(
	retryTimeout, retryDelay time.Duration,
	operations configReplaceOperations,
	operation func() error,
) error {
	deadline := operations.now().Add(retryTimeout)
	for {
		operationErr := operation()
		if operationErr == nil {
			return nil
		}
		if operations.unsafe(operationErr) || !operations.retryable(operationErr) || retryTimeout <= 0 {
			return operationErr
		}

		remaining := deadline.Sub(operations.now())
		if remaining <= 0 {
			return operationErr
		}
		pause := min(retryDelay, remaining)
		if pause > 0 {
			operations.wait(pause)
		}
		if !operations.now().Before(deadline) {
			return operationErr
		}
	}
}

func isTransientConfigFilesystemError(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

// The coverage E2E pass deliberately exercises the native retry path. The
// injection is unreachable unless the insecure test backend, the E2E child
// marker, and subprocess coverage are all explicitly enabled. It affects only
// replacement of an existing regular test config and returns one real Windows
// sharing errno before the unchanged os.Rename operation is attempted.
func isCoverageInstrumentedBuild() bool {
	e2eCoverageBuildIdentity.once.Do(func() {
		e2eCoverageBuildIdentity.enabled = coverage.WriteMeta(io.Discard) == nil
	})
	return e2eCoverageBuildIdentity.enabled
}

func e2eWindowsReplaceFailureMode(path string, coverageBuild bool) (inject bool, err error) {
	requested := isE2EWindowsReplaceFailureRequested(path)
	if !requested {
		return false, nil
	}
	if !coverageBuild {
		return false, errE2EWindowsCoverageIdentity
	}
	return true, nil
}

func isE2EWindowsReplaceFailureRequested(path string) bool {
	if os.Getenv("ENV_VAULT_BACKEND") != "test" ||
		os.Getenv("ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND") != "1" ||
		os.Getenv("ENV_VAULT_TEST_STORE") == "" ||
		os.Getenv("ENV_VAULT_E2E_CHILD_MARKER") != "public-marker" ||
		os.Getenv("GOCOVERDIR") == "" {
		return false
	}

	tempRoot, err := filepath.Abs(filepath.Clean(os.TempDir()))
	if err != nil {
		return false
	}
	scenarioRoot := filepath.Dir(tempRoot)
	store := os.Getenv("ENV_VAULT_TEST_STORE")
	if !filepath.IsAbs(store) || !pathWithinRoot(store, tempRoot) {
		return false
	}
	configPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil || !pathWithinRoot(configPath, scenarioRoot) {
		return false
	}
	resolvedRoot, err := filepath.EvalSymlinks(scenarioRoot)
	if err != nil {
		return false
	}
	resolvedConfig, err := filepath.EvalSymlinks(configPath)
	if err != nil || !pathWithinRoot(resolvedConfig, resolvedRoot) {
		return false
	}
	info, err := os.Lstat(configPath)
	return err == nil && info.Mode().IsRegular()
}

func pathWithinRoot(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
