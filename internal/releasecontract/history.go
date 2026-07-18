package releasecontract

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
)

// HistoricalIdentity is the complete immutable tuple required to opt into
// archival v1 verification. It is deliberately not inferred from filenames or
// accepted by operational contract loaders.
type HistoricalIdentity struct {
	Repository                             string `json:"repository"`
	ReleaseVersion                         string `json:"release_version"`
	SourceSHA                              string `json:"source_sha"`
	ContractGeneration                     string `json:"contract_generation"`
	EvidenceFormat                         string `json:"evidence_format"`
	EvidenceCommitSHA                      string `json:"evidence_commit_sha"`
	EvidenceParentCommitSHA                string `json:"evidence_parent_commit_sha"`
	EvidenceRootPath                       string `json:"evidence_root_path"`
	EvidenceRootFileSHA256                 string `json:"evidence_root_file_sha256"`
	EvidenceRootSemanticSHA256             string `json:"evidence_root_semantic_sha256"`
	EvidenceRootSchemaID                   string `json:"evidence_root_schema_id"`
	EvidenceRootSchemaVersion              int    `json:"evidence_root_schema_version"`
	ReconstructedLegacyEvidenceSHA256      string `json:"reconstructed_legacy_evidence_sha256,omitempty"`
	ReconstructedLegacyCanonicalJSONSHA256 string `json:"reconstructed_legacy_canonical_json_sha256,omitempty"`
	ReconstructedLegacyCanonicalJSONSize   int64  `json:"reconstructed_legacy_canonical_json_size,omitempty"`
	PublisherRunID                         int64  `json:"publisher_run_id,omitempty"`
	PublisherRunAttempt                    int    `json:"publisher_run_attempt,omitempty"`
	EvidenceRunID                          int64  `json:"evidence_run_id,omitempty"`
	EvidenceRunAttempt                     int    `json:"evidence_run_attempt,omitempty"`
	CompactArtifactID                      int64  `json:"compact_artifact_id,omitempty"`
	CompactArtifactDigest                  string `json:"compact_artifact_digest,omitempty"`
}

// HistoricalBundleObservation is the complete identity available from an
// offline compact bundle plus its immutable evidence-branch commit. The
// release contract package deliberately does not parse bundle internals, which
// keeps the contract/evidence dependency one-directional.
type HistoricalBundleObservation struct {
	Repository                             string
	ReleaseVersion                         string
	SourceSHA                              string
	EvidenceCommitSHA                      string
	EvidenceRootFileSHA256                 string
	EvidenceRootSemanticSHA256             string
	EvidenceRootSchemaID                   string
	EvidenceRootSchemaVersion              int
	ReconstructedLegacyEvidenceSHA256      string
	ReconstructedLegacyCanonicalJSONSHA256 string
	ReconstructedLegacyCanonicalJSONSize   int64
	EvidenceParentCommitSHA                string
	EvidenceRunID                          int64
	EvidenceRunAttempt                     int
	CompactArtifactID                      int64
	CompactArtifactDigest                  string
	PublisherRunID                         int64
	PublisherRunAttempt                    int
}

type HistoricalSourceAuthorization struct {
	SchemaID      string             `json:"schema_id"`
	SchemaVersion int                `json:"schema_version"`
	OK            bool               `json:"ok"`
	Identity      HistoricalIdentity `json:"identity"`
}

type historicalRegistry struct {
	SchemaID      string                    `json:"schema_id"`
	SchemaVersion int                       `json:"schema_version"`
	Contract      historicalContractBinding `json:"contract"`
	Entries       []HistoricalIdentity      `json:"entries"`
}

type historicalContractBinding struct {
	Path           string `json:"path"`
	SchemaID       string `json:"schema_id"`
	SchemaVersion  int    `json:"schema_version"`
	FileSHA256     string `json:"file_sha256"`
	SemanticSHA256 string `json:"semantic_sha256"`
}

