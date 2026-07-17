package releasectl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/githubapi"
	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
	"github.com/ildarbinanas-design/env-vault/internal/releasemetrics"
)

const (
	releasePlanSchema        = "env-vault.release-plan.v1"
	releaseVerifySchema      = "env-vault.release-verify.v1"
	releaseMetricsSchema     = "env-vault.release-metrics.v1"
	releaseRepairApplySchema = "env-vault.release-repair-apply.v1"
	maxPlanBytes             = 1 << 20
	repairKindCIAttempt      = "ci_attempt"
)

type releasePlanDocument struct {
	Schema      string     `json:"schema"`
	OK          bool       `json:"ok"`
	GeneratedAt time.Time  `json:"generated_at"`
	PlanDigest  string     `json:"plan_digest"`
	Identity    query      `json:"identity"`
	Overall     *overall   `json:"overall,omitempty"`
	Stages      *stages    `json:"stages,omitempty"`
	Action      nextAction `json:"action"`
	ReasonCode  string     `json:"reason_code"`
	ReadOnly    bool       `json:"read_only"`
	Error       *errorInfo `json:"error,omitempty"`
}

type releaseVerificationDocument struct {
	Schema      string     `json:"schema"`
	OK          bool       `json:"ok"`
	ObservedAt  time.Time  `json:"observed_at"`
	Outcome     string     `json:"outcome"`
	ReasonCode  string     `json:"reason_code"`
	Identity    query      `json:"identity"`
	Status      *document  `json:"status,omitempty"`
	Error       *errorInfo `json:"error,omitempty"`
	EvidenceAPI string     `json:"evidence_api"`
}

type releaseMetricsDocument struct {
	Schema     string          `json:"schema"`
	OK         bool            `json:"ok"`
	ObservedAt time.Time       `json:"observed_at"`
	Repository string          `json:"repository"`
	Run        *runEvidence    `json:"run,omitempty"`
	Metrics    *releaseMetrics `json:"metrics,omitempty"`
	Error      *errorInfo      `json:"error,omitempty"`
}

type releaseMetrics struct {
	QueueSeconds            int64           `json:"queue_seconds"`
	WallSeconds             int64           `json:"wall_seconds"`
	JobCount                int             `json:"job_count"`
	ExecutedJobCount        int             `json:"executed_job_count"`
	AttemptRunnerSeconds    int64           `json:"attempt_runner_seconds"`
	AggregateRunnerSeconds  int64           `json:"aggregate_runner_seconds"`
	Retries                 int             `json:"retries"`
	CriticalPathSeconds     int64           `json:"critical_path_seconds"`
	CacheStepSeconds        int64           `json:"cache_step_seconds"`
	ArtifactTransferSeconds int64           `json:"artifact_transfer_seconds"`
	TimingComplete          bool            `json:"timing_complete"`
	UnavailableReasonCodes  []string        `json:"unavailable_reason_codes"`
	Attempts                []attemptTiming `json:"attempts"`
	Jobs                    []jobTiming     `json:"jobs"`
}

type attemptTiming struct {
	Attempt       int   `json:"attempt"`
	JobCount      int   `json:"job_count"`
	RunnerSeconds int64 `json:"runner_seconds"`
}

type jobTiming struct {
	ID              int64  `json:"id"`
	Attempt         int    `json:"attempt"`
	Name            string `json:"name"`
	DurationSeconds int64  `json:"duration_seconds"`
}

type artifactsResponse struct {
	TotalCount int                `json:"total_count"`
	Artifacts  []workflowArtifact `json:"artifacts"`
}

type workflowArtifact struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Expired     bool   `json:"expired"`
	WorkflowRun struct {
		ID      int64  `json:"id"`
		HeadSHA string `json:"head_sha"`
	} `json:"workflow_run"`
}

type attemptMatrix struct {
	RunID           int64    `json:"run_id"`
	Attempt         int      `json:"attempt"`
	ExpectedTargets []string `json:"expected_targets"`
	Expected        []string `json:"expected_artifacts"`
	Observed        []string `json:"observed_artifacts"`
	MissingTargets  []string `json:"missing_targets"`
	Missing         []string `json:"missing_artifacts"`
	Unexpected      []string `json:"unexpected_artifacts"`
	Duplicates      []string `json:"duplicate_artifacts"`
	Complete        bool     `json:"complete"`
	ReasonCode      string   `json:"reason_code"`
}

type repairPreconditions struct {
	RunID         int64    `json:"run_id"`
	Attempt       int      `json:"attempt"`
	HeadSHA       string   `json:"head_sha"`
	Event         string   `json:"event"`
	Status        string   `json:"status"`
	Conclusion    string   `json:"conclusion"`
	ArtifactNames []string `json:"artifact_names"`
	MatrixDigest  string   `json:"matrix_digest"`
}

