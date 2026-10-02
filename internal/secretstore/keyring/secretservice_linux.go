package keyring

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/99designs/keyring"
	"github.com/godbus/dbus"
	libsecret "github.com/gsterjov/go-libsecret"

	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
)

// Keep the dependency's collection paths and JSON Item encoding. Only the
// Secret Service adapter changes: identity is (collection, profile), never Label.
// In particular, neither metadata operation opens a secret session or GetSecret.
type secretService struct {
	service      *libsecret.Service
	conn         *dbus.Conn
	name         string
	property     func(dbus.ObjectPath, string) (dbus.Variant, error)
	unlockObject func(libsecret.DBusObject) error
}

func openPlatform(cfg keyring.Config) (keyring.Keyring, error) {
	for _, backend := range cfg.AllowedBackends {
		one := cfg
		one.AllowedBackends = []keyring.BackendType{backend}
		if backend != keyring.SecretServiceBackend {
			if ring, err := keyring.Open(one); err == nil {
				return ring, nil
			}
			continue
		}
		ring, err := openSecretService(cfg.ServiceName)
		if err == nil {
			return ring, nil
		}
		if !errors.Is(err, keyring.ErrNoAvailImpl) {
			return nil, err
		}
	}
	return nil, keyring.ErrNoAvailImpl
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
	s.property = func(path dbus.ObjectPath, property string) (dbus.Variant, error) {
		return conn.Object(libsecret.DBusServiceName, path).GetProperty(property)
	}
	// Probe availability without swallowing permission/metadata errors and
	// falling back to a different store that would appear empty.
	if _, err := s.collection(); err != nil {
		var busErr dbus.Error
		if errors.As(err, &busErr) && (busErr.Name == "org.freedesktop.DBus.Error.ServiceUnknown" || busErr.Name == "org.freedesktop.DBus.Error.NameHasNoOwner") {
			return nil, keyring.ErrNoAvailImpl
		}
		return nil, err
	}
	return s, nil
}

func decodeCollectionPath(src string) string {
	var out strings.Builder
	for i := 0; i < len(src); i++ {
		if src[i] == '_' && i+2 < len(src) {
			if b, err := hex.DecodeString(src[i+1 : i+3]); err == nil {
				out.Write(b)
				i += 2
				continue
			}
		}
		out.WriteByte(src[i])
	}
	return out.String()
}

func (s *secretService) collection() (*libsecret.Collection, error) {
	value, err := s.property(dbus.ObjectPath(libsecret.DBusPath), "org.freedesktop.Secret.Service.Collections")
	if err != nil {
		return nil, err
	}
	paths, ok := value.Value().([]dbus.ObjectPath)
	if !ok {
		return nil, errors.New("invalid Secret Service collection metadata")
	}
	var found *libsecret.Collection
	for _, path := range paths {
		if decodeCollectionPath(string(path)) == libsecret.DBusPath+"/collection/"+s.name {
			if found != nil {
				return nil, secretstore.ErrAmbiguous
			}
			found = libsecret.NewCollection(s.conn, path)
		}
	}
	return found, nil
}

func (s *secretService) unlock(object libsecret.DBusObject, kind string) error {
	property := "org.freedesktop.Secret." + kind + ".Locked"
	value, err := s.property(object.Path(), property)
	if err != nil {
		return err
	}
	locked, ok := value.Value().(bool)
	if !ok {
		return errors.New("invalid Secret Service lock metadata")
	}
	if !locked {
		return nil
	}
	if err := s.unlockObject(object); err != nil {
		return err
	}
	value, err = s.property(object.Path(), property)
	if err != nil {
		return err
	}
	locked, ok = value.Value().(bool)
	if !ok || locked {
		return secretstore.ErrUnreadable
	}
	return nil
}

