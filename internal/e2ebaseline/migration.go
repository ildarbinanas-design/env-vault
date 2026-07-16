package e2ebaseline

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/e2esuite"
	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

const (
	MigrationSchemaID           = "env-vault.e2e-baseline-migration.v1"
	MigrationSchemaVersion      = 1
	MigrationVerificationSchema = "env-vault.e2e-baseline-migration-verification.v1"
	CanonicalMigrationPath      = "evidence/e2e-baseline-migration/migration.json"
	CanonicalComparatorPath     = "evidence/e2e-baseline-migration/comparison.json"
	CanonicalTransitionPatch    = "evidence/e2e-baseline-migration/independent-sentinel.patch"
	LegacySuiteAlgorithm        = "env-vault.e2e-whole-tree.v1"
	CanonicalComparatorSHA256   = "84a9f5f9d2e6b129f7dc1338db3c5d0b7fabd6577b62d6d2048a02a01dbcf293"
)

var requiredLegacyChecks = []string{
	"baseline matrix validation",
	"candidate matrix validation",
	"baseline reports",
	"candidate reports",
	"platform set",
	"suite and run identity",
	"critical scenarios and results",
	"public CLI contracts",
	"statement coverage non-regression",
	"secret leak gates",
}

type MigrationProof struct {
	SchemaID            string             `json:"schema_id"`
	SchemaVersion       int                `json:"schema_version"`
	Status              string             `json:"status"`
	Comparator          ComparisonArtifact `json:"comparator"`
	Transition          SuiteTransition    `json:"transition"`
	BaselineFactsSHA256 string             `json:"baseline_facts_sha256"`
}

type ComparisonArtifact struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Name       string `json:"name"`
	RunID      string `json:"run_id"`
	RunAttempt string `json:"run_attempt"`
}

type SuiteTransition struct {
	Code              string `json:"code"`
	SourceAlgorithm   string `json:"source_algorithm"`
	SourceSuiteHash   string `json:"source_suite_hash"`
	TargetAlgorithm   string `json:"target_algorithm"`
	TargetSuiteHash   string `json:"target_suite_hash"`
	ChangedFile       string `json:"changed_file"`
	BeforeSHA256      string `json:"before_sha256"`
	AfterSHA256       string `json:"after_sha256"`
	BeforeFragment    string `json:"before_fragment"`
	AfterFragment     string `json:"after_fragment"`
	ReviewPatchPath   string `json:"review_patch_path"`
	ReviewPatchSHA256 string `json:"review_patch_sha256"`
}

type LegacyComparison struct {
	SchemaVersion              string        `json:"schema_version"`
	Mode                       string        `json:"mode"`
	Status                     string        `json:"status"`
	SuiteHash                  string        `json:"suite_hash"`
	Platforms                  []string      `json:"platforms"`
	GeneratedAt                time.Time     `json:"generated_at"`
	BaselineCommit             string        `json:"baseline_commit"`
	BaselineRunID              string        `json:"baseline_run_id"`
	BaselineRunURL             string        `json:"baseline_run_url"`
	BaselineRunAttempt         string        `json:"baseline_run_attempt"`
	BaselineRepository         string        `json:"baseline_repository"`
	BaselineReporter           string        `json:"baseline_reporter"`
	BaselineValidationOutcome  string        `json:"baseline_validation_outcome"`
	BaselineGoVersion          string        `json:"baseline_go_version"`
	CandidateCommit            string        `json:"candidate_commit"`
	CandidateRunID             string        `json:"candidate_run_id"`
	CandidateRunURL            string        `json:"candidate_run_url"`
	CandidateRunAttempt        string        `json:"candidate_run_attempt"`
	CandidateRepository        string        `json:"candidate_repository"`
	CandidateReporter          string        `json:"candidate_reporter"`
	CandidateVersion           string        `json:"candidate_version"`
	CandidateValidationOutcome string        `json:"candidate_validation_outcome"`
	CandidateGoVersion         string        `json:"candidate_go_version"`
	Checks                     []LegacyCheck `json:"checks"`
}

type LegacyCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type MigrationVerification struct {
	SchemaID             string              `json:"schema_id"`
	SchemaVersion        int                 `json:"schema_version"`
	Status               string              `json:"status"`
	MigrationProofSHA256 string              `json:"migration_proof_sha256"`
	ComparatorSHA256     string              `json:"comparator_sha256"`
	BaselineFactsSHA256  string              `json:"baseline_facts_sha256"`
	SourceSuiteHash      string              `json:"source_suite_hash"`
	CurrentSemanticHash  string              `json:"current_semantic_hash"`
	Checks               []VerificationCheck `json:"checks"`
}

