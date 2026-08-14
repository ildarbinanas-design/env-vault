package bundle

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	"golang.org/x/crypto/argon2"
)

// Seal encrypts payload under a key derived from passphrase and returns the
// complete container document.
//
// A fresh salt and a fresh nonce are drawn for every call, so sealing the same
// profile twice with the same passphrase produces unrelated ciphertext and the
// key/nonce pair is never reused.
func Seal(payload Payload, passphrase []byte, opts Options) ([]byte, error) {
	if err := ValidatePassphrase(passphrase); err != nil {
		return nil, err
	}
	if err := validatePayload(payload); err != nil {
		return nil, err
	}

	params := opts.Params.resolve()
	if err := params.validate(); err != nil {
		return nil, err
	}

	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}
	nonce := make([]byte, nonceLength)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	createdAt := opts.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	header := Header{
		Schema:      Schema,
		Version:     Version,
		CreatedAt:   createdAt.UTC().Format(time.RFC3339),
		ToolVersion: opts.ToolVersion,
		KDF: KDFParams{
			Algorithm:   KDFArgon2id,
			Salt:        salt,
			Time:        params.Time,
			MemoryKiB:   params.MemoryKiB,
			Parallelism: params.Parallelism,
			KeyLength:   keyLength,
		},
		Cipher: CipherParams{
			Algorithm: CipherAES256GCM,
			Nonce:     nonce,
		},
	}

	additional, err := json.Marshal(header)
	if err != nil {
		return nil, fmt.Errorf("encode header: %w", err)
	}

	plaintext, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode payload: %w", err)
	}
	defer Wipe(plaintext)

	key := deriveKey(passphrase, salt, params)
	defer Wipe(key)

	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}

	document := container{
		Header:  header,
		Payload: gcm.Seal(nil, nonce, plaintext, additional),
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode container: %w", err)
	}
	return raw, nil
}

func deriveKey(passphrase, salt []byte, params Params) []byte {
	return argon2.IDKey(
		passphrase,
		salt,
		params.Time,
		params.MemoryKiB,
		uint8(params.Parallelism),
		keyLength,
	)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("initialize cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("initialize GCM: %w", err)
	}
	return gcm, nil
}
