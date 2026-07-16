package releasemetrics

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"time"
)

const (
	BaselineSchemaID   = "env-vault.release-metrics-baseline.v1"
	ComparisonSchemaID = "env-vault.release-metrics-comparison.v1"
	baselineVersion    = 1

	ScenarioMainCI    = "main_ci"
	ScenarioPRCI      = "pr_ci"
	ScenarioPublisher = "publisher"
)

var scenarioOrder = []string{ScenarioMainCI, ScenarioPRCI, ScenarioPublisher}

// Baseline deliberately omits queue time because it was not captured for the
// historical runs. Comparison must never manufacture unavailable observations.
type Baseline struct {
	SchemaID      string                `json:"schema_id"`
	SchemaVersion int                   `json:"schema_version"`
	Measurements  []BaselineMeasurement `json:"measurements"`
}

type BaselineMeasurement struct {
	Scenario               string `json:"scenario"`
	RunID                  int64  `json:"run_id"`
	JobCount               int64  `json:"job_count"`
	WallSeconds            int64  `json:"wall_seconds"`
	AggregateRunnerSeconds int64  `json:"aggregate_runner_seconds"`
}

type Comparison struct {
	SchemaID             string                `json:"schema_id"`
	SchemaVersion        int                   `json:"schema_version"`
	BaselineSchema       string                `json:"baseline_schema"`
	CurrentMetricsSchema string                `json:"current_metrics_schema"`
	QueueComparison      UnavailableComparison `json:"queue_comparison"`
	Scenarios            []ScenarioComparison  `json:"scenarios"`
	Totals               MeasureSetComparison  `json:"totals"`
}

type UnavailableComparison struct {
	Available  bool   `json:"available"`
	ReasonCode string `json:"reason_code"`
}

type ScenarioComparison struct {
	Scenario        string               `json:"scenario"`
	BaselineRunID   int64                `json:"baseline_run_id"`
	CurrentRunID    int64                `json:"current_run_id"`
	CurrentAttempt  int                  `json:"current_attempt"`
	CurrentHeadSHA  string               `json:"current_head_sha"`
	CurrentWorkflow string               `json:"current_workflow"`
	CurrentEvent    string               `json:"current_event"`
	Measures        MeasureSetComparison `json:"measures"`
}

type MeasureSetComparison struct {
	JobCount               MeasureComparison `json:"job_count"`
	WallSeconds            MeasureComparison `json:"wall_seconds"`
	AggregateRunnerSeconds MeasureComparison `json:"aggregate_runner_seconds"`
}

// Savings are baseline minus current. A negative value is an explicit
// regression, not clamped to zero. PercentSavings is rounded to two decimals.
type MeasureComparison struct {
	Baseline        int64   `json:"baseline"`
	Current         int64   `json:"current"`
	AbsoluteSavings int64   `json:"absolute_savings"`
	PercentSavings  float64 `json:"percent_savings"`
}

func DecodeBaseline(data []byte) (Baseline, error) {
	var baseline Baseline
	if err := decodeExactJSON(data, &baseline); err != nil {
		return Baseline{}, fmt.Errorf("decode metrics baseline: %w", err)
	}
	if err := validateBaseline(baseline); err != nil {
		return Baseline{}, fmt.Errorf("validate metrics baseline: %w", err)
	}
	return baseline, nil
}

// DecodeMetrics reads a persisted Metrics document with the same strict JSON
// rules as the gh transport decoder: exact field spelling, no duplicates, no
// unknown fields, and exactly one JSON value.
func DecodeMetrics(data []byte) (Metrics, error) {
	var metrics Metrics
	if err := decodeExactJSON(data, &metrics); err != nil {
		return Metrics{}, fmt.Errorf("decode release metrics: %w", err)
	}
	if err := Validate(metrics); err != nil {
		return Metrics{}, fmt.Errorf("validate release metrics: %w", err)
	}
	return metrics, nil
}

