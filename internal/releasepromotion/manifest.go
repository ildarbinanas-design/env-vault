package releasepromotion

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/e2ebaseline"
	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

type AssembleOptions struct {
	ContractPath       string
	SourceSHA          string
	ReleaseVersion     string
	Repository         string
	RunID              int64
	RunAttempt         int
	SourceQualityPath  string
	MatrixProofPath    string
	PlatformProofPaths []string
	CreatedAt          time.Time
}

type VerifyOptions struct {
	ContractPath   string
	SourceSHA      string
	ReleaseVersion string
	Repository     string
	RunID          int64
	RunAttempt     int
	ArtifactsRoot  string
}

func SealSourceQualityProof(proof *SourceQualityProof, contract releasecontract.Contract) error {
	if proof == nil {
		return coded(CodePromotionManifestInvalid, "source-quality proof is nil", nil)
	}
	if proof.ObservedJobs.SourceQuality != "success" || proof.ObservedJobs.LicenseMatrix != "success" {
		return coded(CodePromotionManifestInvalid, "source-quality observed jobs did not both conclude success", nil)
	}
	proof.Results = passingSourceQualityResults()
	proof.Result = "pass"
	proof.ProofSHA256 = ""
	if err := validateSourceQualityBody(*proof, contract); err != nil {
		return err
	}
	digest, err := SourceQualityProofSHA256(*proof)
	if err != nil {
		return coded(CodePromotionManifestInvalid, "seal source-quality proof", err)
	}
	proof.ProofSHA256 = digest
	return nil
}