type legacyContractV1 struct {
	SchemaID             string              `json:"schema_id"`
	SchemaVersion        int                 `json:"schema_version"`
	VersionPolicy        legacyVersionPolicy `json:"version_policy"`
	Naming               Naming              `json:"naming"`
	Platforms            []Platform          `json:"platforms"`
	Assets               []string            `json:"assets"`
	Workflows            []legacyWorkflow    `json:"workflows"`
	MainRequiredChecks   []RequiredCheck     `json:"main_required_checks"`
	Apps                 []legacyApp         `json:"apps"`
	ReleaseStages        []ReleaseStage      `json:"release_stages"`
	AllowedRepairActions []RepairAction      `json:"allowed_repair_actions"`
	ActionCodes          []string            `json:"action_codes"`
	ReasonCodes          []string            `json:"reason_codes"`
	ErrorCodes           []string            `json:"error_codes"`
	Schemas              map[string]string   `json:"schemas"`
}

type legacyVersionPolicy struct {
	Pattern               string                      `json:"pattern"`
	ReleasePleaseRecovery ReleasePleaseRecoveryPolicy `json:"release_please_recovery"`
	BlockedVersions       []BlockedVersion            `json:"blocked_versions"`
	LegacyRebuild         LegacyRebuildPolicy         `json:"legacy_rebuild"`
}

type legacyWorkflow struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	File string `json:"file"`
}

type legacyApp struct {
	ID             string `json:"id"`
	Slug           string `json:"slug"`
	Repository     string `json:"repository"`
	Environment    string `json:"environment"`
	AuditWorkflow  string `json:"audit_workflow"`
	CIWorkflowFile string `json:"ci_workflow_file,omitempty"`
	CIWorkflowName string `json:"ci_workflow_name,omitempty"`
}

const maxHistoricalEvidenceBytes = 16 << 20

// LoadHistoricalEvidence verifies the raw evidence bytes and their embedded
// identity before exposing an archival v1 contract to the normal offline
// evidence verifier. The raw digest is always computed here; it is never a
// trusted caller flag.
func LoadHistoricalEvidence(contractFilename, registryFilename, evidenceFilename, evidenceCommitSHA string) (Contract, []byte, error) {
	evidenceData, err := readLimitedFile(evidenceFilename, maxHistoricalEvidenceBytes)
	if err != nil {
		return Contract{}, nil, fmt.Errorf("read historical release evidence: %w", err)
	}
	var envelope struct {
		SchemaID       string `json:"schema_id"`
		SchemaVersion  int    `json:"schema_version"`
		Repository     string `json:"repository"`
		ReleaseVersion string `json:"release_version"`
		SourceSHA      string `json:"source_sha"`
		EvidenceSHA256 string `json:"evidence_sha256"`
	}
	if err := decodeKnownJSON(evidenceData, &envelope); err != nil {
		return Contract{}, nil, fmt.Errorf("decode historical release evidence identity: %w", err)
	}
	identity, err := findHistoricalIdentity(registryFilename, envelope.Repository, envelope.ReleaseVersion, envelope.SourceSHA)
	if err != nil {
		return Contract{}, nil, err
	}
	fileDigest := sha256.Sum256(evidenceData)
	if identity.ContractGeneration != "v1" || identity.EvidenceFormat != "v1" ||
		identity.EvidenceCommitSHA != evidenceCommitSHA ||
		identity.EvidenceRootPath != fmt.Sprintf("evidence/releases/%s/release-evidence.json", envelope.ReleaseVersion) ||
		identity.EvidenceRootFileSHA256 != hex.EncodeToString(fileDigest[:]) ||
		identity.EvidenceRootSemanticSHA256 != envelope.EvidenceSHA256 ||
		identity.EvidenceRootSchemaID != envelope.SchemaID || identity.EvidenceRootSchemaVersion != envelope.SchemaVersion {
		return Contract{}, nil, errors.New("historical v1 evidence root does not match its registered release tuple")
	}
	contract, err := loadHistoricalContract(contractFilename, registryFilename, identity)
	if err != nil {
		return Contract{}, nil, err
	}
	if err := contract.ValidateHistoricalEvidenceIdentity(identity.Repository, identity.ReleaseVersion, identity.SourceSHA, identity.EvidenceRootSemanticSHA256); err != nil {
		return Contract{}, nil, err
	}
	return contract, evidenceData, nil
}

