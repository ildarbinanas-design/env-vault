package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
	"github.com/ildarbinanas-design/env-vault/internal/releasepromotion"
)

type promotionVerificationDocument struct {
	SchemaID       string `json:"schema_id"`
	SchemaVersion  int    `json:"schema_version"`
	OK             bool   `json:"ok"`
	SourceSHA      string `json:"source_sha"`
	ReleaseVersion string `json:"release_version"`
	Repository     string `json:"repository"`
	RunID          int64  `json:"run_id"`
	RunAttempt     int    `json:"run_attempt"`
	ManifestSHA256 string `json:"manifest_sha256"`
	Result         string `json:"result"`
}

type repeatedStrings []string

func (values *repeatedStrings) String() string { return strings.Join(*values, ",") }

func (values *repeatedStrings) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("value must be non-empty")
	}
	*values = append(*values, value)
	return nil
}

func runPromotion(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, promotionUsage())
		return exitUsage
	}
	switch args[0] {
	case "record-platform":
		return runPromotionRecord(args[1:], stdout, stderr)
	case "seal-source-quality":
		return runPromotionSealSource(args[1:], stdout, stderr)
	case "assemble":
		return runPromotionAssemble(args[1:], stdout, stderr)
	case "verify":
		return runPromotionVerify(args[1:], stdout, stderr)
	default:
		fmt.Fprint(stderr, promotionUsage())
		return exitUsage
	}
}

func runPromotionRecord(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("promotion record-platform")
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract JSON")
	platform := set.String("platform", "", "contract platform ID")
	source := set.String("source-sha", "", "exact source SHA")
	version := set.String("release-version", "", "exact vX.Y.Z")
	repository := set.String("repository", "", "owner/repository")
	runID := set.Int64("run-id", 0, "workflow run ID")
	attempt := set.Int("run-attempt", 0, "workflow run attempt")
	archive := set.String("archive", "", "release archive file")
	checksum := set.String("checksum", "", "checksum sidecar file")
	binary := set.String("binary", "", "native binary file")
	versionResults := set.String("version-results", "", "saved native version results JSON")
	output := set.String("output", "", "new proof file, or - for stdout")
	jsonOutput := set.Bool("json", false, "also emit proof JSON to stdout")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *platform == "" || *source == "" || *version == "" || *repository == "" || *runID <= 0 || *attempt <= 0 || *archive == "" || *checksum == "" || *binary == "" || *versionResults == "" || *output == "" {
		fmt.Fprint(stderr, promotionRecordUsage())
		return exitUsage
	}
	if code := requirePromotionContract(*contractPath, stdout, stderr, *jsonOutput || *output == "-"); code != exitOK {
		return code
	}
	proof, err := releasepromotion.RecordPlatform(releasepromotion.RecordOptions{
		ContractPath: *contractPath, PlatformID: *platform, SourceSHA: *source,
		ReleaseVersion: *version, Repository: *repository, RunID: *runID, RunAttempt: *attempt,
		ArchivePath: *archive, ChecksumPath: *checksum, BinaryPath: *binary, VersionResultsPath: *versionResults,
	})
	if err != nil {
		return writePromotionFailure(stdout, stderr, *jsonOutput || *output == "-", err)
	}
	if code := writePromotionOutput(*output, *jsonOutput, proof, stdout, stderr); code != exitOK {
		return code
	}
	if !*jsonOutput && *output != "-" {
		fmt.Fprintf(stdout, "recorded platform proof: platform=%s run_id=%d attempt=%d output=%s\n", proof.PlatformID, proof.RunID, proof.RunAttempt, *output)
	}
	return exitOK
}

func runPromotionSealSource(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("promotion seal-source-quality")
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract JSON")
	input := set.String("input", "", "unsealed source-quality proof JSON")
	output := set.String("output", "", "new sealed proof file, or - for stdout")
	jsonOutput := set.Bool("json", false, "also emit sealed proof JSON to stdout")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *input == "" || *output == "" {
		fmt.Fprint(stderr, promotionSealUsage())
		return exitUsage
	}
	contract, err := releasecontract.LoadFile(*contractPath)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput || *output == "-", "CONTRACT_INVALID", err, exitContractInvalid)
	}
	proof, _, err := releasepromotion.ReadSourceQualityProof(*input)
	if err != nil {
		return writePromotionFailure(stdout, stderr, *jsonOutput || *output == "-", err)
	}
	if err := releasepromotion.SealSourceQualityProof(&proof, contract); err != nil {
		return writePromotionFailure(stdout, stderr, *jsonOutput || *output == "-", err)
	}
	if code := writePromotionOutput(*output, *jsonOutput, proof, stdout, stderr); code != exitOK {
		return code
	}
	if !*jsonOutput && *output != "-" {
		fmt.Fprintf(stdout, "sealed source-quality proof: run_id=%d attempt=%d output=%s\n", proof.Workflow.RunID, proof.Workflow.RunAttempt, *output)
	}
	return exitOK
}

