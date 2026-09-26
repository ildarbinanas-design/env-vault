package apperrors

import (
	stderrors "errors"
	"fmt"
	"os"
)

const (
	CodeBackendUnavailable   = "BACKEND_UNAVAILABLE"
	CodeBundleAuthFailed     = "BUNDLE_AUTH_FAILED"
	CodeBundleInvalid        = "BUNDLE_INVALID"
	CodeCommandNotExecutable = "COMMAND_NOT_EXECUTABLE"
	CodeCommandNotFound      = "COMMAND_NOT_FOUND"
	CodeConfigInvalid        = "CONFIG_INVALID"
	CodeConfigLocked         = "CONFIG_LOCKED"
	CodeConfirmationRequired = "CONFIRMATION_REQUIRED"
	CodeEnvCollision         = "ENV_COLLISION"
	CodeProfileExists        = "PROFILE_EXISTS"
	CodeProfileNotFound      = "PROFILE_NOT_FOUND"
	CodeMissingSecret        = "MISSING_SECRET"
	CodePassphraseInvalid    = "PASSPHRASE_INVALID"
	CodeRuntimeError         = "RUNTIME_ERROR"
	CodeSecretExists         = "SECRET_EXISTS"
	CodeSecretUnverified     = "SECRET_UNVERIFIED"
	CodeUsage                = "USAGE"
)

const (
	ExitSuccess              = 0
	ExitRuntimeError         = 1
	ExitUsage                = 2
	ExitMissingSecret        = 3
	ExitBackendUnavailable   = 4
	ExitConfigInvalid        = 5
	ExitCommandNotExecutable = 126
	ExitCommandNotFound      = 127
)

type AppError struct {
	Code        string
	Message     string
	Remediation string
	Command     string
	ExitCode    int
	Cause       error
}

func New(command, code, message, remediation string, exitCode int) *AppError {
	return &AppError{
		Code:        code,
		Message:     message,
		Remediation: remediation,
		Command:     command,
		ExitCode:    exitCode,
	}
}

func Wrap(command, code, message, remediation string, exitCode int, cause error) *AppError {
	err := New(command, code, message, remediation, exitCode)
	err.Cause = cause
	return err
}

func Usage(command, message, remediation string) *AppError {
	return New(command, CodeUsage, message, remediation, ExitUsage)
}

func ConfigInvalid(command, message, remediation string, cause error) *AppError {
	return Wrap(command, CodeConfigInvalid, message, remediation, ExitConfigInvalid, cause)
}

func BackendUnavailable(command, message, remediation string, cause error) *AppError {
	return Wrap(command, CodeBackendUnavailable, message, remediation, ExitBackendUnavailable, cause)
}

func MissingSecret(command, name string) *AppError {
	return New(command, CodeMissingSecret, "Missing secret: "+name, "Run: env-vault secret set "+name, ExitMissingSecret)
}

// BundleInvalid reports a transfer container that is malformed, carries an
// unknown schema or algorithm, or declares key-derivation parameters outside
// the accepted bounds. It shares ExitConfigInvalid because both describe an
// input artifact this build refuses to interpret.
func BundleInvalid(command, message, remediation string, cause error) *AppError {
	return Wrap(command, CodeBundleInvalid, message, remediation, ExitConfigInvalid, cause)
}

// BundleAuthFailed reports that authenticated decryption failed. The message
// deliberately does not separate a wrong passphrase from a tampered container,
// because the cryptography cannot distinguish them either.
func BundleAuthFailed(command string, cause error) *AppError {
	return Wrap(
		command,
		CodeBundleAuthFailed,
		"Unable to decrypt the container",
		"Check the passphrase; the container may also have been modified",
		ExitConfigInvalid,
		cause,
	)
}

// PassphraseInvalid reports a passphrase the user must retype.
func PassphraseInvalid(command, message, remediation string) *AppError {
	return New(command, CodePassphraseInvalid, message, remediation, ExitUsage)
}

// SecretExists reports an import conflict the user resolves with a flag.
func SecretExists(command, name string) *AppError {
	return New(
		command,
		CodeSecretExists,
		"Secret already exists: "+name,
		"Re-run with --on-conflict overwrite or --on-conflict skip",
		ExitUsage,
	)
}

// SecretUnverified reports a write whose read-back did not match the input,
// so the caller must not assume the new value is in effect.
func SecretUnverified(command, name string) *AppError {
	return New(
		command,
		CodeSecretUnverified,
		"Stored secret could not be verified: "+name,
		"Re-run secret set, then env-vault doctor if it persists",
		ExitRuntimeError,
	)
}

func (e *AppError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func From(err error) (*AppError, bool) {
	var appErr *AppError
	if stderrors.As(err, &appErr) {
		return appErr, true
	}
	return nil, false
}

type ExitStatus struct {
	Code int
	// Signal is the signal that killed the child process, or nil when the
	// child exited on its own. Code is then 128 plus the signal number.
	Signal os.Signal
}

func (e *ExitStatus) Error() string {
	return fmt.Sprintf("exit status %d", e.Code)
}

func NewExitStatus(code int) *ExitStatus {
	return &ExitStatus{Code: code}
}

func ExitStatusFrom(err error) (*ExitStatus, bool) {
	var exitErr *ExitStatus
	if stderrors.As(err, &exitErr) {
		return exitErr, true
	}
	return nil, false
}