// LoadHistoricalBundleContract authorizes a compact bundle only when every
// identity observable offline matches a closed registry entry. It returns the
// immutable archival v1 contract required to replay the reconstructed legacy
// evidence semantics.
func LoadHistoricalBundleContract(contractFilename, registryFilename string, observed HistoricalBundleObservation) (Contract, HistoricalIdentity, error) {
	identity, err := findHistoricalIdentity(registryFilename, observed.Repository, observed.ReleaseVersion, observed.SourceSHA)
	if err != nil {
		return Contract{}, HistoricalIdentity{}, err
	}
	if identity.ContractGeneration != "v1" || identity.EvidenceFormat != "v2" ||
		identity.EvidenceCommitSHA != observed.EvidenceCommitSHA ||
		identity.EvidenceRootPath != fmt.Sprintf("evidence/releases/%s/release-evidence-bundle.json", observed.ReleaseVersion) ||
		identity.EvidenceRootFileSHA256 != observed.EvidenceRootFileSHA256 ||
		identity.EvidenceRootSemanticSHA256 != observed.EvidenceRootSemanticSHA256 ||
		identity.EvidenceRootSchemaID != observed.EvidenceRootSchemaID ||
		identity.EvidenceRootSchemaVersion != observed.EvidenceRootSchemaVersion ||
		identity.ReconstructedLegacyEvidenceSHA256 != observed.ReconstructedLegacyEvidenceSHA256 ||
		identity.ReconstructedLegacyCanonicalJSONSHA256 != observed.ReconstructedLegacyCanonicalJSONSHA256 ||
		identity.ReconstructedLegacyCanonicalJSONSize != observed.ReconstructedLegacyCanonicalJSONSize ||
		identity.EvidenceParentCommitSHA != observed.EvidenceParentCommitSHA ||
		identity.PublisherRunID != observed.PublisherRunID || identity.PublisherRunAttempt != observed.PublisherRunAttempt ||
		identity.EvidenceRunID != observed.EvidenceRunID || identity.EvidenceRunAttempt != observed.EvidenceRunAttempt ||
		identity.CompactArtifactID != observed.CompactArtifactID || identity.CompactArtifactDigest != observed.CompactArtifactDigest {
		return Contract{}, HistoricalIdentity{}, errors.New("historical compact evidence does not match its registered release tuple")
	}
	contract, err := loadHistoricalContract(contractFilename, registryFilename, identity)
	if err != nil {
		return Contract{}, HistoricalIdentity{}, err
	}
	return contract, identity, nil
}

// ValidateHistoricalBundleReconstruction closes the last offline boundary
// after bundle verification has reconstructed canonical legacy evidence.
func ValidateHistoricalBundleReconstruction(identity HistoricalIdentity, evidenceSHA256, canonicalJSONSHA256 string, canonicalJSONSize int64) error {
	if identity.EvidenceFormat != "v2" || identity.ReconstructedLegacyEvidenceSHA256 != evidenceSHA256 ||
		identity.ReconstructedLegacyCanonicalJSONSHA256 != canonicalJSONSHA256 ||
		identity.ReconstructedLegacyCanonicalJSONSize != canonicalJSONSize {
		return errors.New("historical compact evidence reconstruction differs from the registry")
	}
	return nil
}

// AuthorizeHistoricalSource resolves an exact closed registry entry and also
// verifies the supplied archival contract bytes and semantic digest. It is the
// only supported bridge for default-branch listeners that must safely inspect
// a registered pre-v2 source before final full evidence replay.
func AuthorizeHistoricalSource(contractFilename, registryFilename, repository, version, sourceSHA string) (HistoricalSourceAuthorization, error) {
	identity, err := findHistoricalIdentity(registryFilename, repository, version, sourceSHA)
	if err != nil {
		return HistoricalSourceAuthorization{}, err
	}
	if _, err := loadHistoricalContract(contractFilename, registryFilename, identity); err != nil {
		return HistoricalSourceAuthorization{}, err
	}
	return HistoricalSourceAuthorization{SchemaID: HistoricalSourceSchemaID, SchemaVersion: 1, OK: true, Identity: identity}, nil
}

func findHistoricalIdentity(registryFilename, repository, version, sourceSHA string) (HistoricalIdentity, error) {
	registryData, err := readLimitedFile(registryFilename, maxContractBytes)
	if err != nil {
		return HistoricalIdentity{}, fmt.Errorf("read historical contract registry: %w", err)
	}
	var registry historicalRegistry
	if err := decodeJSON(registryData, &registry, true); err != nil {
		return HistoricalIdentity{}, fmt.Errorf("decode historical contract registry: %w", err)
	}
	if err := validateHistoricalRegistry(registry); err != nil {
		return HistoricalIdentity{}, err
	}
	for _, identity := range registry.Entries {
		if identity.Repository == repository && identity.ReleaseVersion == version && identity.SourceSHA == sourceSHA {
			return identity, nil
		}
	}
	return HistoricalIdentity{}, errors.New("historical source tuple is not registered")
}

