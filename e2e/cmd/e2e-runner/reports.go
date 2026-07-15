package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	htmlstd "html"
	"io"
	"math"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type resultCounts struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
	Missing int `json:"missing"`
}

type runMetadata struct {
	SchemaVersion         string            `json:"schema_version"`
	Phase                 string            `json:"phase"`
	Status                string            `json:"status"`
	CommitSHA             string            `json:"commit_sha"`
	GitHubRunID           string            `json:"github_run_id"`
	GitHubRunURL          string            `json:"github_run_url"`
	GitHubRunAttempt      string            `json:"github_run_attempt"`
	GitHubRepository      string            `json:"github_repository"`
	GoVersion             string            `json:"go_version"`
	BinaryGoVersion       string            `json:"binary_go_version"`
	BinaryGOOS            string            `json:"binary_goos"`
	BinaryGOARCH          string            `json:"binary_goarch"`
	GOOS                  string            `json:"goos"`
	GOARCH                string            `json:"goarch"`
	RunnerOS              string            `json:"runner_os"`
	Platform              string            `json:"platform"`
	BinarySHA256          string            `json:"binary_sha256"`
	SubjectKind           string            `json:"subject_kind"`
	SuiteHash             string            `json:"suite_hash"`
	GotestsumVersion      string            `json:"gotestsum_version"`
	StartedAt             time.Time         `json:"started_at"`
	EndedAt               time.Time         `json:"ended_at"`
	DurationMS            int64             `json:"duration_ms"`
	Counts                resultCounts      `json:"counts"`
	StatementCoverage     float64           `json:"statement_coverage_percent"`
	ExpectedPlatformSkips []string          `json:"expected_platform_skips"`
	UnexpectedSkips       []string          `json:"unexpected_skips"`
	SentinelRecords       int               `json:"sentinel_registry_records"`
	ContractRecords       int               `json:"contract_records"`
	Artifact              artifactEvidence  `json:"artifact"`
	EvidenceSHA256        map[string]string `json:"evidence_sha256"`
	Commands              []commandResult   `json:"commands"`
	Failures              []string          `json:"failures"`
}

type summaryReport struct {
	SchemaVersion         string       `json:"schema_version"`
	Phase                 string       `json:"phase"`
	Status                string       `json:"status"`
	Platform              string       `json:"platform"`
	SuiteHash             string       `json:"suite_hash"`
	Counts                resultCounts `json:"counts"`
	StatementCoverage     float64      `json:"statement_coverage_percent"`
	CriticalCoverage      float64      `json:"critical_feature_coverage_percent"`
	ExpectedPlatformSkips []string     `json:"expected_platform_skips"`
	UnexpectedSkips       []string     `json:"unexpected_skips"`
	Failures              []string     `json:"failures"`
}

type leakFinding struct {
	Path        string `json:"path"`
	Occurrences int    `json:"occurrences"`
}

type leakScanReport struct {
	SchemaVersion   string        `json:"schema_version"`
	Status          string        `json:"status"`
	Detected        bool          `json:"detected"`
	FilesScanned    int           `json:"files_scanned"`
	Occurrences     int           `json:"occurrences"`
	RegistryRecords int           `json:"registry_records"`
	Findings        []leakFinding `json:"findings"`
}

func writeJSON(filename string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return writeFileAtomic(filename, data, 0o600)
}

func writeFileAtomic(filename string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(filename), ".e2e-report-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		_ = os.Remove(filename)
	}
	return os.Rename(tempName, filename)
}

func writeFeatureMarkdown(filename string, coverage featureCoverage) error {
	return writeFileAtomic(filename, featureMarkdown(coverage), 0o600)
}

func featureMarkdown(coverage featureCoverage) []byte {
	var out strings.Builder
	out.WriteString("# env-vault E2E feature coverage\n\n")
	fmt.Fprintf(&out, "Platform: `%s`  \n", coverage.Platform)
	fmt.Fprintf(&out, "Suite hash: `%s`  \n", coverage.SuiteHash)
	fmt.Fprintf(&out, "Critical coverage: **%d/%d (%.2f%%)**\n\n", coverage.CriticalCovered, coverage.CriticalTotal, coverage.CriticalCoveragePct)
	out.WriteString("| Feature | Requirement | Scenario | Go test | Platforms | Result |\n")
	out.WriteString("|---|---|---|---|---|---|\n")
	for _, item := range coverage.Scenarios {
		fmt.Fprintf(&out, "| %s | %s | `%s` | `%s` | %s | **%s** |\n",
			markdownCell(item.Feature), markdownCell(item.Requirement), markdownCell(item.ScenarioID),
			markdownCell(item.GoTest), markdownCell(strings.Join(item.Platforms, ", ")), markdownCell(item.Result))
	}
	return []byte(out.String())
}

func writeSummaryReports(reportDir string, metadata runMetadata, coverage featureCoverage) error {
	summary := summaryReport{
		SchemaVersion:         reportSchemaVersion,
		Phase:                 metadata.Phase,
		Status:                metadata.Status,
		Platform:              metadata.Platform,
		SuiteHash:             metadata.SuiteHash,
		Counts:                metadata.Counts,
		StatementCoverage:     metadata.StatementCoverage,
		CriticalCoverage:      coverage.CriticalCoveragePct,
		ExpectedPlatformSkips: append([]string{}, metadata.ExpectedPlatformSkips...),
		UnexpectedSkips:       append([]string{}, metadata.UnexpectedSkips...),
		Failures:              append([]string{}, metadata.Failures...),
	}
	if err := writeJSON(filepath.Join(reportDir, "summary.json"), summary); err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(reportDir, "summary.md"), summaryMarkdown(metadata, coverage), 0o600)
}

func summaryMarkdown(metadata runMetadata, coverage featureCoverage) []byte {
	var out strings.Builder
	out.WriteString("# env-vault E2E summary\n\n")
	fmt.Fprintf(&out, "- Phase: `%s`\n", metadata.Phase)
	fmt.Fprintf(&out, "- Platform: `%s`\n", metadata.Platform)
	fmt.Fprintf(&out, "- Status: **%s**\n", strings.ToUpper(metadata.Status))
	fmt.Fprintf(&out, "- Scenarios: %d passed, %d failed, %d skipped, %d missing\n", metadata.Counts.Passed, metadata.Counts.Failed, metadata.Counts.Skipped, metadata.Counts.Missing)
	fmt.Fprintf(&out, "- Critical feature coverage: %.2f%%\n", coverage.CriticalCoveragePct)
	fmt.Fprintf(&out, "- Statement coverage: %.2f%%\n", metadata.StatementCoverage)
	fmt.Fprintf(&out, "- Suite hash: `%s`\n", metadata.SuiteHash)
	fmt.Fprintf(&out, "- Binary SHA-256: `%s`\n", metadata.BinarySHA256)
	fmt.Fprintf(&out, "- Binary Go version: `%s`\n", metadata.BinaryGoVersion)
	fmt.Fprintf(&out, "- Binary target: `%s/%s`\n", metadata.BinaryGOOS, metadata.BinaryGOARCH)
	fmt.Fprintf(&out, "- Commit SHA: `%s`\n", metadata.CommitSHA)
	fmt.Fprintf(&out, "- GitHub Actions run: [%s](%s) (attempt `%s`)\n", metadata.GitHubRunID, metadata.GitHubRunURL, metadata.GitHubRunAttempt)
	if len(metadata.ExpectedPlatformSkips) > 0 {
		fmt.Fprintf(&out, "- Expected platform skips: `%s`\n", strings.Join(metadata.ExpectedPlatformSkips, "`, `"))
	}
	if len(metadata.UnexpectedSkips) > 0 {
		fmt.Fprintf(&out, "- Unexpected skips: `%s`\n", strings.Join(metadata.UnexpectedSkips, "`, `"))
	}
	if len(metadata.Failures) > 0 {
		out.WriteString("\n## Failures\n\n")
		for _, failure := range metadata.Failures {
			fmt.Fprintf(&out, "- %s\n", markdownCell(failure))
		}
	}
	return []byte(out.String())
}

func markdownCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}

func countsFromCoverage(coverage featureCoverage) resultCounts {
	var counts resultCounts
	for _, item := range coverage.Scenarios {
		switch item.Result {
		case "pass":
			counts.Passed++
		case "fail":
			counts.Failed++
		case "skip", "expected_skip":
			counts.Skipped++
		case "missing":
			counts.Missing++
		}
	}
	return counts
}

func parseCoveragePercent(filename string) (float64, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return 0, err
	}
	pattern := regexp.MustCompile(`(?m)^total:\s+\(statements\)\s+([0-9]+(?:\.[0-9]+)?)%\s*$`)
	match := pattern.FindSubmatch(data)
	if len(match) != 2 {
		return 0, errors.New("coverage.txt has no total statements percentage")
	}
	value, err := strconv.ParseFloat(string(match[1]), 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}

func validateReportDirectory(directory string) (runMetadata, featureCoverage, error) {
	var metadata runMetadata
	var coverage featureCoverage
	repoRoot, err := findRepoRoot()
	if err != nil {
		return metadata, coverage, fmt.Errorf("resolve checkout for report validation: %w", err)
	}
	if err := validateReportArtifactsSyntax(directory); err != nil {
		return metadata, coverage, err
	}
	if err := readJSON(filepath.Join(directory, "metadata.json"), &metadata); err != nil {
		return metadata, coverage, fmt.Errorf("metadata.json: %w", err)
	}
	if err := readJSON(filepath.Join(directory, "feature-coverage.json"), &coverage); err != nil {
		return metadata, coverage, fmt.Errorf("feature-coverage.json: %w", err)
	}
	var summary summaryReport
	if err := readJSON(filepath.Join(directory, "summary.json"), &summary); err != nil {
		return metadata, coverage, fmt.Errorf("summary.json: %w", err)
	}
	var leaks leakScanReport
	if err := readJSON(filepath.Join(directory, "leak-scan.json"), &leaks); err != nil {
		return metadata, coverage, fmt.Errorf("leak-scan.json: %w", err)
	}
	if leaks.Detected || leaks.Status != "pass" {
		return metadata, coverage, errors.New("secret leak scan did not pass")
	}
	if metadata.SchemaVersion != reportSchemaVersion || metadata.Platform == "" || metadata.SuiteHash == "" || metadata.BinarySHA256 == "" {
		return metadata, coverage, errors.New("metadata has missing or unsupported identity fields")
	}
	if metadata.Phase != "baseline" && metadata.Phase != "candidate" {
		return metadata, coverage, fmt.Errorf("metadata has unsupported phase %q", metadata.Phase)
	}
	if metadata.CommitSHA == "" || metadata.GitHubRunID == "" || metadata.GitHubRunURL == "" || metadata.GitHubRunAttempt == "" || metadata.GitHubRepository == "" || metadata.GoVersion == "" || metadata.BinaryGoVersion == "" || metadata.BinaryGOOS == "" || metadata.BinaryGOARCH == "" || metadata.GOOS == "" || metadata.GOARCH == "" || metadata.RunnerOS == "" || metadata.SubjectKind == "" || metadata.StartedAt.IsZero() || metadata.EndedAt.IsZero() || metadata.EndedAt.Before(metadata.StartedAt) || metadata.DurationMS < 0 {
		return metadata, coverage, errors.New("metadata has incomplete execution evidence")
	}
	if metadata.DurationMS != metadata.EndedAt.Sub(metadata.StartedAt).Milliseconds() {
		return metadata, coverage, errors.New("metadata duration differs from start/end timestamps")
	}
	if err := validateGitHubRunIdentity(metadata); err != nil {
		return metadata, coverage, err
	}
	if !validSHA256(metadata.SuiteHash) || !validSHA256(metadata.BinarySHA256) {
		return metadata, coverage, errors.New("metadata suite or binary SHA-256 is invalid")
	}
	if !regexp.MustCompile(`^go[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(metadata.GoVersion) {
		return metadata, coverage, errors.New("metadata Go version is not an exact stable patch release")
	}
	if metadata.BinaryGoVersion != metadata.GoVersion || metadata.BinaryGOOS != metadata.GOOS || metadata.BinaryGOARCH != metadata.GOARCH {
		return metadata, coverage, errors.New("subject binary compiler or target differs from runner evidence")
	}
	if err := validateEvidenceDigests(directory, metadata.EvidenceSHA256); err != nil {
		return metadata, coverage, fmt.Errorf("evidence digests: %w", err)
	}
	if !regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(metadata.GotestsumVersion) {
		return metadata, coverage, errors.New("metadata has invalid pinned gotestsum version")
	}
	if err := validateArtifactEvidence(metadata); err != nil {
		return metadata, coverage, fmt.Errorf("artifact evidence: %w", err)
	}
	if metadata.Platform != metadata.GOOS+"-"+metadata.GOARCH {
		return metadata, coverage, errors.New("metadata platform does not match GOOS/GOARCH")
	}
	if !runnerOSMatchesGOOS(metadata.RunnerOS, metadata.GOOS) {
		return metadata, coverage, errors.New("metadata runner OS label does not match GOOS")
	}
	if metadata.Status != "pass" || summary.Status != "pass" || metadata.Counts.Failed != 0 || metadata.Counts.Missing != 0 || len(metadata.Failures) != 0 || len(summary.Failures) != 0 {
		return metadata, coverage, errors.New("report records a failed or incomplete E2E run")
	}
	if err := assertNoRedactionMarker(directory); err != nil {
		return metadata, coverage, fmt.Errorf("pass report redaction marker: %w", err)
	}
	if summary.SchemaVersion != reportSchemaVersion || summary.Phase != metadata.Phase || summary.Platform != metadata.Platform || summary.SuiteHash != metadata.SuiteHash || summary.Counts != metadata.Counts || math.Abs(summary.StatementCoverage-metadata.StatementCoverage) > 0.001 || math.Abs(summary.CriticalCoverage-coverage.CriticalCoveragePct) > 0.001 || !equalStrings(summary.ExpectedPlatformSkips, metadata.ExpectedPlatformSkips) || !equalStrings(summary.UnexpectedSkips, metadata.UnexpectedSkips) || !equalStrings(summary.Failures, metadata.Failures) {
		return metadata, coverage, errors.New("summary and metadata identity, results, or coverage differ")
	}
	if err := validateFeatureCoverageConsistency(coverage); err != nil {
		return metadata, coverage, fmt.Errorf("feature coverage: %w", err)
	}
	manifest, err := loadManifest(filepath.Join(repoRoot, "e2e", "scenarios.json"))
	if err != nil {
		return metadata, coverage, fmt.Errorf("load checked-in feature manifest: %w", err)
	}
	if err := validateFeatureCoverageAgainstManifest(coverage, manifest); err != nil {
		return metadata, coverage, fmt.Errorf("feature manifest trace: %w", err)
	}
	if len(metadata.UnexpectedSkips) != 0 || len(coverage.UnexpectedSkips) != 0 || len(coverage.MissingCritical) != 0 || coverage.CriticalCoveragePct != 100 {
		return metadata, coverage, errors.New("feature coverage gate did not pass")
	}
	if metadata.SuiteHash != coverage.SuiteHash || metadata.Platform != coverage.Platform {
		return metadata, coverage, errors.New("metadata and feature coverage identity mismatch")
	}
	if countsFromCoverage(coverage) != metadata.Counts {
		return metadata, coverage, errors.New("feature coverage counts differ from metadata")
	}
	if err := requireExactFile(filepath.Join(directory, "summary.md"), summaryMarkdown(metadata, coverage)); err != nil {
		return metadata, coverage, fmt.Errorf("summary.md: %w", err)
	}
	if err := requireExactFile(filepath.Join(directory, "feature-coverage.md"), featureMarkdown(coverage)); err != nil {
		return metadata, coverage, fmt.Errorf("feature-coverage.md: %w", err)
	}
	var expectedSkips, passedScenarios []string
	for _, item := range coverage.Scenarios {
		if item.Result == "expected_skip" {
			expectedSkips = append(expectedSkips, item.ScenarioID)
		}
		if item.Result == "pass" {
			passedScenarios = append(passedScenarios, item.ScenarioID)
		}
	}
	sort.Strings(expectedSkips)
	sort.Strings(passedScenarios)
	if !equalStrings(expectedSkips, metadata.ExpectedPlatformSkips) {
		return metadata, coverage, errors.New("recorded expected skips differ from feature results")
	}
	if err := validateContracts(filepath.Join(directory, "contracts.json"), metadata.Platform, passedScenarios, metadata.ContractRecords); err != nil {
		return metadata, coverage, fmt.Errorf("contracts.json: %w", err)
	}
	parsedCoverage, err := parseCoveragePercent(filepath.Join(directory, "coverage.txt"))
	if err != nil {
		return metadata, coverage, fmt.Errorf("coverage.txt: %w", err)
	}
	if math.Abs(parsedCoverage-metadata.StatementCoverage) > 0.001 {
		return metadata, coverage, errors.New("coverage.txt differs from metadata statement coverage")
	}
	profileCoverage, err := coverageProfilePercent(filepath.Join(directory, "coverage.out"))
	if err != nil {
		return metadata, coverage, fmt.Errorf("coverage.out: %w", err)
	}
	if err := validateCoverageProfileSources(filepath.Join(directory, "coverage.out"), repoRoot); err != nil {
		return metadata, coverage, fmt.Errorf("coverage.out source identity: %w", err)
	}
	if err := validateDerivedCoverageArtifacts(directory, repoRoot, metadata.GoVersion); err != nil {
		return metadata, coverage, fmt.Errorf("derived coverage evidence: %w", err)
	}
	if math.Abs(profileCoverage-metadata.StatementCoverage) > 0.051 {
		return metadata, coverage, fmt.Errorf("coverage.out statement coverage %.1f differs from metadata %.1f", profileCoverage, metadata.StatementCoverage)
	}
	if err := validateTestJSONLEvidence(filepath.Join(directory, "raw-test.jsonl"), coverage, 1, nil); err != nil {
		return metadata, coverage, fmt.Errorf("raw-test.jsonl evidence: %w", err)
	}
	if err := validateTestJSONLEvidence(filepath.Join(directory, "coverage-raw-test.jsonl"), coverage, 1, nil); err != nil {
		return metadata, coverage, fmt.Errorf("coverage-raw-test.jsonl evidence: %w", err)
	}
	if err := validateJUnitEvidence(filepath.Join(directory, "junit.xml"), coverage); err != nil {
		return metadata, coverage, fmt.Errorf("junit.xml evidence: %w", err)
	}
	if err := validateJUnitEvidence(filepath.Join(directory, "coverage-junit.xml"), coverage); err != nil {
		return metadata, coverage, fmt.Errorf("coverage-junit.xml evidence: %w", err)
	}
	if err := validateBurnInEvidence(directory, metadata, coverage, false); err != nil {
		return metadata, coverage, fmt.Errorf("burn-in evidence: %w", err)
	}
	if err := validateBurnInEvidence(directory, metadata, coverage, true); err != nil {
		return metadata, coverage, fmt.Errorf("locking burn-in evidence: %w", err)
	}
	var failureManifest struct {
		SchemaVersion string   `json:"schema_version"`
		Status        string   `json:"status"`
		Platform      string   `json:"platform"`
		Failures      []string `json:"failures"`
		IncludedFiles []string `json:"included_files"`
	}
	if err := readJSON(filepath.Join(directory, "sanitized-failure-bundle", "manifest.json"), &failureManifest); err != nil {
		return metadata, coverage, fmt.Errorf("failure bundle manifest: %w", err)
	}
	if failureManifest.SchemaVersion != reportSchemaVersion || failureManifest.Status != metadata.Status || failureManifest.Platform != metadata.Platform || !equalStrings(failureManifest.Failures, metadata.Failures) || !containsString(failureManifest.IncludedFiles, "command-output.txt") {
		return metadata, coverage, errors.New("failure bundle manifest differs from final metadata")
	}
	if leaks.SchemaVersion != reportSchemaVersion || leaks.RegistryRecords != metadata.SentinelRecords || metadata.SentinelRecords <= 0 || leaks.FilesScanned <= 0 || leaks.Occurrences != 0 || len(leaks.Findings) != 0 {
		return metadata, coverage, errors.New("leak scan identity or counts differ from metadata")
	}
	return metadata, coverage, nil
}

func validateReportArtifactsSyntax(directory string) error {
	if err := assertNoSentinelMarker(directory); err != nil {
		return fmt.Errorf("report tree security scan: %w", err)
	}
	required := []string{
		"junit.xml", "raw-test.jsonl", "summary.json", "summary.md",
		"feature-coverage.json", "feature-coverage.md", "coverage.out",
		"coverage.txt", "coverage.html", "coverage-percent.txt",
		"coverage-junit.xml", "coverage-raw-test.jsonl", "burn-in.jsonl",
		"locking-burn-in.jsonl", "metadata.json", "contracts.json",
		"leak-scan.json", "sanitized-failure-bundle/manifest.json",
		"sanitized-failure-bundle/command-output.txt",
	}
	for _, relative := range required {
		filename := filepath.Join(directory, filepath.FromSlash(relative))
		info, err := os.Lstat(filename)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("required report is missing, empty, non-regular, or a symlink: %s", relative)
		}
	}
	for _, relative := range []string{"metadata.json", "feature-coverage.json", "summary.json", "contracts.json", "leak-scan.json", "sanitized-failure-bundle/manifest.json"} {
		if err := validateJSONFile(filepath.Join(directory, filepath.FromSlash(relative))); err != nil {
			return fmt.Errorf("%s: %w", relative, err)
		}
	}
	if err := validateJSONLines(filepath.Join(directory, "raw-test.jsonl")); err != nil {
		return fmt.Errorf("raw-test.jsonl: %w", err)
	}
	for _, relative := range []string{"coverage-raw-test.jsonl", "burn-in.jsonl", "locking-burn-in.jsonl"} {
		if err := validateJSONLines(filepath.Join(directory, relative)); err != nil {
			return fmt.Errorf("%s: %w", relative, err)
		}
	}
	if err := validateXML(filepath.Join(directory, "junit.xml")); err != nil {
		return fmt.Errorf("junit.xml: %w", err)
	}
	if err := validateXML(filepath.Join(directory, "coverage-junit.xml")); err != nil {
		return fmt.Errorf("coverage-junit.xml: %w", err)
	}
	if _, err := parseCoveragePercent(filepath.Join(directory, "coverage.txt")); err != nil {
		return fmt.Errorf("coverage.txt: %w", err)
	}
	if err := validateCovdataPercent(filepath.Join(directory, "coverage-percent.txt")); err != nil {
		return fmt.Errorf("coverage-percent.txt: %w", err)
	}
	if err := validateCoverageProfile(filepath.Join(directory, "coverage.out")); err != nil {
		return fmt.Errorf("coverage.out: %w", err)
	}
	if err := validateCoverageHTML(filepath.Join(directory, "coverage.html")); err != nil {
		return fmt.Errorf("coverage.html: %w", err)
	}
	return nil
}

func validateFeatureCoverageConsistency(coverage featureCoverage) error {
	if coverage.SchemaVersion != reportSchemaVersion || coverage.Platform == "" || !validSHA256(coverage.SuiteHash) || len(coverage.Scenarios) == 0 {
		return errors.New("missing or invalid feature coverage identity")
	}
	seen := make(map[string]bool, len(coverage.Scenarios))
	criticalTotal := 0
	criticalCovered := 0
	var missingCritical, unexpectedSkips []string
	for _, item := range coverage.Scenarios {
		if item.ScenarioID == "" || item.GoTest == "" || item.Feature == "" || item.Requirement == "" || seen[item.ScenarioID] {
			return fmt.Errorf("scenario has missing fields or duplicate ID %q", item.ScenarioID)
		}
		seen[item.ScenarioID] = true
		switch item.Result {
		case "pass", "fail", "skip", "expected_skip", "missing", "not_applicable":
		default:
			return fmt.Errorf("scenario %s has unsupported result %q", item.ScenarioID, item.Result)
		}
		if item.Result == "skip" {
			unexpectedSkips = append(unexpectedSkips, item.ScenarioID)
		}
		if item.Critical && item.Result != "not_applicable" {
			criticalTotal++
			if item.Result == "pass" || item.Result == "expected_skip" {
				criticalCovered++
			} else {
				missingCritical = append(missingCritical, item.ScenarioID)
			}
		}
	}
	sort.Strings(missingCritical)
	sort.Strings(unexpectedSkips)
	wantPercent := 0.0
	if criticalTotal > 0 {
		wantPercent = 100 * float64(criticalCovered) / float64(criticalTotal)
	}
	if coverage.CriticalTotal != criticalTotal || coverage.CriticalCovered != criticalCovered || math.Abs(coverage.CriticalCoveragePct-wantPercent) > 0.001 || !equalStrings(coverage.MissingCritical, missingCritical) || !equalStrings(coverage.UnexpectedSkips, unexpectedSkips) {
		return errors.New("aggregate critical counts, results, or skip lists are inconsistent")
	}
	return nil
}

func validSHA256(value string) bool {
	if len(value) != 2*32 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validateGitHubRunIdentity(metadata runMetadata) error {
	if !validGitCommitSHA(metadata.CommitSHA) {
		return errors.New("metadata commit SHA is invalid")
	}
	if metadata.GitHubRunID == "local" {
		if metadata.GitHubRunURL != "local" || metadata.GitHubRunAttempt != "local" || metadata.GitHubRepository != "local" {
			return errors.New("local run metadata has inconsistent GitHub identity")
		}
		return nil
	}
	if _, err := strconv.ParseUint(metadata.GitHubRunID, 10, 64); err != nil {
		return errors.New("metadata GitHub run ID is not numeric")
	}
	if attempt, err := strconv.ParseUint(metadata.GitHubRunAttempt, 10, 32); err != nil || attempt == 0 {
		return errors.New("metadata GitHub run attempt is not a positive integer")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(metadata.GitHubRepository) {
		return errors.New("metadata GitHub repository is invalid")
	}
	parsed, err := url.Parse(metadata.GitHubRunURL)
	wantPath := "/" + metadata.GitHubRepository + "/actions/runs/" + metadata.GitHubRunID
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.Path != wantPath || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("metadata GitHub run URL does not match repository and run ID")
	}
	return nil
}

func validateArtifactEvidence(metadata runMetadata) error {
	switch metadata.SubjectKind {
	case "artifact":
		wantFormat := "tar.gz"
		if metadata.GOOS == "windows" {
			wantFormat = "zip"
		}
		wantBase := "env-vault-" + metadata.GOOS + "-" + metadata.GOARCH + "." + wantFormat
		if metadata.Artifact.Format != wantFormat || portableBase(metadata.Artifact.Path) != wantBase || portableBase(metadata.Artifact.ChecksumPath) != wantBase+".sha256" || !validSHA256(metadata.Artifact.SHA256) || !metadata.Artifact.ChecksumVerified {
			return fmt.Errorf("native archive/checksum evidence is incomplete or mismatched for %s", metadata.Platform)
		}
	case "built", "prebuilt":
		if metadata.Artifact.Path != "" || metadata.Artifact.ChecksumPath != "" || metadata.Artifact.SHA256 != "" || metadata.Artifact.ChecksumVerified || metadata.Artifact.Format != "" {
			return errors.New("non-artifact subject unexpectedly records archive evidence")
		}
	default:
		return fmt.Errorf("unsupported subject kind %q", metadata.SubjectKind)
	}
	return nil
}

func portableBase(value string) string {
	return path.Base(strings.ReplaceAll(value, `\`, "/"))
}

func validGitCommitSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validateContracts(filename, platform string, expected []string, recordedCount int) error {
	var report struct {
		SchemaVersion string                     `json:"schema_version"`
		Platform      string                     `json:"platform"`
		Scenarios     map[string]json.RawMessage `json:"scenarios"`
	}
	if err := readJSON(filename, &report); err != nil {
		return err
	}
	if report.SchemaVersion != reportSchemaVersion || report.Platform != platform {
		return errors.New("contract identity does not match metadata")
	}
	ids := make([]string, 0, len(report.Scenarios))
	for id, raw := range report.Scenarios {
		var contract scenarioContract
		if err := decodeStrictJSON(raw, &contract); err != nil {
			return fmt.Errorf("scenario %s has invalid contract schema: %w", id, err)
		}
		if contract.SchemaVersion != 1 || contract.ScenarioID != id || len(contract.Observations) == 0 {
			return fmt.Errorf("scenario %s has invalid identity or no observations", id)
		}
		for index, observation := range contract.Observations {
			if observation.Ordinal != index+1 || len(observation.Args) == 0 || observation.ExitCode < -1 || observation.ExitCode > 255 || observation.TimedOut {
				return fmt.Errorf("scenario %s observation %d has invalid ordinal, args, exit code, or timeout", id, index+1)
			}
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if recordedCount != len(ids) || !equalStrings(ids, expected) {
		return fmt.Errorf("scenario IDs/count differ: recorded=%d ids=%v expected=%v", recordedCount, ids, expected)
	}
	return nil
}

type scenarioContract struct {
	SchemaVersion int                   `json:"schema_version"`
	ScenarioID    string                `json:"scenario_id"`
	Observations  []contractObservation `json:"observations"`
}

type contractObservation struct {
	Ordinal  int      `json:"ordinal"`
	Args     []string `json:"args"`
	ExitCode int      `json:"exit_code"`
	Stdout   string   `json:"stdout"`
	Stderr   string   `json:"stderr"`
	TimedOut bool     `json:"timed_out"`
}

func decodeStrictJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func validateFeatureCoverageAgainstManifest(coverage featureCoverage, manifest scenarioManifest) error {
	if len(coverage.Scenarios) != len(manifest.Scenarios) {
		return fmt.Errorf("scenario count=%d, want %d", len(coverage.Scenarios), len(manifest.Scenarios))
	}
	for index, expected := range manifest.Scenarios {
		actual := coverage.Scenarios[index]
		wantSkip := containsString(expected.ExpectedPlatformSkips, coverage.Platform)
		if actual.ScenarioID != expected.ID || actual.Feature != expected.Feature || actual.Requirement != expected.Requirement || actual.GoTest != expected.GoTest || !equalStrings(actual.Platforms, expected.Platforms) || actual.Critical != expected.Critical || actual.ExpectedSkip != wantSkip {
			return fmt.Errorf("scenario trace %d (%s) differs from checked-in manifest", index, expected.ID)
		}
	}
	return nil
}

func requireExactFile(filename string, expected []byte) error {
	actual, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, expected) {
		return errors.New("content is not the canonical rendering of machine-readable evidence")
	}
	return nil
}

func evidenceDigestFiles() []string {
	return []string{
		"junit.xml", "raw-test.jsonl", "feature-coverage.json", "feature-coverage.md",
		"contracts.json", "coverage.out", "coverage.txt", "coverage.html", "coverage-percent.txt",
		"coverage-junit.xml", "coverage-raw-test.jsonl", "burn-in.jsonl", "locking-burn-in.jsonl",
		"sanitized-failure-bundle/command-output.txt",
	}
}

func computeEvidenceDigests(directory string) (map[string]string, error) {
	digests := make(map[string]string)
	for _, relative := range evidenceDigestFiles() {
		digest, err := sha256File(filepath.Join(directory, filepath.FromSlash(relative)))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", relative, err)
		}
		digests[relative] = digest
	}
	return digests, nil
}

func validateEvidenceDigests(directory string, recorded map[string]string) error {
	if len(recorded) != len(evidenceDigestFiles()) {
		return fmt.Errorf("recorded digest count=%d, want %d", len(recorded), len(evidenceDigestFiles()))
	}
	actual, err := computeEvidenceDigests(directory)
	if err != nil {
		return err
	}
	for _, relative := range evidenceDigestFiles() {
		if !validSHA256(recorded[relative]) || recorded[relative] != actual[relative] {
			return fmt.Errorf("%s digest differs", relative)
		}
	}
	return nil
}

func validateCoverageProfileSources(filename, repoRoot string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	const modulePrefix = "github.com/ildarbinanas-design/env-vault/"
	seen := make(map[string]bool)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	if !scanner.Scan() {
		return errors.New("coverage profile is empty")
	}
	for scanner.Scan() {
		fields := strings.Fields(strings.TrimSpace(scanner.Text()))
		if len(fields) != 3 {
			return errors.New("coverage profile contains malformed source record")
		}
		location := fields[0]
		colon := strings.LastIndex(location, ":")
		if colon <= 0 {
			return fmt.Errorf("coverage location %q has no source path", location)
		}
		source := location[:colon]
		if !strings.HasPrefix(source, modulePrefix) {
			return fmt.Errorf("coverage source %q is outside env-vault module", source)
		}
		relative := strings.TrimPrefix(source, modulePrefix)
		if strings.HasSuffix(relative, "_test.go") || (!strings.HasPrefix(relative, "cmd/env-vault/") && !strings.HasPrefix(relative, "internal/")) {
			return fmt.Errorf("coverage source %q is not production CLI code", relative)
		}
		cleaned := filepath.Clean(filepath.FromSlash(relative))
		if cleaned == "." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || cleaned == ".." {
			return fmt.Errorf("coverage source %q escapes checkout", relative)
		}
		info, statErr := os.Lstat(filepath.Join(repoRoot, cleaned))
		if statErr != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("coverage source %q is missing, non-regular, or a symlink", relative)
		}
		seen[relative] = true
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !seen["cmd/env-vault/main.go"] || len(seen) < 5 {
		return errors.New("coverage profile does not contain the release CLI and its production packages")
	}
	return nil
}

func validateDerivedCoverageArtifacts(directory, repoRoot, goVersion string) error {
	profile := filepath.Join(directory, "coverage.out")
	toolEnvironment := environment(map[string]string{"GOTOOLCHAIN": goVersion})
	functional, result := commandOutput("go", []string{"tool", "cover", "-func=" + profile}, repoRoot, toolEnvironment, 2*time.Minute)
	if result.ExitCode != 0 {
		return fmt.Errorf("regenerate coverage.txt: %s", commandLabel(result))
	}
	recordedFunctional, err := os.ReadFile(filepath.Join(directory, "coverage.txt"))
	if err != nil {
		return err
	}
	if !bytes.Equal(canonicalCoverageText(recordedFunctional), canonicalCoverageText(functional)) {
		return errors.New("coverage.txt is not derived from coverage.out")
	}

	tempDir, err := os.MkdirTemp("", "env-vault-e2e-cover-validate-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	regeneratedHTML := filepath.Join(tempDir, "coverage.html")
	_, htmlResult := commandOutput("go", []string{"tool", "cover", "-html=" + profile, "-o", regeneratedHTML}, repoRoot, toolEnvironment, 2*time.Minute)
	if htmlResult.ExitCode != 0 {
		return fmt.Errorf("regenerate coverage.html: %s", commandLabel(htmlResult))
	}
	recordedHTML, err := os.ReadFile(filepath.Join(directory, "coverage.html"))
	if err != nil {
		return err
	}
	regeneratedHTMLBytes, err := os.ReadFile(regeneratedHTML)
	if err != nil {
		return err
	}
	if !bytes.Equal(canonicalSuiteBytes(recordedHTML), canonicalSuiteBytes(regeneratedHTMLBytes)) {
		return errors.New("coverage.html is not the exact output of the recorded Go toolchain and coverage.out")
	}
	recordedSemantic, err := coverageHTMLSemantic(recordedHTML)
	if err != nil {
		return fmt.Errorf("recorded coverage.html: %w", err)
	}
	regeneratedSemantic, err := coverageHTMLSemantic(regeneratedHTMLBytes)
	if err != nil {
		return fmt.Errorf("regenerated coverage.html: %w", err)
	}
	if !bytes.Equal(recordedSemantic, regeneratedSemantic) {
		return errors.New("coverage.html semantic content is not derived from coverage.out")
	}

	expectedPackages, err := coveragePackagePercentages(profile)
	if err != nil {
		return err
	}
	recordedPackages, err := readCovdataPercentages(filepath.Join(directory, "coverage-percent.txt"))
	if err != nil {
		return err
	}
	if len(expectedPackages) != len(recordedPackages) {
		return fmt.Errorf("coverage-percent package count=%d, want %d", len(recordedPackages), len(expectedPackages))
	}
	for packageName, expected := range expectedPackages {
		actual, ok := recordedPackages[packageName]
		if !ok || math.Abs(actual-expected) > 0.051 {
			return fmt.Errorf("coverage-percent package %s=%.1f, want %.1f", packageName, actual, expected)
		}
	}
	return nil
}

func canonicalCoverageText(data []byte) []byte {
	lines := strings.Split(string(canonicalSuiteBytes(data)), "\n")
	var output strings.Builder
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		output.WriteString(strings.Join(fields, "\t"))
		output.WriteByte('\n')
	}
	return []byte(output.String())
}

func coverageHTMLSemantic(data []byte) ([]byte, error) {
	optionPattern := regexp.MustCompile(`(?s)<option\s+value="file[0-9]+"[^>]*>(.*?)</option>`)
	prePattern := regexp.MustCompile(`(?s)<pre\s+class="file"\s+id="file[0-9]+"[^>]*>(.*?)</pre>`)
	options := optionPattern.FindAllSubmatch(data, -1)
	blocks := prePattern.FindAllSubmatch(data, -1)
	if len(options) == 0 || len(options) != len(blocks) {
		return nil, errors.New("coverage HTML file index/source blocks are missing or inconsistent")
	}
	classPattern := regexp.MustCompile(`class="cov[0-9]+"`)
	var semantic bytes.Buffer
	for index := range options {
		option := htmlstd.UnescapeString(strings.TrimSpace(string(options[index][1])))
		if !strings.HasPrefix(option, "github.com/ildarbinanas-design/env-vault/") {
			return nil, fmt.Errorf("coverage HTML option %q is outside env-vault", option)
		}
		body := canonicalSuiteBytes(blocks[index][1])
		body = classPattern.ReplaceAll(body, []byte(`class="cov"`))
		fmt.Fprintf(&semantic, "%s\x00%s\x00", option, bytes.TrimSpace(body))
	}
	return semantic.Bytes(), nil
}

func coveragePackagePercentages(filename string) (map[string]float64, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	type totals struct{ all, covered int }
	values := make(map[string]totals)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	if !scanner.Scan() {
		return nil, errors.New("coverage profile is empty")
	}
	for scanner.Scan() {
		fields := strings.Fields(strings.TrimSpace(scanner.Text()))
		if len(fields) != 3 {
			return nil, errors.New("coverage profile contains malformed package record")
		}
		colon := strings.LastIndex(fields[0], ":")
		if colon <= 0 {
			return nil, errors.New("coverage profile contains malformed source location")
		}
		packageName := path.Dir(fields[0][:colon])
		statements, statementErr := strconv.Atoi(fields[1])
		count, countErr := strconv.ParseUint(fields[2], 10, 64)
		if statementErr != nil || countErr != nil || statements < 0 {
			return nil, errors.New("coverage profile contains malformed counts")
		}
		total := values[packageName]
		total.all += statements
		if count > 0 {
			total.covered += statements
		}
		values[packageName] = total
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	percentages := make(map[string]float64, len(values))
	for packageName, total := range values {
		if total.all == 0 {
			return nil, fmt.Errorf("coverage package %s has no statements", packageName)
		}
		percentages[packageName] = math.Round(1000*float64(total.covered)/float64(total.all)) / 10
	}
	return percentages, nil
}

func readCovdataPercentages(filename string) (map[string]float64, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	pattern := regexp.MustCompile(`^\s*(\S+)\s+coverage:\s+([0-9]+(?:\.[0-9]+)?)% of statements\s*$`)
	values := make(map[string]float64)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		match := pattern.FindStringSubmatch(scanner.Text())
		if len(match) != 3 {
			return nil, fmt.Errorf("invalid covdata percent record %q", scanner.Text())
		}
		if _, exists := values[match[1]]; exists {
			return nil, fmt.Errorf("duplicate covdata percent package %s", match[1])
		}
		value, err := strconv.ParseFloat(match[2], 64)
		if err != nil || value < 0 || value > 100 {
			return nil, fmt.Errorf("invalid covdata percentage %q", match[2])
		}
		values[match[1]] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, errors.New("contains no package coverage records")
	}
	return values, nil
}

func validateCoverageProfile(filename string) error {
	_, err := coverageProfilePercent(filename)
	return err
}

func coverageProfilePercent(filename string) (float64, error) {
	file, err := os.Open(filename)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	if !scanner.Scan() {
		return 0, errors.New("coverage profile is empty")
	}
	mode := strings.TrimSpace(scanner.Text())
	if mode != "mode: set" && mode != "mode: count" && mode != "mode: atomic" {
		return 0, fmt.Errorf("unsupported coverage mode %q", mode)
	}
	records := 0
	totalStatements := 0
	coveredStatements := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 || !strings.Contains(fields[0], ":") || !strings.Contains(fields[0], ",") {
			return 0, fmt.Errorf("invalid coverage record %q", line)
		}
		statements, statementErr := strconv.Atoi(fields[1])
		count, countErr := strconv.ParseUint(fields[2], 10, 64)
		if statementErr != nil || countErr != nil || statements < 0 {
			return 0, fmt.Errorf("invalid coverage counts in %q", line)
		}
		records++
		totalStatements += statements
		if count > 0 {
			coveredStatements += statements
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	if records == 0 {
		return 0, errors.New("coverage profile contains no records")
	}
	if totalStatements == 0 {
		return 0, errors.New("coverage profile contains no statements")
	}
	percentage := 100 * float64(coveredStatements) / float64(totalStatements)
	return math.Round(percentage*10) / 10, nil
}

func validateCoverageHTML(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	lower := bytes.ToLower(data)
	if !bytes.Contains(lower, []byte("<html")) || !bytes.Contains(lower, []byte("</html>")) || !bytes.Contains(lower, []byte("<body")) {
		return errors.New("coverage HTML is missing a complete html/body document")
	}
	return nil
}

func validateCovdataPercent(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	pattern := regexp.MustCompile(`^\s*\S+\s+coverage:\s+([0-9]+(?:\.[0-9]+)?)% of statements\s*$`)
	scanner := bufio.NewScanner(file)
	records := 0
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		match := pattern.FindStringSubmatch(line)
		if len(match) != 2 {
			return fmt.Errorf("invalid covdata percent record %q", line)
		}
		value, parseErr := strconv.ParseFloat(match[1], 64)
		if parseErr != nil || value < 0 || value > 100 {
			return fmt.Errorf("invalid covdata percentage %q", match[1])
		}
		records++
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if records == 0 {
		return errors.New("contains no package coverage records")
	}
	return nil
}

func readJSON(filename string, destination any) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func validateJSONFile(filename string) error {
	var value any
	return readJSON(filename, &value)
}

func validateJSONLines(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	count := 0
	for scanner.Scan() {
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		count++
		var value any
		if err := json.Unmarshal(scanner.Bytes(), &value); err != nil {
			return fmt.Errorf("line %d: %w", count, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if count == 0 {
		return errors.New("contains no JSON records")
	}
	return nil
}

func validateXML(filename string) error {
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := xml.NewDecoder(file)
	root := ""
	testSuites := 0
	for {
		token, tokenErr := decoder.Token()
		if errors.Is(tokenErr, io.EOF) {
			break
		}
		if tokenErr != nil {
			return fmt.Errorf("parse XML: %w", tokenErr)
		}
		start, ok := token.(xml.StartElement)
		if !ok {
			continue
		}
		if root == "" {
			root = start.Name.Local
		}
		if start.Name.Local == "testsuite" {
			testSuites++
		}
	}
	if root != "testsuites" && root != "testsuite" {
		return fmt.Errorf("JUnit XML root=%q, want testsuites or testsuite", root)
	}
	if testSuites == 0 {
		return errors.New("JUnit XML has no testsuite element")
	}
	return nil
}

func validateTestJSONLEvidence(filename string, coverage featureCoverage, repetitions int, selector func(scenarioTrace) bool) error {
	if repetitions < 1 {
		return errors.New("repetition count must be positive")
	}
	expected := make(map[string]string)
	known := make(map[string]bool)
	for _, trace := range coverage.Scenarios {
		known[trace.GoTest] = true
		if trace.Result != "pass" && trace.Result != "expected_skip" {
			continue
		}
		if selector != nil && !selector(trace) {
			continue
		}
		expectedAction := "pass"
		if trace.Result == "expected_skip" {
			expectedAction = "skip"
		}
		expected[trace.GoTest] = expectedAction
	}
	if len(expected) == 0 {
		return errors.New("no scenarios selected for JSONL validation")
	}
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	allowedActions := map[string]bool{
		"start": true, "run": true, "pause": true, "cont": true,
		"pass": true, "bench": true, "fail": true, "output": true, "skip": true,
	}
	runs := make(map[string]int)
	terminal := make(map[string]map[string]int)
	packagePasses := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8<<20)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var event testEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return fmt.Errorf("line %d: %w", lineNumber, err)
		}
		if !allowedActions[event.Action] || event.Package == "" {
			return fmt.Errorf("line %d has unsupported action or empty package", lineNumber)
		}
		if event.Test == "" {
			if event.Action == "fail" {
				return fmt.Errorf("package failed at line %d", lineNumber)
			}
			if event.Action == "pass" {
				packagePasses++
			}
			continue
		}
		if event.Action == "run" {
			runs[event.Test]++
		}
		if event.Action != "pass" && event.Action != "fail" && event.Action != "skip" {
			continue
		}
		if event.Test != "TestE2E" {
			if !known[event.Test] {
				return fmt.Errorf("unexpected terminal test %q", event.Test)
			}
			if _, selected := expected[event.Test]; !selected {
				return fmt.Errorf("unselected scenario %q reached terminal action %s", event.Test, event.Action)
			}
		}
		if terminal[event.Test] == nil {
			terminal[event.Test] = make(map[string]int)
		}
		terminal[event.Test][event.Action]++
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	for testName, wantAction := range expected {
		if runs[testName] != repetitions || terminal[testName][wantAction] != repetitions || terminal[testName]["fail"] != 0 || terminal[testName][oppositeTerminal(wantAction)] != 0 {
			return fmt.Errorf("%s events run=%d pass=%d skip=%d fail=%d, want %d %s", testName, runs[testName], terminal[testName]["pass"], terminal[testName]["skip"], terminal[testName]["fail"], repetitions, wantAction)
		}
	}
	if runs["TestE2E"] != repetitions || terminal["TestE2E"]["pass"] != repetitions || terminal["TestE2E"]["fail"] != 0 || terminal["TestE2E"]["skip"] != 0 {
		return fmt.Errorf("TestE2E parent events run=%d pass=%d skip=%d fail=%d, want %d passes", runs["TestE2E"], terminal["TestE2E"]["pass"], terminal["TestE2E"]["skip"], terminal["TestE2E"]["fail"], repetitions)
	}
	if packagePasses != 1 {
		return fmt.Errorf("package pass records=%d, want 1", packagePasses)
	}
	return nil
}

func oppositeTerminal(action string) string {
	if action == "pass" {
		return "skip"
	}
	return "pass"
}

type junitSuitesDocument struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Errors   int          `xml:"errors,attr"`
	Skipped  *int         `xml:"skipped,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Tests     int         `xml:"tests,attr"`
	Failures  int         `xml:"failures,attr"`
	Errors    int         `xml:"errors,attr"`
	Skipped   int         `xml:"skipped,attr"`
	TestCases []junitCase `xml:"testcase"`
}

type junitCase struct {
	Name     string     `xml:"name,attr"`
	Failures []struct{} `xml:"failure"`
	Errors   []struct{} `xml:"error"`
	Skipped  []struct{} `xml:"skipped"`
}

func validateJUnitEvidence(filename string, coverage featureCoverage) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	var document junitSuitesDocument
	if err := xml.Unmarshal(data, &document); err != nil {
		return err
	}
	if document.XMLName.Local != "testsuites" || len(document.Suites) == 0 {
		return errors.New("expected a testsuites document with at least one suite")
	}
	expected := map[string]string{"TestE2E": "pass"}
	for _, trace := range coverage.Scenarios {
		switch trace.Result {
		case "pass":
			expected[trace.GoTest] = "pass"
		case "expected_skip":
			expected[trace.GoTest] = "skip"
		}
	}
	seen := make(map[string]bool)
	tests := 0
	failures := 0
	errorsCount := 0
	skipped := 0
	for _, suite := range document.Suites {
		if suite.Tests != len(suite.TestCases) {
			return fmt.Errorf("suite tests=%d, testcase elements=%d", suite.Tests, len(suite.TestCases))
		}
		suiteFailures := 0
		suiteErrors := 0
		suiteSkipped := 0
		for _, testCase := range suite.TestCases {
			if testCase.Name == "" || seen[testCase.Name] {
				return fmt.Errorf("empty or duplicate testcase %q", testCase.Name)
			}
			want, ok := expected[testCase.Name]
			if !ok {
				return fmt.Errorf("unexpected testcase %q", testCase.Name)
			}
			seen[testCase.Name] = true
			caseFailures := len(testCase.Failures)
			caseErrors := len(testCase.Errors)
			caseSkipped := len(testCase.Skipped)
			if want == "pass" && (caseFailures != 0 || caseErrors != 0 || caseSkipped != 0) {
				return fmt.Errorf("testcase %q is not a pass", testCase.Name)
			}
			if want == "skip" && (caseFailures != 0 || caseErrors != 0 || caseSkipped != 1) {
				return fmt.Errorf("testcase %q is not exactly one skip", testCase.Name)
			}
			suiteFailures += caseFailures
			suiteErrors += caseErrors
			suiteSkipped += caseSkipped
		}
		if suite.Failures != suiteFailures || suite.Errors != suiteErrors || suite.Skipped != suiteSkipped {
			return errors.New("testsuite aggregate counts differ from testcase elements")
		}
		tests += suite.Tests
		failures += suiteFailures
		errorsCount += suiteErrors
		skipped += suiteSkipped
	}
	if len(seen) != len(expected) {
		var missing []string
		for name := range expected {
			if !seen[name] {
				missing = append(missing, name)
			}
		}
		sort.Strings(missing)
		return fmt.Errorf("missing testcases: %v", missing)
	}
	if document.Tests != tests || document.Failures != failures || document.Errors != errorsCount || document.Skipped != nil && *document.Skipped != skipped || failures != 0 || errorsCount != 0 {
		return errors.New("testsuites aggregate counts differ from testcase elements")
	}
	return nil
}

