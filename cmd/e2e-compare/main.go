// Command e2e-compare compares two E2E matrices after each matrix has been
// validated against its own exact source checkout and Go toolchain.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	schemaVersion  = "1"
	sentinelPrefix = "ENV_VAULT_E2E_SENTINEL_"
)

var requiredPlatforms = []string{
	"darwin-amd64",
	"darwin-arm64",
	"linux-amd64",
	"linux-arm64",
	"windows-amd64",
}

var releaseVersionPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type options struct {
	baseline            string
	candidate           string
	output              string
	coverageTolerance   float64
	expectedSuiteHash   string
	baselineValidation  string
	candidateValidation string
	baselineCommit      string
	baselineRunID       string
	baselineRunURL      string
	baselineRunAttempt  string
	baselineRepository  string
	baselineReporter    string
	candidateCommit     string
	candidateRunID      string
	candidateRunURL     string
	candidateRunAttempt string
	candidateRepository string
	candidateReporter   string
	candidateVersion    string
}

type counts struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
	Missing int `json:"missing"`
}

type metadata struct {
	SchemaVersion         string   `json:"schema_version"`
	Phase                 string   `json:"phase"`
	Status                string   `json:"status"`
	CommitSHA             string   `json:"commit_sha"`
	GitHubRunID           string   `json:"github_run_id"`
	GitHubRunURL          string   `json:"github_run_url"`
	GitHubRunAttempt      string   `json:"github_run_attempt"`
	GitHubRepository      string   `json:"github_repository"`
	GoVersion             string   `json:"go_version"`
	Platform              string   `json:"platform"`
	SubjectKind           string   `json:"subject_kind"`
	SuiteHash             string   `json:"suite_hash"`
	GotestsumVersion      string   `json:"gotestsum_version"`
	Counts                counts   `json:"counts"`
	StatementCoverage     float64  `json:"statement_coverage_percent"`
	ExpectedPlatformSkips []string `json:"expected_platform_skips"`
	UnexpectedSkips       []string `json:"unexpected_skips"`
	Failures              []string `json:"failures"`
}

type scenarioTrace struct {
	ScenarioID string `json:"scenario_id"`
	Critical   bool   `json:"critical"`
	Result     string `json:"result"`
}

type featureCoverage struct {
	SchemaVersion       string          `json:"schema_version"`
	Platform            string          `json:"platform"`
	SuiteHash           string          `json:"suite_hash"`
	CriticalTotal       int             `json:"critical_total"`
	CriticalCovered     int             `json:"critical_covered"`
	CriticalCoveragePct float64         `json:"critical_coverage_percent"`
	UnexpectedSkips     []string        `json:"unexpected_skips"`
	MissingCritical     []string        `json:"missing_critical"`
	Scenarios           []scenarioTrace `json:"scenarios"`
}

type leakScan struct {
	SchemaVersion string `json:"schema_version"`
	Status        string `json:"status"`
	Detected      bool   `json:"detected"`
	Occurrences   int    `json:"occurrences"`
	RegistryCount int    `json:"registry_records"`
	Findings      []any  `json:"findings"`
}

type check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type matrixValidation struct {
	SchemaVersion string   `json:"schema_version"`
	Mode          string   `json:"mode"`
	Status        string   `json:"status"`
	Phase         string   `json:"phase"`
	SuiteHash     string   `json:"suite_hash"`
	Platforms     []string `json:"platforms"`
	Checks        []check  `json:"checks"`
}

type reportEntry struct {
	Directory string
	Metadata  metadata
	Coverage  featureCoverage
	Contracts []byte
}

