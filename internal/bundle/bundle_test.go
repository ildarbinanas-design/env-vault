package bundle

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// testParams are the cheapest cost parameters that still satisfy the accepted
// bounds, so the suite exercises the real key-derivation path without paying
// the production memory cost on every case.
var testParams = Params{Time: 1, MemoryKiB: minMemoryKiB, Parallelism: 1}

const testPassphrase = "correct horse battery staple"

func testPayload() Payload {
	return Payload{
		Secrets: []SecretEntry{
			{Service: "env-vault", Name: "nexus-token", Value: []byte("token-value")},
			{Service: "env-vault", Name: "raw-bytes", Value: []byte{0x00, 0xff, 0xfe, 0x80}},
			{Service: "env-vault", Name: "empty", Value: []byte{}},
			{Service: "custom/service", Name: "nexus-token", Value: []byte("other-value")},
		},
	}
}

func sealForTest(t *testing.T, payload Payload) []byte {
	t.Helper()
	raw, err := Seal(payload, []byte(testPassphrase), Options{
		Params:      testParams,
		CreatedAt:   time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC),
		ToolVersion: "v0.1.0-test",
	})
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return raw
}

func TestRoundTripPreservesPayload(t *testing.T) {
	want := testPayload()
	raw := sealForTest(t, want)

	got, err := Open(raw, []byte(testPassphrase))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(got.Secrets) != len(want.Secrets) {
		t.Fatalf("got %d secrets, want %d", len(got.Secrets), len(want.Secrets))
	}
	for i, entry := range got.Secrets {
		expected := want.Secrets[i]
		if entry.Service != expected.Service || entry.Name != expected.Name {
			t.Errorf("secret %d = %+v, want %+v", i, entry, expected)
		}
		if !bytes.Equal(entry.Value, expected.Value) {
			t.Errorf("secret %d value = %v, want %v", i, entry.Value, expected.Value)
		}
	}
}

// The container must disclose nothing about its contents to someone holding
// the file but not the passphrase.
func TestSealedContainerHidesNamesAndValues(t *testing.T) {
	raw := sealForTest(t, testPayload())

	for _, secret := range []string{"nexus-token", "token-value", "custom/service", "other-value"} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Errorf("container discloses %q in the clear", secret)
		}
	}

	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, field := range []string{"secrets", "service"} {
		if _, ok := document[field]; ok {
			t.Errorf("header carries %q, which belongs inside the ciphertext", field)
		}
	}
}

func TestSealIsRandomizedPerCall(t *testing.T) {
	payload := testPayload()
	first := sealForTest(t, payload)
	second := sealForTest(t, payload)

	if bytes.Equal(first, second) {
		t.Fatal("two seals of the same payload produced identical containers")
	}

	firstHeader := decodeContainer(t, first)
	secondHeader := decodeContainer(t, second)
	if bytes.Equal(firstHeader.KDF.Salt, secondHeader.KDF.Salt) {
		t.Error("salt was reused across seals")
	}
	if bytes.Equal(firstHeader.Cipher.Nonce, secondHeader.Cipher.Nonce) {
		t.Error("nonce was reused across seals")
	}
	if bytes.Equal(firstHeader.Payload, secondHeader.Payload) {
		t.Error("ciphertext was identical across seals")
	}
}

func TestOpenRejectsWrongPassphrase(t *testing.T) {
	raw := sealForTest(t, testPayload())

	_, err := Open(raw, []byte("wrong passphrase entirely"))
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("Open error = %v, want ErrAuthFailed", err)
	}
}

// Every cleartext header field must be covered by authentication. The cost
// parameters are bound implicitly, because changing them changes the derived
// key; created_at and tool_version are bound only by the additional
// authenticated data, so they are the cases that actually prove the AAD is
// wired up.
func TestOpenRejectsTamperedHeaderField(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"created_at", func(document map[string]any) {
			document["created_at"] = "2020-01-01T00:00:00Z"
		}},
		{"tool_version", func(document map[string]any) {
			document["tool_version"] = "v9.9.9-forged"
		}},
		{"kdf time", func(document map[string]any) {
			kdf(document)["time"] = float64(2)
		}},
		{"kdf memory", func(document map[string]any) {
			kdf(document)["memory_kib"] = float64(minMemoryKiB * 2)
		}},
		{"kdf parallelism", func(document map[string]any) {
			kdf(document)["parallelism"] = float64(2)
		}},
		{"kdf salt", func(document map[string]any) {
			kdf(document)["salt"] = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, saltLength))
		}},
		{"cipher nonce", func(document map[string]any) {
			cipherSection(document)["nonce"] = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x02}, nonceLength))
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := mutateContainer(t, sealForTest(t, testPayload()), test.mutate)

			_, err := Open(raw, []byte(testPassphrase))
			if !errors.Is(err, ErrAuthFailed) {
				t.Fatalf("Open error = %v, want ErrAuthFailed", err)
			}
		})
	}
}

