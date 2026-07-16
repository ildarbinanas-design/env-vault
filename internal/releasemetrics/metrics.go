package releasemetrics

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
)

const SchemaID = "env-vault.release-metrics.v1"

const maxRunStartClockSkew = 2 * time.Second

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// GHRun is the deliberately small, versioned input surface emitted by:
//
//	gh run view RUN_ID --attempt RUN_ATTEMPT --json attempt,conclusion,createdAt,databaseId,event,headSha,jobs,startedAt,status,updatedAt,url,workflowName
//
// The checker never invokes gh itself. Keeping the transport document separate
// makes it possible to archive and re-check the exact observation later.
type GHRun struct {
	Attempt      int     `json:"attempt"`
	Conclusion   string  `json:"conclusion"`
	CreatedAt    string  `json:"createdAt"`
	DatabaseID   int64   `json:"databaseId"`
	Event        string  `json:"event"`
	HeadSHA      string  `json:"headSha"`
	Jobs         []GHJob `json:"jobs"`
	StartedAt    string  `json:"startedAt"`
	Status       string  `json:"status"`
	UpdatedAt    string  `json:"updatedAt"`
	URL          string  `json:"url"`
	WorkflowName string  `json:"workflowName"`
}

type GHJob struct {
	CompletedAt string   `json:"completedAt"`
	Conclusion  string   `json:"conclusion"`
	DatabaseID  int64    `json:"databaseId"`
	Name        string   `json:"name"`
	StartedAt   string   `json:"startedAt"`
	Status      string   `json:"status"`
	Steps       []GHStep `json:"steps"`
	URL         string   `json:"url"`
}

type GHStep struct {
	CompletedAt string `json:"completedAt"`
	Conclusion  string `json:"conclusion"`
	Name        string `json:"name"`
	Number      int    `json:"number"`
	StartedAt   string `json:"startedAt"`
	Status      string `json:"status"`
}

type Metrics struct {
	SchemaID                string         `json:"schema_id"`
	InputSchema             string         `json:"input_schema"`
	RunID                   int64          `json:"run_id"`
	Attempt                 int            `json:"attempt"`
	WorkflowName            string         `json:"workflow_name"`
	Event                   string         `json:"event"`
	HeadSHA                 string         `json:"head_sha"`
	Conclusion              string         `json:"conclusion"`
	CreatedAt               string         `json:"created_at"`
	StartedAt               string         `json:"started_at"`
	CompletedAt             string         `json:"completed_at"`
	QueueSeconds            int64          `json:"queue_seconds"`
	WallSeconds             int64          `json:"wall_seconds"`
	JobCount                int            `json:"job_count"`
	AggregateRunnerSeconds  int64          `json:"aggregate_runner_seconds"`
	RetryCount              int            `json:"retry_count"`
	CriticalPath            CriticalPath   `json:"critical_path"`
	ArtifactTransferSeconds TransferMetric `json:"artifact_transfer"`
	CacheTransferSeconds    TransferMetric `json:"cache_transfer"`
	Jobs                    []JobMetric    `json:"jobs"`
}

type CriticalPath struct {
	Method      string `json:"method"`
	Seconds     int64  `json:"seconds"`
	TerminalJob string `json:"terminal_job"`
	Note        string `json:"note"`
}

type TransferMetric struct {
	Available bool     `json:"available"`
	Seconds   int64    `json:"seconds"`
	Steps     []string `json:"steps"`
}

type JobMetric struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Conclusion    string `json:"conclusion"`
	StartedAt     string `json:"started_at"`
	CompletedAt   string `json:"completed_at"`
	RunnerSeconds int64  `json:"runner_seconds"`
}

func DecodeGHRun(data []byte) (GHRun, error) {
	if err := validateJSONShape(data); err != nil {
		return GHRun{}, fmt.Errorf("decode gh run view document: %w", err)
	}
	var run GHRun
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&run); err != nil {
		return GHRun{}, fmt.Errorf("decode gh run view document: %w", err)
	}
	if err := requireEOF(decoder); err != nil {
		return GHRun{}, err
	}
	return run, nil
}

type jsonShape uint8

const (
	shapeScalar jsonShape = iota
	shapeRoot
	shapeJobs
	shapeJob
	shapeSteps
	shapeStep
)

