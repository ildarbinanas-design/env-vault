package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestValidateXMLParsesCompleteJUnit(t *testing.T) {
	valid := writeRunnerFixture(t, "junit.xml", []byte(`<?xml version="1.0"?><testsuites><testsuite name="e2e"></testsuite></testsuites>`))
	if err := validateXML(valid); err != nil {
		t.Fatalf("valid JUnit rejected: %v", err)
	}
	for name, data := range map[string][]byte{
		"truncated.xml":  []byte(`<testsuites><testsuite>`),
		"wrong-root.xml": []byte(`<report><testsuite></testsuite></report>`),
		"empty.xml":      nil,
	} {
		filename := writeRunnerFixture(t, name, data)
		if err := validateXML(filename); err == nil {
			t.Fatalf("invalid JUnit %s was accepted", name)
		}
	}
}

func TestParseBinaryBuildInfoRequiresCompilerAndTarget(t *testing.T) {
	valid := []byte("/tmp/env-vault: go1.22.12\n\tpath\tgithub.com/example/env-vault\n\tbuild\tGOARCH=arm64\n\tbuild\tGOOS=darwin\n")
	version, goos, goarch, err := parseBinaryBuildInfo(valid)
	if err != nil || version != "go1.22.12" || goos != "darwin" || goarch != "arm64" {
		t.Fatalf("valid build info parsed as version=%q target=%s/%s err=%v", version, goos, goarch, err)
	}
	for name, data := range map[string][]byte{
		"missing target":  []byte("/tmp/env-vault: go1.22.12\n\tbuild\tGOOS=darwin\n"),
		"missing version": []byte("/tmp/env-vault: unknown\n\tbuild\tGOARCH=arm64\n\tbuild\tGOOS=darwin\n"),
	} {
		if _, _, _, err := parseBinaryBuildInfo(data); err == nil {
			t.Fatalf("%s build info was accepted", name)
		}
	}
}

func TestValidateCoverageArtifacts(t *testing.T) {
	profile := writeRunnerFixture(t, "coverage.out", []byte("mode: set\nexample.go:1.1,2.2 1 1\n"))
	if err := validateCoverageProfile(profile); err != nil {
		t.Fatalf("valid coverage profile rejected: %v", err)
	}
	invalidProfile := writeRunnerFixture(t, "invalid.out", []byte("mode: set\nnot-a-record\n"))
	if err := validateCoverageProfile(invalidProfile); err == nil {
		t.Fatal("invalid coverage profile was accepted")
	}

	html := writeRunnerFixture(t, "coverage.html", []byte("<!doctype html><html><body>coverage</body></html>"))
	if err := validateCoverageHTML(html); err != nil {
		t.Fatalf("valid coverage HTML rejected: %v", err)
	}
	truncatedHTML := writeRunnerFixture(t, "truncated.html", []byte("<html><body>"))
	if err := validateCoverageHTML(truncatedHTML); err == nil {
		t.Fatal("truncated coverage HTML was accepted")
	}
}

func TestValidateScenarioShuffleSeeds(t *testing.T) {
	if err := validateScenarioShuffleSeeds([]string{"11", "22", "33"}, 3); err != nil {
		t.Fatalf("distinct seeds rejected: %v", err)
	}
	for name, seeds := range map[string][]string{
		"missing":   {"11", "22"},
		"duplicate": {"11", "11", "22"},
		"empty":     {"11", "", "22"},
	} {
		if err := validateScenarioShuffleSeeds(seeds, 3); err == nil {
			t.Fatalf("invalid %s seeds were accepted", name)
		}
	}
}

func TestMatchesGoRunPatternUsesSlashSeparatedComponents(t *testing.T) {
	pattern := `TestE2E/(?i)(concurr|lock|atomic|crash)`
	for _, name := range []string{
		"TestE2E/CONCURRENCY_PROFILE_MUTATIONS",
		"TestE2E/PROFILE_ATOMIC_PERMISSIONS",
		"TestE2E/LOCK_TIMEOUT_CRASH_INTEGRITY",
	} {
		matched, err := matchesGoRunPattern(pattern, name)
		if err != nil || !matched {
			t.Fatalf("pattern did not match %s: matched=%t err=%v", name, matched, err)
		}
	}
	if matched, err := matchesGoRunPattern(pattern, "TestE2E/SECRET_LIFECYCLE"); err != nil || matched {
		t.Fatalf("pattern unexpectedly matched secret scenario: matched=%t err=%v", matched, err)
	}
}