// loadHistoricalContract performs an explicit, release-bounded archival load.
// The registry, v1 bytes, semantic digest, and full evidence tuple must all
// match before a compatibility-bound Contract is returned.
func loadHistoricalContract(contractFilename, registryFilename string, identity HistoricalIdentity) (Contract, error) {
	registryData, err := readLimitedFile(registryFilename, maxContractBytes)
	if err != nil {
		return Contract{}, fmt.Errorf("read historical contract registry: %w", err)
	}
	var registry historicalRegistry
	if err := decodeJSON(registryData, &registry, true); err != nil {
		return Contract{}, fmt.Errorf("decode historical contract registry: %w", err)
	}
	if err := validateHistoricalRegistry(registry); err != nil {
		return Contract{}, err
	}
	found := false
	for _, entry := range registry.Entries {
		if reflect.DeepEqual(entry, identity) {
			found = true
			break
		}
	}
	if !found {
		return Contract{}, errors.New("historical release/evidence tuple is not registered")
	}

	contractData, err := readLimitedFile(contractFilename, maxContractBytes)
	if err != nil {
		return Contract{}, fmt.Errorf("read historical release contract: %w", err)
	}
	fileDigest := sha256.Sum256(contractData)
	if hex.EncodeToString(fileDigest[:]) != registry.Contract.FileSHA256 {
		return Contract{}, errors.New("historical release contract file digest differs from the registry")
	}
	var legacy legacyContractV1
	if err := decodeJSON(contractData, &legacy, true); err != nil {
		return Contract{}, fmt.Errorf("decode historical release contract: %w", err)
	}
	if err := validateReleasePleaseRecoveryEncoding(contractData, legacy.VersionPolicy.ReleasePleaseRecovery); err != nil {
		return Contract{}, fmt.Errorf("decode historical release contract: %w", err)
	}
	contract, err := convertLegacyContract(legacy, identity)
	if err != nil {
		return Contract{}, err
	}
	semantic, err := SemanticSHA256(contract)
	if err != nil {
		return Contract{}, err
	}
	if semantic != registry.Contract.SemanticSHA256 {
		return Contract{}, errors.New("historical release contract semantic digest differs from the registry")
	}
	contract.fileSHA256 = hex.EncodeToString(fileDigest[:])
	return contract, nil
}

func loadHistoricalCanonical(repositoryRoot string, identity HistoricalIdentity) (Contract, error) {
	return loadHistoricalContract(
		repositoryRoot+"/"+LegacyArchivePath,
		repositoryRoot+"/"+HistoricalRegistryPath,
		identity,
	)
}

// ValidateHistoricalEvidenceIdentity binds an archival contract to exactly one
// registered release document. Operational v2 contracts have no such binding.
func (c Contract) ValidateHistoricalEvidenceIdentity(repository, version, sourceSHA, evidenceSHA256 string) error {
	if c.SchemaID == SchemaID && c.SchemaVersion == SchemaVersion {
		return nil
	}
	if c.historicalIdentity == nil {
		return errors.New("historical contract has no compatibility binding")
	}
	want := c.historicalIdentity
	expectedEvidenceSHA256 := want.EvidenceRootSemanticSHA256
	if want.EvidenceFormat == "v2" {
		expectedEvidenceSHA256 = want.ReconstructedLegacyEvidenceSHA256
	}
	if repository != want.Repository || version != want.ReleaseVersion || sourceSHA != want.SourceSHA || evidenceSHA256 != expectedEvidenceSHA256 {
		return errors.New("historical evidence does not match its registered release/source/digest tuple")
	}
	return nil
}

