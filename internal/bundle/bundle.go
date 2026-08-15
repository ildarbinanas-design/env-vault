// Package bundle seals a profile and its secret values into an authenticated
// encrypted transfer container, and opens one again.
//
// This is the only package in env-vault that performs cryptography. It knows
// nothing about cobra, the config file, or the secret backend: it takes and
// returns plain structures and bytes, so every property below is testable
// without a terminal and without a keychain.
//
// The container is JSON. Its header carries the key-derivation and cipher
// parameters in the clear, because they are needed before the passphrase can
// be turned into a key. The header is therefore authenticated as additional
// authenticated data: an attacker who rewrites the KDF cost downward produces
// a container that fails to open rather than one that is cheap to attack.
//
// The header deliberately carries no profile name and no secret names. Those
// live inside the ciphertext, so a container discloses nothing about its
// contents without the passphrase.
package bundle

import (
	"errors"
	"fmt"
	"time"
)

const (
	// Schema identifies the container format. A container that does not carry
	// this exact value is refused rather than guessed at.
	Schema = "env-vault.bundle.v1"
	// Version is the format revision within Schema.
	Version = 1

	// KDFArgon2id is the only supported key-derivation function. The field
	// exists so a future function can be added without breaking v1 readers.
	KDFArgon2id = "argon2id"
	// CipherAES256GCM is the only supported cipher.
	CipherAES256GCM = "aes-256-gcm"
)

const (
	saltLength  = 16
	nonceLength = 12
	keyLength   = 32
)

// Key-derivation cost bounds. They are enforced when sealing and, more
// importantly, when opening: the parameters in a container come from whoever
// wrote it, so an unbounded memory cost would be a denial of service that
// lands before the passphrase is ever checked.
const (
	minTime        = 1
	maxTime        = 10
	minMemoryKiB   = 8 * 1024
	maxMemoryKiB   = 1024 * 1024
	minParallelism = 1
	maxParallelism = 8
)

const (
	// MaxContainerBytes caps the raw container before it is parsed. Callers
	// reading a container from disk should refuse a larger file rather than
	// load it only for Open to reject it.
	MaxContainerBytes = 24 << 20
	// maxCiphertextBytes caps the ciphertext after base64 decoding.
	maxCiphertextBytes = 16 << 20
)

// MinPassphraseLength is the shortest accepted passphrase. The container is
// offline-attackable, so passphrase strength is the only thing standing
// between a leaked file and its contents.
const MinPassphraseLength = 12

var (
	// ErrInvalid reports a container that is malformed, carries an unknown
	// schema or algorithm, or declares parameters outside the accepted bounds.
	ErrInvalid = errors.New("invalid container")

	// ErrAuthFailed reports that authenticated decryption failed. A wrong
	// passphrase and a tampered container are indistinguishable here, and are
	// reported identically on purpose.
	ErrAuthFailed = errors.New("container authentication failed")
)

// Params holds the Argon2id cost parameters.
type Params struct {
	Time        uint32 `json:"time"`
	MemoryKiB   uint32 `json:"memory_kib"`
	Parallelism uint32 `json:"parallelism"`
}

// DefaultParams returns the second parameter set recommended by RFC 9106:
// 64 MiB of memory, three passes, four lanes.
func DefaultParams() Params {
	return Params{
		Time:        3,
		MemoryKiB:   64 * 1024,
		Parallelism: 4,
	}
}

// KDFParams is the on-disk key-derivation header section.
type KDFParams struct {
	Algorithm   string `json:"algorithm"`
	Salt        []byte `json:"salt"`
	Time        uint32 `json:"time"`
	MemoryKiB   uint32 `json:"memory_kib"`
	Parallelism uint32 `json:"parallelism"`
	KeyLength   uint32 `json:"key_length"`
}

// CipherParams is the on-disk cipher header section.
type CipherParams struct {
	Algorithm string `json:"algorithm"`
	Nonce     []byte `json:"nonce"`
}

// Header is the cleartext, authenticated portion of a container.
//
// Field order is part of the format: the additional authenticated data is this
// structure re-encoded, and encoding/json emits struct fields in declaration
// order. Reordering these fields changes the AAD and breaks every existing
// container.
type Header struct {
	Schema      string       `json:"schema"`
	Version     int          `json:"version"`
	CreatedAt   string       `json:"created_at"`
	ToolVersion string       `json:"tool_version"`
	KDF         KDFParams    `json:"kdf"`
	Cipher      CipherParams `json:"cipher"`
}

// container is the complete on-disk document.
type container struct {
	Header
	Payload []byte `json:"payload"`
}

// SecretEntry is one stored secret, addressed the way the backend addresses
// it: by the pair (service, name). Value is encoded as base64 by encoding/json
// because keychain values are arbitrary bytes and are not required to be valid
// UTF-8.
type SecretEntry struct {
	Service string `json:"service"`
	Name    string `json:"name"`
	Value   []byte `json:"value"`
}

// Payload is the plaintext contents of a container.
//
// It carries secret values and nothing else. Profile mappings are not here on
// purpose: `.env-vault.yaml` holds no values, travels with the repository, and
// is already portable, so duplicating it into the container would only widen
// what a leaked container discloses.
type Payload struct {
	Secrets []SecretEntry `json:"secrets"`
}

// Options controls a seal operation.
type Options struct {
	// Params is the Argon2id cost. The zero value means DefaultParams.
	Params Params
	// CreatedAt is recorded in the header. The zero value means time.Now.
	CreatedAt time.Time
	// ToolVersion is recorded in the header for diagnostics.
	ToolVersion string
}

func (p Params) resolve() Params {
	if p == (Params{}) {
		return DefaultParams()
	}
	return p
}

func (p Params) validate() error {
	if p.Time < minTime || p.Time > maxTime {
		return fmt.Errorf("%w: kdf time %d outside [%d, %d]", ErrInvalid, p.Time, minTime, maxTime)
	}
	if p.MemoryKiB < minMemoryKiB || p.MemoryKiB > maxMemoryKiB {
		return fmt.Errorf("%w: kdf memory %d KiB outside [%d, %d]", ErrInvalid, p.MemoryKiB, minMemoryKiB, maxMemoryKiB)
	}
	if p.Parallelism < minParallelism || p.Parallelism > maxParallelism {
		return fmt.Errorf("%w: kdf parallelism %d outside [%d, %d]", ErrInvalid, p.Parallelism, minParallelism, maxParallelism)
	}
	return nil
}

// ValidatePassphrase enforces the minimum passphrase length.
func ValidatePassphrase(passphrase []byte) error {
	if len(passphrase) == 0 {
		return fmt.Errorf("passphrase is empty")
	}
	if len(passphrase) < MinPassphraseLength {
		return fmt.Errorf("passphrase must be at least %d characters", MinPassphraseLength)
	}
	return nil
}

// Wipe overwrites b in place. Go gives no guarantee that a value was never
// copied elsewhere by the garbage collector, so this reduces the window in
// which key material sits in memory rather than eliminating it.
func Wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
