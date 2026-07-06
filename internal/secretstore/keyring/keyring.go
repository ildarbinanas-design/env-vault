package keyring

import (
	"context"
	stderrors "errors"
	"sort"

	"github.com/99designs/keyring"

	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
)

type Store struct{}

func New() Store {
	return Store{}
}

func (s Store) Set(_ context.Context, service, name string, value []byte) error {
	kr, err := open(service)
	if err != nil {
		return err
	}
	return kr.Set(keyring.Item{
		Key:         name,
		Data:        append([]byte(nil), value...),
		Label:       name,
		Description: "env-vault secret",
	})
}

func (s Store) Get(_ context.Context, service, name string) ([]byte, error) {
	kr, err := open(service)
	if err != nil {
		return nil, err
	}
	item, err := kr.Get(name)
	if stderrors.Is(err, keyring.ErrKeyNotFound) {
		return nil, secretstore.ErrNotFound
	}
	if err != nil {
		return nil, err
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
	kr, err := open(service)
	if err != nil {
		return err
	}
	if err := kr.Remove(name); stderrors.Is(err, keyring.ErrKeyNotFound) {
		return secretstore.ErrNotFound
	} else if err != nil {
		return err
	}
	return nil
}

func (s Store) List(_ context.Context, service string) ([]secretstore.Metadata, error) {
	kr, err := open(service)
	if err != nil {
		return nil, err
	}
	keys, err := kr.Keys()
	if err != nil {
		return nil, err
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

func open(service string) (keyring.Keyring, error) {
	kr, err := keyring.Open(keyring.Config{
		ServiceName: service,
		AllowedBackends: []keyring.BackendType{
			keyring.KeychainBackend,
			keyring.SecretServiceBackend,
			keyring.KWalletBackend,
			keyring.WinCredBackend,
		},
		KeychainSynchronizable: false,
	})
	if stderrors.Is(err, keyring.ErrNoAvailImpl) {
		return nil, secretstore.ErrUnavailable
	}
	return kr, err
}
