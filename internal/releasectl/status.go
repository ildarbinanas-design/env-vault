package releasectl

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

const (
	statusSchema      = "env-vault.release-status.v1"
	defaultRepository = "ildarbinanas-design/env-vault"
)

var (
	repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	shaPattern        = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type stageState string

const (
	stateNotStarted   stageState = "not_started"
	stateWaiting      stageState = "waiting"
	stateQueued       stageState = "queued"
	stateInProgress   stageState = "in_progress"
	stateSucceeded    stageState = "succeeded"
	stateFailed       stageState = "failed"
	stateCancelled    stageState = "cancelled"
	stateSkipped      stageState = "skipped"
	stateBlocked      stageState = "blocked"
	stateInconsistent stageState = "inconsistent"
)

type query struct {
	Repository string `json:"repository"`
	Version    string `json:"version"`
	SourceSHA  string `json:"source_sha,omitempty"`
}

type document struct {
	Schema        string         `json:"schema"`
	OK            bool           `json:"ok"`
	ObservedAt    time.Time      `json:"observed_at"`
	Query         query          `json:"query"`
	Overall       *overall       `json:"overall,omitempty"`
	Identity      *identity      `json:"identity,omitempty"`
	Stages        *stages        `json:"stages,omitempty"`
	FailedJobs    []failedJob    `json:"failed_jobs,omitempty"`
	NextAction    *nextAction    `json:"next_action,omitempty"`
	Watch         *watchInfo     `json:"watch,omitempty"`
	AttemptMatrix *attemptMatrix `json:"attempt_matrix,omitempty"`
	RepairRuns    []runEvidence  `json:"manual_repair_runs,omitempty"`
	Error         *errorInfo     `json:"error,omitempty"`
}

type overall struct {
	State    string `json:"state"`
	Terminal bool   `json:"terminal"`
	Reason   string `json:"reason"`
}

type identity struct {
	SourceSHA string `json:"source_sha,omitempty"`
	Source    string `json:"source,omitempty"`
}

type stages struct {
	Tag           stage `json:"tag"`
	MainCI        stage `json:"main_ci"`
	Planning      stage `json:"planning"`
	Publisher     stage `json:"publisher"`
	GitHubRelease stage `json:"github_release"`
	SupplyChain   stage `json:"supply_chain"`
	Homebrew      stage `json:"homebrew"`
	Health        stage `json:"health"`
}

type stage struct {
	State        stageState           `json:"state"`
	Reason       string               `json:"reason,omitempty"`
	SHA          string               `json:"sha,omitempty"`
	Run          *runEvidence         `json:"run,omitempty"`
	Job          *jobEvidence         `json:"job,omitempty"`
	Release      *releaseEvidence     `json:"release,omitempty"`
	Attestations *attestationEvidence `json:"attestations,omitempty"`
	Homebrew     *homebrewEvidence    `json:"homebrew,omitempty"`
}

type runEvidence struct {
	ID           int64  `json:"id"`
	Attempt      int    `json:"attempt"`
	Event        string `json:"event"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion,omitempty"`
	HeadBranch   string `json:"head_branch"`
	HeadSHA      string `json:"head_sha"`
	WorkflowPath string `json:"workflow_path,omitempty"`
	RepairMode   string `json:"repair_mode,omitempty"`
	URL          string `json:"url"`
}

type jobEvidence struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion,omitempty"`
	URL        string `json:"url"`
}

type releaseEvidence struct {
	TagName    string        `json:"tag_name"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	URL        string        `json:"url"`
	Assets     assetEvidence `json:"assets"`
}

type assetEvidence struct {
	ExpectedCount  int                    `json:"expected_count"`
	ObservedCount  int                    `json:"observed_count"`
	Missing        []string               `json:"missing"`
	Unexpected     []string               `json:"unexpected"`
	Duplicates     []string               `json:"duplicates"`
	DigestComplete bool                   `json:"digest_complete"`
	Digests        []releaseAssetEvidence `json:"digests"`
}

type failedJob struct {
	ID          int64        `json:"id"`
	Name        string       `json:"name"`
	Conclusion  string       `json:"conclusion"`
	URL         string       `json:"url"`
	FailedSteps []failedStep `json:"failed_steps"`
}

type failedStep struct {
	Number     int    `json:"number"`
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
}

type watchInfo struct {
	Polls          int   `json:"polls"`
	ElapsedSeconds int64 `json:"elapsed_seconds"`
	TimedOut       bool  `json:"timed_out"`
}

type errorInfo struct {
	Code       string `json:"code"`
	Operation  string `json:"operation"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Retryable  bool   `json:"retryable"`
}

type nextAction struct {
	Code string `json:"code"`
}

type observationError struct {
	code      string
	operation string
	cause     error
}

func (e *observationError) Error() string { return e.code + ": " + e.operation }
func (e *observationError) Unwrap() error { return e.cause }

