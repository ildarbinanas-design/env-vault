package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComparePassesValidatedEquivalentMatrices(t *testing.T) {
	opts := writeComparisonFixture(t)
	report, err := compare(opts)
	if err != nil {
		t.Fatalf("compare valid matrices: %v", err)
	}
	if report.Status != "pass" || report.SuiteHash != opts.expectedSuiteHash || report.BaselineGoVersion != "go1.22.12" || report.CandidateGoVersion != "go1.26.5" {
		t.Fatalf("comparison report=%+v", report)
	}
	for _, item := range report.Checks {
		if item.Status != "pass" {
			t.Fatalf("check did not pass: %+v", item)
		}
	}
}

func TestCompareFailsClosed(t *testing.T) {
	tests := map[string]func(t *testing.T, opts options){
		"matrix status": func(t *testing.T, opts options) {
			mutateJSON(t, filepath.Join(opts.candidate, "matrix-validation.json"), func(value map[string]any) {
				value["status"] = "fail"
			})
		},
		"matrix symlink": func(t *testing.T, opts options) {
			filename := filepath.Join(opts.candidate, "matrix-validation.json")
			target := filepath.Join(t.TempDir(), "matrix-validation.json")
			data, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(target, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filename); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filename); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		},
		"suite hash": func(t *testing.T, opts options) {
			mutateJSON(t, filepath.Join(reportDirectory(opts.candidate, "go1.26.5", requiredPlatforms[0]), "metadata.json"), func(value map[string]any) {
				value["suite_hash"] = strings.Repeat("d", 64)
			})
		},
		"contract": func(t *testing.T, opts options) {
			mutateJSON(t, filepath.Join(reportDirectory(opts.candidate, "go1.26.5", requiredPlatforms[0]), "contracts.json"), func(value map[string]any) {
				value["changed"] = true
			})
		},
		"scenario result": func(t *testing.T, opts options) {
			mutateJSON(t, filepath.Join(reportDirectory(opts.candidate, "go1.26.5", requiredPlatforms[0]), "feature-coverage.json"), func(value map[string]any) {
				scenarios := value["scenarios"].([]any)
				scenarios[0].(map[string]any)["result"] = "fail"
			})
		},
		"coverage regression": func(t *testing.T, opts options) {
			mutateJSON(t, filepath.Join(reportDirectory(opts.candidate, "go1.26.5", requiredPlatforms[0]), "metadata.json"), func(value map[string]any) {
				value["statement_coverage_percent"] = 70.9
			})
		},
		"sentinel leak": func(t *testing.T, opts options) {
			if err := os.WriteFile(filepath.Join(opts.candidate, "leaked.txt"), []byte(sentinelPrefix+"not-a-real-secret\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"identity": func(t *testing.T, opts options) {
			mutateJSON(t, filepath.Join(reportDirectory(opts.candidate, "go1.26.5", requiredPlatforms[0]), "metadata.json"), func(value map[string]any) {
				value["commit_sha"] = strings.Repeat("e", 40)
			})
		},
		"missing platform": func(t *testing.T, opts options) {
			if err := os.RemoveAll(reportDirectory(opts.candidate, "go1.26.5", requiredPlatforms[0])); err != nil {
				t.Fatal(err)
			}
		},
		"leak gate": func(t *testing.T, opts options) {
			mutateJSON(t, filepath.Join(reportDirectory(opts.candidate, "go1.26.5", requiredPlatforms[0]), "leak-scan.json"), func(value map[string]any) {
				value["status"] = "fail"
				value["detected"] = true
				value["occurrences"] = float64(1)
			})
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			opts := writeComparisonFixture(t)
			mutate(t, opts)
			report, err := compare(opts)
			if err == nil || report.Status != "fail" {
				t.Fatalf("comparison err=%v status=%s", err, report.Status)
			}
			failed := false
			for _, item := range report.Checks {
				failed = failed || item.Status == "fail"
			}
			if !failed {
				t.Fatal("failed report contains no failed check")
			}
		})
	}
}

