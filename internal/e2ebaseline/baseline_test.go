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

func TestCanonicalBaselineAndMigrationAreDurable(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	contract, err := releasecontract.LoadCanonical(repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	baselinePath := filepath.Join(repositoryRoot, filepath.FromSlash(CanonicalPath))
	baseline, err := LoadFile(baselinePath, contract)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Provenance.RunID != "29479484474" || baseline.Toolchain.GoVersion != "go1.26.5" || baseline.Toolchain.GotestsumVersion != "v1.13.0" {
		t.Fatalf("baseline identity=%+v toolchain=%+v", baseline.Provenance, baseline.Toolchain)
	}
	if baseline.SemanticSuite.Algorithm != e2esuite.SchemaID || baseline.SemanticSuite.Hash != "6b7f1d8a715e7f8b0f9e75e71f45a139e01deb1804a9d5556ca14071d10ae2f8" {
		t.Fatalf("semantic suite=%+v", baseline.SemanticSuite)
	}
	migrationPath := filepath.Join(repositoryRoot, filepath.FromSlash(CanonicalMigrationPath))
	report, err := VerifyMigration(repositoryRoot, migrationPath, baseline, contract)
	if err != nil || report.Status != "pass" {
		t.Fatalf("migration status=%s err=%v checks=%+v", report.Status, err, report.Checks)
	}
	if report.ComparatorSHA256 != CanonicalComparatorSHA256 || report.MigrationProofSHA256 != baseline.Migration.SHA256 {
		t.Fatalf("migration digests=%+v baseline=%+v", report, baseline.Migration)
	}
	data, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"artifact_id", "artifact_expires_at", "minimum_artifact_expires_at"} {
		if strings.Contains(string(data), forbidden) {
			t.Fatalf("durable baseline contains expiring field %q", forbidden)
		}
	}
}

func TestMigrationFailsClosedOnMalformedOrTamperedEvidence(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	contract, err := releasecontract.LoadCanonical(repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := LoadFile(filepath.Join(repositoryRoot, filepath.FromSlash(CanonicalPath)), contract)
	if err != nil {
		t.Fatal(err)
	}
	migrationPath := filepath.Join(repositoryRoot, filepath.FromSlash(CanonicalMigrationPath))
	proof, proofBytes, err := LoadMigrationProof(migrationPath)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("unknown migration field", func(t *testing.T) {
		malformed := strings.Replace(string(proofBytes), `"status": "pass",`, `"status": "pass", "unknown": true,`, 1)
		filename := filepath.Join(t.TempDir(), "migration.json")
		if err := os.WriteFile(filename, []byte(malformed), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := LoadMigrationProof(filename); err == nil || !strings.Contains(err.Error(), "unknown") || !strings.Contains(err.Error(), "field") {
			t.Fatalf("unknown field accepted: %v", err)
		}
	})

	t.Run("comparator bytes", func(t *testing.T) {
		root := t.TempDir()
		filename := filepath.Join(root, "comparison.json")
		data, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(CanonicalComparatorPath)))
		if err != nil {
			t.Fatal(err)
		}
		data = append(append([]byte(nil), data...), ' ')
		if err := os.WriteFile(filename, data, 0o600); err != nil {
			t.Fatal(err)
		}
		binding := proof.Comparator
		binding.Path = "comparison.json"
		if _, _, err := loadLegacyComparison(root, binding); err == nil || !strings.Contains(err.Error(), "SHA-256") {
			t.Fatalf("tampered comparator accepted: %v", err)
		}
	})

	t.Run("legacy status", func(t *testing.T) {
		comparison, _, err := loadLegacyComparison(repositoryRoot, proof.Comparator)
		if err != nil {
			t.Fatal(err)
		}
		comparison.Checks[0].Status = "unknown"
		if err := validateLegacyComparison(comparison, proof, contract); err == nil {
			t.Fatal("unknown legacy check status accepted")
		}
	})

	t.Run("baseline facts", func(t *testing.T) {
		tampered := baseline
		tampered.Platforms = append([]PlatformBaseline(nil), baseline.Platforms...)
		tampered.Platforms[0].CoverageFloorPercent--
		report, err := VerifyMigration(repositoryRoot, migrationPath, tampered, contract)
		if err == nil || report.Status != "fail" || checkStatus(report.Checks, "baseline_facts") != "fail" {
			t.Fatalf("tampered baseline accepted: status=%s err=%v checks=%+v", report.Status, err, report.Checks)
		}
	})
}