type repairAction struct {
	Code       string `json:"code"`
	ReasonCode string `json:"reason_code"`
	Method     string `json:"method,omitempty"`
	Endpoint   string `json:"endpoint,omitempty"`
}

type releaseRepairPlan struct {
	Schema        string              `json:"schema"`
	Kind          string              `json:"kind"`
	OK            bool                `json:"ok"`
	GeneratedAt   time.Time           `json:"generated_at"`
	PlanDigest    string              `json:"plan_digest"`
	Repository    string              `json:"repository"`
	Run           runEvidence         `json:"run"`
	Preconditions repairPreconditions `json:"preconditions"`
	Matrix        attemptMatrix       `json:"matrix"`
	Action        repairAction        `json:"action"`
	Applyable     bool                `json:"applyable"`
}

type releaseRepairApplyDocument struct {
	Schema      string       `json:"schema"`
	OK          bool         `json:"ok"`
	ObservedAt  time.Time    `json:"observed_at"`
	PlanDigest  string       `json:"plan_digest"`
	DryRun      bool         `json:"dry_run"`
	Status      string       `json:"status"`
	Action      repairAction `json:"action"`
	RunID       int64        `json:"run_id"`
	FromAttempt int          `json:"from_attempt"`
	ToAttempt   int          `json:"to_attempt,omitempty"`
	Error       *errorInfo   `json:"error,omitempty"`
}

func buildReleasePlan(ctx context.Context, request query, contract releasecontract.Contract, github githubGetter, clk clock) releasePlanDocument {
	status, err := (collector{github: github, clock: clk, contract: contract}).snapshot(ctx, request)
	plan := releasePlanDocument{
		Schema: contract.Schemas["release_plan"], OK: err == nil, GeneratedAt: clk.Now().UTC(),
		Identity: request, ReadOnly: true, Action: nextAction{Code: "resolve_inconsistency"},
		ReasonCode: "REMOTE_STATE_UNKNOWN",
	}
	if err != nil {
		errorDoc := errorDocument(clk.Now(), request, err)
		plan.Error = errorDoc.Error
	} else {
		plan.Overall, plan.Stages = status.Overall, status.Stages
		if status.NextAction != nil {
			plan.Action = *status.NextAction
		}
		if status.Overall != nil {
			plan.ReasonCode = status.Overall.Reason
		}
	}
	plan.PlanDigest = digestJSON(plan, func(value *releasePlanDocument) { value.PlanDigest = "" })
	return plan
}

func verifyRelease(ctx context.Context, request query, contract releasecontract.Contract, github githubGetter, clk clock) releaseVerificationDocument {
	status, err := (collector{github: github, clock: clk, contract: contract}).snapshot(ctx, request)
	result := releaseVerificationDocument{
		Schema: contract.Schemas["release_verify"], ObservedAt: clk.Now().UTC(), Identity: request,
		Outcome: "unknown", ReasonCode: "REMOTE_STATE_UNKNOWN", EvidenceAPI: "github_rest_v" + githubapi.Version,
	}
	if err != nil {
		errorDoc := errorDocument(clk.Now(), request, err)
		result.Error = errorDoc.Error
		return result
	}
	result.Status = &status
	if status.Overall == nil {
		return result
	}
	result.ReasonCode = status.Overall.Reason
	switch status.Overall.State {
	case "succeeded":
		verifier, ok := github.(cryptographicAttestationVerifier)
		if !ok {
			result.ReasonCode = "attestation_crypto_verifier_unavailable"
			result.Error = &errorInfo{Code: "DEPENDENCY_MISSING", Operation: "artifact_attestation_crypto"}
			return result
		}
		assets, sourceSHA, verifyErr := exactAttestationVerificationInputs(status, contract)
		if verifyErr == nil {
			verifyErr = verifier.VerifyArtifactAttestations(
				ctx, request.Repository, sourceSHA,
				request.Repository+"/.github/workflows/"+workflowFile(contract, "publisher"), assets,
			)
		}
		if verifyErr != nil {
			var invalid *attestationVerificationFailure
			if errors.As(verifyErr, &invalid) {
				result.Outcome = "fail"
				result.ReasonCode = "attestation_crypto_verification_failed"
				result.Error = &errorInfo{Code: "ATTESTATION_VERIFICATION_FAILED", Operation: "artifact_attestation_crypto"}
				return result
			}
			result.ReasonCode = "attestation_crypto_verification_unknown"
			result.Error = operatorErrorInfo(verifyErr)
			return result
		}
		result.OK, result.Outcome = true, "pass"
	case "failed", "inconsistent":
		result.Outcome = "fail"
	default:
		result.Outcome = "unknown"
	}
	return result
}

