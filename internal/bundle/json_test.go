package bundle

import (
	"errors"
	"testing"
)

// These fixtures contain metadata only and exercise parsing independently of
// encryption, passphrases, or a secret backend.
func TestDecodeStrictJSONRejectsAmbiguousDocuments(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		payload   bool
	}{
		{name: "second object", raw: `{"version":1}{}`},
		{name: "second scalar", raw: `{"version":1} true`},
		{name: "trailing garbage", raw: `{"version":1} trailing`},
		{name: "duplicate header field", raw: `{"version":1,"version":2}`},
		{name: "duplicate escaped field", raw: `{"version":1,"vers\u0069on":2}`},
		{name: "duplicate field case alias", raw: `{"version":1,"Version":2}`},
		{name: "duplicate nested kdf field", raw: `{"kdf":{"time":1,"time":2}}`},
		{name: "duplicate nested cipher field", raw: `{"cipher":{"algorithm":"first","algorithm":"second"}}`},
		{name: "duplicate payload list", raw: `{"secrets":[],"secrets":[]}`, payload: true},
		{name: "duplicate payload entry field", raw: `{"secrets":[{"service":"team","name":"first","name":"second"}]}`, payload: true},
		{name: "duplicate payload field case alias", raw: `{"secrets":[{"service":"team","Service":"other"}]}`, payload: true},
		{name: "payload trailing object", raw: `{"secrets":[]} {}`, payload: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var target any = &container{}
			if tc.payload {
				target = &Payload{}
			}
			if err := decodeStrictJSON([]byte(tc.raw), target); !errors.Is(err, ErrInvalid) {
				t.Fatal("ambiguous JSON was not rejected as an invalid container")
			}
		})
	}
}

func TestDecodeStrictJSONPreservesMetadata(t *testing.T) {
	var document container
	if err := decodeStrictJSON([]byte(" \n"+`{"version":1,"tool_version":"punctuation: {}, []","kdf":{"algorithm":"argon2id","time":1},"cipher":{"algorithm":"aes-256-gcm"}}`+"\r\n\t "), &document); err != nil {
		t.Fatal("one document with surrounding whitespace was rejected")
	}
	if document.Version != 1 || document.ToolVersion != "punctuation: {}, []" || document.KDF.Time != 1 || document.KDF.Algorithm != KDFArgon2id || document.Cipher.Algorithm != CipherAES256GCM {
		t.Fatal("container metadata changed during decoding")
	}
	var payload Payload
	if err := decodeStrictJSON([]byte(`{"secrets":[{"service":"team","name":"first"},{"service":"other","name":"second"}]}`), &payload); err != nil {
		t.Fatal("same field names in separate entries were rejected")
	}
	if len(payload.Secrets) != 2 || payload.Secrets[0].Service != "team" || payload.Secrets[0].Name != "first" || payload.Secrets[1].Service != "other" || payload.Secrets[1].Name != "second" {
		t.Fatal("payload metadata changed during decoding")
	}
}

func TestDecodeStrictJSONRejectsInvalidStructure(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"empty", ``},
		{"truncated", `{"version":`},
		{"unknown root field", `{"unrecognized_metadata":"public-label"}`},
		{"unknown nested field", `{"kdf":{"unrecognized_metadata":"public-label"}}`},
		{"wrong type", `{"version":"public-label"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var document container
			err := decodeStrictJSON([]byte(tc.raw), &document)
			if !errors.Is(err, ErrInvalid) {
				t.Fatal("invalid structure was accepted")
			}
			if err.Error() != "invalid container: invalid JSON document" {
				t.Fatal("JSON diagnostic must be fixed text without input fields or values")
			}
		})
	}
}