func runPromotionAssemble(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("promotion assemble")
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract JSON")
	source := set.String("source-sha", "", "exact source SHA")
	version := set.String("release-version", "", "exact vX.Y.Z")
	repository := set.String("repository", "", "owner/repository")
	runID := set.Int64("run-id", 0, "workflow run ID")
	attempt := set.Int("run-attempt", 0, "workflow run attempt")
	sourceQuality := set.String("source-quality", "", "sealed source-quality proof JSON")
	matrixProof := set.String("matrix-proof", "", "sealed E2E matrix proof JSON")
	createdAt := set.String("created-at", "", "manifest creation time (RFC3339)")
	output := set.String("output", "", "new promotion manifest file, or - for stdout")
	jsonOutput := set.Bool("json", false, "also emit manifest JSON to stdout")
	var platformProofs repeatedStrings
	set.Var(&platformProofs, "platform-proof", "platform proof JSON (repeat exactly five times)")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *source == "" || *version == "" || *repository == "" || *runID <= 0 || *attempt <= 0 || *sourceQuality == "" || *matrixProof == "" || *createdAt == "" || len(platformProofs) == 0 || *output == "" {
		fmt.Fprint(stderr, promotionAssembleUsage())
		return exitUsage
	}
	when, err := time.Parse(time.RFC3339, *createdAt)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput || *output == "-", "INPUT_INVALID", fmt.Errorf("created-at is not RFC3339"), exitSnapshotInvalid)
	}
	if code := requirePromotionContract(*contractPath, stdout, stderr, *jsonOutput || *output == "-"); code != exitOK {
		return code
	}
	manifest, err := releasepromotion.Assemble(releasepromotion.AssembleOptions{
		ContractPath: *contractPath, SourceSHA: *source, ReleaseVersion: *version,
		Repository: *repository, RunID: *runID, RunAttempt: *attempt,
		SourceQualityPath: *sourceQuality, MatrixProofPath: *matrixProof,
		PlatformProofPaths: append([]string(nil), platformProofs...), CreatedAt: when,
	})
	if err != nil {
		return writePromotionFailure(stdout, stderr, *jsonOutput || *output == "-", err)
	}
	if code := writePromotionOutput(*output, *jsonOutput, manifest, stdout, stderr); code != exitOK {
		return code
	}
	if !*jsonOutput && *output != "-" {
		fmt.Fprintf(stdout, "assembled promotion manifest: run_id=%d attempt=%d output=%s\n", manifest.Workflow.RunID, manifest.Workflow.RunAttempt, *output)
	}
	return exitOK
}

