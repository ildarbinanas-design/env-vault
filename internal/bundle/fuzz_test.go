package bundle

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"testing"
)

func FuzzContainerParser(f *testing.F) {
	passphrase := []byte(rand.Text())
	defer Wipe(passphrase)
	payload := Payload{Secrets: []SecretEntry{{Service: "env-vault", Name: "fuzz-entry", Value: []byte(rand.Text())}}}
	defer Wipe(payload.Secrets[0].Value)
	raw, err := Seal(payload, passphrase, Options{Params: testParams})
	if err != nil {
		f.Fatal("create generated encrypted seed")
	}
	f.Add(raw)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"schema":"env-vault.bundle.v1","version":1}`))
	f.Add([]byte(`{"schema":`))
	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 64<<10 {
			t.Skip()
		}
		// Keep fuzzing bounded even when a mutation declares expensive but
		// valid Argon2 parameters. The normal tests cover those cost bounds.
		var header container
		if json.Unmarshal(input, &header) == nil {
			params := Params{Time: header.KDF.Time, MemoryKiB: header.KDF.MemoryKiB, Parallelism: header.KDF.Parallelism}
			if params.validate() == nil && params != testParams {
				t.Skip()
			}
		}
		opened, err := Open(input, passphrase)
		defer func() {
			for _, entry := range opened.Secrets {
				Wipe(entry.Value)
			}
		}()
		if err != nil {
			if !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrAuthFailed) {
				t.Fatal("parser returned an unclassified error")
			}
			return
		}
		if err := validatePayload(opened); err != nil {
			t.Fatal("parser accepted an invalid payload")
		}
	})
}
