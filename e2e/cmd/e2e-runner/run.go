package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type failureState struct {
	code    int
	reasons []string
}

func (state *failureState) add(code int, format string, args ...any) {
	if code == 0 {
		code = 1
	}
	if state.code == 0 {
		state.code = code
	}
	state.reasons = append(state.reasons, fmt.Sprintf(format, args...))
}

type exitStatusError struct {
	code int
	err  error
}

func (err exitStatusError) Error() string { return err.err.Error() }

func runSuite(opts runOptions) error {
	started := time.Now().UTC()
	failures := &failureState{}
	repoRoot, err := findRepoRoot()
	if err != nil {
		return err
	}
	goVersion, goVersionCommand := resolveGoVersion(repoRoot, opts.commandTimeout)
	if goVersionCommand.ExitCode != 0 {
		failures.add(goVersionCommand.ExitCode, "resolve Go version: %s", goVersionCommand.Error)
	}
	platform := expectedGOOS() + "-" + expectedGOARCH()
	reportsRoot := opts.reportsRoot
	if !filepath.IsAbs(reportsRoot) {
		reportsRoot = filepath.Join(repoRoot, reportsRoot)
	}
	reportDir := filepath.Join(reportsRoot, opts.phase, safePathComponent(goVersion), platform)
	if err := prepareReportDirectory(repoRoot, reportDir); err != nil {
		return err
	}
	privateDir, err := os.MkdirTemp("", "env-vault-e2e-runner-*")
	if err != nil {
		return fmt.Errorf("create private runner directory: %w", err)
	}
	defer os.RemoveAll(privateDir)
	_ = os.Chmod(privateDir, 0o700)
	if err := os.MkdirAll(filepath.Join(privateDir, "bin"), 0o700); err != nil {
		return fmt.Errorf("create private binary directory: %w", err)
	}

	metadata := runMetadata{
		SchemaVersion:         reportSchemaVersion,
		Phase:                 opts.phase,
		Status:                "fail",
		CommitSHA:             resolveCommitSHA(repoRoot, opts.commandTimeout),
		GitHubRunID:           githubRunID(),
		GitHubRunURL:          githubRunURL(),
		GitHubRunAttempt:      githubRunAttempt(),
		GitHubRepository:      githubRepository(),
		GoVersion:             goVersion,
		GOOS:                  expectedGOOS(),
		GOARCH:                expectedGOARCH(),
		RunnerOS:              opts.runnerOS,
		Platform:              platform,
		GotestsumVersion:      gotestsumVersion,
		SubjectKind:           subjectKind(opts),
		StartedAt:             started,
		ExpectedPlatformSkips: []string{},
		UnexpectedSkips:       []string{},
		Failures:              []string{},
		Commands:              []commandResult{goVersionCommand},
	}

	manifest, manifestErr := loadManifest(filepath.Join(repoRoot, filepath.FromSlash(opts.scenariosPath)))
	if manifestErr != nil {
		failures.add(1, "%v", manifestErr)
	}
	suiteDigest, suiteErr := suiteHash(repoRoot)
	if suiteErr != nil {
		failures.add(1, "%v", suiteErr)
	}
	metadata.SuiteHash = suiteDigest
	for _, item := range manifest.Scenarios {
		if containsString(item.ExpectedPlatformSkips, platform) {
			metadata.ExpectedPlatformSkips = append(metadata.ExpectedPlatformSkips, item.ID)
		}
	}
	sort.Strings(metadata.ExpectedPlatformSkips)

	nativeRuntimeErr := validateNativeRuntime(opts.runnerOS)
	var binary string
	var artifact artifactEvidence
	var buildCommand commandResult
	var binaryErr error
	if nativeRuntimeErr != nil {
		buildCommand = commandResult{
			Name:      "validate-native-runtime",
			StartedAt: time.Now().UTC(),
			EndedAt:   time.Now().UTC(),
			ExitCode:  1,
			Error:     nativeRuntimeErr.Error(),
		}
		binaryErr = nativeRuntimeErr
	} else {
		binary, artifact, buildCommand, binaryErr = prepareSubjectBinary(repoRoot, privateDir, opts)
	}
	metadata.Artifact = artifact
	metadata.Commands = append(metadata.Commands, buildCommand)
	if binaryErr != nil {
		failures.add(buildCommand.ExitCode, "prepare release-like binary: %v", binaryErr)
	} else {
		metadata.BinarySHA256, err = sha256File(binary)
		if err != nil {
			failures.add(1, "hash release-like binary: %v", err)
		}
		binaryGoVersion, binaryGOOS, binaryGOARCH, inspectCommand, inspectErr := inspectBinaryBuild(repoRoot, binary, opts.commandTimeout)
		metadata.Commands = append(metadata.Commands, inspectCommand)
		metadata.BinaryGoVersion = binaryGoVersion
		metadata.BinaryGOOS = binaryGOOS
		metadata.BinaryGOARCH = binaryGOARCH
		if inspectErr != nil {
			failures.add(inspectCommand.ExitCode, "inspect release-like binary Go version: %v", inspectErr)
			binaryErr = inspectErr
		} else if binaryGoVersion != metadata.GoVersion || binaryGOOS != metadata.GOOS || binaryGOARCH != metadata.GOARCH {
			binaryErr = fmt.Errorf("binary build identity %s %s/%s differs from runner %s %s/%s", binaryGoVersion, binaryGOOS, binaryGOARCH, metadata.GoVersion, metadata.GOOS, metadata.GOARCH)
			failures.add(1, "%v", binaryErr)
		}
	}

	helper, helperCommand, helperErr := buildHelper(repoRoot, privateDir, opts)
	if helperCommand.Name != "" {
		metadata.Commands = append(metadata.Commands, helperCommand)
	}
	if helperErr != nil {
		failures.add(helperCommand.ExitCode, "build subprocess helper: %v", helperErr)
	}

	gotestsum, probe, reporterErr := resolveGotestsum(
		repoRoot,
		opts.reporter,
		opts.reporterChecksum,
		goVersion,
		opts.commandTimeout,
	)
	if probe.Name != "" {
		metadata.Commands = append(metadata.Commands, probe)
	}
	if reporterErr != nil {
		failures.add(probe.ExitCode, "verify E2E reporter: %v", reporterErr)
	}

	initialLeaks := []leakFinding{}
	functionalRawPrivate := filepath.Join(privateDir, "functional-raw.jsonl")
	functionalJUnitPrivate := filepath.Join(privateDir, "functional-junit.xml")
	functionalContracts := filepath.Join(privateDir, "functional-contracts")
	functionalRegistry := filepath.Join(privateDir, "functional-sentinels.jsonl")
	if binaryErr == nil && helperErr == nil && manifestErr == nil && reporterErr == nil {
		env := suiteEnvironment(binary, helper, opts.phase, functionalContracts, functionalRegistry, "", false)
		result := runGotestsum(gotestsum, repoRoot, env, functionalJUnitPrivate, functionalRawPrivate, opts, 1, "off", "^TestE2E$")
		result.Name = "functional-e2e: " + result.Name
		metadata.Commands = append(metadata.Commands, result)
		if result.ExitCode != 0 {
			failures.add(result.ExitCode, "release-like E2E suite failed (exit %d)", result.ExitCode)
		}
	} else {
		failures.add(1, "release-like E2E suite was not started because a prerequisite failed")
	}

	events, eventErr := parseTestEvents(functionalRawPrivate)
	if eventErr != nil {
		failures.add(1, "parse functional test events: %v", eventErr)
		events = map[string]testEvent{}
	}
	feature := buildFeatureCoverage(manifest, suiteDigest, platform, events)
	metadata.Counts = countsFromCoverage(feature)
	metadata.UnexpectedSkips = append([]string{}, feature.UnexpectedSkips...)
	if len(feature.UnexpectedSkips) != 0 {
		failures.add(1, "unexpected platform skips: %s", strings.Join(feature.UnexpectedSkips, ", "))
	}
	if len(feature.MissingCritical) != 0 || feature.CriticalCoveragePct != 100 {
		failures.add(1, "critical feature coverage is %.2f%%; missing: %s", feature.CriticalCoveragePct, strings.Join(feature.MissingCritical, ", "))
	}

	if count, promoteErr := promoteSanitized(functionalRawPrivate, filepath.Join(reportDir, "raw-test.jsonl")); promoteErr != nil {
		failures.add(1, "publish raw test JSONL: %v", promoteErr)
	} else if count > 0 {
		initialLeaks = append(initialLeaks, leakFinding{Path: "raw-test.jsonl", Occurrences: count})
	}
	if count, promoteErr := promoteSanitized(functionalJUnitPrivate, filepath.Join(reportDir, "junit.xml")); promoteErr != nil {
		failures.add(1, "publish JUnit report: %v", promoteErr)
	} else if count > 0 {
		initialLeaks = append(initialLeaks, leakFinding{Path: "junit.xml", Occurrences: count})
	}

	expectedExecuted := applicableScenarioIDs(manifest, platform, false)
	expectedRegistry := applicableScenarioIDs(manifest, platform, true)
	contractsPrivate := filepath.Join(privateDir, "contracts.json")
	contractCount, contractErr := aggregateContracts(functionalContracts, contractsPrivate, expectedExecuted, platform)
	metadata.ContractRecords = contractCount
	if contractErr != nil {
		failures.add(1, "aggregate functional contracts: %v", contractErr)
	}
	if count, promoteErr := promoteSanitized(contractsPrivate, filepath.Join(reportDir, "contracts.json")); promoteErr != nil {
		failures.add(1, "publish contracts: %v", promoteErr)
	} else if count > 0 {
		initialLeaks = append(initialLeaks, leakFinding{Path: "contracts.json", Occurrences: count})
	}
	registryCount, registryErr := validateSentinelRegistryRepeated(functionalRegistry, expectedRegistry, 1)
	metadata.SentinelRecords += registryCount
	if registryErr != nil {
		failures.add(1, "validate functional sentinel registry: %v", registryErr)
	}

	if nativeRuntimeErr != nil {
		failures.add(1, "coverage E2E was not started because the runner is not native: %v", nativeRuntimeErr)
	} else if reporterErr != nil {
		failures.add(1, "coverage E2E was not started because the reporter prerequisite failed: %v", reporterErr)
	} else {
		coverageBinary, coverageBuild := buildCoverageBinary(repoRoot, privateDir, opts)
		metadata.Commands = append(metadata.Commands, coverageBuild)
		if coverageBuild.ExitCode != 0 {
			failures.add(coverageBuild.ExitCode, "coverage binary build failed (exit %d)", coverageBuild.ExitCode)
		} else {
			coverageDir := filepath.Join(privateDir, "covdata")
			if err := os.MkdirAll(coverageDir, 0o700); err != nil {
				failures.add(1, "create GOCOVERDIR: %v", err)
			} else {
				coverageRaw := filepath.Join(privateDir, "coverage-raw.jsonl")
				coverageJUnit := filepath.Join(privateDir, "coverage-junit.xml")
				coverageContracts := filepath.Join(privateDir, "coverage-contracts")
				coverageRegistry := filepath.Join(privateDir, "coverage-sentinels.jsonl")
				env := suiteEnvironment(coverageBinary, helper, opts.phase, coverageContracts, coverageRegistry, coverageDir, false)
				result := runGotestsum(gotestsum, repoRoot, env, coverageJUnit, coverageRaw, opts, 1, "off", "^TestE2E$")
				result.Name = "coverage-e2e: " + result.Name
				metadata.Commands = append(metadata.Commands, result)
				if result.ExitCode != 0 {
					failures.add(result.ExitCode, "coverage E2E suite failed (exit %d)", result.ExitCode)
				}
				if count, promoteErr := promoteSanitized(coverageRaw, filepath.Join(reportDir, "coverage-raw-test.jsonl")); promoteErr != nil {
					failures.add(1, "publish coverage raw JSONL: %v", promoteErr)
				} else if count > 0 {
					initialLeaks = append(initialLeaks, leakFinding{Path: "coverage-raw-test.jsonl", Occurrences: count})
				}
				if count, promoteErr := promoteSanitized(coverageJUnit, filepath.Join(reportDir, "coverage-junit.xml")); promoteErr != nil {
					failures.add(1, "publish coverage JUnit: %v", promoteErr)
				} else if count > 0 {
					initialLeaks = append(initialLeaks, leakFinding{Path: "coverage-junit.xml", Occurrences: count})
				}
				count, registryErr := validateSentinelRegistryRepeated(coverageRegistry, expectedRegistry, 1)
				metadata.SentinelRecords += count
				if registryErr != nil {
					failures.add(1, "validate coverage sentinel registry: %v", registryErr)
				}
				runCoverageReports(repoRoot, privateDir, coverageDir, reportDir, opts, &metadata, failures)
			}
		}
	}

	if binaryErr == nil && helperErr == nil && manifestErr == nil {
		burnLeaks := runBurnIn(repoRoot, privateDir, reportDir, binary, helper, manifest, opts, false, &metadata, failures)
		initialLeaks = append(initialLeaks, burnLeaks...)
		lockingLeaks := runBurnIn(repoRoot, privateDir, reportDir, binary, helper, manifest, opts, true, &metadata, failures)
		initialLeaks = append(initialLeaks, lockingLeaks...)
	} else {
		failures.add(1, "burn-in suites were not started because a prerequisite failed")
	}

	ensureReportPlaceholders(reportDir)
	if err := writeJSON(filepath.Join(reportDir, "feature-coverage.json"), feature); err != nil {
		failures.add(1, "write feature coverage JSON: %v", err)
	}
	if err := writeFeatureMarkdown(filepath.Join(reportDir, "feature-coverage.md"), feature); err != nil {
		failures.add(1, "write feature coverage Markdown: %v", err)
	}
	persistStatus := func(stage string) {
		normalizeFailureEvidence(failures, repoRoot, privateDir)
		normalizeMetadataEvidence(&metadata, repoRoot, privateDir)
		if err := writeFinalStatusReports(reportDir, &metadata, feature, failures); err != nil {
			failures.add(1, "%s final report status: %v", stage, err)
			// A retry records the first write error in every typed status artifact
			// that remains writable; the original non-zero result is preserved.
			_ = writeFinalStatusReports(reportDir, &metadata, feature, failures)
		}
	}
	persistStatus("initialize")
	bundleLeaks, bundleErr := writeFailureBundle(reportDir, privateDir, repoRoot, metadata)
	initialLeaks = append(initialLeaks, bundleLeaks...)
	if bundleErr != nil {
		failures.add(1, "write sanitized failure bundle: %v", bundleErr)
	}
	evidenceDigests, evidenceErr := computeEvidenceDigests(reportDir)
	if evidenceErr != nil {
		failures.add(1, "hash immutable report evidence: %v", evidenceErr)
	} else {
		metadata.EvidenceSHA256 = evidenceDigests
	}
	persistStatus("failure bundle")

	leakReport, leakErr := scanAndSanitizeLeaks(reportDir, metadata.SentinelRecords, initialLeaks)
	if leakErr != nil {
		failures.add(1, "scan reports for sentinel secrets: %v", leakErr)
	} else if leakReport.Detected {
		failures.add(1, "sentinel secret marker detected in %d report location(s)", leakReport.Occurrences)
	}
	persistStatus("initial leak scan")
	// Syntax and completeness are mandatory for failed runs too: CI must never
	// upload an incomplete bundle that merely records the original test error.
	if validationErr := validateReportArtifactsSyntax(reportDir); validationErr != nil {
		failures.add(1, "final report syntax validation: %v", validationErr)
	}
	persistStatus("syntax validation")
	if failures.code == 0 {
		if _, _, validationErr := validateReportDirectory(reportDir); validationErr != nil {
			failures.add(1, "final report validation: %v", validationErr)
		}
	}
	persistStatus("semantic validation")

	// Publish only the status that survived all report validation. A second leak
	// pass includes the newly appended GitHub step summary and preserves any
	// earlier finding even after its bytes were sanitized.
	if err := appendStepSummary(filepath.Join(reportDir, "summary.md")); err != nil {
		failures.add(1, "append GitHub step summary: %v", err)
		persistStatus("step summary")
	}
	finalFindings := initialLeaks
	if leakErr == nil {
		finalFindings = append([]leakFinding{}, leakReport.Findings...)
	}
	failureCountBeforeFinalScan := len(failures.reasons)
	finalLeakReport, finalLeakErr := scanAndSanitizeLeaks(reportDir, metadata.SentinelRecords, finalFindings)
	if finalLeakErr != nil {
		failures.add(1, "final sentinel scan: %v", finalLeakErr)
	} else if finalLeakReport.Detected && !leakReport.Detected {
		failures.add(1, "sentinel secret marker detected during final scan")
	}
	persistStatus("final leak scan")
	if len(failures.reasons) != failureCountBeforeFinalScan {
		// If the summary itself introduced a late leak failure, append the updated
		// failure state so the last human-readable entry is authoritative.
		if err := appendStepSummary(filepath.Join(reportDir, "summary.md")); err != nil {
			failures.add(1, "append updated GitHub step summary: %v", err)
			persistStatus("updated step summary")
		}
	}
	// A marker found here indicates an external race or a scanner defect. Make
	// one fail-closed remediation pass, persist it, then make the read-only
	// assertion the final filesystem operation.
	if err := assertNoSentinelMarker(reportDir); err != nil {
		failures.add(1, "post-sanitization report assertion: %v", err)
		remediated, remediationErr := scanAndSanitizeLeaks(reportDir, metadata.SentinelRecords, finalFindings)
		if remediationErr != nil {
			failures.add(1, "post-assertion sentinel remediation: %v", remediationErr)
		} else if remediated.Detected {
			failures.add(1, "post-assertion sentinel remediation recorded %d location(s)", remediated.Occurrences)
		}
		persistStatus("post-assertion remediation")
		if err := appendStepSummary(filepath.Join(reportDir, "summary.md")); err != nil {
			failures.add(1, "append remediated GitHub step summary: %v", err)
			persistStatus("remediated step summary")
		}
	}
	finalAssertionErr := assertNoSentinelMarker(reportDir)
	if finalAssertionErr != nil {
		// The previous remediation has already made the persisted status fail.
		// Preserve a non-zero process result without writing after this read-only
		// final assertion.
		failures.add(1, "final read-only sentinel assertion: %v", finalAssertionErr)
	}

	fmt.Fprintf(os.Stdout, "env-vault E2E %s %s: %s (%s)\n", opts.phase, platform, strings.ToUpper(metadata.Status), filepath.ToSlash(reportDir))
	if failures.code != 0 {
		return exitStatusError{code: failures.code, err: fmt.Errorf("E2E run failed; see sanitized reports at %s", reportDir)}
	}
	return nil
}

