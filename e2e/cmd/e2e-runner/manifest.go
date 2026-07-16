package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ildarbinanas-design/env-vault/internal/e2esuite"
)

type scenarioManifest struct {
	SchemaVersion  json.RawMessage `json:"schema_version"`
	SentinelPrefix string          `json:"sentinel_prefix"`
	Scenarios      []scenario      `json:"scenarios"`
}

type scenario struct {
	ID                    string   `json:"id"`
	Feature               string   `json:"feature"`
	Requirement           string   `json:"requirement"`
	GoTest                string   `json:"go_test"`
	Platforms             []string `json:"platforms"`
	Critical              bool     `json:"critical"`
	ExpectedPlatformSkips []string `json:"expected_platform_skips,omitempty"`
}

type testEvent struct {
	Time    string  `json:"Time"`
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
	Output  string  `json:"Output"`
}

type scenarioTrace struct {
	Feature      string   `json:"feature"`
	Requirement  string   `json:"requirement"`
	ScenarioID   string   `json:"scenario_id"`
	GoTest       string   `json:"go_test"`
	Platforms    []string `json:"platforms"`
	Critical     bool     `json:"critical"`
	Result       string   `json:"result"`
	ExpectedSkip bool     `json:"expected_skip"`
	ElapsedMS    int64    `json:"elapsed_ms,omitempty"`
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

func loadManifest(filename string) (scenarioManifest, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return scenarioManifest{}, fmt.Errorf("read scenarios manifest: %w", err)
	}
	var manifest scenarioManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return scenarioManifest{}, fmt.Errorf("parse scenarios manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return scenarioManifest{}, errors.New("parse scenarios manifest: trailing JSON value")
	}
	if !bytes.Equal(bytes.TrimSpace(manifest.SchemaVersion), []byte(reportSchemaVersion)) {
		return scenarioManifest{}, fmt.Errorf("scenarios manifest schema_version must be %s", reportSchemaVersion)
	}
	if len(manifest.Scenarios) == 0 {
		return scenarioManifest{}, errors.New("scenarios manifest contains no scenarios")
	}
	if manifest.SentinelPrefix == "" {
		manifest.SentinelPrefix = defaultSentinelPrefix
	}
	if manifest.SentinelPrefix != defaultSentinelPrefix {
		return scenarioManifest{}, errors.New("scenarios manifest sentinel_prefix does not match the mandatory E2E marker")
	}
	seenID := make(map[string]bool)
	seenTest := make(map[string]bool)
	knownPlatforms := make(map[string]bool)
	for _, platform := range requiredPlatforms() {
		knownPlatforms[platform] = true
	}
	for index := range manifest.Scenarios {
		scenario := &manifest.Scenarios[index]
		scenario.ID = strings.TrimSpace(scenario.ID)
		scenario.GoTest = strings.TrimSpace(scenario.GoTest)
		if scenario.ID == "" || scenario.GoTest == "" || scenario.Feature == "" || scenario.Requirement == "" {
			return scenarioManifest{}, fmt.Errorf("scenario %d is missing id, feature, requirement, or go_test", index)
		}
		if seenID[scenario.ID] {
			return scenarioManifest{}, fmt.Errorf("duplicate scenario ID %q", scenario.ID)
		}
		if seenTest[scenario.GoTest] {
			return scenarioManifest{}, fmt.Errorf("duplicate scenario go_test %q", scenario.GoTest)
		}
		seenID[scenario.ID] = true
		seenTest[scenario.GoTest] = true
		if len(scenario.Platforms) == 0 {
			scenario.Platforms = append([]string(nil), requiredPlatforms()...)
		}
		seenPlatforms := make(map[string]bool)
		for _, platform := range scenario.Platforms {
			if !knownPlatforms[platform] {
				return scenarioManifest{}, fmt.Errorf("scenario %q names unsupported platform %q", scenario.ID, platform)
			}
			if seenPlatforms[platform] {
				return scenarioManifest{}, fmt.Errorf("scenario %q repeats platform %q", scenario.ID, platform)
			}
			seenPlatforms[platform] = true
		}
		seenSkips := make(map[string]bool)
		for _, platform := range scenario.ExpectedPlatformSkips {
			if !knownPlatforms[platform] {
				return scenarioManifest{}, fmt.Errorf("scenario %q names unsupported expected-skip platform %q", scenario.ID, platform)
			}
			if seenSkips[platform] {
				return scenarioManifest{}, fmt.Errorf("scenario %q repeats expected-skip platform %q", scenario.ID, platform)
			}
			seenSkips[platform] = true
		}
		sort.Strings(scenario.Platforms)
		sort.Strings(scenario.ExpectedPlatformSkips)
		for _, platform := range scenario.ExpectedPlatformSkips {
			if !containsString(scenario.Platforms, platform) {
				return scenarioManifest{}, fmt.Errorf("scenario %q expects a skip on unsupported platform %q", scenario.ID, platform)
			}
		}
	}
	return manifest, nil
}