func TestValidateFeatureCoverageRejectsAggregateTampering(t *testing.T) {
	valid := func() featureCoverage {
		return featureCoverage{
			SchemaVersion:       reportSchemaVersion,
			Platform:            "linux-amd64",
			SuiteHash:           strings.Repeat("a", 64),
			CriticalTotal:       1,
			CriticalCovered:     1,
			CriticalCoveragePct: 100,
			UnexpectedSkips:     []string{},
			MissingCritical:     []string{},
			Scenarios: []scenarioTrace{{
				Feature:     "secret lifecycle",
				Requirement: "set/check/delete",
				ScenarioID:  "SECRET_LIFECYCLE",
				GoTest:      "TestE2E/SECRET_LIFECYCLE",
				Platforms:   []string{"linux-amd64"},
				Critical:    true,
				Result:      "pass",
			}},
		}
	}

	if err := validateFeatureCoverageConsistency(valid()); err != nil {
		t.Fatalf("valid feature coverage rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*featureCoverage)
	}{
		{name: "critical total", mutate: func(report *featureCoverage) { report.CriticalTotal++ }},
		{name: "critical covered", mutate: func(report *featureCoverage) { report.CriticalCovered-- }},
		{name: "critical percentage", mutate: func(report *featureCoverage) { report.CriticalCoveragePct = 99 }},
		{name: "missing critical list", mutate: func(report *featureCoverage) { report.MissingCritical = []string{"SECRET_LIFECYCLE"} }},
		{name: "unexpected skip list", mutate: func(report *featureCoverage) { report.UnexpectedSkips = []string{"SECRET_LIFECYCLE"} }},
		{name: "scenario result without aggregates", mutate: func(report *featureCoverage) { report.Scenarios[0].Result = "fail" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := valid()
			test.mutate(&report)
			if err := validateFeatureCoverageConsistency(report); err == nil {
				t.Fatal("tampered feature coverage was accepted")
			}
		})
	}
}

func TestValidateFeatureCoverageRejectsManifestTraceTampering(t *testing.T) {
	repository, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := loadManifest(filepath.Join(repository, "e2e", "scenarios.json"))
	if err != nil {
		t.Fatal(err)
	}
	coverage := buildFeatureCoverage(manifest, strings.Repeat("a", 64), "linux-amd64", map[string]testEvent{})
	if err := validateFeatureCoverageAgainstManifest(coverage, manifest); err != nil {
		t.Fatalf("untampered trace rejected: %v", err)
	}
	coverage.Scenarios[0].Requirement += " tampered"
	if err := validateFeatureCoverageAgainstManifest(coverage, manifest); err == nil {
		t.Fatal("feature trace differing from checked-in manifest was accepted")
	}
}

func TestValidateContractsRejectsMalformedObservationSchema(t *testing.T) {
	valid := scenarioContract{
		SchemaVersion: 1,
		ScenarioID:    "SCENARIO",
		Observations:  []contractObservation{{Ordinal: 1, Args: []string{"--help"}, ExitCode: 0}},
	}
	write := func(value any) string {
		t.Helper()
		filename := filepath.Join(t.TempDir(), "contracts.json")
		mustWriteRunnerJSON(t, filename, map[string]any{
			"schema_version": reportSchemaVersion,
			"platform":       "linux-amd64",
			"scenarios":      map[string]any{"SCENARIO": value},
		})
		return filename
	}
	if err := validateContracts(write(valid), "linux-amd64", []string{"SCENARIO"}, 1); err != nil {
		t.Fatalf("valid contract rejected: %v", err)
	}
	invalid := []any{
		map[string]any{},
		map[string]any{"schema_version": 1, "scenario_id": "OTHER", "observations": []any{}},
		map[string]any{"schema_version": 1, "scenario_id": "SCENARIO", "observations": []any{map[string]any{"ordinal": 2, "args": []string{"--help"}, "exit_code": 0, "stdout": "", "stderr": "", "timed_out": false}}},
		map[string]any{"schema_version": 1, "scenario_id": "SCENARIO", "observations": []any{map[string]any{"ordinal": 1, "args": []string{}, "exit_code": 0, "stdout": "", "stderr": "", "timed_out": false}}},
		map[string]any{"schema_version": 1, "scenario_id": "SCENARIO", "observations": []any{map[string]any{"ordinal": 1, "args": []string{"--help"}, "exit_code": 0, "stdout": "", "stderr": "", "timed_out": true}}},
		map[string]any{"schema_version": 1, "scenario_id": "SCENARIO", "observations": []any{map[string]any{"ordinal": 1, "args": []string{"--help"}, "exit_code": 0, "stdout": "", "stderr": "", "timed_out": false, "unknown": true}}},
	}
	for index, value := range invalid {
		if err := validateContracts(write(value), "linux-amd64", []string{"SCENARIO"}, 1); err == nil {
			t.Fatalf("malformed contract %d was accepted", index)
		}
	}
}

func TestEnsureReportPlaceholdersAreSyntacticallyValid(t *testing.T) {
	directory := t.TempDir()
	ensureReportPlaceholders(directory)

	for _, relative := range []string{"junit.xml", "coverage-junit.xml"} {
		if err := validateXML(filepath.Join(directory, relative)); err != nil {
			t.Fatalf("placeholder %s is not valid JUnit XML: %v", relative, err)
		}
	}
	for _, relative := range []string{"raw-test.jsonl", "coverage-raw-test.jsonl", "burn-in.jsonl", "locking-burn-in.jsonl"} {
		if err := validateJSONLines(filepath.Join(directory, relative)); err != nil {
			t.Fatalf("placeholder %s is not valid JSONL: %v", relative, err)
		}
	}
	if err := validateJSONFile(filepath.Join(directory, "contracts.json")); err != nil {
		t.Fatalf("placeholder contracts.json is not valid JSON: %v", err)
	}
	if err := validateCoverageProfile(filepath.Join(directory, "coverage.out")); err != nil {
		t.Fatalf("placeholder coverage.out is invalid: %v", err)
	}
	if _, err := parseCoveragePercent(filepath.Join(directory, "coverage.txt")); err != nil {
		t.Fatalf("placeholder coverage.txt is invalid: %v", err)
	}
	if err := validateCovdataPercent(filepath.Join(directory, "coverage-percent.txt")); err != nil {
		t.Fatalf("placeholder coverage-percent.txt is invalid: %v", err)
	}
	if err := validateCoverageHTML(filepath.Join(directory, "coverage.html")); err != nil {
		t.Fatalf("placeholder coverage.html is invalid: %v", err)
	}
}

func TestValidateReportArtifactsSyntaxAcceptsCompleteFailureBundle(t *testing.T) {
	directory := writeValidReportDirectory(t, "linux", "amd64")
	if err := validateReportArtifactsSyntax(directory); err != nil {
		t.Fatalf("complete report bundle with failure placeholders rejected: %v", err)
	}
}

func TestValidateReportDirectoryRejectsLeakScanTampering(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*leakScanReport)
	}{
		{name: "schema identity", mutate: func(report *leakScanReport) { report.SchemaVersion = "tampered" }},
		{name: "registry identity", mutate: func(report *leakScanReport) { report.RegistryRecords++ }},
		{name: "files scanned count", mutate: func(report *leakScanReport) { report.FilesScanned = 0 }},
		{name: "occurrence count", mutate: func(report *leakScanReport) { report.Occurrences = 1 }},
		{name: "findings count", mutate: func(report *leakScanReport) {
			report.Findings = []leakFinding{{Path: "summary.json", Occurrences: 1}}
		}},
		{name: "detection flag", mutate: func(report *leakScanReport) { report.Detected = true }},
		{name: "status", mutate: func(report *leakScanReport) { report.Status = "fail" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := writeValidReportDirectory(t, "linux", "amd64")
			if _, _, err := validateReportDirectory(directory); err != nil {
				t.Fatalf("valid report fixture rejected: %v", err)
			}
			var report leakScanReport
			if err := readJSON(filepath.Join(directory, "leak-scan.json"), &report); err != nil {
				t.Fatalf("read leak scan fixture: %v", err)
			}
			test.mutate(&report)
			if err := writeJSON(filepath.Join(directory, "leak-scan.json"), report); err != nil {
				t.Fatalf("write tampered leak scan: %v", err)
			}
			if _, _, err := validateReportDirectory(directory); err == nil {
				t.Fatal("tampered leak scan was accepted")
			}
		})
	}
}

