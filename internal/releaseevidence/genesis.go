package releaseevidence

import (
	"bytes"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

const (
	GenesisSchemaID       = "env-vault.release-evidence-genesis.v1"
	GenesisSchemaVersion  = 1
	GenesisAnchorPath     = "evidence/genesis.v1.json"
	GenesisLedgerMode     = "evidence-only-parentless-v1"
	MaxGenesisAnchorBytes = 16 << 10
)

// GenesisAnchor is the immutable trust root for a freshly created evidence
// ledger. It intentionally does not claim the not-yet-created Git commit SHA.
// AnchorSHA256 is over canonical compact JSON with that field empty.
type GenesisAnchor struct {
	SchemaID               string `json:"schema_id"`
	SchemaVersion          int    `json:"schema_version"`
	LedgerMode             string `json:"ledger_mode"`
	Repository             string `json:"repository"`
	SourceSHA              string `json:"source_sha"`
	FirstReleaseVersion    string `json:"first_release_version"`
	EvidenceFormatSchemaID string `json:"evidence_format_schema_id"`
	EvidenceFormatVersion  int    `json:"evidence_format_schema_version"`
	FirstBundleSHA256      string `json:"first_bundle_sha256"`
	PublisherRunID         int64  `json:"publisher_run_id"`
	PublisherRunAttempt    int    `json:"publisher_run_attempt"`
	PublisherRepairMode    string `json:"publisher_repair_mode"`
	EvidenceWorkflowID     string `json:"evidence_workflow_id"`
	EvidenceWorkflowName   string `json:"evidence_workflow_name"`
	EvidenceWorkflowFile   string `json:"evidence_workflow_file"`
	EvidenceRunID          int64  `json:"evidence_run_id"`
	EvidenceRunAttempt     int    `json:"evidence_run_attempt"`
	AnchorSHA256           string `json:"anchor_sha256"`
}

func BuildGenesisAnchor(bundle Bundle, evidence Evidence) (GenesisAnchor, error) {
	if err := validateBundleIndex(bundle); err != nil {
		return GenesisAnchor{}, err
	}
	wantBundle, err := BundleSHA256(bundle)
	if err != nil || bundle.BundleSHA256 != wantBundle {
		return GenesisAnchor{}, fail(CodeDigestMismatch, "cannot anchor a bundle with an invalid self-digest", err)
	}
	if err := verifyGenesisBundleEvidenceTuple(bundle, evidence); err != nil {
		return GenesisAnchor{}, err
	}
	evidenceWorkflow := evidence.Authorization.EvidenceWorkflow
	anchor := GenesisAnchor{
		SchemaID: GenesisSchemaID, SchemaVersion: GenesisSchemaVersion, LedgerMode: GenesisLedgerMode,
		Repository: bundle.Repository, SourceSHA: bundle.SourceSHA, FirstReleaseVersion: bundle.ReleaseVersion,
		EvidenceFormatSchemaID: bundle.SchemaID, EvidenceFormatVersion: bundle.SchemaVersion,
		FirstBundleSHA256: bundle.BundleSHA256,
		PublisherRunID:    bundle.PublisherRunID, PublisherRunAttempt: bundle.PublisherRunAttempt,
		PublisherRepairMode: bundle.PublisherRepairMode,
		EvidenceWorkflowID:  evidenceWorkflow.ID, EvidenceWorkflowName: evidenceWorkflow.Name,
		EvidenceWorkflowFile: evidenceWorkflow.File,
		EvidenceRunID:        evidenceWorkflow.RunID, EvidenceRunAttempt: evidenceWorkflow.RunAttempt,
	}
	if err := validateGenesisIdentity(anchor); err != nil {
		return GenesisAnchor{}, err
	}
	anchor.AnchorSHA256, err = GenesisAnchorSHA256(anchor)
	if err != nil {
		return GenesisAnchor{}, fail(CodeDigestMismatch, "seal genesis anchor", err)
	}
	return anchor, nil
}

func ParseGenesisAnchor(data []byte) (GenesisAnchor, error) {
	if len(data) == 0 || len(data) > MaxGenesisAnchorBytes {
		return GenesisAnchor{}, fail(CodeInputInvalid, "genesis anchor size is outside the supported limit", nil)
	}
	var anchor GenesisAnchor
	if err := decodeStrict(data, &anchor); err != nil {
		return GenesisAnchor{}, fail(CodeInputInvalid, "strictly decode genesis anchor", err)
	}
	canonical, err := MarshalJSON(anchor)
	if err != nil || !bytes.Equal(canonical, data) {
		return GenesisAnchor{}, fail(CodeInputInvalid, "genesis anchor is not canonical JSON", err)
	}
	return anchor, nil
}

func VerifyGenesisAnchor(anchor GenesisAnchor, bundle *Bundle, evidence *Evidence) error {
	if err := validateGenesisIdentity(anchor); err != nil {
		return err
	}
	want, err := GenesisAnchorSHA256(anchor)
	if err != nil || anchor.AnchorSHA256 != want || !digestPattern.MatchString(anchor.AnchorSHA256) {
		return fail(CodeDigestMismatch, "genesis anchor self-digest mismatch", err)
	}
	if bundle != nil {
		if evidence == nil {
			return fail(CodeInputIncomplete, "genesis bundle verification requires reconstructed evidence", nil)
		}
		if err := validateBundleIndex(*bundle); err != nil {
			return err
		}
		wantBundle, digestErr := BundleSHA256(*bundle)
		if digestErr != nil || bundle.BundleSHA256 != wantBundle {
			return fail(CodeDigestMismatch, "genesis bundle self-digest mismatch", digestErr)
		}
		if anchor.Repository != bundle.Repository || anchor.SourceSHA != bundle.SourceSHA ||
			anchor.FirstReleaseVersion != bundle.ReleaseVersion || anchor.EvidenceFormatSchemaID != bundle.SchemaID ||
			anchor.EvidenceFormatVersion != bundle.SchemaVersion || anchor.FirstBundleSHA256 != bundle.BundleSHA256 ||
			anchor.PublisherRunID != bundle.PublisherRunID || anchor.PublisherRunAttempt != bundle.PublisherRunAttempt ||
			anchor.PublisherRepairMode != bundle.PublisherRepairMode {
			return fail(CodeSourceMismatch, "genesis anchor differs from its first bundle tuple", nil)
		}
		if err := verifyGenesisBundleEvidenceTuple(*bundle, *evidence); err != nil {
			return err
		}
		workflow := evidence.Authorization.EvidenceWorkflow
		if anchor.EvidenceWorkflowID != workflow.ID || anchor.EvidenceWorkflowName != workflow.Name ||
			anchor.EvidenceWorkflowFile != workflow.File || anchor.EvidenceRunID != workflow.RunID ||
			anchor.EvidenceRunAttempt != workflow.RunAttempt {
			return fail(CodeSourceMismatch, "genesis evidence workflow differs from the digest-bound evidence core", nil)
		}
	} else if evidence != nil {
		return fail(CodeInputIncomplete, "genesis evidence verification requires its bundle", nil)
	}
	return nil
}

func GenesisAnchorSHA256(anchor GenesisAnchor) (string, error) {
	anchor.AnchorSHA256 = ""
	return compactSHA256(anchor)
}

func validateGenesisIdentity(anchor GenesisAnchor) error {
	if anchor.SchemaID != GenesisSchemaID || anchor.SchemaVersion != GenesisSchemaVersion || anchor.LedgerMode != GenesisLedgerMode ||
		!validRepository(anchor.Repository) || !shaPattern.MatchString(anchor.SourceSHA) || !releasecontract.IsVersion(anchor.FirstReleaseVersion) ||
		anchor.EvidenceFormatSchemaID != BundleSchemaID || anchor.EvidenceFormatVersion != BundleSchemaVersion ||
		!digestPattern.MatchString(anchor.FirstBundleSHA256) || anchor.PublisherRunID <= 0 || anchor.PublisherRunAttempt <= 0 ||
		!validBundleRepairMode(anchor.PublisherRepairMode) || anchor.EvidenceWorkflowID != "release_evidence" ||
		anchor.EvidenceWorkflowName != "release-evidence" || anchor.EvidenceWorkflowFile != "release-evidence.yml" ||
		anchor.EvidenceRunID <= 0 || anchor.EvidenceRunAttempt <= 0 {
		return fail(CodeInputInvalid, "genesis anchor identity is invalid", nil)
	}
	return nil
}

func verifyGenesisBundleEvidenceTuple(bundle Bundle, evidence Evidence) error {
	wantEvidence, err := EvidenceSHA256(evidence)
	if err != nil || evidence.EvidenceSHA256 != wantEvidence {
		return fail(CodeDigestMismatch, "genesis reconstructed evidence self-digest mismatch", err)
	}
	if bundle.Repository != evidence.Repository || bundle.ReleaseVersion != evidence.ReleaseVersion ||
		bundle.SourceSHA != evidence.SourceSHA || bundle.PublisherRepairMode != evidence.PublisherRepairMode ||
		bundle.PublisherRunID != evidence.PublisherMetrics.RunID || bundle.PublisherRunAttempt != evidence.PublisherMetrics.Attempt ||
		bundle.LegacyEvidenceSHA256 != evidence.EvidenceSHA256 {
		return fail(CodeSourceMismatch, "genesis bundle differs from reconstructed evidence", nil)
	}
	return nil
}
