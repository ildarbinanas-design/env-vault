package teststore

import (
	"context"
	"encoding/gob"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	apperrors "github.com/ildarbinanas-design/env-vault/internal/errors"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
)

const (
	BackendEnv = "ENV_VAULT_BACKEND"
	AllowEnv   = "ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND"
	StoreEnv   = "ENV_VAULT_TEST_STORE"
)

type Store struct {
	path string
	mu   sync.Mutex
}

type diskData struct {
	Services map[string]map[string][]byte
}

func EnabledFromEnv() bool {
	return os.Getenv(BackendEnv) == "test" &&
		os.Getenv(AllowEnv) == "1" &&
		os.Getenv(StoreEnv) != ""
}

func RequestedFromEnv() bool {
	return os.Getenv(BackendEnv) == "test" || os.Getenv(AllowEnv) != "" || os.Getenv(StoreEnv) != ""
}

func NewFromEnv(command string) (*Store, error) {
	if os.Getenv(BackendEnv) != "test" {
		return nil, apperrors.BackendUnavailable(command, "Test backend was not requested", "Unset test backend env vars or set all required test backend gates", secretstore.ErrUnavailable)
	}
	if os.Getenv(AllowEnv) != "1" {
		return nil, apperrors.BackendUnavailable(command, "Insecure test backend is not explicitly allowed", "Set ENV_VAULT_ALLOW_INSECURE_TEST_BACKEND=1 only in tests", secretstore.ErrUnavailable)
	}
	path := os.Getenv(StoreEnv)
	if path == "" {
		return nil, apperrors.BackendUnavailable(command, "Test backend store path is missing", "Set ENV_VAULT_TEST_STORE to an absolute path under /tmp", secretstore.ErrUnavailable)
	}
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) || !isTempPath(clean) {
		return nil, apperrors.BackendUnavailable(command, "Test backend store path must be under /tmp", "Use ENV_VAULT_TEST_STORE=/tmp/env-vault-test-store", secretstore.ErrUnavailable)
	}
	return &Store{path: clean}, nil
}

func (s *Store) Set(_ context.Context, service, name string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.read()
	if err != nil {
		return err
	}
	if data.Services[service] == nil {
		data.Services[service] = map[string][]byte{}
	}
	data.Services[service][name] = append([]byte(nil), value...)
	return s.write(data)
}

func (s *Store) Get(_ context.Context, service, name string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.read()
	if err != nil {
		return nil, err
	}
	value, ok := data.Services[service][name]
	if !ok {
		return nil, secretstore.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (s *Store) Exists(ctx context.Context, service, name string) (bool, error) {
	_, err := s.Get(ctx, service, name)
	if err == secretstore.ErrNotFound {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) Delete(_ context.Context, service, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.read()
	if err != nil {
		return err
	}
	if _, ok := data.Services[service][name]; !ok {
		return secretstore.ErrNotFound
	}
	delete(data.Services[service], name)
	return s.write(data)
}

func (s *Store) List(_ context.Context, service string) ([]secretstore.Metadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := s.read()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(data.Services[service]))
	for name := range data.Services[service] {
		names = append(names, name)
	}
	sort.Strings(names)
	items := make([]secretstore.Metadata, 0, len(names))
	for _, name := range names {
		items = append(items, secretstore.Metadata{
			Service:     service,
			Name:        name,
			Fingerprint: secretstore.Fingerprint(service, name),
		})
	}
	return items, nil
}

func (s *Store) read() (diskData, error) {
	data := diskData{Services: map[string]map[string][]byte{}}
	file, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return data, nil
	}
	if err != nil {
		return data, err
	}
	defer file.Close()
	if err := gob.NewDecoder(file).Decode(&data); err != nil {
		return data, err
	}
	if data.Services == nil {
		data.Services = map[string]map[string][]byte{}
	}
	return data, nil
}

func (s *Store) write(data diskData) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := gob.NewEncoder(file).Encode(data); err != nil {
		return err
	}
	return os.Chmod(s.path, 0o600)
}

func isTempPath(path string) bool {
	candidates := []string{"/tmp", "/private/tmp", filepath.Clean(os.TempDir())}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		candidate = filepath.Clean(candidate)
		if path == candidate || strings.HasPrefix(path, candidate+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}