func parseTestEvents(filename string) (map[string]testEvent, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	results := make(map[string]testEvent)
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 8<<20)
	line := 0
	for scanner.Scan() {
		line++
		data := scanner.Bytes()
		if len(strings.TrimSpace(string(data))) == 0 {
			continue
		}
		var event testEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("parse test JSONL line %d: %w", line, err)
		}
		if event.Test == "" {
			continue
		}
		switch event.Action {
		case "pass", "fail", "skip":
			results[event.Test] = event
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read test JSONL: %w", err)
	}
	return results, nil
}

func buildFeatureCoverage(manifest scenarioManifest, suiteHash, platform string, events map[string]testEvent) featureCoverage {
	coverage := featureCoverage{
		SchemaVersion:   reportSchemaVersion,
		Platform:        platform,
		SuiteHash:       suiteHash,
		UnexpectedSkips: []string{},
		MissingCritical: []string{},
		Scenarios:       make([]scenarioTrace, 0, len(manifest.Scenarios)),
	}
	for _, item := range manifest.Scenarios {
		trace := scenarioTrace{
			Feature:      item.Feature,
			Requirement:  item.Requirement,
			ScenarioID:   item.ID,
			GoTest:       item.GoTest,
			Platforms:    append([]string(nil), item.Platforms...),
			Critical:     item.Critical,
			ExpectedSkip: containsString(item.ExpectedPlatformSkips, platform),
			Result:       "not_applicable",
		}
		applicable := containsString(item.Platforms, platform)
		if applicable {
			trace.Result = "missing"
			if event, ok := matchTestEvent(events, item.GoTest); ok {
				trace.Result = event.Action
				trace.ElapsedMS = int64(event.Elapsed * 1000)
			}
			if trace.Result == "skip" && trace.ExpectedSkip {
				trace.Result = "expected_skip"
			} else if trace.Result == "skip" {
				coverage.UnexpectedSkips = append(coverage.UnexpectedSkips, item.ID)
			}
			if item.Critical {
				coverage.CriticalTotal++
				if trace.Result == "pass" || trace.Result == "expected_skip" {
					coverage.CriticalCovered++
				} else {
					coverage.MissingCritical = append(coverage.MissingCritical, item.ID)
				}
			}
		}
		coverage.Scenarios = append(coverage.Scenarios, trace)
	}
	if coverage.CriticalTotal > 0 {
		coverage.CriticalCoveragePct = 100 * float64(coverage.CriticalCovered) / float64(coverage.CriticalTotal)
	}
	sort.Strings(coverage.UnexpectedSkips)
	sort.Strings(coverage.MissingCritical)
	return coverage
}

func matchTestEvent(events map[string]testEvent, goTest string) (testEvent, bool) {
	if event, ok := events[goTest]; ok {
		return event, true
	}
	trimmed := strings.TrimPrefix(goTest, "TestE2E/")
	for name, event := range events {
		if name == "TestE2E/"+trimmed || strings.HasSuffix(name, "/"+trimmed) {
			return event, true
		}
	}
	return testEvent{}, false
}

func suiteHash(repoRoot string) (string, error) {
	return e2esuite.Hash(repoRoot)
}

func canonicalSuiteBytes(data []byte) []byte {
	return e2esuite.CanonicalBytes(data)
}

