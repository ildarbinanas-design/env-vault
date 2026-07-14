package apperrors

import (
	stderrors "errors"
	"fmt"
)

const (
	CodeBackendUnavailable   = "BACKEND_UNAVAILABLE"
	CodeCommandNotExecutable = "COMMAND_NOT_EXECUTABLE"
	CodeCommandNotFound      = "COMMAND_NOT_FOUND"
	CodeConfigInvalid        = "CONFIG_INVALID"
	CodeConfigLocked         = "CONFIG_LOCKED"
	CodeConfirmationRequired = "CONFIRMATION_REQUIRED"
	CodeEnvCollision         = "ENV_COLLISION"
	CodeProfileExists        = "PROFILE_EXISTS"
	CodeProfileNotFound      = "PROFILE_NOT_FOUND"
	CodeMissingSecret        = "MISSING_SECRET"
	CodeRuntimeError         = "RUNTIME_ERROR"
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