func writeFinalStatusReports(reportDir string, metadata *runMetadata, feature featureCoverage, failures *failureState) error {
	metadata.Status = "pass"
	if failures.code != 0 {
		metadata.Status = "fail"
	}
	metadata.EndedAt = time.Now().UTC()
	metadata.DurationMS = metadata.EndedAt.Sub(metadata.StartedAt).Milliseconds()
	metadata.Failures = append([]string{}, failures.reasons...)
	if err := writeJSON(filepath.Join(reportDir, "metadata.json"), *metadata); err != nil {
		return fmt.Errorf("metadata.json: %w", err)
	}
	if err := writeSummaryReports(reportDir, *metadata, feature); err != nil {
		return fmt.Errorf("summary reports: %w", err)
	}
	if err := writeFailureBundleManifest(reportDir, *metadata); err != nil {
		return fmt.Errorf("failure bundle manifest: %w", err)
	}
	return nil
}

type gotestsumCommand struct {
	name string
}

func runGotestsum(command gotestsumCommand, repoRoot string, env []string, junit, raw string, opts runOptions, count int, shuffle, runPattern string) commandResult {
	args := []string{
		"--format=standard-verbose",
		"--no-color",
		"--junitfile", junit,
		"--jsonfile", raw,
		"--",
		"-count=" + strconv.Itoa(count),
		"-shuffle=" + shuffle,
		"-timeout=" + opts.testTimeout.String(),
	}
	if runPattern != "" {
		args = append(args, "-run", runPattern)
	}
	args = append(args, opts.testPackage)
	return runCommand(commandSpec{
		name:       command.name,
		args:       args,
		dir:        repoRoot,
		env:        env,
		timeout:    opts.testTimeout + 2*time.Minute,
		stdoutPath: junit + ".runner-stdout",
		stderrPath: junit + ".runner-stderr",
	})
}

