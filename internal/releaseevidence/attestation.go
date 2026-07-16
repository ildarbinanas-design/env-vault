package releaseevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
	"github.com/ildarbinanas-design/env-vault/internal/releasepromotion"
)

const (
	ghBundleMediaType             = "application/vnd.dev.sigstore.bundle.v0.3+json"
	ghVerificationResultMediaType = "application/vnd.dev.sigstore.verificationresult+json;version=0.1"
	inTotoStatementType           = "https://in-toto.io/Statement/v1"
	inTotoPayloadType             = "application/vnd.in-toto+json"
)

var attestationInvocationPattern = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/actions/runs/([1-9][0-9]*)/attempts/([1-9][0-9]*)$`)

// These types intentionally model the complete gh attestation verify JSON
// envelope observed for Sigstore bundle v0.3 and verification-result v0.1.
// The predicate is the one opaque field: it has a different, independently
// versioned schema for SLSA and SPDX. All object members, including members in
// that opaque value, are still checked for duplicates and case variants by
// decodeStrict before typed unknown-field rejection is applied here.
type ghAttestationVerificationEntry struct {
	Attestation        ghAttestation        `json:"attestation"`
	VerificationResult ghVerificationResult `json:"verificationResult"`
}

type ghAttestation struct {
	Bundle    ghSigstoreBundle `json:"bundle"`
	BundleURL string           `json:"bundle_url"`
	Initiator string           `json:"initiator"`
}

type ghSigstoreBundle struct {
	MediaType            string                 `json:"mediaType"`
	VerificationMaterial ghVerificationMaterial `json:"verificationMaterial"`
	DSSEEnvelope         ghDSSEEnvelope         `json:"dsseEnvelope"`
}

type ghVerificationMaterial struct {
	Certificate               ghRawCertificate            `json:"certificate"`
	TlogEntries               []ghTransparencyLogEntry    `json:"tlogEntries"`
	TimestampVerificationData ghTimestampVerificationData `json:"timestampVerificationData"`
}

type ghRawCertificate struct {
	RawBytes string `json:"rawBytes"`
}

type ghTimestampVerificationData struct{}

type ghTransparencyLogEntry struct {
	LogIndex          string                       `json:"logIndex"`
	LogID             ghTransparencyLogID          `json:"logId"`
	KindVersion       ghTransparencyLogKindVersion `json:"kindVersion"`
	IntegratedTime    string                       `json:"integratedTime"`
	InclusionPromise  ghInclusionPromise           `json:"inclusionPromise"`
	InclusionProof    ghInclusionProof             `json:"inclusionProof"`
	CanonicalizedBody string                       `json:"canonicalizedBody"`
}

type ghTransparencyLogID struct {
	KeyID string `json:"keyId"`
}

type ghTransparencyLogKindVersion struct {
	Kind    string `json:"kind"`
	Version string `json:"version"`
}

type ghInclusionPromise struct {
	SignedEntryTimestamp string `json:"signedEntryTimestamp"`
}

type ghInclusionProof struct {
	LogIndex   string       `json:"logIndex"`
	RootHash   string       `json:"rootHash"`
	TreeSize   string       `json:"treeSize"`
	Hashes     []string     `json:"hashes"`
	Checkpoint ghCheckpoint `json:"checkpoint"`
}

type ghCheckpoint struct {
	Envelope string `json:"envelope"`
}

type ghDSSEEnvelope struct {
	Payload     string            `json:"payload"`
	PayloadType string            `json:"payloadType"`
	Signatures  []ghDSSESignature `json:"signatures"`
}

type ghDSSESignature struct {
	Sig string `json:"sig"`
}

type ghVerificationResult struct {
	MediaType          string                `json:"mediaType"`
	Signature          ghVerifiedSignature   `json:"signature"`
	VerifiedTimestamps []ghVerifiedTimestamp `json:"verifiedTimestamps"`
	VerifiedIdentity   ghVerifiedIdentity    `json:"verifiedIdentity"`
	Statement          ghStatement           `json:"statement"`
}

type ghVerifiedSignature struct {
	Certificate ghVerifiedCertificate `json:"certificate"`
}

type ghVerifiedCertificate struct {
	CertificateIssuer                   string `json:"certificateIssuer"`
	SubjectAlternativeName              string `json:"subjectAlternativeName"`
	Issuer                              string `json:"issuer"`
	GitHubWorkflowTrigger               string `json:"githubWorkflowTrigger"`
	GitHubWorkflowSHA                   string `json:"githubWorkflowSHA"`
	GitHubWorkflowName                  string `json:"githubWorkflowName"`
	GitHubWorkflowRepository            string `json:"githubWorkflowRepository"`
	GitHubWorkflowRef                   string `json:"githubWorkflowRef"`
	BuildSignerURI                      string `json:"buildSignerURI"`
	BuildSignerDigest                   string `json:"buildSignerDigest"`
	RunnerEnvironment                   string `json:"runnerEnvironment"`
	SourceRepositoryURI                 string `json:"sourceRepositoryURI"`
	SourceRepositoryDigest              string `json:"sourceRepositoryDigest"`
	SourceRepositoryRef                 string `json:"sourceRepositoryRef"`
	SourceRepositoryIdentifier          string `json:"sourceRepositoryIdentifier"`
	SourceRepositoryOwnerURI            string `json:"sourceRepositoryOwnerURI"`
	SourceRepositoryOwnerIdentifier     string `json:"sourceRepositoryOwnerIdentifier"`
	BuildConfigURI                      string `json:"buildConfigURI"`
	BuildConfigDigest                   string `json:"buildConfigDigest"`
	BuildTrigger                        string `json:"buildTrigger"`
	RunInvocationURI                    string `json:"runInvocationURI"`
	SourceRepositoryVisibilityAtSigning string `json:"sourceRepositoryVisibilityAtSigning"`
}

type ghVerifiedTimestamp struct {
	Type      string `json:"type"`
	URI       string `json:"uri"`
	Timestamp string `json:"timestamp"`
}

type ghVerifiedIdentity struct {
	SubjectAlternativeName ghVerifiedSubjectAlternativeName `json:"subjectAlternativeName"`
	Issuer                 ghVerifiedIssuer                 `json:"issuer"`
}

type ghVerifiedSubjectAlternativeName struct {
	SubjectAlternativeName string `json:"subjectAlternativeName"`
	Regexp                 string `json:"regexp"`
}

type ghVerifiedIssuer struct {
	Issuer string `json:"issuer"`
	Regexp string `json:"regexp"`
}

type ghStatement struct {
	Type          string               `json:"_type"`
	Subject       []ghStatementSubject `json:"subject"`
	PredicateType string               `json:"predicateType"`
	Predicate     json.RawMessage      `json:"predicate"`
}

type ghStatementSubject struct {
	Name   string                   `json:"name"`
	Digest ghStatementSubjectDigest `json:"digest"`
}

type ghStatementSubjectDigest struct {
	SHA256 string `json:"sha256"`
}

type selectedAttestation struct {
	RunID      int64
	RunAttempt int
}

// ValidateAttestationVerificationBundle validates the exact saved gh output
// and its binding to both the promotion manifest and the typed observation.
// It performs no network or credential access.
func ValidateAttestationVerificationBundle(contract releasecontract.Contract, manifest releasepromotion.Manifest, observed []ArchiveAttestation, bundle AttestationVerificationBundle) error {
	if contract.Schemas["attestation_verification_bundle"] != AttestationVerificationBundleSchemaID {
		return fail(CodeInputInvalid, "release contract does not declare the attestation verification bundle schema", nil)
	}
	if bundle.SchemaID != AttestationVerificationBundleSchemaID || bundle.SchemaVersion != AttestationVerificationBundleSchemaVersion {
		return fail(CodeInputInvalid, "attestation verification bundle schema is invalid", nil)
	}
	if bundle.Repository != manifest.Repository || bundle.SourceSHA != manifest.SourceSHA {
		return fail(CodeSourceMismatch, "attestation verification bundle differs from the promotion tuple", nil)
	}
	if len(bundle.Entries) != len(contract.Platforms)*2 || len(observed) != len(contract.Platforms) {
		return fail(CodeInputIncomplete, "attestation verification bundle must contain provenance and SBOM documents for every native archive", nil)
	}

	assets := make(map[string]string, len(contract.Platforms))
	for _, platform := range contract.Platforms {
		for _, asset := range manifest.Assets {
			if asset.Name == platform.Archive {
				assets[asset.Name] = asset.SHA256
				break
			}
		}
		if !digestPattern.MatchString(assets[platform.Archive]) {
			return fail(CodeInputIncomplete, "promotion manifest is missing a native archive digest", nil)
		}
	}
	publisher, ok := contract.WorkflowByID("publisher")
	if !ok {
		return fail(CodeInputIncomplete, "release contract has no publisher workflow", nil)
	}
	wantSignerURI := "https://github.com/" + manifest.Repository + "/.github/workflows/" + publisher.File + "@refs/tags/" + manifest.ReleaseVersion

	entryIndex := 0
	for platformIndex, platform := range contract.Platforms {
		archiveObservation := observed[platformIndex]
		for _, expectation := range []struct {
			kind         string
			predicate    string
			verification AttestationVerification
		}{
			{kind: "provenance", predicate: ProvenancePredicate, verification: archiveObservation.Provenance},
			{kind: "sbom", predicate: SBOMPredicate, verification: archiveObservation.SBOM},
		} {
			entry := bundle.Entries[entryIndex]
			entryIndex++
			if entry.AssetName != platform.Archive || entry.Kind != expectation.kind || entry.PredicateType != expectation.predicate {
				return fail(CodeInputInvalid, "attestation verification bundle order or identity differs from the release contract", nil)
			}
			if entry.DocumentJSON == "" || !digestPattern.MatchString(entry.DocumentSHA256) {
				return fail(CodeInputIncomplete, "attestation verification bundle contains an empty or unsealed document", nil)
			}
			digest := sha256.Sum256([]byte(entry.DocumentJSON))
			if entry.DocumentSHA256 != hex.EncodeToString(digest[:]) {
				return fail(CodeDigestMismatch, "attestation verification document digest mismatch for "+platform.Archive+" "+expectation.kind, nil)
			}
			selected, err := validateRawAttestationDocument([]byte(entry.DocumentJSON), manifest, expectation.predicate, wantSignerURI, assets)
			if err != nil {
				return fail(CodeAttestationVerificationFailed, "invalid saved gh attestation verification for "+platform.Archive+" "+expectation.kind, err)
			}
			verification := expectation.verification
			if verification.DocumentSHA256 != entry.DocumentSHA256 || verification.WorkflowRunID != selected.RunID || verification.WorkflowRunAttempt != selected.RunAttempt {
				return fail(CodeAttestationVerificationFailed, "typed attestation observation differs from the deterministically selected verification for "+platform.Archive+" "+expectation.kind, nil)
			}
		}
	}
	return nil
}

func validateRawAttestationDocument(data []byte, manifest releasepromotion.Manifest, predicate, wantSignerURI string, assets map[string]string) (selectedAttestation, error) {
	var entries []ghAttestationVerificationEntry
	if err := decodeStrict(data, &entries); err != nil {
		return selectedAttestation{}, fmt.Errorf("strictly decode gh attestation verification JSON: %w", err)
	}
	if len(entries) == 0 {
		return selectedAttestation{}, fmt.Errorf("gh attestation verification returned no entries")
	}
	var selected selectedAttestation
	for index, entry := range entries {
		result := entry.VerificationResult
		certificate := result.Signature.Certificate
		if entry.Attestation.Bundle.MediaType != ghBundleMediaType || entry.Attestation.Bundle.DSSEEnvelope.PayloadType != inTotoPayloadType || entry.Attestation.Bundle.DSSEEnvelope.Payload == "" || len(entry.Attestation.Bundle.DSSEEnvelope.Signatures) == 0 || entry.Attestation.Bundle.VerificationMaterial.Certificate.RawBytes == "" {
			return selectedAttestation{}, fmt.Errorf("entry %d has an incomplete or incompatible Sigstore bundle", index)
		}
		if result.MediaType != ghVerificationResultMediaType || result.Statement.Type != inTotoStatementType || len(result.Statement.Predicate) == 0 {
			return selectedAttestation{}, fmt.Errorf("entry %d has an incomplete or incompatible verification result", index)
		}
		if result.Statement.PredicateType != predicate {
			return selectedAttestation{}, fmt.Errorf("entry %d predicate %q does not match %q", index, result.Statement.PredicateType, predicate)
		}
		if certificate.GitHubWorkflowSHA != manifest.SourceSHA || certificate.SourceRepositoryDigest != manifest.SourceSHA || certificate.GitHubWorkflowRepository != manifest.Repository || certificate.BuildSignerURI != wantSignerURI {
			return selectedAttestation{}, fmt.Errorf("entry %d certificate does not bind the exact source, repository, publisher workflow, and tag", index)
		}
		invocation := attestationInvocationPattern.FindStringSubmatch(certificate.RunInvocationURI)
		if len(invocation) != 4 || invocation[1] != manifest.Repository {
			return selectedAttestation{}, fmt.Errorf("entry %d run invocation URI is not exact", index)
		}
		runID, runErr := strconv.ParseInt(invocation[2], 10, 64)
		attempt, attemptErr := strconv.Atoi(invocation[3])
		if runErr != nil || attemptErr != nil || runID <= 0 || attempt <= 0 {
			return selectedAttestation{}, fmt.Errorf("entry %d run invocation tuple is invalid", index)
		}
		if err := validateAttestationSubjects(result.Statement.Subject, assets); err != nil {
			return selectedAttestation{}, fmt.Errorf("entry %d subjects: %w", index, err)
		}
		if runID > selected.RunID || (runID == selected.RunID && attempt > selected.RunAttempt) {
			selected = selectedAttestation{RunID: runID, RunAttempt: attempt}
		}
	}
	return selected, nil
}

func validateAttestationSubjects(subjects []ghStatementSubject, assets map[string]string) error {
	if len(subjects) != len(assets) {
		return fmt.Errorf("got %d subjects, want %d native archives", len(subjects), len(assets))
	}
	seen := make(map[string]bool, len(subjects))
	for _, subject := range subjects {
		want, ok := assets[subject.Name]
		if !ok || seen[subject.Name] || subject.Digest.SHA256 != want || !digestPattern.MatchString(subject.Digest.SHA256) {
			return fmt.Errorf("subject %q is duplicate, unknown, or has the wrong digest", subject.Name)
		}
		seen[subject.Name] = true
	}
	return nil
}
