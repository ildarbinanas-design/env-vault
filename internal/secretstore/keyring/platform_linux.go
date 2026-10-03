package keyring

import (
	"errors"
	"os"

	"github.com/99designs/keyring"
	"github.com/godbus/dbus"
	libsecret "github.com/gsterjov/go-libsecret"
)

func openPlatform(cfg keyring.Config) (keyring.Keyring, error) {
	for _, backend := range cfg.AllowedBackends {
		var ring keyring.Keyring
		var err error
		switch backend {
		case keyring.SecretServiceBackend:
			ring, err = openSecretService(cfg.ServiceName)
		case keyring.KWalletBackend:
			ring, err = openKWallet(cfg)
		default:
			one := cfg
			one.AllowedBackends = []keyring.BackendType{backend}
			ring, err = keyring.Open(one)
			// Preserve the dependency's fallback for the remaining backends.
			if err != nil {
				continue
			}
		}
		if err == nil {
			return ring, nil
		}
		// A present but refused wallet is never grounds to choose another store.
		if !errors.Is(err, keyring.ErrNoAvailImpl) {
			return nil, err
		}
	}
	return nil, keyring.ErrNoAvailImpl
}

func unavailableDBusService(err error) bool {
	var busErr dbus.Error
	return errors.As(err, &busErr) && (busErr.Name == "org.freedesktop.DBus.Error.ServiceUnknown" || busErr.Name == "org.freedesktop.DBus.Error.NameHasNoOwner")
}

func openKWallet(cfg keyring.Config) (*kwallet, error) {
	if os.Getenv("DISABLE_KWALLET") == "1" {
		return nil, keyring.ErrNoAvailImpl
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, keyring.ErrNoAvailImpl
	}
	object := conn.Object("org.kde.kwalletd5", "/modules/kwalletd5")
	ring := newKWallet(cfg, func(method string, args ...any) *dbus.Call { return object.Call("org.kde.KWallet."+method, 0, args...) })
	if err := ring.openWallet(); err != nil {
		if unavailableDBusService(err) {
			return nil, keyring.ErrNoAvailImpl
		}
		return nil, err
	}
	return ring, nil
}

func openSecretService(name string) (*secretService, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, keyring.ErrNoAvailImpl
	}
	service, err := libsecret.NewService()
	if err != nil {
		return nil, err
	}
	s := &secretService{service: service, conn: conn, name: name, unlockObject: service.Unlock}
	object := conn.Object(libsecret.DBusServiceName, libsecret.DBusPath)
	s.readAlias = func(alias string) (dbus.ObjectPath, error) {
		var path dbus.ObjectPath
		err := object.Call("org.freedesktop.Secret.Service.ReadAlias", 0, alias).Store(&path)
		return path, err
	}
	s.setAlias = func(alias string, path dbus.ObjectPath) error {
		return object.Call("org.freedesktop.Secret.Service.SetAlias", 0, alias, path).Err
	}
	s.property = func(path dbus.ObjectPath, property string) (dbus.Variant, error) {
		return conn.Object(libsecret.DBusServiceName, path).GetProperty(property)
	}
	// Probe availability without swallowing permission/metadata errors and
	// falling back to a different store that would appear empty.
	if _, err := s.collection(); err != nil {
		if unavailableDBusService(err) {
			return nil, keyring.ErrNoAvailImpl
		}
		return nil, err
	}
	return s, nil
}
