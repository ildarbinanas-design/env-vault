package releasectl

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

const repairKindPublisher = "publisher_dispatch"

type publisherWorkflowSnapshot struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	State string `json:"state"`
}

type publisherRepairStatusSnapshot struct {
	Overall    overall     `json:"overall"`
	Stages     stages      `json:"stages"`
	FailedJobs []failedJob `json:"failed_jobs"`
	NextAction nextAction  `json:"next_action"`
}

// publisherArtifactInventory is deliberately narrower than attemptMatrix.
// The latter proves that a CI attempt produced both the five native artifacts
// and five platform evidence records, which is needed to decide whether a
// failed CI attempt must be rerun in full. The publisher consumes only this
// exact six-artifact inventory: the aggregate promotion manifest plus the five
// native release artifacts from one successful CI run attempt.
type publisherArtifactInventory struct {
	RunID      int64                        `json:"run_id"`
	Attempt    int                          `json:"attempt"`
	SourceSHA  string                       `json:"source_sha"`
	Candidates []publisherArtifactCandidate `json:"candidates"`
	Expected   []string                     `json:"expected_artifacts"`
	Observed   []string                     `json:"observed_artifacts"`
	Missing    []string                     `json:"missing_artifacts"`
	Unexpected []string                     `json:"unexpected_artifacts"`
	Expired    []string                     `json:"expired_artifacts"`
	Duplicates []string                     `json:"duplicate_artifacts"`
	Stale      []string                     `json:"stale_artifacts"`
	WrongRun   []string                     `json:"wrong_run_artifacts"`
	Complete   bool                         `json:"complete"`
	ReasonCode string                       `json:"reason_code"`
}

type publisherArtifactCandidate struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Expired   bool   `json:"expired"`
	RunID     int64  `json:"run_id"`
	SourceSHA string `json:"source_sha"`
}

type publisherRepairPreconditions struct {
	TagSHA      string                        `json:"tag_sha"`
	DispatchSHA string                        `json:"dispatch_sha"`
	Workflow    publisherWorkflowSnapshot     `json:"workflow"`
	Status      publisherRepairStatusSnapshot `json:"status"`
	Artifacts   publisherArtifactInventory    `json:"publisher_artifacts"`
	StateDigest string                        `json:"state_digest"`
}

type publisherRepairInputs struct {
	Version     string `json:"version"`
	Repair      string `json:"repair"`
	StateDigest string `json:"repair_state_digest"`
}

type publisherRepairPlan struct {
	Schema        string                       `json:"schema"`
	Kind          string                       `json:"kind"`
	OK            bool                         `json:"ok"`
	GeneratedAt   time.Time                    `json:"generated_at"`
	PlanDigest    string                       `json:"plan_digest"`
	Repository    string                       `json:"repository"`
	Version       string                       `json:"version"`
	SourceSHA     string                       `json:"source_sha"`
	WorkflowRef   string                       `json:"workflow_ref"`
	Inputs        publisherRepairInputs        `json:"inputs"`
	Preconditions publisherRepairPreconditions `json:"preconditions"`
	Action        repairAction                 `json:"action"`
	Applyable     bool                         `json:"applyable"`
}

type publisherRepairApplyDocument struct {
	Schema      string       `json:"schema"`
	OK          bool         `json:"ok"`
	ObservedAt  time.Time    `json:"observed_at"`
	PlanDigest  string       `json:"plan_digest"`
	DryRun      bool         `json:"dry_run"`
	Status      string       `json:"status"`
	Action      repairAction `json:"action"`
	Version     string       `json:"version"`
	SourceSHA   string       `json:"source_sha"`
	WorkflowRun *runEvidence `json:"workflow_run,omitempty"`
	Error       *errorInfo   `json:"error,omitempty"`
}

type publisherWorkflowAPI struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	State string `json:"state"`
}

type publisherRunsResponse struct {
	TotalCount   int           `json:"total_count"`
	WorkflowRuns []workflowRun `json:"workflow_runs"`
}

