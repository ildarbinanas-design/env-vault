package releasectl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

type operatorGitHub struct {
	run       workflowRun
	jobs      []workflowJob
	allJobs   []workflowJob
	artifacts []workflowArtifact
	gets      []string
	mutations []recordedMutation
}

type recordedMutation struct {
	method   string
	endpoint string
	body     any
}

func (g *operatorGitHub) Get(_ context.Context, endpoint string, _ map[string]string, target any) error {
	g.gets = append(g.gets, endpoint)
	switch {
	case endpoint == fmt.Sprintf("repos/%s/actions/runs/%d", testRepository, g.run.ID):
		*(target.(*workflowRun)) = g.run
	case strings.Contains(endpoint, "/attempts/") && strings.HasSuffix(endpoint, "/jobs"):
		response := target.(*jobsResponse)
		response.Jobs = append([]workflowJob(nil), g.jobs...)
		response.TotalCount = len(response.Jobs)
	case strings.HasSuffix(endpoint, "/jobs"):
		response := target.(*jobsResponse)
		values := g.allJobs
		if values == nil {
			values = g.jobs
		}
		response.Jobs = append([]workflowJob(nil), values...)
		response.TotalCount = len(response.Jobs)
	case strings.HasSuffix(endpoint, "/artifacts"):
		response := target.(*artifactsResponse)
		response.Artifacts = append([]workflowArtifact(nil), g.artifacts...)
		response.TotalCount = len(response.Artifacts)
	default:
		return fmt.Errorf("unexpected GET endpoint %s", endpoint)
	}
	return nil
}

func (g *operatorGitHub) Mutate(_ context.Context, method, endpoint string, body, _ any) error {
	g.mutations = append(g.mutations, recordedMutation{method: method, endpoint: endpoint, body: body})
	if method == "POST" && strings.HasSuffix(endpoint, "/rerun") {
		g.run.RunAttempt++
		g.run.Status = "queued"
		g.run.Conclusion = ""
	}
	return nil
}