func buildHelper(repoRoot, privateDir string, opts runOptions) (string, commandResult, error) {
	packageName := opts.helperPackage
	if packageName == "" {
		packageName = "./e2e/testhelper"
	}
	directory := filepath.Join(repoRoot, filepath.FromSlash(strings.TrimPrefix(packageName, "./")))
	if info, err := os.Stat(directory); err != nil || !info.IsDir() {
		return "", commandResult{Name: "go build subprocess-helper", ExitCode: 1}, fmt.Errorf("helper package not found: %s", packageName)
	}
	name := "env-vault-e2e-helper"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	output := filepath.Join(privateDir, "bin", name)
	result := runCommand(commandSpec{
		name:       "go",
		args:       []string{"build", "-trimpath", "-o", output, packageName},
		dir:        repoRoot,
		env:        environment(nil),
		timeout:    opts.commandTimeout,
		stdoutPath: filepath.Join(privateDir, "helper-build.stdout"),
		stderrPath: filepath.Join(privateDir, "helper-build.stderr"),
	})
	if result.ExitCode != 0 {
		return "", result, fmt.Errorf("go build failed with exit code %d", result.ExitCode)
	}
	binary, err := requireRegularBinary(output)
	return binary, result, err
}

func buildCoverageBinary(repoRoot, privateDir string, opts runOptions) (string, commandResult) {
	name := "env-vault-coverage"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	output := filepath.Join(privateDir, "bin", name)
	args := []string{"build", "-trimpath", "-cover", "-coverpkg=./..."}
	if version := firstNonEmpty(os.Getenv("ENV_VAULT_E2E_VERSION"), os.Getenv("VERSION")); version != "" {
		args = append(args, "-ldflags=-X github.com/ildarbinanas-design/env-vault/internal/cli.Version="+version)
	}
	args = append(args, "-o", output, "./cmd/env-vault")
	result := runCommand(commandSpec{
		name:       "go",
		args:       args,
		dir:        repoRoot,
		env:        environment(nil),
		timeout:    opts.commandTimeout,
		stdoutPath: filepath.Join(privateDir, "coverage-build.stdout"),
		stderrPath: filepath.Join(privateDir, "coverage-build.stderr"),
	})
	return output, result
}