func planPublisherRepair(ctx context.Context, repository, version, sourceSHA string, contract releasecontract.Contract, github githubGetter, clk clock) (publisherRepairPlan, error) {
	if err := validatePublisherRepairVersion(version, sourceSHA, contract); err != nil {
		return publisherRepairPlan{}, err
	}
	preconditions, err := observePublisherRepairPreconditions(ctx, repository, version, sourceSHA, contract, github, clk)
	if err != nil {
		return publisherRepairPlan{}, err
	}
	return buildPublisherRepairPlan(repository, version, sourceSHA, contract, clk, preconditions)
}

func buildPublisherRepairPlan(repository, version, sourceSHA string, contract releasecontract.Contract, clk clock, preconditions publisherRepairPreconditions) (publisherRepairPlan, error) {
	mode, reasonCode, applyable := classifyPublisherRepair(preconditions.Status, preconditions.Artifacts)
	action := canonicalPublisherRepairAction(repository, mode, reasonCode, contract)
	if applyable && action.Code == "none" {
		return publisherRepairPlan{}, malformed("publisher_repair_action", "contract does not contain the derived publisher repair action")
	}
	plan := publisherRepairPlan{
		Schema: contract.Schemas["repair_plan"], Kind: repairKindPublisher, OK: true,
		GeneratedAt: clk.Now().UTC(), Repository: repository, Version: version, SourceSHA: sourceSHA,
		WorkflowRef: version, Inputs: publisherRepairInputs{Version: version, Repair: mode, StateDigest: preconditions.StateDigest},
		Preconditions: preconditions, Action: action, Applyable: applyable,
	}
	if plan.Schema == "" {
		plan.Schema = "env-vault.release-repair-plan.v1"
	}
	plan.PlanDigest = digestPublisherRepairPlan(plan)
	return plan, nil
}