func TestRepairPlanClassifiesIncompleteCurrentAttemptAndOnlyPlansFullRerun(t *testing.T) {
	run := completedRun(701, "push", testVersion, testSHA, "failure")
	run.Path = ".github/workflows/ci.yml"
	run.RunAttempt = 2
	github := &operatorGitHub{run: run, artifacts: completeAttemptArtifacts(run)}
	github.artifacts = github.artifacts[:len(github.artifacts)-2]
	plan, err := planReleaseRepair(context.Background(), testRepository, run.ID, mustTestReleaseContract(), github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Action.Code != "rerun_all_jobs" || plan.Action.ReasonCode != "ATTEMPT_MATRIX_INCOMPLETE" || plan.Action.Method != "POST" || !plan.Applyable {
		t.Fatalf("action=%+v applyable=%v", plan.Action, plan.Applyable)
	}
	if plan.Action.Endpoint != fmt.Sprintf("repos/%s/actions/runs/%d/rerun", testRepository, run.ID) || strings.Contains(plan.Action.Endpoint, "failed") {
		t.Fatalf("unsafe rerun endpoint=%q", plan.Action.Endpoint)
	}
	if plan.Matrix.RunID != run.ID || plan.Matrix.Attempt != 2 || len(plan.Matrix.MissingTargets) != 1 || plan.Matrix.MissingTargets[0] != "windows-amd64" || plan.Matrix.ReasonCode != "ATTEMPT_MATRIX_INCOMPLETE" {
		t.Fatalf("matrix=%+v", plan.Matrix)
	}
	if plan.PlanDigest == "" || plan.PlanDigest != digestRepairPlan(plan) || plan.Preconditions.MatrixDigest != canonicalDigest(plan.Matrix) {
		t.Fatalf("digest=%q preconditions=%+v", plan.PlanDigest, plan.Preconditions)
	}
}

func TestRepairPlanTreatsFiveEarlyReleaseArchivesWithoutPlatformEvidenceAsIncomplete(t *testing.T) {
	run := completedRun(709, "push", testVersion, testSHA, "failure")
	run.Path = ".github/workflows/ci.yml"
	all := completeAttemptArtifacts(run)
	archives := make([]workflowArtifact, 0, 5)
	for _, artifact := range all {
		if strings.HasPrefix(artifact.Name, "env-vault-release-") {
			archives = append(archives, artifact)
		}
	}
	plan, err := planReleaseRepair(context.Background(), testRepository, run.ID, mustTestReleaseContract(), &operatorGitHub{run: run, artifacts: archives}, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Matrix.Complete || len(plan.Matrix.MissingTargets) != 5 || len(plan.Matrix.Missing) != 5 || plan.Action.Code != "rerun_all_jobs" || plan.Action.ReasonCode != "ATTEMPT_MATRIX_INCOMPLETE" {
		t.Fatalf("five early archives incorrectly satisfied matrix: %+v", plan)
	}
}

func TestRepairPlanRerunsCompletedFailedAttemptEvenWhenArtifactMatrixIsComplete(t *testing.T) {
	run := completedRun(710, "push", testVersion, testSHA, "failure")
	run.Path = ".github/workflows/ci.yml"
	plan, err := planReleaseRepair(context.Background(), testRepository, run.ID, mustTestReleaseContract(), &operatorGitHub{run: run, artifacts: completeAttemptArtifacts(run)}, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Matrix.Complete || !plan.Applyable || plan.Action.Code != "rerun_all_jobs" || plan.Action.ReasonCode != "CI_ATTEMPT_FAILED" || strings.Contains(plan.Action.Endpoint, "rerun-failed-jobs") {
		t.Fatalf("completed failed attempt did not produce canonical full rerun: %+v", plan)
	}
}

func TestRepairPlanWaitsForRunningAttemptInsteadOfRerunningIt(t *testing.T) {
	run := completedRun(702, "push", testVersion, testSHA, "")
	run.Path = ".github/workflows/ci.yml"
	run.Status = "in_progress"
	github := &operatorGitHub{run: run, artifacts: completeAttemptArtifacts(run)[:2]}
	plan, err := planReleaseRepair(context.Background(), testRepository, run.ID, mustTestReleaseContract(), github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Action.Code != "wait_publisher" || plan.Applyable {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestRepairApplyIsDryRunByDefaultThenUsesFullRerun(t *testing.T) {
	run := completedRun(703, "push", testVersion, testSHA, "failure")
	run.Path = ".github/workflows/ci.yml"
	run.RunAttempt = 2
	github := &operatorGitHub{run: run, artifacts: completeAttemptArtifacts(run)[:4]}
	contract := mustTestReleaseContract()
	plan, err := planReleaseRepair(context.Background(), testRepository, run.ID, contract, github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	dry, code := applyReleaseRepair(context.Background(), plan, plan.PlanDigest, false, contract, github, github, fixedClock())
	if code != exitSuccess || !dry.OK || dry.Status != "dry_run" || len(github.mutations) != 0 {
		t.Fatalf("dry=%+v code=%d mutations=%+v", dry, code, github.mutations)
	}
	applied, code := applyReleaseRepair(context.Background(), plan, plan.PlanDigest, true, contract, github, github, fixedClock())
	if code != exitSuccess || !applied.OK || applied.Status != "applied" || len(github.mutations) != 1 {
		t.Fatalf("applied=%+v code=%d mutations=%+v", applied, code, github.mutations)
	}
	mutation := github.mutations[0]
	if mutation.method != "POST" || mutation.endpoint != plan.Action.Endpoint || mutation.body != nil || strings.Contains(mutation.endpoint, "rerun-failed-jobs") {
		t.Fatalf("mutation=%+v", mutation)
	}
	repeated, code := applyReleaseRepair(context.Background(), plan, plan.PlanDigest, true, contract, github, github, fixedClock())
	if code != exitSuccess || !repeated.OK || repeated.Status != "already_applied" || len(github.mutations) != 1 {
		t.Fatalf("repeat=%+v code=%d mutations=%+v", repeated, code, github.mutations)
	}
}

func TestRepairApplyRejectsChangedAttemptMatrixAndDigest(t *testing.T) {
	run := completedRun(704, "push", testVersion, testSHA, "failure")
	run.Path = ".github/workflows/ci.yml"
	github := &operatorGitHub{run: run, artifacts: completeAttemptArtifacts(run)[:4]}
	contract := mustTestReleaseContract()
	plan, err := planReleaseRepair(context.Background(), testRepository, run.ID, contract, github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	github.artifacts = completeAttemptArtifacts(run)[:3]
	doc, code := applyReleaseRepair(context.Background(), plan, plan.PlanDigest, true, contract, github, github, fixedClock())
	if code != exitPreconditionFailed || doc.Error == nil || doc.Error.Code != "REMOTE_PRECONDITION_FAILED" || len(github.mutations) != 0 {
		t.Fatalf("doc=%+v code=%d", doc, code)
	}
	doc, code = applyReleaseRepair(context.Background(), plan, strings.Repeat("0", 64), true, contract, github, github, fixedClock())
	if code != exitPreconditionFailed || doc.Error == nil || doc.Error.Code != "PLAN_DIGEST_MISMATCH" {
		t.Fatalf("doc=%+v code=%d", doc, code)
	}
}

func TestMetricsAreAttemptAwareAndComputeRunnerTiming(t *testing.T) {
	base := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	run := completedRun(705, "push", testVersion, testSHA, "success")
	run.RunAttempt = 3
	run.CreatedAt = base
	run.RunStartedAt = base.Add(10 * time.Second)
	run.UpdatedAt = base.Add(80 * time.Second)
	jobs := []workflowJob{
		{
			ID: 1, RunAttempt: 3, Name: "build", Status: "completed", Conclusion: "success", HTMLURL: "https://example/jobs/1",
			StartedAt: base.Add(12 * time.Second), CompletedAt: base.Add(42 * time.Second),
			Steps: []workflowStep{
				{Number: 1, Name: "Run actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16", Status: "completed", Conclusion: "success", StartedAt: base.Add(12 * time.Second), CompletedAt: base.Add(17 * time.Second)},
				{Number: 2, Name: "Upload native release artifact", Status: "completed", Conclusion: "success", StartedAt: base.Add(37 * time.Second), CompletedAt: base.Add(42 * time.Second)},
			},
		},
		{ID: 2, RunAttempt: 3, Name: "gate", Status: "completed", Conclusion: "success", HTMLURL: "https://example/jobs/2", StartedAt: base.Add(40 * time.Second), CompletedAt: base.Add(70 * time.Second)},
	}
	allJobs := []workflowJob{
		{ID: 10, RunAttempt: 1, Name: "build", Status: "completed", Conclusion: "failure", HTMLURL: "https://example/jobs/10", StartedAt: base.Add(-2 * time.Minute), CompletedAt: base.Add(-90 * time.Second), Steps: []workflowStep{{Number: 1, Name: "Upload platform quality evidence", Status: "completed", Conclusion: "success", StartedAt: base.Add(-100 * time.Second), CompletedAt: base.Add(-97 * time.Second)}}},
		{ID: 20, RunAttempt: 2, Name: "build", Status: "completed", Conclusion: "failure", HTMLURL: "https://example/jobs/20", StartedAt: base.Add(-60 * time.Second), CompletedAt: base.Add(-30 * time.Second), Steps: []workflowStep{{Number: 1, Name: "Download five exact native release artifacts from CI", Status: "completed", Conclusion: "success", StartedAt: base.Add(-50 * time.Second), CompletedAt: base.Add(-46 * time.Second)}}},
	}
	allJobs = append(allJobs, jobs...)
	github := &operatorGitHub{run: run, jobs: jobs, allJobs: allJobs}
	doc, err := collectReleaseMetrics(context.Background(), testRepository, run.ID, mustTestReleaseContract(), github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	want := &releaseMetrics{
		QueueSeconds: 10, WallSeconds: 70, JobCount: 2, ExecutedJobCount: 4, AttemptRunnerSeconds: 60, AggregateRunnerSeconds: 120, Retries: 2,
		CriticalPathSeconds: 60, CacheStepSeconds: 5, ArtifactTransferSeconds: 12,
		TimingComplete: true, UnavailableReasonCodes: []string{},
		Attempts: []attemptTiming{{Attempt: 1, JobCount: 1, RunnerSeconds: 30}, {Attempt: 2, JobCount: 1, RunnerSeconds: 30}, {Attempt: 3, JobCount: 2, RunnerSeconds: 60}},
		Jobs: []jobTiming{
			{ID: 10, Attempt: 1, Name: "build", DurationSeconds: 30},
			{ID: 20, Attempt: 2, Name: "build", DurationSeconds: 30},
			{ID: 1, Attempt: 3, Name: "build", DurationSeconds: 30},
			{ID: 2, Attempt: 3, Name: "gate", DurationSeconds: 30},
		},
	}
	if !reflect.DeepEqual(doc.Metrics, want) {
		t.Fatalf("metrics=%+v want=%+v", doc.Metrics, want)
	}
	wantJobsEndpoint := fmt.Sprintf("repos/%s/actions/runs/%d/attempts/3/jobs", testRepository, run.ID)
	if !containsString(github.gets, wantJobsEndpoint) {
		t.Fatalf("GET endpoints=%v", github.gets)
	}
	allJobsEndpoint := fmt.Sprintf("repos/%s/actions/runs/%d/jobs", testRepository, run.ID)
	if !containsString(github.gets, allJobsEndpoint) {
		t.Fatalf("GET endpoints=%v", github.gets)
	}
}

func TestMetricsFailClosedWhenClassifiedStepTimestampIsMissing(t *testing.T) {
	base := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	run := completedRun(711, "push", testVersion, testSHA, "success")
	run.CreatedAt, run.RunStartedAt = base, base.Add(time.Second)
	job := workflowJob{
		ID: 1, RunAttempt: 1, Name: "build", Status: "completed", Conclusion: "success", HTMLURL: "https://example/jobs/1",
		StartedAt: base.Add(time.Second), CompletedAt: base.Add(10 * time.Second),
		Steps: []workflowStep{{Number: 1, Name: "Upload exact promotion manifest", Status: "completed", Conclusion: "success"}},
	}
	doc, err := collectReleaseMetrics(context.Background(), testRepository, run.ID, mustTestReleaseContract(), &operatorGitHub{run: run, jobs: []workflowJob{job}}, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	if doc.Metrics == nil || doc.Metrics.TimingComplete || !containsString(doc.Metrics.UnavailableReasonCodes, "artifact_step_timestamps_unavailable") {
		t.Fatalf("missing artifact timestamp was silently accepted: %+v", doc.Metrics)
	}
}

func TestRepairPlanCLIEmitsOneVersionedJSONDocument(t *testing.T) {
	runValue := completedRun(706, "push", testVersion, testSHA, "failure")
	runValue.Path = ".github/workflows/ci.yml"
	github := &operatorGitHub{run: runValue, artifacts: completeAttemptArtifacts(runValue)[:4]}
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"release", "repair", "plan", "--repo", testRepository, "--run-id", "706",
		"--contract", "../../release/contract.v1.json", "--json",
	}, &stdout, &stderr, dependencies{github: github, clock: fixedClock()})
	if code != exitSuccess || stderr.Len() != 0 || strings.Count(stdout.String(), "\n") != 1 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var plan releaseRepairPlan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Schema != "env-vault.release-repair-plan.v1" || plan.Action.Code != "rerun_all_jobs" {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestReadCommandsUseTheirOwnSchemaOnContractFailure(t *testing.T) {
	for _, test := range []struct {
		command string
		schema  string
	}{
		{command: "plan", schema: releasePlanSchema},
		{command: "verify", schema: releaseVerifySchema},
	} {
		t.Run(test.command, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run([]string{
				"release", test.command, "--version", testVersion, "--repo", testRepository,
				"--contract", t.TempDir() + "/missing.json", "--json",
			}, &stdout, &stderr, dependencies{clock: fixedClock()})
			if code != exitUsage || stderr.Len() != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			var document struct {
				Schema string     `json:"schema"`
				Error  *errorInfo `json:"error"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
				t.Fatal(err)
			}
			if document.Schema != test.schema || document.Error == nil || document.Error.Code != "CONTRACT_INVALID" {
				t.Fatalf("document=%+v", document)
			}
		})
	}
}

func TestRepairApplyRejectsRecomputedPlanWithAlteredEndpoint(t *testing.T) {
	run := completedRun(707, "push", "main", testSHA, "failure")
	run.Path = ".github/workflows/ci.yml"
	github := &operatorGitHub{run: run, artifacts: completeAttemptArtifacts(run)[:4]}
	contract := mustTestReleaseContract()
	plan, err := planReleaseRepair(context.Background(), testRepository, run.ID, contract, github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	plan.Action.Endpoint = "repos/" + testRepository + "/actions/runs/999/rerun"
	plan.PlanDigest = digestRepairPlan(plan)
	doc, code := applyReleaseRepair(context.Background(), plan, plan.PlanDigest, true, contract, github, github, fixedClock())
	if code != exitPreconditionFailed || doc.Error == nil || doc.Error.Code != "MALFORMED_RESPONSE" || len(github.mutations) != 0 {
		t.Fatalf("altered endpoint accepted: code=%d doc=%+v mutations=%+v", code, doc, github.mutations)
	}
}

func TestRepairPlanDoesNotRerunPublisherWorkflow(t *testing.T) {
	run := completedRun(708, "push", testVersion, testSHA, "failure")
	run.Path = ".github/workflows/build-binaries.yml"
	github := &operatorGitHub{run: run, artifacts: completeAttemptArtifacts(run)[:1]}
	plan, err := planReleaseRepair(context.Background(), testRepository, run.ID, mustTestReleaseContract(), github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Applyable || plan.Action.Code != "none" || plan.Action.ReasonCode != "workflow_not_repairable" {
		t.Fatalf("publisher run became repairable: %+v", plan)
	}
}
