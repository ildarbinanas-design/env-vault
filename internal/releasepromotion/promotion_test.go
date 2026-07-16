package releasepromotion

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/e2ebaseline"
	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

const (
	testSource     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testVersion    = "v9.8.7"
	testRepository = "ildarbinanas-design/env-vault"
	testRunID      = int64(424242)
	testAttempt    = 3
)

var testTime = time.Date(2026, 7, 16, 8, 30, 0, 0, time.UTC)

type promotionFixture struct {
	root          string
	contractPath  string
	contract      releasecontract.Contract
	artifactsRoot string
	sourcePath    string
	matrixPath    string
	proofPaths    []string
	manifest      Manifest
}

func TestRecordPlatformChecksBytesNamesAndAllLiteralVersions(t *testing.T) {
	contractPath, contract := loadTestContract(t)
	platform := contract.Platforms[0]
	root := t.TempDir()
	archivePath := filepath.Join(root, platform.Archive)
	archiveBytes := []byte("native archive bytes")
	writeTestFile(t, archivePath, archiveBytes)
	digest := sha256.Sum256(archiveBytes)
	checksumPath := filepath.Join(root, platform.Checksum)
	writeTestFile(t, checksumPath, []byte(hex.EncodeToString(digest[:])+"  "+platform.Archive+"\n"))
	binaryPath := filepath.Join(root, platform.Binary)
	writeTestFile(t, binaryPath, []byte("native binary bytes"))

	versionResultsPath := writeVersionEvidence(t, root, platform, binaryPath, testVersion)
	proof, err := RecordPlatform(RecordOptions{
		ContractPath: contractPath, PlatformID: platform.ID, SourceSHA: testSource,
		ReleaseVersion: testVersion, Repository: testRepository, RunID: testRunID, RunAttempt: testAttempt,
		ArchivePath: archivePath, ChecksumPath: checksumPath, BinaryPath: binaryPath,
		VersionResultsPath: versionResultsPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidatePlatformProof(proof, contract); err != nil {
		t.Fatal(err)
	}
	if proof.ArtifactName != "env-vault-release-linux-amd64-attempt-3" || proof.ProofArtifactName != "env-vault-promotion-platform-linux-amd64-attempt-3" {
		t.Fatalf("attempt-qualified names are wrong: %+v", proof)
	}

	t.Run("wrong literal version", func(t *testing.T) {
		options := basicRecordOptions(contractPath, platform, root)
		options.VersionResultsPath = writeVersionEvidence(t, t.TempDir(), platform, options.BinaryPath, "v9.8.6")
		_, err := RecordPlatform(options)
		assertCode(t, err, CodeVersionMismatch)
	})
	t.Run("legacy JSON argv", func(t *testing.T) {
		data, err := os.ReadFile(versionResultsPath)
		if err != nil {
			t.Fatal(err)
		}
		evidence, err := ParseLiteralVersionEvidence(data)
		if err != nil {
			t.Fatal(err)
		}
		evidence.Commands[2].Args = []string{"--json", "--version"}
		assertCode(t, ValidateLiteralVersionEvidence(evidence), CodeVersionMismatch)
	})
	t.Run("checksum mismatch", func(t *testing.T) {
		bad := filepath.Join(t.TempDir(), platform.Checksum)
		writeTestFile(t, bad, []byte(strings.Repeat("0", 64)+"  "+platform.Archive+"\n"))
		options := basicRecordOptions(contractPath, platform, root)
		options.ChecksumPath = bad
		options.VersionResultsPath = versionResultsPath
		_, err := RecordPlatform(options)
		assertCode(t, err, CodeDigestMismatch)
	})
	t.Run("symlink input", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), platform.Archive)
		if err := os.Symlink(archivePath, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		options := basicRecordOptions(contractPath, platform, root)
		options.ArchivePath = link
		options.VersionResultsPath = versionResultsPath
		_, err := RecordPlatform(options)
		assertCode(t, err, CodeArtifactInventoryInvalid)
	})
}

