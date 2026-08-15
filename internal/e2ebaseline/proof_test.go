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

// encoding/json fails open on duplicate object keys and matches struct fields
// case-insensitively. The strict decoder in codec.go closes both, and this
// proves it stays closed for the sealed matrix proof.
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
	matrixBytes, err := marshalIndented(validProof(t, contract, suiteHash))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "duplicate key",
			data: replaceOnce(t, matrixBytes,
				`"schema_id": "env-vault.e2e-matrix-proof.v1",`,
				`"schema_id": "env-vault.e2e-matrix-proof.v1", "schema_id": "env-vault.e2e-matrix-proof.v1",`),
		},
		{
			name: "nested map case collision",
			data: replaceFirst(t, matrixBytes,
				`"junit.xml": "`+strings.Repeat("e", 64)+`"`,
				`"junit.xml": "`+strings.Repeat("e", 64)+`", "JUNIT.XML": "`+strings.Repeat("e", 64)+`"`),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			filename := filepath.Join(t.TempDir(), "evidence.json")
			if err := os.WriteFile(filename, test.data, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadMatrixProof(filename, contract)
			if err == nil {
				t.Fatal("adversarial JSON was accepted")
			}
			message := err.Error()
			if !strings.Contains(message, "duplicate") && !strings.Contains(message, "case") && !strings.Contains(message, "cased") {
				t.Fatalf("wrong error for adversarial JSON: %v", err)
			}
		})
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

func marshalIndented(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func writeProofFixture(t *testing.T, root string, proof MatrixProof) string {
	t.Helper()
	filename := filepath.Join(root, "matrix-validation.json")
	data, err := marshalIndented(proof)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return filename
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