func aggregateContracts(directory, output string, expected []string, platform string) (int, error) {
	contracts := make(map[string]json.RawMessage)
	entries, err := os.ReadDir(directory)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_ = writeJSON(output, map[string]any{"schema_version": reportSchemaVersion, "platform": platform, "scenarios": contracts})
			return 0, errors.New("E2E suite did not create a contracts directory")
		}
		return 0, fmt.Errorf("read contracts directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			return 0, fmt.Errorf("unexpected contracts entry %q", entry.Name())
		}
		filename := filepath.Join(directory, entry.Name())
		info, err := os.Lstat(filename)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > 8<<20 {
			return 0, fmt.Errorf("contract entry %q is not a bounded regular file", entry.Name())
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return 0, fmt.Errorf("read contract %q: %w", entry.Name(), err)
		}
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			return 0, fmt.Errorf("parse contract %q: %w", entry.Name(), err)
		}
		canonical, err := json.Marshal(value)
		if err != nil {
			return 0, fmt.Errorf("canonicalize contract %q: %w", entry.Name(), err)
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		contracts[id] = canonical
	}
	if err := writeJSON(output, map[string]any{"schema_version": reportSchemaVersion, "platform": platform, "scenarios": contracts}); err != nil {
		return len(contracts), err
	}
	var missing []string
	for _, id := range expected {
		if _, ok := contracts[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) != 0 {
		sort.Strings(missing)
		return len(contracts), fmt.Errorf("contracts missing for scenarios: %s", strings.Join(missing, ", "))
	}
	return len(contracts), nil
}

type sentinelRecord struct {
	SchemaVersion any    `json:"schema_version"`
	ScenarioID    string `json:"scenario_id"`
	SHA256        string `json:"sha256"`
}

func validateSentinelRegistryRepeated(filename string, expected []string, repetitions int) (int, error) {
	file, err := os.Open(filename)
	if err != nil {
		return 0, fmt.Errorf("open sentinel registry: %w", err)
	}
	defer file.Close()
	expectedSet := make(map[string]bool, len(expected))
	for _, id := range expected {
		expectedSet[id] = true
	}
	seenID := make(map[string]int)
	seenHash := make(map[string]bool)
	scanner := bufio.NewScanner(file)
	line := 0
	for scanner.Scan() {
		line++
		var record sentinelRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return len(seenID), fmt.Errorf("parse sentinel registry line %d: %w", line, err)
		}
		if !expectedSet[record.ScenarioID] {
			return line - 1, fmt.Errorf("sentinel registry has unexpected scenario ID %q on line %d", record.ScenarioID, line)
		}
		if len(record.SHA256) != 64 {
			return len(seenID), fmt.Errorf("sentinel registry has invalid hash on line %d", line)
		}
		if _, err := hex.DecodeString(record.SHA256); err != nil || strings.ToLower(record.SHA256) != record.SHA256 || seenHash[record.SHA256] {
			return len(seenID), fmt.Errorf("sentinel registry has invalid or duplicate hash on line %d", line)
		}
		seenID[record.ScenarioID]++
		seenHash[record.SHA256] = true
	}
	if err := scanner.Err(); err != nil {
		return len(seenID), fmt.Errorf("read sentinel registry: %w", err)
	}
	var wrong []string
	for id := range expectedSet {
		// Every scenario creates at least one sentinel. Scenarios exercising
		// multi-secret behavior intentionally create more, so an exact global
		// count would reject useful coverage.
		if seenID[id] < repetitions {
			wrong = append(wrong, fmt.Sprintf("%s=%d(want at least %d)", id, seenID[id], repetitions))
		}
	}
	sort.Strings(wrong)
	if len(wrong) != 0 || len(seenHash) < len(expectedSet)*repetitions {
		return len(seenHash), fmt.Errorf("sentinel registry record counts differ: %s", strings.Join(wrong, ", "))
	}
	return len(seenHash), nil
}

func applicableScenarioIDs(manifest scenarioManifest, platform string, includeExpectedSkips bool) []string {
	var result []string
	for _, item := range manifest.Scenarios {
		if !containsString(item.Platforms, platform) {
			continue
		}
		if !includeExpectedSkips && containsString(item.ExpectedPlatformSkips, platform) {
			continue
		}
		result = append(result, item.ID)
	}
	sort.Strings(result)
	return result
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
