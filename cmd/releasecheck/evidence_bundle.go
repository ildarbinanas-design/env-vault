package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
	"github.com/ildarbinanas-design/env-vault/internal/releaseevidence"
)

type bundleVerificationDocument struct {
	SchemaID            string `json:"schema_id"`
	SchemaVersion       int    `json:"schema_version"`
	OK                  bool   `json:"ok"`
	Repository          string `json:"repository"`
	ReleaseVersion      string `json:"release_version"`
	SourceSHA           string `json:"source_sha"`
	EvidenceSHA256      string `json:"evidence_sha256"`
	BundleSHA256        string `json:"bundle_sha256"`
	ReconstructedV1Byte int64  `json:"reconstructed_v1_bytes"`
	Decision            string `json:"decision"`
	ErrorCode           string `json:"error_code"`
}

func runEvidenceBundleCreate(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("evidence bundle-create")
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract JSON")
	input := set.String("input", "", "canonical v1 release evidence JSON")
	outputDir := set.String("output-dir", "", "new v2 bundle directory")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *input == "" || *outputDir == "" {
		fmt.Fprint(stderr, "usage: releasecheck evidence bundle-create --input FILE --output-dir DIR [--contract FILE]\n")
		return exitUsage
	}
	contract, err := releasecontract.LoadFile(*contractPath)
	if err != nil {
		return writeFailure(stdout, stderr, false, "CONTRACT_INVALID", err, exitContractInvalid)
	}
	legacy, err := readRegularEvidenceInput(*input)
	if err != nil {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	evidence, err := releaseevidence.ParseEvidence(legacy)
	if err != nil {
		return writeEvidenceFailure(stdout, stderr, false, err)
	}
	if err := releaseevidence.Verify(evidence, contract); err != nil {
		return writeEvidenceFailure(stdout, stderr, false, err)
	}
	canonical, err := releaseevidence.MarshalJSON(evidence)
	if err != nil || !bytes.Equal(canonical, legacy) {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", errors.New("v1 evidence input is not canonical JSON"), exitSnapshotInvalid)
	}
	files, err := releaseevidence.BuildBundle(evidence, contract)
	if err != nil {
		return writeEvidenceFailure(stdout, stderr, false, err)
	}
	if err := writeBundleDirectoryNoClobber(*outputDir, files); err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	bundle, err := releaseevidence.ParseBundle(files.Root)
	if err != nil {
		return writeEvidenceFailure(stdout, stderr, false, err)
	}
	fmt.Fprintf(stdout, "assembled compact release evidence: version=%s source_sha=%s bundle_sha256=%s objects=%d\n", bundle.ReleaseVersion, bundle.SourceSHA, bundle.BundleSHA256, len(bundle.Objects))
	return exitOK
}

func runEvidenceBundleVerify(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("evidence bundle-verify")
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract JSON")
	historicalContractPath := set.String("historical-contract", releasecontract.LegacyArchivePath, "immutable archival v1 release contract JSON")
	registryPath := set.String("registry", releasecontract.HistoricalRegistryPath, "closed historical compatibility registry")
	historicalEvidenceCommit := set.String("historical-evidence-commit", "", "immutable release-evidence branch commit SHA for registry-bound replay")
	historicalEvidenceParent := set.String("historical-evidence-parent", "", "exact parent of the immutable evidence commit")
	historicalEvidenceRunID := set.Int64("historical-evidence-run-id", 0, "exact evidence workflow run ID")
	historicalEvidenceRunAttempt := set.Int("historical-evidence-run-attempt", 0, "exact evidence workflow run attempt")
	historicalArtifactID := set.Int64("historical-artifact-id", 0, "exact compact evidence artifact ID")
	historicalArtifactDigest := set.String("historical-artifact-digest", "", "exact sha256: digest of the compact evidence artifact")
	bundleDir := set.String("bundle-dir", "", "complete v2 bundle directory")
	jsonOutput := set.Bool("json", false, "emit typed verification JSON")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *bundleDir == "" {
		fmt.Fprint(stderr, "usage: releasecheck evidence bundle-verify --bundle-dir DIR [--contract FILE] [--historical-contract FILE --registry FILE --historical-evidence-commit SHA --historical-evidence-parent SHA --historical-evidence-run-id ID --historical-evidence-run-attempt N --historical-artifact-id ID --historical-artifact-digest sha256:DIGEST] [--json]\n")
		return exitUsage
	}
	files, err := readBundleDirectory(*bundleDir)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	bundle, err := releaseevidence.ParseBundle(files.Root)
	if err != nil {
		return writeEvidenceFailure(stdout, stderr, *jsonOutput, err)
	}
	var contract releasecontract.Contract
	var historicalIdentity releasecontract.HistoricalIdentity
	historicalCoordinatesSupplied := *historicalEvidenceParent != "" || *historicalEvidenceRunID != 0 ||
		*historicalEvidenceRunAttempt != 0 || *historicalArtifactID != 0 || *historicalArtifactDigest != ""
	if *historicalEvidenceCommit == "" {
		if historicalCoordinatesSupplied {
			fmt.Fprint(stderr, "historical bundle coordinates require an exact evidence commit\n")
			return exitUsage
		}
		contract, err = releasecontract.LoadFile(*contractPath)
	} else {
		artifactDigestValue := strings.TrimPrefix(*historicalArtifactDigest, "sha256:")
		artifactDigestBytes, artifactDigestErr := hex.DecodeString(artifactDigestValue)
		if !evidenceSHA.MatchString(*historicalEvidenceCommit) || !evidenceSHA.MatchString(*historicalEvidenceParent) ||
			*historicalEvidenceRunID <= 0 || *historicalEvidenceRunAttempt <= 0 || *historicalArtifactID <= 0 ||
			!strings.HasPrefix(*historicalArtifactDigest, "sha256:") || len(artifactDigestValue) != 64 || artifactDigestErr != nil || len(artifactDigestBytes) != 32 {
			fmt.Fprint(stderr, "historical bundle coordinates are incomplete or malformed\n")
			return exitUsage
		}
		rootDigest := sha256.Sum256(files.Root)
		contract, historicalIdentity, err = releasecontract.LoadHistoricalBundleContract(
			*historicalContractPath,
			*registryPath,
			releasecontract.HistoricalBundleObservation{
				Repository: bundle.Repository, ReleaseVersion: bundle.ReleaseVersion, SourceSHA: bundle.SourceSHA,
				EvidenceCommitSHA:      *historicalEvidenceCommit,
				EvidenceRootFileSHA256: hex.EncodeToString(rootDigest[:]), EvidenceRootSemanticSHA256: bundle.BundleSHA256,
				EvidenceRootSchemaID: bundle.SchemaID, EvidenceRootSchemaVersion: bundle.SchemaVersion,
				ReconstructedLegacyEvidenceSHA256:      bundle.LegacyEvidenceSHA256,
				ReconstructedLegacyCanonicalJSONSHA256: bundle.LegacyCanonicalJSONSHA256,
				ReconstructedLegacyCanonicalJSONSize:   bundle.LegacyCanonicalJSONSize,
				EvidenceParentCommitSHA:                *historicalEvidenceParent,
				PublisherRunID:                         bundle.PublisherRunID, PublisherRunAttempt: bundle.PublisherRunAttempt,
				EvidenceRunID: *historicalEvidenceRunID, EvidenceRunAttempt: *historicalEvidenceRunAttempt,
				CompactArtifactID: *historicalArtifactID, CompactArtifactDigest: *historicalArtifactDigest,
			},
		)
	}
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "CONTRACT_INVALID", err, exitContractInvalid)
	}
	evidence, err := releaseevidence.VerifyBundle(files, contract)
	if err != nil {
		return writeEvidenceFailure(stdout, stderr, *jsonOutput, err)
	}
	legacy, err := releaseevidence.MarshalJSON(evidence)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "OUTPUT_FAILED", err, exitInternal)
	}
	if *historicalEvidenceCommit != "" {
		canonicalDigest := sha256.Sum256(legacy)
		if err := releasecontract.ValidateHistoricalBundleReconstruction(
			historicalIdentity, evidence.EvidenceSHA256, hex.EncodeToString(canonicalDigest[:]), int64(len(legacy)),
		); err != nil {
			return writeFailure(stdout, stderr, *jsonOutput, "CONTRACT_INVALID", err, exitContractInvalid)
		}
	}
	if *jsonOutput {
		document := bundleVerificationDocument{
			SchemaID: "env-vault.release-evidence-bundle-verification.v1", SchemaVersion: 1, OK: true,
			Repository: evidence.Repository, ReleaseVersion: evidence.ReleaseVersion, SourceSHA: evidence.SourceSHA,
			EvidenceSHA256: evidence.EvidenceSHA256, BundleSHA256: bundle.BundleSHA256,
			ReconstructedV1Byte: int64(len(legacy)), Decision: "pass", ErrorCode: "",
		}
		if err := writeJSON(stdout, document); err != nil {
			fmt.Fprintf(stderr, "write JSON: %v\n", err)
			return exitInternal
		}
	} else {
		fmt.Fprintf(stdout, "verified compact release evidence: version=%s source_sha=%s bundle_sha256=%s\n", evidence.ReleaseVersion, evidence.SourceSHA, bundle.BundleSHA256)
	}
	return exitOK
}

