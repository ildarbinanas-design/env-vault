package releaseevidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
	"github.com/ildarbinanas-design/env-vault/internal/releasepromotion"
)

const testSHA = "0123456789abcdef0123456789abcdef01234567"

func TestValidateImplementationEvidence(t *testing.T) {
	evidence := validEvidence()
	if err := evidence.Validate(); err != nil {
		t.Fatal(err)
	}
	evidence.Checks[0].Result = "maybe"
	if err := evidence.Validate(); err == nil {
		t.Fatal("unknown check result was accepted")
	}
}

func TestPublishedReleaseRequiresFullExactSourceEvidence(t *testing.T) {
	contract, err := releasecontract.LoadCanonical(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	evidence := validEvidence()
	evidence.Release = ReleaseResult{
		Status: "published", Version: "v1.2.3", SourceSHA: testSHA, TagSHA: testSHA, GitHubRelease: "present",
		Assets:       make([]Asset, 10),
		Attestations: []Attestation{{Kind: "provenance", SourceSHA: testSHA, Workflow: "build-binaries.yml", RunID: 2, RunAttempt: 1}},
		Homebrew: &Homebrew{
			State: "complete", PullRequest: 4, PRHeadSHA: testSHA, MergeSHA: testSHA, TapVersion: "1.2.3",
			PRHeadCI:       RunReference{RunID: 5, RunAttempt: 1, HeadSHA: testSHA, Conclusion: "success"},
			PostMergeTapCI: RunReference{RunID: 6, RunAttempt: 1, HeadSHA: testSHA, Conclusion: "success"},
		},
	}
	var archiveSubjects []string
	for index, name := range contract.Assets {
		digest := fmt.Sprintf("%064x", index+1)
		evidence.Release.Assets[index] = Asset{Name: name, SHA256: digest, SourceSHA: testSHA}
		for _, platform := range contract.Platforms {
			if name == platform.Archive {
				archiveSubjects = append(archiveSubjects, digest)
			}
		}
	}
	evidence.Release.Attestations[0].SubjectSHA256s = append([]string(nil), archiveSubjects...)
	promotion := testPromotionManifest(t, contract, evidence.Release.Assets)
	evidence.Release.Promotion = &promotion
	if err := evidence.Validate(contract); err == nil {
		t.Fatal("published release without SBOM attestation was accepted")
	}
	evidence.Release.Attestations = append(evidence.Release.Attestations, Attestation{Kind: "sbom", SubjectSHA256s: append([]string(nil), archiveSubjects...), SourceSHA: testSHA, Workflow: "build-binaries.yml", RunID: 2, RunAttempt: 1})
	if err := evidence.Validate(contract); err != nil {
		t.Fatal(err)
	}
	checksumDigest := evidence.Release.Assets[1].SHA256
	for _, attestation := range evidence.Release.Attestations {
		for _, subject := range attestation.SubjectSHA256s {
			if subject == checksumDigest {
				t.Fatal("checksum digest unexpectedly required as an attestation subject")
			}
		}
	}
	evidence.Release.Assets[0].Name = "unexpected.tar.gz"
	if err := evidence.Validate(contract); err == nil {
		t.Fatal("published asset set that drifted from the contract was accepted")
	}
}

func testPromotionManifest(t *testing.T, contract releasecontract.Contract, assets []Asset) releasepromotion.Manifest {
	t.Helper()
	byName := map[string]string{}
	for _, asset := range assets {
		byName[asset.Name] = asset.SHA256
	}
	manifest := releasepromotion.Manifest{
		SchemaID: releasepromotion.ManifestSchemaID, SchemaVersion: 1, SourceSHA: testSHA, ReleaseVersion: "v1.2.3", Repository: "owner/repository",
		Workflow:       releasepromotion.Workflow{ID: "ci", Name: "ci", File: "ci.yml", RunID: 11, RunAttempt: 1, Event: "push", HeadSHA: testSHA},
		ContractSchema: releasecontract.SchemaID, ContractSHA256: strings.Repeat("a", 64), SuiteHash: strings.Repeat("b", 64),
		SourceQuality: releasepromotion.SourceQuality{Module: "pass", Test: "pass", Vet: "pass", Smoke: "pass", Race: "pass", Licenses: map[string]string{"linux": "pass", "darwin": "pass", "windows": "pass"}},
		CreatedAt:     time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC).Format(time.RFC3339), Result: "pass",
	}
	for _, platform := range contract.Platforms {
		manifest.Platforms = append(manifest.Platforms, releasepromotion.PlatformEvidence{
			SchemaID: releasepromotion.PlatformSchemaID, PlatformID: platform.ID, GOOS: platform.GOOS, GOARCH: platform.GOARCH,
			SourceSHA: testSHA, ReleaseVersion: "v1.2.3", Repository: "owner/repository", RunID: 11, RunAttempt: 1,
			ArtifactName: "env-vault-release-" + platform.ID + "-attempt-1", E2EArtifact: "env-vault-e2e-candidate-" + platform.ID + "-attempt-1",
			Archive: releasepromotion.FileDigest{Name: platform.Archive, SHA256: byName[platform.Archive]}, Checksum: releasepromotion.FileDigest{Name: platform.Checksum, SHA256: byName[platform.Checksum]},
			BinarySHA256: strings.Repeat("c", 64), SuiteHash: manifest.SuiteHash, Metadata: releasepromotion.FileDigest{Name: "metadata.json", SHA256: strings.Repeat("d", 64)},
			LiteralVersion: releasepromotion.LiteralVersionResults{Flag: "pass", Command: "pass", JSON: "pass"}, Contracts: releasepromotion.ContractEvidence{SHA256: strings.Repeat("e", 64), Count: 1},
			Coverage: releasepromotion.CoverageEvidence{SHA256: strings.Repeat("f", 64), StatementPercent: 90, FloorPercent: 80, CriticalCovered: 2, CriticalTotal: 2},
			Leak:     releasepromotion.LeakEvidence{SHA256: strings.Repeat("1", 64), Status: "pass", RegistryRecords: 1}, GoVersion: "go1.26.5", BinaryGo: "go1.26.5", Gotestsum: "v1", Result: "pass",
		})
	}
	copy := manifest
	copy.ManifestSHA256 = ""
	data, err := json.Marshal(copy)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	manifest.ManifestSHA256 = hex.EncodeToString(digest[:])
	return manifest
}