// Compare requires one exact successful current metrics document for every
// baseline scenario. The caller assigns documents to scenarios explicitly;
// transport and GitHub observation remain outside this offline package.
func Compare(baseline Baseline, current map[string]Metrics) (Comparison, error) {
	if err := validateBaseline(baseline); err != nil {
		return Comparison{}, fmt.Errorf("validate metrics baseline: %w", err)
	}
	if len(current) != len(scenarioOrder) {
		return Comparison{}, fmt.Errorf("current metrics must contain exactly %d scenarios", len(scenarioOrder))
	}

	baselineByScenario := make(map[string]BaselineMeasurement, len(baseline.Measurements))
	for _, measurement := range baseline.Measurements {
		baselineByScenario[measurement.Scenario] = measurement
	}
	comparison := Comparison{
		SchemaID:             ComparisonSchemaID,
		SchemaVersion:        1,
		BaselineSchema:       baseline.SchemaID,
		CurrentMetricsSchema: SchemaID,
		QueueComparison: UnavailableComparison{
			Available:  false,
			ReasonCode: "baseline_queue_not_recorded",
		},
		Scenarios: make([]ScenarioComparison, 0, len(scenarioOrder)),
	}

	var baselineTotals, currentTotals measurementValues
	seenCurrentRunIDs := make(map[int64]string, len(current))
	for _, scenario := range scenarioOrder {
		metrics, ok := current[scenario]
		if !ok {
			return Comparison{}, fmt.Errorf("current metrics is missing scenario %q", scenario)
		}
		if err := Validate(metrics); err != nil {
			return Comparison{}, fmt.Errorf("current metrics %q: %w", scenario, err)
		}
		if err := validateScenarioIdentity(scenario, metrics); err != nil {
			return Comparison{}, err
		}
		if previousScenario, exists := seenCurrentRunIDs[metrics.RunID]; exists {
			return Comparison{}, fmt.Errorf("current metrics scenarios %q and %q use the same run_id %d", previousScenario, scenario, metrics.RunID)
		}
		seenCurrentRunIDs[metrics.RunID] = scenario
		baselineMeasurement := baselineByScenario[scenario]
		baselineValues := measurementValues{
			Jobs:   baselineMeasurement.JobCount,
			Wall:   baselineMeasurement.WallSeconds,
			Runner: baselineMeasurement.AggregateRunnerSeconds,
		}
		currentValues := measurementValues{
			Jobs:   int64(metrics.JobCount),
			Wall:   metrics.WallSeconds,
			Runner: metrics.AggregateRunnerSeconds,
		}
		comparison.Scenarios = append(comparison.Scenarios, ScenarioComparison{
			Scenario:        scenario,
			BaselineRunID:   baselineMeasurement.RunID,
			CurrentRunID:    metrics.RunID,
			CurrentAttempt:  metrics.Attempt,
			CurrentHeadSHA:  metrics.HeadSHA,
			CurrentWorkflow: metrics.WorkflowName,
			CurrentEvent:    metrics.Event,
			Measures:        compareValues(baselineValues, currentValues),
		})
		baselineTotals.add(baselineValues)
		currentTotals.add(currentValues)
	}
	for scenario := range current {
		if !knownScenario(scenario) {
			return Comparison{}, fmt.Errorf("current metrics has unknown scenario %q", scenario)
		}
	}
	comparison.Totals = compareValues(baselineTotals, currentTotals)
	return comparison, nil
}

func RenderComparisonMarkdown(comparison Comparison) (string, error) {
	if comparison.SchemaID != ComparisonSchemaID || comparison.SchemaVersion != 1 {
		return "", errors.New("unsupported metrics comparison schema")
	}
	if len(comparison.Scenarios) != len(scenarioOrder) {
		return "", errors.New("metrics comparison does not contain all scenarios")
	}
	var output strings.Builder
	output.WriteString("# Release pipeline metrics comparison\n\n")
	output.WriteString("| Scenario | Jobs (before → after) | Jobs saved | Wall seconds (before → after) | Wall saved | Runner seconds (before → after) | Runner saved |\n")
	output.WriteString("|---|---:|---:|---:|---:|---:|---:|\n")
	for index, expected := range scenarioOrder {
		scenario := comparison.Scenarios[index]
		if scenario.Scenario != expected {
			return "", errors.New("metrics comparison scenarios are not in canonical order")
		}
		writeMarkdownRow(&output, scenario.Scenario, scenario.Measures)
	}
	writeMarkdownRow(&output, "total", comparison.Totals)
	output.WriteString("\nQueue time is not compared because the historical baseline did not record it.\n")
	return output.String(), nil
}

type measurementValues struct {
	Jobs   int64
	Wall   int64
	Runner int64
}

func (values *measurementValues) add(other measurementValues) {
	values.Jobs += other.Jobs
	values.Wall += other.Wall
	values.Runner += other.Runner
}

