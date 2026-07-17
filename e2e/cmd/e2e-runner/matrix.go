package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/e2ebaseline"
)

type reportSetEntry struct {
	Directory    string
	Metadata     runMetadata
	Coverage     featureCoverage
	Leak         leakScanReport
	ContractHash string
}

type gateCheck = e2ebaseline.ProofCheck
type gateReport = e2ebaseline.MatrixProof

func discoverReports(root string, requireValid bool) (map[string]reportSetEntry, []string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return nil, nil, fmt.Errorf("report root is not a directory: %s", abs)
	}
	if err := assertNoSentinelMarker(abs); err != nil {
		return nil, nil, fmt.Errorf("report root security scan: %w", err)
	}
	entries := make(map[string]reportSetEntry)
	var validationErrors []string
	err = filepath.WalkDir(abs, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("report tree contains symlink: %s", filename)
		}
		if entry.IsDir() || entry.Name() != "metadata.json" {
			return nil
		}
		directory := filepath.Dir(filename)
		var metadata runMetadata
		var coverage featureCoverage
		var leaks leakScanReport
		contractHash := ""
		var reportErr error
		if requireValid {
			metadata, coverage, reportErr = validateReportDirectory(directory)
			if reportErr == nil {
				reportErr = readJSON(filepath.Join(directory, "leak-scan.json"), &leaks)
			}
			if reportErr == nil {
				contractHash, reportErr = canonicalJSONSHA256(filepath.Join(directory, "contracts.json"))
			}
		} else {
			reportErr = readJSON(filename, &metadata)
			if reportErr == nil {
				reportErr = readJSON(filepath.Join(directory, "feature-coverage.json"), &coverage)
			}
		}
		if reportErr != nil {
			validationErrors = append(validationErrors, fmt.Sprintf("%s: %v", filepath.ToSlash(directory), reportErr))
			return nil
		}
		if metadata.Platform == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("%s: metadata platform is empty", filepath.ToSlash(directory)))
			return nil
		}
		if prior, exists := entries[metadata.Platform]; exists {
			return fmt.Errorf("duplicate report platform %s in %s and %s", metadata.Platform, prior.Directory, directory)
		}
		entries[metadata.Platform] = reportSetEntry{Directory: directory, Metadata: metadata, Coverage: coverage, Leak: leaks, ContractHash: contractHash}
		return nil
	})
	if err != nil {
		return nil, validationErrors, err
	}
	if len(entries) == 0 && len(validationErrors) == 0 {
		return nil, nil, fmt.Errorf("no metadata.json reports found below %s", abs)
	}
	sort.Strings(validationErrors)
	return entries, validationErrors, nil
}