type comparisonReport struct {
	SchemaVersion       string    `json:"schema_version"`
	Mode                string    `json:"mode"`
	Status              string    `json:"status"`
	SuiteHash           string    `json:"suite_hash,omitempty"`
	Platforms           []string  `json:"platforms"`
	GeneratedAt         time.Time `json:"generated_at"`
	BaselineCommit      string    `json:"baseline_commit"`
	BaselineRunID       string    `json:"baseline_run_id"`
	BaselineRunURL      string    `json:"baseline_run_url"`
	BaselineRunAttempt  string    `json:"baseline_run_attempt"`
	BaselineRepository  string    `json:"baseline_repository"`
	BaselineReporter    string    `json:"baseline_reporter"`
	BaselineValidation  string    `json:"baseline_validation_outcome"`
	BaselineGoVersion   string    `json:"baseline_go_version,omitempty"`
	CandidateCommit     string    `json:"candidate_commit"`
	CandidateRunID      string    `json:"candidate_run_id"`
	CandidateRunURL     string    `json:"candidate_run_url"`
	CandidateRunAttempt string    `json:"candidate_run_attempt"`
	CandidateRepository string    `json:"candidate_repository"`
	CandidateReporter   string    `json:"candidate_reporter"`
	CandidateVersion    string    `json:"candidate_version"`
	CandidateValidation string    `json:"candidate_validation_outcome"`
	CandidateGoVersion  string    `json:"candidate_go_version,omitempty"`
	Checks              []check   `json:"checks"`
}

func main() {
	if err := realMain(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "e2e-compare:", err)
		os.Exit(1)
	}
}

func realMain(args []string) error {
	opts, err := parseFlags(args)
	if err != nil {
		return err
	}
	report, compareErr := compare(opts)
	if err := writeReport(opts.output, report); err != nil {
		return err
	}
	if compareErr != nil {
		return fmt.Errorf("baseline/candidate comparison failed; see %s: %w", filepath.Join(opts.output, "comparison.json"), compareErr)
	}
	fmt.Fprintf(os.Stdout, "baseline/candidate E2E comparison passed for: %s\n", strings.Join(requiredPlatforms, ", "))
	return nil
}

func parseFlags(args []string) (options, error) {
	var opts options
	set := flag.NewFlagSet("e2e-compare", flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	set.StringVar(&opts.baseline, "baseline", "", "validated baseline artifact root")
	set.StringVar(&opts.candidate, "candidate", "", "validated candidate artifact root")
	set.StringVar(&opts.output, "output", "", "comparison output directory")
	set.Float64Var(&opts.coverageTolerance, "coverage-tolerance", 0, "allowed statement-coverage decrease")
	set.StringVar(&opts.expectedSuiteHash, "expected-suite-hash", "", "exact semantic E2E suite SHA-256")
	set.StringVar(&opts.baselineValidation, "baseline-validation-outcome", "", "baseline source-validation step outcome")
	set.StringVar(&opts.candidateValidation, "candidate-validation-outcome", "", "candidate source-validation step outcome")
	set.StringVar(&opts.baselineCommit, "baseline-commit", "", "exact baseline commit")
	set.StringVar(&opts.baselineRunID, "baseline-run-id", "", "exact baseline run ID")
	set.StringVar(&opts.baselineRunURL, "baseline-run-url", "", "exact baseline run URL")
	set.StringVar(&opts.baselineRunAttempt, "baseline-run-attempt", "", "exact baseline run attempt")
	set.StringVar(&opts.baselineRepository, "baseline-repository", "", "exact baseline repository")
	set.StringVar(&opts.baselineReporter, "baseline-reporter", "", "exact baseline reporter")
	set.StringVar(&opts.candidateCommit, "candidate-commit", "", "exact candidate commit")
	set.StringVar(&opts.candidateRunID, "candidate-run-id", "", "exact candidate run ID")
	set.StringVar(&opts.candidateRunURL, "candidate-run-url", "", "exact candidate run URL")
	set.StringVar(&opts.candidateRunAttempt, "candidate-run-attempt", "", "exact candidate run attempt")
	set.StringVar(&opts.candidateRepository, "candidate-repository", "", "exact candidate repository")
	set.StringVar(&opts.candidateReporter, "candidate-reporter", "", "exact candidate reporter")
	set.StringVar(&opts.candidateVersion, "candidate-version", "", "exact version embedded in candidate binaries")
	if err := set.Parse(args); err != nil {
		return options{}, err
	}
	if set.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(set.Args(), " "))
	}
	values := []string{
		opts.baseline, opts.candidate, opts.output, opts.expectedSuiteHash, opts.baselineValidation, opts.candidateValidation,
		opts.baselineCommit, opts.baselineRunID, opts.baselineRunURL, opts.baselineRunAttempt, opts.baselineRepository, opts.baselineReporter,
		opts.candidateCommit, opts.candidateRunID, opts.candidateRunURL, opts.candidateRunAttempt, opts.candidateRepository, opts.candidateReporter, opts.candidateVersion,
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return options{}, errors.New("all report, suite, and baseline/candidate identity flags are required")
		}
	}
	if !validSHA256(opts.expectedSuiteHash) || !validCommit(opts.baselineCommit) || !validCommit(opts.candidateCommit) {
		return options{}, errors.New("suite hash must be SHA-256 and commit values must be full Git SHAs")
	}
	if !numeric(opts.baselineRunID) || !numeric(opts.candidateRunID) {
		return options{}, errors.New("run IDs must be numeric")
	}
	if opts.coverageTolerance < 0 || math.IsNaN(opts.coverageTolerance) || math.IsInf(opts.coverageTolerance, 0) {
		return options{}, errors.New("coverage tolerance must be finite and non-negative")
	}
	return opts, nil
}