func applyPublisherRepair(ctx context.Context, plan publisherRepairPlan, suppliedDigest string, apply bool, contract releasecontract.Contract, github githubGetter, mutator githubMutator, clk clock) (publisherRepairApplyDocument, int) {
	result := publisherRepairApplyDocument{
		Schema: contract.Schemas["release_repair_apply"], ObservedAt: clk.Now().UTC(),
		PlanDigest: suppliedDigest, DryRun: !apply, Status: "blocked", Action: plan.Action,
		Version: plan.Version, SourceSHA: plan.SourceSHA,
	}
	if result.Schema == "" {
		result.Schema = releaseRepairApplySchema
	}
	if err := validatePublisherRepairPlan(plan, suppliedDigest, contract); err != nil {
		result.Error = operatorErrorInfo(err)
		return result, exitPreconditionFailed
	}
	resolved, found, err := (collector{github: github, contract: contract}).resolveTag(ctx, plan.Repository, plan.Version)
	if err != nil {
		result.Error = operatorErrorInfo(err)
		return result, exitObservationError
	}
	if !found || resolved != plan.SourceSHA {
		result.Error = &errorInfo{Code: "REMOTE_PRECONDITION_FAILED", Operation: "publisher_tag_source_sha"}
		return result, exitPreconditionFailed
	}

	existing, found, err := findEquivalentPublisherRepairRun(ctx, github, plan, contract)
	if err != nil {
		result.Error = operatorErrorInfo(err)
		if result.Error.Code == "REMOTE_PRECONDITION_FAILED" {
			return result, exitPreconditionFailed
		}
		return result, exitObservationError
	}
	if found {
		result.OK, result.Status, result.WorkflowRun = true, "already_applied", evidenceFromRun(existing)
		return result, exitSuccess
	}

	current, err := observePublisherRepairPreconditions(ctx, plan.Repository, plan.Version, plan.SourceSHA, contract, github, clk)
	if err != nil {
		result.Error = operatorErrorInfo(err)
		if result.Error.Code == "REMOTE_PRECONDITION_FAILED" {
			return result, exitPreconditionFailed
		}
		return result, exitObservationError
	}
	if current.StateDigest != plan.Preconditions.StateDigest || digestPublisherRepairPreconditions(current) != digestPublisherRepairPreconditions(plan.Preconditions) {
		result.Error = &errorInfo{Code: "REMOTE_PRECONDITION_FAILED", Operation: "publisher_remote_state"}
		return result, exitPreconditionFailed
	}
	mode, reasonCode, applyable := classifyPublisherRepair(current.Status, current.Artifacts)
	wantAction := canonicalPublisherRepairAction(plan.Repository, mode, reasonCode, contract)
	if !applyable || mode != plan.Inputs.Repair || wantAction != plan.Action {
		result.Error = &errorInfo{Code: "REMOTE_PRECONDITION_FAILED", Operation: "publisher_repair_classification"}
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
	body := map[string]any{
		"ref": plan.WorkflowRef,
		"inputs": map[string]string{
			"version": plan.Version, "repair": plan.Inputs.Repair,
			"repair_state_digest": plan.Inputs.StateDigest,
		},
	}
	if err := mutator.Mutate(ctx, "POST", plan.Action.Endpoint, body, nil); err != nil {
		result.Error = operatorErrorInfo(err)
		return result, exitObservationError
	}
	result.OK, result.Status = true, "applied"
	return result, exitSuccess
}

func observePublisherRepairPreconditions(ctx context.Context, repository, version, sourceSHA string, contract releasecontract.Contract, github githubGetter, clk clock) (publisherRepairPreconditions, error) {
	resolved, found, err := (collector{github: github, contract: contract}).resolveTag(ctx, repository, version)
	if err != nil {
		return publisherRepairPreconditions{}, err
	}
	if !found || resolved != sourceSHA {
		return publisherRepairPreconditions{}, &observationError{code: "REMOTE_PRECONDITION_FAILED", operation: "publisher_tag_source_sha"}
	}

	workflowName := workflowFile(contract, "publisher")
	workflowEndpoint := "repos/" + repository + "/actions/workflows/" + workflowName
	var workflow publisherWorkflowAPI
	if err := github.Get(ctx, workflowEndpoint, nil, &workflow); err != nil {
		return publisherRepairPreconditions{}, err
	}
	wantPath := ".github/workflows/" + workflowName
	if workflow.ID <= 0 || workflow.Name != "build-binaries" || workflow.Path != wantPath || workflow.State != "active" {
		return publisherRepairPreconditions{}, malformed(workflowEndpoint, "publisher workflow identity or state is malformed")
	}

	status, err := (collector{github: github, clock: clk, contract: contract}).snapshot(ctx, query{Repository: repository, Version: version, SourceSHA: sourceSHA})
	if err != nil {
		return publisherRepairPreconditions{}, err
	}
	if status.Identity == nil || status.Identity.SourceSHA != sourceSHA || status.Overall == nil || status.Stages == nil || status.NextAction == nil {
		return publisherRepairPreconditions{}, malformed("publisher_release_status", "release status identity or stage graph is incomplete")
	}
	snapshot := publisherRepairStatusSnapshot{
		Overall: *status.Overall, Stages: *status.Stages,
		FailedJobs: append([]failedJob(nil), status.FailedJobs...), NextAction: *status.NextAction,
	}
	artifacts := publisherArtifactInventory{SourceSHA: sourceSHA, Candidates: []publisherArtifactCandidate{}, Expected: []string{}, Observed: []string{}, Missing: []string{}, Unexpected: []string{}, Expired: []string{}, Duplicates: []string{}, Stale: []string{}, WrongRun: []string{}, ReasonCode: "PUBLISHER_MAIN_CI_RUN_MISSING"}
	if status.Stages.MainCI.Run != nil {
		mainRun := status.Stages.MainCI.Run
		inventoryArtifacts, artifactErr := getWorkflowArtifacts(ctx, github, repository, mainRun.ID)
		if artifactErr != nil {
			return publisherRepairPreconditions{}, artifactErr
		}
		artifacts = classifyPublisherArtifactInventory(workflowRun{
			ID: mainRun.ID, RunAttempt: mainRun.Attempt, HeadSHA: mainRun.HeadSHA,
		}, inventoryArtifacts, contract)
	}
	preconditions := publisherRepairPreconditions{
		TagSHA: resolved, DispatchSHA: resolved,
		Workflow: publisherWorkflowSnapshot{ID: workflow.ID, Name: workflow.Name, Path: workflow.Path, State: workflow.State},
		Status:   snapshot, Artifacts: artifacts,
	}
	preconditions.StateDigest = digestPublisherRepairPreconditions(preconditions)
	return preconditions, nil
}

func classifyPublisherRepair(status publisherRepairStatusSnapshot, artifacts publisherArtifactInventory) (mode, reasonCode string, applyable bool) {
	if status.Overall.State == "succeeded" {
		return "none", "release_already_succeeded", false
	}
	for _, stage := range []stage{status.Stages.Publisher, status.Stages.GitHubRelease, status.Stages.SupplyChain, status.Stages.Homebrew, status.Stages.Health} {
		if stage.State == stateQueued || stage.State == stateInProgress {
			return "none", "publisher_repair_in_progress", false
		}
	}
	if status.Stages.Tag.State != stateSucceeded || status.Stages.MainCI.State != stateSucceeded || status.Stages.Planning.State != stateSucceeded {
		return "none", "publisher_repair_prerequisites_unsatisfied", false
	}

	// Recognized release-state inconsistencies have a deterministic repair. All
	// other inconsistent/unknown states remain fail closed.
	if status.Stages.GitHubRelease.State == stateInconsistent {
		switch status.Stages.GitHubRelease.Reason {
		case "release_assets_incomplete", "successful_release_job_without_release":
			if !artifacts.Complete {
				return "none", artifacts.ReasonCode, false
			}
			return "release-assets", "release_assets_incomplete", true
		default:
			return "none", "publisher_remote_state_inconsistent", false
		}
	}
	if status.Stages.SupplyChain.State == stateInconsistent && status.Stages.SupplyChain.Reason == "attestations_incomplete" {
		if !artifacts.Complete {
			return "none", artifacts.ReasonCode, false
		}
		return "release-assets", "release_attestations_incomplete", true
	}
	if evidence := status.Stages.Homebrew.Homebrew; evidence != nil &&
		(evidence.ReasonCode == "homebrew_pr_head_ci_not_successful" || evidence.ReasonCode == "homebrew_post_merge_ci_not_successful") {
		return "none", "tap_ci_repair_required", false
	}
	if status.Stages.Homebrew.State == stateInconsistent {
		switch status.Stages.Homebrew.Reason {
		case "homebrew_pr_missing", "homebrew_pr_not_merged":
			return "homebrew", "homebrew_evidence_repair_required", true
		default:
			return "none", "publisher_remote_state_inconsistent", false
		}
	}
	if status.Overall.State == "inconsistent" {
		return "none", "publisher_remote_state_inconsistent", false
	}

	derived := map[string]bool{}
	for _, failed := range status.FailedJobs {
		switch failed.Name {
		case "preflight", "promotion", "release", "supply_chain":
			derived["release-assets"] = true
		case "homebrew":
			derived["homebrew"] = true
		case "health":
			derived["health"] = true
		default:
			return "none", "publisher_failure_not_repairable", false
		}
	}
	for _, candidate := range []string{"release-assets", "homebrew", "health"} {
		if !derived[candidate] {
			continue
		}
		if candidate == "release-assets" && !artifacts.Complete {
			return "none", artifacts.ReasonCode, false
		}
		return candidate, publisherRepairReason(candidate), true
	}

	switch status.Overall.Reason {
	case "github_release_failed", "supply_chain_failed":
		if !artifacts.Complete {
			return "none", artifacts.ReasonCode, false
		}
		return "release-assets", publisherRepairReason("release-assets"), true
	case "homebrew_failed":
		if status.Stages.GitHubRelease.State == stateSucceeded && status.Stages.SupplyChain.State == stateSucceeded {
			return "homebrew", publisherRepairReason("homebrew"), true
		}
	case "health_failed":
		if status.Stages.GitHubRelease.State == stateSucceeded && status.Stages.SupplyChain.State == stateSucceeded && status.Stages.Homebrew.State == stateSucceeded {
			return "health", publisherRepairReason("health"), true
		}
	}
	return "none", "publisher_failure_not_repairable", false
}

func classifyPublisherArtifactInventory(run workflowRun, artifacts []workflowArtifact, contract releasecontract.Contract) publisherArtifactInventory {
	inventory := publisherArtifactInventory{
		RunID: run.ID, Attempt: run.RunAttempt, SourceSHA: run.HeadSHA,
		Candidates: []publisherArtifactCandidate{}, Expected: []string{}, Observed: []string{}, Missing: []string{}, Unexpected: []string{},
		Expired: []string{}, Duplicates: []string{}, Stale: []string{}, WrongRun: []string{},
	}
	manifest := publisherManifestArtifactName(run.HeadSHA, run.RunAttempt)
	inventory.Expected = expectedPublisherArtifactNames(run.HeadSHA, run.RunAttempt, contract)

	wanted := make(map[string]bool, len(inventory.Expected))
	total := make(map[string]int, len(inventory.Expected))
	valid := make(map[string]int, len(inventory.Expected))
	for _, name := range inventory.Expected {
		wanted[name] = true
	}
	currentSuffix := fmt.Sprintf("-attempt-%d", run.RunAttempt)
	for _, artifact := range artifacts {
		if strings.HasPrefix(artifact.Name, "env-vault-promotion-platform-") {
			continue
		}
		relevant := wanted[artifact.Name] || strings.HasSuffix(artifact.Name, currentSuffix) &&
			(strings.HasPrefix(artifact.Name, "env-vault-promotion-") || strings.HasPrefix(artifact.Name, "env-vault-release-"))
		if !relevant {
			continue
		}
		inventory.Candidates = append(inventory.Candidates, publisherArtifactCandidate{
			ID: artifact.ID, Name: artifact.Name, Expired: artifact.Expired,
			RunID: artifact.WorkflowRun.ID, SourceSHA: artifact.WorkflowRun.HeadSHA,
		})
		if !wanted[artifact.Name] {
			if strings.HasPrefix(artifact.Name, "env-vault-promotion-") && strings.HasSuffix(artifact.Name, currentSuffix) {
				inventory.Stale = append(inventory.Stale, artifact.Name)
			} else if strings.HasPrefix(artifact.Name, "env-vault-release-") && strings.HasSuffix(artifact.Name, currentSuffix) {
				inventory.Unexpected = append(inventory.Unexpected, artifact.Name)
			}
			continue
		}

		total[artifact.Name]++
		usable := true
		if artifact.Expired {
			inventory.Expired = append(inventory.Expired, artifact.Name)
			usable = false
		}
		if artifact.WorkflowRun.ID != run.ID {
			inventory.WrongRun = append(inventory.WrongRun, artifact.Name)
			usable = false
		}
		if artifact.WorkflowRun.HeadSHA != run.HeadSHA {
			inventory.Stale = append(inventory.Stale, artifact.Name)
			usable = false
		}
		if usable {
			valid[artifact.Name]++
		}
	}
	for _, name := range inventory.Expected {
		if valid[name] == 0 {
			inventory.Missing = append(inventory.Missing, name)
		} else {
			inventory.Observed = append(inventory.Observed, name)
		}
		if total[name] > 1 {
			inventory.Duplicates = append(inventory.Duplicates, name)
		}
	}
	inventory.Observed = canonicalArtifactNames(inventory.Observed)
	sort.Slice(inventory.Candidates, func(i, j int) bool {
		return publisherArtifactCandidateLess(inventory.Candidates[i], inventory.Candidates[j])
	})
	inventory.Missing = canonicalArtifactNames(inventory.Missing)
	inventory.Unexpected = canonicalArtifactNames(inventory.Unexpected)
	inventory.Expired = canonicalArtifactNames(inventory.Expired)
	inventory.Duplicates = canonicalArtifactNames(inventory.Duplicates)
	inventory.Stale = canonicalArtifactNames(inventory.Stale)
	inventory.WrongRun = canonicalArtifactNames(inventory.WrongRun)
	inventory.Complete = len(contract.Platforms) == 5 && len(inventory.Expected) == 6 &&
		len(inventory.Missing) == 0 && len(inventory.Unexpected) == 0 && len(inventory.Expired) == 0 &&
		len(inventory.Duplicates) == 0 && len(inventory.Stale) == 0 && len(inventory.WrongRun) == 0
	inventory.ReasonCode = publisherArtifactInventoryReason(inventory, manifest)
	return inventory
}

func publisherArtifactInventoryReason(inventory publisherArtifactInventory, manifest string) string {
	if inventory.Complete {
		return "publisher_promotion_inventory_complete"
	}
	if slices.Contains(inventory.Expired, manifest) {
		return "PUBLISHER_PROMOTION_MANIFEST_EXPIRED"
	}
	if slices.Contains(inventory.WrongRun, manifest) || slices.Contains(inventory.Stale, manifest) {
		return "PUBLISHER_PROMOTION_MANIFEST_IDENTITY_MISMATCH"
	}
	if slices.Contains(inventory.Missing, manifest) {
		return "PUBLISHER_PROMOTION_MANIFEST_MISSING"
	}
	if len(inventory.Duplicates) != 0 {
		return "PUBLISHER_PROMOTION_ARTIFACT_DUPLICATE"
	}
	if len(inventory.WrongRun) != 0 {
		return "PUBLISHER_PROMOTION_ARTIFACT_WRONG_RUN"
	}
	if len(inventory.Stale) != 0 {
		return "PUBLISHER_PROMOTION_ARTIFACT_STALE"
	}
	if len(inventory.Expired) != 0 {
		return "PUBLISHER_PROMOTION_ARTIFACT_EXPIRED"
	}
	if len(inventory.Unexpected) != 0 {
		return "PUBLISHER_PROMOTION_ARTIFACT_UNEXPECTED"
	}
	return "PUBLISHER_PROMOTION_ARTIFACT_MISSING"
}

func canonicalArtifactNames(values []string) []string {
	sort.Strings(values)
	return slices.Compact(values)
}

func publisherManifestArtifactName(sourceSHA string, attempt int) string {
	return fmt.Sprintf("env-vault-promotion-%s-attempt-%d", sourceSHA, attempt)
}

func expectedPublisherArtifactNames(sourceSHA string, attempt int, contract releasecontract.Contract) []string {
	names := []string{publisherManifestArtifactName(sourceSHA, attempt)}
	for _, platform := range contract.Platforms {
		names = append(names, fmt.Sprintf("env-vault-release-%s-attempt-%d", platform.ID, attempt))
	}
	sort.Strings(names)
	return names
}

func publisherArtifactCandidateLess(left, right publisherArtifactCandidate) bool {
	if left.Name != right.Name {
		return left.Name < right.Name
	}
	if left.ID != right.ID {
		return left.ID < right.ID
	}
	if left.RunID != right.RunID {
		return left.RunID < right.RunID
	}
	if left.SourceSHA != right.SourceSHA {
		return left.SourceSHA < right.SourceSHA
	}
	return !left.Expired && right.Expired
}

func canonicalPublisherRepairAction(repository, mode, reasonCode string, contract releasecontract.Contract) repairAction {
	if mode == "none" {
		return repairAction{Code: "none", ReasonCode: reasonCode}
	}
	codes := map[string]string{
		"release-assets": "dispatch_release_assets_repair",
		"homebrew":       "dispatch_homebrew_repair",
		"health":         "dispatch_health_repair",
	}
	code := codes[mode]
	if code == "" || !contractHasActionCode(contract, code) {
		return repairAction{Code: "none", ReasonCode: "publisher_repair_contract_invalid"}
	}
	return repairAction{
		Code: code, ReasonCode: reasonCode, Method: "POST",
		Endpoint: "repos/" + repository + "/actions/workflows/" + workflowFile(contract, "publisher") + "/dispatches",
	}
}

func contractHasActionCode(contract releasecontract.Contract, wanted string) bool {
	for _, code := range contract.ActionCodes {
		if code == wanted {
			return true
		}
	}
	return false
}

func publisherRepairReason(mode string) string {
	switch mode {
	case "release-assets":
		return "release_assets_repair_required"
	case "homebrew":
		return "homebrew_repair_required"
	case "health":
		return "health_repair_required"
	default:
		return "publisher_failure_not_repairable"
	}
}

func findEquivalentPublisherRepairRun(ctx context.Context, github githubGetter, plan publisherRepairPlan, contract releasecontract.Contract) (workflowRun, bool, error) {
	endpoint := "repos/" + plan.Repository + "/actions/workflows/" + workflowFile(contract, "publisher") + "/runs"
	var response publisherRunsResponse
	if err := github.Get(ctx, endpoint, map[string]string{"branch": plan.WorkflowRef, "event": "workflow_dispatch", "per_page": "100"}, &response); err != nil {
		return workflowRun{}, false, err
	}
	if response.WorkflowRuns == nil || response.TotalCount != len(response.WorkflowRuns) || response.TotalCount > 100 {
		return workflowRun{}, false, malformed(endpoint, "publisher dispatch inventory is absent or requires unsupported pagination")
	}
	title := publisherRepairRunTitle(plan.Version, plan.Inputs.Repair, plan.Inputs.StateDigest)
	matches := make([]workflowRun, 0, 1)
	for _, run := range response.WorkflowRuns {
		if run.DisplayTitle != title {
			continue
		}
		if err := validateRun(run, endpoint); err != nil {
			return workflowRun{}, false, err
		}
		if run.Event != "workflow_dispatch" || run.HeadBranch != plan.WorkflowRef || run.HeadSHA != plan.Preconditions.DispatchSHA || !workflowPathMatches(run.Path, workflowFile(contract, "publisher")) {
			return workflowRun{}, false, &observationError{code: "REMOTE_PRECONDITION_FAILED", operation: "publisher_repair_run_identity"}
		}
		matches = append(matches, run)
	}
	if len(matches) == 0 {
		return workflowRun{}, false, nil
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID > matches[j].ID })
	return matches[0], true, nil
}

