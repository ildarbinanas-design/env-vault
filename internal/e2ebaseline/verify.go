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
	SourceProof        bool
}

type VerificationReport struct {
	SchemaID          string              `json:"schema_id"`
	SchemaVersion     int                 `json:"schema_version"`
	Status            string              `json:"status"`
	BaselineDigest    string              `json:"baseline_digest"`
	MatrixProofDigest string              `json:"matrix_proof_digest,omitempty"`
	SuiteHash         string              `json:"suite_hash"`
	Platforms         []string            `json:"platforms"`
	SourceProof       bool                `json:"source_proof"`
	Checks            []VerificationCheck `json:"checks"`
}

type VerificationCheck struct {
	Code   string `json:"code"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Verify compares a sealed matrix proof with the durable baseline. It never
// opens the raw report tree; all deep report, burn-in, contract, coverage, and
// leak validation belongs to the single proof-producing validator.
func Verify(options VerifyOptions, baseline Baseline, contract releasecontract.Contract) (VerificationReport, error) {
	digest, err := Digest(baseline)
	if err != nil {
		return VerificationReport{}, err
	}
	platformIDs := make([]string, 0, len(contract.Platforms))
	for _, platform := range contract.Platforms {
		platformIDs = append(platformIDs, platform.ID)
	}
	verification := VerificationReport{
		SchemaID:       VerificationSchemaID,
		SchemaVersion:  1,
		Status:         "pass",
		BaselineDigest: digest,
		SuiteHash:      baseline.SemanticSuite.Hash,
		Platforms:      platformIDs,
		SourceProof:    options.SourceProof,
	}
	add := func(code string, err error) {
		check := VerificationCheck{Code: code, Status: "pass"}
		if err != nil {
			check.Status = "fail"
			check.Detail = err.Error()
			verification.Status = "fail"
		}
		verification.Checks = append(verification.Checks, check)
	}
	if options.Phase == "" {
		options.Phase = "candidate"
	}
	proof, proofErr := LoadMatrixProof(options.ProofPath, contract)
	add("matrix_validation", proofErr)
	if proof.SchemaID != "" {
		if proofDigest, digestErr := Digest(proof); digestErr != nil {
			add("matrix_proof_digest", digestErr)
		} else {
			verification.MatrixProofDigest = proofDigest
			add("matrix_proof_digest", nil)
		}
	} else {
		add("matrix_proof_digest", errors.New("matrix proof could not be decoded"))
	}
	add("platform_set", requireProofPlatformSet(proof, platformIDs))
	add("exact_run_identity", verifyIdentity(options, proof, baseline))
	checkoutHash, suiteErr := e2esuite.Hash(options.RepositoryRoot)
	wantCheckoutHash := baseline.SemanticSuite.Hash
	if options.SourceProof {
		wantCheckoutHash = baseline.SemanticSuite.SourceReportHash
	}
	if suiteErr == nil && checkoutHash != wantCheckoutHash {
		suiteErr = fmt.Errorf("checkout suite hash=%s, want %s", checkoutHash, wantCheckoutHash)
	}
	if suiteErr == nil {
		wantProofHash := baseline.SemanticSuite.Hash
		if options.SourceProof {
			wantProofHash = baseline.SemanticSuite.SourceReportHash
		}
		if proof.SuiteHash != wantProofHash {
			suiteErr = fmt.Errorf("matrix proof suite hash=%s, want %s", proof.SuiteHash, wantProofHash)
		}
	}
	add("semantic_suite", suiteErr)

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
	if verification.Status != "pass" {
		return verification, errors.New("durable E2E baseline verification failed")
	}
	return verification, nil
}

func verifyIdentity(options VerifyOptions, proof MatrixProof, baseline Baseline) error {
	expected := RunIdentity{
		CommitSHA: options.ExpectedCommit, RunID: options.ExpectedRunID, RunURL: options.ExpectedRunURL,
		RunAttempt: options.ExpectedRunAttempt, Repository: options.ExpectedRepository,
	}
	if err := validateRunIdentity(expected); err != nil {
		return fmt.Errorf("expected run identity: %w", err)
	}
	if proof.Phase != options.Phase || proof.Run != expected {
		return errors.New("matrix proof identity differs from expected phase/source/run tuple")
	}
	if options.SourceProof {
		provenance := baseline.Provenance
		if expected.CommitSHA != provenance.CommitSHA || expected.RunID != provenance.RunID || expected.RunURL != provenance.RunURL || expected.RunAttempt != provenance.RunAttempt || expected.Repository != provenance.Repository {
			return errors.New("source proof requires the exact baseline provenance tuple")
		}
	}
	return nil
}

func requireProofPlatformSet(proof MatrixProof, required []string) error {
	want := append([]string(nil), required...)
	actual := append([]string(nil), proof.Platforms...)
	if !equalStrings(actual, want) {
		return fmt.Errorf("matrix proof platform set=%v, want %v", actual, want)
	}
	if len(proof.PlatformEvidence) != len(want) {
		return fmt.Errorf("matrix proof evidence count=%d, want %d", len(proof.PlatformEvidence), len(want))
	}
	for index := range want {
		if proof.PlatformEvidence[index].ID != want[index] {
			return fmt.Errorf("matrix proof evidence platform %d=%q, want %q", index, proof.PlatformEvidence[index].ID, want[index])
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
	fmt.Fprintf(&output, "Semantic suite hash: `%s`  \n", report.SuiteHash)
	fmt.Fprintf(&output, "Platforms: `%s`\n\n", strings.Join(report.Platforms, "`, `"))
	output.WriteString("| Check code | Status | Detail |\n|---|---|---|\n")
	for _, check := range report.Checks {
		detail := strings.ReplaceAll(strings.ReplaceAll(check.Detail, "|", "\\|"), "\n", " ")
		fmt.Fprintf(&output, "| `%s` | **%s** | %s |\n", check.Code, strings.ToUpper(check.Status), detail)
	}
	return []byte(output.String())
}
