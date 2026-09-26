package keyring

import (
	"context"
	stderrors "errors"
	"fmt"
	"runtime"
	"slices"
	"sort"
	"time"

	"github.com/99designs/keyring"

	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
)

// backendTimeout bounds every backend call. A system prompt that nobody
// answers, such as a macOS Keychain prompt shown to a LaunchAgent after an
// upgrade or a locked Secret Service collection, would otherwise block the
// command forever.
var backendTimeout = 2 * time.Minute

type Store struct {
	allowedBackends []keyring.BackendType
	unavailableErr  error
	passCmd         string
	passDir         string
	openKeyring     func(keyring.Config) (keyring.Keyring, error)
	notFoundCheck   *bool
}

// withTimeout runs call and gives up after backendTimeout. The abandoned call
// keeps running in the background, and a backend helper such as pass or gpg
// may still finish its work after the command has reported the timeout.
func withTimeout[T any](call func() (T, error)) (T, error) {
	type result struct {
		value T
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, err := call()
		done <- result{value, err}
	}()
	timer := time.NewTimer(backendTimeout)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.value, r.err
	case <-timer.C:
		var zero T
		return zero, secretstore.ErrTimeout
	}
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
	if err := secretstore.ValidateSecretName(name); err != nil {
		return fmt.Errorf("invalid secret name: %w", err)
	}
	kr, err := s.open(service)
	if err != nil {
		return err
	}
	_, err = withTimeout(func() (struct{}, error) {
		return struct{}{}, kr.Set(keyring.Item{
			Key:         name,
			Data:        append([]byte(nil), value...),
			Label:       name,
			Description: "env-vault secret",
		})
	})
	if err != nil {
		return s.backendError(err)
	}
	return nil
}

func (s Store) Get(_ context.Context, service, name string) ([]byte, error) {
	if err := secretstore.ValidateSecretName(name); err != nil {
		return nil, fmt.Errorf("invalid secret name: %w", err)
	}
	kr, err := s.open(service)
	if err != nil {
		return nil, err
	}
	item, err := withTimeout(func() (keyring.Item, error) { return kr.Get(name) })
	if stderrors.Is(err, keyring.ErrKeyNotFound) {
		if !s.notFoundMayHideRefusal() {
			return nil, secretstore.ErrNotFound
		}
		// The macOS Keychain backend reports a denied prompt or a locked
		// keychain as "not found". A record that is still listed was refused,
		// not missing, and must not be treated as an absent optional secret.
		keys, keysErr := withTimeout(kr.Keys)
		if keysErr != nil {
			return nil, s.backendError(keysErr)
		}
		if slices.Contains(keys, name) {
			return nil, s.backendError(secretstore.ErrUnreadable)
		}
		return nil, secretstore.ErrNotFound
	}
	if err != nil {
		return nil, s.backendError(err)
	}
	return append([]byte(nil), item.Data...), nil
}

// Exists answers from the backend's key listing, the same metadata that List
// reads. Get would decrypt the value only to discard it, and on macOS it asks
// for Keychain access to the item just to report that the record exists.
func (s Store) Exists(_ context.Context, service, name string) (bool, error) {
	if err := secretstore.ValidateSecretName(name); err != nil {
		return false, fmt.Errorf("invalid secret name: %w", err)
	}
	kr, err := s.open(service)
	if err != nil {
		return false, err
	}
	keys, err := withTimeout(kr.Keys)
	if err != nil {
		return false, s.backendError(err)
	}
	return slices.Contains(keys, name), nil
}

func (s Store) Delete(_ context.Context, service, name string) error {
	if err := secretstore.ValidateSecretName(name); err != nil {
		return fmt.Errorf("invalid secret name: %w", err)
	}
	kr, err := s.open(service)
	if err != nil {
		return err
	}
	_, err = withTimeout(func() (struct{}, error) { return struct{}{}, kr.Remove(name) })
	if stderrors.Is(err, keyring.ErrKeyNotFound) {
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
	keys, err := withTimeout(kr.Keys)
	if err != nil {
		return nil, s.backendError(err)
	}
	sort.Strings(keys)
	items := make([]secretstore.Metadata, 0, len(keys))
	for _, name := range keys {
		items = append(items, secretstore.Metadata{
			Service:  service,
			Name:     name,
			RecordID: secretstore.RecordID(service, name),
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

// notFoundMayHideRefusal reports whether a "not found" from Get needs a
// second look. Only the macOS Keychain backend folds a refused or locked read
// into "not found"; on Linux an extra listing could open a Secret Service
// unlock prompt for a secret that is simply absent.
func (s Store) notFoundMayHideRefusal() bool {
	if s.notFoundCheck != nil {
		return *s.notFoundCheck
	}
	return runtime.GOOS == "darwin" && !slices.Equal(s.allowedBackends, []keyring.BackendType{keyring.PassBackend})
}

func (s Store) open(service string) (keyring.Keyring, error) {
	if err := secretstore.ValidateServiceName(service); err != nil {
		return nil, fmt.Errorf("invalid service name: %w", err)
	}
	allowed := append([]keyring.BackendType(nil), s.allowedBackends...)
	if len(allowed) == 0 {
		allowed = productionAllowedBackends()
	}
	openKeyring := s.openKeyring
	if openKeyring == nil {
		openKeyring = keyring.Open
	}
	cfg := keyring.Config{
		ServiceName:            service,
		AllowedBackends:        allowed,
		KeychainSynchronizable: false,
		PassCmd:                s.passCmd,
		PassDir:                s.passDir,
		// The pass backend ignores ServiceName and keys entries by PassPrefix.
		// Without an explicit prefix it operates on the user's entire
		// ~/.password-store, so List/Delete would enumerate and destroy
		// unrelated personal entries. Scope every operation to env-vault's own
		// namespace, per service, to keep it isolated and consistent with the
		// other backends' ServiceName scoping.
		PassPrefix: "env-vault/" + service,
	}
	kr, err := withTimeout(func() (keyring.Keyring, error) { return openKeyring(cfg) })
	if stderrors.Is(err, keyring.ErrNoAvailImpl) || stderrors.Is(err, secretstore.ErrTimeout) {
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
