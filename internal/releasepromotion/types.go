// Package releasepromotion records and verifies offline, exact-attempt release
// promotion evidence. It has no network, credential, or GitHub client code.
package releasepromotion

import (
	"errors"
	"fmt"

	"github.com/ildarbinanas-design/env-vault/internal/e2ebaseline"
)

const (
	SchemaVersion                = 1
	LiteralVersionEvidenceSchema = "env-vault.literal-version-results.v1"

	CodePromotionManifestInvalid = "PROMOTION_MANIFEST_INVALID"
	CodeArtifactInventoryInvalid = "ARTIFACT_INVENTORY_INVALID"
	CodeDigestMismatch           = "DIGEST_MISMATCH"
	CodeVersionMismatch          = "VERSION_MISMATCH"
	CodeSourceMismatch           = "SOURCE_MISMATCH"
)

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

func ErrorCode(err error) string {
	var coded *CodedError
	if errors.As(err, &coded) {
		return coded.Code
	}
	return ""
}

func coded(code, detail string, err error) error {
	return &CodedError{Code: code, Detail: detail, Err: err}
}

type Workflow struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	File       string `json:"file"`
	RunID      int64  `json:"run_id"`
	RunAttempt int    `json:"run_attempt"`
	Event      string `json:"event"`
	HeadSHA    string `json:"head_sha"`
}

type FileDigest struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type LiteralVersionResults struct {
	Flag    string `json:"flag"`
	Command string `json:"command"`
	JSON    string `json:"json"`
}

type LiteralVersionCommand struct {
	Surface  string   `json:"surface"`
	Args     []string `json:"args"`
	Stdout   string   `json:"stdout"`
	Stderr   string   `json:"stderr"`
	ExitCode int      `json:"exit_code"`
}

// LiteralVersionEvidence is produced by the native execution probe. The
// offline release checker only parses and binds these saved bytes.
type LiteralVersionEvidence struct {
	SchemaID       string                  `json:"schema_id"`
	SchemaVersion  int                     `json:"schema_version"`
	PlatformID     string                  `json:"platform_id"`
	SourceSHA      string                  `json:"source_sha"`
	ReleaseVersion string                  `json:"release_version"`
	Repository     string                  `json:"repository"`
	RunID          int64                   `json:"run_id"`
	RunAttempt     int                     `json:"run_attempt"`
	Binary         FileDigest              `json:"binary"`
	Commands       []LiteralVersionCommand `json:"commands"`
	Results        LiteralVersionResults   `json:"results"`
	Result         string                  `json:"result"`
	EvidenceSHA256 string                  `json:"evidence_sha256"`
}

type LiteralVersionBinding struct {
	File     FileDigest             `json:"file"`
	Evidence LiteralVersionEvidence `json:"evidence"`
}

type PlatformProof struct {
	SchemaID          string                `json:"schema_id"`
	SchemaVersion     int                   `json:"schema_version"`
	PlatformID        string                `json:"platform_id"`
	GOOS              string                `json:"goos"`
	GOARCH            string                `json:"goarch"`
	SourceSHA         string                `json:"source_sha"`
	ReleaseVersion    string                `json:"release_version"`
	Repository        string                `json:"repository"`
	RunID             int64                 `json:"run_id"`
	RunAttempt        int                   `json:"run_attempt"`
	ArtifactName      string                `json:"artifact_name"`
	ProofArtifactName string                `json:"proof_artifact_name"`
	Archive           FileDigest            `json:"archive"`
	Checksum          FileDigest            `json:"checksum"`
	Binary            FileDigest            `json:"binary"`
	LiteralVersion    LiteralVersionBinding `json:"literal_version"`
	Result            string                `json:"result"`
	ProofSHA256       string                `json:"proof_sha256"`
}

type SourceQualityResults struct {
	Module   string            `json:"module"`
	Test     string            `json:"test"`
	Vet      string            `json:"vet"`
	Smoke    string            `json:"smoke"`
	Race     string            `json:"race"`
	Licenses map[string]string `json:"licenses"`
}

// SourceQualityObservedJobs preserves the two GitHub workflow conclusions
// used to derive the component-level release result. Values are intentionally
// GitHub's literal "success", not promotion's normalized "pass".
type SourceQualityObservedJobs struct {
	SourceQuality string `json:"source_quality"`
	LicenseMatrix string `json:"license_matrix"`
}

type SourceQualityProof struct {
	SchemaID       string                    `json:"schema_id"`
	SchemaVersion  int                       `json:"schema_version"`
	SourceSHA      string                    `json:"source_sha"`
	ReleaseVersion string                    `json:"release_version"`
	Repository     string                    `json:"repository"`
	Workflow       Workflow                  `json:"workflow"`
	ObservedJobs   SourceQualityObservedJobs `json:"observed_jobs"`
	Results        SourceQualityResults      `json:"results"`
	Result         string                    `json:"result"`
	ProofSHA256    string                    `json:"proof_sha256"`
}

type SourceQualityBinding struct {
	File  FileDigest         `json:"file"`
	Proof SourceQualityProof `json:"proof"`
}

type MatrixProofBinding struct {
	File  FileDigest              `json:"file"`
	Proof e2ebaseline.MatrixProof `json:"proof"`
}

type Manifest struct {
	SchemaID               string               `json:"schema_id"`
	SchemaVersion          int                  `json:"schema_version"`
	SourceSHA              string               `json:"source_sha"`
	ReleaseVersion         string               `json:"release_version"`
	Repository             string               `json:"repository"`
	Workflow               Workflow             `json:"workflow"`
	ContractSchema         string               `json:"contract_schema"`
	ContractSHA256         string               `json:"contract_sha256"`
	ContractSemanticSHA256 string               `json:"contract_semantic_sha256"`
	SemanticSuiteHash      string               `json:"semantic_suite_hash"`
	SourceQuality          SourceQualityBinding `json:"source_quality"`
	E2EMatrix              MatrixProofBinding   `json:"e2e_matrix"`
	Platforms              []PlatformProof      `json:"platforms"`
	Assets                 []FileDigest         `json:"assets"`
	CreatedAt              string               `json:"created_at"`
	Result                 string               `json:"result"`
	ManifestSHA256         string               `json:"manifest_sha256"`
}
