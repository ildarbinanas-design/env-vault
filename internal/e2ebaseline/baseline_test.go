package e2ebaseline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/e2esuite"
	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

func TestCanonicalBaselineIsValidAndDurable(t *testing.T) {
	contract, err := releasecontract.LoadCanonical(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadFile(filepath.Join("..", "..", filepath.FromSlash(CanonicalPath)), contract)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Provenance.RunID != "29479484474" || baseline.Toolchain.GoVersion != "go1.26.5" || baseline.Toolchain.GotestsumVersion != "v1.13.0" {
		t.Fatalf("baseline identity=%+v toolchain=%+v", baseline.Provenance, baseline.Toolchain)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(CanonicalPath)))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"artifact_id", "artifact_expires_at", "minimum_artifact_expires_at"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("durable baseline contains expiring field %q", forbidden)
		}
	}
}

func TestDiffJSONIsReviewableAndDeterministic(t *testing.T) {
	previous := []byte(`{"schema_version":1,"platforms":{"linux":{"coverage":60}}}`)
	updated := Baseline{SchemaID: SchemaID, SchemaVersion: SchemaVersion}
	first, err := DiffJSON(previous, updated)
	if err != nil {
		t.Fatal(err)
	}
	second, err := DiffJSON(previous, updated)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := json.Marshal(first)
	right, _ := json.Marshal(second)
	if string(left) != string(right) || len(first.Changes) == 0 || first.PreviousDigest == first.UpdatedDigest {
		t.Fatalf("diff is not deterministic/reviewable: %+v", first)
	}
}

