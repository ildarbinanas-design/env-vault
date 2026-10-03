package keyring

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/99designs/keyring"
	"github.com/godbus/dbus"
	libsecret "github.com/gsterjov/go-libsecret"
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
	return &secretService{name: "test", readAlias: func(string) (dbus.ObjectPath, error) { return "/", nil }, property: func(path dbus.ObjectPath, property string) (dbus.Variant, error) {
		switch property {
		case "org.freedesktop.Secret.Service.Collections":
			return dbus.MakeVariant([]dbus.ObjectPath{collection}), nil
		case "org.freedesktop.Secret.Collection.Label":
			return dbus.MakeVariant("test"), nil
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
	for _, property := range []string{"org.freedesktop.Secret.Service.Collections", "org.freedesktop.Secret.Collection.Label", "org.freedesktop.Secret.Collection.Locked", "org.freedesktop.Secret.Collection.Items", "org.freedesktop.Secret.Item.Attributes"} {
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

func TestSecretServiceLockedOrDeniedIsNotAbsence(t *testing.T) {
	for _, denied := range []bool{false, true} {
		service := metadataService(t, map[string]string{"profile": "token"})
		original := service.property
		service.property = func(path dbus.ObjectPath, property string) (dbus.Variant, error) {
			if property == "org.freedesktop.Secret.Collection.Locked" {
				return dbus.MakeVariant(true), nil
			}
			return original(path, property)
		}
		calls := 0
		service.unlockObject = func(libsecret.DBusObject) error {
			calls++
			if denied {
				return dbus.Error{Name: "org.freedesktop.DBus.Error.AccessDenied"}
			}
			return nil // A dismissed prompt may leave the collection locked.
		}
		store := serviceStore(service)
		exists, err := store.Exists(context.Background(), "test", "token")
		if exists || !errors.Is(err, secretstore.ErrUnavailable) || errors.Is(err, secretstore.ErrNotFound) || calls != 1 {
			t.Fatal("a locked or denied collection was treated as absent")
		}
		if !denied && !errors.Is(err, secretstore.ErrUnreadable) {
			t.Fatal("dismissed unlock was treated as success")
		}
	}
}

func collectionService(labels map[dbus.ObjectPath]string) *secretService {
	return &secretService{
		readAlias: func(string) (dbus.ObjectPath, error) { return "/", nil },
		property: func(path dbus.ObjectPath, property string) (dbus.Variant, error) {
			switch property {
			case "org.freedesktop.Secret.Service.Collections":
				paths := make([]dbus.ObjectPath, 0, len(labels))
				for path := range labels {
					paths = append(paths, path)
				}
				return dbus.MakeVariant(paths), nil
			case "org.freedesktop.Secret.Collection.Label":
				return dbus.MakeVariant(labels[path]), nil
			}
			return dbus.Variant{}, errors.New("unexpected property")
		},
	}
}

func TestSecretServiceCollectionNamespace(t *testing.T) {
	const base = "/org/freedesktop/secrets/collection/"
	tests := []struct {
		name      string
		service   string
		labels    map[dbus.ObjectPath]string
		want      dbus.ObjectPath
		ambiguous bool
	}{
		{"gnome-hyphen", "team-ab", map[dbus.ObjectPath]string{base + "team_2dab": "team-ab"}, base + "team_2dab", false},
		{"gnome-underscore", "team_ab", map[dbus.ObjectPath]string{base + "team_5fab": "team_ab"}, base + "team_5fab", false},
		{"gnome-literal-escape", "team_2dab", map[dbus.ObjectPath]string{base + "team_5f2dab": "team_2dab"}, base + "team_5f2dab", false},
		{"kde-underscore", "team_ab", map[dbus.ObjectPath]string{base + "team_ab": "team_ab"}, base + "team_ab", false},
		{"kde-literal-escape", "team_2dab", map[dbus.ObjectPath]string{base + "team_2dab": "team_2dab"}, base + "team_2dab", false},
		{"kde-collision-needs-alias", "team-ab", map[dbus.ObjectPath]string{base + "team_2dab": "team_2dab", base + "team_2dab0": "team-ab"}, "", true},
		{"kde-collision-reversed-needs-alias", "team_2dab", map[dbus.ObjectPath]string{base + "team_2dab": "team-ab", base + "team_2dab0": "team_2dab"}, "", true},
		{"renamed-legacy-competes-with-exact-label", "team-ab", map[dbus.ObjectPath]string{base + "team_2dab": "renamed", base + "other": "team-ab"}, "", true},
		{"raw-legacy-competes-with-exact-label", "team_ab", map[dbus.ObjectPath]string{base + "team_ab": "renamed", base + "other": "team_ab"}, "", true},
		{"two-plausible-legacy-paths", "team_2dab", map[dbus.ObjectPath]string{base + "team_2dab": "team_2dab", base + "team_5f2dab": "renamed"}, "", true},
		{"opaque-path", "team-ab", map[dbus.ObjectPath]string{base + "42": "team-ab"}, base + "42", false},
		{"foreign-escape-is-never-selected", "team-ab", map[dbus.ObjectPath]string{base + "team_2dab": "team_2dab"}, "", true},
		{"legacy-renamed-is-not-absence", "team-ab", map[dbus.ObjectPath]string{base + "team_2dab": "renamed"}, "", true},
		{"duplicate-label", "team-ab", map[dbus.ObjectPath]string{base + "team_2dab": "team-ab", base + "team_2dab0": "team-ab"}, "", true},
		{"absent", "team-ab", map[dbus.ObjectPath]string{base + "other": "other"}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := collectionService(tt.labels)
			service.name = tt.service
			collection, err := service.collection()
			if tt.ambiguous {
				if !errors.Is(err, secretstore.ErrAmbiguous) || collection != nil {
					t.Fatal("ambiguous namespace was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.want == "" {
				if collection != nil {
					t.Fatal("selected a foreign collection")
				}
			} else if collection == nil || collection.Path() != tt.want {
				t.Fatal("selected the wrong collection")
			}
		})
	}
}

func TestSecretServiceAliasIdentity(t *testing.T) {
	path := dbus.ObjectPath("/org/freedesktop/secrets/collection/opaque")
	service := collectionService(map[dbus.ObjectPath]string{path: "renamed"})
	service.name = "team_2dab"
	alias := service.alias()
	service.name = "team-ab"
	if service.alias() == alias {
		t.Fatal("colliding service aliases")
	}
	service.readAlias = func(got string) (dbus.ObjectPath, error) {
		if got != service.alias() {
			t.Fatal("wrong alias")
		}
		return path, nil
	}
	service.setAlias = func(string, dbus.ObjectPath) error { t.Fatal("read changed alias"); return nil }
	collection, err := service.collection()
	if err != nil || collection == nil || collection.Path() != path {
		t.Fatal("alias did not preserve renamed collection identity")
	}
	// An alias must point to a currently advertised collection.
	service.readAlias = func(string) (dbus.ObjectPath, error) { return "/org/freedesktop/secrets/collection/missing", nil }
	if _, err := service.collection(); err == nil {
		t.Fatal("dangling alias accepted")
	}
	service.readAlias = func(string) (dbus.ObjectPath, error) {
		return "", dbus.Error{Name: "org.freedesktop.DBus.Error.AccessDenied"}
	}
	if _, err := service.collection(); err == nil {
		t.Fatal("alias refusal was treated as absence")
	}
}

func TestSecretServiceBindCollection(t *testing.T) {
	path := dbus.ObjectPath("/org/freedesktop/secrets/collection/test")
	for _, mode := range []string{"success", "existing", "not-supported", "denied", "silent-failure", "conflict", "read-denied"} {
		t.Run(mode, func(t *testing.T) {
			service := collectionService(map[dbus.ObjectPath]string{path: "test"})
			service.name = "test"
			alias := dbus.ObjectPath("/")
			if mode == "existing" {
				alias = path
			}
			if mode == "conflict" {
				alias = "/org/freedesktop/secrets/collection/other"
			}
			writes := 0
			service.readAlias = func(string) (dbus.ObjectPath, error) {
				if mode == "read-denied" {
					return "", dbus.Error{Name: "org.freedesktop.DBus.Error.AccessDenied"}
				}
				return alias, nil
			}
			service.setAlias = func(_ string, got dbus.ObjectPath) error {
				writes++
				if got != path {
					t.Fatal("bound the wrong collection")
				}
				switch mode {
				case "not-supported":
					return dbus.Error{Name: "org.freedesktop.DBus.Error.NotSupported"}
				case "denied":
					return dbus.Error{Name: "org.freedesktop.DBus.Error.AccessDenied"}
				case "silent-failure":
					return nil
				}
				alias = got
				return nil
			}
			err := service.bindCollection(libsecret.NewCollection(nil, path))
			success := mode == "success" || mode == "existing" || mode == "not-supported"
			if (err == nil) != success {
				t.Fatal("incorrect alias binding result")
			}
			if writes > 1 {
				t.Fatal("alias mutation retried")
			}
			if (mode == "existing" || mode == "conflict" || mode == "read-denied") && writes != 0 {
				t.Fatal("existing alias changed")
			}
		})
	}
}

func TestSecretServiceAmbiguousCollectionRefusesEveryOperation(t *testing.T) {
	const base = "/org/freedesktop/secrets/collection/"
	for scenario, labels := range map[string]map[dbus.ObjectPath]string{
		"foreign-literal-escape":          {base + "team_2dab": "team_2dab"},
		"renamed-legacy-with-exact-label": {base + "team_2dab": "renamed", base + "other": "team-ab"},
		// This is both a valid KDE collision and a GNOME collection renamed to
		// the literal escaped name. Only an explicit alias can disambiguate it.
		"literal-escape-with-exact-label": {base + "team_2dab": "team_2dab", base + "other": "team-ab"},
	} {
		t.Run(scenario, func(t *testing.T) {
			service := collectionService(labels)
			service.name = "team-ab"
			store := serviceStore(service)
			value := []byte(testutil.EphemeralValue(t))
			defer clear(value)
			ctx := context.Background()
			for name, operation := range map[string]func() error{
				"set":    func() error { return store.Set(ctx, service.name, "token", value) },
				"get":    func() error { _, err := store.Get(ctx, service.name, "token"); return err },
				"delete": func() error { return store.Delete(ctx, service.name, "token") },
				"exists": func() error { _, err := store.Exists(ctx, service.name, "token"); return err },
				"list":   func() error { _, err := store.List(ctx, service.name); return err },
			} {
				t.Run(name, func(t *testing.T) {
					if !errors.Is(operation(), secretstore.ErrAmbiguous) {
						t.Fatal("ambiguous namespace reached backend operation")
					}
				})
			}
		})
	}
}

func TestSecretServiceExplicitAliasResolvesCompetingLegacyPaths(t *testing.T) {
	const base = "/org/freedesktop/secrets/collection/"
	paths := []dbus.ObjectPath{base + "team_2dab", base + "team_2dab0"}
	labels := map[dbus.ObjectPath]string{paths[0]: "team_2dab", paths[1]: "team-ab"}
	for _, path := range paths {
		t.Run(labels[path], func(t *testing.T) {
			service := collectionService(labels)
			service.name = labels[path]
			service.readAlias = func(string) (dbus.ObjectPath, error) { return path, nil }
			collection, err := service.collection()
			if err != nil || collection == nil || collection.Path() != path {
				t.Fatal("explicit alias did not resolve collection identity")
			}
		})
	}
}

func TestSecretServiceAliasFailureStopsBeforeSecretWrite(t *testing.T) {
	service := metadataService(t, map[string]string{"profile": "token"})
	service.setAlias = func(string, dbus.ObjectPath) error {
		return dbus.Error{Name: "org.freedesktop.DBus.Error.AccessDenied"}
	}
	value := []byte(testutil.EphemeralValue(t))
	defer clear(value)
	// No session service or connection is installed. An attempt to encode/send
	// the secret after the refused identity binding would fail this test.
	err := serviceStore(service).Set(context.Background(), "test", "token", value)
	if !errors.Is(err, secretstore.ErrUnavailable) {
		t.Fatal("refused alias reached secret write")
	}
}