func LoadMigrationProof(filename string) (MigrationProof, []byte, error) {
	data, err := readBoundedRegular(filename, 1<<20)
	if err != nil {
		return MigrationProof{}, nil, fmt.Errorf("read migration proof: %w", err)
	}
	var proof MigrationProof
	if err := decodeStrict(data, &proof); err != nil {
		return MigrationProof{}, data, fmt.Errorf("decode migration proof: %w", err)
	}
	if err := proof.Validate(); err != nil {
		return proof, data, err
	}
	return proof, data, nil
}

func (proof MigrationProof) Validate() error {
	var problems []string
	add := func(format string, values ...any) { problems = append(problems, fmt.Sprintf(format, values...)) }
	if proof.SchemaID != MigrationSchemaID || proof.SchemaVersion != MigrationSchemaVersion || proof.Status != "pass" {
		add("migration schema/status must be %s version %d pass", MigrationSchemaID, MigrationSchemaVersion)
	}
	if proof.Comparator.Path != CanonicalComparatorPath || !validSHA256(proof.Comparator.SHA256) || !positiveInteger(proof.Comparator.RunID) || !positiveInteger(proof.Comparator.RunAttempt) || proof.Comparator.Name != "env-vault-e2e-candidate-comparison-attempt-"+proof.Comparator.RunAttempt {
		add("comparator artifact binding is malformed")
	}
	transition := proof.Transition
	if transition.Code != ReviewedSuiteTransition || transition.SourceAlgorithm != LegacySuiteAlgorithm || transition.TargetAlgorithm != e2esuite.SchemaID || !validSHA256(transition.SourceSuiteHash) || !validSHA256(transition.TargetSuiteHash) {
		add("suite transition algorithms, code, or hashes are invalid")
	}
	if transition.ChangedFile != "e2e/scenarios_test.go" || !validSHA256(transition.BeforeSHA256) || !validSHA256(transition.AfterSHA256) || transition.BeforeSHA256 == transition.AfterSHA256 || transition.BeforeFragment == "" || transition.AfterFragment == "" || transition.BeforeFragment == transition.AfterFragment {
		add("suite transition source change is malformed")
	}
	if transition.ReviewPatchPath != CanonicalTransitionPatch || !validSHA256(transition.ReviewPatchSHA256) || !validSHA256(proof.BaselineFactsSHA256) {
		add("review patch or baseline facts binding is malformed")
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return errors.New("invalid E2E baseline migration proof: " + strings.Join(problems, "; "))
	}
	return nil
}

