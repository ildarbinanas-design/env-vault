package keyring

import (
	"encoding/json"
	"errors"

	"github.com/99designs/keyring"
	"github.com/godbus/dbus"

	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
)

// The legacy KWallet adapter preserves 99designs/keyring's wallet, folder,
// application ID, and JSON encoding. KWallet reports many refusals as integer
// results, not D-Bus errors; every handle and mutation status must be checked.
// Only platform_linux.go connects this adapter to a desktop service. Keeping
// the protocol code portable lets fake replies exercise it on every test OS.
type kwallet struct {
	name, appID, folder string
	handle              int32
	call                func(string, ...any) *dbus.Call
}

func newKWallet(cfg keyring.Config, call func(string, ...any) *dbus.Call) *kwallet {
	name, appID, folder := cfg.ServiceName, cfg.KWalletAppID, cfg.KWalletFolder
	if name == "" {
		name = "kdewallet"
	}
	if appID == "" {
		appID = "keyring"
	}
	if folder == "" {
		folder = "keyring"
	}
	return &kwallet{name: name, appID: appID, folder: folder, handle: -1, call: call}
}

func walletReply[T any](call *dbus.Call) (T, error) {
	var zero T
	if call.Err != nil {
		return zero, call.Err
	}
	if len(call.Body) != 1 {
		return zero, errors.New("invalid KWallet reply")
	}
	value, ok := call.Body[0].(T)
	if !ok {
		return zero, errors.New("invalid KWallet reply type")
	}
	return value, nil
}

func (k *kwallet) openWallet() error {
	if k.handle >= 0 {
		opened, err := walletReply[bool](k.call("isOpen", k.handle))
		if err != nil {
			return err
		}
		if opened {
			return nil
		}
	}
	k.handle = -1
	handle, err := walletReply[int32](k.call("open", k.name, int64(0), k.appID))
	if err != nil {
		return err
	}
	if handle < 0 {
		return secretstore.ErrUnreadable
	}
	k.handle = handle
	return nil
}

// Read/list replies have no status field. Check that the wallet did not close
// during the call before interpreting an empty result as an absent record.
func (k *kwallet) stillOpen() error {
	opened, err := walletReply[bool](k.call("isOpen", k.handle))
	if err != nil {
		return err
	}
	if !opened {
		return secretstore.ErrUnreadable
	}
	return nil
}

func (k *kwallet) Get(name string) (keyring.Item, error) {
	if err := k.openWallet(); err != nil {
		return keyring.Item{}, err
	}
	data, err := walletReply[[]byte](k.call("readEntry", k.handle, k.folder, name, k.appID))
	if err != nil {
		return keyring.Item{}, err
	}
	defer clear(data)
	if err := k.stillOpen(); err != nil {
		return keyring.Item{}, err
	}
	if len(data) == 0 {
		return keyring.Item{}, keyring.ErrKeyNotFound
	}
	var item keyring.Item
	if err := json.Unmarshal(data, &item); err != nil {
		return keyring.Item{}, errors.New("invalid KWallet record encoding")
	}
	item.Key = name
	return item, nil
}

func (k *kwallet) GetMetadata(string) (keyring.Metadata, error) {
	return keyring.Metadata{}, keyring.ErrMetadataNeedsCredentials
}

func (k *kwallet) Set(item keyring.Item) error {
	if err := k.openWallet(); err != nil {
		return err
	}
	data, err := json.Marshal(item)
	if err != nil {
		return errors.New("cannot encode KWallet record")
	}
	defer clear(data)
	status, err := walletReply[int32](k.call("writeEntry", k.handle, k.folder, item.Key, data, k.appID))
	if err != nil {
		return err
	}
	if status != 0 {
		return secretstore.ErrUnreadable
	}
	return nil
}

func (k *kwallet) Remove(name string) error {
	if err := k.openWallet(); err != nil {
		return err
	}
	status, err := walletReply[int32](k.call("removeEntry", k.handle, k.folder, name, k.appID))
	if err != nil {
		return err
	}
	if status == -3 {
		return keyring.ErrKeyNotFound
	}
	if status != 0 {
		return secretstore.ErrUnreadable
	}
	return nil
}

func (k *kwallet) Keys() ([]string, error) {
	if err := k.openWallet(); err != nil {
		return nil, err
	}
	keys, err := walletReply[[]string](k.call("entryList", k.handle, k.folder, k.appID))
	if err != nil {
		return nil, err
	}
	if err := k.stillOpen(); err != nil {
		return nil, err
	}
	return keys, nil
}
