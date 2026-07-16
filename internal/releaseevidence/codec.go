package releaseevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ildarbinanas-design/env-vault/internal/releasemetrics"
	"github.com/ildarbinanas-design/env-vault/internal/strictjson"
)

const maxJSONBytes = 16 << 20

func ParseObservation(data []byte) (Observation, error) {
	var value Observation
	if err := decodeStrict(data, &value); err != nil {
		return Observation{}, fmt.Errorf("decode release observation: %w", err)
	}
	return value, nil
}

func ParseAuthorization(data []byte) (Authorization, error) {
	var value Authorization
	if err := decodeStrict(data, &value); err != nil {
		return Authorization{}, fmt.Errorf("decode release authorization: %w", err)
	}
	return value, nil
}

func ParseAttestationVerificationBundle(data []byte) (AttestationVerificationBundle, error) {
	var value AttestationVerificationBundle
	if err := decodeStrict(data, &value); err != nil {
		return AttestationVerificationBundle{}, fmt.Errorf("decode attestation verification bundle: %w", err)
	}
	return value, nil
}

func ParseHealthProof(data []byte) (HealthProof, error) {
	var value HealthProof
	if err := decodeStrict(data, &value); err != nil {
		return HealthProof{}, fmt.Errorf("decode release health proof: %w", err)
	}
	return value, nil
}

func ParseMetrics(data []byte) (releasemetrics.Metrics, error) {
	if len(data) == 0 || len(data) > maxJSONBytes {
		return releasemetrics.Metrics{}, fmt.Errorf("decode release metrics: JSON size %d is outside 1..%d", len(data), maxJSONBytes)
	}
	value, err := releasemetrics.DecodeMetrics(data)
	if err != nil {
		return releasemetrics.Metrics{}, fmt.Errorf("decode release metrics: %w", err)
	}
	return value, nil
}

func ParseEvidence(data []byte) (Evidence, error) {
	var value Evidence
	if err := decodeStrict(data, &value); err != nil {
		return Evidence{}, fmt.Errorf("decode release evidence: %w", err)
	}
	return value, nil
}

// MarshalJSON emits a deterministic, reviewable file. Self-digests use the
// corresponding compact encoding/json representation of the same typed value.
func MarshalJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func SealHealthProof(proof *HealthProof) error {
	if proof == nil {
		return errors.New("health proof is nil")
	}
	if proof.ProofSHA256 != "" {
		return errors.New("health proof is already sealed")
	}
	digest, err := HealthProofSHA256(*proof)
	if err != nil {
		return err
	}
	proof.ProofSHA256 = digest
	return nil
}

func HealthProofSHA256(proof HealthProof) (string, error) {
	proof.ProofSHA256 = ""
	return compactSHA256(proof)
}

func EvidenceSHA256(evidence Evidence) (string, error) {
	evidence.EvidenceSHA256 = ""
	return compactSHA256(evidence)
}

func compactSHA256(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func decodeStrict(data []byte, destination any) error {
	return strictjson.Decode(data, maxJSONBytes, destination)
}
