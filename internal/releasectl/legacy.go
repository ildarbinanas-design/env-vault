package releasectl

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

const (
	legacyRebuildPlanSchema  = "env-vault.legacy-rebuild-plan.v1"
	legacyRebuildApplySchema = "env-vault.legacy-rebuild-apply.v1"
	legacyWorkflowRef        = "main"
	legacyDispatchPolls      = 5
	legacyDispatchPollDelay  = time.Second
)

type legacyAssetSnapshot struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

type legacyReleaseSnapshot struct {
	Exists          bool                  `json:"exists"`
	ID              int64                 `json:"id"`
	TagName         string                `json:"tag_name"`
	TargetCommitish string                `json:"target_commitish"`
	Draft           bool                  `json:"draft"`
	Prerelease      bool                  `json:"prerelease"`
	Immutable       bool                  `json:"immutable"`
	Assets          []legacyAssetSnapshot `json:"assets"`
}

type legacyWorkflowSnapshot struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	State string `json:"state"`
}

type legacyRemotePreconditions struct {
	TagSHA      string                 `json:"tag_sha"`
	ControlSHA  string                 `json:"control_sha"`
	Workflow    legacyWorkflowSnapshot `json:"workflow"`
	Release     legacyReleaseSnapshot  `json:"release"`
	StateDigest string                 `json:"state_digest"`
}

type legacyBuildScope struct {
	Kind                string   `json:"kind"`
	ReasonCode          string   `json:"reason_code"`
	Toolchain           string   `json:"toolchain"`
	Platforms           []string `json:"platforms"`
	PublicationEligible bool     `json:"publication_eligible"`
	Limitations         []string `json:"limitations"`
}

type legacyRebuildPlan struct {
	Schema        string                    `json:"schema"`
	OK            bool                      `json:"ok"`
	GeneratedAt   time.Time                 `json:"generated_at"`
	PlanDigest    string                    `json:"plan_digest"`
	Repository    string                    `json:"repository"`
	Version       string                    `json:"version"`
	SourceSHA     string                    `json:"source_sha"`
	WorkflowRef   string                    `json:"workflow_ref"`
	Preconditions legacyRemotePreconditions `json:"preconditions"`
	BuildScope    legacyBuildScope          `json:"build_scope"`
	Action        repairAction              `json:"action"`
	Applyable     bool                      `json:"applyable"`
}

type legacyRebuildApplyDocument struct {
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

type legacyReleaseAPI struct {
	ID              int64                 `json:"id"`
	TagName         string                `json:"tag_name"`
	TargetCommitish string                `json:"target_commitish"`
	Draft           bool                  `json:"draft"`
	Prerelease      bool                  `json:"prerelease"`
	Immutable       bool                  `json:"immutable"`
	Assets          []legacyAssetSnapshot `json:"assets"`
}

type legacyWorkflowAPI struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	State string `json:"state"`
}

func runLegacyRebuildCommand(args []string, stdout, stderr io.Writer, deps dependencies) int {
	if len(args) == 0 || (args[0] != "plan" && args[0] != "apply") {
		return usage(stderr, "usage: releasectl release legacy-rebuild <plan|apply> [flags] --json")
	}
	if args[0] == "plan" {
		return runLegacyRebuildPlan(args[1:], stdout, stderr, deps)
	}
	return runLegacyRebuildApply(args[1:], stdout, stderr, deps)
}