func validatePublisherRepairPlan(plan publisherRepairPlan, suppliedDigest string, contract releasecontract.Contract) error {
	if !validHexDigest(suppliedDigest) || plan.PlanDigest != suppliedDigest || digestPublisherRepairPlan(plan) != suppliedDigest {
		return &observationError{code: "PLAN_DIGEST_MISMATCH", operation: "publisher_repair_plan_digest"}
	}
	if plan.Schema != contract.Schemas["repair_plan"] || plan.Kind != repairKindPublisher || !plan.OK || !plan.Applyable || !repositoryPattern.MatchString(plan.Repository) || plan.WorkflowRef != plan.Version || plan.GeneratedAt.IsZero() {
		return malformed("publisher_repair_plan", "publisher repair plan identity is malformed")
	}
	if err := validatePublisherRepairVersion(plan.Version, plan.SourceSHA, contract); err != nil {
		return err
	}
	if plan.Inputs.Version != plan.Version || plan.Inputs.Repair == "none" || plan.Inputs.StateDigest != plan.Preconditions.StateDigest || plan.Preconditions.TagSHA != plan.SourceSHA || plan.Preconditions.DispatchSHA != plan.SourceSHA || plan.Preconditions.StateDigest != digestPublisherRepairPreconditions(plan.Preconditions) {
		return malformed("publisher_repair_plan", "publisher repair inputs or preconditions are malformed")
	}
	wantWorkflow := publisherWorkflowSnapshot{Name: "build-binaries", Path: ".github/workflows/" + workflowFile(contract, "publisher"), State: "active"}
	if plan.Preconditions.Workflow.ID <= 0 || plan.Preconditions.Workflow.Name != wantWorkflow.Name || plan.Preconditions.Workflow.Path != wantWorkflow.Path || plan.Preconditions.Workflow.State != wantWorkflow.State {
		return malformed("publisher_repair_plan", "publisher workflow precondition is malformed")
	}
	if err := validatePublisherArtifactInventory(plan.Preconditions.Artifacts, plan.Preconditions.Status.Stages.MainCI.Run, contract); err != nil {
		return err
	}
	mode, reasonCode, applyable := classifyPublisherRepair(plan.Preconditions.Status, plan.Preconditions.Artifacts)
	wantAction := canonicalPublisherRepairAction(plan.Repository, mode, reasonCode, contract)
	if !applyable || mode != plan.Inputs.Repair || plan.Action != wantAction {
		return malformed("publisher_repair_plan", "publisher action is not canonically derived from release state")
	}
	return nil
}