func exactAttestationVerificationInputs(status document, contract releasecontract.Contract) ([]releaseAssetEvidence, string, error) {
	if status.Identity == nil || !shaPattern.MatchString(status.Identity.SourceSHA) || status.Stages == nil ||
		status.Stages.GitHubRelease.Release == nil || !status.Stages.GitHubRelease.Release.Assets.DigestComplete {
		return nil, "", malformed("artifact_attestation_crypto", "exact source or release asset evidence is missing")
	}
	byName := make(map[string]releaseAssetEvidence, len(status.Stages.GitHubRelease.Release.Assets.Digests))
	for _, asset := range status.Stages.GitHubRelease.Release.Assets.Digests {
		if _, exists := byName[asset.Name]; exists {
			return nil, "", malformed("artifact_attestation_crypto", "release asset evidence is duplicated")
		}
		byName[asset.Name] = asset
	}
	assets := make([]releaseAssetEvidence, 0, len(contract.Platforms))
	for _, platform := range contract.Platforms {
		asset, ok := byName[platform.Archive]
		if !ok || asset.ID <= 0 || asset.Size <= 0 || !digestPattern.MatchString(asset.SHA256) {
			return nil, "", malformed("artifact_attestation_crypto", "archive evidence is incomplete")
		}
		assets = append(assets, asset)
	}
	return assets, status.Identity.SourceSHA, nil
}

func collectReleaseMetrics(ctx context.Context, repository string, runID int64, contract releasecontract.Contract, github githubGetter, clk clock) (releaseMetricsDocument, error) {
	doc := releaseMetricsDocument{Schema: contract.Schemas["release_metrics"], OK: true, ObservedAt: clk.Now().UTC(), Repository: repository}
	run, err := getWorkflowRun(ctx, github, repository, runID)
	if err != nil {
		return doc, err
	}
	jobs, err := getAttemptJobs(ctx, github, repository, run.ID, run.RunAttempt)
	if err != nil {
		return doc, err
	}
	allJobs, err := getAllAttemptJobs(ctx, github, repository, run.ID, run.RunAttempt)
	if err != nil {
		return doc, err
	}
	if err := requireExactLatestAttempt(jobs, allJobs, run.RunAttempt); err != nil {
		return doc, err
	}
	doc.Run = evidenceFromRun(run)
	metrics := releaseMetrics{
		JobCount: len(jobs), ExecutedJobCount: len(allJobs), Retries: run.RunAttempt - 1, TimingComplete: true,
		UnavailableReasonCodes: []string{}, Attempts: make([]attemptTiming, run.RunAttempt), Jobs: []jobTiming{},
	}
	for index := range metrics.Attempts {
		metrics.Attempts[index].Attempt = index + 1
	}
	if run.CreatedAt.IsZero() || run.RunStartedAt.IsZero() || run.RunStartedAt.Before(run.CreatedAt) {
		metrics.TimingComplete = false
		metrics.UnavailableReasonCodes = append(metrics.UnavailableReasonCodes, "run_queue_timestamps_unavailable")
	} else {
		metrics.QueueSeconds = seconds(run.RunStartedAt.Sub(run.CreatedAt))
	}
	completion := time.Time{}
	for _, job := range allJobs {
		metrics.Attempts[job.RunAttempt-1].JobCount++
		if job.StartedAt.IsZero() || job.CompletedAt.IsZero() || job.CompletedAt.Before(job.StartedAt) {
			metrics.TimingComplete = false
			metrics.UnavailableReasonCodes = append(metrics.UnavailableReasonCodes, "job_timestamps_unavailable")
			continue
		}
		duration := seconds(job.CompletedAt.Sub(job.StartedAt))
		metrics.AggregateRunnerSeconds += duration
		metrics.Attempts[job.RunAttempt-1].RunnerSeconds += duration
		metrics.Jobs = append(metrics.Jobs, jobTiming{ID: job.ID, Attempt: job.RunAttempt, Name: job.Name, DurationSeconds: duration})
		if job.RunAttempt == run.RunAttempt && job.CompletedAt.After(completion) {
			completion = job.CompletedAt
		}
		for _, step := range job.Steps {
			kind := releasemetrics.ClassifyStep(step.Name)
			if kind == releasemetrics.StepOther {
				continue
			}
			if step.StartedAt.IsZero() || step.CompletedAt.IsZero() || step.CompletedAt.Before(step.StartedAt) {
				metrics.TimingComplete = false
				if kind == releasemetrics.StepCache {
					metrics.UnavailableReasonCodes = append(metrics.UnavailableReasonCodes, "cache_step_timestamps_unavailable")
				} else {
					metrics.UnavailableReasonCodes = append(metrics.UnavailableReasonCodes, "artifact_step_timestamps_unavailable")
				}
				continue
			}
			stepSeconds := seconds(step.CompletedAt.Sub(step.StartedAt))
			if kind == releasemetrics.StepCache {
				metrics.CacheStepSeconds += stepSeconds
			} else {
				metrics.ArtifactTransferSeconds += stepSeconds
			}
		}
	}
	metrics.AttemptRunnerSeconds = metrics.Attempts[run.RunAttempt-1].RunnerSeconds
	if completion.IsZero() || run.CreatedAt.IsZero() || run.RunStartedAt.IsZero() {
		metrics.TimingComplete = false
		metrics.UnavailableReasonCodes = append(metrics.UnavailableReasonCodes, "workflow_completion_timestamp_unavailable")
	} else {
		metrics.WallSeconds = seconds(completion.Sub(run.CreatedAt))
		metrics.CriticalPathSeconds = seconds(completion.Sub(run.RunStartedAt))
	}
	metrics.UnavailableReasonCodes = uniqueSorted(metrics.UnavailableReasonCodes)
	sort.Slice(metrics.Jobs, func(i, j int) bool {
		if metrics.Jobs[i].Attempt != metrics.Jobs[j].Attempt {
			return metrics.Jobs[i].Attempt < metrics.Jobs[j].Attempt
		}
		return metrics.Jobs[i].ID < metrics.Jobs[j].ID
	})
	doc.Metrics = &metrics
	return doc, nil
}