func SourceQualityProofSHA256(proof SourceQualityProof) (string, error) {
	proof.ProofSHA256 = ""
	data, err := json.Marshal(proof)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func ValidateSourceQualityProof(proof SourceQualityProof, contract releasecontract.Contract) error {
	if err := validateSourceQualityBody(proof, contract); err != nil {
		return err
	}
	digest, err := SourceQualityProofSHA256(proof)
	if err != nil {
		return coded(CodePromotionManifestInvalid, "hash source-quality proof", err)
	}
	if proof.ProofSHA256 != digest {
		return coded(CodeDigestMismatch, "source-quality proof self-digest mismatch", nil)
	}
	return nil
}

func validateSourceQualityBody(proof SourceQualityProof, contract releasecontract.Contract) error {
	if proof.SchemaID != contract.Schemas["source_quality_proof"] || proof.SchemaVersion != SchemaVersion || proof.Result != "pass" {
		return coded(CodePromotionManifestInvalid, "source-quality proof schema or result is invalid", nil)
	}
	if err := validateTuple(proof.SourceSHA, proof.ReleaseVersion, proof.Repository, proof.Workflow.RunID, proof.Workflow.RunAttempt); err != nil {
		return err
	}
	workflow, ok := contract.WorkflowByID("ci")
	if !ok || proof.Workflow.ID != workflow.ID || proof.Workflow.Name != workflow.Name || proof.Workflow.File != workflow.File || proof.Workflow.Event != "push" || proof.Workflow.HeadSHA != proof.SourceSHA {
		return coded(CodeSourceMismatch, "source-quality workflow does not bind the exact push CI source", nil)
	}
	results := proof.Results
	if proof.ObservedJobs.SourceQuality != "success" || proof.ObservedJobs.LicenseMatrix != "success" ||
		results.Module != "pass" || results.Test != "pass" || results.Vet != "pass" || results.Smoke != "pass" || results.Race != "pass" || len(results.Licenses) != 3 || results.Licenses["linux"] != "pass" || results.Licenses["darwin"] != "pass" || results.Licenses["windows"] != "pass" {
		return coded(CodePromotionManifestInvalid, "source-quality results are incomplete", nil)
	}
	return nil
}

func passingSourceQualityResults() SourceQualityResults {
	return SourceQualityResults{
		Module: "pass", Test: "pass", Vet: "pass", Smoke: "pass", Race: "pass",
		Licenses: map[string]string{"linux": "pass", "darwin": "pass", "windows": "pass"},
	}
}

func Assemble(options AssembleOptions) (Manifest, error) {
	contract, contractFile, contractSemantic, err := loadContract(options.ContractPath)
	if err != nil {
		return Manifest{}, err
	}
	if err := validateTuple(options.SourceSHA, options.ReleaseVersion, options.Repository, options.RunID, options.RunAttempt); err != nil {
		return Manifest{}, err
	}
	if len(options.PlatformProofPaths) != len(contract.Platforms) {
		return Manifest{}, coded(CodePromotionManifestInvalid, fmt.Sprintf("platform proof count=%d, want %d", len(options.PlatformProofPaths), len(contract.Platforms)), nil)
	}
	createdAt := options.CreatedAt.UTC()
	if options.CreatedAt.IsZero() {
		return Manifest{}, coded(CodePromotionManifestInvalid, "created_at is required", nil)
	}

	sourceQuality, sourceQualityFile, err := ReadSourceQualityProof(options.SourceQualityPath)
	if err != nil {
		return Manifest{}, err
	}
	if err := validateCanonicalProofFile(sourceQualityFile, sourceQuality, "source-quality"); err != nil {
		return Manifest{}, err
	}
	if err := ValidateSourceQualityProof(sourceQuality, contract); err != nil {
		return Manifest{}, err
	}
	if err := requireExactTuple(sourceQuality.SourceSHA, sourceQuality.ReleaseVersion, sourceQuality.Repository, sourceQuality.Workflow.RunID, sourceQuality.Workflow.RunAttempt, options.SourceSHA, options.ReleaseVersion, options.Repository, options.RunID, options.RunAttempt); err != nil {
		return Manifest{}, err
	}
	matrix, matrixFile, err := loadMatrixProof(options.MatrixProofPath, contract)
	if err != nil {
		return Manifest{}, err
	}
	if err := validateCanonicalProofFile(matrixFile, matrix, "E2E matrix"); err != nil {
		return Manifest{}, err
	}
	if err := requireExactMatrixTuple(matrix, options.SourceSHA, options.Repository, options.RunID, options.RunAttempt); err != nil {
		return Manifest{}, err
	}

	byPlatform := make(map[string]PlatformProof, len(options.PlatformProofPaths))
	for _, filename := range options.PlatformProofPaths {
		proof, _, err := ReadPlatformProof(filename)
		if err != nil {
			return Manifest{}, err
		}
		if err := ValidatePlatformProof(proof, contract); err != nil {
			return Manifest{}, err
		}
		if err := requireExactTuple(proof.SourceSHA, proof.ReleaseVersion, proof.Repository, proof.RunID, proof.RunAttempt, options.SourceSHA, options.ReleaseVersion, options.Repository, options.RunID, options.RunAttempt); err != nil {
			return Manifest{}, err
		}
		if _, duplicate := byPlatform[proof.PlatformID]; duplicate {
			return Manifest{}, coded(CodePromotionManifestInvalid, "duplicate platform proof "+proof.PlatformID, nil)
		}
		byPlatform[proof.PlatformID] = proof
	}

	ordered := make([]PlatformProof, 0, len(contract.Platforms))
	assetByName := make(map[string]FileDigest, len(contract.Assets))
	for _, platform := range contract.Platforms {
		proof, ok := byPlatform[platform.ID]
		if !ok {
			return Manifest{}, coded(CodePromotionManifestInvalid, "missing platform proof "+platform.ID, nil)
		}
		ordered = append(ordered, proof)
		for _, asset := range []FileDigest{proof.Archive, proof.Checksum} {
			if _, duplicate := assetByName[asset.Name]; duplicate {
				return Manifest{}, coded(CodeArtifactInventoryInvalid, "duplicate promotion asset "+asset.Name, nil)
			}
			assetByName[asset.Name] = asset
		}
	}
	assets := make([]FileDigest, 0, len(contract.Assets))
	for _, name := range contract.Assets {
		asset, ok := assetByName[name]
		if !ok {
			return Manifest{}, coded(CodeArtifactInventoryInvalid, "missing promotion asset "+name, nil)
		}
		assets = append(assets, asset)
	}
	if err := bindMatrixToNativeProofs(matrix, contract, ordered); err != nil {
		return Manifest{}, err
	}

	manifest := Manifest{
		SchemaID: contract.Schemas["promotion_manifest"], SchemaVersion: SchemaVersion,
		SourceSHA: options.SourceSHA, ReleaseVersion: options.ReleaseVersion, Repository: options.Repository,
		Workflow:       sourceQuality.Workflow,
		ContractSchema: contract.SchemaID, ContractSHA256: contractFile.SHA256,
		ContractSemanticSHA256: contractSemantic, SemanticSuiteHash: matrix.SuiteHash,
		SourceQuality: SourceQualityBinding{File: sourceQualityFile, Proof: sourceQuality},
		E2EMatrix:     MatrixProofBinding{File: matrixFile, Proof: matrix},
		Platforms:     ordered, Assets: assets, CreatedAt: createdAt.Format(time.RFC3339), Result: "pass",
	}
	manifest.ManifestSHA256, err = ManifestSHA256(manifest)
	if err != nil {
		return Manifest{}, coded(CodePromotionManifestInvalid, "seal promotion manifest", err)
	}
	return manifest, nil
}

func ManifestSHA256(manifest Manifest) (string, error) {
	manifest.ManifestSHA256 = ""
	data, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func VerifyFile(manifestPath string, options VerifyOptions) error {
	manifest, err := ReadManifest(manifestPath)
	if err != nil {
		return err
	}
	return Verify(manifest, options)
}

func Verify(manifest Manifest, options VerifyOptions) error {
	contract, contractFile, contractSemantic, err := loadContract(options.ContractPath)
	if err != nil {
		return err
	}
	if err := validateTuple(options.SourceSHA, options.ReleaseVersion, options.Repository, options.RunID, options.RunAttempt); err != nil {
		return err
	}
	if manifest.SchemaID != contract.Schemas["promotion_manifest"] || manifest.SchemaVersion != SchemaVersion || manifest.Result != "pass" {
		return coded(CodePromotionManifestInvalid, "promotion manifest schema or result is invalid", nil)
	}
	if err := requireExactTuple(manifest.SourceSHA, manifest.ReleaseVersion, manifest.Repository, manifest.Workflow.RunID, manifest.Workflow.RunAttempt, options.SourceSHA, options.ReleaseVersion, options.Repository, options.RunID, options.RunAttempt); err != nil {
		return err
	}
	workflow, ok := contract.WorkflowByID("ci")
	if !ok || manifest.Workflow.ID != workflow.ID || manifest.Workflow.Name != workflow.Name || manifest.Workflow.File != workflow.File || manifest.Workflow.Event != "push" || manifest.Workflow.HeadSHA != manifest.SourceSHA {
		return coded(CodeSourceMismatch, "promotion workflow identity is invalid", nil)
	}
	if manifest.ContractSchema != contract.SchemaID || manifest.ContractSHA256 != contractFile.SHA256 || manifest.ContractSemanticSHA256 != contractSemantic {
		return coded(CodeDigestMismatch, "promotion release-contract binding is invalid", nil)
	}
	createdAt, err := time.Parse(time.RFC3339, manifest.CreatedAt)
	if err != nil || createdAt.Location() != time.UTC || createdAt.Format(time.RFC3339) != manifest.CreatedAt {
		return coded(CodePromotionManifestInvalid, "created_at is not canonical UTC RFC3339", err)
	}
	if !validFileDigest(manifest.SourceQuality.File) {
		return coded(CodeDigestMismatch, "source-quality file binding is invalid", nil)
	}
	if err := ValidateSourceQualityProof(manifest.SourceQuality.Proof, contract); err != nil {
		return err
	}
	proof := manifest.SourceQuality.Proof
	if err := requireExactTuple(proof.SourceSHA, proof.ReleaseVersion, proof.Repository, proof.Workflow.RunID, proof.Workflow.RunAttempt, manifest.SourceSHA, manifest.ReleaseVersion, manifest.Repository, manifest.Workflow.RunID, manifest.Workflow.RunAttempt); err != nil {
		return err
	}
	if !validFileDigest(manifest.E2EMatrix.File) {
		return coded(CodeDigestMismatch, "E2E matrix file binding is invalid", nil)
	}
	if err := manifest.E2EMatrix.Proof.Validate(contract); err != nil {
		return coded(CodePromotionManifestInvalid, "E2E matrix proof is invalid", err)
	}
	if err := requireExactMatrixTuple(manifest.E2EMatrix.Proof, manifest.SourceSHA, manifest.Repository, manifest.Workflow.RunID, manifest.Workflow.RunAttempt); err != nil {
		return err
	}
	if manifest.SemanticSuiteHash != manifest.E2EMatrix.Proof.SuiteHash {
		return coded(CodePromotionManifestInvalid, "matrix semantic suite hash differs from manifest", nil)
	}
	if err := ValidateEmbeddedProofFiles(manifest); err != nil {
		return err
	}

	if len(manifest.Platforms) != len(contract.Platforms) || len(manifest.Assets) != len(contract.Assets) {
		return coded(CodePromotionManifestInvalid, "platform or asset matrix is incomplete", nil)
	}
	assetByName := make(map[string]FileDigest, len(manifest.Assets))
	for index, platform := range contract.Platforms {
		platformProof := manifest.Platforms[index]
		if platformProof.PlatformID != platform.ID {
			return coded(CodePromotionManifestInvalid, "platform proof order differs from the release contract", nil)
		}
		if err := ValidatePlatformProof(platformProof, contract); err != nil {
			return err
		}
		if err := requireExactTuple(platformProof.SourceSHA, platformProof.ReleaseVersion, platformProof.Repository, platformProof.RunID, platformProof.RunAttempt, manifest.SourceSHA, manifest.ReleaseVersion, manifest.Repository, manifest.Workflow.RunID, manifest.Workflow.RunAttempt); err != nil {
			return err
		}
		for _, asset := range []FileDigest{platformProof.Archive, platformProof.Checksum} {
			if _, duplicate := assetByName[asset.Name]; duplicate {
				return coded(CodeArtifactInventoryInvalid, "duplicate asset in platform proofs", nil)
			}
			assetByName[asset.Name] = asset
		}
	}
	seenAssets := make(map[string]bool, len(manifest.Assets))
	for index, expectedName := range contract.Assets {
		asset := manifest.Assets[index]
		if asset.Name != expectedName || !validFileDigest(asset) || seenAssets[asset.Name] || assetByName[asset.Name] != asset {
			return coded(CodeArtifactInventoryInvalid, "manifest asset matrix is reordered, duplicated, or inconsistent", nil)
		}
		seenAssets[asset.Name] = true
	}
	if err := bindMatrixToNativeProofs(manifest.E2EMatrix.Proof, contract, manifest.Platforms); err != nil {
		return err
	}
	digest, err := ManifestSHA256(manifest)
	if err != nil {
		return coded(CodePromotionManifestInvalid, "hash promotion manifest", err)
	}
	if manifest.ManifestSHA256 != digest {
		return coded(CodeDigestMismatch, "promotion manifest self-digest mismatch", nil)
	}
	return verifyArtifactDirectory(options.ArtifactsRoot, contract, manifest.Assets)
}

// ValidateEmbeddedProofFiles reconstructs the canonical source proof and E2E
// matrix files from their embedded typed values and checks the exact file
// bindings recorded by the promotion manifest. This lets evidence replay prove
// those bindings without depending on retained workflow artifacts.
func ValidateEmbeddedProofFiles(manifest Manifest) error {
	if err := validateCanonicalProofFile(manifest.SourceQuality.File, manifest.SourceQuality.Proof, "source-quality"); err != nil {
		return err
	}
	return validateCanonicalProofFile(manifest.E2EMatrix.File, manifest.E2EMatrix.Proof, "E2E matrix")
}

func validateCanonicalProofFile(record FileDigest, value any, label string) error {
	if !validFileDigest(record) {
		return coded(CodeDigestMismatch, label+" file binding is invalid", nil)
	}
	data, err := MarshalJSON(value)
	if err != nil {
		return coded(CodePromotionManifestInvalid, "marshal embedded "+label+" proof", err)
	}
	digest := sha256.Sum256(data)
	want := FileDigest{Name: record.Name, Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:])}
	if record != want {
		return coded(CodeDigestMismatch, label+" file digest does not bind its canonical embedded proof", nil)
	}
	return nil
}

