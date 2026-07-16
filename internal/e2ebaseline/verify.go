package e2ebaseline

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/ildarbinanas-design/env-vault/internal/e2esuite"
	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

type VerifyOptions struct {
	ProofPath          string
	RepositoryRoot     string
	Phase              string
	ExpectedCommit     string
	ExpectedRunID      string
	ExpectedRunURL     string
	ExpectedRunAttempt string
	ExpectedRepository string
}

type VerificationReport struct {
	SchemaID             string              `json:"schema_id"`
	SchemaVersion        int                 `json:"schema_version"`
	Status               string              `json:"status"`
	BaselineDigest       string              `json:"baseline_digest"`
	MatrixProofDigest    string              `json:"matrix_proof_digest,omitempty"`
	MigrationProofDigest string              `json:"migration_proof_digest,omitempty"`
	SuiteHash            string              `json:"suite_hash"`
	Platforms            []string            `json:"platforms"`
	Checks               []VerificationCheck `json:"checks"`
}

type VerificationCheck struct {
	Code   string `json:"code"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Verify compares a newly sealed matrix proof with the durable baseline. It
// does not open raw reports. When the baseline originated from the historical
// comparator, its checked-in migration proof is verified on every invocation.
func Verify(options VerifyOptions, baseline Baseline, contract releasecontract.Contract) (VerificationReport, error) {
	digest, err := Digest(baseline)
	if err != nil {
		return VerificationReport{}, err
	}
	platformIDs := make([]string, 0, len(contract.Platforms))
	for _, platform := range contract.Platforms {
		platformIDs = append(platformIDs, platform.ID)
	}
	report := VerificationReport{
		SchemaID: VerificationSchemaID, SchemaVersion: 1, Status: "pass", BaselineDigest: digest,
		SuiteHash: baseline.SemanticSuite.Hash, Platforms: platformIDs,
	}
	add := func(code string, checkErr error) {
		check := VerificationCheck{Code: code, Status: "pass"}
		if checkErr != nil {
			check.Status, check.Detail, report.Status = "fail", checkErr.Error(), "fail"
		}
		report.Checks = append(report.Checks, check)
	}
	if options.Phase == "" {
		options.Phase = "candidate"
	}
	proof, proofErr := LoadMatrixProof(options.ProofPath, contract)
	add("matrix_validation", proofErr)
	if proof.SchemaID != "" {
		proofDigest, digestErr := Digest(proof)
		report.MatrixProofDigest = proofDigest
		add("matrix_proof_digest", digestErr)
	} else {
		add("matrix_proof_digest", errors.New("matrix proof could not be decoded"))
	}
	add("platform_set", requireProofPlatformSet(proof, platformIDs))
	add("exact_run_identity", verifyIdentity(options, proof))
	checkoutHash, suiteErr := e2esuite.Hash(options.RepositoryRoot)
	if suiteErr == nil && checkoutHash != baseline.SemanticSuite.Hash {
		suiteErr = fmt.Errorf("checkout semantic suite hash=%s, want %s", checkoutHash, baseline.SemanticSuite.Hash)
	}
	if suiteErr == nil && proof.SuiteHash != baseline.SemanticSuite.Hash {
		suiteErr = fmt.Errorf("matrix proof suite hash=%s, want %s", proof.SuiteHash, baseline.SemanticSuite.Hash)
	}
	add("semantic_suite", suiteErr)
	if baseline.Migration != nil {
		migrationPath, pathErr := repositoryFile(options.RepositoryRoot, baseline.Migration.Path)
		if pathErr != nil {
			add("migration_proof", pathErr)
		} else {
			migrationReport, migrationErr := VerifyMigration(options.RepositoryRoot, migrationPath, baseline, contract)
			report.MigrationProofDigest = migrationReport.MigrationProofSHA256
			add("migration_proof", migrationErr)
		}
	} else {
		add("migration_proof", nil)
	}

	var normalizedErrors, toolchainErrors, contractErrors, coverageErrors, scenarioErrors, leakErrors []string
	baselineByPlatform := make(map[string]PlatformBaseline, len(baseline.Platforms))
	for _, platform := range baseline.Platforms {
		baselineByPlatform[platform.ID] = platform
	}
	proofByPlatform := make(map[string]PlatformProof, len(proof.PlatformEvidence))
	for _, platform := range proof.PlatformEvidence {
		proofByPlatform[platform.ID] = platform
	}
	for _, declared := range contract.Platforms {
		actual, ok := proofByPlatform[declared.ID]
		if !ok {
			continue
		}
		expected := baselineByPlatform[declared.ID]
		wantDigest, digestErr := PlatformProofDigest(actual)
		if digestErr != nil || actual.NormalizedEvidenceSHA256 != wantDigest {
			normalizedErrors = append(normalizedErrors, declared.ID+": normalized evidence digest mismatch")
		}
		if actual.GoVersion != baseline.Toolchain.GoVersion || actual.GotestsumVersion != baseline.Toolchain.GotestsumVersion {
			toolchainErrors = append(toolchainErrors, fmt.Sprintf("%s: Go=%s gotestsum=%s", declared.ID, actual.GoVersion, actual.GotestsumVersion))
		}
		if actual.ContractSHA256 != expected.ContractSHA256 {
			contractErrors = append(contractErrors, fmt.Sprintf("%s: contract hash=%s", declared.ID, actual.ContractSHA256))
		}
		if actual.StatementCoveragePercent+0.000001 < expected.CoverageFloorPercent {
			coverageErrors = append(coverageErrors, fmt.Sprintf("%s: %.2f < %.2f", declared.ID, actual.StatementCoveragePercent, expected.CoverageFloorPercent))
		}
		if err := compareScenarioEvidence(actual, expected); err != nil {
			scenarioErrors = append(scenarioErrors, declared.ID+": "+err.Error())
		}
		if actual.Leak != expected.Leak {
			leakErrors = append(leakErrors, fmt.Sprintf("%s: observed=%+v, want %+v", declared.ID, actual.Leak, expected.Leak))
		}
	}
	add("normalized_evidence", joinedError(normalizedErrors))
	add("exact_toolchain", joinedError(toolchainErrors))
	add("public_contracts", joinedError(contractErrors))
	add("coverage_floors", joinedError(coverageErrors))
	add("critical_scenarios", joinedError(scenarioErrors))
	add("leak_expectations", joinedError(leakErrors))
	if report.Status != "pass" {
		return report, errors.New("durable E2E baseline verification failed")
	}
	return report, nil
}

func verifyIdentity(options VerifyOptions, proof MatrixProof) error {
	expected := RunIdentity{CommitSHA: options.ExpectedCommit, RunID: options.ExpectedRunID, RunURL: options.ExpectedRunURL, RunAttempt: options.ExpectedRunAttempt, Repository: options.ExpectedRepository}
	if err := validateRunIdentity(expected); err != nil {
		return fmt.Errorf("expected run identity: %w", err)
	}
	if proof.Phase != options.Phase || proof.Run != expected {
		return errors.New("matrix proof identity differs from expected phase/source/run tuple")
	}
	return nil
}

func requireProofPlatformSet(proof MatrixProof, required []string) error {
	if !equalStrings(proof.Platforms, required) {
		return fmt.Errorf("matrix proof platform set=%v, want %v", proof.Platforms, required)
	}
	if len(proof.PlatformEvidence) != len(required) {
		return fmt.Errorf("matrix proof evidence count=%d, want %d", len(proof.PlatformEvidence), len(required))
	}
	for index := range required {
		if proof.PlatformEvidence[index].ID != required[index] {
			return fmt.Errorf("matrix proof evidence platform %d=%q, want %q", index, proof.PlatformEvidence[index].ID, required[index])
		}
	}
	return nil
}

func compareScenarioEvidence(actual PlatformProof, expected PlatformBaseline) error {
	if actual.Counts != expected.Counts {
		return fmt.Errorf("counts=%+v, want %+v", actual.Counts, expected.Counts)
	}
	if !equalStrings(actual.ExpectedSkips, expected.ExpectedSkips) {
		return fmt.Errorf("expected skips=%v, want %v", actual.ExpectedSkips, expected.ExpectedSkips)
	}
	if len(actual.CriticalScenarios) != len(expected.CriticalScenarios) {
		return fmt.Errorf("critical scenario count=%d, want %d", len(actual.CriticalScenarios), len(expected.CriticalScenarios))
	}
	for index := range actual.CriticalScenarios {
		if actual.CriticalScenarios[index] != expected.CriticalScenarios[index] {
			return fmt.Errorf("scenario %d=%+v, want %+v", index, actual.CriticalScenarios[index], expected.CriticalScenarios[index])
		}
	}
	return nil
}

func joinedError(values []string) error {
	if len(values) == 0 {
		return nil
	}
	sort.Strings(values)
	return errors.New(strings.Join(values, "; "))
}

func VerificationMarkdown(report VerificationReport) []byte {
	var output strings.Builder
	output.WriteString("# Durable E2E baseline verification\n\n")
	fmt.Fprintf(&output, "Status: **%s**  \n", strings.ToUpper(report.Status))
	fmt.Fprintf(&output, "Baseline digest: `%s`  \n", report.BaselineDigest)
	fmt.Fprintf(&output, "Matrix proof digest: `%s`  \n", report.MatrixProofDigest)
	fmt.Fprintf(&output, "Migration proof digest: `%s`  \n", report.MigrationProofDigest)
	fmt.Fprintf(&output, "Semantic suite hash: `%s`  \n", report.SuiteHash)
	fmt.Fprintf(&output, "Platforms: `%s`\n\n", strings.Join(report.Platforms, "`, `"))
	output.WriteString("| Check code | Status | Detail |\n|---|---|---|\n")
	for _, check := range report.Checks {
		detail := strings.ReplaceAll(strings.ReplaceAll(check.Detail, "|", "\\|"), "\n", " ")
		fmt.Fprintf(&output, "| `%s` | **%s** | %s |\n", check.Code, strings.ToUpper(check.Status), detail)
	}
	return []byte(output.String())
}