func planReleaseRepair(ctx context.Context, repository string, runID int64, contract releasecontract.Contract, github githubGetter, clk clock) (releaseRepairPlan, error) {
	run, err := getWorkflowRun(ctx, github, repository, runID)
	if err != nil {
		return releaseRepairPlan{}, err
	}
	artifacts, err := getWorkflowArtifacts(ctx, github, repository, runID)
	if err != nil {
		return releaseRepairPlan{}, err
	}
	matrix := classifyAttemptMatrix(run, artifacts, contract)
	action, applyable := classifyRepairAction(repository, run, matrix, contract)
	artifactNames := append([]string(nil), matrix.Observed...)
	plan := releaseRepairPlan{
		Schema: contract.Schemas["repair_plan"], Kind: repairKindCIAttempt, OK: true, GeneratedAt: clk.Now().UTC(), Repository: repository,
		Run: *evidenceFromRun(run), Matrix: matrix, Action: action, Applyable: applyable,
		Preconditions: repairPreconditions{
			RunID: run.ID, Attempt: run.RunAttempt, HeadSHA: run.HeadSHA, Event: run.Event,
			Status: run.Status, Conclusion: run.Conclusion, ArtifactNames: artifactNames, MatrixDigest: canonicalDigest(matrix),
		},
	}
	if plan.Schema == "" {
		plan.Schema = "env-vault.release-repair-plan.v1"
	}
	plan.PlanDigest = digestRepairPlan(plan)
	return plan, nil
}