func TestValidateReportDirectoryRejectsSemanticEvidenceTampering(t *testing.T) {
	tests := []struct {
		name     string
		relative string
		data     []byte
	}{
		{name: "unrelated raw JSONL", relative: "raw-test.jsonl", data: []byte("{\"Action\":\"pass\",\"Package\":\"unrelated\"}\n")},
		{name: "unrelated coverage JSONL", relative: "coverage-raw-test.jsonl", data: []byte("{\"Action\":\"pass\",\"Package\":\"unrelated\"}\n")},
		{name: "unrelated JUnit", relative: "junit.xml", data: []byte(`<?xml version="1.0"?><testsuites tests="1"><testsuite tests="1"><testcase name="OtherTest"></testcase></testsuite></testsuites>`)},
		{name: "fake coverage profile", relative: "coverage.out", data: []byte("mode: set\nfixture.go:1.1,1.2 1 0\n")},
		{name: "truncated burn in", relative: "burn-in.jsonl", data: testJSONLEvidenceFixture(t, 1, "101")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := writeValidReportDirectory(t, "linux", "amd64")
			if _, _, err := validateReportDirectory(directory); err != nil {
				t.Fatalf("valid report fixture rejected: %v", err)
			}
			mustWriteReportFixture(t, directory, test.relative, test.data)
			if _, _, err := validateReportDirectory(directory); err == nil {
				t.Fatal("semantically corrupted report was accepted")
			}
		})
	}
}

func TestValidateReportDirectoryRejectsHumanReportTampering(t *testing.T) {
	for _, relative := range []string{"summary.md", "feature-coverage.md"} {
		t.Run(relative, func(t *testing.T) {
			directory := writeValidReportDirectory(t, "linux", "amd64")
			mustWriteReportFixture(t, directory, relative, []byte("# forged human report\n"))
			if _, _, err := validateReportDirectory(directory); err == nil {
				t.Fatalf("tampered %s was accepted", relative)
			}
		})
	}
}

func TestValidatePassReportRejectsRedactionMarkerAfterDigestRecalculation(t *testing.T) {
	directory := writeValidReportDirectory(t, "linux", "amd64")
	mustWriteReportFixture(t, directory, "sanitized-failure-bundle/command-output.txt", []byte(redactionMarker+"\n"))
	var metadata runMetadata
	if err := readJSON(filepath.Join(directory, "metadata.json"), &metadata); err != nil {
		t.Fatal(err)
	}
	var err error
	metadata.EvidenceSHA256, err = computeEvidenceDigests(directory)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteRunnerJSON(t, filepath.Join(directory, "metadata.json"), metadata)
	if _, _, err := validateReportDirectory(directory); err == nil {
		t.Fatal("pass report containing a redaction marker was accepted")
	}
}