func runPromotionVerify(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("promotion verify")
	contractPath := set.String("contract", releasecontract.CanonicalPath, "release contract JSON")
	source := set.String("source-sha", "", "exact source SHA")
	version := set.String("release-version", "", "exact vX.Y.Z")
	repository := set.String("repository", "", "owner/repository")
	runID := set.Int64("run-id", 0, "workflow run ID")
	attempt := set.Int("run-attempt", 0, "workflow run attempt")
	manifestPath := set.String("manifest", "", "promotion manifest JSON")
	artifactsRoot := set.String("artifacts-root", "", "directory containing exactly ten release assets")
	jsonOutput := set.Bool("json", false, "emit versioned verification JSON")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *source == "" || *version == "" || *repository == "" || *runID <= 0 || *attempt <= 0 || *manifestPath == "" || *artifactsRoot == "" {
		fmt.Fprint(stderr, promotionVerifyUsage())
		return exitUsage
	}
	contract, err := releasecontract.LoadFile(*contractPath)
	if err != nil {
		return writeFailure(stdout, stderr, *jsonOutput, "CONTRACT_INVALID", err, exitContractInvalid)
	}
	manifest, err := releasepromotion.ReadManifest(*manifestPath)
	if err != nil {
		return writePromotionFailure(stdout, stderr, *jsonOutput, err)
	}
	if err := releasepromotion.Verify(manifest, releasepromotion.VerifyOptions{
		ContractPath: *contractPath, SourceSHA: *source, ReleaseVersion: *version,
		Repository: *repository, RunID: *runID, RunAttempt: *attempt, ArtifactsRoot: *artifactsRoot,
	}); err != nil {
		return writePromotionFailure(stdout, stderr, *jsonOutput, err)
	}
	document := promotionVerificationDocument{
		SchemaID: contract.Schemas["promotion_verification"], SchemaVersion: 1, OK: true,
		SourceSHA: manifest.SourceSHA, ReleaseVersion: manifest.ReleaseVersion,
		Repository: manifest.Repository, RunID: manifest.Workflow.RunID, RunAttempt: manifest.Workflow.RunAttempt,
		ManifestSHA256: manifest.ManifestSHA256, Result: "pass",
	}
	if *jsonOutput {
		if err := writeJSON(stdout, document); err != nil {
			fmt.Fprintf(stderr, "write JSON: %v\n", err)
			return exitInternal
		}
	} else {
		fmt.Fprintf(stdout, "verified promotion: version=%s source_sha=%s run_id=%d attempt=%d manifest_sha256=%s\n", document.ReleaseVersion, document.SourceSHA, document.RunID, document.RunAttempt, document.ManifestSHA256)
	}
	return exitOK
}

func requirePromotionContract(filename string, stdout, stderr io.Writer, jsonOutput bool) int {
	if _, err := releasecontract.LoadFile(filename); err != nil {
		return writeFailure(stdout, stderr, jsonOutput, "CONTRACT_INVALID", err, exitContractInvalid)
	}
	return exitOK
}

func writePromotionFailure(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	code := releasepromotion.ErrorCode(err)
	if code == "" {
		code = "PROMOTION_MANIFEST_INVALID"
	}
	return writeFailure(stdout, stderr, jsonOutput, code, err, exitSnapshotInvalid)
}

func writePromotionOutput(filename string, echoJSON bool, value any, stdout, stderr io.Writer) int {
	data, err := releasepromotion.MarshalJSON(value)
	if err != nil {
		return writeFailure(stdout, stderr, echoJSON || filename == "-", "OUTPUT_FAILED", err, exitInternal)
	}
	if filename == "-" {
		if written, err := stdout.Write(data); err != nil || written != len(data) {
			fmt.Fprintf(stderr, "write JSON: %v\n", err)
			return exitInternal
		}
		return exitOK
	}
	if err := writeExclusiveFile(filename, data); err != nil {
		return writeFailure(stdout, stderr, echoJSON, "OUTPUT_FAILED", err, exitInternal)
	}
	if echoJSON {
		if written, err := stdout.Write(data); err != nil || written != len(data) {
			fmt.Fprintf(stderr, "write JSON: %v\n", err)
			return exitInternal
		}
	}
	return exitOK
}

func promotionUsage() string {
	return `usage: releasecheck promotion <command> [flags]
commands: record-platform, seal-source-quality, assemble, verify
`
}

func promotionRecordUsage() string {
	return "usage: releasecheck promotion record-platform --platform ID --source-sha SHA --release-version vX.Y.Z --repository OWNER/REPO --run-id ID --run-attempt N --archive FILE --checksum FILE --binary FILE --version-results FILE --output FILE|- [--contract FILE] [--json]\n"
}

func promotionSealUsage() string {
	return "usage: releasecheck promotion seal-source-quality --input FILE --output FILE|- [--contract FILE] [--json]\n"
}

func promotionAssembleUsage() string {
	return "usage: releasecheck promotion assemble --source-sha SHA --release-version vX.Y.Z --repository OWNER/REPO --run-id ID --run-attempt N --source-quality FILE --matrix-proof FILE --platform-proof FILE... --created-at RFC3339 --output FILE|- [--contract FILE] [--json]\n"
}

func promotionVerifyUsage() string {
	return "usage: releasecheck promotion verify --source-sha SHA --release-version vX.Y.Z --repository OWNER/REPO --run-id ID --run-attempt N --manifest FILE --artifacts-root DIR [--contract FILE] [--json]\n"
}