func TestGenerateAndVerifyUseOnlySealedProof(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	contract, err := releasecontract.LoadCanonical(repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	suiteHash, err := e2esuite.Hash(repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	proofPath := writeProofFixture(t, root, validProof(t, contract, suiteHash))
	// A malformed raw report beside the proof demonstrates that neither
	// generation nor verification discovers/parses the report tree again.
	if err := os.MkdirAll(filepath.Join(root, "raw-report"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "raw-report", "metadata.json"), []byte("not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	baseline, err := Generate(GenerateOptions{ProofPath: proofPath, RepositoryRoot: repositoryRoot}, contract)
	if err != nil {
		t.Fatal(err)
	}
	report, err := Verify(validVerifyOptions(proofPath, repositoryRoot), baseline, contract)
	if err != nil || report.Status != "pass" || report.MatrixProofDigest == "" {
		t.Fatalf("verify status=%s proof=%q err=%v checks=%+v", report.Status, report.MatrixProofDigest, err, report.Checks)
	}
	baseline.Platforms[0].CoverageFloorPercent = 99
	report, err = Verify(validVerifyOptions(proofPath, repositoryRoot), baseline, contract)
	if err == nil || report.Status != "fail" {
		t.Fatal("coverage regression was accepted")
	}
}

func TestSourceProofUsesHistoricalSuiteHash(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	contract, err := releasecontract.LoadCanonical(repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	suiteHash, err := e2esuite.Hash(repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	proofPath := writeProofFixture(t, t.TempDir(), validProof(t, contract, suiteHash))
	baseline, err := Generate(GenerateOptions{ProofPath: proofPath, RepositoryRoot: repositoryRoot}, contract)
	if err != nil {
		t.Fatal(err)
	}
	baseline.SemanticSuite.Hash = strings.Repeat("d", 64)
	baseline.SemanticSuite.SourceReportHash = suiteHash
	baseline.SemanticSuite.TransitionCode = ReviewedSuiteTransition

	options := validVerifyOptions(proofPath, repositoryRoot)
	options.SourceProof = true
	report, err := Verify(options, baseline, contract)
	if err != nil || report.Status != "pass" {
		t.Fatalf("source proof status=%s err=%v checks=%+v", report.Status, err, report.Checks)
	}
	options.SourceProof = false
	if _, err := Verify(options, baseline, contract); err == nil {
		t.Fatal("ordinary verification accepted proof from the historical source suite")
	}
}

func TestMatrixProofRejectsNormalizedEvidenceTampering(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	contract, err := releasecontract.LoadCanonical(repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	suiteHash, err := e2esuite.Hash(repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	proof := validProof(t, contract, suiteHash)
	proof.PlatformEvidence[0].ContractSHA256 = strings.Repeat("f", 64)
	path := writeProofFixture(t, t.TempDir(), proof)
	if _, err := LoadMatrixProof(path, contract); err == nil || !strings.Contains(err.Error(), "normalized evidence digest") {
		t.Fatalf("normalized proof tampering was accepted: %v", err)
	}
}

func TestMatrixProofStrictSchemaRejectsRawReportFields(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	contract, err := releasecontract.LoadCanonical(repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	suiteHash, err := e2esuite.Hash(repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	data, err := Marshal(validProof(t, contract, suiteHash))
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"mode": "validate-matrix",`, `"mode": "validate-matrix", "reports_root": "raw",`, 1))
	path := filepath.Join(t.TempDir(), "matrix-validation.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMatrixProof(path, contract); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown raw-report field was accepted: %v", err)
	}
}

func validProof(t *testing.T, contract releasecontract.Contract, suiteHash string) MatrixProof {
	t.Helper()
	run := RunIdentity{
		CommitSHA: strings.Repeat("a", 40), RunID: "123",
		RunURL:     "https://github.com/ildarbinanas-design/env-vault/actions/runs/123",
		RunAttempt: "1", Repository: "ildarbinanas-design/env-vault",
	}
	proof := MatrixProof{
		SchemaID: MatrixProofSchemaID, SchemaVersion: MatrixProofSchemaVersion,
		Mode: "validate-matrix", Status: "pass", Phase: "candidate", SuiteHash: suiteHash, Run: run,
		GeneratedAt: time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC),
		Checks:      []ProofCheck{{Name: "deep report validation", Status: "pass"}},
	}
	rawEvidence := make(map[string]string, len(requiredProofEvidenceFiles))
	for _, name := range requiredProofEvidenceFiles {
		rawEvidence[name] = strings.Repeat("e", 64)
	}
	for _, platform := range contract.Platforms {
		proof.Platforms = append(proof.Platforms, platform.ID)
		evidence := PlatformProof{
			ID: platform.ID, Phase: proof.Phase, Run: run, SuiteHash: suiteHash,
			GOOS: platform.GOOS, GOARCH: platform.GOARCH, GoVersion: "go1.26.5", GotestsumVersion: "v1.13.0",
			SubjectKind: "artifact", BinarySHA256: strings.Repeat("b", 64),
			Artifact:       ArtifactProof{Archive: platform.Archive, Checksum: platform.Checksum, Format: platform.ArchiveFormat, SHA256: strings.Repeat("c", 64), ChecksumVerified: true},
			ContractSHA256: strings.Repeat("d", 64), EvidenceSHA256: rawEvidence,
			StatementCoveragePercent: 70, Counts: Counts{Passed: 1}, ExpectedSkips: []string{},
			CriticalScenarios: []ScenarioExpectation{{ID: "SCENARIO", Result: "pass"}},
			Leak:              LeakExpectation{Status: "pass", FilesScanned: 1, RegistryRecords: 1},
		}
		if err := SealPlatformProof(&evidence); err != nil {
			t.Fatal(err)
		}
		proof.PlatformEvidence = append(proof.PlatformEvidence, evidence)
	}
	return proof
}

func writeProofFixture(t *testing.T, root string, proof MatrixProof) string {
	t.Helper()
	filename := filepath.Join(root, "matrix-validation.json")
	if err := WriteFile(filename, proof); err != nil {
		t.Fatal(err)
	}
	return filename
}

func validVerifyOptions(proofPath, repositoryRoot string) VerifyOptions {
	return VerifyOptions{
		ProofPath: proofPath, RepositoryRoot: repositoryRoot, Phase: "candidate",
		ExpectedCommit: strings.Repeat("a", 40), ExpectedRunID: "123",
		ExpectedRunURL:     "https://github.com/ildarbinanas-design/env-vault/actions/runs/123",
		ExpectedRunAttempt: "1", ExpectedRepository: "ildarbinanas-design/env-vault",
	}
}