func validatePublisherArtifactInventory(inventory publisherArtifactInventory, mainRun *runEvidence, contract releasecontract.Contract) error {
	if mainRun == nil || mainRun.ID <= 0 || mainRun.Attempt <= 0 || !shaPattern.MatchString(mainRun.HeadSHA) ||
		inventory.RunID != mainRun.ID || inventory.Attempt != mainRun.Attempt || inventory.SourceSHA != mainRun.HeadSHA {
		return malformed("publisher_repair_plan", "publisher artifact inventory identity is malformed")
	}
	if inventory.Candidates == nil {
		return malformed("publisher_repair_plan", "publisher artifact candidates are absent")
	}
	raw := make([]workflowArtifact, 0, len(inventory.Candidates))
	for index, candidate := range inventory.Candidates {
		if candidate.ID <= 0 || candidate.Name == "" || index > 0 && publisherArtifactCandidateLess(candidate, inventory.Candidates[index-1]) {
			return malformed("publisher_repair_plan", "publisher artifact candidates are malformed or not canonical")
		}
		artifact := workflowArtifact{ID: candidate.ID, Name: candidate.Name, Expired: candidate.Expired}
		artifact.WorkflowRun.ID = candidate.RunID
		artifact.WorkflowRun.HeadSHA = candidate.SourceSHA
		raw = append(raw, artifact)
	}
	if !slices.Equal(inventory.Expected, expectedPublisherArtifactNames(mainRun.HeadSHA, mainRun.Attempt, contract)) {
		return malformed("publisher_repair_plan", "publisher artifact inventory expected set is malformed")
	}
	for _, values := range [][]string{inventory.Expected, inventory.Observed, inventory.Missing, inventory.Unexpected, inventory.Expired, inventory.Duplicates, inventory.Stale, inventory.WrongRun} {
		if values == nil || !sort.StringsAreSorted(values) || len(slices.Compact(append([]string(nil), values...))) != len(values) {
			return malformed("publisher_repair_plan", "publisher artifact inventory lists are not canonical")
		}
		for _, value := range values {
			if value == "" {
				return malformed("publisher_repair_plan", "publisher artifact inventory contains an empty name")
			}
		}
	}
	for _, name := range inventory.Expected {
		observed := slices.Contains(inventory.Observed, name)
		missing := slices.Contains(inventory.Missing, name)
		if observed == missing {
			return malformed("publisher_repair_plan", "publisher artifact inventory does not partition the expected set")
		}
	}
	for _, name := range append(append([]string(nil), inventory.Observed...), inventory.Missing...) {
		if !slices.Contains(inventory.Expected, name) {
			return malformed("publisher_repair_plan", "publisher artifact inventory contains an unknown expected name")
		}
	}
	complete := len(contract.Platforms) == 5 && len(inventory.Expected) == 6 &&
		len(inventory.Missing) == 0 && len(inventory.Unexpected) == 0 && len(inventory.Expired) == 0 &&
		len(inventory.Duplicates) == 0 && len(inventory.Stale) == 0 && len(inventory.WrongRun) == 0
	canonical := inventory
	canonical.Complete = complete
	canonical.ReasonCode = publisherArtifactInventoryReason(canonical, publisherManifestArtifactName(mainRun.HeadSHA, mainRun.Attempt))
	if inventory.Complete != complete || inventory.ReasonCode != canonical.ReasonCode {
		return malformed("publisher_repair_plan", "publisher artifact inventory result is not canonical")
	}
	recomputed := classifyPublisherArtifactInventory(workflowRun{ID: mainRun.ID, RunAttempt: mainRun.Attempt, HeadSHA: mainRun.HeadSHA}, raw, contract)
	if !reflect.DeepEqual(recomputed, inventory) {
		return malformed("publisher_repair_plan", "publisher artifact inventory is not derived from its exact candidates")
	}
	return nil
}