func loadMatrixProof(filename string, contract releasecontract.Contract) (e2ebaseline.MatrixProof, FileDigest, error) {
	_, before, err := readBoundedRegular(filename, maxProofBytes)
	if err != nil {
		return e2ebaseline.MatrixProof{}, FileDigest{}, coded(CodePromotionManifestInvalid, "read bounded E2E matrix proof", err)
	}
	proof, err := e2ebaseline.LoadMatrixProof(filename, contract)
	if err != nil {
		return e2ebaseline.MatrixProof{}, FileDigest{}, coded(CodePromotionManifestInvalid, "load E2E matrix proof", err)
	}
	_, after, err := readBoundedRegular(filename, maxProofBytes)
	if err != nil || after != before {
		return e2ebaseline.MatrixProof{}, FileDigest{}, coded(CodePromotionManifestInvalid, "E2E matrix proof changed while loading", err)
	}
	return proof, before, nil
}

func requireExactMatrixTuple(proof e2ebaseline.MatrixProof, source, repository string, runID int64, attempt int) error {
	if proof.Phase != "candidate" || proof.Run.CommitSHA != source || proof.Run.Repository != repository || proof.Run.RunID != strconv.FormatInt(runID, 10) || proof.Run.RunAttempt != strconv.Itoa(attempt) {
		return coded(CodeSourceMismatch, "E2E matrix differs from the exact source/run/attempt tuple", nil)
	}
	return nil
}