func applyReleaseRepair(ctx context.Context, plan releaseRepairPlan, suppliedDigest string, apply bool, contract releasecontract.Contract, github githubGetter, mutator githubMutator, clk clock) (releaseRepairApplyDocument, int) {
	result := releaseRepairApplyDocument{
		Schema: contract.Schemas["release_repair_apply"], ObservedAt: clk.Now().UTC(), PlanDigest: suppliedDigest,
		DryRun: !apply, Status: "blocked", Action: plan.Action, RunID: plan.Preconditions.RunID,
		FromAttempt: plan.Preconditions.Attempt,
	}
	if err := validateRepairPlan(plan, suppliedDigest, contract); err != nil {
		result.Error = operatorErrorInfo(err)
		return result, exitPreconditionFailed
	}
	run, err := getWorkflowRun(ctx, github, plan.Repository, plan.Preconditions.RunID)
	if err != nil {
		result.Error = operatorErrorInfo(err)
		return result, exitObservationError
	}
	if run.HeadSHA != plan.Preconditions.HeadSHA || run.Event != plan.Preconditions.Event {
		result.Error = &errorInfo{Code: "REMOTE_PRECONDITION_FAILED", Operation: "workflow_run_identity"}
		return result, exitPreconditionFailed
	}
	if run.RunAttempt > plan.Preconditions.Attempt {
		artifacts, artifactErr := getWorkflowArtifacts(ctx, github, plan.Repository, run.ID)
		if artifactErr != nil {
			result.Error = operatorErrorInfo(artifactErr)
			return result, exitObservationError
		}
		matrix := classifyAttemptMatrix(run, artifacts, contract)
		if run.Status != "completed" || matrix.Complete {
			result.OK, result.Status, result.ToAttempt = true, "already_applied", run.RunAttempt
			return result, exitSuccess
		}
		result.Error = &errorInfo{Code: "REMOTE_PRECONDITION_FAILED", Operation: "newer_attempt_incomplete"}
		return result, exitPreconditionFailed
	}
	if run.RunAttempt == plan.Preconditions.Attempt && run.Status != plan.Preconditions.Status && run.Status != "completed" {
		result.OK, result.Status, result.ToAttempt = true, "already_applied", run.RunAttempt
		return result, exitSuccess
	}
	if run.RunAttempt != plan.Preconditions.Attempt || run.Status != plan.Preconditions.Status || run.Conclusion != plan.Preconditions.Conclusion {
		result.Error = &errorInfo{Code: "REMOTE_PRECONDITION_FAILED", Operation: "workflow_run_state"}
		return result, exitPreconditionFailed
	}
	artifacts, err := getWorkflowArtifacts(ctx, github, plan.Repository, run.ID)
	if err != nil {
		result.Error = operatorErrorInfo(err)
		return result, exitObservationError
	}
	matrix := classifyAttemptMatrix(run, artifacts, contract)
	if !equalStrings(matrix.Observed, plan.Preconditions.ArtifactNames) || canonicalDigest(matrix) != plan.Preconditions.MatrixDigest {
		result.Error = &errorInfo{Code: "REMOTE_PRECONDITION_FAILED", Operation: "attempt_artifact_matrix"}
		return result, exitPreconditionFailed
	}
	if plan.Action.Code == "none" {
		result.OK, result.Status = true, "already_satisfied"
		return result, exitSuccess
	}
	if plan.Action.Code != "rerun_all_jobs" || !plan.Applyable || plan.Action.Method != "POST" || plan.Action.Endpoint == "" {
		result.Error = &errorInfo{Code: "REMOTE_STATE_UNKNOWN", Operation: "repair_action_not_supported"}
		return result, exitMutationBlocked
	}
	if !apply {
		result.OK, result.Status = true, "dry_run"
		return result, exitSuccess
	}
	if mutator == nil {
		result.Error = &errorInfo{Code: "DEPENDENCY_MISSING", Operation: "github_mutation_capability"}
		return result, exitObservationError
	}
	if err := mutator.Mutate(ctx, "POST", plan.Action.Endpoint, nil, nil); err != nil {
		result.Error = operatorErrorInfo(err)
		return result, exitObservationError
	}
	for poll := 0; poll < 5; poll++ {
		observed, observationErr := getWorkflowRun(ctx, github, plan.Repository, plan.Preconditions.RunID)
		if observationErr != nil {
			result.Error = operatorErrorInfo(observationErr)
			return result, exitObservationError
		}
		if observed.HeadSHA != plan.Preconditions.HeadSHA || observed.Event != plan.Preconditions.Event || !workflowPathMatches(observed.Path, workflowFile(contract, "ci")) {
			result.Error = &errorInfo{Code: "REMOTE_PRECONDITION_FAILED", Operation: "workflow_run_identity"}
			return result, exitPreconditionFailed
		}
		if observed.RunAttempt > plan.Preconditions.Attempt || observed.Status != "completed" {
			result.OK, result.Status, result.ToAttempt = true, "applied", observed.RunAttempt
			return result, exitSuccess
		}
		if poll < 4 {
			if sleepErr := clk.Sleep(ctx, time.Second); sleepErr != nil {
				result.Error = operatorErrorInfo(sleepErr)
				return result, exitObservationError
			}
		}
	}
	result.Error = &errorInfo{Code: "REMOTE_STATE_UNKNOWN", Operation: "rerun_transition_not_observed", Retryable: true}
	return result, exitObservationError
}