func TestValidateReportRejectsCoordinatedDerivedCoverageTampering(t *testing.T) {
	tests := []struct {
		relative string
		data     []byte
	}{
		{relative: "coverage.txt", data: []byte("forged.go:1:\tforged\t80.0%\ntotal:\t(statements)\t80.0%\n")},
		{relative: "coverage-percent.txt", data: []byte("\tforged.example/unrelated\t\tcoverage: 80.0% of statements\n")},
		{relative: "coverage.html", data: []byte("<!doctype html><html><body>FORGED UNRELATED COVERAGE</body></html>\n")},
	}
	for _, test := range tests {
		t.Run(test.relative, func(t *testing.T) {
			directory := writeValidReportDirectory(t, "linux", "amd64")
			mustWriteReportFixture(t, directory, test.relative, test.data)
			var metadata runMetadata
			if err := readJSON(filepath.Join(directory, "metadata.json"), &metadata); err != nil {
				t.Fatal(err)
			}
			var err error
			metadata.EvidenceSHA256, err = computeEvidenceDigests(directory)
			if err != nil {
				t.Fatal(err)
			}
			mustWriteRunnerJSON(t, filepath.Join(directory, "metadata.json"), metadata)
			if _, _, err := validateReportDirectory(directory); err == nil {
				t.Fatalf("coordinated tamper of %s was accepted", test.relative)
			}
		})
	}
}

func TestValidateReportRejectsCoverageHTMLOverlayWithUpdatedDigest(t *testing.T) {
	directory := writeValidReportDirectory(t, "linux", "amd64")
	htmlPath := filepath.Join(directory, "coverage.html")
	data, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("<body>"), []byte("<body><h1>FORGED 100% COVERAGE</h1>"), 1)
	mustWriteReportFixture(t, directory, "coverage.html", data)
	var metadata runMetadata
	if err := readJSON(filepath.Join(directory, "metadata.json"), &metadata); err != nil {
		t.Fatal(err)
	}
	metadata.EvidenceSHA256, err = computeEvidenceDigests(directory)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteRunnerJSON(t, filepath.Join(directory, "metadata.json"), metadata)
	if _, _, err := validateReportDirectory(directory); err == nil {
		t.Fatal("coverage HTML overlay with coordinated digest update was accepted")
	}
}

func TestValidateReportDirectoryRejectsContradictoryMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*runMetadata)
	}{
		{name: "pass with failures", mutate: func(metadata *runMetadata) { metadata.Failures = []string{"injected failure"} }},
		{name: "false duration", mutate: func(metadata *runMetadata) { metadata.DurationMS++ }},
		{name: "wrong binary GOOS", mutate: func(metadata *runMetadata) { metadata.BinaryGOOS = "windows" }},
		{name: "wrong binary GOARCH", mutate: func(metadata *runMetadata) { metadata.BinaryGOARCH = "amd64" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := writeValidReportDirectory(t, "darwin", "arm64")
			mutateReportMetadata(t, directory, test.mutate)
			if _, _, err := validateReportDirectory(directory); err == nil {
				t.Fatal("contradictory metadata was accepted")
			}
		})
	}
}

func TestNormalizeMetadataEvidenceRemovesMachinePaths(t *testing.T) {
	repo := filepath.Join(string(filepath.Separator), "Users", "person", "repo")
	private := filepath.Join(os.TempDir(), "private-e2e")
	metadata := runMetadata{
		Artifact: artifactEvidence{Path: filepath.Join(repo, "dist", "archive.tar.gz"), ChecksumPath: filepath.Join(repo, "dist", "archive.tar.gz.sha256")},
		Commands: []commandResult{{
			Name:      "functional-e2e: " + filepath.Join(string(filepath.Separator), "Users", "person", "go", "bin", "gotestsum"),
			Arguments: []string{"-o", filepath.Join(private, "result.json"), "-i=" + filepath.Join(private, "covdata")},
			Error:     "failed below " + private,
		}},
		Failures: []string{"failure in " + filepath.Join(repo, "reports"), "external artifact /tmp/archive.tar.gz"},
	}
	normalizeMetadataEvidence(&metadata, repo, private)
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{repo, private, "/Users/person", "/tmp/archive.tar.gz"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("normalized metadata still contains %q: %s", forbidden, encoded)
		}
	}
}

func TestReportValidationRescansUnexpectedFilesForSentinelMarkers(t *testing.T) {
	directory := writeValidReportDirectory(t, "linux", "amd64")
	mustWriteReportFixture(t, directory, "unexpected-extra.txt", []byte(defaultSentinelPrefix+"unit_test_marker\n"))
	if _, _, err := validateReportDirectory(directory); err == nil {
		t.Fatal("report containing an unregistered sentinel marker was accepted")
	}

	root := t.TempDir()
	reportDirectory := filepath.Join(root, "linux-amd64")
	writeValidReportDirectoryAt(t, reportDirectory, "linux", "amd64")
	mustWriteReportFixture(t, root, "outside-platform-report.txt", []byte(defaultSentinelPrefix+"root_marker\n"))
	if _, _, err := discoverReports(root, true); err == nil {
		t.Fatal("report root containing an extra sentinel marker was accepted")
	}
}