func TestAssembleAndVerifyFiveTargetPromotion(t *testing.T) {
	fixture := newPromotionFixture(t)
	if len(fixture.manifest.Platforms) != 5 || len(fixture.manifest.Assets) != 10 {
		t.Fatalf("manifest platform/assets=%d/%d", len(fixture.manifest.Platforms), len(fixture.manifest.Assets))
	}
	if fixture.manifest.SemanticSuiteHash != fixture.manifest.E2EMatrix.Proof.SuiteHash {
		t.Fatal("manifest did not bind the sealed semantic suite")
	}
	if err := Verify(fixture.manifest, fixture.verifyOptions()); err != nil {
		t.Fatal(err)
	}
	encoded, err := MarshalJSON(fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(fixture.root, "promotion-manifest.json")
	writeTestFile(t, manifestPath, encoded)
	if err := VerifyFile(manifestPath, fixture.verifyOptions()); err != nil {
		t.Fatal(err)
	}
}

func TestSealSourceQualityDerivesPassOnlyFromExactObservedSuccess(t *testing.T) {
	_, contract := loadTestContract(t)
	workflow, _ := contract.WorkflowByID("ci")
	proof := SourceQualityProof{
		SchemaID: contract.Schemas["source_quality_proof"], SchemaVersion: SchemaVersion,
		SourceSHA: testSource, ReleaseVersion: testVersion, Repository: testRepository,
		Workflow:     Workflow{ID: workflow.ID, Name: workflow.Name, File: workflow.File, RunID: testRunID, RunAttempt: testAttempt, Event: "push", HeadSHA: testSource},
		ObservedJobs: SourceQualityObservedJobs{SourceQuality: "success", LicenseMatrix: "success"},
	}
	if err := SealSourceQualityProof(&proof, contract); err != nil {
		t.Fatal(err)
	}
	if proof.Result != "pass" || !reflect.DeepEqual(proof.Results, passingSourceQualityResults()) {
		t.Fatalf("derived results=%+v result=%q", proof.Results, proof.Result)
	}
	for _, conclusion := range []string{"", "pass", "failure", "cancelled", "skipped", "Success"} {
		candidate := proof
		candidate.ObservedJobs.SourceQuality = conclusion
		if err := SealSourceQualityProof(&candidate, contract); err == nil {
			t.Fatalf("observed conclusion %q was accepted", conclusion)
		}
	}
}

func TestVerifyFailsClosedOnTupleMatrixAndInventoryTampering(t *testing.T) {
	t.Run("expected version", func(t *testing.T) {
		fixture := newPromotionFixture(t)
		options := fixture.verifyOptions()
		options.ReleaseVersion = "v9.8.8"
		assertCode(t, Verify(fixture.manifest, options), CodeVersionMismatch)
	})
	t.Run("expected source", func(t *testing.T) {
		fixture := newPromotionFixture(t)
		options := fixture.verifyOptions()
		options.SourceSHA = strings.Repeat("b", 40)
		assertCode(t, Verify(fixture.manifest, options), CodeSourceMismatch)
	})
	t.Run("expected attempt", func(t *testing.T) {
		fixture := newPromotionFixture(t)
		options := fixture.verifyOptions()
		options.RunAttempt++
		assertCode(t, Verify(fixture.manifest, options), CodeSourceMismatch)
	})
	t.Run("missing native proof", func(t *testing.T) {
		fixture := newPromotionFixture(t)
		fixture.manifest.Platforms = fixture.manifest.Platforms[:4]
		assertCode(t, Verify(fixture.manifest, fixture.verifyOptions()), CodePromotionManifestInvalid)
	})
	t.Run("swapped native proofs", func(t *testing.T) {
		fixture := newPromotionFixture(t)
		fixture.manifest.Platforms[0], fixture.manifest.Platforms[1] = fixture.manifest.Platforms[1], fixture.manifest.Platforms[0]
		assertCode(t, Verify(fixture.manifest, fixture.verifyOptions()), CodePromotionManifestInvalid)
	})
	t.Run("duplicate asset", func(t *testing.T) {
		fixture := newPromotionFixture(t)
		fixture.manifest.Assets[1] = fixture.manifest.Assets[0]
		assertCode(t, Verify(fixture.manifest, fixture.verifyOptions()), CodeArtifactInventoryInvalid)
	})
	t.Run("matrix leak result", func(t *testing.T) {
		fixture := newPromotionFixture(t)
		fixture.manifest.E2EMatrix.Proof.PlatformEvidence[0].Leak.Detected = true
		assertCode(t, Verify(fixture.manifest, fixture.verifyOptions()), CodePromotionManifestInvalid)
	})
	t.Run("matrix leak raw digest", func(t *testing.T) {
		fixture := newPromotionFixture(t)
		fixture.manifest.E2EMatrix.Proof.PlatformEvidence[0].LeakSHA256 = strings.Repeat("2", 64)
		assertCode(t, Verify(fixture.manifest, fixture.verifyOptions()), CodePromotionManifestInvalid)
	})
	t.Run("matrix archive binding", func(t *testing.T) {
		fixture := newPromotionFixture(t)
		platform := &fixture.manifest.E2EMatrix.Proof.PlatformEvidence[0]
		platform.Artifact.SHA256 = strings.Repeat("3", 64)
		if err := e2ebaseline.SealPlatformProof(platform); err != nil {
			t.Fatal(err)
		}
		assertCode(t, Verify(fixture.manifest, fixture.verifyOptions()), CodeDigestMismatch)
	})
	t.Run("source-quality embedded file binding", func(t *testing.T) {
		fixture := newPromotionFixture(t)
		fixture.manifest.SourceQuality.File.SHA256 = strings.Repeat("0", 64)
		var err error
		fixture.manifest.ManifestSHA256, err = ManifestSHA256(fixture.manifest)
		if err != nil {
			t.Fatal(err)
		}
		assertCode(t, Verify(fixture.manifest, fixture.verifyOptions()), CodeDigestMismatch)
	})
	t.Run("E2E matrix embedded file binding", func(t *testing.T) {
		fixture := newPromotionFixture(t)
		fixture.manifest.E2EMatrix.File.Size++
		var err error
		fixture.manifest.ManifestSHA256, err = ManifestSHA256(fixture.manifest)
		if err != nil {
			t.Fatal(err)
		}
		assertCode(t, Verify(fixture.manifest, fixture.verifyOptions()), CodeDigestMismatch)
	})
	t.Run("changed artifact bytes", func(t *testing.T) {
		fixture := newPromotionFixture(t)
		name := fixture.contract.Platforms[0].Archive
		writeTestFile(t, filepath.Join(fixture.artifactsRoot, name), []byte("changed"))
		assertCode(t, Verify(fixture.manifest, fixture.verifyOptions()), CodeDigestMismatch)
	})
	t.Run("extra artifact", func(t *testing.T) {
		fixture := newPromotionFixture(t)
		writeTestFile(t, filepath.Join(fixture.artifactsRoot, "unexpected"), []byte("x"))
		assertCode(t, Verify(fixture.manifest, fixture.verifyOptions()), CodeArtifactInventoryInvalid)
	})
	t.Run("symlink artifact", func(t *testing.T) {
		fixture := newPromotionFixture(t)
		name := fixture.contract.Platforms[0].Archive
		path := filepath.Join(fixture.artifactsRoot, name)
		target := filepath.Join(fixture.root, "outside-archive")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, target, data)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		assertCode(t, Verify(fixture.manifest, fixture.verifyOptions()), CodeArtifactInventoryInvalid)
	})
}