func bindMatrixToNativeProofs(matrix e2ebaseline.MatrixProof, contract releasecontract.Contract, proofs []PlatformProof) error {
	if len(matrix.PlatformEvidence) != len(contract.Platforms) || len(proofs) != len(contract.Platforms) {
		return coded(CodePromotionManifestInvalid, "E2E matrix or native proof set is incomplete", nil)
	}
	for index, platform := range contract.Platforms {
		e2e := matrix.PlatformEvidence[index]
		native := proofs[index]
		if e2e.ID != platform.ID || native.PlatformID != platform.ID ||
			e2e.Artifact.Archive != native.Archive.Name || e2e.Artifact.Checksum != native.Checksum.Name ||
			e2e.Artifact.SHA256 != native.Archive.SHA256 || e2e.BinarySHA256 != native.Binary.SHA256 {
			return coded(CodeDigestMismatch, "E2E matrix bytes differ from native proof for "+platform.ID, nil)
		}
	}
	return nil
}

func loadContract(filename string) (releasecontract.Contract, FileDigest, string, error) {
	_, before, err := readBoundedRegular(filename, maxContractBytes)
	if err != nil {
		return releasecontract.Contract{}, FileDigest{}, "", coded(CodePromotionManifestInvalid, "read bounded release contract", err)
	}
	contract, err := releasecontract.LoadFile(filename)
	if err != nil {
		return releasecontract.Contract{}, FileDigest{}, "", coded(CodePromotionManifestInvalid, "load release contract", err)
	}
	_, after, err := readBoundedRegular(filename, maxContractBytes)
	if err != nil || after != before {
		return releasecontract.Contract{}, FileDigest{}, "", coded(CodePromotionManifestInvalid, "release contract changed while loading", err)
	}
	semantic, err := releasecontract.SemanticSHA256(contract)
	if err != nil {
		return releasecontract.Contract{}, FileDigest{}, "", coded(CodePromotionManifestInvalid, "hash semantic release contract", err)
	}
	return contract, before, semantic, nil
}

