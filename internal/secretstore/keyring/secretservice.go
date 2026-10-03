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

// Keep the dependency's JSON Item encoding and profile attributes. Collection
// aliases (where supported) and API labels identify the service; object paths
// are opaque. Item labels never identify records. Metadata operations do not
// open a secret session or call GetSecret.
type secretService struct {
	service      *libsecret.Service
	conn         *dbus.Conn
	name         string
	property     func(dbus.ObjectPath, string) (dbus.Variant, error)
	unlockObject func(libsecret.DBusObject) error
	readAlias    func(string) (dbus.ObjectPath, error)
	setAlias     func(string, dbus.ObjectPath) error
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

// Encode every byte: KDE preserves literal underscores in object paths, while
// GNOME escapes them. In particular, team-ab and team_2dab need distinct aliases.
func (s *secretService) alias() string { return "env_vault_" + hex.EncodeToString([]byte(s.name)) }

func (s *secretService) collection() (*libsecret.Collection, error) {
	path, err := s.readAlias(s.alias())
	if err != nil {
		return nil, err
	}
	value, err := s.property(dbus.ObjectPath(libsecret.DBusPath), "org.freedesktop.Secret.Service.Collections")
	if err != nil {
		return nil, err
	}
	paths, ok := value.Value().([]dbus.ObjectPath)
	if !ok {
		return nil, errors.New("invalid Secret Service collection metadata")
	}
	if path != "/" {
		for _, candidate := range paths {
			if candidate == path {
				return libsecret.NewCollection(s.conn, path), nil
			}
		}
		return nil, errors.New("Secret Service alias refers to an unavailable collection")
	}
	// The old adapter created collections using ServiceName as their label but
	// never set an alias. Read that label through the API: interpreting path
	// escapes confuses KDE literal _HH with another service and misses suffixes
	// assigned to colliding collection paths.
	var found *libsecret.Collection
	var legacyPaths []dbus.ObjectPath
	for _, path := range paths {
		value, err := s.property(path, "org.freedesktop.Secret.Collection.Label")
		if err != nil {
			return nil, err
		}
		label, ok := value.Value().(string)
		if !ok {
			return nil, errors.New("invalid Secret Service collection label")
		}
		if label == s.name {
			if found != nil {
				return nil, secretstore.ErrAmbiguous
			}
			found = libsecret.NewCollection(s.conn, path)
		}
		oldPath := libsecret.DBusPath + "/collection/" + s.name
		if string(path) == oldPath || decodeCollectionPath(string(path)) == oldPath {
			legacyPaths = append(legacyPaths, path)
		}
	}
	for _, legacyPath := range legacyPaths {
		if found == nil || found.Path() != legacyPath {
			// A renamed legacy GNOME collection and a literal KDE name can look
			// identical, even when another collection has the requested label.
			// The label alone cannot authorize switching namespaces. Require the
			// owner to resolve the conflict, or an explicit alias to select it.
			return nil, secretstore.ErrAmbiguous
		}
	}
	return found, nil
}

func (s *secretService) bindCollection(collection *libsecret.Collection) error {
	alias := s.alias()
	path, err := s.readAlias(alias)
	if err != nil {
		return err
	}
	if path == collection.Path() {
		return nil
	}
	if path != "/" {
		return secretstore.ErrAmbiguous
	}
	if err := s.setAlias(alias, collection.Path()); err != nil {
		var busErr dbus.Error
		// GNOME only supports setting the default alias. Keep its existing
		// collections usable, with the same fail-closed label resolution above.
		if errors.As(err, &busErr) && busErr.Name == "org.freedesktop.DBus.Error.NotSupported" {
			return nil
		}
		return err
	}
	// KDE can return success without changing an alias when the collection no
	// longer exists. Verify before touching a secret; never retry the mutation.
	path, err = s.readAlias(alias)
	if err != nil {
		return err
	}
	if path != collection.Path() {
		return secretstore.ErrAmbiguous
	}
	return nil
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
	if err := s.bindCollection(collection); err != nil {
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