func TestValidateMatrixEnforcesCrossPlatformRunIdentity(t *testing.T) {
	commit := strings.Repeat("c", 40)
	tests := []struct {
		name          string
		expectedRunID string
		prepare       func(*runMetadata)
		mutate        func(*runMetadata)
		wantErr       bool
	}{
		{name: "consistent matrix", expectedRunID: "local"},
		{name: "consistent numeric matrix", expectedRunID: "42", prepare: setNumericRunIdentity},
		{name: "commit mismatch", wantErr: true, mutate: func(metadata *runMetadata) {
			metadata.CommitSHA = strings.Repeat("d", 40)
		}, expectedRunID: "local"},
		{name: "Go version mismatch", wantErr: true, mutate: func(metadata *runMetadata) {
			metadata.GoVersion = "go1.22.11"
		}, expectedRunID: "local"},
		{name: "GitHub run mismatch", wantErr: true, mutate: func(metadata *runMetadata) {
			metadata.GitHubRunID = "42"
			metadata.GitHubRunURL = "https://github.com/example/env-vault/actions/runs/42"
			metadata.GitHubRunAttempt = "1"
		}, expectedRunID: "local"},
		{
			name:          "GitHub run URL mismatch",
			expectedRunID: "42",
			wantErr:       true,
			prepare:       setNumericRunIdentity,
			mutate: func(metadata *runMetadata) {
				metadata.GitHubRunURL = "https://github.com/another/env-vault/actions/runs/42"
			},
		},
		{
			name:          "GitHub run attempt mismatch",
			expectedRunID: "42",
			wantErr:       true,
			prepare:       setNumericRunIdentity,
			mutate: func(metadata *runMetadata) {
				metadata.GitHubRunAttempt = "2"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			linuxDirectory := filepath.Join(root, "linux-amd64")
			writeValidReportDirectoryAt(t, linuxDirectory, "linux", "amd64")
			darwinDirectory := filepath.Join(root, "darwin-arm64")
			writeValidReportDirectoryAt(t, darwinDirectory, "darwin", "arm64")
			if test.prepare != nil {
				mutateReportMetadata(t, linuxDirectory, test.prepare)
				mutateReportMetadata(t, darwinDirectory, test.prepare)
			}
			if test.mutate != nil {
				mutateReportMetadata(t, darwinDirectory, test.mutate)
			}

			err := validateMatrix(matrixOptions{
				reportsRoot:        root,
				phase:              "baseline",
				required:           "linux-amd64,darwin-arm64",
				expectedCommit:     commit,
				expectedRunID:      test.expectedRunID,
				expectedRunURL:     map[bool]string{true: "local", false: "https://github.com/example/env-vault/actions/runs/42"}[test.expectedRunID == "local"],
				expectedRunAttempt: map[bool]string{true: "local", false: "1"}[test.expectedRunID == "local"],
				expectedRepository: map[bool]string{true: "local", false: "example/env-vault"}[test.expectedRunID == "local"],
				expectedReporter:   gotestsumVersion,
			})
			if test.wantErr && err == nil {
				t.Fatal("matrix identity tampering was accepted")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("consistent matrix rejected: %v", err)
			}
			if !test.wantErr && test.expectedRunID == "42" {
				var proof gateReport
				if err := readJSON(filepath.Join(root, "matrix-validation.json"), &proof); err != nil {
					t.Fatalf("read sealed matrix proof: %v", err)
				}
				if proof.SchemaID != "env-vault.e2e-matrix-proof.v1" || proof.SchemaVersion != 1 || proof.Run.RunID != "42" || len(proof.PlatformEvidence) != 2 {
					t.Fatalf("matrix proof identity/evidence=%+v", proof)
				}
				for _, evidence := range proof.PlatformEvidence {
					if !validSHA256(evidence.ContractSHA256) || len(evidence.EvidenceSHA256) != len(evidenceDigestFiles()) || !validSHA256(evidence.NormalizedEvidenceSHA256) {
						t.Fatalf("platform %s proof is incomplete: %+v", evidence.ID, evidence)
					}
				}
			}
		})
	}
}

func setNumericRunIdentity(metadata *runMetadata) {
	metadata.GitHubRunID = "42"
	metadata.GitHubRunURL = "https://github.com/example/env-vault/actions/runs/42"
	metadata.GitHubRunAttempt = "1"
	metadata.GitHubRepository = "example/env-vault"
	metadata.SubjectKind = "artifact"
	format := "tar.gz"
	if metadata.GOOS == "windows" {
		format = "zip"
	}
	base := "env-vault-" + metadata.Platform + "." + format
	metadata.Artifact = artifactEvidence{
		Path:             "<REPO>/dist/" + base,
		ChecksumPath:     "<REPO>/dist/" + base + ".sha256",
		SHA256:           strings.Repeat("a", 64),
		ChecksumVerified: true,
		Format:           format,
	}
}

func TestValidateMatrixRejectsReportsFromStaleSuite(t *testing.T) {
	root := t.TempDir()
	for _, target := range []struct{ goos, goarch string }{{"linux", "amd64"}, {"darwin", "arm64"}} {
		directory := filepath.Join(root, target.goos+"-"+target.goarch)
		writeValidReportDirectoryAt(t, directory, target.goos, target.goarch)
		mutateReportSuiteHash(t, directory, strings.Repeat("d", 64))
	}
	err := validateMatrix(matrixOptions{
		reportsRoot: root, phase: "baseline", required: "linux-amd64,darwin-arm64",
		expectedCommit: strings.Repeat("c", 40), expectedRunID: "local", expectedRunURL: "local",
		expectedRunAttempt: "local", expectedRepository: "local", expectedReporter: gotestsumVersion,
	})
	if err == nil {
		t.Fatal("internally consistent reports from a stale suite were accepted")
	}
}