func validateBurnInEvidence(directory string, metadata runMetadata, coverage featureCoverage, locking bool) error {
	prefix := "burn-in:"
	filename := "burn-in.jsonl"
	minimumCount := 3
	if locking {
		prefix = "locking-burn-in:"
		filename = "locking-burn-in.jsonl"
		minimumCount = 5
	}
	var matches []commandResult
	for _, command := range metadata.Commands {
		if strings.HasPrefix(command.Name, prefix) {
			matches = append(matches, command)
		}
	}
	if len(matches) != 1 {
		return fmt.Errorf("metadata command records=%d, want 1", len(matches))
	}
	command := matches[0]
	if command.ExitCode != 0 || command.TimedOut || command.Count < minimumCount || command.Seed == "" {
		return fmt.Errorf("invalid command status/count/seed: exit=%d timeout=%t count=%d seed=%q", command.ExitCode, command.TimedOut, command.Count, command.Seed)
	}
	path := filepath.Join(directory, filename)
	if seed := extractShuffleSeed(path); seed != command.Seed {
		return fmt.Errorf("go shuffle seed=%q, metadata=%q", seed, command.Seed)
	}
	seeds := extractScenarioShuffleSeeds(path)
	if !equalStrings(seeds, command.ScenarioSeeds) {
		return fmt.Errorf("scenario shuffle seeds differ: report=%v metadata=%v", seeds, command.ScenarioSeeds)
	}
	if err := validateScenarioShuffleSeeds(seeds, command.Count); err != nil {
		return err
	}
	var selector func(scenarioTrace) bool
	if locking {
		pattern := commandRunPattern(command.Arguments)
		if pattern == "" {
			return errors.New("locking command has no -run pattern")
		}
		if _, err := matchesGoRunPattern(pattern, "TestE2E/LOCK_TIMEOUT_CRASH_INTEGRITY"); err != nil {
			return fmt.Errorf("invalid locking -run pattern %q: %v", pattern, err)
		}
		selector = func(trace scenarioTrace) bool {
			matches, _ := matchesGoRunPattern(pattern, trace.GoTest)
			return matches
		}
	}
	return validateTestJSONLEvidence(path, coverage, command.Count, selector)
}