func TestOpenRejectsTamperedCiphertext(t *testing.T) {
	tests := map[string]func([]byte) []byte{
		"flipped bit": func(ciphertext []byte) []byte {
			out := append([]byte(nil), ciphertext...)
			out[0] ^= 0x01
			return out
		},
		"truncated": func(ciphertext []byte) []byte {
			return ciphertext[:len(ciphertext)-1]
		},
		"extended": func(ciphertext []byte) []byte {
			return append(append([]byte(nil), ciphertext...), 0x00)
		},
	}

	for name, transform := range tests {
		t.Run(name, func(t *testing.T) {
			raw := mutateContainer(t, sealForTest(t, testPayload()), func(document map[string]any) {
				ciphertext, err := base64.StdEncoding.DecodeString(document["payload"].(string))
				if err != nil {
					t.Fatalf("DecodeString: %v", err)
				}
				document["payload"] = base64.StdEncoding.EncodeToString(transform(ciphertext))
			})

			_, err := Open(raw, []byte(testPassphrase))
			if !errors.Is(err, ErrAuthFailed) {
				t.Fatalf("Open error = %v, want ErrAuthFailed", err)
			}
		})
	}
}

// A hostile container must be refused on structure alone, before the
// passphrase is stretched. An unbounded memory cost would otherwise be a
// denial of service that lands ahead of any authentication.
func TestOpenRejectsParametersOutsideBounds(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"memory far above the cap", func(document map[string]any) {
			kdf(document)["memory_kib"] = float64(4 * 1024 * 1024)
		}},
		{"memory below the floor", func(document map[string]any) {
			kdf(document)["memory_kib"] = float64(1)
		}},
		{"time above the cap", func(document map[string]any) {
			kdf(document)["time"] = float64(1000)
		}},
		{"time zero", func(document map[string]any) {
			kdf(document)["time"] = float64(0)
		}},
		{"parallelism above the cap", func(document map[string]any) {
			kdf(document)["parallelism"] = float64(64)
		}},
		{"parallelism zero", func(document map[string]any) {
			kdf(document)["parallelism"] = float64(0)
		}},
		{"key length", func(document map[string]any) {
			kdf(document)["key_length"] = float64(16)
		}},
		{"short salt", func(document map[string]any) {
			kdf(document)["salt"] = base64.StdEncoding.EncodeToString([]byte{0x01})
		}},
		{"short nonce", func(document map[string]any) {
			cipherSection(document)["nonce"] = base64.StdEncoding.EncodeToString([]byte{0x01})
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := mutateContainer(t, sealForTest(t, testPayload()), test.mutate)

			started := time.Now()
			_, err := Open(raw, []byte(testPassphrase))
			elapsed := time.Since(started)

			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Open error = %v, want ErrInvalid", err)
			}
			// Rejection has to happen before key derivation. A real Argon2id
			// pass at these declared costs would be far slower than this.
			if elapsed > time.Second {
				t.Fatalf("rejection took %v, which suggests the key was derived first", elapsed)
			}
		})
	}
}

