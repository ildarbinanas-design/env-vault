package keyring

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/99designs/keyring"
	"github.com/godbus/dbus"

	"github.com/ildarbinanas-design/env-vault/internal/secretstore"
	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

func walletStore(wallet *kwallet) Store {
	return Store{openKeyring: func(keyring.Config) (keyring.Keyring, error) { return wallet, nil }}
}

func TestKWalletDeniedOpenRefusesEveryOperation(t *testing.T) {
	value := []byte(testutil.EphemeralValue(t))
	defer clear(value)
	ctx := context.Background()
	for _, operation := range []string{"set", "get", "delete", "exists", "list"} {
		t.Run(operation, func(t *testing.T) {
			calls := 0
			wallet := newKWallet(keyring.Config{ServiceName: "test"}, func(method string, args ...any) *dbus.Call {
				calls++
				if method != "open" {
					t.Fatal("operation used a refused wallet handle")
				}
				if args[0] != "test" || args[2] != "keyring" {
					t.Fatal("legacy namespace changed")
				}
				return &dbus.Call{Body: []any{int32(-1)}}
			})
			store := walletStore(wallet)
			var err error
			switch operation {
			case "set":
				err = store.Set(ctx, "test", "token", value)
			case "get":
				_, err = store.Get(ctx, "test", "token")
			case "delete":
				err = store.Delete(ctx, "test", "token")
			case "exists":
				_, err = store.Exists(ctx, "test", "token")
			case "list":
				_, err = store.List(ctx, "test")
			}
			if !errors.Is(err, secretstore.ErrUnavailable) || !errors.Is(err, secretstore.ErrUnreadable) || errors.Is(err, secretstore.ErrNotFound) {
				t.Fatal("wallet refusal was reported as success or absence")
			}
			if calls != 1 || wallet.handle != -1 {
				t.Fatal("refused handle retained or operation retried")
			}
		})
	}
}

func TestKWalletMutationReplies(t *testing.T) {
	value := []byte(testutil.EphemeralValue(t))
	defer clear(value)
	tests := []struct {
		name    string
		reply   *dbus.Call
		success bool
	}{
		{"success", &dbus.Call{Body: []any{int32(0)}}, true},
		{"refused", &dbus.Call{Body: []any{int32(-1)}}, false},
		{"unknown-positive", &dbus.Call{Body: []any{int32(1)}}, false},
		{"missing", &dbus.Call{Body: []any{int32(-3)}}, false},
		{"empty", &dbus.Call{}, false},
		{"wrong-type", &dbus.Call{Body: []any{false}}, false},
		{"extra-field", &dbus.Call{Body: []any{int32(0), int32(0)}}, false},
		{"transport-error", &dbus.Call{Err: dbus.Error{Name: "org.freedesktop.DBus.Error.NoReply"}}, false},
	}
	for _, method := range []string{"writeEntry", "removeEntry"} {
		for _, tt := range tests {
			t.Run(method+"/"+tt.name, func(t *testing.T) {
				writes := 0
				wallet := newKWallet(keyring.Config{ServiceName: "test"}, func(got string, args ...any) *dbus.Call {
					if got == "open" {
						return &dbus.Call{Body: []any{int32(7)}}
					}
					if got != method {
						t.Fatal("unexpected wallet operation")
					}
					writes++
					if args[0] != int32(7) || args[1] != "keyring" || args[2] != "token" || args[len(args)-1] != "keyring" {
						t.Fatal("legacy record coordinates changed")
					}
					return tt.reply
				})
				store := walletStore(wallet)
				var err error
				if method == "writeEntry" {
					err = store.Set(context.Background(), "test", "token", value)
				} else {
					err = store.Delete(context.Background(), "test", "token")
				}
				if (err == nil) != tt.success {
					t.Fatal("incorrect mutation status")
				}
				if !tt.success && !(method == "removeEntry" && tt.name == "missing") && !errors.Is(err, secretstore.ErrUnavailable) {
					t.Fatal("failure lost backend classification")
				}
				if method == "removeEntry" && tt.name == "missing" && !errors.Is(err, secretstore.ErrNotFound) {
					t.Fatal("missing delete status lost")
				}
				if writes != 1 {
					t.Fatal("ambiguous mutation retried")
				}
			})
		}
	}
}