func suiteEnvironment(binary, helper, phase, contractsDir, sentinelRegistry, coverDir string, shuffleScenarios bool) []string {
	shuffle := "0"
	if shuffleScenarios {
		shuffle = "1"
	}
	overrides := map[string]string{
		"ENV_VAULT_E2E_BINARY":            binary,
		"ENV_VAULT_E2E_HELPER":            helper,
		"ENV_VAULT_E2E_PHASE":             phase,
		"ENV_VAULT_E2E_CONTRACTS_DIR":     contractsDir,
		"ENV_VAULT_E2E_SENTINEL_REGISTRY": sentinelRegistry,
		"GOCOVERDIR":                      coverDir,
		"ENV_VAULT_E2E_SHUFFLE_SCENARIOS": shuffle,
	}
	return environment(overrides)
}

func runCoverageReports(repoRoot, privateDir, covdataDir, reportDir string, opts runOptions, metadata *runMetadata, failures *failureState) {
	commands := []struct {
		name             string
		args             []string
		output           string
		outputFromStdout bool
	}{
		{name: "go", args: []string{"tool", "covdata", "percent", "-i=" + covdataDir}, output: filepath.Join(reportDir, "coverage-percent.txt"), outputFromStdout: true},
		{name: "go", args: []string{"tool", "covdata", "textfmt", "-i=" + covdataDir, "-o", filepath.Join(reportDir, "coverage.out")}},
		{name: "go", args: []string{"tool", "cover", "-func=" + filepath.Join(reportDir, "coverage.out")}, output: filepath.Join(reportDir, "coverage.txt"), outputFromStdout: true},
		{name: "go", args: []string{"tool", "cover", "-html=" + filepath.Join(reportDir, "coverage.out"), "-o", filepath.Join(reportDir, "coverage.html")}},
	}
	for index, item := range commands {
		stdoutPath := filepath.Join(privateDir, fmt.Sprintf("coverage-command-%d.stdout", index))
		if item.outputFromStdout {
			stdoutPath = item.output
		}
		result := runCommand(commandSpec{
			name:       item.name,
			args:       item.args,
			dir:        repoRoot,
			env:        environment(nil),
			timeout:    opts.commandTimeout,
			stdoutPath: stdoutPath,
			stderrPath: filepath.Join(privateDir, fmt.Sprintf("coverage-command-%d.stderr", index)),
		})
		metadata.Commands = append(metadata.Commands, result)
		if result.ExitCode != 0 {
			failures.add(result.ExitCode, "coverage report command failed: %s", commandLabel(result))
		}
	}
	coverage, err := parseCoveragePercent(filepath.Join(reportDir, "coverage.txt"))
	if err != nil {
		failures.add(1, "parse statement coverage: %v", err)
		return
	}
	metadata.StatementCoverage = coverage
	if opts.coverageFloor > 0 && coverage < opts.coverageFloor {
		failures.add(1, "statement coverage %.2f%% is below floor %.2f%%", coverage, opts.coverageFloor)
	}
}