func TestCompareRequiresSuccessfulSourceValidation(t *testing.T) {
	opts := writeComparisonFixture(t)
	opts.candidateValidation = "failure"
	report, err := compare(opts)
	if err == nil || report.Status != "fail" {
		t.Fatalf("comparison err=%v status=%s", err, report.Status)
	}
	if got := report.Checks[1]; got.Name != "candidate matrix validation" || got.Status != "fail" || !strings.Contains(got.Detail, "outcome") {
		t.Fatalf("candidate validation check=%+v", got)
	}
}

func TestParseFlagsRejectsNonFiniteCoverageTolerance(t *testing.T) {
	for _, value := range []string{"NaN", "+Inf", "-Inf"} {
		t.Run(value, func(t *testing.T) {
			opts := writeComparisonFixture(t)
			args := optionArgs(opts)
			for index := range args {
				if args[index] == "--coverage-tolerance" {
					args[index+1] = value
					break
				}
			}
			if _, err := parseFlags(args); err == nil {
				t.Fatalf("accepted coverage tolerance %q", value)
			}
		})
	}
}

func TestRealMainRejectsSymlinkOutputDirectory(t *testing.T) {
	opts := writeComparisonFixture(t)
	target := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, opts.output); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := realMain(optionArgs(opts)); err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Fatalf("symlink output error=%v", err)
	}
}