func validateRepairPlan(plan releaseRepairPlan, suppliedDigest string, contract releasecontract.Contract) error {
	if suppliedDigest == "" || len(suppliedDigest) != sha256.Size*2 {
		return &observationError{code: "PLAN_DIGEST_MISMATCH", operation: "repair_plan_digest"}
	}
	if _, err := hex.DecodeString(suppliedDigest); err != nil || plan.PlanDigest != suppliedDigest || digestRepairPlan(plan) != suppliedDigest {
		return &observationError{code: "PLAN_DIGEST_MISMATCH", operation: "repair_plan_digest", cause: err}
	}
	wantSchema := contract.Schemas["repair_plan"]
	if wantSchema == "" {
		wantSchema = "env-vault.release-repair-plan.v1"
	}
	if plan.Schema != wantSchema || plan.Kind != repairKindCIAttempt || !plan.OK || !repositoryPattern.MatchString(plan.Repository) || plan.Preconditions.RunID <= 0 || plan.Preconditions.Attempt <= 0 || !shaPattern.MatchString(plan.Preconditions.HeadSHA) || len(plan.Preconditions.MatrixDigest) != sha256.Size*2 {
		return malformed("repair_plan", "plan fields are malformed")
	}
	if plan.Run.ID != plan.Preconditions.RunID || plan.Run.Attempt != plan.Preconditions.Attempt || plan.Run.HeadSHA != plan.Preconditions.HeadSHA {
		return malformed("repair_plan", "run identity does not match preconditions")
	}
	if plan.Run.Event != plan.Preconditions.Event || plan.Run.Status != plan.Preconditions.Status || plan.Run.Conclusion != plan.Preconditions.Conclusion || !workflowPathMatches(plan.Run.WorkflowPath, workflowFile(contract, "ci")) {
		return malformed("repair_plan", "run state or workflow identity does not match preconditions")
	}
	if plan.Matrix.RunID != plan.Preconditions.RunID || plan.Matrix.Attempt != plan.Preconditions.Attempt || canonicalDigest(plan.Matrix) != plan.Preconditions.MatrixDigest || !equalStrings(plan.Matrix.Observed, plan.Preconditions.ArtifactNames) {
		return malformed("repair_plan", "attempt matrix does not match preconditions")
	}
	run := workflowRun{
		ID: plan.Preconditions.RunID, RunAttempt: plan.Preconditions.Attempt, Event: plan.Preconditions.Event,
		Status: plan.Preconditions.Status, Conclusion: plan.Preconditions.Conclusion, HeadSHA: plan.Preconditions.HeadSHA,
		Path: plan.Run.WorkflowPath,
	}
	wantAction, wantApplyable := classifyRepairAction(plan.Repository, run, plan.Matrix, contract)
	if plan.Action != wantAction || plan.Applyable != wantApplyable {
		return malformed("repair_plan", "repair action is not canonically derived from run state")
	}
	return nil
}

func classifyRepairAction(repository string, run workflowRun, matrix attemptMatrix, contract releasecontract.Contract) (repairAction, bool) {
	if !workflowPathMatches(run.Path, workflowFile(contract, "ci")) {
		return repairAction{Code: "none", ReasonCode: "workflow_not_repairable"}, false
	}
	if run.Status != "completed" {
		return repairAction{Code: "wait_publisher", ReasonCode: "attempt_still_running"}, false
	}
	if isFailureConclusion(run.Conclusion) {
		reason := "CI_ATTEMPT_FAILED"
		if !matrix.Complete {
			reason = "ATTEMPT_MATRIX_INCOMPLETE"
		}
		return repairAction{
			Code: "rerun_all_jobs", ReasonCode: reason, Method: "POST",
			Endpoint: "repos/" + repository + "/actions/runs/" + strconv.FormatInt(run.ID, 10) + "/rerun",
		}, true
	}
	if matrix.Complete {
		return repairAction{Code: "none", ReasonCode: "attempt_matrix_complete"}, false
	}
	if !isFailureConclusion(run.Conclusion) {
		return repairAction{Code: "none", ReasonCode: "successful_attempt_artifacts_unavailable"}, false
	}
	return repairAction{Code: "none", ReasonCode: "workflow_state_unknown"}, false
}

func workflowPathMatches(actual, canonical string) bool {
	if canonical == "" || actual == "" {
		return false
	}
	if before, _, found := strings.Cut(actual, "@"); found {
		actual = before
	}
	return actual == canonical || actual == ".github/workflows/"+canonical
}