// VerifyMigration proves the one-time link from the successful historical
// comparator to the durable baseline without re-parsing historical raw reports
// under newer validation rules.
func VerifyMigration(repositoryRoot, proofPath string, baseline Baseline, contract releasecontract.Contract) (MigrationVerification, error) {
	report := MigrationVerification{SchemaID: MigrationVerificationSchema, SchemaVersion: 1, Status: "pass"}
	add := func(code string, err error) {
		check := VerificationCheck{Code: code, Status: "pass"}
		if err != nil {
			check.Status, check.Detail, report.Status = "fail", err.Error(), "fail"
		}
		report.Checks = append(report.Checks, check)
	}

	proof, proofBytes, proofErr := LoadMigrationProof(proofPath)
	add("migration_schema", proofErr)
	if len(proofBytes) != 0 {
		report.MigrationProofSHA256 = bytesSHA256(proofBytes)
	}
	if baseline.Migration == nil {
		add("baseline_migration_reference", errors.New("baseline has no migration reference"))
	} else {
		referenceErr := error(nil)
		if baseline.Migration.Path != repositoryRelative(repositoryRoot, proofPath) || baseline.Migration.SHA256 != report.MigrationProofSHA256 {
			referenceErr = errors.New("baseline migration path/digest does not bind the supplied proof")
		}
		add("baseline_migration_reference", referenceErr)
	}

	comparison, comparatorDigest, comparatorErr := loadLegacyComparison(repositoryRoot, proof.Comparator)
	report.ComparatorSHA256 = comparatorDigest
	add("historical_comparator_bytes", comparatorErr)
	legacyErr := validateLegacyComparison(comparison, proof, contract)
	add("historical_comparator_result", legacyErr)

	factsDigest, factsErr := Digest(baseline.Facts())
	report.BaselineFactsSHA256 = factsDigest
	if factsErr == nil && factsDigest != proof.BaselineFactsSHA256 {
		factsErr = errors.New("baseline run facts differ from migration proof")
	}
	add("baseline_facts", factsErr)
	add("comparator_candidate_binding", compareCandidateToBaseline(comparison, baseline))

	transitionErr := verifyTransition(repositoryRoot, proof.Transition)
	report.SourceSuiteHash = proof.Transition.SourceSuiteHash
	report.CurrentSemanticHash = proof.Transition.TargetSuiteHash
	add("reviewed_suite_transition", transitionErr)
	currentHash, currentErr := e2esuite.Hash(repositoryRoot)
	if currentErr == nil && currentHash != proof.Transition.TargetSuiteHash {
		currentErr = fmt.Errorf("current semantic suite hash=%s, want %s", currentHash, proof.Transition.TargetSuiteHash)
	}
	if currentErr == nil && (baseline.SemanticSuite.Algorithm != proof.Transition.TargetAlgorithm || baseline.SemanticSuite.Hash != currentHash || baseline.SemanticSuite.SourceReportHash != proof.Transition.SourceSuiteHash || baseline.SemanticSuite.TransitionCode != proof.Transition.Code) {
		currentErr = errors.New("baseline semantic suite does not match the reviewed transition")
	}
	add("current_semantic_suite", currentErr)

	if report.Status != "pass" {
		return report, errors.New("durable E2E baseline migration verification failed")
	}
	return report, nil
}

func loadLegacyComparison(repositoryRoot string, binding ComparisonArtifact) (LegacyComparison, string, error) {
	filename, err := repositoryFile(repositoryRoot, binding.Path)
	if err != nil {
		return LegacyComparison{}, "", err
	}
	data, err := readBoundedRegular(filename, 1<<20)
	if err != nil {
		return LegacyComparison{}, "", err
	}
	digest := bytesSHA256(data)
	if digest != binding.SHA256 {
		return LegacyComparison{}, digest, fmt.Errorf("comparator SHA-256=%s, want %s", digest, binding.SHA256)
	}
	var comparison LegacyComparison
	if err := decodeStrict(data, &comparison); err != nil {
		return LegacyComparison{}, digest, fmt.Errorf("decode comparator: %w", err)
	}
	return comparison, digest, nil
}

func validateLegacyComparison(comparison LegacyComparison, proof MigrationProof, contract releasecontract.Contract) error {
	if comparison.SchemaVersion != "1" || comparison.Mode != "compare" || comparison.Status != "pass" || comparison.GeneratedAt.IsZero() || comparison.BaselineValidationOutcome != "success" || comparison.CandidateValidationOutcome != "success" {
		return errors.New("historical comparator schema, mode, status, time, or validation outcome is invalid")
	}
	if comparison.SuiteHash != proof.Transition.SourceSuiteHash || comparison.CandidateRunID != proof.Comparator.RunID || comparison.CandidateRunAttempt != proof.Comparator.RunAttempt || comparison.CandidateVersion != "ci-"+comparison.CandidateCommit {
		return errors.New("historical comparator suite, artifact run, or candidate version identity differs")
	}
	baselineRun := RunIdentity{CommitSHA: comparison.BaselineCommit, RunID: comparison.BaselineRunID, RunURL: comparison.BaselineRunURL, RunAttempt: comparison.BaselineRunAttempt, Repository: comparison.BaselineRepository}
	candidateRun := RunIdentity{CommitSHA: comparison.CandidateCommit, RunID: comparison.CandidateRunID, RunURL: comparison.CandidateRunURL, RunAttempt: comparison.CandidateRunAttempt, Repository: comparison.CandidateRepository}
	if err := validateRunIdentity(baselineRun); err != nil {
		return fmt.Errorf("historical baseline identity: %w", err)
	}
	if err := validateRunIdentity(candidateRun); err != nil {
		return fmt.Errorf("historical candidate identity: %w", err)
	}
	if !goPattern.MatchString(comparison.BaselineGoVersion) || !goPattern.MatchString(comparison.CandidateGoVersion) || !reporterPattern.MatchString(comparison.BaselineReporter) || !reporterPattern.MatchString(comparison.CandidateReporter) {
		return errors.New("historical comparator toolchain identity is malformed")
	}
	wantPlatforms := make([]string, 0, len(contract.Platforms))
	for _, platform := range contract.Platforms {
		wantPlatforms = append(wantPlatforms, platform.ID)
	}
	sort.Strings(wantPlatforms)
	actualPlatforms := append([]string(nil), comparison.Platforms...)
	sort.Strings(actualPlatforms)
	if !equalStrings(actualPlatforms, wantPlatforms) {
		return fmt.Errorf("historical comparator platforms=%v, want %v", actualPlatforms, wantPlatforms)
	}
	if len(comparison.Checks) != len(requiredLegacyChecks) {
		return fmt.Errorf("historical comparator check count=%d, want %d", len(comparison.Checks), len(requiredLegacyChecks))
	}
	for index, expected := range requiredLegacyChecks {
		actual := comparison.Checks[index]
		if actual.Name != expected || actual.Status != "pass" || actual.Detail != "" {
			return fmt.Errorf("historical comparator check %d=%q/%q, want %q/pass", index, actual.Name, actual.Status, expected)
		}
	}
	return nil
}