func TestRealMainWritesReportBeforeReturningFailure(t *testing.T) {
	opts := writeComparisonFixture(t)
	mutateJSON(t, filepath.Join(opts.candidate, "matrix-validation.json"), func(value map[string]any) {
		value["status"] = "fail"
	})
	if err := realMain(optionArgs(opts)); err == nil {
		t.Fatal("realMain accepted failed matrix validation")
	}
	var report comparisonReport
	readFixtureJSON(t, filepath.Join(opts.output, "comparison.json"), &report)
	if report.Status != "fail" {
		t.Fatalf("persisted comparison status=%q", report.Status)
	}
	info, err := os.Stat(filepath.Join(opts.output, "comparison.md"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("comparison markdown info=%v err=%v", info, err)
	}
}

func writeComparisonFixture(t *testing.T) options {
	t.Helper()
	root := t.TempDir()
	opts := options{
		baseline:            filepath.Join(root, "baseline"),
		candidate:           filepath.Join(root, "candidate"),
		output:              filepath.Join(root, "output"),
		expectedSuiteHash:   strings.Repeat("a", 64),
		baselineValidation:  "success",
		candidateValidation: "success",
		baselineCommit:      strings.Repeat("b", 40),
		baselineRunID:       "101",
		baselineRunURL:      "https://github.example/actions/runs/101",
		baselineRunAttempt:  "1",
		baselineRepository:  "example/env-vault",
		baselineReporter:    "v1.12.2",
		candidateCommit:     strings.Repeat("c", 40),
		candidateRunID:      "202",
		candidateRunURL:     "https://github.example/actions/runs/202",
		candidateRunAttempt: "1",
		candidateRepository: "example/env-vault",
		candidateReporter:   "v1.13.0",
	}
	writeMatrixFixture(t, opts.baseline, "baseline", opts.expectedSuiteHash)
	writeMatrixFixture(t, opts.candidate, "candidate", opts.expectedSuiteHash)
	for _, platform := range requiredPlatforms {
		writeReportFixture(t, opts, opts.baseline, "baseline", "go1.22.12", platform, 71.1)
		writeReportFixture(t, opts, opts.candidate, "candidate", "go1.26.5", platform, 71.3)
	}
	return opts
}

func writeMatrixFixture(t *testing.T, root, phase, suiteHash string) {
	t.Helper()
	writeFixtureJSON(t, filepath.Join(root, "matrix-validation.json"), matrixValidation{
		SchemaVersion: schemaVersion,
		Mode:          "validate-matrix",
		Status:        "pass",
		Phase:         phase,
		SuiteHash:     suiteHash,
		Platforms:     append([]string(nil), requiredPlatforms...),
		Checks:        []check{{Name: "matrix identity and gates", Status: "pass"}},
	})
}

func writeReportFixture(t *testing.T, opts options, root, phase, goVersion, platform string, statementCoverage float64) {
	t.Helper()
	directory := reportDirectory(root, goVersion, platform)
	commit, runID, runURL, runAttempt, repository, reporter := opts.baselineCommit, opts.baselineRunID, opts.baselineRunURL, opts.baselineRunAttempt, opts.baselineRepository, opts.baselineReporter
	if phase == "candidate" {
		commit, runID, runURL, runAttempt, repository, reporter = opts.candidateCommit, opts.candidateRunID, opts.candidateRunURL, opts.candidateRunAttempt, opts.candidateRepository, opts.candidateReporter
	}
	expectedSkips := []string{}
	result := "pass"
	passed, skipped := 2, 0
	if platform == "windows-amd64" {
		expectedSkips = []string{"SIGNAL"}
		result = "expected_skip"
		passed, skipped = 1, 1
	}
	writeFixtureJSON(t, filepath.Join(directory, "metadata.json"), metadata{
		SchemaVersion:         schemaVersion,
		Phase:                 phase,
		Status:                "pass",
		CommitSHA:             commit,
		GitHubRunID:           runID,
		GitHubRunURL:          runURL,
		GitHubRunAttempt:      runAttempt,
		GitHubRepository:      repository,
		GoVersion:             goVersion,
		Platform:              platform,
		SubjectKind:           "artifact",
		SuiteHash:             opts.expectedSuiteHash,
		GotestsumVersion:      reporter,
		Counts:                counts{Passed: passed, Skipped: skipped},
		StatementCoverage:     statementCoverage,
		ExpectedPlatformSkips: expectedSkips,
		UnexpectedSkips:       []string{},
		Failures:              []string{},
	})
	writeFixtureJSON(t, filepath.Join(directory, "feature-coverage.json"), featureCoverage{
		SchemaVersion:       schemaVersion,
		Platform:            platform,
		SuiteHash:           opts.expectedSuiteHash,
		CriticalTotal:       2,
		CriticalCovered:     2,
		CriticalCoveragePct: 100,
		UnexpectedSkips:     []string{},
		MissingCritical:     []string{},
		Scenarios: []scenarioTrace{
			{ScenarioID: "CORE", Critical: true, Result: "pass"},
			{ScenarioID: "SIGNAL", Critical: true, Result: result},
		},
	})
	writeFixtureJSON(t, filepath.Join(directory, "contracts.json"), map[string]any{
		"schema_version": schemaVersion,
		"platform":       platform,
		"scenarios": map[string]any{
			"CORE": map[string]any{"exit_code": 0, "stdout": "ok\n", "stderr": ""},
		},
	})
	writeFixtureJSON(t, filepath.Join(directory, "leak-scan.json"), leakScan{
		SchemaVersion: schemaVersion,
		Status:        "pass",
		RegistryCount: 2,
		Findings:      []any{},
	})
}

func reportDirectory(root, goVersion, platform string) string {
	return filepath.Join(root, goVersion, platform)
}

func writeFixtureJSON(t *testing.T, filename string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFixtureJSON(t *testing.T, filename string, destination any) {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, destination); err != nil {
		t.Fatal(err)
	}
}

func mutateJSON(t *testing.T, filename string, mutate func(map[string]any)) {
	t.Helper()
	var value map[string]any
	readFixtureJSON(t, filename, &value)
	mutate(value)
	writeFixtureJSON(t, filename, value)
}

func optionArgs(opts options) []string {
	return []string{
		"--baseline", opts.baseline,
		"--candidate", opts.candidate,
		"--output", opts.output,
		"--coverage-tolerance", "0",
		"--expected-suite-hash", opts.expectedSuiteHash,
		"--baseline-validation-outcome", opts.baselineValidation,
		"--candidate-validation-outcome", opts.candidateValidation,
		"--baseline-commit", opts.baselineCommit,
		"--baseline-run-id", opts.baselineRunID,
		"--baseline-run-url", opts.baselineRunURL,
		"--baseline-run-attempt", opts.baselineRunAttempt,
		"--baseline-repository", opts.baselineRepository,
		"--baseline-reporter", opts.baselineReporter,
		"--candidate-commit", opts.candidateCommit,
		"--candidate-run-id", opts.candidateRunID,
		"--candidate-run-url", opts.candidateRunURL,
		"--candidate-run-attempt", opts.candidateRunAttempt,
		"--candidate-repository", opts.candidateRepository,
		"--candidate-reporter", opts.candidateReporter,
	}
}