func commandRunPattern(arguments []string) string {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == "-run" {
			return arguments[index+1]
		}
	}
	return ""
}

func scanAndSanitizeLeaks(reportDir string, registryRecords int, initial []leakFinding) (leakScanReport, error) {
	report := leakScanReport{
		SchemaVersion:   reportSchemaVersion,
		Status:          "pass",
		RegistryRecords: registryRecords,
		Findings:        append([]leakFinding{}, initial...),
	}
	for _, finding := range initial {
		if finding.Occurrences > 0 {
			report.Detected = true
			report.Status = "fail"
			report.Occurrences += finding.Occurrences
		}
	}
	var files []string
	err := filepath.WalkDir(reportDir, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("report tree contains a symlink: %s", filename)
		}
		if !entry.IsDir() && entry.Type().IsRegular() && filepath.Base(filename) != "leak-scan.json" {
			files = append(files, filename)
		}
		return nil
	})
	if err != nil {
		return report, err
	}
	if summary := os.Getenv("GITHUB_STEP_SUMMARY"); summary != "" {
		if info, statErr := os.Lstat(summary); statErr == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			files = append(files, summary)
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return report, fmt.Errorf("inspect GITHUB_STEP_SUMMARY: %w", statErr)
		}
	}
	sort.Strings(files)
	for _, filename := range files {
		data, err := os.ReadFile(filename)
		if err != nil {
			return report, fmt.Errorf("scan report %s: %w", filename, err)
		}
		sanitized, count := redactSentinels(data)
		report.FilesScanned++
		if count == 0 {
			continue
		}
		report.Detected = true
		report.Status = "fail"
		report.Occurrences += count
		relative, relErr := filepath.Rel(reportDir, filename)
		if relErr != nil || strings.HasPrefix(relative, "..") {
			relative = "GITHUB_STEP_SUMMARY"
		}
		report.Findings = append(report.Findings, leakFinding{Path: filepath.ToSlash(relative), Occurrences: count})
		info, statErr := os.Stat(filename)
		if statErr != nil {
			return report, statErr
		}
		if err := writeFileAtomic(filename, sanitized, info.Mode().Perm()); err != nil {
			return report, fmt.Errorf("sanitize leaked sentinel in %s: %w", filename, err)
		}
	}
	if err := writeJSON(filepath.Join(reportDir, "leak-scan.json"), report); err != nil {
		return report, err
	}
	return report, nil
}