func runBurnIn(repoRoot, privateDir, reportDir, binary, helper string, manifest scenarioManifest, opts runOptions, locking bool, metadata *runMetadata, failures *failureState) []leakFinding {
	name := "burn-in"
	count := opts.burnInCount
	pattern := "^TestE2E$"
	expected := applicableScenarioIDs(manifest, metadata.Platform, false)
	registryExpected := applicableScenarioIDs(manifest, metadata.Platform, true)
	if locking {
		name = "locking-burn-in"
		count = opts.lockingBurnInCount
		pattern = opts.lockingPattern
		if _, err := matchesGoRunPattern(pattern, "TestE2E/LOCK_TIMEOUT_CRASH_INTEGRITY"); err != nil {
			failures.add(1, "invalid locking burn-in pattern: %v", err)
			return nil
		}
		expected = expected[:0]
		registryExpected = registryExpected[:0]
		for _, item := range manifest.Scenarios {
			matches, _ := matchesGoRunPattern(pattern, item.GoTest)
			if containsString(item.Platforms, metadata.Platform) && matches {
				if !containsString(item.ExpectedPlatformSkips, metadata.Platform) {
					expected = append(expected, item.ID)
				}
			}
		}
		if len(expected) == 0 {
			failures.add(1, "locking burn-in pattern matched no manifest scenarios")
			return nil
		}
		// The locking selector intentionally excludes both platform-specific
		// skip scenarios, so its executed and registry scenario sets are equal.
		registryExpected = append(registryExpected[:0], expected...)
	}
	contractsDir := filepath.Join(privateDir, name+"-contracts")
	registry := filepath.Join(privateDir, name+"-sentinels.jsonl")
	rawPrivate := filepath.Join(privateDir, name+".jsonl")
	env := suiteEnvironment(binary, helper, opts.phase, contractsDir, registry, "", true)
	args := []string{"test", "-json", "-count=" + strconv.Itoa(count), "-shuffle=on", "-timeout=" + opts.testTimeout.String()}
	if pattern != "" {
		args = append(args, "-run", pattern)
	}
	args = append(args, opts.testPackage)
	result := runCommand(commandSpec{
		name:       "go",
		args:       args,
		dir:        repoRoot,
		env:        env,
		timeout:    opts.testTimeout + 2*time.Minute,
		stdoutPath: rawPrivate,
		stderrPath: filepath.Join(privateDir, name+".stderr"),
	})
	result.Name = name + ": " + result.Name
	result.Count = count
	result.Seed = extractShuffleSeed(rawPrivate)
	result.ScenarioSeeds = extractScenarioShuffleSeeds(rawPrivate)
	metadata.Commands = append(metadata.Commands, result)
	if result.ExitCode != 0 {
		failures.add(result.ExitCode, "%s failed (exit %d)", name, result.ExitCode)
	}
	if result.Seed == "" {
		failures.add(1, "%s did not report a shuffle seed", name)
	}
	if err := validateScenarioShuffleSeeds(result.ScenarioSeeds, count); err != nil {
		failures.add(1, "%s scenario shuffle evidence: %v", name, err)
	}
	if err := verifyBurnInEvents(rawPrivate, expected, count); err != nil {
		failures.add(1, "%s event validation: %v", name, err)
	}
	if actual, err := terminalScenarioIDs(rawPrivate, manifest); err != nil {
		failures.add(1, "%s scenario-set validation: %v", name, err)
	} else {
		// Go's -run treats slash-separated expressions as separate subtest
		// selectors. Validate the registry against the scenarios that actually
		// reached a terminal event, while verifyBurnInEvents above still enforces
		// every required locking scenario and repetition.
		registryExpected = actual
	}
	registryCount, err := validateSentinelRegistryRepeated(registry, registryExpected, count)
	metadata.SentinelRecords += registryCount
	if err != nil {
		failures.add(1, "validate %s sentinel registry: %v", name, err)
	}
	destination := filepath.Join(reportDir, name+".jsonl")
	leaks, err := promoteSanitized(rawPrivate, destination)
	if err != nil {
		failures.add(1, "publish %s JSONL: %v", name, err)
		return nil
	}
	if leaks > 0 {
		return []leakFinding{{Path: filepath.Base(destination), Occurrences: leaks}}
	}
	return nil
}