func validatePublisherRepairVersion(version, sourceSHA string, contract releasecontract.Contract) error {
	if !releasecontract.IsVersion(version) || !shaPattern.MatchString(sourceSHA) {
		return &observationError{code: "INPUT_INVALID", operation: "publisher_repair_identity"}
	}
	if _, legacy := contract.LegacyVersion(version); legacy {
		return &observationError{code: "LEGACY_REBUILD_UNSUPPORTED", operation: "publisher_repair_legacy_version"}
	}
	for _, blocked := range contract.VersionPolicy.BlockedVersions {
		if blocked.Version == version {
			return &observationError{code: "REMOTE_PRECONDITION_FAILED", operation: "publisher_repair_blocked_version"}
		}
	}
	return nil
}

func publisherRepairRunTitle(version, mode, stateDigest string) string {
	return "env-vault-publication event=workflow_dispatch version=" + version + " repair=" + mode + " state=" + stateDigest
}

func digestPublisherRepairPlan(plan publisherRepairPlan) string {
	return digestJSON(plan, func(value *publisherRepairPlan) { value.PlanDigest = "" })
}

func digestPublisherRepairPreconditions(preconditions publisherRepairPreconditions) string {
	return digestJSON(preconditions, func(value *publisherRepairPreconditions) { value.StateDigest = "" })
}