func mutateReportSuiteHash(t *testing.T, directory, digest string) {
	t.Helper()
	var metadata runMetadata
	var coverage featureCoverage
	if err := readJSON(filepath.Join(directory, "metadata.json"), &metadata); err != nil {
		t.Fatal(err)
	}
	if err := readJSON(filepath.Join(directory, "feature-coverage.json"), &coverage); err != nil {
		t.Fatal(err)
	}
	metadata.SuiteHash = digest
	coverage.SuiteHash = digest
	mustWriteRunnerJSON(t, filepath.Join(directory, "feature-coverage.json"), coverage)
	if err := writeFeatureMarkdown(filepath.Join(directory, "feature-coverage.md"), coverage); err != nil {
		t.Fatal(err)
	}
	if err := writeSummaryReports(directory, metadata, coverage); err != nil {
		t.Fatal(err)
	}
	var err error
	metadata.EvidenceSHA256, err = computeEvidenceDigests(directory)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteRunnerJSON(t, filepath.Join(directory, "metadata.json"), metadata)
}

func mutateReportMetadata(t *testing.T, directory string, mutate func(*runMetadata)) {
	t.Helper()
	metadataPath := filepath.Join(directory, "metadata.json")
	var metadata runMetadata
	if err := readJSON(metadataPath, &metadata); err != nil {
		t.Fatalf("read matrix metadata fixture: %v", err)
	}
	mutate(&metadata)
	mustWriteRunnerJSON(t, metadataPath, metadata)
	var coverage featureCoverage
	if err := readJSON(filepath.Join(directory, "feature-coverage.json"), &coverage); err != nil {
		t.Fatalf("read matrix feature fixture: %v", err)
	}
	if err := writeSummaryReports(directory, metadata, coverage); err != nil {
		t.Fatalf("rewrite matrix summary fixture: %v", err)
	}
}

func writeValidReportDirectory(t *testing.T, goos, goarch string) string {
	t.Helper()
	directory := t.TempDir()
	writeValidReportDirectoryAt(t, directory, goos, goarch)
	return directory
}