func classifyAttemptMatrix(run workflowRun, artifacts []workflowArtifact, contract releasecontract.Contract) attemptMatrix {
	matrix := attemptMatrix{RunID: run.ID, Attempt: run.RunAttempt, ExpectedTargets: []string{}, Expected: []string{}, Observed: []string{}, MissingTargets: []string{}, Missing: []string{}, Unexpected: []string{}, Duplicates: []string{}}
	wanted := make(map[string]string, len(contract.Platforms)*2)
	for _, platform := range contract.Platforms {
		matrix.ExpectedTargets = append(matrix.ExpectedTargets, platform.ID)
		for _, name := range []string{
			fmt.Sprintf("env-vault-release-%s-attempt-%d", platform.ID, run.RunAttempt),
			fmt.Sprintf("env-vault-promotion-platform-%s-attempt-%d", platform.ID, run.RunAttempt),
		} {
			matrix.Expected = append(matrix.Expected, name)
			wanted[name] = platform.ID
		}
	}
	counts := make(map[string]int)
	currentSuffix := fmt.Sprintf("-attempt-%d", run.RunAttempt)
	for _, artifact := range artifacts {
		if artifact.ID <= 0 || artifact.Name == "" || artifact.Expired {
			continue
		}
		if artifact.WorkflowRun.ID != 0 && artifact.WorkflowRun.ID != run.ID {
			continue
		}
		if artifact.WorkflowRun.HeadSHA != "" && artifact.WorkflowRun.HeadSHA != run.HeadSHA {
			continue
		}
		if (strings.HasPrefix(artifact.Name, "env-vault-release-") || strings.HasPrefix(artifact.Name, "env-vault-promotion-platform-")) && strings.HasSuffix(artifact.Name, currentSuffix) {
			counts[artifact.Name]++
		}
	}
	missingTargets := make(map[string]bool)
	for _, name := range matrix.Expected {
		count := counts[name]
		if count == 0 {
			matrix.Missing = append(matrix.Missing, name)
			missingTargets[wanted[name]] = true
		} else {
			matrix.Observed = append(matrix.Observed, name)
		}
		if count > 1 {
			matrix.Duplicates = append(matrix.Duplicates, name)
		}
	}
	for target := range missingTargets {
		matrix.MissingTargets = append(matrix.MissingTargets, target)
	}
	for name := range counts {
		if _, ok := wanted[name]; !ok {
			matrix.Unexpected = append(matrix.Unexpected, name)
		}
	}
	sort.Strings(matrix.ExpectedTargets)
	sort.Strings(matrix.Expected)
	sort.Strings(matrix.Observed)
	sort.Strings(matrix.MissingTargets)
	sort.Strings(matrix.Missing)
	sort.Strings(matrix.Unexpected)
	sort.Strings(matrix.Duplicates)
	matrix.Complete = len(matrix.Expected) == len(contract.Platforms)*2 && len(contract.Platforms) == 5 && len(matrix.Missing) == 0 && len(matrix.Unexpected) == 0 && len(matrix.Duplicates) == 0
	if matrix.Complete {
		matrix.ReasonCode = "attempt_matrix_complete"
	} else {
		matrix.ReasonCode = "ATTEMPT_MATRIX_INCOMPLETE"
	}
	return matrix
}

func getWorkflowRun(ctx context.Context, github githubGetter, repository string, runID int64) (workflowRun, error) {
	endpoint := "repos/" + repository + "/actions/runs/" + strconv.FormatInt(runID, 10)
	var run workflowRun
	if err := github.Get(ctx, endpoint, nil, &run); err != nil {
		return workflowRun{}, err
	}
	if err := validateRun(run, endpoint); err != nil {
		return workflowRun{}, err
	}
	return run, nil
}

func getAttemptJobs(ctx context.Context, github githubGetter, repository string, runID int64, attempt int) ([]workflowJob, error) {
	endpoint := "repos/" + repository + "/actions/runs/" + strconv.FormatInt(runID, 10) + "/attempts/" + strconv.Itoa(attempt) + "/jobs"
	var response jobsResponse
	if err := github.Get(ctx, endpoint, map[string]string{"per_page": "100"}, &response); err != nil {
		return nil, err
	}
	if response.Jobs == nil || response.TotalCount != 0 && response.TotalCount != len(response.Jobs) {
		return nil, malformed(endpoint, "attempt job matrix is absent or requires unsupported pagination")
	}
	for _, job := range response.Jobs {
		if err := validateJob(job, endpoint); err != nil {
			return nil, err
		}
		if job.RunAttempt != 0 && job.RunAttempt != attempt {
			return nil, malformed(endpoint, "job belongs to a different attempt")
		}
	}
	return response.Jobs, nil
}

func getAllAttemptJobs(ctx context.Context, github githubGetter, repository string, runID int64, latestAttempt int) ([]workflowJob, error) {
	endpoint := "repos/" + repository + "/actions/runs/" + strconv.FormatInt(runID, 10) + "/jobs"
	query := map[string]string{"filter": "all", "per_page": "100"}
	all := []workflowJob{}
	seen := map[int64]bool{}
	total := -1
	for page := 1; page <= 100; page++ {
		query["page"] = strconv.Itoa(page)
		var response jobsResponse
		if err := github.Get(ctx, endpoint, query, &response); err != nil {
			return nil, err
		}
		if response.Jobs == nil || response.TotalCount <= 0 {
			return nil, malformed(endpoint, "all-attempt job matrix is absent or empty")
		}
		if total == -1 {
			total = response.TotalCount
		} else if response.TotalCount != total {
			return nil, malformed(endpoint, "all-attempt job total changed during pagination")
		}
		if len(response.Jobs) == 0 && len(all) < total {
			return nil, malformed(endpoint, "all-attempt job pagination ended before total_count")
		}
		for _, job := range response.Jobs {
			if err := validateJob(job, endpoint); err != nil {
				return nil, err
			}
			if job.RunAttempt <= 0 || job.RunAttempt > latestAttempt || seen[job.ID] {
				return nil, malformed(endpoint, "all-attempt job identity is invalid or duplicated")
			}
			seen[job.ID] = true
			all = append(all, job)
		}
		if len(all) > total {
			return nil, malformed(endpoint, "all-attempt jobs exceed total_count")
		}
		if len(all) == total {
			break
		}
	}
	if len(all) != total {
		return nil, malformed(endpoint, "all-attempt job pagination exceeded the deterministic bound")
	}
	counts := make([]int, latestAttempt)
	for _, job := range all {
		counts[job.RunAttempt-1]++
	}
	for index, count := range counts {
		if count == 0 {
			return nil, malformed(endpoint, "all-attempt job matrix is missing attempt "+strconv.Itoa(index+1))
		}
	}
	return all, nil
}

