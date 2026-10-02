package keyring

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/99designs/keyring"
	"github.com/godbus/dbus"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

func metadataService(t *testing.T, attributes ...map[string]string) *secretService {
	t.Helper()
	collection := dbus.ObjectPath("/org/freedesktop/secrets/collection/test")
	paths := []dbus.ObjectPath{}
	for i := range attributes {
		paths = append(paths, dbus.ObjectPath(string(collection)+"/"+string(rune('a'+i))))
	}
	return &secretService{name: "test", property: func(path dbus.ObjectPath, property string) (dbus.Variant, error) {
		switch property {
		case "org.freedesktop.Secret.Service.Collections":
			return dbus.MakeVariant([]dbus.ObjectPath{collection}), nil
		case "org.freedesktop.Secret.Collection.Locked":
			return dbus.MakeVariant(false), nil
		case "org.freedesktop.Secret.Collection.Items":
			return dbus.MakeVariant(paths), nil
		case "org.freedesktop.Secret.Item.Attributes":
			i := slices.Index(paths, path)
			if i >= 0 {
				return dbus.MakeVariant(attributes[i]), nil
			}
		}
		t.Errorf("unexpected metadata property %s", property)
		return dbus.Variant{}, errors.New("unexpected property")
	}}
}
func serviceStore(service *secretService) Store {
	return Store{openKeyring: func(keyring.Config) (keyring.Keyring, error) { return service, nil }}
}
func TestSecretServiceMetadataIdentity(t *testing.T) {
	service := metadataService(t, map[string]string{"profile": "actual"}, map[string]string{"foreign": "label-only"})
	store := serviceStore(service)
	ctx := context.Background()
	// No connection or secret session exists: these operations must use metadata alone.
	keys, err := store.List(ctx, "test")
	if err != nil || len(keys) != 1 || keys[0].Name != "actual" {
		t.Fatal("listing did not use profile attributes")
	}
	for _, name := range []string{"label-only", "absent"} {
		exists, err := store.Exists(ctx, "test", name)
		if err != nil || exists {
			t.Fatal("foreign or missing record exists")
		}
		if _, err := store.Get(ctx, "test", name); !errors.Is(err, secretstore.ErrNotFound) {
			t.Fatal("missing read did not report absence")
		}
		if err := store.Delete(ctx, "test", name); !errors.Is(err, secretstore.ErrNotFound) {
			t.Fatal("missing delete did not report absence")
		}
	}
}
func TestSecretServiceDuplicateRefusesEveryOperation(t *testing.T) {
	store := serviceStore(metadataService(t, map[string]string{"profile": "token"}, map[string]string{"profile": "token", "extra": "kept"}))
	ctx := context.Background()
	value := []byte(testutil.EphemeralValue(t))
	tests := map[string]func() error{
		"set":    func() error { return store.Set(ctx, "test", "token", value) },
		"get":    func() error { _, err := store.Get(ctx, "test", "token"); return err },
		"exists": func() error { _, err := store.Exists(ctx, "test", "token"); return err },
		"list":   func() error { _, err := store.List(ctx, "test"); return err },
		"delete": func() error { return store.Delete(ctx, "test", "token") },
	}
	for name, run := range tests {
		t.Run(name, func(t *testing.T) {
			err := run()
			if !errors.Is(err, secretstore.ErrAmbiguous) || !errors.Is(err, secretstore.ErrUnavailable) {
				t.Fatal("duplicate identity did not fail closed")
			}
		})
	}
}
func TestSecretServiceMetadataFailuresAndDeadline(t *testing.T) {
	for _, property := range []string{"org.freedesktop.Secret.Service.Collections", "org.freedesktop.Secret.Collection.Locked", "org.freedesktop.Secret.Collection.Items", "org.freedesktop.Secret.Item.Attributes"} {
		t.Run(property, func(t *testing.T) {
			service := metadataService(t, map[string]string{"profile": "token"})
			original := service.property
			service.property = func(path dbus.ObjectPath, name string) (dbus.Variant, error) {
				if name == property {
					return dbus.Variant{}, dbus.Error{Name: "org.freedesktop.DBus.Error.AccessDenied"}
				}
				return original(path, name)
			}
			store := serviceStore(service)
			if exists, err := store.Exists(context.Background(), "test", "token"); exists || !errors.Is(err, secretstore.ErrUnavailable) || errors.Is(err, secretstore.ErrNotFound) {
				t.Fatal("metadata failure was treated as absence")
			}
			if _, err := store.List(context.Background(), "test"); !errors.Is(err, secretstore.ErrUnavailable) {
				t.Fatal("listing swallowed metadata failure")
			}
		})
	}
	service := metadataService(t)
	release := make(chan struct{})
	defer close(release)
	service.property = func(dbus.ObjectPath, string) (dbus.Variant, error) {
		<-release
		return dbus.Variant{}, errors.New("released")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := serviceStore(service).List(ctx, "test"); !errors.Is(err, secretstore.ErrTimeout) {
		t.Fatal("deadline did not stop a metadata call")
	}
}
func TestSecretServiceCollectionPathCompatibility(t *testing.T) {
	if decodeCollectionPath("/org/freedesktop/secrets/collection/env_2dvault") != "/org/freedesktop/secrets/collection/env-vault" {
		t.Fatal("legacy collection path changed")
	}
}
