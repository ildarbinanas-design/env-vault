package e2ebaseline

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

const (
	MatrixProofSchemaID      = "env-vault.e2e-matrix-proof.v1"
	MatrixProofSchemaVersion = 1
)

var requiredProofEvidenceFiles = []string{
	"junit.xml", "raw-test.jsonl", "feature-coverage.json", "feature-coverage.md",
	"contracts.json", "coverage.out", "coverage.txt", "coverage.html", "coverage-percent.txt",
	"coverage-junit.xml", "coverage-raw-test.jsonl", "burn-in.jsonl", "locking-burn-in.jsonl",
	"sanitized-failure-bundle/command-output.txt",
}

func RequiredProofEvidenceFiles() []string {
	return append([]string(nil), requiredProofEvidenceFiles...)
}

// MatrixProof is the durable boundary between deep report validation and
// baseline comparison. The E2E runner validates raw reports once and seals the
// normalized evidence below; baseline commands never reinterpret old reports.
type MatrixProof struct {
	SchemaID         string          `json:"schema_id"`
	SchemaVersion    int             `json:"schema_version"`
	Mode             string          `json:"mode"`
	Status           string          `json:"status"`
	Phase            string          `json:"phase"`
	SuiteHash        string          `json:"suite_hash"`
	Run              RunIdentity     `json:"run"`
	Platforms        []string        `json:"platforms"`
	GeneratedAt      time.Time       `json:"generated_at"`
	Checks           []ProofCheck    `json:"checks"`
	PlatformEvidence []PlatformProof `json:"platform_evidence"`
}

type RunIdentity struct {
	CommitSHA  string `json:"commit_sha"`
	RunID      string `json:"run_id"`
	RunURL     string `json:"run_url"`
	RunAttempt string `json:"run_attempt"`
	Repository string `json:"repository"`
}

type ProofCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type PlatformProof struct {
	ID                       string                `json:"id"`
	Phase                    string                `json:"phase"`
	Run                      RunIdentity           `json:"run"`
	SuiteHash                string                `json:"suite_hash"`
	GOOS                     string                `json:"goos"`
	GOARCH                   string                `json:"goarch"`
	GoVersion                string                `json:"go_version"`
	GotestsumVersion         string                `json:"gotestsum_version"`
	SubjectKind              string                `json:"subject_kind"`
	BinarySHA256             string                `json:"binary_sha256"`
	Artifact                 ArtifactProof         `json:"artifact"`
	ContractSHA256           string                `json:"contract_sha256"`
	MetadataSHA256           string                `json:"metadata_sha256"`
	LeakSHA256               string                `json:"leak_sha256"`
	EvidenceSHA256           map[string]string     `json:"evidence_sha256"`
	NormalizedEvidenceSHA256 string                `json:"normalized_evidence_sha256"`
	StatementCoveragePercent float64               `json:"statement_coverage_percent"`
	Counts                   Counts                `json:"counts"`
	ExpectedSkips            []string              `json:"expected_skips"`
	CriticalScenarios        []ScenarioExpectation `json:"critical_scenarios"`
	Leak                     LeakExpectation       `json:"leak"`
}

type ArtifactProof struct {
	Archive          string `json:"archive"`
	Checksum         string `json:"checksum"`
	Format           string `json:"format"`
	SHA256           string `json:"sha256"`
	ChecksumVerified bool   `json:"checksum_verified"`
}

func SealPlatformProof(proof *PlatformProof) error {
	digest, err := PlatformProofDigest(*proof)
	if err != nil {
		return err
	}
	proof.NormalizedEvidenceSHA256 = digest
	return nil
}

func PlatformProofDigest(proof PlatformProof) (string, error) {
	proof.NormalizedEvidenceSHA256 = ""
	return Digest(proof)
}

func LoadMatrixProof(filename string, contract releasecontract.Contract) (MatrixProof, error) {
	data, err := readBoundedRegular(filename, maxJSONBytes)
	if err != nil {
		return MatrixProof{}, fmt.Errorf("read matrix proof: %w", err)
	}
	var proof MatrixProof
	if err := decodeStrict(data, &proof); err != nil {
		return MatrixProof{}, fmt.Errorf("decode matrix proof: %w", err)
	}
	return proof, proof.Validate(contract)
}

