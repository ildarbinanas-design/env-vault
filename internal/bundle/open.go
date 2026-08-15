package bundle

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Open authenticates and decrypts a container.
//
// Every structural check runs before the key is derived, so a hostile
// container cannot force an expensive Argon2id call — or a large allocation —
// merely by declaring absurd parameters.
func Open(raw []byte, passphrase []byte) (Payload, error) {
	if len(passphrase) == 0 {
		return Payload{}, fmt.Errorf("passphrase is empty")
	}
	if len(raw) == 0 {
		return Payload{}, fmt.Errorf("%w: container is empty", ErrInvalid)
	}
	if len(raw) > MaxContainerBytes {
		return Payload{}, fmt.Errorf("%w: container exceeds %d bytes", ErrInvalid, MaxContainerBytes)
	}

	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document container
	if err := decoder.Decode(&document); err != nil {
		return Payload{}, fmt.Errorf("%w: %s", ErrInvalid, err)
	}

	if document.Schema != Schema {
		return Payload{}, fmt.Errorf("%w: unsupported schema %q", ErrInvalid, document.Schema)
	}
	if document.Version != Version {
		return Payload{}, fmt.Errorf("%w: unsupported version %d", ErrInvalid, document.Version)
	}
	if document.KDF.Algorithm != KDFArgon2id {
		return Payload{}, fmt.Errorf("%w: unsupported kdf %q", ErrInvalid, document.KDF.Algorithm)
	}
	if document.Cipher.Algorithm != CipherAES256GCM {
		return Payload{}, fmt.Errorf("%w: unsupported cipher %q", ErrInvalid, document.Cipher.Algorithm)
	}
	if document.KDF.KeyLength != keyLength {
		return Payload{}, fmt.Errorf("%w: kdf key length %d, want %d", ErrInvalid, document.KDF.KeyLength, keyLength)
	}
	if len(document.KDF.Salt) != saltLength {
		return Payload{}, fmt.Errorf("%w: salt is %d bytes, want %d", ErrInvalid, len(document.KDF.Salt), saltLength)
	}
	if len(document.Cipher.Nonce) != nonceLength {
		return Payload{}, fmt.Errorf("%w: nonce is %d bytes, want %d", ErrInvalid, len(document.Cipher.Nonce), nonceLength)
	}
	if len(document.Payload) == 0 {
		return Payload{}, fmt.Errorf("%w: container has no payload", ErrInvalid)
	}
	if len(document.Payload) > maxCiphertextBytes {
		return Payload{}, fmt.Errorf("%w: payload exceeds %d bytes", ErrInvalid, maxCiphertextBytes)
	}

	params := Params{
		Time:        document.KDF.Time,
		MemoryKiB:   document.KDF.MemoryKiB,
		Parallelism: document.KDF.Parallelism,
	}
	if err := params.validate(); err != nil {
		return Payload{}, err
	}

	// The additional authenticated data is the header re-encoded from the
	// parsed structure. Any change to a header field — including the KDF cost
	// an attacker would want to lower — changes this and fails the open.
	additional, err := json.Marshal(document.Header)
	if err != nil {
		return Payload{}, fmt.Errorf("%w: %s", ErrInvalid, err)
	}

	key := deriveKey(passphrase, document.KDF.Salt, params)
	defer Wipe(key)

	gcm, err := newGCM(key)
	if err != nil {
		return Payload{}, err
	}

	plaintext, err := gcm.Open(nil, document.Cipher.Nonce, document.Payload, additional)
	if err != nil {
		// Wrong passphrase and tampered container are the same answer.
		return Payload{}, ErrAuthFailed
	}
	defer Wipe(plaintext)

	payloadDecoder := json.NewDecoder(bytes.NewReader(plaintext))
	payloadDecoder.DisallowUnknownFields()
	var payload Payload
	if err := payloadDecoder.Decode(&payload); err != nil {
		return Payload{}, fmt.Errorf("%w: %s", ErrInvalid, err)
	}
	if err := validatePayload(payload); err != nil {
		return Payload{}, err
	}
	return payload, nil
}

// validatePayload enforces the structural rules shared by sealing and opening.
// On the opening side the contents came from another machine, so they are
// untrusted input even though the tag verified.
func validatePayload(payload Payload) error {
	if len(payload.Secrets) == 0 {
		return fmt.Errorf("%w: container holds no secrets", ErrInvalid)
	}
	seen := make(map[string]struct{}, len(payload.Secrets))
	for _, entry := range payload.Secrets {
		if entry.Service == "" {
			return fmt.Errorf("%w: secret entry has no service", ErrInvalid)
		}
		if entry.Name == "" {
			return fmt.Errorf("%w: secret entry has no name", ErrInvalid)
		}
		key := entry.Service + "\x00" + entry.Name
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("%w: duplicate secret entry %s", ErrInvalid, entry.Name)
		}
		seen[key] = struct{}{}
	}
	return nil
}