func terminalScenarioIDs(filename string, manifest scenarioManifest) ([]string, error) {
	events, err := parseTestEvents(filename)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	for _, item := range manifest.Scenarios {
		if _, ok := matchTestEvent(events, item.GoTest); ok {
			seen[item.ID] = true
		}
	}
	result := make([]string, 0, len(seen))
	for id := range seen {
		result = append(result, id)
	}
	sort.Strings(result)
	if len(result) == 0 {
		return nil, errors.New("no manifest scenario reached a terminal event")
	}
	return result, nil
}

func verifyBurnInEvents(filename string, expected []string, count int) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	counts := make(map[string]int)
	for _, line := range bytes.Split(data, []byte{'\n'}) {
		var event testEvent
		if json.Unmarshal(line, &event) != nil || event.Action != "pass" || event.Test == "" {
			continue
		}
		for _, id := range expected {
			if event.Test == "TestE2E/"+id || strings.HasSuffix(event.Test, "/"+id) {
				counts[id]++
			}
		}
	}
	var wrong []string
	for _, id := range expected {
		if counts[id] != count {
			wrong = append(wrong, fmt.Sprintf("%s=%d(want %d)", id, counts[id], count))
		}
	}
	if len(wrong) > 0 {
		return fmt.Errorf("scenario pass counts differ: %s", strings.Join(wrong, ", "))
	}
	return nil
}

func extractShuffleSeed(filename string) string {
	data, err := os.ReadFile(filename)
	if err != nil {
		return ""
	}
	pattern := regexp.MustCompile(`-test\.shuffle ([0-9]+)`)
	match := pattern.FindSubmatch(data)
	if len(match) == 2 {
		return string(match[1])
	}
	return ""
}

func matchesGoRunPattern(pattern, testName string) (bool, error) {
	patternParts := strings.Split(pattern, "/")
	nameParts := strings.Split(testName, "/")
	if len(patternParts) > len(nameParts) {
		return false, nil
	}
	for index, part := range patternParts {
		matcher, err := regexp.Compile(part)
		if err != nil {
			return false, err
		}
		if !matcher.MatchString(nameParts[index]) {
			return false, nil
		}
	}
	return true, nil
}

func extractScenarioShuffleSeeds(filename string) []string {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil
	}
	pattern := regexp.MustCompile(`ENV_VAULT_E2E_SCENARIO_SHUFFLE_SEED=([0-9]+)`)
	matches := pattern.FindAllSubmatch(data, -1)
	seeds := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) == 2 {
			seeds = append(seeds, string(match[1]))
		}
	}
	return seeds
}

func validateScenarioShuffleSeeds(seeds []string, count int) error {
	if len(seeds) != count {
		return fmt.Errorf("recorded %d seeds, want %d", len(seeds), count)
	}
	seen := make(map[string]bool, len(seeds))
	for _, seed := range seeds {
		if seed == "" || seen[seed] {
			return fmt.Errorf("seed is empty or repeated: %q", seed)
		}
		seen[seed] = true
	}
	return nil
}

func resolveGoVersion(repoRoot string, timeout time.Duration) (string, commandResult) {
	output, result := commandOutput("go", []string{"env", "GOVERSION"}, repoRoot, environment(nil), timeout)
	version := strings.TrimSpace(string(output))
	if result.ExitCode != 0 || version == "" {
		version = runtime.Version()
	}
	return version, result
}

func inspectBinaryBuild(repoRoot, binary string, timeout time.Duration) (string, string, string, commandResult, error) {
	output, result := commandOutput("go", []string{"version", "-m", binary}, repoRoot, environment(nil), timeout)
	if result.ExitCode != 0 {
		return "", "", "", result, fmt.Errorf("go version -m exited %d: %s", result.ExitCode, result.Error)
	}
	goVersion, goos, goarch, err := parseBinaryBuildInfo(output)
	if err != nil {
		return "", "", "", result, err
	}
	return goVersion, goos, goarch, result, nil
}

func parseBinaryBuildInfo(output []byte) (string, string, string, error) {
	lines := strings.Split(string(output), "\n")
	if len(lines) == 0 {
		return "", "", "", errors.New("go version -m returned empty output")
	}
	line := strings.TrimSpace(lines[0])
	fields := strings.Fields(line)
	if len(fields) < 2 || !strings.HasPrefix(fields[len(fields)-1], "go1.") {
		return "", "", "", fmt.Errorf("go version -m returned no compiler version")
	}
	goVersion := fields[len(fields)-1]
	settings := make(map[string]string)
	for _, raw := range lines[1:] {
		fields := strings.Fields(strings.TrimSpace(raw))
		if len(fields) != 2 || fields[0] != "build" {
			continue
		}
		key, value, ok := strings.Cut(fields[1], "=")
		if ok {
			settings[key] = value
		}
	}
	if settings["GOOS"] == "" || settings["GOARCH"] == "" {
		return "", "", "", fmt.Errorf("go version -m returned no GOOS/GOARCH build settings")
	}
	return goVersion, settings["GOOS"], settings["GOARCH"], nil
}

