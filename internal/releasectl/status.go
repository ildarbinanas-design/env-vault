package releasectl

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"time"
)

const (
	statusSchema      = "env-vault.release-status.v1"
	defaultRepository = "ildarbinanas-design/env-vault"
)

var (
	versionPattern    = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
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
	Schema     string      `json:"schema"`
	OK         bool        `json:"ok"`
	ObservedAt time.Time   `json:"observed_at"`
	Query      query       `json:"query"`
	Overall    *overall    `json:"overall,omitempty"`
	Identity   *identity   `json:"identity,omitempty"`
	Stages     *stages     `json:"stages,omitempty"`
	FailedJobs []failedJob `json:"failed_jobs,omitempty"`
	NextAction *nextAction `json:"next_action,omitempty"`
	Watch      *watchInfo  `json:"watch,omitempty"`
	Error      *errorInfo  `json:"error,omitempty"`
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
	State   stageState       `json:"state"`
	Reason  string           `json:"reason,omitempty"`
	SHA     string           `json:"sha,omitempty"`
	Run     *runEvidence     `json:"run,omitempty"`
	Job     *jobEvidence     `json:"job,omitempty"`
	Release *releaseEvidence `json:"release,omitempty"`
}

type runEvidence struct {
	ID         int64  `json:"id"`
	Attempt    int    `json:"attempt"`
	Event      string `json:"event"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion,omitempty"`
	HeadBranch string `json:"head_branch"`
	HeadSHA    string `json:"head_sha"`
	URL        string `json:"url"`
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
	ExpectedCount int      `json:"expected_count"`
	ObservedCount int      `json:"observed_count"`
	Missing       []string `json:"missing"`
	Unexpected    []string `json:"unexpected"`
	Duplicates    []string `json:"duplicates"`
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
	github githubGetter
	clock  clock
}

type tagRefResponse struct {
	Object gitObject `json:"object"`
}

type repositoryResponse struct {
	FullName string `json:"full_name"`
}

type annotatedTagResponse struct {
	Object gitObject `json:"object"`
}