func compareCandidateToBaseline(comparison LegacyComparison, baseline Baseline) error {
	if comparison.CandidateCommit != baseline.Provenance.CommitSHA || comparison.CandidateRunID != baseline.Provenance.RunID || comparison.CandidateRunURL != baseline.Provenance.RunURL || comparison.CandidateRunAttempt != baseline.Provenance.RunAttempt || comparison.CandidateRepository != baseline.Provenance.Repository || baseline.Provenance.Phase != "candidate" {
		return errors.New("baseline provenance differs from the successful comparator candidate tuple")
	}
	if comparison.CandidateGoVersion != baseline.Toolchain.GoVersion || comparison.CandidateReporter != baseline.Toolchain.GotestsumVersion {
		return errors.New("baseline toolchain differs from the successful comparator candidate")
	}
	return nil
}

func verifyTransition(repositoryRoot string, transition SuiteTransition) error {
	changedFile, err := repositoryFile(repositoryRoot, transition.ChangedFile)
	if err != nil {
		return err
	}
	data, err := readBoundedRegular(changedFile, maxJSONBytes)
	if err != nil {
		return err
	}
	if bytesSHA256(data) != transition.AfterSHA256 {
		return errors.New("reviewed transition target file digest differs")
	}
	if bytes.Count(data, []byte(transition.BeforeFragment)) != 0 || bytes.Count(data, []byte(transition.AfterFragment)) != 1 {
		return errors.New("reviewed transition fragments are absent, duplicated, or reverted")
	}
	patchFile, err := repositoryFile(repositoryRoot, transition.ReviewPatchPath)
	if err != nil {
		return err
	}
	patch, err := readBoundedRegular(patchFile, 1<<20)
	if err != nil {
		return err
	}
	if bytesSHA256(patch) != transition.ReviewPatchSHA256 {
		return errors.New("review patch digest differs")
	}
	if bytes.Count(patch, []byte("--- a/"+transition.ChangedFile)) != 1 || bytes.Count(patch, []byte("+++ b/"+transition.ChangedFile)) != 1 || bytes.Count(patch, []byte("-\t"+transition.BeforeFragment)) != 1 || bytes.Count(patch, []byte("+\t"+transition.AfterFragment)) != 1 {
		return errors.New("review patch does not contain the exact one-line sentinel transition")
	}
	return nil
}

func repositoryFile(root, relative string) (string, error) {
	if !safeRepositoryPath(relative) {
		return "", fmt.Errorf("unsafe repository-relative path %q", relative)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	filename := filepath.Join(absRoot, filepath.FromSlash(relative))
	clean := filepath.Clean(filename)
	if clean == absRoot || !strings.HasPrefix(clean, absRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes repository root: %q", relative)
	}
	return clean, nil
}

func repositoryRelative(root, filename string) string {
	absRoot, rootErr := filepath.Abs(root)
	absFile, fileErr := filepath.Abs(filename)
	if rootErr != nil || fileErr != nil {
		return ""
	}
	relative, err := filepath.Rel(absRoot, absFile)
	if err != nil {
		return ""
	}
	return filepath.ToSlash(relative)
}

func bytesSHA256(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
