package releasemetrics

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCompute(t *testing.T) {
	run := validRun()
	metrics, err := Compute(run)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.SchemaID != SchemaID || metrics.QueueSeconds != 3 || metrics.WallSeconds != 117 {
		t.Fatalf("unexpected run metrics: %+v", metrics)
	}
	if metrics.JobCount != 2 || metrics.AggregateRunnerSeconds != 90 || metrics.RetryCount != 1 {
		t.Fatalf("unexpected aggregate metrics: %+v", metrics)
	}
	if metrics.CriticalPath.TerminalJob != "quality / native-linux-amd64" || metrics.CriticalPath.Seconds != 90 {
		t.Fatalf("unexpected critical path: %+v", metrics.CriticalPath)
	}
	if !metrics.ArtifactTransferSeconds.Available || metrics.ArtifactTransferSeconds.Seconds != 4 {
		t.Fatalf("unexpected artifact metric: %+v", metrics.ArtifactTransferSeconds)
	}
	if metrics.CacheTransferSeconds.Available || metrics.CacheTransferSeconds.Seconds != 0 {
		t.Fatalf("unexpected cache metric: %+v", metrics.CacheTransferSeconds)
	}
}

func TestComputeNormalizesSkippedClockSkew(t *testing.T) {
	run := validRun()
	run.Jobs[1].Conclusion = "skipped"
	run.Jobs[1].StartedAt = "2026-07-16T06:01:31Z"
	run.Jobs[1].CompletedAt = "2026-07-16T06:01:30Z"
	run.Jobs[1].Steps = nil
	metrics, err := Compute(run)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.AggregateRunnerSeconds != 60 || metrics.Jobs[1].RunnerSeconds != 0 {
		t.Fatalf("skipped job consumed runner time: %+v", metrics)
	}
}

func TestComputeCriticalPathTieBreakIsDeterministic(t *testing.T) {
	run := validRun()
	run.Jobs[0].CompletedAt = run.Jobs[1].CompletedAt
	run.Jobs[0].Name = "quality / z-last"
	run.Jobs[1].Name = "quality / a-first"

	first, err := Compute(run)
	if err != nil {
		t.Fatal(err)
	}
	run.Jobs[0], run.Jobs[1] = run.Jobs[1], run.Jobs[0]
	second, err := Compute(run)
	if err != nil {
		t.Fatal(err)
	}
	if first.CriticalPath != second.CriticalPath || first.CriticalPath.TerminalJob != "quality / a-first" {
		t.Fatalf("critical path depends on input order: first=%+v second=%+v", first.CriticalPath, second.CriticalPath)
	}
	if err := Validate(first); err != nil {
		t.Fatalf("deterministically selected critical path does not validate: %v", err)
	}
}

func TestComputeAcceptsBoundedRunClockSkewFromRealAttempt(t *testing.T) {
	tests := []struct {
		path       string
		runID      int64
		attempt    int
		createdAt  string
		startedAt  string
		persistent bool
	}{
		{"testdata/run-clock-skew-publisher.json", 29475939348, 1, "2026-07-16T06:10:31Z", "2026-07-16T06:10:30Z", false},
		{"testdata/run-clock-skew-attempt3.json", 29479864725, 3, "2026-07-16T07:35:39Z", "2026-07-16T07:35:38Z", true},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			data, err := os.ReadFile(test.path)
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
			if metrics.RunID != test.runID || metrics.Attempt != test.attempt || metrics.QueueSeconds != 0 {
				t.Fatalf("unexpected real-attempt metrics: %+v", metrics)
			}
			if metrics.CreatedAt != test.createdAt || metrics.StartedAt != test.startedAt {
				t.Fatalf("raw run timestamps were not preserved: created=%s started=%s", metrics.CreatedAt, metrics.StartedAt)
			}
			// Persisted comparison/evidence metrics require a successful run. The
			// failed v0.0.8 publisher fixture exercises computation only.
			if test.persistent {
				if err := Validate(metrics); err != nil {
					t.Fatalf("computed metrics failed persisted-document validation: %v", err)
				}
			}
		})
	}
}

func TestComputeRejectsRunClockSkewBeyondBound(t *testing.T) {
	bounded := validRun()
	bounded.CreatedAt = "2026-07-16T06:00:02Z"
	bounded.StartedAt = "2026-07-16T06:00:00Z"
	bounded.Jobs[0].StartedAt = bounded.CreatedAt
	bounded.Jobs[0].CompletedAt = "2026-07-16T06:01:02Z"
	metrics, err := Compute(bounded)
	if err != nil || metrics.QueueSeconds != 0 {
		t.Fatalf("two-second run skew was not accepted and normalized: metrics=%+v err=%v", metrics, err)
	}
	if err := Validate(metrics); err != nil {
		t.Fatalf("two-second run skew did not survive persisted validation: %v", err)
	}

	run := validRun()
	run.CreatedAt = "2026-07-16T06:00:03Z"
	run.StartedAt = "2026-07-16T06:00:00Z"
	if _, err := Compute(run); err == nil || !strings.Contains(err.Error(), "more than 2 seconds") {
		t.Fatalf("three-second run skew was not rejected: %v", err)
	}
}