func compareValues(baseline, current measurementValues) MeasureSetComparison {
	return MeasureSetComparison{
		JobCount:               compareMeasure(baseline.Jobs, current.Jobs),
		WallSeconds:            compareMeasure(baseline.Wall, current.Wall),
		AggregateRunnerSeconds: compareMeasure(baseline.Runner, current.Runner),
	}
}

func compareMeasure(baseline, current int64) MeasureComparison {
	savings := baseline - current
	percent := math.Round((float64(savings)/float64(baseline))*10000) / 100
	if percent == 0 { // Avoid a non-canonical JSON negative zero.
		percent = 0
	}
	return MeasureComparison{
		Baseline: baseline, Current: current, AbsoluteSavings: savings, PercentSavings: percent,
	}
}

func writeMarkdownRow(output *strings.Builder, name string, measures MeasureSetComparison) {
	fmt.Fprintf(output, "| %s | %d → %d | %d (%.2f%%) | %d → %d | %d (%.2f%%) | %d → %d | %d (%.2f%%) |\n",
		name,
		measures.JobCount.Baseline, measures.JobCount.Current, measures.JobCount.AbsoluteSavings, measures.JobCount.PercentSavings,
		measures.WallSeconds.Baseline, measures.WallSeconds.Current, measures.WallSeconds.AbsoluteSavings, measures.WallSeconds.PercentSavings,
		measures.AggregateRunnerSeconds.Baseline, measures.AggregateRunnerSeconds.Current, measures.AggregateRunnerSeconds.AbsoluteSavings, measures.AggregateRunnerSeconds.PercentSavings,
	)
}

func validateBaseline(baseline Baseline) error {
	if baseline.SchemaID != BaselineSchemaID || baseline.SchemaVersion != baselineVersion {
		return errors.New("unsupported metrics baseline schema")
	}
	if len(baseline.Measurements) != len(scenarioOrder) {
		return fmt.Errorf("measurements must contain exactly %d scenarios", len(scenarioOrder))
	}
	seen := make(map[string]bool, len(baseline.Measurements))
	for index, measurement := range baseline.Measurements {
		if !knownScenario(measurement.Scenario) {
			return fmt.Errorf("measurements[%d] has unknown scenario %q", index, measurement.Scenario)
		}
		if measurement.Scenario != scenarioOrder[index] {
			return fmt.Errorf("measurements[%d] must be canonical scenario %q", index, scenarioOrder[index])
		}
		if seen[measurement.Scenario] {
			return fmt.Errorf("measurements[%d] duplicates scenario %q", index, measurement.Scenario)
		}
		seen[measurement.Scenario] = true
		if measurement.RunID <= 0 || measurement.JobCount <= 0 || measurement.WallSeconds <= 0 || measurement.AggregateRunnerSeconds <= 0 {
			return fmt.Errorf("measurements[%d] contains a non-positive value", index)
		}
	}
	for _, scenario := range scenarioOrder {
		if !seen[scenario] {
			return fmt.Errorf("measurements is missing scenario %q", scenario)
		}
	}
	return nil
}

func knownScenario(scenario string) bool {
	switch scenario {
	case ScenarioMainCI, ScenarioPRCI, ScenarioPublisher:
		return true
	default:
		return false
	}
}

func validateScenarioIdentity(scenario string, metrics Metrics) error {
	switch scenario {
	case ScenarioMainCI:
		if metrics.WorkflowName != "ci" || metrics.Event != "push" {
			return fmt.Errorf("current metrics %q must be workflow ci event push", scenario)
		}
	case ScenarioPRCI:
		if metrics.WorkflowName != "ci" || metrics.Event != "pull_request" {
			return fmt.Errorf("current metrics %q must be workflow ci event pull_request", scenario)
		}
	case ScenarioPublisher:
		if metrics.WorkflowName != "build-binaries" || (metrics.Event != "push" && metrics.Event != "workflow_dispatch") {
			return fmt.Errorf("current metrics %q must be workflow build-binaries event push or workflow_dispatch", scenario)
		}
	default:
		return fmt.Errorf("unknown metrics scenario %q", scenario)
	}
	return nil
}