func redactSentinels(data []byte) ([]byte, int) {
	prefix := []byte(defaultSentinelPrefix)
	if !bytes.Contains(data, prefix) {
		return data, 0
	}
	redaction := []byte(redactionMarker)
	var output bytes.Buffer
	count := 0
	for len(data) > 0 {
		index := bytes.Index(data, prefix)
		if index < 0 {
			output.Write(data)
			break
		}
		output.Write(data[:index])
		data = data[index+len(prefix):]
		for len(data) > 0 && sentinelTokenByte(data[0]) {
			data = data[1:]
		}
		output.Write(redaction)
		count++
	}
	return output.Bytes(), count
}

const redactionMarker = "[REDACTED-E2E-SECRET]"

func assertNoRedactionMarker(reportDir string) error {
	return filepath.WalkDir(reportDir, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > 256<<20 {
			return fmt.Errorf("report entry is unsafe: %s", filename)
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(redactionMarker)) {
			return fmt.Errorf("redaction marker remains in %s", filepath.Base(filename))
		}
		return nil
	})
}

func sentinelTokenByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '_' || value == '-' || value == '.'
}

func appendStepSummary(summaryPath string) error {
	target := os.Getenv("GITHUB_STEP_SUMMARY")
	if target == "" {
		return nil
	}
	data, err := os.ReadFile(summaryPath)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(append([]byte("\n"), data...)); err != nil {
		return err
	}
	return nil
}

func assertNoSentinelMarker(reportDir string) error {
	const maxScannedFileBytes int64 = 256 << 20
	var files []string
	err := filepath.WalkDir(reportDir, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("report tree contains a symlink: %s", filename)
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > maxScannedFileBytes {
			return fmt.Errorf("report entry is non-regular or too large: %s", filename)
		}
		files = append(files, filename)
		return nil
	})
	if err != nil {
		return err
	}
	if summary := os.Getenv("GITHUB_STEP_SUMMARY"); summary != "" {
		if info, statErr := os.Lstat(summary); statErr == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && info.Size() <= maxScannedFileBytes {
			files = append(files, summary)
		} else if statErr == nil {
			return errors.New("GITHUB_STEP_SUMMARY is a symlink, non-regular, or too large")
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
	}
	for _, filename := range files {
		data, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(defaultSentinelPrefix)) {
			return fmt.Errorf("sentinel marker remains in %s", filepath.Base(filename))
		}
	}
	return nil
}
