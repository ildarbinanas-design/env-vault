package secretstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const DefaultService = "env-vault"

var secretNameRE = regexp.MustCompile(`^[A-Za-z0-9._/@-]+$`)

var (
	ErrNotFound        = errors.New("secret not found")
	ErrUnavailable     = errors.New("secret backend unavailable")
	ErrPassUnavailable = errors.New("pass backend unavailable")
	// ErrTimeout reports a backend call that did not return in time, usually a
	// system prompt that nobody answered.
	ErrTimeout = errors.New("secret backend did not respond in time")
	// ErrUnreadable reports a record the backend lists but refused to return,
	// for example after a denied macOS Keychain prompt or a locked keychain.
	ErrUnreadable = errors.New("secret exists but the backend did not return it")
	// ErrValueTooLarge reports a value larger than the backend can store.
	ErrValueTooLarge = errors.New("secret value is larger than the backend stores")
	// ErrPassServiceSlash reports a service name the pass backend cannot keep
	// apart from secret names; see ValidatePassServiceName.
	ErrPassServiceSlash = errors.New("the pass backend keeps the service and the secret name in one path, so a service name cannot contain a slash there")
)

const (
	DefaultBackendRemediation    = "Run env-vault doctor or configure the OS keychain"
	PassBackendRemediation       = "install pass or use another supported OS keychain backend."
	TimeoutBackendRemediation    = "Answer the system keychain prompt or unlock the keychain, then retry"
	UnreadableBackendRemediation = "Allow env-vault in the system keychain prompt or unlock the keychain, then retry"
	ValueTooLargeRemediation     = "Windows Credential Manager stores at most 2560 bytes per secret; store a shorter value"
	PassServiceRemediation       = "Use a service name without a slash with the pass backend"
)

func BackendRemediation(err error) string {
	switch {
	case errors.Is(err, ErrTimeout):
		return TimeoutBackendRemediation
	case errors.Is(err, ErrUnreadable):
		return UnreadableBackendRemediation
	case errors.Is(err, ErrPassServiceSlash):
		return PassServiceRemediation
	case errors.Is(err, ErrPassUnavailable):
		return PassBackendRemediation
	}
	return DefaultBackendRemediation
}

type Metadata struct {
	Service  string
	Name     string
	RecordID string
}

// ValueLimiter is implemented by a store whose backend limits the size of a
// value, so a command that writes several secrets can refuse before the first
// write instead of stopping halfway.
type ValueLimiter interface {
	// MaxValueBytes returns the largest value the backend stores, or 0 when
	// env-vault knows of no limit.
	MaxValueBytes() int
}

type Store interface {
	Set(ctx context.Context, service, name string, value []byte) error
	Get(ctx context.Context, service, name string) ([]byte, error)
	Exists(ctx context.Context, service, name string) (bool, error)
	Delete(ctx context.Context, service, name string) error
	List(ctx context.Context, service string) ([]Metadata, error)
}

// ValidateSecretName accepts the documented slash-separated secret name
// syntax while rejecting path forms that could escape a backend namespace.
func ValidateSecretName(name string) error {
	if name == "" {
		return fmt.Errorf("secret name is empty")
	}
	if strings.Contains(name, ":") {
		return fmt.Errorf("secret name must not contain ':'")
	}
	if !utf8.ValidString(name) || !secretNameRE.MatchString(name) {
		return fmt.Errorf("secret name contains unsupported characters")
	}
	if err := validateSlashPath("secret name", name); err != nil {
		return err
	}
	return nil
}

// ValidateServiceName rejects path traversal without unnecessarily narrowing
// keychain service labels. Safe slash-separated service names remain valid.
func ValidateServiceName(service string) error {
	if service == "" {
		return fmt.Errorf("service name is empty")
	}
	if !utf8.ValidString(service) {
		return fmt.Errorf("service name is not valid UTF-8")
	}
	for _, r := range service {
		if r == '\n' || r == '\r' || r < 0x20 || r == 0x7f {
			return fmt.Errorf("service name contains a control character")
		}
	}
	if err := validateSlashPath("service name", service); err != nil {
		return err
	}
	return nil
}

// ValidatePassServiceName rejects a service name that the pass backend cannot
// keep apart from secret names. pass stores a secret at
// env-vault/<service>/<name>, and both parts may contain slashes, so the
// service "team/ci" with the secret "tok" and the service "team" with the
// secret "ci/tok" would be one entry.
func ValidatePassServiceName(service string) error {
	if strings.Contains(service, "/") {
		return ErrPassServiceSlash
	}
	return nil
}

func validateSlashPath(label, value string) error {
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\`) || hasWindowsAbsolutePrefix(value) {
		return fmt.Errorf("%s must be relative", label)
	}
	if strings.Contains(value, `\`) {
		return fmt.Errorf("%s contains an unsupported path separator", label)
	}
	for _, component := range strings.Split(value, "/") {
		switch component {
		case "":
			return fmt.Errorf("%s contains an empty path component", label)
		case ".", "..":
			return fmt.Errorf("%s contains a forbidden path component %q", label, component)
		}
	}
	return nil
}

func hasWindowsAbsolutePrefix(value string) bool {
	if len(value) < 3 || value[1] != ':' || (value[2] != '/' && value[2] != '\\') {
		return false
	}
	first := value[0]
	return first >= 'A' && first <= 'Z' || first >= 'a' && first <= 'z'
}

// RecordID identifies the stored record for a service and secret name. It is
// derived only from those public identifiers, never from the secret value, so
// it stays the same when the value is overwritten and gives nothing to an
// offline guess. Output reports it as record_id and, for compatibility, as the
// deprecated alias fingerprint.
func RecordID(service, name string) string {
	sum := sha256.Sum256([]byte(service + "\x00" + name))
	return hex.EncodeToString(sum[:])[:16]
}