func validateMatrix(opts matrixOptions) error {
	reports, invalid, discoverErr := discoverReports(opts.reportsRoot, true)
	required := parseCSV(opts.required)
	gate := gateReport{
		SchemaID:      e2ebaseline.MatrixProofSchemaID,
		SchemaVersion: e2ebaseline.MatrixProofSchemaVersion,
		Mode:          "validate-matrix",
		Status:        "pass",
		Phase:         opts.phase,
		Run: e2ebaseline.RunIdentity{
			CommitSHA: opts.expectedCommit, RunID: opts.expectedRunID, RunURL: opts.expectedRunURL,
			RunAttempt: opts.expectedRunAttempt, Repository: opts.expectedRepository,
		},
		Platforms:   required,
		GeneratedAt: time.Now().UTC(),
		Checks:      []gateCheck{},
	}
	add := func(name string, err error) {
		check := gateCheck{Name: name, Status: "pass"}
		if err != nil {
			check.Status = "fail"
			check.Detail = err.Error()
			gate.Status = "fail"
		}
		gate.Checks = append(gate.Checks, check)
	}
	add("discover reports", discoverErr)
	repoRoot, repoErr := findRepoRoot()
	checkoutSuiteHash := ""
	if repoErr == nil {
		checkoutSuiteHash, repoErr = suiteHash(repoRoot)
	}
	add("resolve checkout suite identity", repoErr)
	if len(invalid) > 0 {
		add("validate report files", errors.New(strings.Join(invalid, "; ")))
	} else {
		add("validate report files", nil)
	}
	var missing []string
	for _, platform := range required {
		if _, ok := reports[platform]; !ok {
			missing = append(missing, platform)
		}
	}
	var extra []string
	for platform := range reports {
		if !containsString(required, platform) {
			extra = append(extra, platform)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		add("required platform set", fmt.Errorf("missing=%v extra=%v", missing, extra))
	} else {
		add("required platform set", nil)
	}
	var suiteHash, commitSHA, goVersion, runID, runURL, runAttempt string
	var identityErrors []string
	for _, platform := range required {
		entry, ok := reports[platform]
		if !ok {
			continue
		}
		if entry.Metadata.Phase != opts.phase {
			identityErrors = append(identityErrors, fmt.Sprintf("%s phase=%s", platform, entry.Metadata.Phase))
		}
		if entry.Metadata.CommitSHA != opts.expectedCommit {
			identityErrors = append(identityErrors, fmt.Sprintf("%s commit=%s, want %s", platform, entry.Metadata.CommitSHA, opts.expectedCommit))
		}
		if entry.Metadata.GitHubRunID != opts.expectedRunID {
			identityErrors = append(identityErrors, fmt.Sprintf("%s run_id=%s, want %s", platform, entry.Metadata.GitHubRunID, opts.expectedRunID))
		}
		if entry.Metadata.GitHubRunURL != opts.expectedRunURL || entry.Metadata.GitHubRunAttempt != opts.expectedRunAttempt || entry.Metadata.GitHubRepository != opts.expectedRepository {
			identityErrors = append(identityErrors, fmt.Sprintf("%s GitHub run URL/attempt/repository differs from expected canonical identity", platform))
		}
		if entry.Metadata.GotestsumVersion != opts.expectedReporter {
			identityErrors = append(identityErrors, fmt.Sprintf("%s reporter=%s, want %s", platform, entry.Metadata.GotestsumVersion, opts.expectedReporter))
		}
		if opts.expectedRunID != "local" && entry.Metadata.SubjectKind != "artifact" {
			identityErrors = append(identityErrors, fmt.Sprintf("%s CI subject_kind=%s, want artifact", platform, entry.Metadata.SubjectKind))
		}
		if suiteHash == "" {
			suiteHash = entry.Metadata.SuiteHash
		} else if suiteHash != entry.Metadata.SuiteHash {
			identityErrors = append(identityErrors, fmt.Sprintf("%s suite_hash differs", platform))
		}
		if commitSHA == "" {
			commitSHA = entry.Metadata.CommitSHA
			goVersion = entry.Metadata.GoVersion
			runID = entry.Metadata.GitHubRunID
			runURL = entry.Metadata.GitHubRunURL
			runAttempt = entry.Metadata.GitHubRunAttempt
		} else {
			if commitSHA != entry.Metadata.CommitSHA {
				identityErrors = append(identityErrors, fmt.Sprintf("%s commit SHA differs", platform))
			}
			if goVersion != entry.Metadata.GoVersion {
				identityErrors = append(identityErrors, fmt.Sprintf("%s Go version differs", platform))
			}
			if runID != entry.Metadata.GitHubRunID || runURL != entry.Metadata.GitHubRunURL || runAttempt != entry.Metadata.GitHubRunAttempt {
				identityErrors = append(identityErrors, fmt.Sprintf("%s GitHub run identity differs", platform))
			}
		}
		if entry.Metadata.Status != "pass" || len(entry.Metadata.UnexpectedSkips) != 0 || entry.Coverage.CriticalCoveragePct != 100 {
			identityErrors = append(identityErrors, fmt.Sprintf("%s did not pass all gates", platform))
		}
	}
	gate.SuiteHash = suiteHash
	if repoErr == nil && suiteHash != checkoutSuiteHash {
		identityErrors = append(identityErrors, fmt.Sprintf("report suite_hash=%s, checkout suite_hash=%s", suiteHash, checkoutSuiteHash))
	}
	if len(identityErrors) > 0 {
		add("matrix identity and gates", errors.New(strings.Join(identityErrors, "; ")))
	} else {
		add("matrix identity and gates", nil)
	}
	var proofErrors []string
	for _, platform := range required {
		entry, ok := reports[platform]
		if !ok {
			continue
		}
		proof, proofErr := normalizedPlatformProof(entry)
		if proofErr != nil {
			proofErrors = append(proofErrors, platform+": "+proofErr.Error())
			continue
		}
		gate.PlatformEvidence = append(gate.PlatformEvidence, proof)
	}
	if len(proofErrors) > 0 {
		add("seal normalized platform evidence", errors.New(strings.Join(proofErrors, "; ")))
	} else {
		add("seal normalized platform evidence", nil)
	}
	if err := writeGateReport(opts.reportsRoot, "matrix-validation", gate); err != nil {
		return err
	}
	if gate.Status != "pass" {
		return fmt.Errorf("E2E matrix validation failed; see %s", filepath.Join(opts.reportsRoot, "matrix-validation.json"))
	}
	fmt.Fprintf(os.Stdout, "validated %s E2E matrix: %s\n", opts.phase, strings.Join(required, ", "))
	return nil
}

func writeGateReport(directory, base string, report gateReport) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(directory, base+".json"), report); err != nil {
		return err
	}
	var output strings.Builder
	fmt.Fprintf(&output, "# E2E %s\n\n", report.Mode)
	fmt.Fprintf(&output, "Status: **%s**  \n", strings.ToUpper(report.Status))
	fmt.Fprintf(&output, "Schema: `%s` version `%d`  \n", report.SchemaID, report.SchemaVersion)
	fmt.Fprintf(&output, "Platforms: `%s`  \n", strings.Join(report.Platforms, "`, `"))
	if report.SuiteHash != "" {
		fmt.Fprintf(&output, "Suite hash: `%s`  \n", report.SuiteHash)
	}
	if digest, err := e2ebaseline.Digest(report); err == nil {
		fmt.Fprintf(&output, "Proof digest: `%s`\n", digest)
	}
	output.WriteString("\n| Check | Status | Detail |\n|---|---|---|\n")
	for _, check := range report.Checks {
		fmt.Fprintf(&output, "| %s | **%s** | %s |\n", markdownCell(check.Name), strings.ToUpper(check.Status), markdownCell(check.Detail))
	}
	return writeFileAtomic(filepath.Join(directory, base+".md"), []byte(output.String()), 0o600)
}