func writeValidReportDirectoryAt(t *testing.T, directory, goos, goarch string) {
	t.Helper()
	ensureReportPlaceholders(directory)

	platform := goos + "-" + goarch
	repository, err := findRepoRoot()
	if err != nil {
		t.Fatalf("find repository root: %v", err)
	}
	suiteDigest, err := suiteHash(repository)
	if err != nil {
		t.Fatalf("hash current suite: %v", err)
	}
	fixtureGoVersion, goVersionCommand := resolveGoVersion(repository, 30*time.Second)
	if goVersionCommand.ExitCode != 0 {
		t.Fatalf("resolve fixture Go version: %s", commandLabel(goVersionCommand))
	}
	manifest, err := loadManifest(filepath.Join(repository, "e2e", "scenarios.json"))
	if err != nil {
		t.Fatalf("load current feature manifest: %v", err)
	}
	events := make(map[string]testEvent)
	for _, item := range manifest.Scenarios {
		if containsString(item.Platforms, platform) {
			action := "pass"
			if containsString(item.ExpectedPlatformSkips, platform) {
				action = "skip"
			}
			events[item.GoTest] = testEvent{Action: action, Test: item.GoTest}
		}
	}
	coverage := buildFeatureCoverage(manifest, suiteDigest, platform, events)
	started := time.Unix(1_700_000_000, 0).UTC()
	var expectedSkips []string
	contracts := make(map[string]any)
	for _, trace := range coverage.Scenarios {
		if trace.Result == "expected_skip" {
			expectedSkips = append(expectedSkips, trace.ScenarioID)
		}
		if trace.Result == "pass" {
			contracts[trace.ScenarioID] = scenarioContract{
				SchemaVersion: 1,
				ScenarioID:    trace.ScenarioID,
				Observations: []contractObservation{{
					Ordinal: 1, Args: []string{"--help"}, ExitCode: 0,
				}},
			}
		}
	}
	sort.Strings(expectedSkips)
	metadata := runMetadata{
		SchemaVersion:         reportSchemaVersion,
		Phase:                 "baseline",
		Status:                "pass",
		CommitSHA:             strings.Repeat("c", 40),
		GitHubRunID:           "local",
		GitHubRunURL:          "local",
		GitHubRunAttempt:      "local",
		GitHubRepository:      "local",
		GoVersion:             fixtureGoVersion,
		BinaryGoVersion:       fixtureGoVersion,
		BinaryGOOS:            goos,
		BinaryGOARCH:          goarch,
		GOOS:                  goos,
		GOARCH:                goarch,
		RunnerOS:              goos,
		Platform:              platform,
		BinarySHA256:          strings.Repeat("b", 64),
		SubjectKind:           "built",
		SuiteHash:             suiteDigest,
		GotestsumVersion:      gotestsumVersion,
		StartedAt:             started,
		EndedAt:               started.Add(time.Second),
		DurationMS:            1000,
		Counts:                countsFromCoverage(coverage),
		StatementCoverage:     80,
		ExpectedPlatformSkips: expectedSkips,
		UnexpectedSkips:       []string{},
		SentinelRecords:       1,
		ContractRecords:       len(contracts),
		Commands: []commandResult{
			{Name: "burn-in: go", Arguments: []string{"test", "-run", "^TestE2E$"}, ExitCode: 0, Seed: "101", ScenarioSeeds: []string{"1001", "1002", "1003"}, Count: 3},
			{Name: "locking-burn-in: go", Arguments: []string{"test", "-run", "TestE2E/SECRET"}, ExitCode: 0, Seed: "202", ScenarioSeeds: []string{"2001", "2002", "2003", "2004", "2005"}, Count: 5},
		},
		Failures: []string{},
	}

	mustWriteRunnerJSON(t, filepath.Join(directory, "metadata.json"), metadata)
	mustWriteRunnerJSON(t, filepath.Join(directory, "feature-coverage.json"), coverage)
	if err := writeFeatureMarkdown(filepath.Join(directory, "feature-coverage.md"), coverage); err != nil {
		t.Fatalf("write feature Markdown: %v", err)
	}
	if err := writeSummaryReports(directory, metadata, coverage); err != nil {
		t.Fatalf("write summary reports: %v", err)
	}
	mustWriteRunnerJSON(t, filepath.Join(directory, "contracts.json"), map[string]any{
		"schema_version": reportSchemaVersion,
		"platform":       platform,
		"scenarios":      contracts,
	})
	mustWriteRunnerJSON(t, filepath.Join(directory, "leak-scan.json"), leakScanReport{
		SchemaVersion:   reportSchemaVersion,
		Status:          "pass",
		Detected:        false,
		FilesScanned:    18,
		Occurrences:     0,
		RegistryRecords: metadata.SentinelRecords,
		Findings:        []leakFinding{},
	})
	if err := writeFailureBundleManifest(directory, metadata); err != nil {
		t.Fatalf("write failure bundle manifest: %v", err)
	}
	mustWriteReportFixture(t, directory, "sanitized-failure-bundle/command-output.txt", []byte("No captured command output.\n"))
	profile := filepath.Join(directory, "coverage.out")
	mustWriteReportFixture(t, directory, "coverage.out", []byte("mode: set\n"+
		"github.com/ildarbinanas-design/env-vault/cmd/env-vault/main.go:9.13,11.2 1 1\n"+
		"github.com/ildarbinanas-design/env-vault/internal/cli/cli.go:29.30,30.22 1 1\n"+
		"github.com/ildarbinanas-design/env-vault/internal/config/config.go:41.20,46.2 1 1\n"+
		"github.com/ildarbinanas-design/env-vault/internal/errors/errors.go:43.86,51.2 1 1\n"+
		"github.com/ildarbinanas-design/env-vault/internal/output/output.go:46.83,54.2 1 0\n"))
	functionalCoverage, coverageTextResult := commandOutput("go", []string{"tool", "cover", "-func=" + profile}, repository, environment(nil), 30*time.Second)
	if coverageTextResult.ExitCode != 0 {
		t.Fatalf("generate coverage.txt fixture: %s", commandLabel(coverageTextResult))
	}
	mustWriteReportFixture(t, directory, "coverage.txt", functionalCoverage)
	_, coverageHTMLResult := commandOutput("go", []string{"tool", "cover", "-html=" + profile, "-o", filepath.Join(directory, "coverage.html")}, repository, environment(nil), 30*time.Second)
	if coverageHTMLResult.ExitCode != 0 {
		t.Fatalf("generate coverage.html fixture: %s", commandLabel(coverageHTMLResult))
	}
	packagePercentages, err := coveragePackagePercentages(profile)
	if err != nil {
		t.Fatal(err)
	}
	packageNames := make([]string, 0, len(packagePercentages))
	for packageName := range packagePercentages {
		packageNames = append(packageNames, packageName)
	}
	sort.Strings(packageNames)
	var coveragePercent strings.Builder
	for _, packageName := range packageNames {
		fmt.Fprintf(&coveragePercent, "\t%s\t\tcoverage: %.1f%% of statements\n", packageName, packagePercentages[packageName])
	}
	mustWriteReportFixture(t, directory, "coverage-percent.txt", []byte(coveragePercent.String()))
	functionalRaw := testJSONLEvidenceForCoverage(t, coverage, 1, "", nil)
	mustWriteReportFixture(t, directory, "raw-test.jsonl", functionalRaw)
	mustWriteReportFixture(t, directory, "coverage-raw-test.jsonl", functionalRaw)
	junit := junitEvidenceForCoverage(coverage)
	mustWriteReportFixture(t, directory, "junit.xml", junit)
	mustWriteReportFixture(t, directory, "coverage-junit.xml", junit)
	mustWriteReportFixture(t, directory, "burn-in.jsonl", testJSONLEvidenceForCoverage(t, coverage, 3, "101", nil))
	lockingSelector := func(trace scenarioTrace) bool { return strings.Contains(trace.GoTest, "SECRET") }
	mustWriteReportFixture(t, directory, "locking-burn-in.jsonl", testJSONLEvidenceForCoverage(t, coverage, 5, "202", lockingSelector))
	metadata.EvidenceSHA256, err = computeEvidenceDigests(directory)
	if err != nil {
		t.Fatalf("hash report fixture evidence: %v", err)
	}
	mustWriteRunnerJSON(t, filepath.Join(directory, "metadata.json"), metadata)
}

