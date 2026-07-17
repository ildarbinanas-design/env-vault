package releasectl

import (
	"context"
	"strconv"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

const repairKindTapCI = "tap_ci_attempt"

type tapCIRepairPlan struct {
	Schema           string                       `json:"schema"`
	Kind             string                       `json:"kind"`
	OK               bool                         `json:"ok"`
	GeneratedAt      time.Time                    `json:"generated_at"`
	PlanDigest       string                       `json:"plan_digest"`
	SourceRepository string                       `json:"source_repository"`
	Repository       string                       `json:"repository"`
	Version          string                       `json:"version"`
	SourceSHA        string                       `json:"source_sha"`
	Stage            string                       `json:"stage"`
	Run              runEvidence                  `json:"run"`
	Preconditions    publisherRepairPreconditions `json:"preconditions"`
	Action           repairAction                 `json:"action"`
	Applyable        bool                         `json:"applyable"`
	NextAction       nextAction                   `json:"next_action"`
}

type tapCIRepairApplyDocument struct {
	Schema      string       `json:"schema"`
	OK          bool         `json:"ok"`
	ObservedAt  time.Time    `json:"observed_at"`
	PlanDigest  string       `json:"plan_digest"`
	DryRun      bool         `json:"dry_run"`
	Status      string       `json:"status"`
	Action      repairAction `json:"action"`
	Repository  string       `json:"repository"`
	Version     string       `json:"version"`
	SourceSHA   string       `json:"source_sha"`
	RunID       int64        `json:"run_id"`
	FromAttempt int          `json:"from_attempt"`
	ToAttempt   int          `json:"to_attempt,omitempty"`
	NextAction  *nextAction  `json:"next_action,omitempty"`
	Error       *errorInfo   `json:"error,omitempty"`
}

func planPublisherOrTapRepair(ctx context.Context, repository, version, sourceSHA string, contract releasecontract.Contract, github githubGetter, clk clock) (any, error) {
	if err := validatePublisherRepairVersion(version, sourceSHA, contract); err != nil {
		return nil, err
	}
	preconditions, err := observePublisherRepairPreconditions(ctx, repository, version, sourceSHA, contract, github, clk)
	if err != nil {
		return nil, err
	}
	if plan, ok, buildErr := buildTapCIRepairPlan(repository, version, sourceSHA, contract, clk, preconditions); buildErr != nil || ok {
		return plan, buildErr
	}
	return buildPublisherRepairPlan(repository, version, sourceSHA, contract, clk, preconditions)
}

func buildTapCIRepairPlan(sourceRepository, version, sourceSHA string, contract releasecontract.Contract, clk clock, preconditions publisherRepairPreconditions) (tapCIRepairPlan, bool, error) {
	stageName, run, action, ok, err := classifyTapCIRepair(sourceRepository, version, preconditions.Status, contract)
	if err != nil || !ok {
		return tapCIRepairPlan{}, false, err
	}
	plan := tapCIRepairPlan{
		Schema: contract.Schemas["repair_plan"], Kind: repairKindTapCI, OK: true, GeneratedAt: clk.Now().UTC(),
		SourceRepository: sourceRepository, Repository: preconditions.Status.Stages.Homebrew.Homebrew.Repository,
		Version: version, SourceSHA: sourceSHA, Stage: stageName, Run: run, Preconditions: preconditions,
		Action: action, Applyable: true, NextAction: nextAction{Code: "wait_tap_ci"},
	}
	if plan.Schema == "" {
		plan.Schema = "env-vault.release-repair-plan.v1"
	}
	plan.PlanDigest = digestTapCIRepairPlan(plan)
	return plan, true, nil
}

func classifyTapCIRepair(sourceRepository, version string, status publisherRepairStatusSnapshot, contract releasecontract.Contract) (string, runEvidence, repairAction, bool, error) {
	if !repositoryPattern.MatchString(sourceRepository) {
		return "", runEvidence{}, repairAction{}, false, malformed("tap_ci_repair", "source repository is malformed")
	}
	evidence := status.Stages.Homebrew.Homebrew
	if evidence == nil {
		return "", runEvidence{}, repairAction{}, false, nil
	}
	app, ok := contract.AppByID("homebrew_tap")
	if !ok {
		return "", runEvidence{}, repairAction{}, false, malformed("tap_ci_repair", "homebrew tap contract is missing")
	}
	if evidence.Repository != app.Repository || evidence.DeterministicBranch != "release/env-vault-"+version || evidence.PullRequest == nil {
		return "", runEvidence{}, repairAction{}, false, nil
	}

	stageName, actionCode := "", ""
	var candidate *runEvidence
	switch evidence.ReasonCode {
	case "homebrew_pr_head_ci_not_successful":
		stageName, actionCode, candidate = "pr_head", "rerun_tap_pr_ci_all_jobs", evidence.PRHeadCI
	case "homebrew_post_merge_ci_not_successful":
		stageName, actionCode, candidate = "post_merge", "rerun_tap_post_merge_ci_all_jobs", evidence.PostMergeCI
	default:
		return "", runEvidence{}, repairAction{}, false, nil
	}
	if !contractHasActionCode(contract, actionCode) || !contractHasActionCode(contract, "wait_tap_ci") || !contractHasActionCode(contract, "replan_publisher") {
		return "", runEvidence{}, repairAction{}, false, malformed("tap_ci_repair", "tap repair action code is absent from the release contract")
	}
	if candidate == nil || candidate.ID <= 0 || candidate.Attempt <= 0 || !workflowPathMatches(candidate.WorkflowPath, app.CIWorkflowFile) {
		return "", runEvidence{}, repairAction{}, false, malformed("tap_ci_repair", "failed tap CI run identity is incomplete")
	}
	if candidate.Status != "completed" {
		if candidate.Conclusion == "" {
			return "", runEvidence{}, repairAction{}, false, nil
		}
		return "", runEvidence{}, repairAction{}, false, malformed("tap_ci_repair", "non-completed tap CI run has a conclusion")
	}
	if !isFailureConclusion(candidate.Conclusion) {
		return "", runEvidence{}, repairAction{}, false, nil
	}
	if stageName == "pr_head" {
		if candidate.Event != "pull_request" || candidate.HeadBranch != evidence.DeterministicBranch || candidate.HeadSHA != evidence.PullRequest.HeadSHA {
			return "", runEvidence{}, repairAction{}, false, malformed("tap_ci_repair", "PR-head tap CI identity is inconsistent")
		}
	} else if candidate.Event != "push" || candidate.HeadBranch != evidence.DefaultBranch || candidate.HeadSHA != evidence.PullRequest.MergeSHA || !evidence.MergeOnDefaultBranch {
		return "", runEvidence{}, repairAction{}, false, malformed("tap_ci_repair", "post-merge tap CI identity is inconsistent")
	}
	action := repairAction{
		Code: actionCode, ReasonCode: "TAP_CI_ATTEMPT_FAILED", Method: "POST",
		Endpoint: "repos/" + app.Repository + "/actions/runs/" + strconv.FormatInt(candidate.ID, 10) + "/rerun",
	}
	return stageName, *candidate, action, true, nil
}

func applyTapCIRepair(ctx context.Context, plan tapCIRepairPlan, suppliedDigest string, apply bool, contract releasecontract.Contract, github githubGetter, mutator githubMutator, clk clock) (tapCIRepairApplyDocument, int) {
	result := tapCIRepairApplyDocument{
		Schema: contract.Schemas["release_repair_apply"], ObservedAt: clk.Now().UTC(), PlanDigest: suppliedDigest,
		DryRun: !apply, Status: "blocked", Action: plan.Action, Repository: plan.Repository,
		Version: plan.Version, SourceSHA: plan.SourceSHA, RunID: plan.Run.ID, FromAttempt: plan.Run.Attempt,
	}
	if result.Schema == "" {
		result.Schema = releaseRepairApplySchema
	}
	if err := validateTapCIRepairPlan(plan, suppliedDigest, contract); err != nil {
		result.Error = operatorErrorInfo(err)
		return result, exitPreconditionFailed
	}
	resolved, found, err := (collector{github: github, contract: contract}).resolveTag(ctx, plan.SourceRepository, plan.Version)
	if err != nil {
		result.Error = operatorErrorInfo(err)
		return result, exitObservationError
	}
	if !found || resolved != plan.SourceSHA {
		result.Error = &errorInfo{Code: "REMOTE_PRECONDITION_FAILED", Operation: "tap_repair_tag_source_sha"}
		return result, exitPreconditionFailed
	}

	run, err := getWorkflowRun(ctx, github, plan.Repository, plan.Run.ID)
	if err != nil {
		result.Error = operatorErrorInfo(err)
		return result, exitObservationError
	}
	if !tapRunMatchesPlan(run, plan, contract) {
		result.Error = &errorInfo{Code: "REMOTE_PRECONDITION_FAILED", Operation: "tap_repair_run_identity"}
		return result, exitPreconditionFailed
	}
	if run.RunAttempt > plan.Run.Attempt || run.Status != plan.Run.Status {
		if run.Status != "completed" {
			result.OK, result.Status, result.ToAttempt = true, "already_applied", run.RunAttempt
			result.NextAction = &nextAction{Code: "wait_tap_ci"}
			return result, exitSuccess
		}
		if run.Conclusion == "success" {
			result.OK, result.Status, result.ToAttempt = true, "already_applied", run.RunAttempt
			result.NextAction = &nextAction{Code: "replan_publisher"}
			return result, exitSuccess
		}
		result.Error = &errorInfo{Code: "REMOTE_PRECONDITION_FAILED", Operation: "tap_repair_newer_attempt_failed"}
		return result, exitPreconditionFailed
	}
	if run.RunAttempt != plan.Run.Attempt || run.Conclusion != plan.Run.Conclusion {
		result.Error = &errorInfo{Code: "REMOTE_PRECONDITION_FAILED", Operation: "tap_repair_run_state"}
		return result, exitPreconditionFailed
	}

	current, err := observePublisherRepairPreconditions(ctx, plan.SourceRepository, plan.Version, plan.SourceSHA, contract, github, clk)
	if err != nil {
		result.Error = operatorErrorInfo(err)
		if result.Error.Code == "REMOTE_PRECONDITION_FAILED" {
			return result, exitPreconditionFailed
		}
		return result, exitObservationError
	}
	if current.StateDigest != plan.Preconditions.StateDigest || digestPublisherRepairPreconditions(current) != digestPublisherRepairPreconditions(plan.Preconditions) {
		result.Error = &errorInfo{Code: "REMOTE_PRECONDITION_FAILED", Operation: "tap_repair_remote_state"}
		return result, exitPreconditionFailed
	}
	stageName, wantedRun, wantedAction, applyable, classifyErr := classifyTapCIRepair(plan.SourceRepository, plan.Version, current.Status, contract)
	if classifyErr != nil || !applyable || stageName != plan.Stage || wantedRun != plan.Run || wantedAction != plan.Action {
		result.Error = &errorInfo{Code: "REMOTE_PRECONDITION_FAILED", Operation: "tap_repair_classification"}
		return result, exitPreconditionFailed
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
		observed, observeErr := getWorkflowRun(ctx, github, plan.Repository, plan.Run.ID)
		if observeErr != nil {
			result.Error = operatorErrorInfo(observeErr)
			return result, exitObservationError
		}
		if !tapRunMatchesPlan(observed, plan, contract) {
			result.Error = &errorInfo{Code: "REMOTE_PRECONDITION_FAILED", Operation: "tap_repair_run_identity"}
			return result, exitPreconditionFailed
		}
		if observed.RunAttempt > plan.Run.Attempt || observed.Status != "completed" {
			result.OK, result.Status, result.ToAttempt = true, "applied", observed.RunAttempt
			result.NextAction = &nextAction{Code: "wait_tap_ci"}
			return result, exitSuccess
		}
		if poll < 4 {
			if sleepErr := clk.Sleep(ctx, time.Second); sleepErr != nil {
				result.Error = operatorErrorInfo(sleepErr)
				return result, exitObservationError
			}
		}
	}
	result.Error = &errorInfo{Code: "REMOTE_STATE_UNKNOWN", Operation: "tap_rerun_transition_not_observed", Retryable: true}
	return result, exitObservationError
}

func validateTapCIRepairPlan(plan tapCIRepairPlan, suppliedDigest string, contract releasecontract.Contract) error {
	if !validHexDigest(suppliedDigest) || plan.PlanDigest != suppliedDigest || digestTapCIRepairPlan(plan) != suppliedDigest {
		return &observationError{code: "PLAN_DIGEST_MISMATCH", operation: "tap_repair_plan_digest"}
	}
	if plan.Schema != contract.Schemas["repair_plan"] || plan.Kind != repairKindTapCI || !plan.OK || !plan.Applyable || plan.GeneratedAt.IsZero() ||
		!repositoryPattern.MatchString(plan.SourceRepository) || !repositoryPattern.MatchString(plan.Repository) || !releasecontract.IsVersion(plan.Version) || !shaPattern.MatchString(plan.SourceSHA) ||
		plan.Preconditions.TagSHA != plan.SourceSHA || plan.Preconditions.DispatchSHA != plan.SourceSHA || plan.Preconditions.StateDigest != digestPublisherRepairPreconditions(plan.Preconditions) {
		return malformed("tap_repair_plan", "tap repair plan identity or preconditions are malformed")
	}
	stageName, run, action, applyable, err := classifyTapCIRepair(plan.SourceRepository, plan.Version, plan.Preconditions.Status, contract)
	if err != nil {
		return err
	}
	if !applyable || stageName != plan.Stage || run != plan.Run || action != plan.Action || plan.Repository != plan.Preconditions.Status.Stages.Homebrew.Homebrew.Repository || plan.NextAction.Code != "wait_tap_ci" {
		return malformed("tap_repair_plan", "tap repair action is not canonically derived from release state")
	}
	return nil
}

func tapRunMatchesPlan(run workflowRun, plan tapCIRepairPlan, contract releasecontract.Contract) bool {
	app, ok := contract.AppByID("homebrew_tap")
	if !ok || run.ID != plan.Run.ID || run.Event != plan.Run.Event || run.HeadBranch != plan.Run.HeadBranch || run.HeadSHA != plan.Run.HeadSHA || !workflowPathMatches(run.Path, app.CIWorkflowFile) {
		return false
	}
	return plan.Repository == app.Repository
}

func digestTapCIRepairPlan(plan tapCIRepairPlan) string {
	return digestJSON(plan, func(value *tapCIRepairPlan) { value.PlanDigest = "" })
}

func readTapCIRepairPlan(filename string) (tapCIRepairPlan, error) {
	data, err := readRepairPlanBytes(filename)
	if err != nil {
		return tapCIRepairPlan{}, err
	}
	var plan tapCIRepairPlan
	if err := decodeStrictJSON(data, &plan); err != nil {
		return tapCIRepairPlan{}, &observationError{code: "INPUT_INVALID", operation: "repair_plan_file", cause: err}
	}
	return plan, nil
}