func compare(opts options) (comparisonReport, error) {
	report := comparisonReport{
		SchemaVersion:       schemaVersion,
		Mode:                "compare",
		Status:              "pass",
		SuiteHash:           opts.expectedSuiteHash,
		Platforms:           append([]string(nil), requiredPlatforms...),
		GeneratedAt:         time.Now().UTC(),
		BaselineCommit:      opts.baselineCommit,
		BaselineRunID:       opts.baselineRunID,
		BaselineRunURL:      opts.baselineRunURL,
		BaselineRunAttempt:  opts.baselineRunAttempt,
		BaselineRepository:  opts.baselineRepository,
		BaselineReporter:    opts.baselineReporter,
		BaselineValidation:  opts.baselineValidation,
		CandidateCommit:     opts.candidateCommit,
		CandidateRunID:      opts.candidateRunID,
		CandidateRunURL:     opts.candidateRunURL,
		CandidateRunAttempt: opts.candidateRunAttempt,
		CandidateRepository: opts.candidateRepository,
		CandidateReporter:   opts.candidateReporter,
		CandidateVersion:    opts.candidateVersion,
		CandidateValidation: opts.candidateValidation,
	}
	add := func(name string, err error) {
		item := check{Name: name, Status: "pass"}
		if err != nil {
			item.Status = "fail"
			item.Detail = err.Error()
			report.Status = "fail"
		}
		report.Checks = append(report.Checks, item)
	}

	add("baseline matrix validation", validateMatrixOutcomeAndRecord(opts.baselineValidation, opts.baseline, "baseline", opts.expectedSuiteHash))
	add("candidate matrix validation", validateMatrixOutcomeAndRecord(opts.candidateValidation, opts.candidate, "candidate", opts.expectedSuiteHash))
	baseline, baselineErr := discoverReports(opts.baseline)
	candidate, candidateErr := discoverReports(opts.candidate)
	add("baseline reports", baselineErr)
	add("candidate reports", candidateErr)
	add("platform set", comparePlatformSet(baseline, candidate))

	var identityErrors, scenarioErrors, contractErrors, coverageErrors, leakErrors []string
	for _, platform := range requiredPlatforms {
		base, baseOK := baseline[platform]
		cand, candOK := candidate[platform]
		if !baseOK || !candOK {
			continue
		}
		identityErrors = append(identityErrors, validateIdentity(base.Metadata, "baseline", opts.baselineCommit, opts.baselineRunID, opts.baselineRunURL, opts.baselineRunAttempt, opts.baselineRepository, opts.baselineReporter, opts.expectedSuiteHash)...)
		identityErrors = append(identityErrors, validateIdentity(cand.Metadata, "candidate", opts.candidateCommit, opts.candidateRunID, opts.candidateRunURL, opts.candidateRunAttempt, opts.candidateRepository, opts.candidateReporter, opts.expectedSuiteHash)...)
		if report.BaselineGoVersion == "" {
			report.BaselineGoVersion = base.Metadata.GoVersion
			report.CandidateGoVersion = cand.Metadata.GoVersion
		} else {
			if report.BaselineGoVersion != base.Metadata.GoVersion {
				identityErrors = append(identityErrors, platform+": inconsistent baseline Go version")
			}
			if report.CandidateGoVersion != cand.Metadata.GoVersion {
				identityErrors = append(identityErrors, platform+": inconsistent candidate Go version")
			}
		}
		if !equalStrings(sortedCopy(base.Metadata.ExpectedPlatformSkips), sortedCopy(cand.Metadata.ExpectedPlatformSkips)) {
			identityErrors = append(identityErrors, platform+": expected platform skips changed")
		}
		if err := compareCritical(base.Coverage, cand.Coverage); err != nil {
			scenarioErrors = append(scenarioErrors, platform+": "+err.Error())
		}
		normalizedCandidateContracts, err := normalizeReleaseVersionContract(cand.Contracts, opts.candidateVersion)
		if err != nil {
			contractErrors = append(contractErrors, platform+": "+err.Error())
		} else if !bytes.Equal(base.Contracts, normalizedCandidateContracts) {
			contractErrors = append(contractErrors, platform+": normalized stdout/stderr, exit-code, or JSON contract changed")
		}
		if cand.Metadata.StatementCoverage+opts.coverageTolerance < base.Metadata.StatementCoverage {
			coverageErrors = append(coverageErrors, fmt.Sprintf("%s: %.2f%% -> %.2f%% (tolerance %.2f)", platform, base.Metadata.StatementCoverage, cand.Metadata.StatementCoverage, opts.coverageTolerance))
		}
		if err := validateLeakScan(base.Directory); err != nil {
			leakErrors = append(leakErrors, platform+" baseline: "+err.Error())
		}
		if err := validateLeakScan(cand.Directory); err != nil {
			leakErrors = append(leakErrors, platform+" candidate: "+err.Error())
		}
	}
	add("suite and run identity", stringsError(identityErrors))
	add("critical scenarios and results", stringsError(scenarioErrors))
	add("public CLI contracts", stringsError(contractErrors))
	add("statement coverage non-regression", stringsError(coverageErrors))
	add("secret leak gates", stringsError(leakErrors))
	if report.Status != "pass" {
		return report, errors.New("one or more comparison gates failed")
	}
	return report, nil
}

