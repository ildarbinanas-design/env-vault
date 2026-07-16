package main

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/ildarbinanas-design/env-vault/internal/releasemetrics"
)

// runMetricsCompare is kept separate so the root dispatcher can wire the
// command without mixing comparison policy into main.go.
func runMetricsCompare(args []string, stdout, stderr io.Writer) int {
	set := newFlagSet("metrics compare")
	baselinePath := set.String("baseline", "release/metrics-baseline.v1.json", "checked-in metrics baseline JSON")
	mainCIPath := set.String("main-ci", "", "current main CI metrics JSON")
	prCIPath := set.String("pr-ci", "", "current pull-request CI metrics JSON")
	publisherPath := set.String("publisher", "", "current publisher metrics JSON")
	outputPath := set.String("output", "", "comparison JSON output file, or - for stdout")
	markdownPath := set.String("markdown-output", "", "optional Markdown output file")
	if err := set.Parse(args); err != nil || set.NArg() != 0 || *mainCIPath == "" || *prCIPath == "" || *publisherPath == "" || *outputPath == "" || *markdownPath == "-" || (*outputPath == "-" && *markdownPath != "") {
		fmt.Fprint(stderr, metricsCompareUsage())
		return exitUsage
	}

	baselineData, err := readLimitedInput(*baselinePath, 1<<20)
	if err != nil {
		return writeFailure(stdout, stderr, *outputPath == "-", "METRICS_COMPARISON_INVALID", err, exitSnapshotInvalid)
	}
	baseline, err := releasemetrics.DecodeBaseline(baselineData)
	if err != nil {
		return writeFailure(stdout, stderr, *outputPath == "-", "METRICS_COMPARISON_INVALID", err, exitSnapshotInvalid)
	}
	paths := []struct {
		scenario string
		path     string
	}{
		{scenario: releasemetrics.ScenarioMainCI, path: *mainCIPath},
		{scenario: releasemetrics.ScenarioPRCI, path: *prCIPath},
		{scenario: releasemetrics.ScenarioPublisher, path: *publisherPath},
	}
	current := make(map[string]releasemetrics.Metrics, len(paths))
	for _, input := range paths {
		data, readErr := readLimitedInput(input.path, 32<<20)
		if readErr != nil {
			return writeFailure(stdout, stderr, *outputPath == "-", "METRICS_COMPARISON_INVALID", fmt.Errorf("read %s metrics: %w", input.scenario, readErr), exitSnapshotInvalid)
		}
		metrics, decodeErr := releasemetrics.DecodeMetrics(data)
		if decodeErr != nil {
			return writeFailure(stdout, stderr, *outputPath == "-", "METRICS_COMPARISON_INVALID", fmt.Errorf("%s: %w", input.scenario, decodeErr), exitSnapshotInvalid)
		}
		current[input.scenario] = metrics
	}
	comparison, err := releasemetrics.Compare(baseline, current)
	if err != nil {
		return writeFailure(stdout, stderr, *outputPath == "-", "METRICS_COMPARISON_INVALID", err, exitSnapshotInvalid)
	}
	markdown, err := releasemetrics.RenderComparisonMarkdown(comparison)
	if err != nil {
		return writeFailure(stdout, stderr, *outputPath == "-", "METRICS_COMPARISON_INVALID", err, exitSnapshotInvalid)
	}

	if *outputPath == "-" {
		if err := writeJSON(stdout, comparison); err != nil {
			fmt.Fprintf(stderr, "write JSON: %v\n", err)
			return exitInternal
		}
		return exitOK
	}
	encoded, err := json.Marshal(comparison)
	if err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	if err := writeExclusiveFile(*outputPath, append(encoded, '\n')); err != nil {
		return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
	}
	if *markdownPath != "" {
		if err := writeExclusiveFile(*markdownPath, []byte(markdown)); err != nil {
			return writeFailure(stdout, stderr, false, "OUTPUT_FAILED", err, exitInternal)
		}
	}
	return exitOK
}

func metricsCompareUsage() string {
	return "usage: releasecheck metrics compare --main-ci FILE --pr-ci FILE --publisher FILE --output FILE|- [--baseline FILE] [--markdown-output FILE]\n"
}