var shapeFields = map[jsonShape]map[string]jsonShape{
	shapeRoot: {
		"attempt": shapeScalar, "conclusion": shapeScalar, "createdAt": shapeScalar,
		"databaseId": shapeScalar, "event": shapeScalar, "headSha": shapeScalar,
		"jobs": shapeJobs, "startedAt": shapeScalar, "status": shapeScalar,
		"updatedAt": shapeScalar, "url": shapeScalar, "workflowName": shapeScalar,
	},
	shapeJob: {
		"completedAt": shapeScalar, "conclusion": shapeScalar, "databaseId": shapeScalar,
		"name": shapeScalar, "startedAt": shapeScalar, "status": shapeScalar,
		"steps": shapeSteps, "url": shapeScalar,
	},
	shapeStep: {
		"completedAt": shapeScalar, "conclusion": shapeScalar, "name": shapeScalar,
		"number": shapeScalar, "startedAt": shapeScalar, "status": shapeScalar,
	},
}

// encoding/json deliberately matches struct fields case-insensitively and
// silently keeps the last duplicate object member. Both behaviours are unsafe
// for durable release evidence, so validate the exact transport shape first.
func validateJSONShape(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := consumeJSONShape(decoder, shapeRoot, "$"); err != nil {
		return err
	}
	return requireEOF(decoder)
}

func consumeJSONShape(decoder *json.Decoder, shape jsonShape, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch delimiter := token.(type) {
	case json.Delim:
		switch delimiter {
		case '{':
			fields, ok := shapeFields[shape]
			if !ok {
				return fmt.Errorf("%s has unexpected object value", path)
			}
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
				child, known := fields[name]
				if !known {
					return fmt.Errorf("%s has unknown or non-canonical field %q", path, name)
				}
				if seen[name] {
					return fmt.Errorf("%s has duplicate field %q", path, name)
				}
				seen[name] = true
				if err := consumeJSONShape(decoder, child, path+"."+name); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			var child jsonShape
			switch shape {
			case shapeJobs:
				child = shapeJob
			case shapeSteps:
				child = shapeStep
			default:
				return fmt.Errorf("%s has unexpected array value", path)
			}
			index := 0
			for decoder.More() {
				if err := consumeJSONShape(decoder, child, fmt.Sprintf("%s[%d]", path, index)); err != nil {
					return err
				}
				index++
			}
			_, err = decoder.Token()
			return err
		default:
			return fmt.Errorf("%s has unexpected delimiter", path)
		}
	default:
		if shape != shapeScalar {
			return fmt.Errorf("%s has unexpected scalar value", path)
		}
		return nil
	}
}