func (proof MatrixProof) Validate(contract releasecontract.Contract) error {
	var problems []string
	add := func(format string, values ...any) { problems = append(problems, fmt.Sprintf(format, values...)) }
	if proof.SchemaID != MatrixProofSchemaID || proof.SchemaVersion != MatrixProofSchemaVersion {
		add("schema must be %s version %d", MatrixProofSchemaID, MatrixProofSchemaVersion)
	}
	if proof.Mode != "validate-matrix" || proof.Status != "pass" || (proof.Phase != "candidate" && proof.Phase != "baseline") || proof.GeneratedAt.IsZero() {
		add("matrix mode, status, phase, or generation time is invalid")
	}
	if !validSHA256(proof.SuiteHash) {
		add("matrix suite hash is invalid")
	}
	if err := validateRunIdentity(proof.Run); err != nil {
		add("matrix run identity: %v", err)
	}
	if len(proof.Checks) == 0 {
		add("matrix checks are empty")
	}
	seenChecks := make(map[string]bool)
	for _, check := range proof.Checks {
		if check.Name == "" || seenChecks[check.Name] || check.Status != "pass" || check.Detail != "" {
			add("matrix check %q is duplicate or did not pass cleanly", check.Name)
		}
		seenChecks[check.Name] = true
	}
	if len(proof.Platforms) != len(contract.Platforms) || len(proof.PlatformEvidence) != len(contract.Platforms) {
		add("matrix platform count=%d evidence count=%d, want %d", len(proof.Platforms), len(proof.PlatformEvidence), len(contract.Platforms))
	}
	commonGoVersion, commonReporterVersion := "", ""
	for index, declared := range contract.Platforms {
		if index >= len(proof.Platforms) || index >= len(proof.PlatformEvidence) {
			break
		}
		if proof.Platforms[index] != declared.ID {
			add("matrix platform %d=%q, want %q", index, proof.Platforms[index], declared.ID)
		}
		actual := proof.PlatformEvidence[index]
		if actual.ID != declared.ID || actual.GOOS != declared.GOOS || actual.GOARCH != declared.GOARCH {
			add("platform evidence %d does not match release target %s", index, declared.ID)
		}
		if actual.Phase != proof.Phase || actual.Run != proof.Run || actual.SuiteHash != proof.SuiteHash {
			add("platform %s does not share matrix phase/source/run/suite identity", declared.ID)
		}
		if !goPattern.MatchString(actual.GoVersion) || !reporterPattern.MatchString(actual.GotestsumVersion) || actual.SubjectKind != "artifact" || !validSHA256(actual.BinarySHA256) || !validSHA256(actual.ContractSHA256) || !validSHA256(actual.MetadataSHA256) || !validSHA256(actual.LeakSHA256) {
			add("platform %s toolchain, subject, binary, contract, metadata, or leak identity is invalid", declared.ID)
		}
		if index == 0 {
			commonGoVersion, commonReporterVersion = actual.GoVersion, actual.GotestsumVersion
		} else if actual.GoVersion != commonGoVersion || actual.GotestsumVersion != commonReporterVersion {
			add("platform %s does not share the matrix toolchain identity", declared.ID)
		}
		if actual.Artifact.Archive != declared.Archive || actual.Artifact.Checksum != declared.Checksum || actual.Artifact.Format != declared.ArchiveFormat || !validSHA256(actual.Artifact.SHA256) || !actual.Artifact.ChecksumVerified {
			add("platform %s archive/checksum proof differs from release contract", declared.ID)
		}
		if len(actual.EvidenceSHA256) != len(requiredProofEvidenceFiles) {
			add("platform %s raw evidence digest count=%d, want %d", declared.ID, len(actual.EvidenceSHA256), len(requiredProofEvidenceFiles))
		}
		for _, name := range requiredProofEvidenceFiles {
			if !validSHA256(actual.EvidenceSHA256[name]) {
				add("platform %s required raw evidence digest %q is missing or invalid", declared.ID, name)
			}
		}
		for name, digest := range actual.EvidenceSHA256 {
			clean := filepath.Clean(filepath.FromSlash(name))
			if name == "" || strings.Contains(name, `\`) || filepath.IsAbs(name) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || !validSHA256(digest) {
				add("platform %s has invalid raw evidence digest %q", declared.ID, name)
			}
		}
		wantDigest, err := PlatformProofDigest(actual)
		if err != nil || actual.NormalizedEvidenceSHA256 != wantDigest || !validSHA256(actual.NormalizedEvidenceSHA256) {
			add("platform %s normalized evidence digest is invalid", declared.ID)
		}
		if math.IsNaN(actual.StatementCoveragePercent) || math.IsInf(actual.StatementCoveragePercent, 0) || actual.StatementCoveragePercent <= 0 || actual.StatementCoveragePercent > 100 {
			add("platform %s statement coverage is invalid", declared.ID)
		}
		if actual.Counts.Passed <= 0 || actual.Counts.Failed != 0 || actual.Counts.Missing != 0 || actual.Counts.Skipped != len(actual.ExpectedSkips) || !sortedUnique(actual.ExpectedSkips) {
			add("platform %s counts or expected skips do not represent a complete passing run", declared.ID)
		}
		if len(actual.CriticalScenarios) == 0 {
			add("platform %s critical scenario evidence is empty", declared.ID)
		}
		seenScenarios := make(map[string]bool)
		var scenarioSkips []string
		for scenarioIndex, scenario := range actual.CriticalScenarios {
			if scenario.ID == "" || seenScenarios[scenario.ID] || (scenario.Result != "pass" && scenario.Result != "expected_skip") {
				add("platform %s has invalid critical scenario %q", declared.ID, scenario.ID)
			}
			if scenarioIndex > 0 && actual.CriticalScenarios[scenarioIndex-1].ID >= scenario.ID {
				add("platform %s critical scenarios are not sorted", declared.ID)
			}
			seenScenarios[scenario.ID] = true
			if scenario.Result == "expected_skip" {
				scenarioSkips = append(scenarioSkips, scenario.ID)
			}
		}
		sort.Strings(scenarioSkips)
		if !equalStrings(scenarioSkips, actual.ExpectedSkips) {
			add("platform %s expected skips differ from critical scenario results", declared.ID)
		}
		if actual.Leak.Status != "pass" || actual.Leak.Detected || actual.Leak.FilesScanned <= 0 || actual.Leak.Occurrences != 0 || actual.Leak.RegistryRecords <= 0 || actual.Leak.Findings != 0 {
			add("platform %s leak evidence does not fail closed", declared.ID)
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New("invalid E2E matrix proof: " + strings.Join(problems, "; "))
	}
	return nil
}

func validateRunIdentity(identity RunIdentity) error {
	if !commitPattern.MatchString(identity.CommitSHA) || !positiveInteger(identity.RunID) || !positiveInteger(identity.RunAttempt) || !validRepository(identity.Repository) {
		return errors.New("repository, commit, run, or attempt is malformed")
	}
	wantURL := "https://github.com/" + identity.Repository + "/actions/runs/" + identity.RunID
	if identity.RunURL != wantURL {
		return errors.New("run URL does not match repository and run ID")
	}
	return nil
}