func validateMatrixOutcomeAndRecord(outcome, root, phase, suiteHash string) error {
	if outcome != "success" {
		return fmt.Errorf("source-validation step outcome=%q, want success", outcome)
	}
	var matrix matrixValidation
	if err := readJSON(filepath.Join(root, "matrix-validation.json"), &matrix); err != nil {
		return err
	}
	platforms := sortedCopy(matrix.Platforms)
	if matrix.SchemaVersion != schemaVersion || matrix.Mode != "validate-matrix" || matrix.Status != "pass" || matrix.Phase != phase || matrix.SuiteHash != suiteHash || !equalStrings(platforms, requiredPlatforms) {
		return fmt.Errorf("matrix identity/status mismatch: phase=%s status=%s suite=%s platforms=%v", matrix.Phase, matrix.Status, matrix.SuiteHash, platforms)
	}
	if len(matrix.Checks) == 0 {
		return errors.New("matrix validation contains no checks")
	}
	for _, item := range matrix.Checks {
		if item.Name == "" || item.Status != "pass" {
			return fmt.Errorf("matrix check %q has status %q", item.Name, item.Status)
		}
	}
	return nil
}

func discoverReports(root string) (map[string]reportEntry, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("report root is not a real directory: %s", abs)
	}
	entries := make(map[string]reportEntry)
	var problems []string
	err = filepath.WalkDir(abs, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("report tree contains symlink: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		fileInfo, err := entry.Info()
		if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Size() > 64<<20 {
			return fmt.Errorf("report entry is not a bounded regular file: %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(sentinelPrefix)) {
			return fmt.Errorf("secret sentinel marker found in %s", path)
		}
		if entry.Name() != "metadata.json" {
			return nil
		}
		directory := filepath.Dir(path)
		var meta metadata
		var coverage featureCoverage
		if err := decodeJSON(data, &meta); err != nil {
			problems = append(problems, path+": "+err.Error())
			return nil
		}
		if err := readJSON(filepath.Join(directory, "feature-coverage.json"), &coverage); err != nil {
			problems = append(problems, path+": feature coverage: "+err.Error())
			return nil
		}
		contracts, err := canonicalJSON(filepath.Join(directory, "contracts.json"))
		if err != nil {
			problems = append(problems, path+": contracts: "+err.Error())
			return nil
		}
		if err := validateReportBasics(meta, coverage); err != nil {
			problems = append(problems, path+": "+err.Error())
			return nil
		}
		if _, exists := entries[meta.Platform]; exists {
			return fmt.Errorf("duplicate platform report: %s", meta.Platform)
		}
		entries[meta.Platform] = reportEntry{Directory: directory, Metadata: meta, Coverage: coverage, Contracts: contracts}
		return nil
	})
	if err != nil {
		return entries, err
	}
	return entries, stringsError(problems)
}

