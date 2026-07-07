package keyring

import (
	"context"
	stderrors "errors"
	"fmt"
	"sort"

	"github.com/99designs/keyring"

	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
)

type Store struct {
	allowedBackends []keyring.BackendType
	unavailableErr  error
}

func New() Store {
	return Store{
		allowedBackends: productionAllowedBackends(),
		unavailableErr:  secretstore.ErrUnavailable,
	}
}

func NewPass() Store {
	return Store{
		allowedBackends: []keyring.BackendType{keyring.PassBackend},
		unavailableErr:  secretstore.ErrPassUnavailable,
	}
}

func (s Store) Set(_ context.Context, service, name string, value []byte) error {
	kr, err := s.open(service)
	if err != nil {
		return err
	}
	if err := kr.Set(keyring.Item{
		Key:         name,
		Data:        append([]byte(nil), value...),
		Label:       name,
		Description: "env-vault secret",
	}); err != nil {
		return s.backendError(err)
	}
	return nil
}

func (s Store) Get(_ context.Context, service, name string) ([]byte, error) {
	kr, err := s.open(service)
	if err != nil {
		return nil, err
	}
	item, err := kr.Get(name)
	if stderrors.Is(err, keyring.ErrKeyNotFound) {
		return nil, secretstore.ErrNotFound
	}
	if err != nil {
		return nil, s.backendError(err)
	}
	return append([]byte(nil), item.Data...), nil
}

func (s Store) Exists(ctx context.Context, service, name string) (bool, error) {
	_, err := s.Get(ctx, service, name)
	if stderrors.Is(err, secretstore.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s Store) Delete(_ context.Context, service, name string) error {
	kr, err := s.open(service)
	if err != nil {
		return err
	}
	if err := kr.Remove(name); stderrors.Is(err, keyring.ErrKeyNotFound) {
		return secretstore.ErrNotFound
	} else if err != nil {
		return s.backendError(err)
	}
	return nil
}

func (s Store) List(_ context.Context, service string) ([]secretstore.Metadata, error) {
	kr, err := s.open(service)
	if err != nil {
		return nil, err
	}
	keys, err := kr.Keys()
	if err != nil {
		return nil, s.backendError(err)
	}
	sort.Strings(keys)
	items := make([]secretstore.Metadata, 0, len(keys))
	for _, name := range keys {
		items = append(items, secretstore.Metadata{
			Service:     service,
			Name:        name,
			Fingerprint: secretstore.Fingerprint(service, name),
		})
	}
	return items, nil
}

func productionAllowedBackends() []keyring.BackendType {
	return []keyring.BackendType{
		keyring.KeychainBackend,
		keyring.SecretServiceBackend,
		keyring.KWalletBackend,
		keyring.WinCredBackend,
		keyring.PassBackend,
	}
}

func (s Store) open(service string) (keyring.Keyring, error) {
	allowed := append([]keyring.BackendType(nil), s.allowedBackends...)
	if len(allowed) == 0 {
		allowed = productionAllowedBackends()
	}
	kr, err := keyring.Open(keyring.Config{
		ServiceName:            service,
		AllowedBackends:        allowed,
		KeychainSynchronizable: false,
	})
	if stderrors.Is(err, keyring.ErrNoAvailImpl) {
		return nil, s.backendError(err)
	}
	return kr, err
}

func (s Store) backendError(err error) error {
	if err == nil {
		return nil
	}
	if s.unavailableErr == nil {
		s.unavailableErr = secretstore.ErrUnavailable
	}
	return fmt.Errorf("%w: %w", s.unavailableErr, err)
}