func TestMachineEvidenceJSONRejectsDuplicateAndCaseVariantKeys(t *testing.T) {
	repositoryRoot := filepath.Join("..", "..")
	contract, err := releasecontract.LoadCanonical(repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	suiteHash, err := e2esuite.Hash(repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	matrixBytes, err := Marshal(validProof(t, contract, suiteHash))
	if err != nil {
		t.Fatal(err)
	}
	baselineBytes, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(CanonicalPath)))
	if err != nil {
		t.Fatal(err)
	}
	migrationBytes, err := os.ReadFile(filepath.Join(repositoryRoot, filepath.FromSlash(CanonicalMigrationPath)))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		data []byte
		load func(string) error
	}{
		{
			name: "baseline duplicate",
			data: replaceOnce(t, baselineBytes, `"schema_id": "env-vault.e2e-baseline.v2",`, `"schema_id": "env-vault.e2e-baseline.v2", "schema_id": "env-vault.e2e-baseline.v2",`),
			load: func(path string) error { _, err := LoadFile(path, contract); return err },
		},
		{
			name: "baseline case variant",
			data: replaceOnce(t, baselineBytes, `"schema_id"`, `"Schema_ID"`),
			load: func(path string) error { _, err := LoadFile(path, contract); return err },
		},
		{
			name: "matrix duplicate",
			data: replaceOnce(t, matrixBytes, `"schema_id": "env-vault.e2e-matrix-proof.v1",`, `"schema_id": "env-vault.e2e-matrix-proof.v1", "schema_id": "env-vault.e2e-matrix-proof.v1",`),
			load: func(path string) error { _, err := LoadMatrixProof(path, contract); return err },
		},
		{
			name: "matrix nested map case collision",
			data: replaceFirst(t, matrixBytes, `"junit.xml": "`+strings.Repeat("e", 64)+`"`, `"junit.xml": "`+strings.Repeat("e", 64)+`", "JUNIT.XML": "`+strings.Repeat("e", 64)+`"`),
			load: func(path string) error { _, err := LoadMatrixProof(path, contract); return err },
		},
		{
			name: "migration nested duplicate",
			data: replaceOnce(t, migrationBytes, `"path": "evidence/e2e-baseline-migration/comparison.json",`, `"path": "evidence/e2e-baseline-migration/comparison.json", "path": "evidence/e2e-baseline-migration/comparison.json",`),
			load: func(path string) error { _, _, err := LoadMigrationProof(path); return err },
		},
		{
			name: "migration case variant",
			load: func(path string) error { _, _, err := LoadMigrationProof(path); return err },
		},
	}
	// Build the case-variant migration input separately so the replacement
	// helper still proves the original exact key existed.
	tests[len(tests)-1].data = replaceOnce(t, migrationBytes, `"schema_id"`, `"Schema_ID"`)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "evidence.json")
			if err := os.WriteFile(filename, test.data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := test.load(filename); err == nil || (!strings.Contains(err.Error(), "duplicate") && !strings.Contains(err.Error(), "case") && !strings.Contains(err.Error(), "cased")) {
				t.Fatalf("adversarial JSON accepted or wrong error: %v", err)
			}
		})
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
	if err == nil || report.Status != "fail" || checkStatus(report.Checks, "coverage_floors") != "fail" {
		t.Fatal("coverage regression was accepted")
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

func TestDiffJSONIsReviewableAndDeterministic(t *testing.T) {
	previous := []byte(`{"schema_version":1,"platforms":[{"coverage":60},{"coverage":61}]}`)
	updated := Baseline{SchemaID: SchemaID, SchemaVersion: SchemaVersion, Platforms: []PlatformBaseline{{CoverageFloorPercent: 60}, {CoverageFloorPercent: 62}}}
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
	if !strings.Contains(string(left), `$.platforms[1]`) {
		t.Fatalf("array diff is not granular: %s", left)
	}
}

func validProof(t *testing.T, contract releasecontract.Contract, suiteHash string) MatrixProof {
	t.Helper()
	run := RunIdentity{CommitSHA: strings.Repeat("a", 40), RunID: "123", RunURL: "https://github.com/ildarbinanas-design/env-vault/actions/runs/123", RunAttempt: "1", Repository: "ildarbinanas-design/env-vault"}
	proof := MatrixProof{
		SchemaID: MatrixProofSchemaID, SchemaVersion: MatrixProofSchemaVersion,
		Mode: "validate-matrix", Status: "pass", Phase: "candidate", SuiteHash: suiteHash, Run: run,
		GeneratedAt: time.Date(2026, 7, 16, 0, 0, 0, 0, time.UTC), Checks: []ProofCheck{{Name: "deep report validation", Status: "pass"}},
	}
	for _, platform := range contract.Platforms {
		proof.Platforms = append(proof.Platforms, platform.ID)
		rawEvidence := make(map[string]string, len(requiredProofEvidenceFiles))
		for _, name := range requiredProofEvidenceFiles {
			rawEvidence[name] = strings.Repeat("e", 64)
		}
		evidence := PlatformProof{
			ID: platform.ID, Phase: proof.Phase, Run: run, SuiteHash: suiteHash,
			GOOS: platform.GOOS, GOARCH: platform.GOARCH, GoVersion: "go1.26.5", GotestsumVersion: "v1.13.0",
			SubjectKind: "artifact", BinarySHA256: strings.Repeat("b", 64),
			Artifact:       ArtifactProof{Archive: platform.Archive, Checksum: platform.Checksum, Format: platform.ArchiveFormat, SHA256: strings.Repeat("c", 64), ChecksumVerified: true},
			ContractSHA256: strings.Repeat("d", 64), MetadataSHA256: strings.Repeat("f", 64), LeakSHA256: strings.Repeat("1", 64), EvidenceSHA256: rawEvidence,
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
		ExpectedRunURL: "https://github.com/ildarbinanas-design/env-vault/actions/runs/123", ExpectedRunAttempt: "1", ExpectedRepository: "ildarbinanas-design/env-vault",
	}
}

func checkStatus(checks []VerificationCheck, code string) string {
	for _, check := range checks {
		if check.Code == code {
			return check.Status
		}
	}
	return ""
}

func replaceOnce(t *testing.T, data []byte, old, updated string) []byte {
	t.Helper()
	if strings.Count(string(data), old) != 1 {
		t.Fatalf("fixture occurrence count for %q = %d, want 1", old, strings.Count(string(data), old))
	}
	return []byte(strings.Replace(string(data), old, updated, 1))
}

func replaceFirst(t *testing.T, data []byte, old, updated string) []byte {
	t.Helper()
	if !strings.Contains(string(data), old) {
		t.Fatalf("fixture does not contain %q", old)
	}
	return []byte(strings.Replace(string(data), old, updated, 1))
}