func TestIndexIsGeneratedFromValidatedJSON(t *testing.T) {
	directory := t.TempDir()
	data := `{
  "schema_id": "env-vault.release-evidence.v1",
  "generated_at": "2026-07-16T08:00:00Z",
  "task_id": "release-refactor",
  "objective_code": "release-pipeline-determinism",
  "repository": {"name":"owner/repository","before_sha":"0123456789abcdef0123456789abcdef01234567","after_sha":"0123456789abcdef0123456789abcdef01234567"},
  "release_result": {"status":"implementation_only","github_release":"not_checked","assets":[],"attestations":[]},
  "change_codes": ["machine-evidence"],
  "checks": [{"id":"go-test","command":"go test ./...","result":"pass","source":"local"}],
  "guarantees": [{"id":"immutable-tags","status":"preserved","evidence":"github-api"}],
  "workflow_runs": [],
  "residual_risks": []
}`
	if err := os.WriteFile(filepath.Join(directory, "task.evidence.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	index, err := Index(directory)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "task.evidence.json") || !strings.Contains(string(index), "release-refactor") {
		t.Fatalf("index does not contain the validated file/task tuple: %s", index)
	}
}

func validEvidence() Evidence {
	return Evidence{
		SchemaID: SchemaID, GeneratedAt: "2026-07-16T08:00:00Z", TaskID: "release-refactor", ObjectiveCode: "release-pipeline-determinism",
		Repository:   Repository{Name: "owner/repository", BeforeSHA: testSHA, AfterSHA: testSHA},
		Release:      ReleaseResult{Status: "implementation_only", GitHubRelease: "not_checked", Assets: []Asset{}, Attestations: []Attestation{}},
		Changes:      []string{"machine-evidence"},
		Checks:       []Check{{ID: "go-test", Command: "go test ./...", Result: "pass", Source: "local"}},
		Guarantees:   []Guarantee{{ID: "immutable-tags", Status: "preserved", Evidence: "github-api"}},
		WorkflowRuns: []WorkflowRun{}, ResidualRisks: []ResidualRisk{},
	}
}