func TestOpenRejectsMalformedContainer(t *testing.T) {
	tests := map[string][]byte{
		"empty":           {},
		"not json":        []byte("this is not a container"),
		"json array":      []byte(`["schema"]`),
		"missing payload": []byte(`{"schema":"` + Schema + `","version":1,"created_at":"","tool_version":"","kdf":{"algorithm":"argon2id","salt":null,"time":1,"memory_kib":8192,"parallelism":1,"key_length":32},"cipher":{"algorithm":"aes-256-gcm","nonce":null}}`),
		"unknown field":   []byte(`{"schema":"` + Schema + `","version":1,"surprise":true}`),
		"oversize": append(
			[]byte(`{"schema":"`+Schema+`","version":1,"padding":"`),
			append(bytes.Repeat([]byte("A"), MaxContainerBytes+1), []byte(`"}`)...)...,
		),
	}

	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Open(raw, []byte(testPassphrase))
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Open error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestOpenRejectsForeignSchemaAndVersion(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"schema", func(document map[string]any) { document["schema"] = "env-vault.bundle.v2" }},
		{"version", func(document map[string]any) { document["version"] = float64(2) }},
		{"kdf algorithm", func(document map[string]any) { kdf(document)["algorithm"] = "pbkdf2" }},
		{"cipher algorithm", func(document map[string]any) { cipherSection(document)["algorithm"] = "aes-128-gcm" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw := mutateContainer(t, sealForTest(t, testPayload()), test.mutate)

			_, err := Open(raw, []byte(testPassphrase))
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Open error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestOpenRejectsEmptyPassphrase(t *testing.T) {
	raw := sealForTest(t, testPayload())

	if _, err := Open(raw, nil); err == nil {
		t.Fatal("Open accepted an empty passphrase")
	}
}

func TestSealRejectsWeakPassphrase(t *testing.T) {
	tests := map[string]string{
		"empty": "",
		"short": strings.Repeat("a", MinPassphraseLength-1),
	}

	for name, passphrase := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Seal(testPayload(), []byte(passphrase), Options{Params: testParams})
			if err == nil {
				t.Fatal("Seal accepted a weak passphrase")
			}
		})
	}
}

func TestSealRejectsIncompletePayload(t *testing.T) {
	tests := map[string]Payload{
		"no secrets": {},
		"no service": {Secrets: []SecretEntry{{Name: "nexus-token", Value: []byte("v")}}},
		"no name":    {Secrets: []SecretEntry{{Service: "env-vault", Value: []byte("v")}}},
		"duplicate entry": {Secrets: []SecretEntry{
			{Service: "env-vault", Name: "nexus-token", Value: []byte("a")},
			{Service: "env-vault", Name: "nexus-token", Value: []byte("b")},
		}},
	}

	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Seal(payload, []byte(testPassphrase), Options{Params: testParams})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Seal error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestSealRejectsParametersOutsideBounds(t *testing.T) {
	tests := map[string]Params{
		"memory too low":       {Time: 1, MemoryKiB: 1, Parallelism: 1},
		"memory too high":      {Time: 1, MemoryKiB: maxMemoryKiB + 1, Parallelism: 1},
		"time too high":        {Time: maxTime + 1, MemoryKiB: minMemoryKiB, Parallelism: 1},
		"parallelism too high": {Time: 1, MemoryKiB: minMemoryKiB, Parallelism: maxParallelism + 1},
	}

	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Seal(testPayload(), []byte(testPassphrase), Options{Params: params})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Seal error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestDefaultParamsAreWithinBounds(t *testing.T) {
	if err := DefaultParams().validate(); err != nil {
		t.Fatalf("DefaultParams: %v", err)
	}
}

func TestSealUsesDefaultParamsForZeroValue(t *testing.T) {
	raw, err := Seal(testPayload(), []byte(testPassphrase), Options{})
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	document := decodeContainer(t, raw)
	want := DefaultParams()
	if document.KDF.Time != want.Time || document.KDF.MemoryKiB != want.MemoryKiB || document.KDF.Parallelism != want.Parallelism {
		t.Fatalf("params = %+v, want %+v", document.KDF, want)
	}
	if document.CreatedAt == "" {
		t.Error("created_at was not recorded")
	}
}

func TestWipeClearsBytes(t *testing.T) {
	buffer := []byte("key material")
	Wipe(buffer)

	for i, b := range buffer {
		if b != 0 {
			t.Fatalf("byte %d = %d, want 0", i, b)
		}
	}
}

func decodeContainer(t *testing.T, raw []byte) container {
	t.Helper()
	var document container
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return document
}

func mutateContainer(t *testing.T, raw []byte, mutate func(map[string]any)) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	mutate(document)
	out, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return out
}

func kdf(document map[string]any) map[string]any {
	return document["kdf"].(map[string]any)
}

func cipherSection(document map[string]any) map[string]any {
	return document["cipher"].(map[string]any)
}