func Compute(run GHRun) (Metrics, error) {
	if err := validateRunIdentity(run); err != nil {
		return Metrics{}, err
	}
	created, err := parseTime("createdAt", run.CreatedAt)
	if err != nil {
		return Metrics{}, err
	}
	started, err := parseTime("startedAt", run.StartedAt)
	if err != nil {
		return Metrics{}, err
	}
	updated, err := parseTime("updatedAt", run.UpdatedAt)
	if err != nil {
		return Metrics{}, err
	}
	queueSeconds, err := normalizedQueueSeconds(created, started)
	if err != nil {
		return Metrics{}, err
	}
	if updated.Before(started) {
		return Metrics{}, errors.New("run updatedAt precedes startedAt")
	}

	metrics := Metrics{
		SchemaID:     SchemaID,
		InputSchema:  "gh-run-view.v1",
		RunID:        run.DatabaseID,
		Attempt:      run.Attempt,
		WorkflowName: run.WorkflowName,
		Event:        run.Event,
		HeadSHA:      run.HeadSHA,
		Conclusion:   run.Conclusion,
		CreatedAt:    created.Format(time.RFC3339),
		StartedAt:    started.Format(time.RFC3339),
		CompletedAt:  updated.Format(time.RFC3339),
		QueueSeconds: queueSeconds,
		WallSeconds:  seconds(updated.Sub(started)),
		JobCount:     len(run.Jobs),
		RetryCount:   run.Attempt - 1,
		CriticalPath: CriticalPath{
			Method: "observed_terminal_span",
			Note:   "Observed makespan to the last completed job; the GitHub run view document does not expose the workflow dependency DAG.",
		},
		ArtifactTransferSeconds: TransferMetric{Steps: []string{}},
		CacheTransferSeconds:    TransferMetric{Steps: []string{}},
		Jobs:                    make([]JobMetric, 0, len(run.Jobs)),
	}

	seenIDs := make(map[int64]struct{}, len(run.Jobs))
	seenNames := make(map[string]struct{}, len(run.Jobs))
	var terminalTime time.Time
	for index, job := range run.Jobs {
		jobMetric, jobStarted, jobCompleted, err := validateAndMeasureJob(job, index)
		if err != nil {
			return Metrics{}, err
		}
		if _, exists := seenIDs[job.DatabaseID]; exists {
			return Metrics{}, fmt.Errorf("jobs[%d] duplicates databaseId %d", index, job.DatabaseID)
		}
		seenIDs[job.DatabaseID] = struct{}{}
		if _, exists := seenNames[job.Name]; exists {
			return Metrics{}, fmt.Errorf("jobs[%d] duplicates name %q", index, job.Name)
		}
		seenNames[job.Name] = struct{}{}
		if jobStarted.Before(created) {
			return Metrics{}, fmt.Errorf("jobs[%d] startedAt precedes run createdAt", index)
		}
		// GitHub occasionally reports a skipped job's startedAt one second after
		// completedAt. Skipped jobs consume no runner time and are normalized to
		// zero, while every executed job remains strictly ordered.
		if job.Conclusion != "skipped" && jobCompleted.After(updated) {
			return Metrics{}, fmt.Errorf("jobs[%d] completedAt follows run updatedAt", index)
		}
		metrics.AggregateRunnerSeconds += jobMetric.RunnerSeconds
		metrics.Jobs = append(metrics.Jobs, jobMetric)
		if laterTerminal(jobCompleted, job.Name, terminalTime, metrics.CriticalPath.TerminalJob) {
			terminalTime = jobCompleted
			metrics.CriticalPath.TerminalJob = job.Name
		}
		if err := addTransferMetrics(&metrics, job, index); err != nil {
			return Metrics{}, err
		}
	}
	if metrics.CriticalPath.TerminalJob == "" {
		return Metrics{}, errors.New("run contains no jobs")
	}
	metrics.CriticalPath.Seconds = seconds(terminalTime.Sub(started))

	sort.Slice(metrics.Jobs, func(i, j int) bool {
		if metrics.Jobs[i].StartedAt == metrics.Jobs[j].StartedAt {
			return metrics.Jobs[i].Name < metrics.Jobs[j].Name
		}
		return metrics.Jobs[i].StartedAt < metrics.Jobs[j].StartedAt
	})
	sort.Strings(metrics.ArtifactTransferSeconds.Steps)
	sort.Strings(metrics.CacheTransferSeconds.Steps)
	metrics.ArtifactTransferSeconds.Available = len(metrics.ArtifactTransferSeconds.Steps) > 0
	metrics.CacheTransferSeconds.Available = len(metrics.CacheTransferSeconds.Steps) > 0
	return metrics, nil
}

func validateRunIdentity(run GHRun) error {
	if run.DatabaseID <= 0 {
		return errors.New("databaseId must be positive")
	}
	if run.Attempt <= 0 {
		return errors.New("attempt must be positive")
	}
	if !shaPattern.MatchString(run.HeadSHA) {
		return errors.New("headSha must be exactly 40 lowercase hexadecimal characters")
	}
	if strings.TrimSpace(run.WorkflowName) == "" || strings.TrimSpace(run.Event) == "" {
		return errors.New("workflowName and event must be non-empty")
	}
	if run.Status != "completed" {
		return fmt.Errorf("run status %q is incomplete", run.Status)
	}
	if !knownConclusion(run.Conclusion) || run.Conclusion == "skipped" {
		return fmt.Errorf("run conclusion %q is invalid", run.Conclusion)
	}
	if len(run.Jobs) == 0 {
		return errors.New("jobs must be non-empty")
	}
	return nil
}

func validateAndMeasureJob(job GHJob, index int) (JobMetric, time.Time, time.Time, error) {
	if job.DatabaseID <= 0 || strings.TrimSpace(job.Name) == "" {
		return JobMetric{}, time.Time{}, time.Time{}, fmt.Errorf("jobs[%d] has invalid identity", index)
	}
	if job.Status != "completed" || !knownConclusion(job.Conclusion) {
		return JobMetric{}, time.Time{}, time.Time{}, fmt.Errorf("jobs[%d] is incomplete or has unknown conclusion", index)
	}
	started, err := parseTime(fmt.Sprintf("jobs[%d].startedAt", index), job.StartedAt)
	if err != nil {
		return JobMetric{}, time.Time{}, time.Time{}, err
	}
	completed, err := parseTime(fmt.Sprintf("jobs[%d].completedAt", index), job.CompletedAt)
	if err != nil {
		return JobMetric{}, time.Time{}, time.Time{}, err
	}
	duration := completed.Sub(started)
	if duration < 0 {
		if job.Conclusion != "skipped" || duration < -2*time.Second {
			return JobMetric{}, time.Time{}, time.Time{}, fmt.Errorf("jobs[%d] completedAt precedes startedAt", index)
		}
		duration = 0
	}
	if job.Conclusion == "skipped" {
		duration = 0
	}
	return JobMetric{
		ID:            job.DatabaseID,
		Name:          job.Name,
		Conclusion:    job.Conclusion,
		StartedAt:     started.Format(time.RFC3339),
		CompletedAt:   completed.Format(time.RFC3339),
		RunnerSeconds: seconds(duration),
	}, started, completed, nil
}