func runEvidenceBundleParity(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("evidence bundle-parity")
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract JSON")
	legacyPath := set.String("legacy", "", "canonical v1 evidence JSON")
	bundleDir := set.String("bundle-dir", "", "complete v2 bundle directory")
	output := set.String("output", "", "new typed parity JSON")
	jsonOutput := set.Bool("json", false, "also emit parity JSON to stdout")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *legacyPath == "" || *bundleDir == "" || *output == "" || *output == "-" {
		fmt.Fprint(stderr, "usage: releasecheck evidence bundle-parity --legacy FILE --bundle-dir DIR --output FILE [--contract FILE] [--json]\n")
		return exitUsage
	}
	contract, err := releasecontract.LoadFile(*contractPath)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "CONTRACT_INVALID", err, exitContractInvalid)
	}
	legacy, err := readRegularEvidenceInput(*legacyPath)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	files, err := readBundleDirectory(*bundleDir)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	parity, err := releaseevidence.VerifyParity(legacy, files, contract)
	if err != nil {
		return writeEvidenceFailure(stdout, stderr, *jsonOutput, err)
	}
	encoded, err := releaseevidence.MarshalJSON(parity)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "OUTPUT_FAILED", err, exitInternal)
	}
	if err := writeExclusiveFile(*output, encoded); err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "OUTPUT_FAILED", err, exitInternal)
	}
	if *jsonOutput {
		if _, err := stdout.Write(encoded); err != nil {
			fmt.Fprintf(stderr, "write JSON: %v\n", err)
			return exitInternal
		}
	} else {
		fmt.Fprintf(stdout, "verified v1/v2 evidence parity: version=%s source_sha=%s bundle_sha256=%s\n", parity.ReleaseVersion, parity.SourceSHA, parity.BundleSHA256)
	}
	return exitOK
}