// Validate applies the complete fail-closed validation used for persisted
// release metrics. Callers may add workflow-specific identity constraints, but
// should not duplicate timing, aggregate, or canonical-form validation.
func Validate(metrics Metrics) error {
	if metrics.SchemaID != SchemaID || metrics.InputSchema != "gh-run-view.v1" {
		return errors.New("unsupported release metrics schema")
	}
	if metrics.RunID <= 0 || metrics.Attempt <= 0 || metrics.RetryCount != metrics.Attempt-1 {
		return errors.New("invalid run or attempt identity")
	}
	if !shaPattern.MatchString(metrics.HeadSHA) {
		return errors.New("head_sha must be exactly 40 lowercase hexadecimal characters")
	}
	if metrics.Conclusion != "success" {
		return fmt.Errorf("metrics conclusion %q is not successful", metrics.Conclusion)
	}
	if strings.TrimSpace(metrics.WorkflowName) == "" || strings.TrimSpace(metrics.Event) == "" {
		return errors.New("workflow_name and event must be non-empty")
	}
	created, err := parseCanonicalTime("created_at", metrics.CreatedAt)
	if err != nil {
		return err
	}
	started, err := parseCanonicalTime("started_at", metrics.StartedAt)
	if err != nil {
		return err
	}
	completed, err := parseCanonicalTime("completed_at", metrics.CompletedAt)
	if err != nil {
		return err
	}
	queueSeconds, err := normalizedQueueSeconds(created, started)
	if err != nil || completed.Before(started) {
		return errors.New("metrics timestamps are not ordered")
	}
	if metrics.QueueSeconds != queueSeconds || metrics.WallSeconds != seconds(completed.Sub(started)) {
		return errors.New("queue_seconds or wall_seconds does not match timestamps")
	}
	if metrics.JobCount <= 0 || metrics.JobCount != len(metrics.Jobs) || metrics.AggregateRunnerSeconds < 0 {
		return errors.New("invalid job aggregates")
	}
	if err := validateTransferMetric("artifact_transfer", metrics.ArtifactTransferSeconds); err != nil {
		return err
	}
	if err := validateTransferMetric("cache_transfer", metrics.CacheTransferSeconds); err != nil {
		return err
	}
	if metrics.CriticalPath.Method != "observed_terminal_span" || strings.TrimSpace(metrics.CriticalPath.Note) == "" || metrics.CriticalPath.Seconds < 0 {
		return errors.New("invalid critical_path")
	}

	seenIDs := make(map[int64]bool, len(metrics.Jobs))
	seenNames := make(map[string]bool, len(metrics.Jobs))
	var runnerTotal int64
	var previous JobMetric
	var terminal time.Time
	terminalName := ""
	for index, job := range metrics.Jobs {
		if job.ID <= 0 || strings.TrimSpace(job.Name) != job.Name || job.Name == "" || (job.Conclusion != "success" && job.Conclusion != "skipped" && job.Conclusion != "neutral") {
			return fmt.Errorf("jobs[%d] has invalid identity or conclusion", index)
		}
		if seenIDs[job.ID] || seenNames[job.Name] {
			return fmt.Errorf("jobs[%d] duplicates an identity", index)
		}
		seenIDs[job.ID], seenNames[job.Name] = true, true
		jobStarted, err := parseCanonicalTime(fmt.Sprintf("jobs[%d].started_at", index), job.StartedAt)
		if err != nil {
			return err
		}
		jobCompleted, err := parseCanonicalTime(fmt.Sprintf("jobs[%d].completed_at", index), job.CompletedAt)
		if err != nil {
			return err
		}
		duration := jobCompleted.Sub(jobStarted)
		if jobStarted.Before(created) || job.RunnerSeconds < 0 || (duration < 0 && (job.Conclusion != "skipped" || duration < -2*time.Second)) || (job.Conclusion != "skipped" && jobCompleted.After(completed)) {
			return fmt.Errorf("jobs[%d] has invalid timing", index)
		}
		expectedRunnerSeconds := seconds(duration)
		if job.Conclusion == "skipped" {
			expectedRunnerSeconds = 0
		}
		if job.RunnerSeconds != expectedRunnerSeconds {
			return fmt.Errorf("jobs[%d] runner_seconds does not match timing", index)
		}
		if index > 0 && (job.StartedAt < previous.StartedAt || (job.StartedAt == previous.StartedAt && job.Name < previous.Name)) {
			return errors.New("jobs are not in canonical order")
		}
		previous = job
		runnerTotal += job.RunnerSeconds
		if laterTerminal(jobCompleted, job.Name, terminal, terminalName) {
			terminal, terminalName = jobCompleted, job.Name
		}
	}
	if runnerTotal != metrics.AggregateRunnerSeconds {
		return errors.New("aggregate_runner_seconds does not equal job total")
	}
	if metrics.CriticalPath.TerminalJob != terminalName || metrics.CriticalPath.Seconds != seconds(terminal.Sub(started)) || metrics.CriticalPath.Seconds > metrics.WallSeconds {
		return errors.New("critical_path does not match terminal job")
	}
	if metrics.ArtifactTransferSeconds.Seconds > runnerTotal || metrics.CacheTransferSeconds.Seconds > runnerTotal {
		return errors.New("transfer duration exceeds aggregate runner duration")
	}
	return nil
}