func validateHistoricalRegistry(registry historicalRegistry) error {
	wantContract := historicalContractBinding{
		Path: LegacyArchivePath, SchemaID: LegacySchemaID, SchemaVersion: LegacySchemaVersion,
		FileSHA256: LegacyCanonicalFileSHA256, SemanticSHA256: LegacySemanticSHA256,
	}
	wantEntries := []HistoricalIdentity{
		{
			Repository: "ildarbinanas-design/env-vault", ReleaseVersion: "v0.0.14",
			SourceSHA: "c42a92144a82c19edea41c76328ec7fd1e408ceb", ContractGeneration: "v1", EvidenceFormat: "v1",
			EvidenceCommitSHA:          "68547bd880a4d49f44389476b77046aac2ab1675",
			EvidenceParentCommitSHA:    "c42a92144a82c19edea41c76328ec7fd1e408ceb",
			EvidenceRootPath:           "evidence/releases/v0.0.14/release-evidence.json",
			EvidenceRootFileSHA256:     "b6d56fc3675c2c4fc441a390249ac868a4453af77f7a0b8b06df8b75f1604d79",
			EvidenceRootSemanticSHA256: "6a4d45205a5a662cfb21beee5726a67473a42dd273763c0662299343c3e85076",
			EvidenceRootSchemaID:       "env-vault.release-evidence.v1", EvidenceRootSchemaVersion: 1,
			PublisherRunID: 29569706872, PublisherRunAttempt: 1, EvidenceRunID: 29569819553, EvidenceRunAttempt: 2,
		},
		{
			Repository: "ildarbinanas-design/env-vault", ReleaseVersion: "v0.0.15",
			SourceSHA: "c7dd1fd6176ac2abbea22f226795a0787e774c1b", ContractGeneration: "v1", EvidenceFormat: "v1",
			EvidenceCommitSHA:          "af521d52b898088cb49f6256964e377e33e95a5d",
			EvidenceParentCommitSHA:    "68547bd880a4d49f44389476b77046aac2ab1675",
			EvidenceRootPath:           "evidence/releases/v0.0.15/release-evidence.json",
			EvidenceRootFileSHA256:     "679a20101ca92f786d7417b984755305728a36fcafcc9d68bbe1540c92ab7026",
			EvidenceRootSemanticSHA256: "2c339829ad1ea77c4f8e91dc8cfb896d43978e281ee76b2e82022fe0c65fc63e",
			EvidenceRootSchemaID:       "env-vault.release-evidence.v1", EvidenceRootSchemaVersion: 1,
			PublisherRunID: 29576465336, PublisherRunAttempt: 1, EvidenceRunID: 29576963736, EvidenceRunAttempt: 1,
		},
		{
			Repository: "ildarbinanas-design/env-vault", ReleaseVersion: "v0.0.16",
			SourceSHA: "ddfd38c3144ed3d0968d2c5e7e4b2acfef841478", ContractGeneration: "v1", EvidenceFormat: "v2",
			EvidenceCommitSHA:                      "e697239298c4b5b1240fc53abe611131d45ac7c0",
			EvidenceParentCommitSHA:                "af521d52b898088cb49f6256964e377e33e95a5d",
			EvidenceRootPath:                       "evidence/releases/v0.0.16/release-evidence-bundle.json",
			EvidenceRootFileSHA256:                 "e6d1014c4c1a977367446827f8764d645fab3015f77e77a843fd4485a4ecd644",
			EvidenceRootSemanticSHA256:             "1cc44109f18d9f6cba0da60e3368afaa186cd5a47d03dcf6b06b7f94f311d003",
			EvidenceRootSchemaID:                   "env-vault.release-evidence-bundle.v2",
			EvidenceRootSchemaVersion:              2,
			ReconstructedLegacyEvidenceSHA256:      "f0e8ab2a0e706192f7ddcffb3d5124bda51d85737f43535763e094b00b96a29f",
			ReconstructedLegacyCanonicalJSONSHA256: "344568ad80a41c6ef97612d604225ce044fe5a48859e0cfd8ab3e89e019d9d70",
			ReconstructedLegacyCanonicalJSONSize:   1475935,
			PublisherRunID:                         29622574820, PublisherRunAttempt: 1, EvidenceRunID: 29622650408, EvidenceRunAttempt: 1,
			CompactArtifactID: 8422728320, CompactArtifactDigest: "sha256:8732f0365a4564c3d063b5a2ae1909c14996dca007a1321b0c66304190030eea",
		},
	}
	if registry.SchemaID != HistoricalRegistrySchemaID || registry.SchemaVersion != HistoricalRegistryVersion {
		return errors.New("historical contract registry schema is unsupported")
	}
	if !reflect.DeepEqual(registry.Contract, wantContract) {
		return errors.New("historical contract registry does not pin the immutable v1 identity")
	}
	if !reflect.DeepEqual(registry.Entries, wantEntries) {
		return errors.New("historical contract registry entries are not the closed canonical release set")
	}
	return nil
}

