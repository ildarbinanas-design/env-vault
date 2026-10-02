package keyring

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/99designs/keyring"
	"github.com/danieljoos/wincred"
	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
	"golang.org/x/sys/windows"
)

func TestWindowsEnumerationMetadataAndErrors(t *testing.T) {
	base := &memoryKeyring{items: map[string][]byte{}}
	ring := &windowsRing{Keyring: base, service: "Téam", enumerate: func() ([]credentialMetadata, error) {
		return []credentialMetadata{
			{"KEYRING:TÉAM:TOKEN", 1}, {"keyring:Téam:foreign", 2},
			{"keyring:Téam:ci:neighbor", 1}, {"keyring:Téam2:neighbor", 1},
			{"other:Téam:foreign", 1}, {"LegacyGeneric:target=keyring:Téam:foreign", 1},
		}, nil
	}}
	store := Store{openKeyring: func(keyring.Config) (keyring.Keyring, error) { return ring, nil }}
	keys, err := ring.Keys()
	if err != nil || !slices.Equal(keys, []string{"TOKEN"}) {
		t.Fatal("enumeration crossed a type, namespace, or service boundary")
	}
	if exists, err := store.Exists(context.Background(), "Téam", "token"); err != nil || !exists {
		t.Fatal("metadata identity differs from native target identity")
	}
	for _, failure := range []error{windows.ERROR_ACCESS_DENIED, windows.ERROR_NO_SUCH_LOGON_SESSION} {
		ring.enumerate = func() ([]credentialMetadata, error) { return nil, failure }
		if _, err := store.List(context.Background(), "Téam"); !errors.Is(err, failure) || !errors.Is(err, secretstore.ErrUnavailable) {
			t.Fatal("listing error was lost")
		}
		if exists, err := store.Exists(context.Background(), "Téam", "token"); exists || !errors.Is(err, failure) {
			t.Fatal("exists swallowed enumeration error")
		}
	}
	ring.enumerate = func() ([]credentialMetadata, error) { return nil, windows.ERROR_NOT_FOUND }
	if exists, err := store.Exists(context.Background(), "Téam", "token"); exists || err != nil {
		t.Fatal("ERROR_NOT_FOUND must mean an empty listing")
	}
}

func TestWindowsNativeCredentialIdentity(t *testing.T) {
	if os.Getenv("ENV_VAULT_NATIVE_WINCRED_TEST") != "1" {
		t.Skip("requires disposable Windows CI logon session")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prefix := "env-vault-native-" + rand.Text() + "-"
	store := New()
	for _, pair := range [][2]string{{"Team", "team"}, {"Équipe", "éQUIPE"}, {"ТЕСТ", "тест"}, {"Σ", "ς"}, {"K", "K"}, {"I", "ı"}, {"Straße", "STRASSE"}, {"𐐀", "𐐨"}} {
		t.Run(pair[0], func(t *testing.T) {
			service, alternate := prefix+pair[0], prefix+pair[1]
			value := []byte(testutil.EphemeralValue(t))
			credential := wincred.NewGenericCredential(credentialTarget(service, "TOKEN"))
			credential.Persist = wincred.PersistSession
			credential.CredentialBlob = value
			if err := credential.Write(); err != nil {
				t.Fatal("cannot create disposable credential")
			}
			t.Cleanup(func() {
				if err := credential.Delete(); err != nil {
					t.Error("cannot delete disposable credential")
				}
			})
			got, err := wincred.GetGenericCredential(credentialTarget(alternate, "token"))
			if err != nil && !errors.Is(err, wincred.ErrElementNotFound) {
				t.Fatal("native read failed")
			}
			nativeEqual := err == nil
			if nativeEqual && !bytes.Equal(got.CredentialBlob, value) {
				t.Fatal("native read returned a different value")
			}
			cmp, err := compareWindowsTargets(credentialTarget(service, "TOKEN"), credentialTarget(alternate, "token"))
			if err != nil || (cmp == 0) != nativeEqual {
				t.Fatal("ordinal comparison disagrees with Credential Manager")
			}
			exists, err := store.Exists(ctx, alternate, "token")
			if err != nil || exists != nativeEqual {
				t.Fatal("Exists disagrees with Credential Manager")
			}
			identities := []secretstore.Metadata{{Service: service, Name: "TOKEN"}, {Service: alternate, Name: "token"}}
			err = store.ValidateIdentities(identities)
			if errors.Is(err, secretstore.ErrIdentityCollision) != nativeEqual {
				t.Fatal("import identity disagrees with Credential Manager")
			}
			if err := NewPass().ValidateIdentities(identities); err != nil {
				t.Fatal("Windows identity reached pass")
			}
		})
	}
}