func (s *secretService) records(collection *libsecret.Collection) (map[string]dbus.ObjectPath, error) {
	records := make(map[string]dbus.ObjectPath)
	if collection == nil {
		return records, nil
	}
	if err := s.unlock(collection, "Collection"); err != nil {
		return nil, err
	}
	value, err := s.property(collection.Path(), "org.freedesktop.Secret.Collection.Items")
	if err != nil {
		return nil, err
	}
	paths, ok := value.Value().([]dbus.ObjectPath)
	if !ok {
		return nil, errors.New("invalid Secret Service item metadata")
	}
	for _, path := range paths {
		value, err := s.property(path, "org.freedesktop.Secret.Item.Attributes")
		if err != nil {
			return nil, err
		}
		attrs, ok := value.Value().(map[string]string)
		if !ok {
			return nil, errors.New("invalid Secret Service attributes")
		}
		name, present := attrs["profile"]
		if !present {
			continue
		}
		if err := secretstore.ValidateSecretName(name); err != nil {
			return nil, fmt.Errorf("invalid Secret Service profile attribute: %w", err)
		}
		if _, exists := records[name]; exists {
			return nil, secretstore.ErrAmbiguous
		}
		records[name] = path
	}
	return records, nil
}

func (s *secretService) lookup(name string) (*libsecret.Item, error) {
	collection, err := s.collection()
	if err != nil {
		return nil, err
	}
	records, err := s.records(collection)
	if err != nil {
		return nil, err
	}
	path, ok := records[name]
	if !ok {
		return nil, keyring.ErrKeyNotFound
	}
	return libsecret.NewItem(s.conn, path), nil
}

func (s *secretService) Keys() ([]string, error) {
	collection, err := s.collection()
	if err != nil {
		return nil, err
	}
	records, err := s.records(collection)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(records))
	for name := range records {
		keys = append(keys, name)
	}
	return keys, nil
}
func (s *secretService) GetMetadata(string) (keyring.Metadata, error) {
	return keyring.Metadata{}, keyring.ErrMetadataNotSupported
}
func (s *secretService) Get(name string) (keyring.Item, error) {
	item, err := s.lookup(name)
	if err != nil {
		return keyring.Item{}, err
	}
	if err := s.unlock(item, "Item"); err != nil {
		return keyring.Item{}, err
	}
	session, err := s.service.Open()
	if err != nil {
		return keyring.Item{}, err
	}
	defer s.closeSession(session)
	secret, err := item.GetSecret(session)
	if err != nil {
		return keyring.Item{}, err
	}
	defer clear(secret.Value)
	var result keyring.Item
	if err := json.Unmarshal(secret.Value, &result); err != nil {
		return keyring.Item{}, errors.New("invalid Secret Service record encoding")
	}
	// Metadata is authoritative even for old records whose label was changed.
	result.Key = name
	return result, nil
}
func (s *secretService) Set(item keyring.Item) error {
	collection, err := s.collection()
	if err != nil {
		return err
	}
	records, err := s.records(collection)
	if err != nil {
		return err
	}
	if collection == nil {
		collection, err = s.service.CreateCollection(s.name)
		if err != nil {
			return err
		}
	}
	if err := s.unlock(collection, "Collection"); err != nil {
		return err
	}
	session, err := s.service.Open()
	if err != nil {
		return err
	}
	defer s.closeSession(session)
	data, err := json.Marshal(item)
	if err != nil {
		return errors.New("cannot encode Secret Service record")
	}
	defer clear(data)
	secret := libsecret.NewSecret(session, []byte{}, data, "application/json")
	if path, exists := records[item.Key]; exists {
		// Update the exact physical record checked above; do not let CreateItem's
		// replace flag choose among records with additional attributes.
		existing := libsecret.NewItem(s.conn, path)
		if err := s.unlock(existing, "Item"); err != nil {
			return err
		}
		return s.conn.Object(libsecret.DBusServiceName, path).Call("org.freedesktop.Secret.Item.SetSecret", 0, secret).Err
	}
	_, err = collection.CreateItem(item.Key, secret, false)
	return err
}
func (s *secretService) Remove(name string) error {
	item, err := s.lookup(name)
	if err != nil {
		return err
	}
	if err := s.unlock(item, "Item"); err != nil {
		return err
	}
	return item.Delete()
}
func (s *secretService) closeSession(session *libsecret.Session) {
	_ = s.conn.Object(libsecret.DBusServiceName, session.Path()).Call("org.freedesktop.Secret.Session.Close", 0).Err
}