func testJSONLEvidenceForCoverage(t *testing.T, coverage featureCoverage, repetitions int, goShuffleSeed string, selector func(scenarioTrace) bool) []byte {
	t.Helper()
	selected := make([]scenarioTrace, 0, len(coverage.Scenarios))
	for _, trace := range coverage.Scenarios {
		if trace.Result != "pass" && trace.Result != "expected_skip" {
			continue
		}
		if selector == nil || selector(trace) {
			selected = append(selected, trace)
		}
	}
	var output bytes.Buffer
	writeEvent := func(event testEvent) {
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		output.Write(data)
		output.WriteByte('\n')
	}
	writeEvent(testEvent{Action: "start", Package: "example/e2e"})
	if goShuffleSeed != "" {
		writeEvent(testEvent{Action: "output", Package: "example/e2e", Output: "-test.shuffle " + goShuffleSeed + "\n"})
	}
	seedBase := 1000
	if goShuffleSeed == "202" {
		seedBase = 2000
	}
	for iteration := 1; iteration <= repetitions; iteration++ {
		writeEvent(testEvent{Action: "run", Package: "example/e2e", Test: "TestE2E"})
		if goShuffleSeed != "" {
			writeEvent(testEvent{Action: "output", Package: "example/e2e", Test: "TestE2E", Output: fmt.Sprintf("ENV_VAULT_E2E_SCENARIO_SHUFFLE_SEED=%d\n", seedBase+iteration)})
		}
		for _, trace := range selected {
			writeEvent(testEvent{Action: "run", Package: "example/e2e", Test: trace.GoTest})
			action := "pass"
			if trace.Result == "expected_skip" {
				action = "skip"
			}
			writeEvent(testEvent{Action: action, Package: "example/e2e", Test: trace.GoTest})
		}
		writeEvent(testEvent{Action: "pass", Package: "example/e2e", Test: "TestE2E"})
	}
	writeEvent(testEvent{Action: "pass", Package: "example/e2e"})
	return output.Bytes()
}

func junitEvidenceForCoverage(coverage featureCoverage) []byte {
	var cases strings.Builder
	tests := 1
	skipped := 0
	for _, trace := range coverage.Scenarios {
		if trace.Result != "pass" && trace.Result != "expected_skip" {
			continue
		}
		tests++
		fmt.Fprintf(&cases, `<testcase name="%s">`, trace.GoTest)
		if trace.Result == "expected_skip" {
			cases.WriteString(`<skipped></skipped>`)
			skipped++
		}
		cases.WriteString(`</testcase>`)
	}
	cases.WriteString(`<testcase name="TestE2E"></testcase>`)
	return []byte(fmt.Sprintf(`<?xml version="1.0"?><testsuites tests="%d" failures="0" errors="0" skipped="%d"><testsuite name="e2e" tests="%d" failures="0" errors="0" skipped="%d">%s</testsuite></testsuites>`, tests, skipped, tests, skipped, cases.String()))
}

func testJSONLEvidenceFixture(t *testing.T, repetitions int, goShuffleSeed string) []byte {
	t.Helper()
	var output bytes.Buffer
	writeEvent := func(event testEvent) {
		data, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		output.Write(data)
		output.WriteByte('\n')
	}
	writeEvent(testEvent{Action: "start", Package: "example/e2e"})
	if goShuffleSeed != "" {
		writeEvent(testEvent{Action: "output", Package: "example/e2e", Output: "-test.shuffle " + goShuffleSeed + "\n"})
	}
	seedBase := 1000
	if goShuffleSeed == "202" {
		seedBase = 2000
	}
	for iteration := 1; iteration <= repetitions; iteration++ {
		writeEvent(testEvent{Action: "run", Package: "example/e2e", Test: "TestE2E"})
		if goShuffleSeed != "" {
			writeEvent(testEvent{Action: "output", Package: "example/e2e", Test: "TestE2E", Output: fmt.Sprintf("ENV_VAULT_E2E_SCENARIO_SHUFFLE_SEED=%d\n", seedBase+iteration)})
		}
		writeEvent(testEvent{Action: "run", Package: "example/e2e", Test: "TestE2E/SECRET_LIFECYCLE"})
		writeEvent(testEvent{Action: "pass", Package: "example/e2e", Test: "TestE2E/SECRET_LIFECYCLE"})
		writeEvent(testEvent{Action: "pass", Package: "example/e2e", Test: "TestE2E"})
	}
	writeEvent(testEvent{Action: "pass", Package: "example/e2e"})
	return output.Bytes()
}

func mustWriteRunnerJSON(t *testing.T, filename string, value any) {
	t.Helper()
	if err := writeJSON(filename, value); err != nil {
		t.Fatalf("write %s: %v", filepath.Base(filename), err)
	}
}

func mustWriteReportFixture(t *testing.T, directory, relative string, data []byte) {
	t.Helper()
	filename := filepath.Join(directory, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
		t.Fatalf("create fixture parent: %v", err)
	}
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", relative, err)
	}
}

func writeRunnerFixture(t *testing.T, name string, data []byte) string {
	t.Helper()
	filename := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return filename
}