func validateTransferMetric(name string, metric TransferMetric) error {
	if metric.Available != (len(metric.Steps) > 0) || metric.Seconds < 0 {
		return fmt.Errorf("%s has inconsistent availability", name)
	}
	if !metric.Available && metric.Seconds != 0 {
		return fmt.Errorf("%s unavailable observation has non-zero seconds", name)
	}
	if !sort.StringsAreSorted(metric.Steps) {
		return fmt.Errorf("%s steps are not in canonical order", name)
	}
	for index, step := range metric.Steps {
		if strings.TrimSpace(step) != step || step == "" || (index > 0 && step == metric.Steps[index-1]) {
			return fmt.Errorf("%s contains an invalid or duplicate step", name)
		}
	}
	return nil
}

func parseCanonicalTime(name, value string) (time.Time, error) {
	parsed, err := parseTime(name, value)
	if err != nil {
		return time.Time{}, err
	}
	if parsed.Format(time.RFC3339) != value {
		return time.Time{}, fmt.Errorf("%s must be canonical UTC RFC3339", name)
	}
	return parsed, nil
}

// decodeExactJSON derives the accepted JSON member names from the destination
// type and validates tokens before encoding/json gets a chance to perform its
// case-insensitive matching or last-duplicate-wins behavior.
func decodeExactJSON(data []byte, destination any) error {
	typeOfDestination := reflect.TypeOf(destination)
	if typeOfDestination == nil || typeOfDestination.Kind() != reflect.Pointer || typeOfDestination.Elem().Kind() != reflect.Struct {
		return errors.New("destination must point to a struct")
	}
	shapeDecoder := json.NewDecoder(bytes.NewReader(data))
	shapeDecoder.UseNumber()
	if err := consumeExactValue(shapeDecoder, typeOfDestination.Elem(), "$"); err != nil {
		return err
	}
	if err := requireEOF(shapeDecoder); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return requireEOF(decoder)
}

func consumeExactValue(decoder *json.Decoder, expected reflect.Type, path string) error {
	for expected.Kind() == reflect.Pointer {
		expected = expected.Elem()
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	switch expected.Kind() {
	case reflect.Struct:
		if !isDelimiter || delimiter != '{' {
			return fmt.Errorf("%s must be an object", path)
		}
		fields := jsonFields(expected)
		seen := make(map[string]bool, len(fields))
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok {
				return fmt.Errorf("%s has a non-string object key", path)
			}
			fieldType, known := fields[name]
			if !known {
				return fmt.Errorf("%s has unknown or non-canonical field %q", path, name)
			}
			if seen[name] {
				return fmt.Errorf("%s has duplicate field %q", path, name)
			}
			seen[name] = true
			if err := consumeExactValue(decoder, fieldType, path+"."+name); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil {
			return err
		}
		for name := range fields {
			if !seen[name] {
				return fmt.Errorf("%s is missing field %q", path, name)
			}
		}
		return nil
	case reflect.Slice, reflect.Array:
		if !isDelimiter || delimiter != '[' {
			return fmt.Errorf("%s must be an array", path)
		}
		index := 0
		for decoder.More() {
			if err := consumeExactValue(decoder, expected.Elem(), fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
			index++
		}
		_, err := decoder.Token()
		return err
	default:
		if isDelimiter || token == nil {
			return fmt.Errorf("%s must be a non-null scalar", path)
		}
		return nil
	}
}

func jsonFields(structType reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type, structType.NumField())
	for index := 0; index < structType.NumField(); index++ {
		field := structType.Field(index)
		if !field.IsExported() {
			continue
		}
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name == "" {
			name = field.Name
		}
		if name != "-" {
			fields[name] = field.Type
		}
	}
	return fields
}