func requireExactLatestAttempt(latest, all []workflowJob, attempt int) error {
	wanted := make(map[int64]bool, len(latest))
	for _, job := range latest {
		wanted[job.ID] = true
	}
	observed := map[int64]bool{}
	for _, job := range all {
		if job.RunAttempt == attempt {
			observed[job.ID] = true
		}
	}
	if len(wanted) != len(observed) {
		return malformed("workflow_metrics", "all-attempt jobs do not contain the exact latest attempt")
	}
	for id := range wanted {
		if !observed[id] {
			return malformed("workflow_metrics", "all-attempt jobs do not contain the exact latest attempt")
		}
	}
	return nil
}

func getWorkflowArtifacts(ctx context.Context, github githubGetter, repository string, runID int64) ([]workflowArtifact, error) {
	endpoint := "repos/" + repository + "/actions/runs/" + strconv.FormatInt(runID, 10) + "/artifacts"
	var response artifactsResponse
	if err := github.Get(ctx, endpoint, map[string]string{"per_page": "100"}, &response); err != nil {
		return nil, err
	}
	if response.Artifacts == nil || response.TotalCount != len(response.Artifacts) {
		return nil, malformed(endpoint, "artifact set is absent or requires unsupported pagination")
	}
	for _, artifact := range response.Artifacts {
		if artifact.ID <= 0 || artifact.Name == "" {
			return nil, malformed(endpoint, "artifact fields are malformed")
		}
	}
	return response.Artifacts, nil
}

func readRepairPlan(filename string) (releaseRepairPlan, error) {
	data, err := readRepairPlanBytes(filename)
	if err != nil {
		return releaseRepairPlan{}, err
	}
	var plan releaseRepairPlan
	if err := decodeStrictJSON(data, &plan); err != nil {
		return releaseRepairPlan{}, &observationError{code: "INPUT_INVALID", operation: "repair_plan_file", cause: err}
	}
	return plan, nil
}

func readPublisherRepairPlan(filename string) (publisherRepairPlan, error) {
	data, err := readRepairPlanBytes(filename)
	if err != nil {
		return publisherRepairPlan{}, err
	}
	var plan publisherRepairPlan
	if err := decodeStrictJSON(data, &plan); err != nil {
		return publisherRepairPlan{}, &observationError{code: "INPUT_INVALID", operation: "repair_plan_file", cause: err}
	}
	return plan, nil
}

func readRepairPlanKind(filename string) (string, error) {
	data, err := readRepairPlanBytes(filename)
	if err != nil {
		return "", err
	}
	var envelope struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.Kind == "" {
		return "", &observationError{code: "INPUT_INVALID", operation: "repair_plan_kind", cause: err}
	}
	return envelope.Kind, nil
}

func readRepairPlanBytes(filename string) ([]byte, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, &observationError{code: "INPUT_INVALID", operation: "repair_plan_file", cause: err}
	}
	if len(data) == 0 || len(data) > maxPlanBytes {
		return nil, &observationError{code: "INPUT_INVALID", operation: "repair_plan_file"}
	}
	return data, nil
}

func decodeStrictJSON(data []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("multiple JSON values")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func digestRepairPlan(plan releaseRepairPlan) string {
	return digestJSON(plan, func(value *releaseRepairPlan) { value.PlanDigest = "" })
}

func digestJSON[T any](value T, clear func(*T)) string {
	copyValue := value
	clear(&copyValue)
	data, err := json.Marshal(copyValue)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func canonicalDigest(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func findRepositoryFile(start, relative string) (string, error) {
	directory, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(directory, filepath.FromSlash(relative))
		info, statErr := os.Stat(candidate)
		if statErr == nil {
			if !info.Mode().IsRegular() {
				return "", fmt.Errorf("repository path is not a regular file: %s", candidate)
			}
			return candidate, nil
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return "", statErr
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return "", fmt.Errorf("repository file %s not found from %s", relative, start)
		}
		directory = parent
	}
}

func operatorErrorInfo(err error) *errorInfo {
	doc := errorDocument(time.Time{}, query{}, err)
	if doc.Error != nil {
		return doc.Error
	}
	return &errorInfo{Code: "OBSERVATION_FAILED", Operation: "operator_command"}
}

func seconds(value time.Duration) int64 {
	if value <= 0 {
		return 0
	}
	return int64(value / time.Second)
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
