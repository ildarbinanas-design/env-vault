package bundle

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/testutil"
)

func TestDefaultParamsMatchRFC9106Recommendation(t *testing.T) {
	want := Params{Time: 3, MemoryKiB: 65536, Parallelism: 4}
	if got := DefaultParams(); got != want {
		t.Fatalf("DefaultParams = %+v, want %+v", got, want)
	}
}

func TestSealCiphertextSizeBoundary(t *testing.T) {
	const ciphertextLimit = 16 << 20
	const gcmTagBytes = 16
	for _, test := range []struct {
		name  string
		delta int
	}{
		{"below", -1},
		{"at", 0},
		{"above", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			want := payloadWithEncodedSize(t, ciphertextLimit-gcmTagBytes+test.delta)
			passphrase := []byte(testutil.EphemeralValue(t))
			defer Wipe(passphrase)
			raw, err := Seal(want, passphrase, Options{Params: testParams})
			if test.delta > 0 {
				if !errors.Is(err, ErrInvalid) || raw != nil {
					t.Fatal("Seal must reject an oversized ciphertext without returning a container")
				}
				return
			}
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}
			if got := len(decodeContainer(t, raw).Payload); got != ciphertextLimit+test.delta {
				t.Fatalf("ciphertext size = %d, want %d", got, ciphertextLimit+test.delta)
			}
			assertSizeBoundaryRoundTrip(t, raw, passphrase, want)
		})
	}
}

func TestSealContainerSizeBoundary(t *testing.T) {
	const containerLimit = 24 << 20
	want := payloadWithEncodedSize(t, 128)
	passphrase := []byte(testutil.EphemeralValue(t))
	defer Wipe(passphrase)
	opts := Options{Params: testParams, CreatedAt: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)}
	baseline, err := Seal(want, passphrase, opts)
	if err != nil {
		t.Fatalf("Seal baseline: %v", err)
	}
	// Escaped header characters must be counted as JSON bytes, not runes or
	// raw string bytes. All remaining padding is ordinary non-secret metadata.
	const escapedPrefix = "<\"\\\n"
	encodedPrefix, err := json.Marshal(escapedPrefix)
	if err != nil {
		t.Fatalf("encode prefix: %v", err)
	}
	for _, test := range []struct {
		name  string
		delta int
	}{
		{"at", 0},
		{"above", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			opts.ToolVersion = escapedPrefix + strings.Repeat("v", containerLimit-len(baseline)-(len(encodedPrefix)-2)+test.delta)
			raw, err := Seal(want, passphrase, opts)
			if test.delta > 0 {
				if !errors.Is(err, ErrInvalid) || raw != nil {
					t.Fatal("Seal must reject an oversized document without returning a container")
				}
				return
			}
			if err != nil {
				t.Fatalf("Seal: %v", err)
			}
			if len(raw) != containerLimit {
				t.Fatalf("container size = %d, want %d", len(raw), containerLimit)
			}
			assertSizeBoundaryRoundTrip(t, raw, passphrase, want)
		})
	}
}

func payloadWithEncodedSize(t *testing.T, size int) Payload {
	t.Helper()
	payload := Payload{Secrets: []SecretEntry{{Service: "env-vault", Name: "size-boundary", Value: []byte{}}}}
	empty, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode empty payload: %v", err)
	}
	defer Wipe(empty)
	remaining := size - len(empty)
	if remaining < 0 {
		t.Fatal("requested payload size is too small")
	}
	payload.Secrets[0].Name += strings.Repeat("n", remaining%4)
	payload.Secrets[0].Value = make([]byte, remaining/4*3)
	if _, err := rand.Read(payload.Secrets[0].Value); err != nil {
		t.Fatalf("generate ephemeral value: %v", err)
	}
	t.Cleanup(func() { Wipe(payload.Secrets[0].Value) })
	return payload
}

func assertSizeBoundaryRoundTrip(t *testing.T, raw, passphrase []byte, want Payload) {
	t.Helper()
	got, err := Open(raw, passphrase)
	if err != nil {
		t.Fatalf("Open refused a container within the size limit: %v", err)
	}
	defer func() {
		for _, entry := range got.Secrets {
			Wipe(entry.Value)
		}
	}()
	if len(got.Secrets) != 1 || got.Secrets[0].Service != want.Secrets[0].Service ||
		got.Secrets[0].Name != want.Secrets[0].Name || !bytes.Equal(got.Secrets[0].Value, want.Secrets[0].Value) {
		t.Fatal("the size-boundary round trip changed the payload")
	}
}