func validateReportBasics(meta metadata, coverage featureCoverage) error {
	if meta.SchemaVersion != schemaVersion || meta.Platform == "" || meta.Status != "pass" || meta.SubjectKind != "artifact" || !validSHA256(meta.SuiteHash) {
		return errors.New("metadata schema, platform, status, subject, or suite hash is invalid")
	}
	if meta.Counts.Failed != 0 || meta.Counts.Missing != 0 || len(meta.UnexpectedSkips) != 0 || len(meta.Failures) != 0 || meta.StatementCoverage < 0 || meta.StatementCoverage > 100 {
		return errors.New("metadata records failed/missing/unexpected/leaked gate state")
	}
	if coverage.SchemaVersion != schemaVersion || coverage.Platform != meta.Platform || coverage.SuiteHash != meta.SuiteHash || coverage.CriticalTotal == 0 || coverage.CriticalCovered != coverage.CriticalTotal || coverage.CriticalCoveragePct != 100 || len(coverage.UnexpectedSkips) != 0 || len(coverage.MissingCritical) != 0 || len(coverage.Scenarios) == 0 {
		return errors.New("feature coverage does not prove 100% critical coverage")
	}
	return nil
}

func validateIdentity(meta metadata, phase, commit, runID, runURL, attempt, repository, reporter, suiteHash string) []string {
	var problems []string
	if meta.Phase != phase || meta.CommitSHA != commit || meta.GitHubRunID != runID || meta.GitHubRunURL != runURL || meta.GitHubRunAttempt != attempt || meta.GitHubRepository != repository || meta.GotestsumVersion != reporter || meta.SuiteHash != suiteHash {
		problems = append(problems, fmt.Sprintf("%s: identity mismatch for platform %s", phase, meta.Platform))
	}
	if !strings.HasPrefix(meta.GoVersion, "go") || meta.GoVersion == "go" {
		problems = append(problems, fmt.Sprintf("%s: invalid Go version for platform %s", phase, meta.Platform))
	}
	return problems
}

func comparePlatformSet(baseline, candidate map[string]reportEntry) error {
	base := sortedKeys(baseline)
	cand := sortedKeys(candidate)
	if !equalStrings(base, requiredPlatforms) || !equalStrings(cand, requiredPlatforms) {
		return fmt.Errorf("required=%v baseline=%v candidate=%v", requiredPlatforms, base, cand)
	}
	return nil
}

