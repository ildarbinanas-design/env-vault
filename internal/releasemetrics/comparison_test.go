package releasemetrics

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCheckedInMetricsBaseline(t *testing.T) {
	data, err := os.ReadFile("../../release/metrics-baseline.v1.json")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := DecodeBaseline(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []BaselineMeasurement{
		{Scenario: ScenarioMainCI, RunID: 29475607744, JobCount: 25, WallSeconds: 387, AggregateRunnerSeconds: 1253},
		{Scenario: ScenarioPRCI, RunID: 29479484474, JobCount: 25, WallSeconds: 359, AggregateRunnerSeconds: 1205},
		{Scenario: ScenarioPublisher, RunID: 29475939348, JobCount: 30, WallSeconds: 417, AggregateRunnerSeconds: 1280},
	}
	if encoded, _ := json.Marshal(baseline.Measurements); string(encoded) != mustJSON(t, want) {
		t.Fatalf("checked-in baseline mismatch:\n got %s\nwant %s", encoded, mustJSON(t, want))
	}
}

func TestDecodeBaselineRejectsAmbiguousAndNonCanonicalJSON(t *testing.T) {
	valid := mustJSON(t, testBaseline())
	tests := map[string]string{
		"unknown":       strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"queue_seconds":4`, 1),
		"duplicate":     strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1),
		"case variant":  strings.Replace(valid, `"schema_id"`, `"Schema_ID"`, 1),
		"second value":  valid + `{}`,
		"missing field": strings.Replace(valid, `,"wall_seconds":387`, ``, 1),
	}
	for name, document := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeBaseline([]byte(document)); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}

	reordered := testBaseline()
	reordered.Measurements[0], reordered.Measurements[1] = reordered.Measurements[1], reordered.Measurements[0]
	if _, err := DecodeBaseline([]byte(mustJSON(t, reordered))); err == nil {
		t.Fatal("non-canonical scenario order was accepted")
	}
}

func TestDecodeMetricsRoundTripAndStrictness(t *testing.T) {
	metrics, err := Compute(validRun())
	if err != nil {
		t.Fatal(err)
	}
	valid := mustJSON(t, metrics)
	decoded, err := DecodeMetrics([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.RunID != metrics.RunID || decoded.AggregateRunnerSeconds != metrics.AggregateRunnerSeconds {
		t.Fatalf("round trip mismatch: %+v", decoded)
	}

	tests := map[string]string{
		"unknown":        strings.Replace(valid, `"run_id":29475607744`, `"run_id":29475607744,"invented":1`, 1),
		"duplicate":      strings.Replace(valid, `"attempt":2`, `"attempt":2,"attempt":2`, 1),
		"case variant":   strings.Replace(valid, `"head_sha"`, `"Head_SHA"`, 1),
		"tampered total": strings.Replace(valid, `"aggregate_runner_seconds":90`, `"aggregate_runner_seconds":89`, 1),
		"failed run":     strings.Replace(valid, `"conclusion":"success"`, `"conclusion":"failure"`, 1),
	}
	for name, document := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeMetrics([]byte(document)); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestDecodeMetricsAcceptsComputedSkippedClockSkew(t *testing.T) {
	run := validRun()
	run.Jobs[1].Conclusion = "skipped"
	run.Jobs[1].StartedAt = "2026-07-16T06:01:31Z"
	run.Jobs[1].CompletedAt = "2026-07-16T06:01:30Z"
	run.Jobs[1].Steps = nil
	metrics, err := Compute(run)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeMetrics([]byte(mustJSON(t, metrics))); err != nil {
		t.Fatal(err)
	}
}

func TestDecodeMetricsAcceptsOnlyBoundedRunClockSkew(t *testing.T) {
	data, err := os.ReadFile("testdata/run-clock-skew-attempt3.json")
	if err != nil {
		t.Fatal(err)
	}
	run, err := DecodeGHRun(data)
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := Compute(run)
	if err != nil {
		t.Fatal(err)
	}
	encoded := []byte(mustJSON(t, metrics))
	decoded, err := DecodeMetrics(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.QueueSeconds != 0 || decoded.CreatedAt != run.CreatedAt || decoded.StartedAt != run.StartedAt {
		t.Fatalf("bounded skew was not normalized without timestamp mutation: %+v", decoded)
	}

	wrongQueue := metrics
	wrongQueue.QueueSeconds = -1
	if _, err := DecodeMetrics([]byte(mustJSON(t, wrongQueue))); err == nil {
		t.Fatal("negative queue duration for bounded skew was accepted")
	}

	tooLarge := metrics
	tooLarge.CreatedAt = "2026-07-16T07:35:41Z"
	tooLarge.Jobs[0].StartedAt = "2026-07-16T07:35:41Z"
	if _, err := DecodeMetrics([]byte(mustJSON(t, tooLarge))); err == nil {
		t.Fatal("three-second persisted run skew was accepted")
	}
}

func TestValidateRejectsPersistedJobBeforeAttemptCreation(t *testing.T) {
	metrics, err := Compute(validRun())
	if err != nil {
		t.Fatal(err)
	}
	metrics.Jobs[0].StartedAt = "2026-07-16T05:59:56Z"
	metrics.Jobs[0].RunnerSeconds = 64
	metrics.AggregateRunnerSeconds = 94
	if err := Validate(metrics); err == nil || !strings.Contains(err.Error(), "invalid timing") {
		t.Fatalf("persisted reused job was not rejected: %v", err)
	}
}

func TestCompareComputesPerScenarioAndTotalSavings(t *testing.T) {
	current := map[string]Metrics{
		ScenarioMainCI:    comparisonMetrics(t, 31001, "ci", "push", 10, 300, 900),
		ScenarioPRCI:      comparisonMetrics(t, 31002, "ci", "pull_request", 11, 250, 800),
		ScenarioPublisher: comparisonMetrics(t, 31003, "build-binaries", "push", 12, 200, 700),
	}
	comparison, err := Compare(testBaseline(), current)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.QueueComparison.Available || comparison.QueueComparison.ReasonCode != "baseline_queue_not_recorded" {
		t.Fatalf("queue availability was invented: %+v", comparison.QueueComparison)
	}
	main := comparison.Scenarios[0].Measures
	if main.JobCount.AbsoluteSavings != 15 || main.JobCount.PercentSavings != 60 {
		t.Fatalf("unexpected main job savings: %+v", main.JobCount)
	}
	if main.WallSeconds.AbsoluteSavings != 87 || main.WallSeconds.PercentSavings != 22.48 {
		t.Fatalf("unexpected main wall savings: %+v", main.WallSeconds)
	}
	if comparison.Totals.JobCount != (MeasureComparison{Baseline: 80, Current: 33, AbsoluteSavings: 47, PercentSavings: 58.75}) {
		t.Fatalf("unexpected total jobs: %+v", comparison.Totals.JobCount)
	}
	if comparison.Totals.WallSeconds != (MeasureComparison{Baseline: 1163, Current: 750, AbsoluteSavings: 413, PercentSavings: 35.51}) {
		t.Fatalf("unexpected total wall: %+v", comparison.Totals.WallSeconds)
	}
	if comparison.Totals.AggregateRunnerSeconds != (MeasureComparison{Baseline: 3738, Current: 2400, AbsoluteSavings: 1338, PercentSavings: 35.79}) {
		t.Fatalf("unexpected total runner: %+v", comparison.Totals.AggregateRunnerSeconds)
	}

	markdown, err := RenderComparisonMarkdown(comparison)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"| main_ci | 25 → 10 | 15 (60.00%) | 387 → 300 | 87 (22.48%) | 1253 → 900 | 353 (28.17%) |",
		"| total | 80 → 33 | 47 (58.75%) | 1163 → 750 | 413 (35.51%) | 3738 → 2400 | 1338 (35.79%) |",
		"Queue time is not compared because the historical baseline did not record it.",
	} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("Markdown is missing %q:\n%s", expected, markdown)
		}
	}
}

func TestCompareReportsRegressionAndFailsClosed(t *testing.T) {
	current := map[string]Metrics{
		ScenarioMainCI:    comparisonMetrics(t, 31001, "ci", "push", 26, 400, 1300),
		ScenarioPRCI:      comparisonMetrics(t, 31002, "ci", "pull_request", 25, 359, 1205),
		ScenarioPublisher: comparisonMetrics(t, 31003, "build-binaries", "push", 30, 417, 1280),
	}
	comparison, err := Compare(testBaseline(), current)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Scenarios[0].Measures.JobCount.AbsoluteSavings != -1 || comparison.Scenarios[0].Measures.JobCount.PercentSavings != -4 {
		t.Fatalf("regression was hidden: %+v", comparison.Scenarios[0].Measures.JobCount)
	}

	delete(current, ScenarioPRCI)
	if _, err := Compare(testBaseline(), current); err == nil {
		t.Fatal("missing current scenario was accepted")
	}
	current[ScenarioPRCI] = comparisonMetrics(t, 31002, "ci", "pull_request", 25, 359, 1205)
	current["unknown"] = current[ScenarioPRCI]
	if _, err := Compare(testBaseline(), current); err == nil {
		t.Fatal("extra current scenario was accepted")
	}
	delete(current, "unknown")
	wrongIdentity := current[ScenarioPRCI]
	wrongIdentity.Event = "push"
	current[ScenarioPRCI] = wrongIdentity
	if _, err := Compare(testBaseline(), current); err == nil {
		t.Fatal("misassigned scenario identity was accepted")
	}
	current[ScenarioPRCI] = comparisonMetrics(t, 31002, "ci", "pull_request", 25, 359, 1205)
	duplicateRun := current[ScenarioPublisher]
	duplicateRun.RunID = current[ScenarioPRCI].RunID
	current[ScenarioPublisher] = duplicateRun
	if _, err := Compare(testBaseline(), current); err == nil {
		t.Fatal("one current run was accepted for two scenarios")
	}
	current[ScenarioPublisher] = comparisonMetrics(t, 31003, "build-binaries", "push", 30, 417, 1280)
	failed := current[ScenarioPublisher]
	failed.Conclusion = "failure"
	current[ScenarioPublisher] = failed
	if _, err := Compare(testBaseline(), current); err == nil {
		t.Fatal("non-success current metrics was accepted")
	}
}

func testBaseline() Baseline {
	return Baseline{
		SchemaID: BaselineSchemaID, SchemaVersion: 1,
		Measurements: []BaselineMeasurement{
			{Scenario: ScenarioMainCI, RunID: 29475607744, JobCount: 25, WallSeconds: 387, AggregateRunnerSeconds: 1253},
			{Scenario: ScenarioPRCI, RunID: 29479484474, JobCount: 25, WallSeconds: 359, AggregateRunnerSeconds: 1205},
			{Scenario: ScenarioPublisher, RunID: 29475939348, JobCount: 30, WallSeconds: 417, AggregateRunnerSeconds: 1280},
		},
	}
}

func comparisonMetrics(t *testing.T, runID int64, workflow, event string, jobs int, wall, runner int64) Metrics {
	t.Helper()
	jobMetrics := make([]JobMetric, jobs)
	baseRunner := runner / int64(jobs)
	remainder := runner % int64(jobs)
	for index := range jobMetrics {
		duration := baseRunner
		if int64(index) < remainder {
			duration++
		}
		jobMetrics[index] = JobMetric{
			ID: int64(index + 1), Name: strings.Repeat("a", index+1), Conclusion: "success",
			StartedAt: timestamp(0), CompletedAt: timestamp(duration), RunnerSeconds: duration,
		}
	}
	return Metrics{
		SchemaID: SchemaID, InputSchema: "gh-run-view.v1", RunID: runID, Attempt: 1,
		WorkflowName: workflow, Event: event, HeadSHA: strings.Repeat("a", 40), Conclusion: "success",
		CreatedAt: timestamp(0), StartedAt: timestamp(0), CompletedAt: timestamp(wall),
		QueueSeconds: 0, WallSeconds: wall, JobCount: jobs, AggregateRunnerSeconds: runner, RetryCount: 0,
		CriticalPath:            CriticalPath{Method: "observed_terminal_span", Seconds: jobMetrics[0].RunnerSeconds, TerminalJob: jobMetrics[0].Name, Note: "observed"},
		ArtifactTransferSeconds: TransferMetric{Steps: []string{}}, CacheTransferSeconds: TransferMetric{Steps: []string{}}, Jobs: jobMetrics,
	}
}

func timestamp(offset int64) string {
	return "2026-07-16T00:" + twoDigits(offset/60) + ":" + twoDigits(offset%60) + "Z"
}

func twoDigits(value int64) string {
	return string([]byte{'0' + byte(value/10), '0' + byte(value%10)})
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