func verifyArtifactDirectory(root string, contract releasecontract.Contract, expected []FileDigest) error {
	info, err := os.Lstat(root)
	if err != nil {
		return coded(CodeArtifactInventoryInvalid, "inspect artifact directory", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return coded(CodeArtifactInventoryInvalid, "artifact root is not a non-symlink directory", nil)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return coded(CodeArtifactInventoryInvalid, "read artifact directory", err)
	}
	if len(entries) != maxArtifactEntryCount || len(entries) != len(contract.Assets) {
		return coded(CodeArtifactInventoryInvalid, fmt.Sprintf("artifact entry count=%d, want %d", len(entries), len(contract.Assets)), nil)
	}
	expectedByName := make(map[string]FileDigest, len(expected))
	for _, asset := range expected {
		if _, duplicate := expectedByName[asset.Name]; duplicate {
			return coded(CodeArtifactInventoryInvalid, "duplicate expected artifact "+asset.Name, nil)
		}
		expectedByName[asset.Name] = asset
	}
	for _, entry := range entries {
		expectedFile, ok := expectedByName[entry.Name()]
		if !ok {
			return coded(CodeArtifactInventoryInvalid, "unexpected artifact "+entry.Name(), nil)
		}
		limit := int64(maxArchiveBytes)
		if strings.HasSuffix(entry.Name(), contract.Naming.ChecksumSuffix) {
			limit = maxChecksumBytes
		}
		actual, err := digestBoundedRegular(filepath.Join(root, entry.Name()), limit)
		if err != nil {
			return coded(CodeArtifactInventoryInvalid, "read artifact "+entry.Name(), err)
		}
		if actual != expectedFile {
			return coded(CodeDigestMismatch, "artifact bytes differ for "+entry.Name(), nil)
		}
	}
	for _, platform := range contract.Platforms {
		archive := expectedByName[platform.Archive]
		checksumPath := filepath.Join(root, platform.Checksum)
		checksumData, _, err := readBoundedRegular(checksumPath, maxChecksumBytes)
		if err != nil {
			return coded(CodeArtifactInventoryInvalid, "read checksum "+platform.Checksum, err)
		}
		if err := verifyChecksum(checksumData, archive); err != nil {
			return err
		}
	}
	return nil
}

func requireExactTuple(actualSource, actualVersion, actualRepository string, actualRunID int64, actualAttempt int, expectedSource, expectedVersion, expectedRepository string, expectedRunID int64, expectedAttempt int) error {
	if actualVersion != expectedVersion {
		return coded(CodeVersionMismatch, "release version differs from the expected tuple", nil)
	}
	if actualSource != expectedSource || actualRepository != expectedRepository || actualRunID != expectedRunID || actualAttempt != expectedAttempt {
		return coded(CodeSourceMismatch, "source, repository, run ID, or attempt differs from the expected tuple", nil)
	}
	return nil
}

// SortedErrorCodes is useful to deterministic callers that expose supported
// promotion failure codes in their own version document.
func SortedErrorCodes() []string {
	codes := []string{CodeArtifactInventoryInvalid, CodeDigestMismatch, CodePromotionManifestInvalid, CodeSourceMismatch, CodeVersionMismatch}
	sort.Strings(codes)
	return codes
}