func normalizeReleaseVersionContract(contracts []byte, candidateVersion string) ([]byte, error) {
	if !releaseVersionPattern.MatchString(candidateVersion) {
		return contracts, nil
	}
	var document map[string]any
	if err := decodeJSON(contracts, &document); err != nil {
		return nil, fmt.Errorf("decode candidate contracts for release-version normalization: %w", err)
	}
	scenarios, ok := document["scenarios"].(map[string]any)
	if !ok {
		return nil, errors.New("candidate contracts scenarios are missing or malformed")
	}
	versionScenario, ok := scenarios["CLI_VERSION_FORMS"].(map[string]any)
	if !ok {
		return nil, errors.New("candidate CLI_VERSION_FORMS contract is missing or malformed")
	}
	observations, ok := versionScenario["observations"].([]any)
	if !ok || len(observations) != 3 {
		return nil, errors.New("candidate CLI_VERSION_FORMS observations must contain exactly three entries")
	}
	seen := make(map[string]bool, 3)
	for index, raw := range observations {
		observation, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("candidate CLI_VERSION_FORMS observation %d is malformed", index+1)
		}
		arguments, ok := contractArguments(observation["args"])
		if !ok {
			return nil, fmt.Errorf("candidate CLI_VERSION_FORMS observation %d arguments are malformed", index+1)
		}
		key := strings.Join(arguments, "\x00")
		if seen[key] {
			return nil, fmt.Errorf("candidate CLI_VERSION_FORMS repeats arguments %q", arguments)
		}
		seen[key] = true
		stdout, ok := observation["stdout"].(string)
		if !ok {
			return nil, fmt.Errorf("candidate CLI_VERSION_FORMS stdout for %q is malformed", arguments)
		}
		switch key {
		case "--version", "version":
			if stdout != candidateVersion+"\n" {
				return nil, fmt.Errorf("candidate CLI_VERSION_FORMS stdout for %q does not equal the exact candidate version", arguments)
			}
			observation["stdout"] = "<VERSION>\n"
		case "--json\x00--version":
			normalized, err := normalizeJSONVersionOutput(stdout, candidateVersion)
			if err != nil {
				return nil, fmt.Errorf("candidate CLI_VERSION_FORMS JSON stdout: %w", err)
			}
			observation["stdout"] = normalized
		default:
			return nil, fmt.Errorf("candidate CLI_VERSION_FORMS has unexpected arguments %q", arguments)
		}
	}
	for _, required := range []string{"--version", "version", "--json\x00--version"} {
		if !seen[required] {
			return nil, fmt.Errorf("candidate CLI_VERSION_FORMS is missing arguments %q", strings.Split(required, "\x00"))
		}
	}
	return json.Marshal(document)
}

func contractArguments(value any) ([]string, bool) {
	raw, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, len(raw))
	for index, item := range raw {
		argument, ok := item.(string)
		if !ok {
			return nil, false
		}
		result[index] = argument
	}
	return result, true
}

func normalizeJSONVersionOutput(stdout, candidateVersion string) (string, error) {
	if !strings.HasSuffix(stdout, "\n") || strings.HasSuffix(stdout, "\r\n") || strings.HasSuffix(strings.TrimSuffix(stdout, "\n"), "\n") {
		return "", errors.New("output must end in exactly one newline")
	}
	var payload map[string]any
	if err := decodeJSON([]byte(stdout), &payload); err != nil {
		return "", fmt.Errorf("decode output: %w", err)
	}
	if payload["command"] != "version" {
		return "", errors.New("command is not version")
	}
	data, ok := payload["data"].(map[string]any)
	if !ok || data["version"] != candidateVersion {
		return "", errors.New("data.version does not equal the exact candidate version")
	}
	data["version"] = "<VERSION>"
	normalized, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(normalized) + "\n", nil
}

func compareCritical(baseline, candidate featureCoverage) error {
	base := criticalResults(baseline)
	cand := criticalResults(candidate)
	baseIDs := sortedStringKeys(base)
	candIDs := sortedStringKeys(cand)
	if !equalStrings(baseIDs, candIDs) {
		return fmt.Errorf("critical scenario IDs differ: baseline=%v candidate=%v", baseIDs, candIDs)
	}
	var changed []string
	for _, id := range baseIDs {
		if base[id] != cand[id] || (cand[id] != "pass" && cand[id] != "expected_skip") {
			changed = append(changed, fmt.Sprintf("%s:%s->%s", id, base[id], cand[id]))
		}
	}
	return stringsError(changed)
}