func TestKWalletLegacyEncodingRoundTrip(t *testing.T) {
	value := []byte(testutil.EphemeralValue(t))
	defer clear(value)
	var stored []byte
	defer func() { clear(stored) }()
	wallet := newKWallet(keyring.Config{ServiceName: "test"}, func(method string, args ...any) *dbus.Call {
		switch method {
		case "open":
			return &dbus.Call{Body: []any{int32(0)}} // Zero is a valid handle.
		case "isOpen":
			return &dbus.Call{Body: []any{true}}
		case "writeEntry":
			stored = append([]byte(nil), args[3].([]byte)...)
			return &dbus.Call{Body: []any{int32(0)}}
		case "readEntry":
			return &dbus.Call{Body: []any{append([]byte(nil), stored...)}}
		case "entryList":
			return &dbus.Call{Body: []any{[]string{"token"}}}
		case "removeEntry":
			clear(stored)
			stored = nil
			return &dbus.Call{Body: []any{int32(0)}}
		}
		t.Fatal("unexpected method")
		return nil
	})
	store := walletStore(wallet)
	ctx := context.Background()
	if err := store.Set(ctx, "test", "token", value); err != nil {
		t.Fatal("write failed")
	}
	var legacy keyring.Item
	if json.Unmarshal(stored, &legacy) != nil || legacy.Key != "token" || !bytes.Equal(legacy.Data, value) {
		t.Fatal("legacy encoding changed")
	}
	clear(legacy.Data)
	got, err := store.Get(ctx, "test", "token")
	if err != nil || !bytes.Equal(got, value) {
		t.Fatal("read did not round trip")
	}
	clear(got)
	if entries, err := store.List(ctx, "test"); err != nil || len(entries) != 1 || entries[0].Name != "token" {
		t.Fatal("metadata listing failed")
	}
	if err := store.Delete(ctx, "test", "token"); err != nil || stored != nil {
		t.Fatal("delete failed")
	}
}

func TestKWalletClosedDuringReadIsNotAbsence(t *testing.T) {
	for _, method := range []string{"readEntry", "entryList"} {
		t.Run(method, func(t *testing.T) {
			wallet := newKWallet(keyring.Config{}, func(got string, _ ...any) *dbus.Call {
				switch got {
				case "open":
					return &dbus.Call{Body: []any{int32(7)}}
				case "isOpen":
					return &dbus.Call{Body: []any{false}}
				case "readEntry":
					return &dbus.Call{Body: []any{[]byte{}}}
				case "entryList":
					return &dbus.Call{Body: []any{[]string{}}}
				}
				t.Fatal("unexpected method")
				return nil
			})
			var err error
			if method == "readEntry" {
				_, err = wallet.Get("token")
			} else {
				_, err = wallet.Keys()
			}
			if !errors.Is(err, secretstore.ErrUnreadable) {
				t.Fatal("closed wallet reported an absent record")
			}
		})
	}
}

func TestKWalletRejectsMalformedOpenReply(t *testing.T) {
	for _, reply := range []*dbus.Call{{}, {Body: []any{false}}, {Body: []any{int32(0), int32(0)}}, {Err: dbus.Error{Name: "org.freedesktop.DBus.Error.AccessDenied"}}} {
		wallet := newKWallet(keyring.Config{}, func(string, ...any) *dbus.Call { return reply })
		if err := wallet.openWallet(); err == nil || wallet.handle != -1 {
			t.Fatal("invalid open reply accepted")
		}
	}
}

func TestKWalletDeniedReopenDoesNotMutate(t *testing.T) {
	calls := []string{}
	wallet := newKWallet(keyring.Config{}, func(method string, _ ...any) *dbus.Call {
		calls = append(calls, method)
		switch method {
		case "isOpen":
			return &dbus.Call{Body: []any{false}}
		case "open":
			return &dbus.Call{Body: []any{int32(-1)}}
		default:
			t.Fatal("operation reached closed wallet")
			return nil
		}
	})
	wallet.handle = 7
	if err := wallet.Remove("token"); !errors.Is(err, secretstore.ErrUnreadable) {
		t.Fatal("reopen refusal accepted")
	}
	if len(calls) != 2 || wallet.handle != -1 {
		t.Fatal("closed handle retained or operation retried")
	}
}