func runEvidenceBundleMeasure(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("evidence bundle-measure")
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract JSON")
	legacyPath := set.String("legacy", "", "canonical v1 evidence JSON")
	bundleDir := set.String("bundle-dir", "", "complete v2 bundle directory")
	indexPath := set.String("index", "", "deterministic Markdown evidence index")
	metricsJSONPath := set.String("metrics-json", "", "metrics comparison JSON")
	metricsMarkdownPath := set.String("metrics-markdown", "", "metrics comparison Markdown")
	output := set.String("output", "", "new storage metrics JSON")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *legacyPath == "" || *bundleDir == "" || *indexPath == "" || *metricsJSONPath == "" || *metricsMarkdownPath == "" || *output == "" || *output == "-" {
		fmt.Fprint(stderr, "usage: releasecheck evidence bundle-measure --legacy FILE --bundle-dir DIR --index FILE --metrics-json FILE --metrics-markdown FILE --output FILE [--contract FILE]\n")
		return exitUsage
	}
	contract, err := releasecontract.LoadFile(*contractPath)
	if err != nil {
		return writeFailure(stdout, stderr, false, "CONTRACT_INVALID", err, exitContractInvalid)
	}
	legacy, err := readRegularEvidenceInput(*legacyPath)
	if err != nil {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	files, err := readBundleDirectory(*bundleDir)
	if err != nil {
		return writeFailure(stdout, stderr, false, "INPUT_INVALID", err, exitSnapshotInvalid)
	}
	auxiliary := make(map[string][]byte, 3)
	for name, filename := range map[string]string{"index.md": *indexPath, "metrics-comparison.json": *metricsJSONPath, "metrics-comparison.md": *metricsMarkdownPath} {
		data, readErr := readRegularEvidenceInput(filename)
		if readErr != nil {
			return writeFailure(stdout, stderr, false, "INPUT_INVALID", readErr, exitSnapshotInvalid)
		}
		auxiliary[name] = data
	}
	metrics, err := releaseevidence.MeasureBundle(legacy, files, auxiliary, contract)
	if err != nil {
		return writeEvidenceFailure(stdout, stderr, false, err)
	}
	encoded, err := releaseevidence.MarshalJSON(metrics)
	if err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	if err := writeExclusiveFile(*output, encoded); err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	fmt.Fprintf(stdout, "measured compact release evidence: root_bytes=%d logical_reduction_permille=%d targets_met=%t\n", metrics.CompactRootIndexBytes, metrics.LogicalPayloadReductionPermille, metrics.TargetsMet)
	return exitOK
}

func writeBundleDirectoryNoClobber(directory string, files releaseevidence.BundleFiles) error {
	return writeBundleDirectoryNoClobberWithHook(directory, files, nil)
}

func writeBundleDirectoryNoClobberWithHook(directory string, files releaseevidence.BundleFiles, beforeReserve func()) error {
	if directory == "" || filepath.Clean(directory) == "." {
		return errors.New("bundle output directory is invalid")
	}
	parent := filepath.Dir(directory)
	parentInfo, err := os.Lstat(parent)
	if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("bundle output parent is not a regular directory: %s", parent)
	}
	if beforeReserve != nil {
		beforeReserve()
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		return fmt.Errorf("reserve no-clobber bundle directory: %w", err)
	}
	marker := filepath.Join(directory, ".incomplete")
	if err := writeExclusiveFile(marker, []byte("bundle publication incomplete\n")); err != nil {
		return fmt.Errorf("create incomplete bundle marker: %w", err)
	}
	objectsDirectory := filepath.Join(directory, "objects")
	if err := os.Mkdir(objectsDirectory, 0o700); err != nil {
		return fmt.Errorf("create bundle object directory: %w", err)
	}
	shaDirectory := filepath.Join(objectsDirectory, "sha256")
	if err := os.Mkdir(shaDirectory, 0o700); err != nil {
		return fmt.Errorf("create bundle digest directory: %w", err)
	}
	paths := make([]string, 0, len(files.Objects))
	for name := range files.Objects {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	for _, name := range paths {
		if !strings.HasPrefix(name, releaseevidence.BundleObjectStoreDirectory+"/") || filepath.Dir(filepath.FromSlash(name)) != filepath.FromSlash(releaseevidence.BundleObjectStoreDirectory) {
			return fmt.Errorf("bundle object path is outside the exact store: %s", name)
		}
		target := filepath.Join(directory, filepath.FromSlash(name))
		if err := writeExclusiveFile(target, files.Objects[name]); err != nil {
			return fmt.Errorf("write bundle object: %w", err)
		}
	}
	// The root index is the completion payload and is deliberately published
	// after every object. The marker remains until the exact candidate exists.
	if err := writeExclusiveFile(filepath.Join(directory, releaseevidence.BundleRootName), files.Root); err != nil {
		return fmt.Errorf("write bundle root: %w", err)
	}
	if err := os.Remove(marker); err != nil {
		return fmt.Errorf("commit bundle directory: %w", err)
	}
	return nil
}

func readBundleDirectory(directory string) (releaseevidence.BundleFiles, error) {
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return releaseevidence.BundleFiles{}, fmt.Errorf("bundle root is not a non-symlink directory: %s", directory)
	}
	rootPath := filepath.Join(directory, releaseevidence.BundleRootName)
	root, err := readBoundedRegularFile(rootPath, releaseevidence.MaxBundleRootBytes)
	if err != nil {
		return releaseevidence.BundleFiles{}, err
	}
	bundle, err := releaseevidence.ParseBundle(root)
	if err != nil {
		return releaseevidence.BundleFiles{}, err
	}
	topLevel, err := os.ReadDir(directory)
	if err != nil {
		return releaseevidence.BundleFiles{}, err
	}
	if len(topLevel) != 2 || topLevel[0].Name() != "objects" || topLevel[1].Name() != releaseevidence.BundleRootName {
		return releaseevidence.BundleFiles{}, errors.New("bundle directory must contain exactly the root index and object store")
	}
	objectsRoot := filepath.Join(directory, "objects")
	objectInfo, err := os.Lstat(objectsRoot)
	if err != nil || !objectInfo.IsDir() || objectInfo.Mode()&os.ModeSymlink != 0 {
		return releaseevidence.BundleFiles{}, errors.New("bundle object store is missing or is not a non-symlink directory")
	}
	objects := make(map[string][]byte, len(bundle.Objects))
	expectedPaths := make(map[string]releaseevidence.BundleObject, len(bundle.Objects))
	for _, descriptor := range bundle.Objects {
		canonical, pathErr := releaseevidence.BundleObjectRelativePath(descriptor.SHA256)
		if pathErr != nil {
			return releaseevidence.BundleFiles{}, pathErr
		}
		expectedPaths[canonical] = descriptor
	}
	seenPaths := make(map[string]bool, len(bundle.Objects))
	seenDirectories := make(map[string]bool, 2)
	err = filepath.WalkDir(objectsRoot, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filename == objectsRoot {
			return nil
		}
		entryInfo, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("bundle object store contains symlink: %s", filename)
		}
		if entry.IsDir() {
			relative, relErr := filepath.Rel(directory, filename)
			if relErr != nil {
				return relErr
			}
			seenDirectories[filepath.ToSlash(relative)] = true
			return nil
		}
		if !entryInfo.Mode().IsRegular() {
			return fmt.Errorf("bundle object store contains non-regular entry: %s", filename)
		}
		relative, relErr := filepath.Rel(directory, filename)
		if relErr != nil {
			return relErr
		}
		canonical := filepath.ToSlash(relative)
		if _, expected := expectedPaths[canonical]; !expected {
			return fmt.Errorf("bundle object store contains unreferenced file: %s", canonical)
		}
		seenPaths[canonical] = true
		return nil
	})
	if err != nil {
		return releaseevidence.BundleFiles{}, err
	}
	if len(seenDirectories) != 1 || !seenDirectories["objects/sha256"] {
		return releaseevidence.BundleFiles{}, errors.New("bundle object store directory shape is not exact")
	}
	if len(seenPaths) != len(bundle.Objects) {
		return releaseevidence.BundleFiles{}, errors.New("bundle object store has missing or extra files")
	}
	var aggregate int64
	for _, descriptor := range bundle.Objects {
		canonical, pathErr := releaseevidence.BundleObjectRelativePath(descriptor.SHA256)
		if pathErr != nil {
			return releaseevidence.BundleFiles{}, pathErr
		}
		if !seenPaths[canonical] {
			return releaseevidence.BundleFiles{}, fmt.Errorf("bundle object path is missing: %s", canonical)
		}
		objectPath := filepath.Join(directory, filepath.FromSlash(canonical))
		objectInfo, statErr := os.Lstat(objectPath)
		if statErr != nil || objectInfo.Mode()&os.ModeSymlink != 0 || !objectInfo.Mode().IsRegular() || objectInfo.Size() != descriptor.CompressedSize || descriptor.CompressedSize <= 0 {
			return releaseevidence.BundleFiles{}, fmt.Errorf("bundle object size or identity differs before read: %s", canonical)
		}
		aggregate += descriptor.CompressedSize
		if aggregate > releaseevidence.MaxBundleAggregateCompressed {
			return releaseevidence.BundleFiles{}, errors.New("bundle compressed object aggregate exceeds the supported limit")
		}
	}
	for _, descriptor := range bundle.Objects {
		canonical, _ := releaseevidence.BundleObjectRelativePath(descriptor.SHA256)
		data, readErr := readBoundedRegularFile(filepath.Join(directory, filepath.FromSlash(canonical)), int(descriptor.CompressedSize))
		if readErr != nil {
			return releaseevidence.BundleFiles{}, readErr
		}
		objects[canonical] = data
	}
	return releaseevidence.BundleFiles{Root: root, Objects: objects}, nil
}

func readBoundedRegularFile(filename string, limit int) ([]byte, error) {
	before, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > int64(limit) {
		return nil, fmt.Errorf("%s is not a bounded regular non-symlink file", filename)
	}
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil || !os.SameFile(before, after) || after.Size() != before.Size() {
		return nil, fmt.Errorf("%s changed identity while opening", filename)
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil || int64(len(data)) != before.Size() {
		return nil, fmt.Errorf("read stable bounded file %s: %w", filename, err)
	}
	return data, nil
}