func criticalResults(coverage featureCoverage) map[string]string {
	result := make(map[string]string)
	for _, item := range coverage.Scenarios {
		if item.Critical && item.Result != "not_applicable" {
			result[item.ScenarioID] = item.Result
		}
	}
	return result
}

func validateLeakScan(directory string) error {
	var scan leakScan
	if err := readJSON(filepath.Join(directory, "leak-scan.json"), &scan); err != nil {
		return err
	}
	if scan.SchemaVersion != schemaVersion || scan.Status != "pass" || scan.Detected || scan.Occurrences != 0 || scan.RegistryCount == 0 || len(scan.Findings) != 0 {
		return errors.New("leak scan did not prove zero sentinel disclosure")
	}
	return nil
}

func writeReport(directory string, report comparisonReport) error {
	if err := prepareOutputDirectory(directory); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := atomicWrite(filepath.Join(directory, "comparison.json"), data); err != nil {
		return err
	}
	var markdown strings.Builder
	fmt.Fprintf(&markdown, "# E2E cross-source comparison\n\nStatus: **%s**  \n", strings.ToUpper(report.Status))
	fmt.Fprintf(&markdown, "Platforms: `%s`  \nSuite hash: `%s`\n", strings.Join(report.Platforms, "`, `"), report.SuiteHash)
	fmt.Fprintf(&markdown, "\nBaseline: `%s`, commit `%s`, run [%s](%s) attempt `%s`, Go `%s`, reporter `%s`  \n", report.BaselineRepository, report.BaselineCommit, report.BaselineRunID, report.BaselineRunURL, report.BaselineRunAttempt, report.BaselineGoVersion, report.BaselineReporter)
	fmt.Fprintf(&markdown, "Candidate: `%s`, commit `%s`, run [%s](%s) attempt `%s`, Go `%s`, reporter `%s`\n", report.CandidateRepository, report.CandidateCommit, report.CandidateRunID, report.CandidateRunURL, report.CandidateRunAttempt, report.CandidateGoVersion, report.CandidateReporter)
	markdown.WriteString("\n| Check | Status | Detail |\n|---|---|---|\n")
	for _, item := range report.Checks {
		fmt.Fprintf(&markdown, "| %s | **%s** | %s |\n", markdownCell(item.Name), strings.ToUpper(item.Status), markdownCell(item.Detail))
	}
	return atomicWrite(filepath.Join(directory, "comparison.md"), []byte(markdown.String()))
}

func atomicWrite(filename string, data []byte) error {
	directory := filepath.Dir(filename)
	file, err := os.CreateTemp(directory, ".e2e-compare-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, filename)
}

func readJSON(filename string, destination any) error {
	data, err := readBoundedRegular(filename, 64<<20)
	if err != nil {
		return err
	}
	return decodeJSON(data, destination)
}

func readBoundedRegular(filename string, limit int64) ([]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("not a bounded regular file: %s", filename)
	}
	return os.ReadFile(filename)
}

func prepareOutputDirectory(directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("comparison output is not a real directory: %s", directory)
	}
	return nil
}

func decodeJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON value")
	}
	return nil
}

func canonicalJSON(filename string) ([]byte, error) {
	var value any
	if err := readJSON(filename, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func stringsError(values []string) error {
	if len(values) == 0 {
		return nil
	}
	return errors.New(strings.Join(values, "; "))
}

func sortedKeys(values map[string]reportEntry) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func sortedStringKeys(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func sortedCopy(values []string) []string {
	result := append([]string(nil), values...)
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

func validSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && strings.ToLower(value) == value
}

func validCommit(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 20 && strings.ToLower(value) == value
}

func numeric(value string) bool {
	_, err := strconv.ParseUint(value, 10, 64)
	return err == nil && value != ""
}

func markdownCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.ReplaceAll(value, "\n", " ")
}
