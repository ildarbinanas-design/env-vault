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
)

const (
	DefaultBackendRemediation = "Run env-vault doctor or configure the OS keychain"
	PassBackendRemediation    = "install pass or use another supported OS keychain backend."
)

func BackendRemediation(err error) string {
	if errors.Is(err, ErrPassUnavailable) {
		return PassBackendRemediation
	}
	return DefaultBackendRemediation
}

type Metadata struct {
	Service     string
	Name        string
	Fingerprint string
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

func Fingerprint(service, name string) string {
	sum := sha256.Sum256([]byte(service + "\x00" + name))
	return hex.EncodeToString(sum[:])[:16]
}
