package releasecontract

import (
	"errors"
	"fmt"
)

// SourceRoute is the current control plane's fail-closed decision for a
// workflow_run source checkout. The source contract decides operational
// values; the current checker decides which contract generations are trusted.
type SourceRoute struct {
	SchemaID               string                 `json:"schema_id"`
	SchemaVersion          int                    `json:"schema_version"`
	OK                     bool                   `json:"ok"`
	ContractGeneration     string                 `json:"contract_generation"`
	EvidenceFormat         string                 `json:"evidence_format"`
	ContractSchemaID       string                 `json:"contract_schema_id"`
	ContractSchemaVersion  int                    `json:"contract_schema_version"`
	ContractSemanticSHA256 string                 `json:"contract_semantic_sha256"`
	ContractFileSHA256     string                 `json:"contract_file_sha256"`
	Repository             Repository             `json:"repository"`
	ReleaseAppSlug         string                 `json:"release_app_slug"`
	Naming                 Naming                 `json:"naming"`
	Operational            *OperationalProjection `json:"operational,omitempty"`
	Historical             *HistoricalIdentity    `json:"historical,omitempty"`
}

// RouteSourceContract accepts current v2 sources and only the closed v1
// release tuples registered by the current control plane. Contract generation
// is selected by exact schema identity, never by file existence or fallback.
func RouteSourceContract(sourceContractFilename, registryFilename, repository, version, sourceSHA string) (SourceRoute, error) {
	if !validRepository(repository) || !isSHA(sourceSHA) || (version != "" && !IsVersion(version)) {
		return SourceRoute{}, errors.New("source repository, version, or SHA is malformed")
	}
	data, err := readLimitedFile(sourceContractFilename, maxContractBytes)
	if err != nil {
		return SourceRoute{}, fmt.Errorf("read source release contract: %w", err)
	}
	var envelope struct {
		SchemaID      string `json:"schema_id"`
		SchemaVersion int    `json:"schema_version"`
	}
	if err := decodeKnownJSON(data, &envelope); err != nil {
		return SourceRoute{}, fmt.Errorf("decode source release contract identity: %w", err)
	}

	var contract Contract
	var contractGeneration string
	var evidenceFormat string
	var operational *OperationalProjection
	var historical *HistoricalIdentity
	switch {
	case envelope.SchemaID == SchemaID && envelope.SchemaVersion == SchemaVersion:
		contract, err = LoadFile(sourceContractFilename)
		if err != nil {
			return SourceRoute{}, err
		}
		projection, projectionErr := contract.OperationalProjection()
		if projectionErr != nil {
			return SourceRoute{}, projectionErr
		}
		contractGeneration, evidenceFormat, operational = "v2", "v2", &projection
	case envelope.SchemaID == LegacySchemaID && envelope.SchemaVersion == LegacySchemaVersion:
		if !IsVersion(version) {
			return SourceRoute{}, errors.New("historical v1 source routing requires an exact release version")
		}
		authorization, authorizationErr := AuthorizeHistoricalSource(sourceContractFilename, registryFilename, repository, version, sourceSHA)
		if authorizationErr != nil {
			return SourceRoute{}, authorizationErr
		}
		contract, err = loadHistoricalContract(sourceContractFilename, registryFilename, authorization.Identity)
		if err != nil {
			return SourceRoute{}, err
		}
		identity := authorization.Identity
		contractGeneration, evidenceFormat, historical = "v1", identity.EvidenceFormat, &identity
	default:
		return SourceRoute{}, errors.New("source release contract schema is unsupported")
	}

	sourceRepository, ok := contract.RepositoryByID("source")
	if !ok || sourceRepository.FullName != repository {
		return SourceRoute{}, errors.New("source contract repository differs from the workflow repository")
	}
	releaseApp, ok := contract.AppByID("release_planning")
	if !ok || releaseApp.RepositoryID != "source" {
		return SourceRoute{}, errors.New("source contract release-planning App binding is invalid")
	}
	digest, err := SemanticSHA256(contract)
	if err != nil {
		return SourceRoute{}, err
	}
	return SourceRoute{
		SchemaID: SourceRouteSchemaID, SchemaVersion: SourceRouteSchemaVersion, OK: true,
		ContractGeneration: contractGeneration, EvidenceFormat: evidenceFormat,
		ContractSchemaID: contract.SchemaID, ContractSchemaVersion: contract.SchemaVersion,
		ContractSemanticSHA256: digest, Repository: sourceRepository,
		ContractFileSHA256: contract.FileSHA256(),
		ReleaseAppSlug:     releaseApp.Slug, Naming: contract.Naming,
		Operational: operational, Historical: historical,
	}, nil
}