type clock interface {
	Now() time.Time
	Sleep(context.Context, time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now().UTC() }
func (realClock) Sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type collector struct {
	github   githubGetter
	clock    clock
	contract releasecontract.Contract
}

type tagRefResponse struct {
	Object gitObject `json:"object"`
}

type repositoryResponse struct {
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
}

type annotatedTagResponse struct {
	Object gitObject `json:"object"`
}

type gitObject struct {
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

type workflowRunsResponse struct {
	TotalCount   int           `json:"total_count"`
	WorkflowRuns []workflowRun `json:"workflow_runs"`
}

type workflowRun struct {
	ID           int64     `json:"id"`
	RunAttempt   int       `json:"run_attempt"`
	Event        string    `json:"event"`
	Status       string    `json:"status"`
	Conclusion   string    `json:"conclusion"`
	HeadBranch   string    `json:"head_branch"`
	HeadSHA      string    `json:"head_sha"`
	Path         string    `json:"path"`
	HTMLURL      string    `json:"html_url"`
	DisplayTitle string    `json:"display_title"`
	CreatedAt    time.Time `json:"created_at"`
	RunStartedAt time.Time `json:"run_started_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type jobsResponse struct {
	TotalCount int           `json:"total_count"`
	Jobs       []workflowJob `json:"jobs"`
}

type workflowJob struct {
	ID          int64          `json:"id"`
	Name        string         `json:"name"`
	Status      string         `json:"status"`
	Conclusion  string         `json:"conclusion"`
	HTMLURL     string         `json:"html_url"`
	RunAttempt  int            `json:"run_attempt"`
	StartedAt   time.Time      `json:"started_at"`
	CompletedAt time.Time      `json:"completed_at"`
	Steps       []workflowStep `json:"steps"`
}

type workflowStep struct {
	Number      int       `json:"number"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

type releaseResponse struct {
	TagName    string         `json:"tag_name"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	HTMLURL    string         `json:"html_url"`
	Assets     []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	State  string `json:"state"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

func (c collector) snapshot(ctx context.Context, request query) (document, error) {
	contract, err := c.releaseDefinition()
	if err != nil {
		return document{}, err
	}
	schema := contract.Schemas["release_status"]
	if schema == "" {
		return document{}, &observationError{code: "CONTRACT_INVALID", operation: releasecontract.CanonicalPath, cause: errors.New("release_status schema is missing")}
	}
	doc := document{Schema: schema, OK: true, ObservedAt: c.clock.Now().UTC(), Query: request}
	resultStages := stages{
		Tag:           stage{State: stateWaiting, Reason: "tag_not_found"},
		MainCI:        stage{State: stateNotStarted, Reason: "source_sha_unknown"},
		Planning:      stage{State: stateNotStarted, Reason: "source_sha_unknown"},
		Publisher:     stage{State: stateNotStarted, Reason: "tag_not_found"},
		GitHubRelease: stage{State: stateNotStarted, Reason: "tag_not_found"},
		SupplyChain:   stage{State: stateNotStarted, Reason: "publisher_not_started"},
		Homebrew:      stage{State: stateNotStarted, Reason: "publisher_not_started"},
		Health:        stage{State: stateNotStarted, Reason: "publisher_not_started"},
	}

	tagSHA, tagFound, err := c.resolveTag(ctx, request.Repository, request.Version)
	if err != nil {
		return document{}, err
	}
	sourceSHA := request.SourceSHA
	sourceKind := ""
	if sourceSHA != "" {
		sourceKind = "requested"
	}
	if sourceSHA == "" && tagFound {
		sourceSHA = tagSHA
		sourceKind = "tag"
	}
	if tagFound {
		resultStages.Tag = stage{State: stateSucceeded, SHA: tagSHA}
		if sourceSHA != "" && tagSHA != sourceSHA {
			resultStages.Tag = stage{State: stateInconsistent, Reason: "tag_source_sha_mismatch", SHA: tagSHA}
		}
	}
	doc.Identity = &identity{SourceSHA: sourceSHA, Source: sourceKind}

	var ciRun workflowRun
	var ciFound bool
	var repairRuns []workflowRun
	if sourceSHA != "" {
		ciRun, ciFound, err = c.findRun(ctx, request.Repository, workflowFile(contract, "ci"), map[string]string{
			"branch": "main", "event": "push", "head_sha": sourceSHA, "per_page": "100",
		}, func(run workflowRun) bool {
			return run.Event == "push" && run.HeadBranch == "main" && run.HeadSHA == sourceSHA
		})
		if err != nil {
			return document{}, err
		}
		if ciFound {
			resultStages.MainCI, err = stageFromRun(ciRun)
			if err != nil {
				return document{}, err
			}
			artifacts, artifactErr := getWorkflowArtifacts(ctx, c.github, request.Repository, ciRun.ID)
			if artifactErr != nil {
				return document{}, artifactErr
			}
			matrix := classifyAttemptMatrix(ciRun, artifacts, contract)
			doc.AttemptMatrix = &matrix
		} else {
			resultStages.MainCI = stage{State: stateWaiting, Reason: "main_ci_not_found"}
		}

		planningRun, found, err := c.findRun(ctx, request.Repository, workflowFile(contract, "planning"), map[string]string{
			"branch": "main", "event": "workflow_run", "head_sha": sourceSHA, "per_page": "100",
		}, func(run workflowRun) bool {
			return run.Event == "workflow_run" && run.HeadBranch == "main" && run.HeadSHA == sourceSHA
		})
		if err != nil {
			return document{}, err
		}
		if found {
			resultStages.Planning, err = stageFromRun(planningRun)
			if err != nil {
				return document{}, err
			}
		} else {
			resultStages.Planning = stage{State: stateWaiting, Reason: "planning_run_not_found"}
		}
		if resultStages.Tag.State == stateSucceeded {
			repairRuns, err = c.findRuns(ctx, request.Repository, workflowFile(contract, "publisher"), map[string]string{
				"branch": request.Version, "event": "workflow_dispatch", "head_sha": sourceSHA, "per_page": "100",
			}, func(run workflowRun) bool {
				return run.Event == "workflow_dispatch" && run.HeadBranch == request.Version && run.HeadSHA == sourceSHA && matchesManualReleaseRun(run, request.Version, contract)
			})
			if err != nil {
				return document{}, err
			}
			for _, run := range repairRuns {
				evidence := evidenceFromRun(run)
				evidence.RepairMode, _ = manualRepairMode(run, request.Version, contract)
				doc.RepairRuns = append(doc.RepairRuns, *evidence)
			}
		}
	}

	var publisherRun workflowRun
	var publisherFound bool
	var jobs []workflowJob
	if tagFound && resultStages.Tag.State == stateSucceeded {
		publisherRun, publisherFound, err = c.findRun(ctx, request.Repository, workflowFile(contract, "publisher"), map[string]string{
			"branch": request.Version, "event": "push", "head_sha": tagSHA, "per_page": "100",
		}, func(run workflowRun) bool {
			return run.Event == "push" && run.HeadBranch == request.Version && run.HeadSHA == tagSHA
		})
		if err != nil {
			return document{}, err
		}
		if publisherFound {
			resultStages.Publisher, err = stageFromRun(publisherRun)
			if err != nil {
				return document{}, err
			}
			jobs, err = c.getJobs(ctx, request.Repository, publisherRun.ID, publisherRun.RunAttempt)
			if err != nil {
				return document{}, err
			}
			doc.FailedJobs = collectFailedJobs(jobs)
			resultStages.SupplyChain = stageFromNamedJob(jobs, "supply_chain", resultStages.Publisher)
			resultStages.Homebrew = stageFromNamedJob(jobs, "homebrew", resultStages.Publisher)
			resultStages.Health = stageFromNamedJob(jobs, "health", resultStages.Publisher)
		} else {
			resultStages.Publisher = stage{State: stateWaiting, Reason: "publisher_run_not_found"}
		}
	}

	var releaseRecord releaseResponse
	var releaseFound bool
	if tagFound {
		releaseRecord, releaseFound, err = c.getRelease(ctx, request.Repository, request.Version)
		if err != nil {
			return document{}, err
		}
	}
	resultStages.GitHubRelease = reduceReleaseStage(request.Version, releaseRecord, releaseFound, jobs, resultStages.Publisher, tagFound, contract.Assets)
	if len(repairRuns) > 0 {
		repairFailures, foldErr := c.foldManualRepairs(
			ctx, request.Repository, request.Version, contract, repairRuns,
			releaseRecord, releaseFound, tagFound, &resultStages,
		)
		if foldErr != nil {
			return document{}, foldErr
		}
		if resultStages.Publisher.Reason == "manual_repair_reconciled" {
			doc.FailedJobs = repairFailures
		} else {
			doc.FailedJobs = mergeFailedJobs(doc.FailedJobs, repairFailures)
		}
	}
	if releaseFound && resultStages.Tag.State == stateSucceeded && resultStages.GitHubRelease.State == stateSucceeded && resultStages.GitHubRelease.Release != nil {
		if resultStages.SupplyChain.State == stateSucceeded {
			attestations, observeErr := c.observeAttestations(
				ctx, request.Repository, sourceSHA, contract, resultStages.GitHubRelease.Release.Assets,
			)
			if observeErr != nil {
				return document{}, observeErr
			}
			resultStages.SupplyChain.Attestations = &attestations
			if !attestations.Complete {
				resultStages.SupplyChain.State = stateInconsistent
				resultStages.SupplyChain.Reason = attestations.ReasonCode
			}
		}

		if resultStages.SupplyChain.State == stateSucceeded && homebrewEvidenceRequired(resultStages.Homebrew.State) {
			homebrew, observeErr := c.observeHomebrew(ctx, request.Repository, request.Version, sourceSHA, contract)
			if observeErr != nil {
				return document{}, observeErr
			}
			resultStages.Homebrew.Homebrew = &homebrew
			if !homebrew.Complete {
				resultStages.Homebrew.State = stateInconsistent
				resultStages.Homebrew.Reason = homebrew.ReasonCode
			}
		}
	}
	doc.Stages = &resultStages
	doc.Overall = reduceOverall(resultStages)
	doc.NextAction = reduceNextAction(resultStages, doc.Overall)
	if action, ok := homebrewRepairNextAction(resultStages.Homebrew); ok {
		doc.NextAction = &nextAction{Code: action}
	}
	if doc.AttemptMatrix != nil && !doc.AttemptMatrix.Complete &&
		ciFound && ciRun.Status == "completed" && isFailureConclusion(ciRun.Conclusion) &&
		doc.Overall != nil && doc.Overall.State == "failed" && doc.Overall.Reason == "main_ci_failed" {
		doc.NextAction = &nextAction{Code: "rerun_all_jobs"}
	}
	return doc, nil
}

func homebrewEvidenceRequired(state stageState) bool {
	switch state {
	case stateSucceeded, stateFailed, stateCancelled, stateInconsistent:
		return true
	default:
		return false
	}
}

func homebrewRepairNextAction(homebrew stage) (string, bool) {
	if homebrew.Homebrew == nil || (homebrew.State != stateFailed && homebrew.State != stateCancelled && homebrew.State != stateInconsistent) {
		return "", false
	}
	evidence := homebrew.Homebrew
	if evidence.Complete {
		return "replan_publisher", true
	}
	if evidence.ReasonCode == "homebrew_pr_not_merged" {
		return "replan_publisher", true
	}
	var run *runEvidence
	failedAction := ""
	switch evidence.ReasonCode {
	case "homebrew_pr_head_ci_not_successful":
		run, failedAction = evidence.PRHeadCI, "rerun_tap_pr_ci_all_jobs"
	case "homebrew_post_merge_ci_not_successful":
		run, failedAction = evidence.PostMergeCI, "rerun_tap_post_merge_ci_all_jobs"
	default:
		return "", false
	}
	if run == nil {
		return "", false
	}
	switch run.Status {
	case "queued", "requested", "waiting", "pending", "in_progress":
		return "wait_tap_ci", true
	case "completed":
		if run.Conclusion == "success" {
			return "replan_publisher", true
		}
		if isFailureConclusion(run.Conclusion) {
			return failedAction, true
		}
	}
	return "", false
}

func matchesManualReleaseRun(run workflowRun, version string, contract releasecontract.Contract) bool {
	_, ok := manualRepairMode(run, version, contract)
	return ok
}

func manualRepairMode(run workflowRun, version string, contract releasecontract.Contract) (string, bool) {
	if run.Path != ".github/workflows/"+workflowFile(contract, "publisher") {
		return "", false
	}
	prefix := "env-vault-publication event=workflow_dispatch version=" + version + " repair="
	if !strings.HasPrefix(run.DisplayTitle, prefix) {
		return "", false
	}
	modeAndState := strings.TrimPrefix(run.DisplayTitle, prefix)
	mode, stateDigest, hasState := strings.Cut(modeAndState, " state=")
	if hasState {
		decoded, err := hex.DecodeString(stateDigest)
		if err != nil || len(decoded) != 32 || stateDigest != strings.ToLower(stateDigest) {
			return "", false
		}
	}
	for _, repair := range contract.RepairModes {
		if mode == repair.Code {
			return mode, true
		}
	}
	return "", false
}

var manualRepairStageIDs = map[string]string{
	"github_release": "publication",
	"supply_chain":   "supply_chain",
	"homebrew":       "homebrew",
	"health":         "health",
}

func (c collector) foldManualRepairs(
	ctx context.Context,
	repository string,
	version string,
	contract releasecontract.Contract,
	runs []workflowRun,
	release releaseResponse,
	releaseFound bool,
	tagFound bool,
	result *stages,
) ([]failedJob, error) {
	selected := make(map[string]workflowRun, len(manualRepairStageIDs))
	for _, run := range runs {
		mode, ok := manualRepairMode(run, version, contract)
		if !ok {
			continue
		}
		for statusStage, contractStage := range manualRepairStageIDs {
			if _, exists := selected[statusStage]; exists {
				continue
			}
			if repairModeCovers(contract, mode, contractStage) {
				selected[statusStage] = run
			}
		}
	}
	if len(selected) == 0 {
		return nil, nil
	}

	jobsByRun := make(map[int64][]workflowJob, len(selected))
	selectedRuns := make(map[int64]workflowRun, len(selected))
	for _, run := range selected {
		selectedRuns[run.ID] = run
	}
	for runID, run := range selectedRuns {
		jobs, err := c.getJobs(ctx, repository, runID, run.RunAttempt)
		if err != nil {
			return nil, err
		}
		jobsByRun[runID] = jobs
	}

	for statusStage, run := range selected {
		runStage, err := stageFromRun(run)
		if err != nil {
			return nil, err
		}
		jobs := jobsByRun[run.ID]
		switch statusStage {
		case "github_release":
			result.GitHubRelease = reduceReleaseStage(version, release, releaseFound, jobs, runStage, tagFound, contract.Assets)
		case "supply_chain":
			result.SupplyChain = stageFromNamedJob(jobs, "supply_chain", runStage)
		case "homebrew":
			result.Homebrew = stageFromNamedJob(jobs, "homebrew", runStage)
		case "health":
			result.Health = stageFromNamedJob(jobs, "health", runStage)
		}
	}

	if result.GitHubRelease.State == stateSucceeded &&
		result.SupplyChain.State == stateSucceeded &&
		result.Homebrew.State == stateSucceeded &&
		result.Health.State == stateSucceeded {
		latest := workflowRun{}
		for _, run := range selectedRuns {
			if run.ID > latest.ID {
				latest = run
			}
		}
		evidence := evidenceFromRun(latest)
		evidence.RepairMode, _ = manualRepairMode(latest, version, contract)
		result.Publisher = stage{State: stateSucceeded, Reason: "manual_repair_reconciled", Run: evidence}
	}

	failed := []failedJob{}
	for _, run := range selectedRuns {
		failed = append(failed, collectFailedJobs(jobsByRun[run.ID])...)
	}
	return mergeFailedJobs(failed), nil
}

func repairModeCovers(contract releasecontract.Contract, modeCode, targetStage string) bool {
	resumeStage := ""
	for _, mode := range contract.RepairModes {
		if mode.Code == modeCode {
			resumeStage = mode.ResumeStage
			break
		}
	}
	resumeOrdinal, targetOrdinal := 0, 0
	for _, releaseStage := range contract.ReleaseStages {
		if releaseStage.ID == resumeStage {
			resumeOrdinal = releaseStage.Ordinal
		}
		if releaseStage.ID == targetStage {
			targetOrdinal = releaseStage.Ordinal
		}
	}
	return resumeOrdinal > 0 && targetOrdinal > 0 && resumeOrdinal <= targetOrdinal
}

func (c collector) releaseDefinition() (releasecontract.Contract, error) {
	if c.contract.SchemaID != "" {
		if err := c.contract.Validate(); err != nil {
			return releasecontract.Contract{}, &observationError{code: "CONTRACT_INVALID", operation: releasecontract.CanonicalPath, cause: err}
		}
		return c.contract, nil
	}
	path, err := findRepositoryFile(".", releasecontract.CanonicalPath)
	if err != nil {
		return releasecontract.Contract{}, &observationError{code: "CONTRACT_INVALID", operation: releasecontract.CanonicalPath, cause: err}
	}
	contract, err := releasecontract.LoadFile(path)
	if err != nil {
		return releasecontract.Contract{}, &observationError{code: "CONTRACT_INVALID", operation: releasecontract.CanonicalPath, cause: err}
	}
	return contract, nil
}

func workflowFile(contract releasecontract.Contract, id string) string {
	for _, workflow := range contract.Workflows {
		if workflow.ID == id {
			return workflow.File
		}
	}
	return ""
}

func (c collector) resolveTag(ctx context.Context, repository, version string) (string, bool, error) {
	endpoint := "repos/" + repository + "/git/ref/tags/" + version
	var response tagRefResponse
	if err := c.github.Get(ctx, endpoint, nil, &response); err != nil {
		if isNotFound(err) {
			repositoryEndpoint := "repos/" + repository
			var repositoryRecord repositoryResponse
			if repositoryErr := c.github.Get(ctx, repositoryEndpoint, nil, &repositoryRecord); repositoryErr != nil {
				if isNotFound(repositoryErr) {
					return "", false, &observationError{code: "REPOSITORY_NOT_ACCESSIBLE", operation: repositoryEndpoint, cause: repositoryErr}
				}
				return "", false, repositoryErr
			}
			if repositoryRecord.FullName != repository {
				return "", false, malformed(repositoryEndpoint, "repository identity is malformed")
			}
			return "", false, nil
		}
		return "", false, err
	}
	object := response.Object
	seen := make(map[string]struct{})
	for depth := 0; depth < 8; depth++ {
		if !shaPattern.MatchString(object.SHA) {
			return "", false, malformed(endpoint, "invalid tag object sha")
		}
		switch object.Type {
		case "commit":
			return object.SHA, true, nil
		case "tag":
			if _, exists := seen[object.SHA]; exists {
				return "", false, malformed(endpoint, "annotated tag cycle")
			}
			seen[object.SHA] = struct{}{}
			annotatedEndpoint := "repos/" + repository + "/git/tags/" + object.SHA
			var annotated annotatedTagResponse
			if err := c.github.Get(ctx, annotatedEndpoint, nil, &annotated); err != nil {
				return "", false, err
			}
			object = annotated.Object
		default:
			return "", false, malformed(endpoint, "unsupported tag object type")
		}
	}
	return "", false, malformed(endpoint, "annotated tag chain is too deep")
}

func (c collector) findRun(ctx context.Context, repository, workflow string, query map[string]string, matches func(workflowRun) bool) (workflowRun, bool, error) {
	runs, err := c.findRuns(ctx, repository, workflow, query, matches)
	if err != nil || len(runs) == 0 {
		return workflowRun{}, false, err
	}
	return runs[0], true, nil
}

func (c collector) findRuns(ctx context.Context, repository, workflow string, query map[string]string, matches func(workflowRun) bool) ([]workflowRun, error) {
	endpoint := "repos/" + repository + "/actions/workflows/" + workflow + "/runs"
	expectedPath := ".github/workflows/" + workflow
	requestQuery := make(map[string]string, len(query)+1)
	for key, value := range query {
		requestQuery[key] = value
	}
	requestQuery["per_page"] = "100"
	allRuns := []workflowRun{}
	totalCount := -1
	seenIDs := map[int64]struct{}{}
	for page := 1; ; page++ {
		if page > 100 {
			return nil, malformed(endpoint, "workflow run pagination exceeds the deterministic bound")
		}
		requestQuery["page"] = strconv.Itoa(page)
		var response workflowRunsResponse
		if err := c.github.Get(ctx, endpoint, requestQuery, &response); err != nil {
			return nil, err
		}
		if response.WorkflowRuns == nil || response.TotalCount < 0 {
			return nil, malformed(endpoint, "workflow_runs or total_count is missing")
		}
		if totalCount == -1 {
			totalCount = response.TotalCount
		} else if response.TotalCount != totalCount {
			return nil, malformed(endpoint, "workflow run total_count changed during pagination")
		}
		if len(response.WorkflowRuns) == 0 && len(allRuns) < totalCount {
			return nil, malformed(endpoint, "workflow run pagination ended before total_count")
		}
		for _, run := range response.WorkflowRuns {
			if err := validateRun(run, endpoint); err != nil {
				return nil, err
			}
			if run.Path != expectedPath {
				return nil, malformed(endpoint, "workflow run path does not match the requested workflow")
			}
			if _, duplicate := seenIDs[run.ID]; duplicate {
				return nil, malformed(endpoint, "workflow run pagination contains duplicate ids")
			}
			seenIDs[run.ID] = struct{}{}
			allRuns = append(allRuns, run)
		}
		if len(allRuns) > totalCount {
			return nil, malformed(endpoint, "workflow run pagination exceeds total_count")
		}
		if len(allRuns) == totalCount {
			break
		}
	}
	selected := []workflowRun{}
	for _, run := range allRuns {
		if !matches(run) {
			continue
		}
		selected = append(selected, run)
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].ID > selected[j].ID })
	return selected, nil
}

func (c collector) getJobs(ctx context.Context, repository string, runID int64, attempt int) ([]workflowJob, error) {
	endpoint := "repos/" + repository + "/actions/runs/" + strconv.FormatInt(runID, 10) + "/attempts/" + strconv.Itoa(attempt) + "/jobs"
	var response jobsResponse
	if err := c.github.Get(ctx, endpoint, map[string]string{"per_page": "100"}, &response); err != nil {
		return nil, err
	}
	if response.Jobs == nil || response.TotalCount != 0 && response.TotalCount != len(response.Jobs) {
		return nil, malformed(endpoint, "jobs are missing or require unsupported pagination")
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

func (c collector) getRelease(ctx context.Context, repository, version string) (releaseResponse, bool, error) {
	endpoint := "repos/" + repository + "/releases/tags/" + version
	var response releaseResponse
	if err := c.github.Get(ctx, endpoint, nil, &response); err != nil {
		if isNotFound(err) {
			return releaseResponse{}, false, nil
		}
		return releaseResponse{}, false, err
	}
	if response.TagName == "" || response.HTMLURL == "" || response.Assets == nil {
		return releaseResponse{}, false, malformed(endpoint, "release fields are missing")
	}
	for _, asset := range response.Assets {
		if asset.ID <= 0 || asset.Name == "" || asset.State != "uploaded" || asset.Size <= 0 || !releaseAssetDigestPattern.MatchString(asset.Digest) {
			return releaseResponse{}, false, malformed(endpoint, "release asset identity or digest is malformed")
		}
	}
	return response, true, nil
}

func stageFromRun(run workflowRun) (stage, error) {
	state, err := stateFromStatus(run.Status, run.Conclusion)
	if err != nil {
		return stage{}, err
	}
	return stage{State: state, Run: evidenceFromRun(run)}, nil
}

func evidenceFromRun(run workflowRun) *runEvidence {
	return &runEvidence{
		ID: run.ID, Attempt: run.RunAttempt, Event: run.Event, Status: run.Status,
		Conclusion: run.Conclusion, HeadBranch: run.HeadBranch, HeadSHA: run.HeadSHA,
		WorkflowPath: run.Path, URL: run.HTMLURL,
	}
}

func stageFromNamedJob(jobs []workflowJob, name string, publisher stage) stage {
	var match *workflowJob
	for i := range jobs {
		if jobs[i].Name != name {
			continue
		}
		if match != nil {
			return stage{State: stateInconsistent, Reason: "duplicate_job"}
		}
		match = &jobs[i]
	}
	if match == nil {
		if publisher.State == stateFailed || publisher.State == stateCancelled {
			return stage{State: stateBlocked, Reason: "publisher_failed"}
		}
		if publisher.State == stateSucceeded {
			return stage{State: stateInconsistent, Reason: "completed_publisher_missing_job"}
		}
		return stage{State: stateNotStarted, Reason: "job_not_created"}
	}
	state, err := stateFromStatus(match.Status, match.Conclusion)
	if err != nil {
		return stage{State: stateInconsistent, Reason: "malformed_job_state"}
	}
	if state == stateSkipped && (publisher.State == stateFailed || publisher.State == stateCancelled) {
		state = stateBlocked
	}
	return stage{State: state, Job: &jobEvidence{
		ID: match.ID, Name: match.Name, Status: match.Status, Conclusion: match.Conclusion, URL: match.HTMLURL,
	}}
}

func reduceReleaseStage(version string, release releaseResponse, found bool, jobs []workflowJob, publisher stage, tagFound bool, expectedAssets []string) stage {
	if !tagFound {
		return stage{State: stateNotStarted, Reason: "tag_not_found"}
	}
	releaseJob := stageFromNamedJob(jobs, "release", publisher)
	if !found {
		switch releaseJob.State {
		case stateFailed, stateCancelled:
			return stage{State: releaseJob.State, Reason: "release_job_failed", Job: releaseJob.Job}
		case stateBlocked, stateSkipped:
			return stage{State: stateBlocked, Reason: "release_job_blocked", Job: releaseJob.Job}
		case stateSucceeded:
			return stage{State: stateInconsistent, Reason: "successful_release_job_without_release", Job: releaseJob.Job}
		case stateQueued, stateInProgress:
			return stage{State: releaseJob.State, Reason: "release_not_created", Job: releaseJob.Job}
		default:
			return stage{State: stateWaiting, Reason: "release_not_found"}
		}
	}
	evidence := releaseEvidence{
		TagName: release.TagName, Draft: release.Draft, Prerelease: release.Prerelease,
		URL: release.HTMLURL, Assets: compareAssets(release.Assets, expectedAssets),
	}
	if release.TagName != version || release.Draft || release.Prerelease {
		return stage{State: stateInconsistent, Reason: "release_metadata_mismatch", Release: &evidence}
	}
	if evidence.Assets.DigestComplete && len(evidence.Assets.Missing) == 0 && len(evidence.Assets.Unexpected) == 0 && len(evidence.Assets.Duplicates) == 0 {
		return stage{State: stateSucceeded, Release: &evidence}
	}
	switch releaseJob.State {
	case stateQueued, stateInProgress:
		return stage{State: stateInProgress, Reason: "release_assets_incomplete", Job: releaseJob.Job, Release: &evidence}
	case stateFailed, stateCancelled, stateBlocked:
		return stage{State: stateFailed, Reason: "release_assets_incomplete", Job: releaseJob.Job, Release: &evidence}
	default:
		return stage{State: stateInconsistent, Reason: "release_assets_incomplete", Job: releaseJob.Job, Release: &evidence}
	}
}

func compareAssets(assets []releaseAsset, expectedAssets []string) assetEvidence {
	want := make(map[string]struct{}, len(expectedAssets))
	for _, name := range expectedAssets {
		want[name] = struct{}{}
	}
	counts := make(map[string]int, len(assets))
	for _, asset := range assets {
		counts[asset.Name]++
	}
	result := assetEvidence{
		ExpectedCount: len(expectedAssets), ObservedCount: len(assets), Missing: []string{},
		Unexpected: []string{}, Duplicates: []string{}, Digests: []releaseAssetEvidence{},
	}
	for _, name := range expectedAssets {
		if counts[name] == 0 {
			result.Missing = append(result.Missing, name)
		}
	}
	for name, count := range counts {
		if _, ok := want[name]; !ok {
			result.Unexpected = append(result.Unexpected, name)
		}
		if count > 1 {
			result.Duplicates = append(result.Duplicates, name)
		}
	}
	for _, asset := range assets {
		if _, expected := want[asset.Name]; !expected || counts[asset.Name] != 1 {
			continue
		}
		result.Digests = append(result.Digests, releaseAssetEvidence{
			ID: asset.ID, Name: asset.Name, SHA256: strings.TrimPrefix(asset.Digest, "sha256:"), Size: asset.Size,
		})
	}
	sort.Slice(result.Digests, func(i, j int) bool { return result.Digests[i].Name < result.Digests[j].Name })
	sort.Strings(result.Missing)
	sort.Strings(result.Unexpected)
	sort.Strings(result.Duplicates)
	result.DigestComplete = len(result.Digests) == len(expectedAssets) && len(result.Missing) == 0 && len(result.Unexpected) == 0 && len(result.Duplicates) == 0
	return result
}

func collectFailedJobs(jobs []workflowJob) []failedJob {
	result := make([]failedJob, 0)
	for _, job := range jobs {
		if !isFailureConclusion(job.Conclusion) {
			continue
		}
		failed := failedJob{ID: job.ID, Name: job.Name, Conclusion: job.Conclusion, URL: job.HTMLURL, FailedSteps: []failedStep{}}
		for _, step := range job.Steps {
			if isFailureConclusion(step.Conclusion) {
				failed.FailedSteps = append(failed.FailedSteps, failedStep{Number: step.Number, Name: step.Name, Conclusion: step.Conclusion})
			}
		}
		sort.Slice(failed.FailedSteps, func(i, j int) bool { return failed.FailedSteps[i].Number < failed.FailedSteps[j].Number })
		result = append(result, failed)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].ID < result[j].ID
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func mergeFailedJobs(groups ...[]failedJob) []failedJob {
	byID := make(map[int64]failedJob)
	for _, group := range groups {
		for _, job := range group {
			byID[job.ID] = job
		}
	}
	result := make([]failedJob, 0, len(byID))
	for _, job := range byID {
		result = append(result, job)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].ID < result[j].ID
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func reduceOverall(value stages) *overall {
	all := []struct {
		name  string
		stage stage
	}{
		{"tag", value.Tag}, {"main_ci", value.MainCI}, {"planning", value.Planning},
		{"publisher", value.Publisher}, {"github_release", value.GitHubRelease},
		{"supply_chain", value.SupplyChain}, {"homebrew", value.Homebrew}, {"health", value.Health},
	}
	for _, item := range all {
		if item.stage.State == stateInconsistent {
			return &overall{State: "inconsistent", Terminal: true, Reason: item.name + "_inconsistent"}
		}
	}
	specific := []struct {
		name  string
		stage stage
	}{
		{"main_ci", value.MainCI}, {"planning", value.Planning},
		{"github_release", value.GitHubRelease}, {"supply_chain", value.SupplyChain},
		{"homebrew", value.Homebrew}, {"health", value.Health},
	}
	for _, item := range specific {
		if item.stage.State == stateFailed || item.stage.State == stateCancelled {
			return &overall{State: "failed", Terminal: true, Reason: item.name + "_failed"}
		}
	}
	if value.Publisher.State == stateFailed || value.Publisher.State == stateCancelled || value.Publisher.State == stateSkipped {
		return &overall{State: "failed", Terminal: true, Reason: "publisher_failed"}
	}
	for _, item := range specific {
		if item.stage.State == stateBlocked || item.stage.State == stateSkipped {
			return &overall{State: "inconsistent", Terminal: true, Reason: item.name + "_blocked"}
		}
	}
	allSucceeded := true
	for _, item := range all {
		if item.stage.State != stateSucceeded {
			allSucceeded = false
			break
		}
	}
	if allSucceeded {
		return &overall{State: "succeeded", Terminal: true, Reason: "release_chain_succeeded"}
	}
	for _, item := range all {
		if item.stage.State == stateQueued || item.stage.State == stateInProgress {
			return &overall{State: "running", Terminal: false, Reason: item.name + "_running"}
		}
	}
	return &overall{State: "pending", Terminal: false, Reason: firstPendingReason(all)}
}

func reduceNextAction(value stages, current *overall) *nextAction {
	if current == nil {
		return &nextAction{Code: "resolve_inconsistency"}
	}
	if current.State == "succeeded" {
		return &nextAction{Code: "none"}
	}
	if current.State == "inconsistent" {
		return &nextAction{Code: "resolve_inconsistency"}
	}
	failedStages := []struct {
		stage stage
		code  string
	}{
		{value.MainCI, "inspect_main_ci_failure"},
		{value.Planning, "inspect_planning_failure"},
		{value.GitHubRelease, "inspect_release_failure"},
		{value.SupplyChain, "inspect_supply_chain_failure"},
		{value.Homebrew, "inspect_homebrew_failure"},
		{value.Health, "inspect_health_failure"},
	}
	for _, item := range failedStages {
		if item.stage.State == stateFailed || item.stage.State == stateCancelled {
			return &nextAction{Code: item.code}
		}
	}
	if value.Publisher.State == stateFailed || value.Publisher.State == stateCancelled || value.Publisher.State == stateSkipped {
		return &nextAction{Code: "inspect_publisher_failure"}
	}
	if current.State == "failed" {
		return &nextAction{Code: "resolve_inconsistency"}
	}
	ordered := []struct {
		stage stage
		code  string
	}{
		{value.Tag, "wait_tag"},
		{value.MainCI, "wait_main_ci"},
		{value.Planning, "wait_planning"},
		{value.Publisher, "wait_publisher"},
		{value.GitHubRelease, "wait_release_assets"},
		{value.SupplyChain, "wait_supply_chain"},
		{value.Homebrew, "wait_homebrew"},
		{value.Health, "wait_health"},
	}
	for _, item := range ordered {
		if item.stage.State != stateSucceeded {
			return &nextAction{Code: item.code}
		}
	}
	return &nextAction{Code: "resolve_inconsistency"}
}

func firstPendingReason(ordered []struct {
	name  string
	stage stage
}) string {
	for _, item := range ordered {
		if item.stage.State != stateSucceeded {
			if item.stage.Reason != "" {
				return item.stage.Reason
			}
			return item.name + "_pending"
		}
	}
	return "release_chain_pending"
}

func stateFromStatus(status, conclusion string) (stageState, error) {
	switch status {
	case "queued", "requested", "waiting", "pending":
		if conclusion != "" {
			return "", malformed("workflow_state", "pending status has conclusion")
		}
		return stateQueued, nil
	case "in_progress":
		if conclusion != "" {
			return "", malformed("workflow_state", "in-progress status has conclusion")
		}
		return stateInProgress, nil
	case "completed":
		switch conclusion {
		case "success":
			return stateSucceeded, nil
		case "skipped", "neutral":
			return stateSkipped, nil
		case "cancelled":
			return stateCancelled, nil
		case "failure", "timed_out", "action_required", "startup_failure", "stale":
			return stateFailed, nil
		default:
			return "", malformed("workflow_state", "completed status has unsupported conclusion")
		}
	default:
		return "", malformed("workflow_state", "unsupported status")
	}
}

func validateRun(run workflowRun, operation string) error {
	if run.ID <= 0 || run.RunAttempt <= 0 || run.Event == "" || run.HeadBranch == "" || !shaPattern.MatchString(run.HeadSHA) || run.HTMLURL == "" {
		return malformed(operation, "workflow run fields are malformed")
	}
	_, err := stateFromStatus(run.Status, run.Conclusion)
	return err
}

func validateJob(job workflowJob, operation string) error {
	if job.ID <= 0 || job.Name == "" || job.HTMLURL == "" {
		return malformed(operation, "workflow job fields are malformed")
	}
	if _, err := stateFromStatus(job.Status, job.Conclusion); err != nil {
		return malformed(operation, "workflow job state is malformed")
	}
	for _, step := range job.Steps {
		if step.Number <= 0 || step.Name == "" {
			return malformed(operation, "workflow step fields are malformed")
		}
		if _, err := stateFromStatus(step.Status, step.Conclusion); err != nil {
			return malformed(operation, "workflow step state is malformed")
		}
	}
	return nil
}

func isFailureConclusion(conclusion string) bool {
	switch conclusion {
	case "failure", "cancelled", "timed_out", "action_required", "startup_failure", "stale":
		return true
	default:
		return false
	}
}

func malformed(operation, message string) error {
	return &observationError{code: "MALFORMED_RESPONSE", operation: operation, cause: errors.New(message)}
}

func validateQuery(value query) error {
	if !repositoryPattern.MatchString(value.Repository) {
		return fmt.Errorf("repository must have the form OWNER/REPO")
	}
	if !releasecontract.IsVersion(value.Version) {
		return fmt.Errorf("version must match vMAJOR.MINOR.PATCH")
	}
	if value.SourceSHA != "" && !shaPattern.MatchString(value.SourceSHA) {
		return fmt.Errorf("source-sha must contain exactly 40 lowercase hexadecimal characters")
	}
	return nil
}

func errorDocument(now time.Time, request query, err error) document {
	info := errorInfo{Code: "OBSERVATION_FAILED", Operation: "release_status"}
	var apiErr *apiError
	var observationErr *observationError
	switch {
	case errors.As(err, &observationErr):
		info.Code = observationErr.code
		info.Operation = observationErr.operation
	case errors.As(err, &apiErr):
		info.Code = apiErr.Code
		info.Operation = apiErr.Endpoint
		info.HTTPStatus = apiErr.HTTPStatus
		info.Retryable = apiErr.Retryable
	}
	return document{Schema: statusSchema, OK: false, ObservedAt: now.UTC(), Query: request, Error: &info}
}

func exitCodeFor(doc document, watch bool) int {
	if !doc.OK {
		return 3
	}
	if doc.Overall == nil {
		return 3
	}
	switch doc.Overall.State {
	case "failed", "inconsistent":
		return 1
	case "succeeded":
		return 0
	case "pending", "running":
		if watch {
			return 4
		}
		return 0
	default:
		return 3
	}
}