func addTransferMetrics(metrics *Metrics, job GHJob, jobIndex int) error {
	seenSteps := make(map[int]struct{}, len(job.Steps))
	for stepIndex, step := range job.Steps {
		if step.Number <= 0 || strings.TrimSpace(step.Name) == "" {
			return fmt.Errorf("jobs[%d].steps[%d] has invalid identity", jobIndex, stepIndex)
		}
		if _, exists := seenSteps[step.Number]; exists {
			return fmt.Errorf("jobs[%d].steps[%d] duplicates step number %d", jobIndex, stepIndex, step.Number)
		}
		seenSteps[step.Number] = struct{}{}
		if step.Status != "completed" || !knownConclusion(step.Conclusion) {
			return fmt.Errorf("jobs[%d].steps[%d] is incomplete or has unknown conclusion", jobIndex, stepIndex)
		}
		started, err := parseTime(fmt.Sprintf("jobs[%d].steps[%d].startedAt", jobIndex, stepIndex), step.StartedAt)
		if err != nil {
			return err
		}
		completed, err := parseTime(fmt.Sprintf("jobs[%d].steps[%d].completedAt", jobIndex, stepIndex), step.CompletedAt)
		if err != nil {
			return err
		}
		duration := completed.Sub(started)
		if duration < 0 {
			if step.Conclusion != "skipped" || duration < -2*time.Second {
				return fmt.Errorf("jobs[%d].steps[%d] completedAt precedes startedAt", jobIndex, stepIndex)
			}
			duration = 0
		}
		if step.Conclusion == "skipped" {
			duration = 0
		}
		lowerName := strings.ToLower(step.Name)
		label := fmt.Sprintf("%s / %s", job.Name, step.Name)
		if strings.Contains(lowerName, "artifact") && (strings.Contains(lowerName, "upload") || strings.Contains(lowerName, "download")) {
			metrics.ArtifactTransferSeconds.Seconds += seconds(duration)
			metrics.ArtifactTransferSeconds.Steps = append(metrics.ArtifactTransferSeconds.Steps, label)
		}
		if strings.Contains(lowerName, "cache") {
			metrics.CacheTransferSeconds.Seconds += seconds(duration)
			metrics.CacheTransferSeconds.Steps = append(metrics.CacheTransferSeconds.Steps, label)
		}
	}
	return nil
}

func knownConclusion(value string) bool {
	switch value {
	case "success", "failure", "cancelled", "skipped", "timed_out", "action_required", "stale", "startup_failure", "neutral":
		return true
	default:
		return false
	}
}

func parseTime(name, value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be RFC3339: %w", name, err)
	}
	return parsed.UTC(), nil
}

func seconds(duration time.Duration) int64 {
	return int64(duration / time.Second)
}

// laterTerminal makes the critical-path terminal identity independent of the
// order returned by GitHub when multiple jobs have the same completion time.
func laterTerminal(candidateTime time.Time, candidateName string, currentTime time.Time, currentName string) bool {
	return currentTime.IsZero() || candidateTime.After(currentTime) || (candidateTime.Equal(currentTime) && candidateName < currentName)
}

// normalizedQueueSeconds tolerates only the bounded run-level timestamp skew
// observed in GitHub's run API. The observed created_at and started_at values
// remain distinct in the metrics document; only the derived queue duration is
// clamped.
// Job timestamps are deliberately not covered by this exception because jobs
// that predate an attempt can be reused results from a failed-only rerun.
func normalizedQueueSeconds(created, started time.Time) (int64, error) {
	if !started.Before(created) {
		return seconds(started.Sub(created)), nil
	}
	if created.Sub(started) > maxRunStartClockSkew {
		return 0, errors.New("run startedAt precedes createdAt by more than 2 seconds")
	}
	return 0, nil
}

func requireEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode trailing data: %w", err)
	}
	return errors.New("input contains multiple JSON values")
}