func convertLegacyContract(legacy legacyContractV1, identity HistoricalIdentity) (Contract, error) {
	contract := Contract{
		SchemaID: legacy.SchemaID, SchemaVersion: legacy.SchemaVersion,
		VersionPolicy: VersionPolicy{
			Pattern:               legacy.VersionPolicy.Pattern,
			ReleasePleaseRecovery: legacy.VersionPolicy.ReleasePleaseRecovery,
			BlockedVersions:       legacy.VersionPolicy.BlockedVersions,
			LegacyRebuild:         legacy.VersionPolicy.LegacyRebuild,
		},
		Naming: legacy.Naming, Platforms: legacy.Platforms, Assets: legacy.Assets,
		MainRequiredChecks: legacy.MainRequiredChecks, ReleaseStages: legacy.ReleaseStages,
		AllowedRepairActions: legacy.AllowedRepairActions, ActionCodes: legacy.ActionCodes,
		ReasonCodes: legacy.ReasonCodes, ErrorCodes: legacy.ErrorCodes, Schemas: legacy.Schemas,
		historicalIdentity: &identity,
	}
	for _, workflow := range legacy.Workflows {
		contract.Workflows = append(contract.Workflows, Workflow{ID: workflow.ID, Name: workflow.Name, File: workflow.File})
	}
	for _, app := range legacy.Apps {
		repositoryID := ""
		switch app.Repository {
		case "ildarbinanas-design/env-vault":
			repositoryID = "source"
		case "ildarbinanas-design/homebrew-tap":
			repositoryID = "homebrew_tap"
		default:
			return Contract{}, fmt.Errorf("historical app %q has an unregistered repository", app.ID)
		}
		contract.Apps = append(contract.Apps, App{
			ID: app.ID, Slug: app.Slug, RepositoryID: repositoryID, Environment: app.Environment,
			AuditWorkflow: app.AuditWorkflow, CIWorkflowFile: app.CIWorkflowFile, CIWorkflowName: app.CIWorkflowName,
		})
	}
	return contract, nil
}

func projectLegacyContract(contract Contract) legacyContractV1 {
	legacy := legacyContractV1{
		SchemaID: contract.SchemaID, SchemaVersion: contract.SchemaVersion,
		VersionPolicy: legacyVersionPolicy{
			Pattern:               contract.VersionPolicy.Pattern,
			ReleasePleaseRecovery: contract.VersionPolicy.ReleasePleaseRecovery,
			BlockedVersions:       contract.VersionPolicy.BlockedVersions,
			LegacyRebuild:         contract.VersionPolicy.LegacyRebuild,
		},
		Naming: contract.Naming, Platforms: contract.Platforms, Assets: contract.Assets,
		MainRequiredChecks: contract.MainRequiredChecks, ReleaseStages: contract.ReleaseStages,
		AllowedRepairActions: contract.AllowedRepairActions, ActionCodes: contract.ActionCodes,
		ReasonCodes: contract.ReasonCodes, ErrorCodes: contract.ErrorCodes, Schemas: contract.Schemas,
	}
	for _, workflow := range contract.Workflows {
		legacy.Workflows = append(legacy.Workflows, legacyWorkflow{ID: workflow.ID, Name: workflow.Name, File: workflow.File})
	}
	for _, app := range contract.Apps {
		repository := map[string]string{
			"source":       "ildarbinanas-design/env-vault",
			"homebrew_tap": "ildarbinanas-design/homebrew-tap",
		}[app.RepositoryID]
		legacy.Apps = append(legacy.Apps, legacyApp{
			ID: app.ID, Slug: app.Slug, Repository: repository, Environment: app.Environment,
			AuditWorkflow: app.AuditWorkflow, CIWorkflowFile: app.CIWorkflowFile, CIWorkflowName: app.CIWorkflowName,
		})
	}
	return legacy
}
