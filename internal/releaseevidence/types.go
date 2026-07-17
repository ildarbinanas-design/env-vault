// Package releaseevidence assembles and verifies the durable, offline record
// of an env-vault release. The package deliberately has no transport or
// credential support: callers save GitHub observations first and pass those
// versioned documents to this package as files or typed values.
package releaseevidence

import (
	"github.com/ildarbinanas-design/env-vault/internal/releasemetrics"
	"github.com/ildarbinanas-design/env-vault/internal/releasepromotion"
	"github.com/ildarbinanas-design/env-vault/internal/releasesettings"
)

const (
	SchemaID      = "env-vault.release-evidence.v1"
	SchemaVersion = 1

	ObservationSchemaID                        = "env-vault.release-observation.v1"
	HealthProofSchemaID                        = "env-vault.release-health-proof.v1"
	AuthorizationSchemaID                      = "env-vault.release-authorization.v1"
	AttestationVerificationBundleSchemaID      = "env-vault.attestation-verification-bundle.v1"
	ObservationSchemaVersion                   = 1
	AuthorizationSchemaVersion                 = 1
	AttestationVerificationBundleSchemaVersion = 1

	ProvenancePredicate = "https://slsa.dev/provenance/v1"
	SBOMPredicate       = "https://spdx.dev/Document/v2.3"
)

// Authorization is the saved, machine-readable record of the one human
// release checkpoint and the workflow identities that consumed it. It does
// not contain credentials or perform remote reads; gh observations must be
// saved into this shape before evidence assembly.
type Authorization struct {
	SchemaID           string                     `json:"schema_id"`
	SchemaVersion      int                        `json:"schema_version"`
	Repository         string                     `json:"repository"`
	ReleaseVersion     string                     `json:"release_version"`
	GeneratedReleasePR GeneratedReleasePRIdentity `json:"generated_release_pr"`
	Confirmation       ReleaseConfirmation        `json:"confirmation"`
	ReleaseSourceSHA   string                     `json:"release_source_sha"`
	PlanningWorkflow   CompletedContractWorkflow  `json:"planning_workflow"`
	ReleasePRCI        ReleasePRCIIdentity        `json:"release_pr_ci"`
	EvidenceWorkflow   ContractWorkflowInvocation `json:"evidence_workflow"`
	Result             string                     `json:"result"`
}

type GeneratedReleasePRIdentity struct {
	Number   int64  `json:"number"`
	HeadSHA  string `json:"head_sha"`
	MergeSHA string `json:"merge_sha"`
	MergedAt string `json:"merged_at"`
}

// ReleaseConfirmation binds the exact human checkpoint to the generated PR
// tuple. BodySHA256 is the SHA-256 of the required canonical UTF-8 comment
// body; CreatedAt and UpdatedAt must both predate the PR merge.
type ReleaseConfirmation struct {
	CommentID        int64  `json:"comment_id"`
	URL              string `json:"url"`
	Actor            string `json:"actor"`
	ActorAssociation string `json:"actor_association"`
	CreatedAt        string `json:"created_at"`
	UpdatedAt        string `json:"updated_at"`
	BodySHA256       string `json:"body_sha256"`
}

// CompletedContractWorkflow binds a completed run to a workflow declared in
// the release contract.
type CompletedContractWorkflow struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	File       string `json:"file"`
	RunID      int64  `json:"run_id"`
	RunAttempt int    `json:"run_attempt"`
	HeadSHA    string `json:"head_sha"`
	Conclusion string `json:"conclusion"`
}

// ReleasePRCIIdentity binds the direct Actions run/job head to the exact
// generated release-PR head. It is distinct from the post-merge release source.
type ReleasePRCIIdentity struct {
	ID                        string `json:"id"`
	Name                      string `json:"name"`
	File                      string `json:"file"`
	RunID                     int64  `json:"run_id"`
	RunAttempt                int    `json:"run_attempt"`
	Event                     string `json:"event"`
	HeadSHA                   string `json:"head_sha"`
	PullRequestNumber         int64  `json:"pull_request_number"`
	GeneratedReleasePRHeadSHA string `json:"generated_release_pr_head_sha"`
	Conclusion                string `json:"conclusion"`
}

// ContractWorkflowInvocation identifies the currently executing evidence
// workflow before it completes, so it has no conclusion field.
type ContractWorkflowInvocation struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	File       string `json:"file"`
	RunID      int64  `json:"run_id"`
	RunAttempt int    `json:"run_attempt"`
}

