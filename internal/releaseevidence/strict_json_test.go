package releaseevidence

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEvidenceParsersRejectSingleWrongCaseFieldReplacements(t *testing.T) {
	observationBytes, err := json.Marshal(Observation{})
	if err != nil {
		t.Fatal(err)
	}
	for name, candidate := range map[string]string{
		"root":                    strings.Replace(string(observationBytes), `"schema_id"`, `"Schema_ID"`, 1),
		"nested":                  strings.Replace(string(observationBytes), `"tag":{"name"`, `"tag":{"Name"`, 1),
		"settings proof nested":   strings.Replace(string(observationBytes), `"repository_release_settings":{"schema_id"`, `"repository_release_settings":{"Schema_ID"`, 1),
		"settings proof boundary": strings.Replace(string(observationBytes), `"repository_release_settings"`, `"Repository_Release_Settings"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseObservation([]byte(candidate)); err == nil {
				t.Fatal("single wrong-case observation field replacement was accepted")
			}
		})
	}

	evidenceBytes, err := json.Marshal(Evidence{})
	if err != nil {
		t.Fatal(err)
	}
	wrongEvidenceSettings := strings.Replace(string(evidenceBytes), `"repository_release_settings":{"schema_id"`, `"repository_release_settings":{"Schema_ID"`, 1)
	if _, err := ParseEvidence([]byte(wrongEvidenceSettings)); err == nil {
		t.Fatal("wrong-case embedded settings proof field was accepted in durable evidence")
	}

	bundle := AttestationVerificationBundle{
		Entries: []AttestationVerificationBundleEntry{{AssetName: "asset"}},
	}
	bundleBytes, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	wrongEntry := strings.Replace(string(bundleBytes), `"asset_name"`, `"Asset_Name"`, 1)
	if _, err := ParseAttestationVerificationBundle([]byte(wrongEntry)); err == nil {
		t.Fatal("wrong-case attestation bundle entry field was accepted")
	}
}

func TestRawAttestationDecoderRejectsWrongCaseNestedField(t *testing.T) {
	entry := ghAttestationVerificationEntry{
		VerificationResult: ghVerificationResult{
			Signature: ghVerifiedSignature{
				Certificate: ghVerifiedCertificate{BuildSignerURI: "signer"},
			},
			Statement: ghStatement{Predicate: json.RawMessage(`{"VendorField":true}`)},
		},
	}
	encoded, err := json.Marshal([]ghAttestationVerificationEntry{entry})
	if err != nil {
		t.Fatal(err)
	}
	var decoded []ghAttestationVerificationEntry
	if err := decodeStrict(encoded, &decoded); err != nil {
		t.Fatalf("canonical attestation shape: %v", err)
	}
	candidate := strings.Replace(string(encoded), `"buildSignerURI"`, `"BuildSignerURI"`, 1)
	if err := decodeStrict([]byte(candidate), &decoded); err == nil {
		t.Fatal("wrong-case nested gh attestation field was accepted")
	}
}
