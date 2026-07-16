package releasesettings

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/ildarbinanas-design/env-vault/internal/strictjson"
)

const maxJSONBytes = 16 << 20

const (
	CodeInputInvalid   = "SETTINGS_INPUT_INVALID"
	CodePolicyInvalid  = "SETTINGS_POLICY_INVALID"
	CodeTupleMismatch  = "SETTINGS_TUPLE_MISMATCH"
	CodeDigestMismatch = "SETTINGS_DIGEST_MISMATCH"
)

// CodedError gives releasecheck a stable machine-readable failure code.
type CodedError struct {
	Code   string
	Detail string
	Err    error
}

func (e *CodedError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Detail, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Detail)
}

func (e *CodedError) Unwrap() error { return e.Err }

// ErrorCode extracts the stable settings error code, if present.
func ErrorCode(err error) string {
	var coded *CodedError
	if errors.As(err, &coded) {
		return coded.Code
	}
	return ""
}

func fail(code, detail string, err error) error {
	return &CodedError{Code: code, Detail: detail, Err: err}
}

// ParseProof strictly decodes one proof using exact JSON field spelling.
func ParseProof(data []byte) (Proof, error) {
	var proof Proof
	if err := strictjson.Decode(data, maxJSONBytes, &proof); err != nil {
		return Proof{}, fail(CodeInputInvalid, "decode repository settings proof", err)
	}
	return proof, nil
}

// MarshalJSON emits deterministic, reviewable JSON. ProofSHA256 is based on
// the compact typed representation rather than indentation.
func MarshalJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// ProofSHA256 returns the proof's self-digest with its digest field cleared.
func ProofSHA256(proof Proof) (string, error) {
	proof.ProofSHA256 = ""
	data, err := json.Marshal(proof)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func sealDocument(data []byte) (Document, error) {
	if len(data) == 0 || len(data) > maxJSONBytes || !utf8.Valid(data) {
		return Document{}, fail(CodeInputInvalid, fmt.Sprintf("saved JSON size %d is outside 1..%d", len(data), maxJSONBytes), nil)
	}
	digest := sha256.Sum256(data)
	return Document{SHA256: hex.EncodeToString(digest[:]), DocumentJSON: string(data)}, nil
}