func TestAssembleRequiresActualSameAttemptProofFiles(t *testing.T) {
	t.Run("missing source quality file", func(t *testing.T) {
		fixture := newPromotionFixture(t)
		options := fixture.assembleOptions()
		options.SourceQualityPath = filepath.Join(fixture.root, "missing.json")
		_, err := Assemble(options)
		assertCode(t, err, CodePromotionManifestInvalid)
	})
	t.Run("incomplete source quality result", func(t *testing.T) {
		fixture := newPromotionFixture(t)
		proof, _, err := ReadSourceQualityProof(fixture.sourcePath)
		if err != nil {
			t.Fatal(err)
		}
		delete(proof.Results.Licenses, "windows")
		proof.ProofSHA256, err = SourceQualityProofSHA256(proof)
		if err != nil {
			t.Fatal(err)
		}
		writeJSONFile(t, fixture.sourcePath, proof)
		_, err = Assemble(fixture.assembleOptions())
		assertCode(t, err, CodePromotionManifestInvalid)
	})
	t.Run("synthetic pass cannot replace failed observed job", func(t *testing.T) {
		fixture := newPromotionFixture(t)
		proof, _, err := ReadSourceQualityProof(fixture.sourcePath)
		if err != nil {
			t.Fatal(err)
		}
		proof.ObservedJobs.LicenseMatrix = "failure"
		proof.Results = passingSourceQualityResults()
		proof.Result = "pass"
		if err := SealSourceQualityProof(&proof, fixture.contract); err == nil {
			t.Fatal("failed observed job was normalized into a synthetic pass")
		}
	})
	t.Run("different native attempt", func(t *testing.T) {
		fixture := newPromotionFixture(t)
		proof, _, err := ReadPlatformProof(fixture.proofPaths[0])
		if err != nil {
			t.Fatal(err)
		}
		proof.RunAttempt++
		proof.LiteralVersion.Evidence.RunAttempt++
		proof.LiteralVersion.Evidence.EvidenceSHA256, err = LiteralVersionEvidenceSHA256(proof.LiteralVersion.Evidence)
		if err != nil {
			t.Fatal(err)
		}
		versionBytes, err := MarshalJSON(proof.LiteralVersion.Evidence)
		if err != nil {
			t.Fatal(err)
		}
		versionDigest := sha256.Sum256(versionBytes)
		proof.LiteralVersion.File.Size = int64(len(versionBytes))
		proof.LiteralVersion.File.SHA256 = hex.EncodeToString(versionDigest[:])
		values := map[string]string{"platform": proof.PlatformID, "attempt": fmt.Sprint(proof.RunAttempt)}
		proof.ArtifactName, err = fixture.contract.RenderName(fixture.contract.Naming.PlatformArtifactTemplate, values)
		if err != nil {
			t.Fatal(err)
		}
		proof.ProofArtifactName, err = fixture.contract.RenderName(fixture.contract.Naming.PlatformEvidenceTemplate, values)
		if err != nil {
			t.Fatal(err)
		}
		proof.ProofSHA256, err = PlatformProofSHA256(proof)
		if err != nil {
			t.Fatal(err)
		}
		writeJSONFile(t, fixture.proofPaths[0], proof)
		_, err = Assemble(fixture.assembleOptions())
		assertCode(t, err, CodeSourceMismatch)
	})
	t.Run("different matrix attempt", func(t *testing.T) {
		fixture := newPromotionFixture(t)
		matrix := fixture.manifest.E2EMatrix.Proof
		matrix.Run.RunAttempt = "4"
		for index := range matrix.PlatformEvidence {
			matrix.PlatformEvidence[index].Run = matrix.Run
			if err := e2ebaseline.SealPlatformProof(&matrix.PlatformEvidence[index]); err != nil {
				t.Fatal(err)
			}
		}
		writeJSONFile(t, fixture.matrixPath, matrix)
		_, err := Assemble(fixture.assembleOptions())
		assertCode(t, err, CodeSourceMismatch)
	})
	t.Run("symlink matrix proof", func(t *testing.T) {
		fixture := newPromotionFixture(t)
		link := filepath.Join(fixture.root, "matrix-link.json")
		if err := os.Symlink(fixture.matrixPath, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		options := fixture.assembleOptions()
		options.MatrixProofPath = link
		_, err := Assemble(options)
		assertCode(t, err, CodePromotionManifestInvalid)
	})
}

func TestStrictJSONRejectsUnknownDuplicateAndTrailingValues(t *testing.T) {
	fixture := newPromotionFixture(t)
	data, err := MarshalJSON(fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	trimmed := bytes.TrimSpace(data)
	unknown := append([]byte{}, trimmed[:len(trimmed)-1]...)
	unknown = append(unknown, []byte(`,"unknown":true}`)...)
	if _, err := ParseManifest(unknown); err == nil {
		t.Fatal("unknown JSON field was accepted")
	}
	duplicate := append([]byte(`{"schema_id":"duplicate",`), trimmed[1:]...)
	if _, err := ParseManifest(duplicate); err == nil {
		t.Fatal("duplicate JSON field was accepted")
	}
	caseAlias := append([]byte(`{"SCHEMA_ID":"duplicate",`), trimmed[1:]...)
	if _, err := ParseManifest(caseAlias); err == nil {
		t.Fatal("case-aliased duplicate JSON field was accepted")
	}
	trailing := append(append([]byte{}, trimmed...), []byte(` {}`)...)
	if _, err := ParseManifest(trailing); err == nil {
		t.Fatal("trailing JSON value was accepted")
	}
}

func newPromotionFixture(t *testing.T) *promotionFixture {
	t.Helper()
	contractPath, contract := loadTestContract(t)
	root := t.TempDir()
	fixture := &promotionFixture{
		root: root, contractPath: contractPath, contract: contract,
		artifactsRoot: filepath.Join(root, "artifacts"),
	}
	if err := os.Mkdir(fixture.artifactsRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, platform := range contract.Platforms {
		archiveBytes := []byte("archive bytes for " + platform.ID)
		archivePath := filepath.Join(fixture.artifactsRoot, platform.Archive)
		writeTestFile(t, archivePath, archiveBytes)
		digest := sha256.Sum256(archiveBytes)
		checksumPath := filepath.Join(fixture.artifactsRoot, platform.Checksum)
		writeTestFile(t, checksumPath, []byte(hex.EncodeToString(digest[:])+" *"+platform.Archive+"\n"))
		nativeRoot := filepath.Join(root, "native", platform.ID)
		if err := os.MkdirAll(nativeRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		binaryPath := filepath.Join(nativeRoot, platform.Binary)
		writeTestFile(t, binaryPath, []byte("binary bytes for "+platform.ID))
		proof, err := RecordPlatform(RecordOptions{
			ContractPath: contractPath, PlatformID: platform.ID, SourceSHA: testSource,
			ReleaseVersion: testVersion, Repository: testRepository, RunID: testRunID, RunAttempt: testAttempt,
			ArchivePath: archivePath, ChecksumPath: checksumPath, BinaryPath: binaryPath,
			VersionResultsPath: writeVersionEvidence(t, nativeRoot, platform, binaryPath, testVersion),
		})
		if err != nil {
			t.Fatal(err)
		}
		proofPath := filepath.Join(root, "native-proof-"+platform.ID+".json")
		writeJSONFile(t, proofPath, proof)
		fixture.proofPaths = append(fixture.proofPaths, proofPath)
	}

	workflow, ok := contract.WorkflowByID("ci")
	if !ok {
		t.Fatal("ci workflow is absent")
	}
	sourceProof := SourceQualityProof{
		SchemaID: contract.Schemas["source_quality_proof"], SchemaVersion: SchemaVersion,
		SourceSHA: testSource, ReleaseVersion: testVersion, Repository: testRepository,
		Workflow:     Workflow{ID: workflow.ID, Name: workflow.Name, File: workflow.File, RunID: testRunID, RunAttempt: testAttempt, Event: "push", HeadSHA: testSource},
		ObservedJobs: SourceQualityObservedJobs{SourceQuality: "success", LicenseMatrix: "success"},
	}
	if err := SealSourceQualityProof(&sourceProof, contract); err != nil {
		t.Fatal(err)
	}
	fixture.sourcePath = filepath.Join(root, "source-quality.json")
	writeJSONFile(t, fixture.sourcePath, sourceProof)

	matrix := matrixProofForNativeProofs(t, contract, fixture.proofPaths)
	fixture.matrixPath = filepath.Join(root, "matrix-proof.json")
	writeJSONFile(t, fixture.matrixPath, matrix)
	manifest, err := Assemble(fixture.assembleOptions())
	if err != nil {
		t.Fatal(err)
	}
	fixture.manifest = manifest
	return fixture
}

func TestVerifyChecksumRequiresExactNativeRecord(t *testing.T) {
	archive := FileDigest{
		Name: "env-vault-windows-amd64.zip", Size: 42,
		SHA256: strings.Repeat("a", 64),
	}
	valid := []string{
		archive.SHA256 + "  " + archive.Name,
		archive.SHA256 + "  " + archive.Name + "\n",
		archive.SHA256 + "  " + archive.Name + "\r\n",
		archive.SHA256 + " *" + archive.Name + "\n",
	}
	for _, record := range valid {
		if err := verifyChecksum([]byte(record), archive); err != nil {
			t.Fatalf("valid checksum record %q: %v", record, err)
		}
	}

	invalid := []string{
		archive.SHA256 + "\t" + archive.Name + "\n",
		archive.SHA256 + "   " + archive.Name + "\n",
		archive.SHA256 + "  " + archive.Name + "\r",
		archive.SHA256 + "  " + archive.Name + "\x00\n",
		archive.SHA256 + "  " + archive.Name + "\nextra\n",
		strings.Repeat("b", 64) + "  " + archive.Name + "\n",
		archive.SHA256 + "  other.zip\n",
	}
	for _, record := range invalid {
		if err := verifyChecksum([]byte(record), archive); err == nil {
			t.Fatalf("invalid checksum record accepted: %q", record)
		}
	}
}

func matrixProofForNativeProofs(t *testing.T, contract releasecontract.Contract, paths []string) e2ebaseline.MatrixProof {
	t.Helper()
	run := e2ebaseline.RunIdentity{
		CommitSHA: testSource, RunID: fmt.Sprint(testRunID), RunAttempt: fmt.Sprint(testAttempt),
		Repository: testRepository, RunURL: fmt.Sprintf("https://github.com/%s/actions/runs/%d", testRepository, testRunID),
	}
	matrix := e2ebaseline.MatrixProof{
		SchemaID: e2ebaseline.MatrixProofSchemaID, SchemaVersion: e2ebaseline.MatrixProofSchemaVersion,
		Mode: "validate-matrix", Status: "pass", Phase: "candidate", SuiteHash: strings.Repeat("9", 64),
		Run: run, GeneratedAt: testTime, Checks: []e2ebaseline.ProofCheck{{Name: "deep report validation", Status: "pass"}},
	}
	for index, platform := range contract.Platforms {
		native, _, err := ReadPlatformProof(paths[index])
		if err != nil {
			t.Fatal(err)
		}
		rawEvidence := make(map[string]string)
		for _, name := range e2ebaseline.RequiredProofEvidenceFiles() {
			rawEvidence[name] = strings.Repeat("e", 64)
		}
		evidence := e2ebaseline.PlatformProof{
			ID: platform.ID, Phase: matrix.Phase, Run: run, SuiteHash: matrix.SuiteHash,
			GOOS: platform.GOOS, GOARCH: platform.GOARCH, GoVersion: "go1.26.5", GotestsumVersion: "v1.13.0",
			SubjectKind: "artifact", BinarySHA256: native.Binary.SHA256,
			Artifact:       e2ebaseline.ArtifactProof{Archive: platform.Archive, Checksum: platform.Checksum, Format: platform.ArchiveFormat, SHA256: native.Archive.SHA256, ChecksumVerified: true},
			ContractSHA256: strings.Repeat("d", 64), MetadataSHA256: strings.Repeat("f", 64), LeakSHA256: strings.Repeat("1", 64), EvidenceSHA256: rawEvidence,
			StatementCoveragePercent: 75, Counts: e2ebaseline.Counts{Passed: 1}, ExpectedSkips: []string{},
			CriticalScenarios: []e2ebaseline.ScenarioExpectation{{ID: "CLI_VERSION_FORMS", Result: "pass"}},
			Leak:              e2ebaseline.LeakExpectation{Status: "pass", FilesScanned: 5, RegistryRecords: 1},
		}
		if err := e2ebaseline.SealPlatformProof(&evidence); err != nil {
			t.Fatal(err)
		}
		matrix.Platforms = append(matrix.Platforms, platform.ID)
		matrix.PlatformEvidence = append(matrix.PlatformEvidence, evidence)
	}
	if err := matrix.Validate(contract); err != nil {
		t.Fatal(err)
	}
	return matrix
}

func (fixture *promotionFixture) assembleOptions() AssembleOptions {
	return AssembleOptions{
		ContractPath: fixture.contractPath, SourceSHA: testSource, ReleaseVersion: testVersion,
		Repository: testRepository, RunID: testRunID, RunAttempt: testAttempt,
		SourceQualityPath: fixture.sourcePath, MatrixProofPath: fixture.matrixPath,
		PlatformProofPaths: append([]string(nil), fixture.proofPaths...), CreatedAt: testTime,
	}
}

func (fixture *promotionFixture) verifyOptions() VerifyOptions {
	return VerifyOptions{
		ContractPath: fixture.contractPath, SourceSHA: testSource, ReleaseVersion: testVersion,
		Repository: testRepository, RunID: testRunID, RunAttempt: testAttempt, ArtifactsRoot: fixture.artifactsRoot,
	}
}

func basicRecordOptions(contractPath string, platform releasecontract.Platform, root string) RecordOptions {
	return RecordOptions{
		ContractPath: contractPath, PlatformID: platform.ID, SourceSHA: testSource,
		ReleaseVersion: testVersion, Repository: testRepository, RunID: testRunID, RunAttempt: testAttempt,
		ArchivePath: filepath.Join(root, platform.Archive), ChecksumPath: filepath.Join(root, platform.Checksum), BinaryPath: filepath.Join(root, platform.Binary),
	}
}

func writeVersionEvidence(t *testing.T, root string, platform releasecontract.Platform, binaryPath, version string) string {
	t.Helper()
	binary, err := digestBoundedRegular(binaryPath, maxBinaryBytes)
	if err != nil {
		t.Fatal(err)
	}
	evidence := LiteralVersionEvidence{
		SchemaID: LiteralVersionEvidenceSchema, SchemaVersion: SchemaVersion,
		PlatformID: platform.ID, SourceSHA: testSource, ReleaseVersion: version,
		Repository: testRepository, RunID: testRunID, RunAttempt: testAttempt, Binary: binary,
		Commands: []LiteralVersionCommand{
			{Surface: "flag", Args: []string{"--version"}, Stdout: version + "\n", Stderr: "", ExitCode: 0},
			{Surface: "command", Args: []string{"version"}, Stdout: version + "\n", Stderr: "", ExitCode: 0},
			{Surface: "json", Args: []string{"version", "--json"}, Stdout: fmt.Sprintf(`{"ok":true,"command":"version","timestamp":"%s","data":{"version":%q},"warnings":[],"error":null}`+"\n", testTime.Format(time.RFC3339Nano), version), Stderr: "", ExitCode: 0},
		},
		Results: LiteralVersionResults{Flag: version, Command: version, JSON: version}, Result: "pass",
	}
	evidence.EvidenceSHA256, err = LiteralVersionEvidenceSHA256(evidence)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, platform.ID+"-version-results.json")
	writeJSONFile(t, path, evidence)
	return path
}

func loadTestContract(t *testing.T) (string, releasecontract.Contract) {
	t.Helper()
	path := filepath.Join("..", "..", "release", "contract.v1.json")
	contract, err := releasecontract.LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, contract
}

func writeJSONFile(t *testing.T, filename string, value any) {
	t.Helper()
	data, err := MarshalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filename, data)
}

func writeTestFile(t *testing.T, filename string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || ErrorCode(err) != want {
		t.Fatalf("error=%v code=%q, want %q", err, ErrorCode(err), want)
	}
}