func subjectKind(opts runOptions) string {
	switch {
	case opts.artifact != "":
		return "artifact"
	case opts.binary != "":
		return "prebuilt"
	default:
		return "built"
	}
}

func resolveCommitSHA(repoRoot string, timeout time.Duration) string {
	if value := firstNonEmpty(os.Getenv("ENV_VAULT_E2E_COMMIT_SHA"), os.Getenv("GITHUB_SHA")); value != "" {
		return value
	}
	output, result := commandOutput("git", []string{"rev-parse", "HEAD"}, repoRoot, environment(nil), timeout)
	if result.ExitCode != 0 {
		return "unknown"
	}
	return strings.TrimSpace(string(output))
}

func validateNativeRuntime(runnerOS string) error {
	if expectedGOOS() != runtime.GOOS || expectedGOARCH() != runtime.GOARCH {
		return fmt.Errorf("requested native platform %s-%s does not match runner %s-%s", expectedGOOS(), expectedGOARCH(), runtime.GOOS, runtime.GOARCH)
	}
	if !runnerOSMatchesGOOS(runnerOS, runtime.GOOS) {
		return fmt.Errorf("runner OS label %q does not match %s", runnerOS, runtime.GOOS)
	}
	return nil
}

func runnerOSMatchesGOOS(label, goos string) bool {
	label = strings.ToLower(strings.TrimSpace(label))
	switch goos {
	case "darwin":
		return label == "darwin" || label == "macos"
	case "linux":
		return label == "linux"
	case "windows":
		return label == "windows"
	default:
		return label == strings.ToLower(goos)
	}
}

func githubRunID() string {
	return firstNonEmpty(os.Getenv("GITHUB_RUN_ID"), "local")
}

func githubRunURL() string {
	runID := os.Getenv("GITHUB_RUN_ID")
	if runID == "" {
		return "local"
	}
	if explicit := strings.TrimSpace(os.Getenv("GITHUB_RUN_URL")); explicit != "" {
		return strings.TrimSuffix(explicit, "/")
	}
	server := strings.TrimSuffix(os.Getenv("GITHUB_SERVER_URL"), "/")
	repository := strings.Trim(os.Getenv("GITHUB_REPOSITORY"), "/")
	if server == "" || repository == "" {
		return "invalid"
	}
	return server + "/" + repository + "/actions/runs/" + runID
}

func githubRunAttempt() string {
	if os.Getenv("GITHUB_RUN_ID") == "" {
		return "local"
	}
	return firstNonEmpty(os.Getenv("GITHUB_RUN_ATTEMPT"), "invalid")
}

func githubRepository() string {
	if os.Getenv("GITHUB_RUN_ID") == "" {
		return "local"
	}
	return firstNonEmpty(strings.Trim(os.Getenv("GITHUB_REPOSITORY"), "/"), "invalid")
}

func normalizeMetadataEvidence(metadata *runMetadata, repoRoot, privateDir string) {
	normalize := func(value string) string { return normalizeEvidenceText(value, repoRoot, privateDir) }
	metadata.Artifact.Path = normalize(metadata.Artifact.Path)
	metadata.Artifact.ChecksumPath = normalize(metadata.Artifact.ChecksumPath)
	for index := range metadata.Commands {
		command := &metadata.Commands[index]
		command.Name = normalizeCommandName(normalize(command.Name))
		for argumentIndex := range command.Arguments {
			command.Arguments[argumentIndex] = normalizeCommandArgument(normalize(command.Arguments[argumentIndex]))
		}
		command.Error = normalize(command.Error)
	}
	for index := range metadata.Failures {
		metadata.Failures[index] = normalize(metadata.Failures[index])
	}
}

func normalizeFailureEvidence(failures *failureState, repoRoot, privateDir string) {
	for index := range failures.reasons {
		failures.reasons[index] = normalizeEvidenceText(failures.reasons[index], repoRoot, privateDir)
	}
}

func normalizeEvidenceText(value, repoRoot, privateDir string) string {
	replacements := [][2]string{{privateDir, "<RUNNER_TMP>"}, {repoRoot, "<REPO>"}}
	if temp := os.TempDir(); temp != "" {
		replacements = append(replacements, [2]string{temp, "<OS_TMP>"})
	}
	// macOS commonly reports a per-user /var/folders temp directory even when
	// an operator supplies an artifact below the conventional /tmp alias.
	// Normalize both spellings, longest first, on every platform.
	replacements = append(replacements, [2]string{"/private/tmp", "<OS_TMP>"}, [2]string{"/tmp", "<OS_TMP>"})
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		replacements = append(replacements, [2]string{home, "<HOME>"})
	}
	for _, replacement := range replacements {
		if replacement[0] == "" {
			continue
		}
		for _, source := range []string{filepath.Clean(replacement[0]), filepath.ToSlash(filepath.Clean(replacement[0]))} {
			value = strings.ReplaceAll(value, source, replacement[1])
		}
	}
	return value
}

func normalizeCommandName(name string) string {
	for _, prefix := range []string{"functional-e2e: ", "coverage-e2e: "} {
		if strings.HasPrefix(name, prefix) {
			return prefix + normalizeExecutableName(strings.TrimPrefix(name, prefix))
		}
	}
	return normalizeExecutableName(name)
}

