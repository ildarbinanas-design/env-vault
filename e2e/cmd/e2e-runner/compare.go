package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type reportSetEntry struct {
	Directory string
	Metadata  runMetadata
	Coverage  featureCoverage
}

type gateCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type gateReport struct {
	SchemaVersion       string      `json:"schema_version"`
	Mode                string      `json:"mode"`
	Status              string      `json:"status"`
	Phase               string      `json:"phase,omitempty"`
	SuiteHash           string      `json:"suite_hash,omitempty"`
	Platforms           []string    `json:"platforms"`
	GeneratedAt         time.Time   `json:"generated_at"`
	BaselineCommit      string      `json:"baseline_commit,omitempty"`
	BaselineRunID       string      `json:"baseline_run_id,omitempty"`
	BaselineRunURL      string      `json:"baseline_run_url,omitempty"`
	BaselineRunAttempt  string      `json:"baseline_run_attempt,omitempty"`
	BaselineRepository  string      `json:"baseline_repository,omitempty"`
	BaselineReporter    string      `json:"baseline_reporter,omitempty"`
	BaselineGoVersion   string      `json:"baseline_go_version,omitempty"`
	CandidateCommit     string      `json:"candidate_commit,omitempty"`
	CandidateRunID      string      `json:"candidate_run_id,omitempty"`
	CandidateRunURL     string      `json:"candidate_run_url,omitempty"`
	CandidateRunAttempt string      `json:"candidate_run_attempt,omitempty"`
	CandidateRepository string      `json:"candidate_repository,omitempty"`
	CandidateReporter   string      `json:"candidate_reporter,omitempty"`
	CandidateGoVersion  string      `json:"candidate_go_version,omitempty"`
	Checks              []gateCheck `json:"checks"`
}

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
		var reportErr error
		if requireValid {
			metadata, coverage, reportErr = validateReportDirectory(directory)
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
		entries[metadata.Platform] = reportSetEntry{Directory: directory, Metadata: metadata, Coverage: coverage}
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
	sort.Strings(required)
	gate := gateReport{
		SchemaVersion: reportSchemaVersion,
		Mode:          "validate-matrix",
		Status:        "pass",
		Phase:         opts.phase,
		Platforms:     required,
		GeneratedAt:   time.Now().UTC(),
		Checks:        []gateCheck{},
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
	if err := writeGateReport(opts.reportsRoot, "matrix-validation", gate); err != nil {
		return err
	}
	if gate.Status != "pass" {
		return fmt.Errorf("E2E matrix validation failed; see %s", filepath.Join(opts.reportsRoot, "matrix-validation.json"))
	}
	fmt.Fprintf(os.Stdout, "validated %s E2E matrix: %s\n", opts.phase, strings.Join(required, ", "))
	return nil
}

func compareReports(opts compareOptions) error {
	baseline, baselineInvalid, baselineErr := discoverReports(opts.baseline, true)
	candidate, candidateInvalid, candidateErr := discoverReports(opts.candidate, true)
	platforms := append([]string(nil), requiredPlatforms()...)
	sort.Strings(platforms)
	gate := gateReport{
		SchemaVersion:       reportSchemaVersion,
		Mode:                "compare",
		Status:              "pass",
		Platforms:           platforms,
		GeneratedAt:         time.Now().UTC(),
		BaselineCommit:      opts.baselineCommit,
		BaselineRunID:       opts.baselineRunID,
		BaselineRunURL:      opts.baselineRunURL,
		BaselineRunAttempt:  opts.baselineRunAttempt,
		BaselineRepository:  opts.baselineRepository,
		BaselineReporter:    opts.baselineReporter,
		CandidateCommit:     opts.candidateCommit,
		CandidateRunID:      opts.candidateRunID,
		CandidateRunURL:     opts.candidateRunURL,
		CandidateRunAttempt: opts.candidateRunAttempt,
		CandidateRepository: opts.candidateRepository,
		CandidateReporter:   opts.candidateReporter,
		Checks:              []gateCheck{},
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
	add("baseline reports", combineDiscoveryErrors(baselineErr, baselineInvalid))
	add("candidate reports", combineDiscoveryErrors(candidateErr, candidateInvalid))
	repoRoot, repoErr := findRepoRoot()
	checkoutSuiteHash := ""
	if repoErr == nil {
		checkoutSuiteHash, repoErr = suiteHash(repoRoot)
	}
	add("resolve checkout suite identity", repoErr)
	baselinePlatforms := sortedReportPlatforms(baseline)
	candidatePlatforms := sortedReportPlatforms(candidate)
	if !equalStrings(platforms, baselinePlatforms) || !equalStrings(platforms, candidatePlatforms) {
		add("platform set", fmt.Errorf("required=%v baseline=%v candidate=%v", platforms, baselinePlatforms, candidatePlatforms))
	} else {
		add("platform set", nil)
	}
	var suiteHash, baselineGoVersion, candidateGoVersion string
	var baselineRunURL, baselineRunAttempt, candidateRunURL, candidateRunAttempt string
	var identityErrors, scenarioErrors, contractErrors, coverageErrors []string
	for _, platform := range platforms {
		base, baseOK := baseline[platform]
		cand, candOK := candidate[platform]
		if !baseOK || !candOK {
			continue
		}
		if base.Metadata.Phase != "baseline" {
			identityErrors = append(identityErrors, platform+": baseline phase is "+base.Metadata.Phase)
		}
		if cand.Metadata.Phase != "candidate" {
			identityErrors = append(identityErrors, platform+": candidate phase is "+cand.Metadata.Phase)
		}
		if base.Metadata.CommitSHA != opts.baselineCommit || base.Metadata.GitHubRunID != opts.baselineRunID || base.Metadata.GitHubRunURL != opts.baselineRunURL || base.Metadata.GitHubRunAttempt != opts.baselineRunAttempt || base.Metadata.GitHubRepository != opts.baselineRepository || base.Metadata.GotestsumVersion != opts.baselineReporter {
			identityErrors = append(identityErrors, fmt.Sprintf("%s: baseline identity commit=%s run=%s, want commit=%s run=%s", platform, base.Metadata.CommitSHA, base.Metadata.GitHubRunID, opts.baselineCommit, opts.baselineRunID))
		}
		if cand.Metadata.CommitSHA != opts.candidateCommit || cand.Metadata.GitHubRunID != opts.candidateRunID || cand.Metadata.GitHubRunURL != opts.candidateRunURL || cand.Metadata.GitHubRunAttempt != opts.candidateRunAttempt || cand.Metadata.GitHubRepository != opts.candidateRepository || cand.Metadata.GotestsumVersion != opts.candidateReporter {
			identityErrors = append(identityErrors, fmt.Sprintf("%s: candidate identity commit=%s run=%s, want commit=%s run=%s", platform, cand.Metadata.CommitSHA, cand.Metadata.GitHubRunID, opts.candidateCommit, opts.candidateRunID))
		}
		if base.Metadata.SubjectKind != "artifact" || cand.Metadata.SubjectKind != "artifact" {
			identityErrors = append(identityErrors, platform+": canonical comparison requires native artifact subjects")
		}
		if base.Metadata.SuiteHash != cand.Metadata.SuiteHash {
			identityErrors = append(identityErrors, platform+": suite hash changed")
		}
		if suiteHash == "" {
			suiteHash = base.Metadata.SuiteHash
			baselineGoVersion = base.Metadata.GoVersion
			candidateGoVersion = cand.Metadata.GoVersion
			baselineRunURL = base.Metadata.GitHubRunURL
			baselineRunAttempt = base.Metadata.GitHubRunAttempt
			candidateRunURL = cand.Metadata.GitHubRunURL
			candidateRunAttempt = cand.Metadata.GitHubRunAttempt
		} else if suiteHash != base.Metadata.SuiteHash || suiteHash != cand.Metadata.SuiteHash {
			identityErrors = append(identityErrors, platform+": matrix suite hash is inconsistent")
		}
		if baselineGoVersion != base.Metadata.GoVersion {
			identityErrors = append(identityErrors, platform+": baseline Go version is inconsistent")
		}
		if candidateGoVersion != cand.Metadata.GoVersion {
			identityErrors = append(identityErrors, platform+": candidate Go version is inconsistent")
		}
		if baselineRunURL != base.Metadata.GitHubRunURL || baselineRunAttempt != base.Metadata.GitHubRunAttempt {
			identityErrors = append(identityErrors, platform+": baseline run URL/attempt is inconsistent")
		}
		if candidateRunURL != cand.Metadata.GitHubRunURL || candidateRunAttempt != cand.Metadata.GitHubRunAttempt {
			identityErrors = append(identityErrors, platform+": candidate run URL/attempt is inconsistent")
		}
		if err := compareCriticalScenarios(base.Coverage, cand.Coverage); err != nil {
			scenarioErrors = append(scenarioErrors, platform+": "+err.Error())
		}
		baseContracts, baseErr := canonicalJSONFile(filepath.Join(base.Directory, "contracts.json"))
		candContracts, candErr := canonicalJSONFile(filepath.Join(cand.Directory, "contracts.json"))
		if baseErr != nil || candErr != nil {
			contractErrors = append(contractErrors, fmt.Sprintf("%s: baseline=%v candidate=%v", platform, baseErr, candErr))
		} else if !bytes.Equal(baseContracts, candContracts) {
			contractErrors = append(contractErrors, platform+": normalized stdout/stderr, exit-code, or JSON contract changed")
		}
		if cand.Metadata.StatementCoverage+opts.coverageTolerance < base.Metadata.StatementCoverage {
			coverageErrors = append(coverageErrors, fmt.Sprintf("%s: %.2f%% -> %.2f%% (tolerance %.2f)", platform, base.Metadata.StatementCoverage, cand.Metadata.StatementCoverage, opts.coverageTolerance))
		}
	}
	gate.SuiteHash = suiteHash
	if repoErr == nil && suiteHash != checkoutSuiteHash {
		identityErrors = append(identityErrors, fmt.Sprintf("report suite_hash=%s, checkout suite_hash=%s", suiteHash, checkoutSuiteHash))
	}
	gate.BaselineGoVersion = baselineGoVersion
	gate.CandidateGoVersion = candidateGoVersion
	add("suite identity", stringsError(identityErrors))
	add("critical scenarios and results", stringsError(scenarioErrors))
	add("public CLI contracts", stringsError(contractErrors))
	add("statement coverage non-regression", stringsError(coverageErrors))
	if err := writeGateReport(opts.output, "comparison", gate); err != nil {
		return err
	}
	if gate.Status != "pass" {
		return fmt.Errorf("baseline/candidate comparison failed; see %s", filepath.Join(opts.output, "comparison.json"))
	}
	fmt.Fprintf(os.Stdout, "baseline/candidate E2E comparison passed for: %s\n", strings.Join(platforms, ", "))
	return nil
}

func compareCriticalScenarios(baseline, candidate featureCoverage) error {
	base := make(map[string]string)
	cand := make(map[string]string)
	for _, item := range baseline.Scenarios {
		if item.Critical && item.Result != "not_applicable" {
			base[item.ScenarioID] = item.Result
		}
	}
	for _, item := range candidate.Scenarios {
		if item.Critical && item.Result != "not_applicable" {
			cand[item.ScenarioID] = item.Result
		}
	}
	baseIDs := sortedMapKeys(base)
	candidateIDs := sortedMapKeys(cand)
	if !equalStrings(baseIDs, candidateIDs) {
		return fmt.Errorf("critical scenario IDs differ: baseline=%v candidate=%v", baseIDs, candidateIDs)
	}
	var changed []string
	for _, id := range baseIDs {
		if base[id] != cand[id] || (cand[id] != "pass" && cand[id] != "expected_skip") {
			changed = append(changed, fmt.Sprintf("%s:%s->%s", id, base[id], cand[id]))
		}
	}
	if len(changed) > 0 {
		return fmt.Errorf("scenario results changed: %s", strings.Join(changed, ", "))
	}
	return nil
}

func canonicalJSONFile(filename string) ([]byte, error) {
	var value any
	if err := readJSON(filename, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func writeGateReport(directory, base string, report gateReport) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(directory, base+".json"), report); err != nil {
		return err
	}
	var out strings.Builder
	fmt.Fprintf(&out, "# E2E %s\n\n", report.Mode)
	fmt.Fprintf(&out, "Status: **%s**  \n", strings.ToUpper(report.Status))
	fmt.Fprintf(&out, "Platforms: `%s`  \n", strings.Join(report.Platforms, "`, `"))
	if report.SuiteHash != "" {
		fmt.Fprintf(&out, "Suite hash: `%s`\n", report.SuiteHash)
	}
	if report.BaselineCommit != "" {
		fmt.Fprintf(&out, "\nBaseline: `%s`, commit `%s`, run [%s](%s) attempt `%s`, Go `%s`, reporter `%s`  \n", report.BaselineRepository, report.BaselineCommit, report.BaselineRunID, report.BaselineRunURL, report.BaselineRunAttempt, report.BaselineGoVersion, report.BaselineReporter)
		fmt.Fprintf(&out, "Candidate: `%s`, commit `%s`, run [%s](%s) attempt `%s`, Go `%s`, reporter `%s`\n", report.CandidateRepository, report.CandidateCommit, report.CandidateRunID, report.CandidateRunURL, report.CandidateRunAttempt, report.CandidateGoVersion, report.CandidateReporter)
	}
	out.WriteString("\n| Check | Status | Detail |\n|---|---|---|\n")
	for _, check := range report.Checks {
		fmt.Fprintf(&out, "| %s | **%s** | %s |\n", markdownCell(check.Name), strings.ToUpper(check.Status), markdownCell(check.Detail))
	}
	return writeFileAtomic(filepath.Join(directory, base+".md"), []byte(out.String()), 0o600)
}

func sortedReportPlatforms(entries map[string]reportSetEntry) []string {
	result := make([]string, 0, len(entries))
	for platform := range entries {
		result = append(result, platform)
	}
	sort.Strings(result)
	return result
}

func sortedMapKeys(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
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

func combineDiscoveryErrors(err error, details []string) error {
	var values []string
	if err != nil {
		values = append(values, err.Error())
	}
	values = append(values, details...)
	return stringsError(values)
}

func stringsError(values []string) error {
	if len(values) == 0 {
		return nil
	}
	return errors.New(strings.Join(values, "; "))
}
