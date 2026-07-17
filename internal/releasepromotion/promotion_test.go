package releasepromotion

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

const (
	testSource     = "0123456789abcdef0123456789abcdef01234567"
	testVersion    = "v1.2.3"
	testRepository = "owner/repository"
)

func TestRecordPlatformBindsArtifactReportsAndLiteralVersion(t *testing.T) {
	root := t.TempDir()
	contractPath := filepath.Join("..", "..", filepath.FromSlash(releasecontract.CanonicalPath))
	archive := writeTestFile(t, root, "env-vault-linux-amd64.tar.gz", []byte("archive bytes"))
	archiveDigest, err := fileSHA256(archive)
	if err != nil {
		t.Fatal(err)
	}
	checksum := writeTestFile(t, root, "env-vault-linux-amd64.tar.gz.sha256", []byte(archiveDigest+"  env-vault-linux-amd64.tar.gz\n"))
	binary := writeTestFile(t, root, "env-vault", []byte("binary bytes"))
	binaryDigest, err := fileSHA256(binary)
	if err != nil {
		t.Fatal(err)
	}
	reportDir := filepath.Join(root, "reports", "go1.26.5", "linux-amd64")
	if err := os.MkdirAll(reportDir, 0o755); err != nil {
		t.Fatal(err)
	}
	contracts := writeTestFile(t, reportDir, "contracts.json", []byte(`{"schema_version":"1","records":[]}`))
	feature := writeJSONFile(t, reportDir, "feature-coverage.json", map[string]any{
		"schema_version": "1", "platform": "linux-amd64", "suite_hash": repeatedDigest("a"),
		"critical_covered": 2, "critical_total": 2, "critical_coverage_percent": 100,
		"missing_critical": []any{}, "unexpected_skips": []any{},
	})
	writeJSONFile(t, reportDir, "leak-scan.json", map[string]any{
		"schema_version": "1", "status": "pass", "detected": false, "occurrences": 0,
		"registry_records": 3, "findings": []any{},
	})
	contractsDigest, _ := fileSHA256(contracts)
	featureDigest, _ := fileSHA256(feature)
	writeJSONFile(t, reportDir, "metadata.json", map[string]any{
		"schema_version": "1", "phase": "candidate", "status": "pass", "subject_kind": "artifact",
		"platform": "linux-amd64", "commit_sha": testSource, "github_repository": testRepository,
		"github_run_id": "42", "github_run_attempt": "3", "suite_hash": repeatedDigest("a"),
		"statement_coverage_percent": 71.2, "binary_sha256": binaryDigest,
		"go_version": "go1.26.5", "binary_go_version": "go1.26.5", "gotestsum_version": "v1.13.0",
		"artifact":         map[string]any{"sha256": archiveDigest, "checksum_verified": true},
		"counts":           map[string]any{"failed": 0, "skipped": 0, "missing": 0},
		"unexpected_skips": []any{}, "contract_records": []any{map[string]any{"id": "critical"}},
		"evidence_sha256": map[string]any{"contracts.json": contractsDigest, "feature-coverage.json": featureDigest},
	})

	evidence, err := RecordPlatform(RecordOptions{
		ContractPath: contractPath, PlatformID: "linux-amd64", SourceSHA: testSource,
		ReleaseVersion: testVersion, Repository: testRepository, RunID: 42, RunAttempt: 3,
		ArchivePath: archive, ChecksumPath: checksum, BinaryPath: binary, ReportsRoot: filepath.Join(root, "reports"),
		ArtifactName:    "env-vault-release-linux-amd64-attempt-3",
		E2EArtifactName: "env-vault-e2e-candidate-linux-amd64-attempt-3", CoverageFloor: 60,
		runBinary: func(_ string, args ...string) ([]byte, error) {
			if len(args) == 2 {
				return []byte(`{"ok":true,"command":"version","data":{"version":"v1.2.3"},"error":null}`), nil
			}
			return []byte(testVersion + "\n"), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Archive.SHA256 != archiveDigest || evidence.Contracts.SHA256 != contractsDigest || evidence.Coverage.CriticalCovered != 2 || evidence.LiteralVersion.JSON != "pass" {
		t.Fatalf("unexpected evidence: %+v", evidence)
	}

	badChecksum := writeTestFile(t, t.TempDir(), "env-vault-linux-amd64.tar.gz.sha256", []byte(repeatedDigest("0")+"  env-vault-linux-amd64.tar.gz\n"))
	_, err = RecordPlatform(RecordOptions{
		ContractPath: contractPath, PlatformID: "linux-amd64", SourceSHA: testSource,
		ReleaseVersion: testVersion, Repository: testRepository, RunID: 42, RunAttempt: 3,
		ArchivePath: archive, ChecksumPath: badChecksum, BinaryPath: binary, ReportsRoot: filepath.Join(root, "reports"),
		CoverageFloor: 60,
	})
	if err == nil {
		t.Fatal("mismatched checksum was accepted")
	}
}

func TestAssembleAndVerifyRequireOneAttemptCompleteMatrix(t *testing.T) {
	contractPath := filepath.Join("..", "..", filepath.FromSlash(releasecontract.CanonicalPath))
	contract, err := releasecontract.LoadFile(contractPath)
	if err != nil {
		t.Fatal(err)
	}
	evidence := make([]PlatformEvidence, 0, len(contract.Platforms))
	for index, platform := range contract.Platforms {
		evidence = append(evidence, passingPlatform(platform, index, 3))
	}
	manifest, err := Assemble(AssembleOptions{
		ContractPath: contractPath, SourceSHA: testSource, ReleaseVersion: testVersion,
		Repository: testRepository, RunID: 42, RunAttempt: 3, Event: "push",
		CreatedAt: time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC), Evidence: evidence,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(manifest, VerifyOptions{
		ContractPath: contractPath, SourceSHA: testSource, ReleaseVersion: testVersion,
		Repository: testRepository, RunID: 42, RunAttempt: 3,
	}); err != nil {
		t.Fatal(err)
	}

	mixed := append([]PlatformEvidence(nil), evidence...)
	mixed[4].RunAttempt = 2
	if _, err := Assemble(AssembleOptions{
		ContractPath: contractPath, SourceSHA: testSource, ReleaseVersion: testVersion,
		Repository: testRepository, RunID: 42, RunAttempt: 3, Event: "push",
		CreatedAt: time.Now(), Evidence: mixed,
	}); err == nil {
		t.Fatal("mixed workflow attempts were accepted")
	}
	if _, err := Assemble(AssembleOptions{
		ContractPath: contractPath, SourceSHA: testSource, ReleaseVersion: testVersion,
		Repository: testRepository, RunID: 42, RunAttempt: 3, Event: "push",
		CreatedAt: time.Now(), Evidence: evidence[:4],
	}); err == nil {
		t.Fatal("incomplete platform matrix was accepted")
	}

	manifest.Platforms[0].Archive.SHA256 = repeatedDigest("f")
	if err := Verify(manifest, VerifyOptions{ContractPath: contractPath}); err == nil {
		t.Fatal("tampered manifest was accepted")
	}
}

func TestRecordIdentityAllowsOnlyExactReleaseOrSourceBoundCIVersion(t *testing.T) {
	if err := validateRecordIdentity(testSource, "ci-"+testSource, testRepository, 42, 1); err != nil {
		t.Fatal(err)
	}
	if err := validateRecordIdentity(testSource, "ci-"+strings.Repeat("f", 40), testRepository, 42, 1); err == nil {
		t.Fatal("CI version for a different source SHA was accepted")
	}
	if err := validateReleaseIdentity(testSource, "ci-"+testSource, testRepository, 42, 1); err == nil {
		t.Fatal("CI version was accepted as a release promotion version")
	}
}

func passingPlatform(platform releasecontract.Platform, index, attempt int) PlatformEvidence {
	digest := repeatedDigest(strconv.FormatInt(int64(index%9), 10))
	return PlatformEvidence{
		SchemaID: PlatformSchemaID, PlatformID: platform.ID, GOOS: platform.GOOS, GOARCH: platform.GOARCH,
		SourceSHA: testSource, ReleaseVersion: testVersion, Repository: testRepository, RunID: 42, RunAttempt: attempt,
		ArtifactName: "env-vault-release-" + platform.ID + "-attempt-" + strconv.Itoa(attempt),
		E2EArtifact:  "env-vault-e2e-candidate-" + platform.ID + "-attempt-" + strconv.Itoa(attempt),
		Archive:      FileDigest{Name: platform.Archive, SHA256: digest},
		Checksum:     FileDigest{Name: platform.Checksum, SHA256: digest}, BinarySHA256: digest,
		SuiteHash: repeatedDigest("a"), Metadata: FileDigest{Name: "metadata.json", SHA256: digest},
		LiteralVersion: LiteralVersionResults{Flag: "pass", Command: "pass", JSON: "pass"},
		Contracts:      ContractEvidence{SHA256: digest, Count: 1},
		Coverage:       CoverageEvidence{SHA256: digest, StatementPercent: 70, FloorPercent: 60, CriticalCovered: 1, CriticalTotal: 1},
		Leak:           LeakEvidence{SHA256: digest, Status: "pass", RegistryRecords: 1}, Result: "pass",
	}
}

func writeTestFile(t *testing.T, directory, name string, data []byte) string {
	t.Helper()
	filename := filepath.Join(directory, name)
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return filename
}

func writeJSONFile(t *testing.T, directory, name string, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return writeTestFile(t, directory, name, data)
}

func repeatedDigest(character string) string {
	sum := sha256.Sum256([]byte(character))
	return hex.EncodeToString(sum[:])
}