func normalizeExecutableName(name string) string {
	if filepath.IsAbs(name) {
		return "<TOOL>/" + filepath.Base(name)
	}
	return name
}

func normalizeCommandArgument(argument string) string {
	if filepath.IsAbs(argument) {
		return "<PATH>/" + filepath.Base(argument)
	}
	if index := strings.IndexByte(argument, '='); index > 0 && filepath.IsAbs(argument[index+1:]) {
		return argument[:index+1] + "<PATH>/" + filepath.Base(argument[index+1:])
	}
	return argument
}

func findRepoRoot() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if info, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil && info.Mode().IsRegular() {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", errors.New("could not find repository go.mod")
		}
		directory = parent
	}
}

func safePathComponent(value string) string {
	var out strings.Builder
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '.' || char == '-' || char == '_' {
			out.WriteRune(char)
		} else {
			out.WriteByte('_')
		}
	}
	if out.Len() == 0 {
		return "unknown"
	}
	return out.String()
}

func prepareReportDirectory(repoRoot, directory string) error {
	absRepo, err := filepath.Abs(repoRoot)
	if err != nil {
		return err
	}
	absDir, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(absRepo, absDir)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || relative == ".." {
		return fmt.Errorf("report directory escapes repository: %s", absDir)
	}
	for current := filepath.Dir(absDir); current != absRepo; current = filepath.Dir(current) {
		if info, statErr := os.Lstat(current); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("report parent must not be a symlink: %s", current)
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	if err := os.RemoveAll(absDir); err != nil {
		return fmt.Errorf("clear prior platform reports: %w", err)
	}
	return os.MkdirAll(absDir, 0o700)
}

func promoteSanitized(source, destination string) (int, error) {
	data, err := os.ReadFile(source)
	if err != nil {
		return 0, err
	}
	sanitized, count := redactSentinels(data)
	return count, writeFileAtomic(destination, sanitized, 0o600)
}

func ensureReportPlaceholders(reportDir string) {
	junitFailure := []byte("<?xml version=\"1.0\" encoding=\"UTF-8\"?><testsuites tests=\"1\" failures=\"1\"><testsuite name=\"e2e-runner\" tests=\"1\" failures=\"1\"><testcase name=\"infrastructure\"><failure message=\"report unavailable\">E2E infrastructure did not produce this report.</failure></testcase></testsuite></testsuites>\n")
	placeholders := map[string][]byte{
		"junit.xml":               junitFailure,
		"raw-test.jsonl":          []byte("{\"Action\":\"fail\",\"Package\":\"e2e-runner\"}\n"),
		"coverage-junit.xml":      junitFailure,
		"coverage-raw-test.jsonl": []byte("{\"Action\":\"fail\",\"Package\":\"e2e-runner-coverage\"}\n"),
		"burn-in.jsonl":           []byte("{\"Action\":\"fail\",\"Package\":\"e2e-runner-burn-in\"}\n"),
		"locking-burn-in.jsonl":   []byte("{\"Action\":\"fail\",\"Package\":\"e2e-runner-locking-burn-in\"}\n"),
		"contracts.json":          []byte("{\"schema_version\":\"1\",\"platform\":\"unavailable\",\"scenarios\":{}}\n"),
		"coverage.out":            []byte("mode: set\ne2e-runner-placeholder.go:1.1,1.2 1 0\n"),
		"coverage.txt":            []byte("total:\t(statements)\t0.0%\n"),
		"coverage-percent.txt":    []byte("\te2e-runner/placeholder\t\tcoverage: 0.0% of statements\n"),
		"coverage.html":           []byte("<!doctype html><html><body>coverage unavailable</body></html>\n"),
	}
	for relative, data := range placeholders {
		filename := filepath.Join(reportDir, relative)
		if info, err := os.Stat(filename); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			continue
		}
		_ = writeFileAtomic(filename, data, 0o600)
	}
}

func writeFailureBundle(reportDir, privateDir, repoRoot string, metadata runMetadata) ([]leakFinding, error) {
	directory := filepath.Join(reportDir, "sanitized-failure-bundle")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	if err := writeFailureBundleManifest(reportDir, metadata); err != nil {
		return nil, err
	}
	var output bytes.Buffer
	entries, err := os.ReadDir(privateDir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !(strings.HasSuffix(entry.Name(), ".stdout") || strings.HasSuffix(entry.Name(), ".stderr") || strings.HasSuffix(entry.Name(), ".runner-stdout") || strings.HasSuffix(entry.Name(), ".runner-stderr")) {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(privateDir, entry.Name()))
		if readErr != nil {
			return nil, readErr
		}
		fmt.Fprintf(&output, "===== %s =====\n", entry.Name())
		output.Write(data)
		if len(data) == 0 || data[len(data)-1] != '\n' {
			output.WriteByte('\n')
		}
	}
	normalized := []byte(normalizeEvidenceText(output.String(), repoRoot, privateDir))
	sanitized, count := redactSentinels(normalized)
	if len(sanitized) == 0 {
		sanitized = []byte("No captured command output.\n")
	}
	if err := writeFileAtomic(filepath.Join(directory, "command-output.txt"), sanitized, 0o600); err != nil {
		return nil, err
	}
	if count > 0 {
		return []leakFinding{{Path: "sanitized-failure-bundle/command-output.txt", Occurrences: count}}, nil
	}
	return nil, nil
}

func writeFailureBundleManifest(reportDir string, metadata runMetadata) error {
	directory := filepath.Join(reportDir, "sanitized-failure-bundle")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	manifest := map[string]any{
		"schema_version": reportSchemaVersion,
		"status":         metadata.Status,
		"platform":       metadata.Platform,
		"failures":       metadata.Failures,
		"included_files": []string{"command-output.txt"},
	}
	if err := writeJSON(filepath.Join(directory, "manifest.json"), manifest); err != nil {
		return err
	}
	return nil
}