// Observation is the strict, saved post-publication input boundary. It is
// intentionally descriptive rather than operational: gh performs all remote
// reads and writes, while this package only validates the saved result.
type Observation struct {
	SchemaID                  string                      `json:"schema_id"`
	SchemaVersion             int                         `json:"schema_version"`
	Repository                string                      `json:"repository"`
	ReleaseVersion            string                      `json:"release_version"`
	SourceSHA                 string                      `json:"source_sha"`
	PublisherRepairMode       string                      `json:"publisher_repair_mode"`
	ObservedAt                string                      `json:"observed_at"`
	Tag                       TagObservation              `json:"tag"`
	Release                   ReleaseObservation          `json:"release"`
	Attestations              []ArchiveAttestation        `json:"attestations"`
	Homebrew                  HomebrewObservation         `json:"homebrew"`
	BlockedVersions           []BlockedVersionObservation `json:"blocked_versions"`
	AbandonedRelease          AbandonedReleaseObservation `json:"abandoned_release"`
	RepositoryReleaseSettings releasesettings.Proof       `json:"repository_release_settings"`
	Health                    HealthProof                 `json:"health"`
}

type TagObservation struct {
	Name             string `json:"name"`
	RefSHA           string `json:"ref_sha"`
	TargetSHA        string `json:"target_sha"`
	Immutable        bool   `json:"immutable"`
	RulesetProtected bool   `json:"ruleset_protected"`
}

type ReleaseObservation struct {
	State             string          `json:"state"`
	URL               string          `json:"url"`
	TagName           string          `json:"tag_name"`
	TargetSHA         string          `json:"target_sha"`
	Draft             bool            `json:"draft"`
	Prerelease        bool            `json:"prerelease"`
	PublishedAt       string          `json:"published_at"`
	NoClobberVerified bool            `json:"no_clobber_verified"`
	Assets            []ObservedAsset `json:"assets"`
}

type ObservedAsset struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type ArchiveAttestation struct {
	AssetName   string                  `json:"asset_name"`
	AssetSHA256 string                  `json:"asset_sha256"`
	Provenance  AttestationVerification `json:"provenance"`
	SBOM        AttestationVerification `json:"sbom"`
}

type AttestationVerification struct {
	PredicateType      string `json:"predicate_type"`
	Verified           bool   `json:"verified"`
	Repository         string `json:"repository"`
	SignerWorkflow     string `json:"signer_workflow"`
	SourceSHA          string `json:"source_sha"`
	WorkflowRunID      int64  `json:"workflow_run_id"`
	WorkflowRunAttempt int    `json:"workflow_run_attempt"`
	VerifiedAt         string `json:"verified_at"`
	DocumentSHA256     string `json:"document_sha256"`
}

// AttestationVerificationBundle preserves the exact JSON emitted by each
// native `gh attestation verify --format json` invocation. Entries are in
// release-contract platform order, provenance then SBOM for each archive.
// DocumentJSON is a string, rather than json.RawMessage, so its byte-for-byte
// SHA-256 binding survives evidence encoding and replay.
type AttestationVerificationBundle struct {
	SchemaID      string                               `json:"schema_id"`
	SchemaVersion int                                  `json:"schema_version"`
	Repository    string                               `json:"repository"`
	SourceSHA     string                               `json:"source_sha"`
	Entries       []AttestationVerificationBundleEntry `json:"entries"`
}

type AttestationVerificationBundleEntry struct {
	AssetName      string `json:"asset_name"`
	Kind           string `json:"kind"`
	PredicateType  string `json:"predicate_type"`
	DocumentSHA256 string `json:"document_sha256"`
	DocumentJSON   string `json:"document_json"`
}

type HomebrewObservation struct {
	Repository           string              `json:"repository"`
	FormulaPath          string              `json:"formula_path"`
	FormulaSHA256        string              `json:"formula_sha256"`
	Version              string              `json:"version"`
	VersionMonotonic     bool                `json:"version_monotonic"`
	PRNumber             int64               `json:"pr_number"`
	PRURL                string              `json:"pr_url"`
	PRHeadSHA            string              `json:"pr_head_sha"`
	PRMergeSHA           string              `json:"pr_merge_sha"`
	TapSHA               string              `json:"tap_sha"`
	MergeIsAncestorOfTap bool                `json:"merge_is_ancestor_of_tap"`
	PRHeadCI             ExternalWorkflowRun `json:"pr_head_ci"`
	PostMergeCI          ExternalWorkflowRun `json:"post_merge_ci"`
}

type ExternalWorkflowRun struct {
	RunID      int64  `json:"run_id"`
	RunAttempt int    `json:"run_attempt"`
	Workflow   string `json:"workflow"`
	Event      string `json:"event"`
	HeadSHA    string `json:"head_sha"`
	Conclusion string `json:"conclusion"`
	URL        string `json:"url"`
}

type BlockedVersionObservation struct {
	Version       string `json:"version"`
	TagSHA        string `json:"tag_sha"`
	TagExists     bool   `json:"tag_exists"`
	ReleaseExists bool   `json:"release_exists"`
}