func normalizedPlatformProof(entry reportSetEntry) (e2ebaseline.PlatformProof, error) {
	metadata := entry.Metadata
	critical := make([]e2ebaseline.ScenarioExpectation, 0, len(entry.Coverage.Scenarios))
	for _, scenario := range entry.Coverage.Scenarios {
		if scenario.Critical && scenario.Result != "not_applicable" {
			critical = append(critical, e2ebaseline.ScenarioExpectation{ID: scenario.ScenarioID, Result: scenario.Result})
		}
	}
	sort.Slice(critical, func(left, right int) bool { return critical[left].ID < critical[right].ID })
	skips := append([]string(nil), metadata.ExpectedPlatformSkips...)
	sort.Strings(skips)
	digests := make(map[string]string, len(metadata.EvidenceSHA256))
	for name, digest := range metadata.EvidenceSHA256 {
		digests[name] = digest
	}
	proof := e2ebaseline.PlatformProof{
		ID:        metadata.Platform,
		Phase:     metadata.Phase,
		SuiteHash: metadata.SuiteHash,
		Run: e2ebaseline.RunIdentity{
			CommitSHA: metadata.CommitSHA, RunID: metadata.GitHubRunID, RunURL: metadata.GitHubRunURL,
			RunAttempt: metadata.GitHubRunAttempt, Repository: metadata.GitHubRepository,
		},
		GOOS:             metadata.GOOS,
		GOARCH:           metadata.GOARCH,
		GoVersion:        metadata.GoVersion,
		GotestsumVersion: metadata.GotestsumVersion,
		SubjectKind:      metadata.SubjectKind,
		BinarySHA256:     metadata.BinarySHA256,
		Artifact: e2ebaseline.ArtifactProof{
			Archive: portableBase(metadata.Artifact.Path), Checksum: portableBase(metadata.Artifact.ChecksumPath),
			Format: metadata.Artifact.Format, SHA256: metadata.Artifact.SHA256, ChecksumVerified: metadata.Artifact.ChecksumVerified,
		},
		ContractSHA256:           entry.ContractHash,
		EvidenceSHA256:           digests,
		StatementCoveragePercent: metadata.StatementCoverage,
		Counts: e2ebaseline.Counts{
			Passed: metadata.Counts.Passed, Failed: metadata.Counts.Failed,
			Skipped: metadata.Counts.Skipped, Missing: metadata.Counts.Missing,
		},
		ExpectedSkips:     skips,
		CriticalScenarios: critical,
		Leak: e2ebaseline.LeakExpectation{
			Status: entry.Leak.Status, Detected: entry.Leak.Detected, FilesScanned: entry.Leak.FilesScanned,
			Occurrences: entry.Leak.Occurrences, RegistryRecords: entry.Leak.RegistryRecords, Findings: len(entry.Leak.Findings),
		},
	}
	if err := e2ebaseline.SealPlatformProof(&proof); err != nil {
		return e2ebaseline.PlatformProof{}, err
	}
	return proof, nil
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