func runLegacyRebuildPlan(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("releasectl release legacy-rebuild plan", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	repository := flags.String("repo", defaultRepository, "GitHub repository")
	version := flags.String("version", "", "legacy release version")
	sourceSHA := flags.String("source-sha", "", "exact tag commit SHA")
	contractPath := flags.String("contract", releasecontract.CanonicalPath, "release contract")
	jsonOutput := flags.Bool("json", false, "write one versioned JSON document")
	timeout := flags.Duration("timeout", defaultStatusTimeout, "observation timeout")
	if err := flags.Parse(args); err != nil {
		return usage(stderr, err.Error())
	}
	if flags.NArg() != 0 || !*jsonOutput || !repositoryPattern.MatchString(*repository) || !shaPattern.MatchString(*sourceSHA) || *timeout <= 0 {
		return usage(stderr, "legacy-rebuild plan requires --repo OWNER/REPO --version v0.0.1..v0.0.7 --source-sha SHA --json and a positive timeout")
	}
	contract, err := loadOperatorContract(*contractPath)
	if err != nil {
		return writeCommandError(stdout, legacyRebuildPlanSchema, "CONTRACT_INVALID", "release_contract", exitUsage)
	}
	if _, supported := contract.LegacyVersion(*version); !supported {
		return writeCommandError(stdout, contract.Schemas["legacy_rebuild_plan"], "LEGACY_REBUILD_UNSUPPORTED", "legacy_version", exitMutationBlocked)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	plan, err := planLegacyRebuild(ctx, *repository, *version, *sourceSHA, contract, deps.github, deps.clock)
	if err != nil {
		return writeCommandAPIError(stdout, contract.Schemas["legacy_rebuild_plan"], err)
	}
	if writeAnyJSON(stdout, plan) != nil {
		return exitObservationError
	}
	return exitSuccess
}

func runLegacyRebuildApply(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("releasectl release legacy-rebuild apply", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	planPath := flags.String("plan", "", "versioned legacy rebuild plan")
	planDigest := flags.String("plan-digest", "", "exact plan SHA-256")
	apply := flags.Bool("apply", false, "dispatch the build-only workflow")
	contractPath := flags.String("contract", releasecontract.CanonicalPath, "release contract")
	jsonOutput := flags.Bool("json", false, "write one versioned JSON document")
	timeout := flags.Duration("timeout", defaultStatusTimeout, "observation timeout")
	if err := flags.Parse(args); err != nil {
		return usage(stderr, err.Error())
	}
	if flags.NArg() != 0 || !*jsonOutput || *planPath == "" || *planDigest == "" || *timeout <= 0 {
		return usage(stderr, "legacy-rebuild apply requires --plan FILE --plan-digest SHA256 --json; --apply opts in to dispatch")
	}
	contract, err := loadOperatorContract(*contractPath)
	if err != nil {
		return writeCommandError(stdout, legacyRebuildApplySchema, "CONTRACT_INVALID", "release_contract", exitUsage)
	}
	plan, err := readLegacyRebuildPlan(*planPath)
	if err != nil {
		return writeCommandAPIError(stdout, contract.Schemas["legacy_rebuild_apply"], err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	doc, code := applyLegacyRebuild(ctx, plan, *planDigest, *apply, contract, deps.github, deps.mutator, deps.clock)
	if writeAnyJSON(stdout, doc) != nil {
		return exitObservationError
	}
	return code
}

func planLegacyRebuild(ctx context.Context, repository, version, sourceSHA string, contract releasecontract.Contract, github githubGetter, clk clock) (legacyRebuildPlan, error) {
	if !repositoryPattern.MatchString(repository) || !shaPattern.MatchString(sourceSHA) {
		return legacyRebuildPlan{}, &observationError{code: "INPUT_INVALID", operation: "legacy_rebuild_identity"}
	}
	legacyVersion, supported := contract.LegacyVersion(version)
	if !supported {
		return legacyRebuildPlan{}, &observationError{code: "LEGACY_REBUILD_UNSUPPORTED", operation: "legacy_version"}
	}
	if legacyVersion.TagSHA != sourceSHA {
		return legacyRebuildPlan{}, &observationError{code: "REMOTE_PRECONDITION_FAILED", operation: "legacy_contract_tag_source_sha"}
	}
	preconditions, err := observeLegacyPreconditions(ctx, repository, version, sourceSHA, contract, github)
	if err != nil {
		return legacyRebuildPlan{}, err
	}
	workflow := workflowFile(contract, "legacy_rebuild")
	platforms := make([]string, 0, len(contract.Platforms))
	for _, platform := range contract.Platforms {
		platforms = append(platforms, platform.ID)
	}
	sort.Strings(platforms)
	limitations := []string{"historical_e2e_contract_absent", "historical_exact_build_toolchain_unrecorded"}
	if !legacyVersion.LiteralVersionSupported {
		limitations = append(limitations, "literal_version_flag_absent")
	}
	plan := legacyRebuildPlan{
		Schema: contract.Schemas["legacy_rebuild_plan"], OK: true, GeneratedAt: clk.Now().UTC(),
		Repository: repository, Version: version, SourceSHA: sourceSHA, WorkflowRef: legacyWorkflowRef,
		Preconditions: preconditions,
		BuildScope: legacyBuildScope{
			Kind: "diagnostic_build_only", ReasonCode: "LEGACY_PROMOTION_PROOF_UNAVAILABLE",
			Toolchain: "go" + contract.VersionPolicy.LegacyRebuild.GoVersion, Platforms: platforms,
			PublicationEligible: false, Limitations: limitations,
		},
		Action: repairAction{
			Code: "dispatch_legacy_rebuild", ReasonCode: "legacy_diagnostic_rebuild_requested", Method: "POST",
			Endpoint: "repos/" + repository + "/actions/workflows/" + workflow + "/dispatches",
		},
		Applyable: true,
	}
	plan.PlanDigest = digestLegacyRebuildPlan(plan)
	return plan, nil
}

func applyLegacyRebuild(ctx context.Context, plan legacyRebuildPlan, suppliedDigest string, apply bool, contract releasecontract.Contract, github githubGetter, mutator githubMutator, clk clock) (legacyRebuildApplyDocument, int) {
	result := legacyRebuildApplyDocument{
		Schema: contract.Schemas["legacy_rebuild_apply"], ObservedAt: clk.Now().UTC(), PlanDigest: suppliedDigest,
		DryRun: !apply, Status: "blocked", Action: plan.Action, Version: plan.Version, SourceSHA: plan.SourceSHA,
	}
	if err := validateLegacyRebuildPlan(plan, suppliedDigest, contract); err != nil {
		result.Error = operatorErrorInfo(err)
		return result, exitPreconditionFailed
	}
	current, err := observeLegacyPreconditions(ctx, plan.Repository, plan.Version, plan.SourceSHA, contract, github)
	if err != nil {
		result.Error = operatorErrorInfo(err)
		if result.Error.Code == "REMOTE_PRECONDITION_FAILED" {
			return result, exitPreconditionFailed
		}
		return result, exitObservationError
	}
	if current.StateDigest != plan.Preconditions.StateDigest || digestLegacyPreconditions(current) != digestLegacyPreconditions(plan.Preconditions) {
		result.Error = &errorInfo{Code: "REMOTE_PRECONDITION_FAILED", Operation: "legacy_remote_state"}
		return result, exitPreconditionFailed
	}
	existing, found, err := findLegacyRebuildRun(ctx, github, plan)
	if err != nil {
		result.Error = operatorErrorInfo(err)
		return result, exitObservationError
	}
	if found {
		result.OK, result.Status, result.WorkflowRun = true, "already_applied", evidenceFromRun(existing)
		return result, exitSuccess
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
			"version": plan.Version, "source_sha": plan.SourceSHA,
			"control_sha": plan.Preconditions.ControlSHA, "plan_digest": plan.PlanDigest,
			"release_state_digest": plan.Preconditions.StateDigest,
		},
	}
	if err := mutator.Mutate(ctx, "POST", plan.Action.Endpoint, body, nil); err != nil {
		result.Error = operatorErrorInfo(err)
		return result, exitObservationError
	}
	// workflow_dispatch does not return a run id. Observe the exact run name that
	// binds the plan digest before reporting success. This also makes two callers
	// that raced before either dispatch became visible converge on the same
	// machine-readable workflow identity.
	for poll := 0; poll < legacyDispatchPolls; poll++ {
		dispatched, found, observationErr := findLegacyRebuildRun(ctx, github, plan)
		if observationErr != nil {
			result.Error = operatorErrorInfo(observationErr)
			return result, exitObservationError
		}
		if found {
			result.OK, result.Status, result.WorkflowRun = true, "applied", evidenceFromRun(dispatched)
			return result, exitSuccess
		}
		if poll < legacyDispatchPolls-1 {
			if sleepErr := clk.Sleep(ctx, legacyDispatchPollDelay); sleepErr != nil {
				result.Error = operatorErrorInfo(sleepErr)
				return result, exitObservationError
			}
		}
	}
	result.Error = &errorInfo{Code: "REMOTE_STATE_UNKNOWN", Operation: "legacy_dispatch_not_observed", Retryable: true}
	return result, exitObservationError
}

func observeLegacyPreconditions(ctx context.Context, repository, version, sourceSHA string, contract releasecontract.Contract, github githubGetter) (legacyRemotePreconditions, error) {
	resolved, found, err := (collector{github: github}).resolveTag(ctx, repository, version)
	if err != nil {
		return legacyRemotePreconditions{}, err
	}
	if !found {
		return legacyRemotePreconditions{}, &observationError{code: "NOT_FOUND", operation: "legacy_release_tag"}
	}
	if resolved != sourceSHA {
		return legacyRemotePreconditions{}, &observationError{code: "REMOTE_PRECONDITION_FAILED", operation: "legacy_tag_source_sha"}
	}

	controlEndpoint := "repos/" + repository + "/git/ref/heads/" + legacyWorkflowRef
	var control tagRefResponse
	if err := github.Get(ctx, controlEndpoint, nil, &control); err != nil {
		return legacyRemotePreconditions{}, err
	}
	if control.Object.Type != "commit" || !shaPattern.MatchString(control.Object.SHA) {
		return legacyRemotePreconditions{}, malformed(controlEndpoint, "control ref is not an exact commit")
	}

	workflowName := workflowFile(contract, "legacy_rebuild")
	workflowEndpoint := "repos/" + repository + "/actions/workflows/" + workflowName
	var workflow legacyWorkflowAPI
	if err := github.Get(ctx, workflowEndpoint, nil, &workflow); err != nil {
		return legacyRemotePreconditions{}, err
	}
	wantPath := ".github/workflows/" + workflowName
	if workflow.ID <= 0 || workflow.Name != "legacy-rebuild" || workflow.Path != wantPath || workflow.State != "active" {
		return legacyRemotePreconditions{}, malformed(workflowEndpoint, "legacy workflow identity or state is malformed")
	}

	releaseEndpoint := "repos/" + repository + "/releases/tags/" + version
	var remote legacyReleaseAPI
	if err := github.Get(ctx, releaseEndpoint, nil, &remote); err != nil {
		if isNotFound(err) {
			return legacyRemotePreconditions{}, &observationError{code: "REMOTE_PRECONDITION_FAILED", operation: "legacy_release_assets", cause: err}
		}
		return legacyRemotePreconditions{}, err
	}
	assets, err := validateLegacyAssets(releaseEndpoint, remote, version, contract.Assets)
	if err != nil {
		return legacyRemotePreconditions{}, err
	}
	preconditions := legacyRemotePreconditions{
		TagSHA: resolved, ControlSHA: control.Object.SHA,
		Workflow: legacyWorkflowSnapshot{ID: workflow.ID, Name: workflow.Name, Path: workflow.Path, State: workflow.State},
		Release: legacyReleaseSnapshot{
			Exists: true, ID: remote.ID, TagName: remote.TagName, TargetCommitish: remote.TargetCommitish,
			Draft: remote.Draft, Prerelease: remote.Prerelease, Immutable: remote.Immutable, Assets: assets,
		},
	}
	preconditions.StateDigest = digestLegacyPreconditions(preconditions)
	return preconditions, nil
}

func validateLegacyAssets(operation string, remote legacyReleaseAPI, version string, expected []string) ([]legacyAssetSnapshot, error) {
	if remote.ID <= 0 || remote.TagName != version || remote.TargetCommitish == "" || remote.Draft || remote.Prerelease || remote.Assets == nil {
		return nil, malformed(operation, "legacy release identity or state is malformed")
	}
	assets := append([]legacyAssetSnapshot(nil), remote.Assets...)
	sort.Slice(assets, func(i, j int) bool {
		if assets[i].Name == assets[j].Name {
			return assets[i].ID < assets[j].ID
		}
		return assets[i].Name < assets[j].Name
	})
	want := append([]string(nil), expected...)
	sort.Strings(want)
	if len(assets) != len(want) {
		return nil, malformed(operation, "legacy release asset count is not canonical")
	}
	for index, asset := range assets {
		if asset.ID <= 0 || asset.Name != want[index] || asset.Size <= 0 || !validSHA256Digest(asset.Digest) {
			return nil, malformed(operation, "legacy release asset identity, size, name, or digest is malformed")
		}
	}
	return assets, nil
}

func findLegacyRebuildRun(ctx context.Context, github githubGetter, plan legacyRebuildPlan) (workflowRun, bool, error) {
	endpoint := "repos/" + plan.Repository + "/actions/workflows/" + workflowFileName(plan.Action.Endpoint) + "/runs"
	var response workflowRunsResponse
	if err := github.Get(ctx, endpoint, map[string]string{"branch": plan.WorkflowRef, "event": "workflow_dispatch", "per_page": "100"}, &response); err != nil {
		return workflowRun{}, false, err
	}
	if response.WorkflowRuns == nil || response.TotalCount < 0 || response.TotalCount != len(response.WorkflowRuns) || response.TotalCount > 100 {
		return workflowRun{}, false, malformed(endpoint, "legacy dispatch inventory is missing or requires unsupported pagination")
	}
	title := legacyRunTitle(plan)
	matches := make([]workflowRun, 0, 1)
	for _, run := range response.WorkflowRuns {
		if run.DisplayTitle != title {
			continue
		}
		if err := validateRun(run, endpoint); err != nil {
			return workflowRun{}, false, err
		}
		if run.Event != "workflow_dispatch" || run.HeadBranch != plan.WorkflowRef || run.HeadSHA != plan.Preconditions.ControlSHA || !workflowPathMatches(run.Path, workflowFileName(plan.Action.Endpoint)) {
			return workflowRun{}, false, &observationError{code: "REMOTE_PRECONDITION_FAILED", operation: "legacy_rebuild_run_identity"}
		}
		matches = append(matches, run)
	}
	if len(matches) == 0 {
		return workflowRun{}, false, nil
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].ID > matches[j].ID })
	return matches[0], true, nil
}

func validateLegacyRebuildPlan(plan legacyRebuildPlan, suppliedDigest string, contract releasecontract.Contract) error {
	if !validHexDigest(suppliedDigest) || plan.PlanDigest != suppliedDigest || digestLegacyRebuildPlan(plan) != suppliedDigest {
		return &observationError{code: "PLAN_DIGEST_MISMATCH", operation: "legacy_rebuild_plan_digest"}
	}
	legacyVersion, supported := contract.LegacyVersion(plan.Version)
	if plan.Schema != contract.Schemas["legacy_rebuild_plan"] || !plan.OK || !plan.Applyable || !repositoryPattern.MatchString(plan.Repository) || !supported || !shaPattern.MatchString(plan.SourceSHA) || plan.WorkflowRef != legacyWorkflowRef {
		return malformed("legacy_rebuild_plan", "plan identity is malformed")
	}
	if legacyVersion.TagSHA != plan.SourceSHA {
		return malformed("legacy_rebuild_plan", "source SHA is not the declarative legacy tag SHA")
	}
	wantEndpoint := "repos/" + plan.Repository + "/actions/workflows/" + workflowFile(contract, "legacy_rebuild") + "/dispatches"
	if plan.Action.Code != "dispatch_legacy_rebuild" || plan.Action.Method != "POST" || plan.Action.Endpoint != wantEndpoint {
		return malformed("legacy_rebuild_plan", "mutation action is not the bounded legacy dispatch")
	}
	if plan.Preconditions.TagSHA != plan.SourceSHA || plan.Preconditions.ControlSHA == "" || !shaPattern.MatchString(plan.Preconditions.ControlSHA) || plan.Preconditions.StateDigest != digestLegacyPreconditions(plan.Preconditions) {
		return malformed("legacy_rebuild_plan", "remote preconditions are malformed")
	}
	wantPlatforms := make([]string, 0, len(contract.Platforms))
	for _, platform := range contract.Platforms {
		wantPlatforms = append(wantPlatforms, platform.ID)
	}
	sort.Strings(wantPlatforms)
	if plan.BuildScope.Kind != "diagnostic_build_only" || plan.BuildScope.ReasonCode != "LEGACY_PROMOTION_PROOF_UNAVAILABLE" || plan.BuildScope.Toolchain != "go"+contract.VersionPolicy.LegacyRebuild.GoVersion || plan.BuildScope.PublicationEligible != contract.VersionPolicy.LegacyRebuild.PublicationEligible || plan.BuildScope.PublicationEligible || !equalStrings(plan.BuildScope.Platforms, wantPlatforms) || !containsString(plan.BuildScope.Limitations, "historical_e2e_contract_absent") || !containsString(plan.BuildScope.Limitations, "historical_exact_build_toolchain_unrecorded") {
		return malformed("legacy_rebuild_plan", "diagnostic-only build scope is malformed")
	}
	return nil
}

func readLegacyRebuildPlan(filename string) (legacyRebuildPlan, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return legacyRebuildPlan{}, &observationError{code: "INPUT_INVALID", operation: "legacy_rebuild_plan_file", cause: err}
	}
	if len(data) == 0 || len(data) > maxPlanBytes {
		return legacyRebuildPlan{}, &observationError{code: "INPUT_INVALID", operation: "legacy_rebuild_plan_file"}
	}
	var plan legacyRebuildPlan
	if err := decodeStrictJSON(data, &plan); err != nil {
		return legacyRebuildPlan{}, &observationError{code: "INPUT_INVALID", operation: "legacy_rebuild_plan_file", cause: err}
	}
	return plan, nil
}

func digestLegacyRebuildPlan(plan legacyRebuildPlan) string {
	return digestJSON(plan, func(value *legacyRebuildPlan) { value.PlanDigest = "" })
}

func digestLegacyPreconditions(preconditions legacyRemotePreconditions) string {
	return digestJSON(preconditions, func(value *legacyRemotePreconditions) { value.StateDigest = "" })
}

func legacyRunTitle(plan legacyRebuildPlan) string {
	return "legacy-rebuild version=" + plan.Version + " source=" + plan.SourceSHA + " plan=" + plan.PlanDigest
}

func workflowFileName(endpoint string) string {
	trimmed := strings.TrimSuffix(endpoint, "/dispatches")
	return trimmed[strings.LastIndex(trimmed, "/")+1:]
}

func validHexDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validSHA256Digest(value string) bool {
	algorithm, digest, ok := strings.Cut(value, ":")
	return ok && algorithm == "sha256" && validHexDigest(digest)
}