// AbandonedReleaseObservation proves that the merged v0.0.12 Release Please
// proposal remains an explicitly abandoned planning record, never a tag or a
// GitHub Release. The immutable incident tuple comes from the release contract.
type AbandonedReleaseObservation struct {
	State                       string                     `json:"state"`
	Version                     string                     `json:"version"`
	SourceSHA                   string                     `json:"source_sha"`
	GeneratedReleasePR          GeneratedReleasePRIdentity `json:"generated_release_pr"`
	PullRequestState            string                     `json:"pull_request_state"`
	PullRequestMerged           bool                       `json:"pull_request_merged"`
	PullRequestTitle            string                     `json:"pull_request_title"`
	PullRequestAuthor           string                     `json:"pull_request_author"`
	BaseRef                     string                     `json:"base_ref"`
	BaseRepository              string                     `json:"base_repository"`
	Labels                      []string                   `json:"labels"`
	BoundaryIsAncestorOfRelease bool                       `json:"boundary_is_ancestor_of_release"`
	TagExists                   bool                       `json:"tag_exists"`
	GitHubReleaseExists         bool                       `json:"github_release_exists"`
	ReasonCode                  string                     `json:"reason_code"`
	ObservedAt                  string                     `json:"observed_at"`
	SemanticContractSHA256      string                     `json:"semantic_contract_sha256"`
}

// HealthProof is sealed before evidence assembly so an independently saved
// health result cannot be silently changed while the final record is built.
type HealthProof struct {
	SchemaID                    string `json:"schema_id"`
	SchemaVersion               int    `json:"schema_version"`
	Repository                  string `json:"repository"`
	ReleaseVersion              string `json:"release_version"`
	SourceSHA                   string `json:"source_sha"`
	PublisherRunID              int64  `json:"publisher_run_id"`
	PublisherRunAttempt         int    `json:"publisher_run_attempt"`
	CheckedAt                   string `json:"checked_at"`
	TagExactSource              bool   `json:"tag_exact_source"`
	ReleasePublished            bool   `json:"release_published"`
	AssetsExact                 bool   `json:"assets_exact"`
	AttestationsExact           bool   `json:"attestations_exact"`
	HomebrewExact               bool   `json:"homebrew_exact"`
	HomebrewPRHeadCISuccess     bool   `json:"homebrew_pr_head_ci_success"`
	HomebrewPostMergeCISuccess  bool   `json:"homebrew_post_merge_ci_success"`
	BlockedVersionPolicyExact   bool   `json:"blocked_version_policy_exact"`
	AbandonedReleasePolicyExact bool   `json:"abandoned_release_policy_exact"`
	Result                      string `json:"result"`
	ProofSHA256                 string `json:"proof_sha256"`
}

type PromotionRecord struct {
	ManifestSHA256 string                    `json:"manifest_sha256"`
	Manifest       releasepromotion.Manifest `json:"manifest"`
}

// Evidence is canonical: all slices are ordered by the release contract and
// EvidenceSHA256 is the digest of the compact JSON form with that field empty.
type Evidence struct {
	SchemaID                      string                        `json:"schema_id"`
	SchemaVersion                 int                           `json:"schema_version"`
	Repository                    string                        `json:"repository"`
	ReleaseVersion                string                        `json:"release_version"`
	SourceSHA                     string                        `json:"source_sha"`
	PublisherRepairMode           string                        `json:"publisher_repair_mode"`
	ObservedAt                    string                        `json:"observed_at"`
	Result                        string                        `json:"result"`
	Authorization                 Authorization                 `json:"authorization"`
	Tag                           TagObservation                `json:"tag"`
	Release                       ReleaseObservation            `json:"release"`
	Promotion                     PromotionRecord               `json:"promotion"`
	CIMetrics                     releasemetrics.Metrics        `json:"ci_metrics"`
	PublisherMetrics              releasemetrics.Metrics        `json:"publisher_metrics"`
	Assets                        []releasepromotion.FileDigest `json:"assets"`
	Attestations                  []ArchiveAttestation          `json:"attestations"`
	AttestationVerificationBundle AttestationVerificationBundle `json:"attestation_verification_bundle"`
	Homebrew                      HomebrewObservation           `json:"homebrew"`
	BlockedVersions               []BlockedVersionObservation   `json:"blocked_versions"`
	AbandonedRelease              AbandonedReleaseObservation   `json:"abandoned_release"`
	RepositoryReleaseSettings     releasesettings.Proof         `json:"repository_release_settings"`
	Health                        HealthProof                   `json:"health"`
	EvidenceSHA256                string                        `json:"evidence_sha256"`
}
