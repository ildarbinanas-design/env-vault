package releasepromotion

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

var (
	sourcePattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

// RecordOptions names only local inputs available on one native runner. Deep
// E2E report validation is deliberately performed once, when Assemble binds a
// sealed e2ebaseline.MatrixProof to the five native proofs.
type RecordOptions struct {
	ContractPath       string
	PlatformID         string
	SourceSHA          string
	ReleaseVersion     string
	Repository         string
	RunID              int64
	RunAttempt         int
	ArchivePath        string
	ChecksumPath       string
	BinaryPath         string
	VersionResultsPath string
}

// RecordPlatform proves exact local artifact bytes and all literal version
// surfaces on the native target that produced those bytes.
func RecordPlatform(options RecordOptions) (PlatformProof, error) {
	contract, _, _, err := loadContract(options.ContractPath)
	if err != nil {
		return PlatformProof{}, err
	}
	if err := validateTuple(options.SourceSHA, options.ReleaseVersion, options.Repository, options.RunID, options.RunAttempt); err != nil {
		return PlatformProof{}, err
	}
	platform, ok := contract.PlatformByID(options.PlatformID)
	if !ok {
		return PlatformProof{}, coded(CodePromotionManifestInvalid, "platform is not in the release contract", nil)
	}
	if filepath.Base(options.ArchivePath) != platform.Archive || filepath.Base(options.ChecksumPath) != platform.Checksum || filepath.Base(options.BinaryPath) != platform.Binary {
		return PlatformProof{}, coded(CodeArtifactInventoryInvalid, "archive, checksum, or binary name differs from the release contract", nil)
	}

	archive, err := digestBoundedRegular(options.ArchivePath, maxArchiveBytes)
	if err != nil {
		return PlatformProof{}, coded(CodeArtifactInventoryInvalid, "read release archive", err)
	}
	checksumData, checksum, err := readBoundedRegular(options.ChecksumPath, maxChecksumBytes)
	if err != nil {
		return PlatformProof{}, coded(CodeArtifactInventoryInvalid, "read checksum sidecar", err)
	}
	if err := verifyChecksum(checksumData, archive); err != nil {
		return PlatformProof{}, err
	}
	binary, err := digestBoundedRegular(options.BinaryPath, maxBinaryBytes)
	if err != nil {
		return PlatformProof{}, coded(CodeArtifactInventoryInvalid, "read native binary", err)
	}
	literal, err := ReadLiteralVersionEvidence(options.VersionResultsPath)
	if err != nil {
		return PlatformProof{}, err
	}
	if literal.Evidence.PlatformID != platform.ID || literal.Evidence.SourceSHA != options.SourceSHA ||
		literal.Evidence.ReleaseVersion != options.ReleaseVersion || literal.Evidence.Repository != options.Repository ||
		literal.Evidence.RunID != options.RunID || literal.Evidence.RunAttempt != options.RunAttempt ||
		literal.Evidence.Binary != binary {
		return PlatformProof{}, coded(CodeVersionMismatch, "literal version evidence is not bound to the exact platform tuple and binary", nil)
	}

	values := map[string]string{"platform": platform.ID, "attempt": strconv.Itoa(options.RunAttempt)}
	artifactName, err := contract.RenderName(contract.Naming.PlatformArtifactTemplate, values)
	if err != nil {
		return PlatformProof{}, coded(CodePromotionManifestInvalid, "render platform artifact name", err)
	}
	proofArtifactName, err := contract.RenderName(contract.Naming.PlatformEvidenceTemplate, values)
	if err != nil {
		return PlatformProof{}, coded(CodePromotionManifestInvalid, "render proof artifact name", err)
	}
	proof := PlatformProof{
		SchemaID:          contract.Schemas["promotion_platform"],
		SchemaVersion:     SchemaVersion,
		PlatformID:        platform.ID,
		GOOS:              platform.GOOS,
		GOARCH:            platform.GOARCH,
		SourceSHA:         options.SourceSHA,
		ReleaseVersion:    options.ReleaseVersion,
		Repository:        options.Repository,
		RunID:             options.RunID,
		RunAttempt:        options.RunAttempt,
		ArtifactName:      artifactName,
		ProofArtifactName: proofArtifactName,
		Archive:           archive,
		Checksum:          checksum,
		Binary:            binary,
		LiteralVersion:    literal,
		Result:            "pass",
	}
	proof.ProofSHA256, err = PlatformProofSHA256(proof)
	if err != nil {
		return PlatformProof{}, coded(CodePromotionManifestInvalid, "seal platform proof", err)
	}
	return proof, nil
}

func PlatformProofSHA256(proof PlatformProof) (string, error) {
	proof.ProofSHA256 = ""
	data, err := json.Marshal(proof)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func ValidatePlatformProof(proof PlatformProof, contract releasecontract.Contract) error {
	if proof.SchemaID != contract.Schemas["promotion_platform"] || proof.SchemaVersion != SchemaVersion || proof.Result != "pass" {
		return coded(CodePromotionManifestInvalid, "platform proof schema or result is invalid", nil)
	}
	if err := validateTuple(proof.SourceSHA, proof.ReleaseVersion, proof.Repository, proof.RunID, proof.RunAttempt); err != nil {
		return err
	}
	platform, ok := contract.PlatformByID(proof.PlatformID)
	if !ok || proof.GOOS != platform.GOOS || proof.GOARCH != platform.GOARCH {
		return coded(CodePromotionManifestInvalid, "platform proof target is not in the release contract", nil)
	}
	values := map[string]string{"platform": platform.ID, "attempt": strconv.Itoa(proof.RunAttempt)}
	wantArtifact, err := contract.RenderName(contract.Naming.PlatformArtifactTemplate, values)
	if err != nil {
		return coded(CodePromotionManifestInvalid, "render platform artifact name", err)
	}
	wantProof, err := contract.RenderName(contract.Naming.PlatformEvidenceTemplate, values)
	if err != nil {
		return coded(CodePromotionManifestInvalid, "render proof artifact name", err)
	}
	if proof.ArtifactName != wantArtifact || proof.ProofArtifactName != wantProof || proof.Archive.Name != platform.Archive || proof.Checksum.Name != platform.Checksum || proof.Binary.Name != platform.Binary {
		return coded(CodeArtifactInventoryInvalid, "platform proof names differ from the release contract", nil)
	}
	for _, file := range []FileDigest{proof.Archive, proof.Checksum, proof.Binary} {
		if !validFileDigest(file) {
			return coded(CodeDigestMismatch, "platform proof contains an invalid file digest", nil)
		}
	}
	if err := ValidateLiteralVersionBinding(proof.LiteralVersion); err != nil {
		return err
	}
	if proof.LiteralVersion.Evidence.PlatformID != proof.PlatformID ||
		proof.LiteralVersion.Evidence.SourceSHA != proof.SourceSHA ||
		proof.LiteralVersion.Evidence.ReleaseVersion != proof.ReleaseVersion ||
		proof.LiteralVersion.Evidence.Repository != proof.Repository ||
		proof.LiteralVersion.Evidence.RunID != proof.RunID ||
		proof.LiteralVersion.Evidence.RunAttempt != proof.RunAttempt ||
		proof.LiteralVersion.Evidence.Binary != proof.Binary {
		return coded(CodeVersionMismatch, "literal native version evidence differs from the platform proof", nil)
	}
	digest, err := PlatformProofSHA256(proof)
	if err != nil {
		return coded(CodePromotionManifestInvalid, "hash platform proof", err)
	}
	if proof.ProofSHA256 != digest {
		return coded(CodeDigestMismatch, "platform proof self-digest mismatch", nil)
	}
	return nil
}

func verifyChecksum(data []byte, archive FileDigest) error {
	fields := strings.Fields(string(data))
	if len(fields) != 2 || fields[0] != archive.SHA256 || strings.TrimPrefix(fields[1], "*") != archive.Name {
		return coded(CodeDigestMismatch, "checksum sidecar does not bind the exact archive name and digest", nil)
	}
	return nil
}

func validFileDigest(file FileDigest) bool {
	return file.Name != "" && filepath.Base(file.Name) == file.Name && file.Size > 0 && digestPattern.MatchString(file.SHA256)
}

func validateTuple(source, version, repository string, runID int64, attempt int) error {
	if !sourcePattern.MatchString(source) || !repositoryPattern.MatchString(repository) || strings.Contains(repository, "..") || runID <= 0 || attempt <= 0 {
		return coded(CodeSourceMismatch, "source, repository, run ID, or attempt is invalid", nil)
	}
	if !releasecontract.IsVersion(version) {
		return coded(CodeVersionMismatch, "release version is not exact vMAJOR.MINOR.PATCH", nil)
	}
	return nil
}