type gitObject struct {
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

type workflowRunsResponse struct {
	WorkflowRuns []workflowRun `json:"workflow_runs"`
}

type workflowRun struct {
	ID           int64  `json:"id"`
	RunAttempt   int    `json:"run_attempt"`
	Event        string `json:"event"`
	Status       string `json:"status"`
	Conclusion   string `json:"conclusion"`
	HeadBranch   string `json:"head_branch"`
	HeadSHA      string `json:"head_sha"`
	HTMLURL      string `json:"html_url"`
	DisplayTitle string `json:"display_title"`
}

type jobsResponse struct {
	Jobs []workflowJob `json:"jobs"`
}

type workflowJob struct {
	ID         int64          `json:"id"`
	Name       string         `json:"name"`
	Status     string         `json:"status"`
	Conclusion string         `json:"conclusion"`
	HTMLURL    string         `json:"html_url"`
	Steps      []workflowStep `json:"steps"`
}

type workflowStep struct {
	Number     int    `json:"number"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

type releaseResponse struct {
	TagName    string         `json:"tag_name"`
	Draft      bool           `json:"draft"`
	Prerelease bool           `json:"prerelease"`
	HTMLURL    string         `json:"html_url"`
	Assets     []releaseAsset `json:"assets"`
}

type releaseAsset struct {
	Name string `json:"name"`
}

var expectedAssets = []string{
	"env-vault-darwin-amd64.tar.gz",
	"env-vault-darwin-amd64.tar.gz.sha256",
	"env-vault-darwin-arm64.tar.gz",
	"env-vault-darwin-arm64.tar.gz.sha256",
	"env-vault-linux-amd64.tar.gz",
	"env-vault-linux-amd64.tar.gz.sha256",
	"env-vault-linux-arm64.tar.gz",
	"env-vault-linux-arm64.tar.gz.sha256",
	"env-vault-windows-amd64.zip",
	"env-vault-windows-amd64.zip.sha256",
}

func (c collector) snapshot(ctx context.Context, request query) (document, error) {
	doc := document{Schema: statusSchema, OK: true, ObservedAt: c.clock.Now().UTC(), Query: request}
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

	if sourceSHA != "" {
		ciRun, found, err := c.findRun(ctx, request.Repository, "ci.yml", map[string]string{
			"branch": "main", "event": "push", "head_sha": sourceSHA, "per_page": "100",
		}, func(run workflowRun) bool {
			return run.Event == "push" && run.HeadBranch == "main" && run.HeadSHA == sourceSHA
		})
		if err != nil {
			return document{}, err
		}
		if found {
			resultStages.MainCI, err = stageFromRun(ciRun)
			if err != nil {
				return document{}, err
			}
		} else {
			resultStages.MainCI = stage{State: stateWaiting, Reason: "main_ci_not_found"}
		}

		planningRun, found, err := c.findRun(ctx, request.Repository, "release-please.yml", map[string]string{
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
	}

	var publisherRun workflowRun
	var publisherFound bool
	var jobs []workflowJob
	if tagFound && resultStages.Tag.State == stateSucceeded {
		publisherRun, publisherFound, err = c.findRun(ctx, request.Repository, "build-binaries.yml", map[string]string{
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
			jobs, err = c.getJobs(ctx, request.Repository, publisherRun.ID)
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
	resultStages.GitHubRelease = reduceReleaseStage(request.Version, releaseRecord, releaseFound, jobs, resultStages.Publisher, tagFound)
	doc.Stages = &resultStages
	doc.Overall = reduceOverall(resultStages)
	doc.NextAction = reduceNextAction(resultStages, doc.Overall)
	return doc, nil
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
	endpoint := "repos/" + repository + "/actions/workflows/" + workflow + "/runs"
	var response workflowRunsResponse
	if err := c.github.Get(ctx, endpoint, query, &response); err != nil {
		return workflowRun{}, false, err
	}
	if response.WorkflowRuns == nil {
		return workflowRun{}, false, malformed(endpoint, "workflow_runs is missing")
	}
	var selected workflowRun
	found := false
	for _, run := range response.WorkflowRuns {
		if !matches(run) {
			continue
		}
		if err := validateRun(run, endpoint); err != nil {
			return workflowRun{}, false, err
		}
		if !found || run.ID > selected.ID {
			selected = run
			found = true
		}
	}
	return selected, found, nil
}

func (c collector) getJobs(ctx context.Context, repository string, runID int64) ([]workflowJob, error) {
	endpoint := "repos/" + repository + "/actions/runs/" + strconv.FormatInt(runID, 10) + "/jobs"
	var response jobsResponse
	if err := c.github.Get(ctx, endpoint, map[string]string{"filter": "latest", "per_page": "100"}, &response); err != nil {
		return nil, err
	}
	if response.Jobs == nil {
		return nil, malformed(endpoint, "jobs is missing")
	}
	for _, job := range response.Jobs {
		if err := validateJob(job, endpoint); err != nil {
			return nil, err
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
		if asset.Name == "" {
			return releaseResponse{}, false, malformed(endpoint, "release asset name is missing")
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
		Conclusion: run.Conclusion, HeadBranch: run.HeadBranch, HeadSHA: run.HeadSHA, URL: run.HTMLURL,
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

func reduceReleaseStage(version string, release releaseResponse, found bool, jobs []workflowJob, publisher stage, tagFound bool) stage {
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
		URL: release.HTMLURL, Assets: compareAssets(release.Assets),
	}
	if release.TagName != version || release.Draft || release.Prerelease {
		return stage{State: stateInconsistent, Reason: "release_metadata_mismatch", Release: &evidence}
	}
	if len(evidence.Assets.Missing) == 0 && len(evidence.Assets.Unexpected) == 0 && len(evidence.Assets.Duplicates) == 0 {
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

func compareAssets(assets []releaseAsset) assetEvidence {
	want := make(map[string]struct{}, len(expectedAssets))
	for _, name := range expectedAssets {
		want[name] = struct{}{}
	}
	counts := make(map[string]int, len(assets))
	for _, asset := range assets {
		counts[asset.Name]++
	}
	result := assetEvidence{ExpectedCount: len(expectedAssets), ObservedCount: len(assets), Missing: []string{}, Unexpected: []string{}, Duplicates: []string{}}
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
	sort.Strings(result.Missing)
	sort.Strings(result.Unexpected)
	sort.Strings(result.Duplicates)
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
	if !versionPattern.MatchString(value.Version) {
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
