package keyring

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/99designs/keyring"
	"github.com/godbus/dbus"
	libsecret "github.com/gsterjov/go-libsecret"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

// This gate is set only inside scripts/secret-service-ci.sh's disposable
// Debian container and private D-Bus session. Never use a desktop collection.
func TestSecretServiceDisposableIntegration(t *testing.T) {
	if os.Getenv("ENV_VAULT_SS_DISPOSABLE") != "1" {
		t.Skip("requires isolated Debian Secret Service scenario")
	}
	cli := os.Getenv("ENV_VAULT_TEST_CLI")
	if cli == "" || os.Getenv("XDG_DATA_HOME") == "" {
		t.Fatal("missing isolated scenario prerequisites")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	conn, err := dbus.SessionBus()
	if err != nil {
		t.Fatal("private D-Bus session unavailable")
	}
	for {
		var owner bool
		err = conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.NameHasOwner", 0, libsecret.DBusServiceName).Store(&owner)
		if err == nil && owner {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("GNOME Keyring never acquired Secret Service")
		case <-time.After(20 * time.Millisecond):
		}
	}
	service, err := libsecret.NewService()
	if err != nil {
		t.Fatal(err)
	}
	session, err := service.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Object(libsecret.DBusServiceName, session.Path()).Call("org.freedesktop.Secret.Session.Close", 0)
	master := []byte(rand.Text())
	defer clear(master)
	var path dbus.ObjectPath
	properties := map[string]dbus.Variant{"org.freedesktop.Secret.Collection.Label": dbus.MakeVariant(secretstore.DefaultService)}
	object := conn.Object(libsecret.DBusServiceName, libsecret.DBusPath)
	err = object.CallWithContext(ctx, "org.gnome.keyring.InternalUnsupportedGuiltRiddenInterface.CreateWithMasterPassword", 0, properties, libsecret.NewSecret(session, []byte{}, master, "text/plain")).Store(&path)
	if err != nil {
		t.Fatal("cannot create disposable collection")
	}
	collection := libsecret.NewCollection(conn, path)
	t.Cleanup(func() {
		if err := collection.Delete(); err != nil {
			t.Error("cannot delete disposable collection")
		}
	})
	err = object.CallWithContext(ctx, "org.gnome.keyring.InternalUnsupportedGuiltRiddenInterface.UnlockWithMasterPassword", 0, path, libsecret.NewSecret(session, []byte{}, master, "text/plain")).Err
	if err != nil {
		t.Fatal("cannot unlock disposable collection")
	}
	locked, err := collection.Locked()
	if err != nil || locked {
		t.Fatal("disposable collection must already be unlocked")
	}
	adapter, err := openSecretService(secretstore.DefaultService)
	if err != nil {
		t.Fatal("cannot open native Secret Service adapter")
	}
	found, err := adapter.collection()
	if err != nil || found == nil || found.Path() != path {
		t.Fatal("adapter did not select the disposable D-Bus collection")
	}
	t.Log("Secret Service confirmed through D-Bus collection metadata; collection unlocked")
	store := New()
	value := []byte(testutil.EphemeralValue(t))
	second := []byte(testutil.EphemeralValue(t))
	legacy, err := keyring.Open(keyring.Config{ServiceName: secretstore.DefaultService, AllowedBackends: []keyring.BackendType{keyring.SecretServiceBackend}})
	if err != nil {
		t.Fatal("legacy adapter unavailable")
	}
	if err := legacy.Set(keyring.Item{Key: "legacy", Data: value}); err != nil {
		t.Fatal("legacy write failed")
	}
	items, err := collection.SearchItems("legacy")
	if err != nil || len(items) != 1 {
		t.Fatal("legacy metadata missing")
	}
	err = conn.Object(libsecret.DBusServiceName, items[0].Path()).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Set", 0, "org.freedesktop.Secret.Item", "Label", dbus.MakeVariant("different-label")).Err
	if err != nil {
		t.Fatal("cannot change display label")
	}
	if got, err := store.Get(ctx, secretstore.DefaultService, "legacy"); err != nil || !bytes.Equal(got, value) {
		t.Fatal("legacy record no longer readable")
	}
	foreign, err := collection.CreateItem("foreign", libsecret.NewSecret(session, []byte{}, value, "application/octet-stream"), false)
	if err != nil {
		t.Fatal("cannot create foreign record")
	}
	err = conn.Object(libsecret.DBusServiceName, foreign.Path()).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Set", 0, "org.freedesktop.Secret.Item", "Attributes", dbus.MakeVariant(map[string]string{"other": "foreign"})).Err
	if err != nil {
		t.Fatal("cannot remove profile attribute")
	}
	for _, name := range []string{"foreign", "absent", "different-label"} {
		if exists, err := store.Exists(ctx, secretstore.DefaultService, name); err != nil || exists {
			t.Fatal("label or absence was treated as identity")
		}
		if _, err := store.Get(ctx, secretstore.DefaultService, name); !errors.Is(err, secretstore.ErrNotFound) {
			t.Fatal("missing Get status changed")
		}
		if err := store.Delete(ctx, secretstore.DefaultService, name); !errors.Is(err, secretstore.ErrNotFound) {
			t.Fatal("missing Delete status changed")
		}
	}
	if entries, err := store.List(ctx, secretstore.DefaultService); err != nil || len(entries) != 1 || entries[0].Name != "legacy" {
		t.Fatal("listing did not filter attributes")
	}
	if err := store.Set(ctx, secretstore.DefaultService, "legacy", second); err != nil {
		t.Fatal("overwrite failed")
	}
	if got, err := legacy.Get("legacy"); err != nil || !bytes.Equal(got.Data, second) {
		t.Fatal("overwrite broke legacy encoding")
	}
	// Run the public CLI, using real Secret Service and comparing only a digest.
	run := func(input []byte, want int, args ...string) []byte {
		t.Helper()
		command := exec.CommandContext(ctx, cli, args...)
		command.Stdin = bytes.NewReader(input)
		command.Dir = t.TempDir()
		output, err := command.CombinedOutput()
		for _, secret := range [][]byte{value, second, master} {
			if bytes.Contains(output, secret) {
				t.Fatal("CLI output leaked generated sensitive material")
			}
		}
		code := 0
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatal("cannot run native CLI")
			}
			code = exit.ExitCode()
		}
		if code != want {
			t.Fatalf("native CLI exit=%d, want %d", code, want)
		}
		return output
	}
	run(value, 0, "--json", "secret", "set", "normal", "--stdin")
	run(nil, 0, "--json", "secret", "check", "normal")
	run(nil, 0, "--json", "secret", "list")
	digest := sha256.Sum256(value)
	output := run(nil, 0, "exec", "--secret", "normal:EV_NATIVE_VALUE", "--", "python3", "-c", `import hashlib,os; print(hashlib.sha256(os.environ["EV_NATIVE_VALUE"].encode()).hexdigest())`)
	if strings.TrimSpace(string(output)) != hex.EncodeToString(digest[:]) {
		t.Fatal("exec received a different value")
	}
	run(second, 0, "secret", "set", "normal", "--stdin", "--verify")
	run(nil, 0, "secret", "delete", "normal", "--confirm", "normal")
	run(nil, 3, "--json", "secret", "check", "normal")
	run(nil, 3, "--json", "secret", "delete", "normal", "--confirm", "normal")
	// Physical duplicates are left untouched by every attempted operation.
	duplicate, err := collection.CreateItem("legacy", libsecret.NewSecret(session, []byte{}, value, "application/json"), false)
	if err != nil {
		t.Fatal("cannot create duplicate")
	}
	for _, operation := range []func() error{
		func() error { _, err := store.Get(ctx, secretstore.DefaultService, "legacy"); return err },
		func() error { return store.Set(ctx, secretstore.DefaultService, "legacy", value) },
		func() error { return store.Delete(ctx, secretstore.DefaultService, "legacy") },
		func() error { _, err := store.List(ctx, secretstore.DefaultService); return err },
		func() error { _, err := store.Exists(ctx, secretstore.DefaultService, "legacy"); return err },
	} {
		if !errors.Is(operation(), secretstore.ErrAmbiguous) {
			t.Fatal("duplicate was not refused")
		}
	}
	run(nil, 4, "--json", "secret", "check", "legacy")
	matches, err := collection.SearchItems("legacy")
	if err != nil || len(matches) != 2 {
		t.Fatal("duplicates were modified")
	}
	if err := duplicate.Delete(); err != nil {
		t.Fatal(err)
	}
	// Confirm the isolated daemon's disk location belongs to the disposable root.
	if !filepath.IsAbs(os.Getenv("XDG_DATA_HOME")) {
		t.Fatal("data directory is not isolated")
	}
	t.Log("legacy encoding, profile identity, duplicates, and CLI set/check/list/exec/overwrite/delete passed")
}