func TestComputeStillRejectsJobsBeforeAttemptCreation(t *testing.T) {
	data, err := os.ReadFile("testdata/run-clock-skew-attempt3.json")
	if err != nil {
		t.Fatal(err)
	}
	run, err := DecodeGHRun(data)
	if err != nil {
		t.Fatal(err)
	}
	// A failed-only rerun can return jobs reused from an older attempt. Even
	// though bounded run-level skew is accepted, those jobs must never count.
	run.Jobs[0].StartedAt = "2026-07-16T07:35:38Z"
	if _, err := Compute(run); err == nil || !strings.Contains(err.Error(), "startedAt precedes run createdAt") {
		t.Fatalf("job predating attempt creation was not rejected: %v", err)
	}
}

func TestComputeRejectsIncompleteAndMalformedInputs(t *testing.T) {
	tests := map[string]func(*GHRun){
		"run incomplete":         func(run *GHRun) { run.Status = "in_progress" },
		"unknown conclusion":     func(run *GHRun) { run.Jobs[0].Conclusion = "mystery" },
		"duplicate job":          func(run *GHRun) { run.Jobs[1].DatabaseID = run.Jobs[0].DatabaseID },
		"executed negative time": func(run *GHRun) { run.Jobs[0].CompletedAt = "2026-07-16T05:59:59Z" },
		"bad head SHA":           func(run *GHRun) { run.HeadSHA = strings.Repeat("A", 40) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			run := validRun()
			mutate(&run)
			if _, err := Compute(run); err == nil {
				t.Fatal("expected failure")
			}
		})
	}
}

func TestDecodeGHRunIsStrictAndSingleDocument(t *testing.T) {
	encoded, err := json.Marshal(validRun())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeGHRun(encoded); err != nil {
		t.Fatal(err)
	}
	withUnknown := strings.Replace(string(encoded), `"attempt":2`, `"attempt":2,"token":"secret"`, 1)
	if _, err := DecodeGHRun([]byte(withUnknown)); err == nil {
		t.Fatal("unknown field was accepted")
	}
	withDuplicate := strings.Replace(string(encoded), `"attempt":2`, `"attempt":2,"attempt":3`, 1)
	if _, err := DecodeGHRun([]byte(withDuplicate)); err == nil {
		t.Fatal("duplicate field was accepted")
	}
	withCaseVariant := strings.Replace(string(encoded), `"databaseId":29475607744`, `"DatabaseID":29475607744`, 1)
	if _, err := DecodeGHRun([]byte(withCaseVariant)); err == nil {
		t.Fatal("case-variant field was accepted")
	}
	if _, err := DecodeGHRun(append(encoded, []byte("\n{}")...)); err == nil {
		t.Fatal("second document was accepted")
	}
}

func validRun() GHRun {
	return GHRun{
		Attempt:      2,
		Conclusion:   "success",
		CreatedAt:    "2026-07-16T05:59:57Z",
		DatabaseID:   29475607744,
		Event:        "push",
		HeadSHA:      strings.Repeat("a", 40),
		StartedAt:    "2026-07-16T06:00:00Z",
		Status:       "completed",
		UpdatedAt:    "2026-07-16T06:01:57Z",
		URL:          "https://github.example/actions/runs/29475607744",
		WorkflowName: "ci",
		Jobs: []GHJob{
			{
				CompletedAt: "2026-07-16T06:01:00Z",
				Conclusion:  "success",
				DatabaseID:  1,
				Name:        "quality / source",
				StartedAt:   "2026-07-16T06:00:00Z",
				Status:      "completed",
				URL:         "https://github.example/jobs/1",
				Steps: []GHStep{
					{CompletedAt: "2026-07-16T06:00:14Z", Conclusion: "success", Name: "Run tests", Number: 1, StartedAt: "2026-07-16T06:00:04Z", Status: "completed"},
				},
			},
			{
				CompletedAt: "2026-07-16T06:01:30Z",
				Conclusion:  "success",
				DatabaseID:  2,
				Name:        "quality / native-linux-amd64",
				StartedAt:   "2026-07-16T06:01:00Z",
				Status:      "completed",
				URL:         "https://github.example/jobs/2",
				Steps: []GHStep{
					{CompletedAt: "2026-07-16T06:01:06Z", Conclusion: "success", Name: "Download artifact", Number: 1, StartedAt: "2026-07-16T06:01:02Z", Status: "completed"},
				},
			},
		},
	}
}
