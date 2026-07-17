package releaseevidence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/githubapi"
	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
	"github.com/ildarbinanas-design/env-vault/internal/releasemetrics"
	"github.com/ildarbinanas-design/env-vault/internal/releasepromotion"
)

type CollectOptions struct {
	Contract            releasecontract.Contract
	ContractPath        string
	Repository          string
	Version             string
	SourceSHA           string
	PublisherRunID      int64
	PublisherRunAttempt int
	AssetsDirectory     string
	PromotionManifest   string
}

// CommandRunner is intentionally GET-only at the API layer. It can run the
// local cryptographic `gh attestation verify` command, but has no request-body
// or mutation method.
type CommandRunner interface {
	Run(context.Context, []string) ([]byte, error)
}

type GHRunner struct{}

func (GHRunner) Run(ctx context.Context, args []string) ([]byte, error) {
	command := exec.CommandContext(ctx, "gh", args...)
	command.Env = safeGitHubEnvironment(os.Environ())
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, &githubCommandError{Code: "API_UNAVAILABLE", Retryable: true, cause: ctx.Err()}
		}
		return nil, classifyGitHubCommandError(stderr.Bytes(), err)
	}
	return stdout.Bytes(), nil
}

func safeGitHubEnvironment(environment []string) []string {
	blocked := map[string]bool{"GH_DEBUG": true, "GH_HOST": true, "GIT_TRACE": true, "GIT_TRACE_CURL": true, "GIT_CURL_VERBOSE": true}
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		key, _, _ := strings.Cut(entry, "=")
		if !blocked[key] {
			result = append(result, entry)
		}
	}
	return result
}

type apiClient struct{ runner CommandRunner }

type githubCommandError struct {
	Code       string
	HTTPStatus int
	Retryable  bool
	cause      error
}

func (e *githubCommandError) Error() string {
	if e.HTTPStatus != 0 {
		return fmt.Sprintf("github observation failed: code=%s status=%d", e.Code, e.HTTPStatus)
	}
	return fmt.Sprintf("github observation failed: code=%s", e.Code)
}

func (e *githubCommandError) Unwrap() error { return e.cause }

type remoteObservationError struct {
	Endpoint string
	cause    error
}

func (e *remoteObservationError) Error() string { return "GET " + e.Endpoint + ": " + e.cause.Error() }
func (e *remoteObservationError) Unwrap() error { return e.cause }

var githubHTTPStatusPattern = regexp.MustCompile(`(?i)(?:\(HTTP |HTTP/[^ ]+ )([0-9]{3})(?:\)|\s|$)`)

func classifyGitHubCommandError(stderr []byte, cause error) error {
	if errors.Is(cause, exec.ErrNotFound) {
		return &githubCommandError{Code: "DEPENDENCY_MISSING", cause: cause}
	}
	status := 0
	if match := githubHTTPStatusPattern.FindSubmatch(stderr); len(match) == 2 {
		status, _ = strconv.Atoi(string(match[1]))
	}
	lower := strings.ToLower(string(stderr))
	if status == 429 || strings.Contains(lower, "rate limit") || strings.Contains(lower, "abuse detection") {
		return &githubCommandError{Code: "RATE_LIMITED", HTTPStatus: status, Retryable: true, cause: cause}
	}
	switch status {
	case 401:
		return &githubCommandError{Code: "AUTH_REQUIRED", HTTPStatus: status, cause: cause}
	case 403:
		return &githubCommandError{Code: "AUTH_FORBIDDEN", HTTPStatus: status, cause: cause}
	case 404:
		return &githubCommandError{Code: "NOT_FOUND", HTTPStatus: status, cause: cause}
	}
	if status >= 500 {
		return &githubCommandError{Code: "API_UNAVAILABLE", HTTPStatus: status, Retryable: true, cause: cause}
	}
	for _, fragment := range []string{"could not resolve", "network is unreachable", "connection refused", "connection reset", "i/o timeout", "context deadline exceeded", "unexpected eof", "error connecting to"} {
		if strings.Contains(lower, fragment) {
			return &githubCommandError{Code: "API_UNAVAILABLE", HTTPStatus: status, Retryable: true, cause: cause}
		}
	}
	for _, fragment := range []string{"not logged into any github hosts", "gh auth login", "bad credentials", "authentication token"} {
		if strings.Contains(lower, fragment) {
			return &githubCommandError{Code: "AUTH_REQUIRED", HTTPStatus: status, cause: cause}
		}
	}
	return &githubCommandError{Code: "API_REQUEST_FAILED", HTTPStatus: status, cause: cause}
}

func (client apiClient) get(ctx context.Context, endpoint string, query map[string]string, target any) error {
	args := []string{"api", "--method", "GET", "--hostname", "github.com", "--header", "Accept: application/vnd.github+json", "--header", "X-GitHub-Api-Version: " + githubapi.Version, endpoint}
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, "--raw-field", key+"="+query[key])
	}
	data, err := client.runner.Run(ctx, args)
	if err != nil {
		return &remoteObservationError{Endpoint: endpoint, cause: err}
	}
	if err := decodeSingleJSON(data, target); err != nil {
		return &remoteObservationError{Endpoint: endpoint, cause: &githubCommandError{Code: "MALFORMED_RESPONSE", cause: err}}
	}
	return nil
}

type apiRun struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Path         string    `json:"path"`
	Event        string    `json:"event"`
	Status       string    `json:"status"`
	Conclusion   string    `json:"conclusion"`
	HeadBranch   string    `json:"head_branch"`
	HeadSHA      string    `json:"head_sha"`
	DisplayTitle string    `json:"display_title"`
	RunAttempt   int       `json:"run_attempt"`
	CreatedAt    time.Time `json:"created_at"`
	RunStartedAt time.Time `json:"run_started_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type apiRuns struct {
	TotalCount   int      `json:"total_count"`
	WorkflowRuns []apiRun `json:"workflow_runs"`
}

type apiJobs struct {
	TotalCount int      `json:"total_count"`
	Jobs       []apiJob `json:"jobs"`
}

type apiJob struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	RunAttempt  int       `json:"run_attempt"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	Steps       []apiStep `json:"steps"`
}

type apiStep struct {
	Name        string    `json:"name"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
}

type apiRelease struct {
	TagName    string     `json:"tag_name"`
	Draft      bool       `json:"draft"`
	Prerelease bool       `json:"prerelease"`
	Assets     []apiAsset `json:"assets"`
}

type apiAsset struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
	State  string `json:"state"`
}

type apiAttestations struct {
	Attestations []json.RawMessage `json:"attestations"`
}

type apiGitObject struct {
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

type apiTagReference struct {
	Ref    string       `json:"ref"`
	Object apiGitObject `json:"object"`
}

type apiAnnotatedTag struct {
	Object apiGitObject `json:"object"`
}

type apiPull struct {
	Number         int        `json:"number"`
	State          string     `json:"state"`
	Title          string     `json:"title"`
	Body           string     `json:"body"`
	Draft          bool       `json:"draft"`
	MergedAt       *time.Time `json:"merged_at"`
	MergeCommitSHA string     `json:"merge_commit_sha"`
	User           struct {
		Login string `json:"login"`
	} `json:"user"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Head apiPullRef `json:"head"`
	Base apiPullRef `json:"base"`
}

type apiPullRef struct {
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repo"`
}

type apiPullFile struct {
	Filename string `json:"filename"`
}

type apiContent struct {
	Type     string `json:"type"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
	SHA      string `json:"sha"`
}

type TriggerIdentity struct {
	Repository          string
	Version             string
	SourceSHA           string
	PublisherRunID      int64
	PublisherRunAttempt int
	PublisherConclusion string
	PublisherEvent      string
	RepairMode          string
	RepairStateDigest   string
	CIRunID             int64
	CIRunAttempt        int
}

type workflowRunEvent struct {
	Action     string `json:"action"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	WorkflowRun apiRun `json:"workflow_run"`
}

var (
	pushRunTitlePattern   = regexp.MustCompile(`^env-vault-publication event=push version=([^ ]+) repair=none state=automatic$`)
	repairRunTitlePattern = regexp.MustCompile(`^env-vault-publication event=workflow_dispatch version=([^ ]+) repair=(release-assets|homebrew|health) state=([0-9a-f]{64})$`)
)

func ResolveTrigger(ctx context.Context, eventFile string, contract releasecontract.Contract, runner CommandRunner) (TriggerIdentity, error) {
	if err := contract.Validate(); err != nil {
		return TriggerIdentity{}, err
	}
	data, err := os.ReadFile(eventFile)
	if err != nil {
		return TriggerIdentity{}, err
	}
	var event workflowRunEvent
	if err := decodeSingleJSON(data, &event); err != nil {
		return TriggerIdentity{}, err
	}
	if event.Action != "completed" || event.Repository.FullName != repositoryForApp(contract, "release_planning") || runner == nil {
		return TriggerIdentity{}, errors.New("workflow_run event identity is invalid")
	}
	api := apiClient{runner: runner}
	var observed apiRun
	endpoint := fmt.Sprintf("repos/%s/actions/runs/%d", event.Repository.FullName, event.WorkflowRun.ID)
	if err := api.get(ctx, endpoint, nil, &observed); err != nil {
		return TriggerIdentity{}, err
	}
	if !sameRunEventIdentity(event.WorkflowRun, observed) {
		return TriggerIdentity{}, errors.New("workflow_run event differs from the current API run identity")
	}
	publisher, ok := contract.WorkflowByID("publisher")
	if !ok || observed.Name != publisher.Name || observed.Path != ".github/workflows/"+publisher.File || observed.Status != "completed" || !oneOf(observed.Conclusion, "success", "failure", "cancelled") || observed.RunAttempt <= 0 || observed.UpdatedAt.IsZero() {
		return TriggerIdentity{}, errors.New("triggering publisher workflow identity or terminal state is invalid")
	}
	identity := TriggerIdentity{
		Repository: event.Repository.FullName, PublisherRunID: observed.ID, PublisherRunAttempt: observed.RunAttempt,
		PublisherConclusion: observed.Conclusion, PublisherEvent: observed.Event,
	}
	switch observed.Event {
	case "push":
		match := pushRunTitlePattern.FindStringSubmatch(observed.DisplayTitle)
		if len(match) != 2 || observed.HeadBranch != match[1] || observed.HeadSHA == "" {
			return TriggerIdentity{}, errors.New("tag-push publisher run-name or ref identity is invalid")
		}
		identity.Version, identity.SourceSHA, identity.RepairMode = match[1], observed.HeadSHA, "none"
	case "workflow_dispatch":
		match := repairRunTitlePattern.FindStringSubmatch(observed.DisplayTitle)
		if len(match) != 4 || observed.HeadBranch != match[1] || !validSHA(observed.HeadSHA) {
			return TriggerIdentity{}, errors.New("repair publisher run-name or tag identity is invalid")
		}
		identity.Version, identity.RepairMode, identity.RepairStateDigest = match[1], match[2], match[3]
	default:
		return TriggerIdentity{}, errors.New("publisher event is outside the steady release evidence protocol")
	}
	if err := validateSteadyVersion(contract, identity.Version); err != nil {
		return TriggerIdentity{}, err
	}
	tagSHA, err := resolveExactTag(ctx, api, identity.Repository, identity.Version)
	if err != nil {
		return TriggerIdentity{}, errors.New("release tag source is unavailable or malformed")
	}
	identity.SourceSHA = tagSHA
	if observed.HeadSHA != identity.SourceSHA {
		return TriggerIdentity{}, errors.New("publisher head differs from the immutable tag source")
	}
	ci, err := findMainCIRun(ctx, api, identity.Repository, identity.SourceSHA, contract)
	if err != nil {
		return TriggerIdentity{}, err
	}
	identity.CIRunID, identity.CIRunAttempt = ci.ID, ci.RunAttempt
	return identity, nil
}

func sameRunEventIdentity(event, observed apiRun) bool {
	return event.ID == observed.ID && event.Event == observed.Event && event.Status == observed.Status && event.Conclusion == observed.Conclusion && event.HeadBranch == observed.HeadBranch && event.HeadSHA == observed.HeadSHA && event.DisplayTitle == observed.DisplayTitle && event.RunAttempt == observed.RunAttempt
}

func validateSteadyVersion(contract releasecontract.Contract, version string) error {
	if !releasecontract.IsVersion(version) {
		return errors.New("publisher version is not strict vX.Y.Z")
	}
	if _, legacy := contract.LegacyVersion(version); legacy {
		return errors.New("legacy diagnostic rebuilds are outside steady-state release evidence")
	}
	for _, blocked := range contract.VersionPolicy.BlockedVersions {
		if blocked.Version == version {
			return errors.New("blocked failed tag must not enter published release evidence")
		}
	}
	return nil
}

func Collect(ctx context.Context, options CollectOptions, runner CommandRunner) (Evidence, error) {
	if runner == nil {
		return Evidence{}, errors.New("GitHub command runner is required")
	}
	if err := validateCollectOptions(options); err != nil {
		return Evidence{}, err
	}
	api := apiClient{runner: runner}
	publisher, jobs, allJobs, err := collectExactRun(ctx, api, options.Repository, options.PublisherRunID, options.PublisherRunAttempt)
	if err != nil {
		return Evidence{}, err
	}
	publisherWorkflow, ok := options.Contract.WorkflowByID("publisher")
	if !ok || publisher.Name != publisherWorkflow.Name || publisher.Path != ".github/workflows/"+publisherWorkflow.File || publisher.Status != "completed" || !publisherRunMatchesRelease(publisher, options) {
		return Evidence{}, errors.New("publisher run identity does not match the exact tag/source workflow tuple")
	}
	publisherMetrics := workflowRunEvidence("publisher", publisher, jobs, allJobs)
	evidence := baseRunEvidence(options, publisher, publisherMetrics)
	tagSHA, err := resolveExactTag(ctx, api, options.Repository, options.Version)
	if err != nil || tagSHA != options.SourceSHA {
		return Evidence{}, errors.New("release tag does not resolve to the exact source SHA")
	}
	evidence.Release.TagSHA = tagSHA
	if publisher.Conclusion != "success" {
		if publisher.Conclusion != "failure" && publisher.Conclusion != "cancelled" {
			return Evidence{}, fmt.Errorf("completed publisher has unsupported conclusion %q", publisher.Conclusion)
		}
		evidence.Release.Status = "failed"
		snapshot, assets, homebrew, snapshotChecks := collectFailedPublicationSnapshot(ctx, api, options)
		evidence.Release.Publication = &snapshot
		evidence.Release.Assets = assets
		evidence.Release.Homebrew = homebrew
		switch snapshot.GitHubRelease.State {
		case "present", "incomplete":
			evidence.Release.GitHubRelease = "present"
		case "absent":
			evidence.Release.GitHubRelease = "absent"
		default:
			evidence.Release.GitHubRelease = "not_checked"
		}
		evidence.Checks = append(evidence.Checks, Check{ID: "publisher-run", Command: "GET exact workflow run and attempt jobs", Result: "fail", Source: "github_api"})
		evidence.Checks = append(evidence.Checks, snapshotChecks...)
		evidence.ResidualRisks = []ResidualRisk{{Code: "publication-run-failed", Status: "open"}}
		if publicationSnapshotUnknown(snapshot) {
			evidence.ResidualRisks = append(evidence.ResidualRisks, ResidualRisk{Code: "publication-state-unknown", Status: "open"})
		}
		if err := evidence.Validate(options.Contract); err != nil {
			return Evidence{}, err
		}
		return evidence, nil
	}
	lineage, err := collectPublisherLineage(ctx, api, options, publisher, publisherMetrics)
	if err != nil {
		return Evidence{}, err
	}
	evidence.WorkflowRuns = lineage

	release, err := getRelease(ctx, api, options.Repository, options.Version)
	if err != nil {
		return Evidence{}, err
	}
	assets, archiveDigests, err := verifyReleaseAssets(release, options)
	if err != nil {
		return Evidence{}, err
	}
	promotion, err := releasepromotion.ReadManifest(options.PromotionManifest)
	if err != nil {
		return Evidence{}, fmt.Errorf("read publisher promotion manifest: %w", err)
	}
	if err := releasepromotion.Verify(promotion, releasepromotion.VerifyOptions{
		ContractPath: options.ContractPath, SourceSHA: options.SourceSHA, ReleaseVersion: options.Version,
		Repository: options.Repository, RunID: promotion.Workflow.RunID, RunAttempt: promotion.Workflow.RunAttempt,
		ArtifactsRoot: options.AssetsDirectory,
	}); err != nil {
		return Evidence{}, fmt.Errorf("verify publisher promotion manifest: %w", err)
	}
	if err := promotionMatchesRelease(promotion, assets, options.Contract); err != nil {
		return Evidence{}, err
	}
	attestations, err := verifyAttestations(ctx, runner, options, archiveDigests, publisherWorkflow)
	if err != nil {
		return Evidence{}, err
	}
	homebrew, err := collectHomebrew(ctx, api, options)
	if err != nil {
		return Evidence{}, err
	}
	releasePR, err := collectReleasePullRequest(ctx, api, options)
	if err != nil {
		return Evidence{}, err
	}
	ciMetrics, err := collectMainCI(ctx, api, options)
	if err != nil {
		return Evidence{}, err
	}
	if ciMetrics.RunID != promotion.Workflow.RunID || ciMetrics.RunAttempt != promotion.Workflow.RunAttempt {
		return Evidence{}, errors.New("promotion manifest does not bind the unique exact-source main CI run")
	}
	evidence.WorkflowRuns = append([]WorkflowRun{ciMetrics}, evidence.WorkflowRuns...)
	evidence.Release = ReleaseResult{
		Status: "published", Version: options.Version, SourceSHA: options.SourceSHA,
		PullRequest: releasePR, TagSHA: tagSHA, GitHubRelease: "present",
		Assets: assets, Attestations: attestations, Homebrew: homebrew, Promotion: &promotion,
		Publication: &PublicationSnapshot{
			GitHubRelease: PublicationState{State: "present", ReasonCode: "github-release-present"},
			Assets:        PublicationState{State: "present", ReasonCode: "release-assets-present"},
			Attestations:  PublicationState{State: "present", ReasonCode: "attestations-verified"},
			Homebrew:      PublicationState{State: "present", ReasonCode: "homebrew-state-present"},
		},
	}
	evidence.Checks = []Check{
		{ID: "exact-tag-source", Command: "GET release tag commit", Result: "pass", Source: "github_api"},
		{ID: "release-assets", Command: "GET release by exact tag and hash local assets", Result: "pass", Source: "artifact"},
		{ID: "checksum-content", Command: "verify five exact archive checksum files", Result: "pass", Source: "artifact"},
		{ID: "provenance-attestations", Command: "gh attestation verify exact five archives as SLSA provenance", Result: "pass", Source: "github_api"},
		{ID: "sbom-attestations", Command: "gh attestation verify exact five archives as SPDX SBOM", Result: "pass", Source: "github_api"},
		{ID: "homebrew-pr-head-ci", Command: "GET exact tap pull-request head CI", Result: "pass", Source: "github_api"},
		{ID: "homebrew-post-merge-ci", Command: "GET exact tap post-merge CI", Result: "pass", Source: "github_api"},
	}
	evidence.Guarantees = []Guarantee{
		{ID: "immutable-tags", Status: "preserved", Evidence: "exact-tag-source"},
		{ID: "exact-source-assets", Status: "improved", Evidence: "release-assets"},
		{ID: "no-clobber-assets", Status: "preserved", Evidence: "release-assets"},
		{ID: "provenance-sbom", Status: "preserved", Evidence: "provenance-attestations"},
		{ID: "homebrew-exact-state", Status: "preserved", Evidence: "homebrew-post-merge-ci"},
	}
	if err := evidence.Validate(options.Contract); err != nil {
		return Evidence{}, err
	}
	return evidence, nil
}

func publisherRunMatchesRelease(run apiRun, options CollectOptions) bool {
	switch run.Event {
	case "push":
		match := pushRunTitlePattern.FindStringSubmatch(run.DisplayTitle)
		return len(match) == 2 && match[1] == options.Version && run.HeadBranch == options.Version && run.HeadSHA == options.SourceSHA
	case "workflow_dispatch":
		match := repairRunTitlePattern.FindStringSubmatch(run.DisplayTitle)
		return len(match) == 4 && match[1] == options.Version && run.HeadBranch == options.Version && run.HeadSHA == options.SourceSHA
	default:
		return false
	}
}

func validateCollectOptions(options CollectOptions) error {
	if err := options.Contract.Validate(); err != nil {
		return err
	}
	if !validRepository(options.Repository) || !releasecontract.IsVersion(options.Version) || !validSHA(options.SourceSHA) || options.PublisherRunID <= 0 || options.PublisherRunAttempt <= 0 {
		return errors.New("collector requires exact repository/version/source/run/attempt identity")
	}
	if options.Repository != repositoryForApp(options.Contract, "release_planning") {
		return errors.New("collector repository does not match the release contract")
	}
	if options.ContractPath == "" {
		return errors.New("collector release contract path is required")
	}
	loaded, err := releasecontract.LoadFile(options.ContractPath)
	if err != nil {
		return err
	}
	providedJSON, err1 := json.Marshal(options.Contract)
	loadedJSON, err2 := json.Marshal(loaded)
	if err1 != nil || err2 != nil || !bytes.Equal(providedJSON, loadedJSON) {
		return errors.New("collector release contract value differs from the exact contract file")
	}
	if err := validateSteadyVersion(options.Contract, options.Version); err != nil {
		return err
	}
	return nil
}

func promotionMatchesRelease(manifest releasepromotion.Manifest, assets []Asset, contract releasecontract.Contract) error {
	byName := make(map[string]string, len(assets))
	for _, asset := range assets {
		byName[asset.Name] = asset.SHA256
	}
	if len(manifest.Platforms) != len(contract.Platforms) {
		return errors.New("promotion manifest platform matrix is incomplete")
	}
	for index, platform := range contract.Platforms {
		evidence := manifest.Platforms[index]
		if evidence.PlatformID != platform.ID || evidence.Archive.Name != platform.Archive || evidence.Checksum.Name != platform.Checksum || evidence.Archive.SHA256 != byName[platform.Archive] || evidence.Checksum.SHA256 != byName[platform.Checksum] {
			return fmt.Errorf("published assets differ from promoted platform %q", platform.ID)
		}
	}
	return nil
}

func baseRunEvidence(options CollectOptions, run apiRun, metrics WorkflowRun) Evidence {
	generatedAt := run.UpdatedAt.UTC().Format(time.RFC3339)
	return Evidence{
		SchemaID: SchemaID, GeneratedAt: generatedAt, TaskID: "release-" + options.Version,
		ObjectiveCode: "steady-state-release-evidence",
		Repository:    Repository{Name: options.Repository, BeforeSHA: options.SourceSHA, AfterSHA: options.SourceSHA},
		Release:       ReleaseResult{Status: "pending_authorization", Version: options.Version, SourceSHA: options.SourceSHA, GitHubRelease: "not_checked", Assets: []Asset{}, Attestations: []Attestation{}},
		Changes:       []string{"automated-evidence", "automated-metrics", "exact-source-observation"},
		Checks:        []Check{{ID: "exact-publisher-run", Command: "GET exact publisher workflow run and attempt jobs", Result: "pass", Source: "github_api"}},
		Guarantees:    []Guarantee{{ID: "exact-run-identity", Status: "improved", Evidence: "exact-publisher-run"}},
		WorkflowRuns:  []WorkflowRun{metrics}, ResidualRisks: []ResidualRisk{},
	}
}

func collectFailedPublicationSnapshot(ctx context.Context, api apiClient, options CollectOptions) (PublicationSnapshot, []Asset, *Homebrew, []Check) {
	unknown := PublicationState{State: "unknown", ReasonCode: "remote-state-unknown"}
	snapshot := PublicationSnapshot{GitHubRelease: unknown, Assets: unknown, Attestations: unknown, Homebrew: unknown}
	assets := []Asset{}

	release, releaseErr := getRelease(ctx, api, options.Repository, options.Version)
	switch {
	case releaseErr == nil:
		if release.TagName == "" {
			snapshot.GitHubRelease = PublicationState{State: "unknown", ReasonCode: "github-release-schema-unknown"}
		} else if release.TagName == options.Version && !release.Draft && !release.Prerelease {
			snapshot.GitHubRelease = PublicationState{State: "present", ReasonCode: "github-release-present"}
		} else {
			snapshot.GitHubRelease = PublicationState{State: "incomplete", ReasonCode: "github-release-metadata-incomplete"}
		}
		assets, snapshot.Assets = observeFailedReleaseAssets(release, options)
		snapshot.Attestations = observeFailedAttestations(ctx, api, options, assets, snapshot.Assets)
	case remoteErrorCode(releaseErr) == "NOT_FOUND":
		snapshot.GitHubRelease = PublicationState{State: "absent", ReasonCode: "github-release-absent"}
		snapshot.Assets = PublicationState{State: "absent", ReasonCode: "release-assets-absent"}
		snapshot.Attestations = PublicationState{State: "unknown", ReasonCode: "attestation-subjects-unavailable"}
	default:
		reason := remoteReasonCode(releaseErr)
		snapshot.GitHubRelease = PublicationState{State: "unknown", ReasonCode: reason}
		snapshot.Assets = PublicationState{State: "unknown", ReasonCode: reason}
		snapshot.Attestations = PublicationState{State: "unknown", ReasonCode: reason}
	}

	homebrew, homebrewState := observeFailedHomebrew(ctx, api, options)
	snapshot.Homebrew = homebrewState
	checks := []Check{
		publicationStateCheck("github-release-state", "GET exact GitHub Release", snapshot.GitHubRelease),
		publicationStateCheck("release-asset-state", "GET exact GitHub Release asset inventory and digests", snapshot.Assets),
		publicationStateCheck("attestation-state", "GET exact archive attestation record inventory", snapshot.Attestations),
		publicationStateCheck("homebrew-state", "GET deterministic Homebrew PR and exact CI state", snapshot.Homebrew),
	}
	return snapshot, assets, homebrew, checks
}

func observeFailedReleaseAssets(release apiRelease, options CollectOptions) ([]Asset, PublicationState) {
	if release.Assets == nil {
		return []Asset{}, PublicationState{State: "unknown", ReasonCode: "release-asset-schema-unknown"}
	}
	if len(release.Assets) == 0 {
		return []Asset{}, PublicationState{State: "absent", ReasonCode: "release-assets-absent"}
	}
	remote := make(map[string]string, len(release.Assets))
	valid, schemaValid := true, true
	for _, asset := range release.Assets {
		digest, ok := strings.CutPrefix(asset.Digest, "sha256:")
		if asset.Name == "" || filepath.Base(asset.Name) != asset.Name || !ok || !validDigest(digest) || asset.State == "" {
			schemaValid = false
			continue
		}
		if asset.State != "uploaded" || remote[asset.Name] != "" {
			valid = false
			continue
		}
		remote[asset.Name] = digest
	}
	assets := make([]Asset, 0, len(remote))
	for _, name := range options.Contract.Assets {
		digest := remote[name]
		if digest == "" {
			valid = false
			continue
		}
		assets = append(assets, Asset{Name: name, SHA256: digest, SourceSHA: options.SourceSHA})
	}
	if len(remote) != len(options.Contract.Assets) {
		valid = false
	}
	if !schemaValid {
		return assets, PublicationState{State: "unknown", ReasonCode: "release-asset-schema-unknown"}
	}
	if valid {
		return assets, PublicationState{State: "present", ReasonCode: "release-assets-present"}
	}
	return assets, PublicationState{State: "incomplete", ReasonCode: "release-assets-incomplete"}
}

func observeFailedAttestations(ctx context.Context, api apiClient, options CollectOptions, assets []Asset, assetState PublicationState) PublicationState {
	if assetState.State == "absent" {
		return PublicationState{State: "unknown", ReasonCode: "attestation-subjects-unavailable"}
	}
	if assetState.State != "present" {
		return PublicationState{State: "incomplete", ReasonCode: "attestation-subjects-incomplete"}
	}
	byName := make(map[string]string, len(assets))
	for _, asset := range assets {
		byName[asset.Name] = asset.SHA256
	}
	observed, expected := 0, len(options.Contract.Platforms)*2
	for _, platform := range options.Contract.Platforms {
		digest := byName[platform.Archive]
		if !validDigest(digest) {
			return PublicationState{State: "incomplete", ReasonCode: "attestation-subjects-incomplete"}
		}
		for _, predicate := range []string{"https://slsa.dev/provenance/v1", "https://spdx.dev/Document/v2.3"} {
			endpoint := fmt.Sprintf("repos/%s/attestations/sha256:%s", options.Repository, digest)
			var response apiAttestations
			if err := api.get(ctx, endpoint, map[string]string{"predicate_type": predicate, "per_page": "100"}, &response); err != nil {
				return PublicationState{State: "unknown", ReasonCode: remoteReasonCode(err)}
			}
			if response.Attestations == nil || len(response.Attestations) >= 100 {
				return PublicationState{State: "unknown", ReasonCode: "attestation-schema-unknown"}
			}
			if len(response.Attestations) > 0 {
				for _, record := range response.Attestations {
					if len(record) == 0 || string(record) == "null" || !json.Valid(record) {
						return PublicationState{State: "unknown", ReasonCode: "attestation-schema-unknown"}
					}
				}
				observed++
			}
		}
	}
	switch {
	case observed == 0:
		return PublicationState{State: "absent", ReasonCode: "attestations-absent"}
	case observed == expected:
		// A failed publisher snapshot records durable presence only. The normal
		// published evidence path performs cryptographic verification.
		return PublicationState{State: "incomplete", ReasonCode: "attestations-present-unverified"}
	default:
		return PublicationState{State: "incomplete", ReasonCode: "attestations-incomplete"}
	}
}

func observeFailedHomebrew(ctx context.Context, api apiClient, options CollectOptions) (*Homebrew, PublicationState) {
	tap, ok := options.Contract.AppByID("homebrew_tap")
	if !ok {
		return nil, PublicationState{State: "unknown", ReasonCode: "homebrew-contract-unknown"}
	}
	branch := "release/env-vault-" + options.Version
	owner, _, _ := strings.Cut(tap.Repository, "/")
	pulls, err := getAllArrayPages[apiPull](ctx, api, fmt.Sprintf("repos/%s/pulls", tap.Repository), map[string]string{"base": "main", "head": owner + ":" + branch, "state": "all"})
	if err != nil {
		return nil, PublicationState{State: "unknown", ReasonCode: remoteReasonCode(err)}
	}
	if len(pulls) == 0 {
		return nil, PublicationState{State: "absent", ReasonCode: "homebrew-pr-absent"}
	}
	if len(pulls) != 1 {
		return nil, PublicationState{State: "incomplete", ReasonCode: "homebrew-state-incomplete"}
	}
	pull := pulls[0]
	if pull.Number <= 0 || pull.State == "" || pull.Head.Ref == "" || !validSHA(pull.Head.SHA) || pull.Base.Ref == "" || pull.Head.Repo.FullName == "" || pull.Base.Repo.FullName == "" {
		return nil, PublicationState{State: "unknown", ReasonCode: "homebrew-schema-unknown"}
	}
	if pull.State != "closed" || pull.MergedAt == nil || !validSHA(pull.MergeCommitSHA) {
		return nil, PublicationState{State: "incomplete", ReasonCode: "homebrew-state-incomplete"}
	}
	homebrew, err := collectHomebrew(ctx, api, options)
	if err == nil {
		return homebrew, PublicationState{State: "present", ReasonCode: "homebrew-state-present"}
	}
	if isRemoteObservationError(err) {
		return nil, PublicationState{State: "unknown", ReasonCode: remoteReasonCode(err)}
	}
	return nil, PublicationState{State: "unknown", ReasonCode: "homebrew-state-unverified"}
}

func publicationStateCheck(id, command string, state PublicationState) Check {
	result := "fail"
	if state.State == "present" {
		result = "pass"
	} else if state.State == "unknown" {
		result = "unknown"
	}
	return Check{ID: id, Command: command, Result: result, Source: "github_api"}
}

func publicationSnapshotUnknown(snapshot PublicationSnapshot) bool {
	for _, state := range []PublicationState{snapshot.GitHubRelease, snapshot.Assets, snapshot.Attestations, snapshot.Homebrew} {
		if state.State == "unknown" {
			return true
		}
	}
	return false
}

func isRemoteObservationError(err error) bool {
	var remote *remoteObservationError
	return errors.As(err, &remote)
}

func remoteErrorCode(err error) string {
	var command *githubCommandError
	if errors.As(err, &command) {
		return command.Code
	}
	return "MALFORMED_RESPONSE"
}

func remoteReasonCode(err error) string {
	code := strings.ToLower(remoteErrorCode(err))
	code = strings.ReplaceAll(code, "_", "-")
	return "remote-" + code
}

func repositoryForApp(contract releasecontract.Contract, id string) string {
	app, ok := contract.AppByID(id)
	if !ok {
		return ""
	}
	return app.Repository
}

func collectExactRun(ctx context.Context, api apiClient, repository string, runID int64, attempt int) (apiRun, []apiJob, []apiJob, error) {
	var run apiRun
	if err := api.get(ctx, fmt.Sprintf("repos/%s/actions/runs/%d", repository, runID), nil, &run); err != nil {
		return apiRun{}, nil, nil, err
	}
	if run.ID != runID || run.RunAttempt != attempt {
		return apiRun{}, nil, nil, errors.New("workflow run ID or attempt changed")
	}
	if run.CreatedAt.IsZero() || run.UpdatedAt.IsZero() {
		return apiRun{}, nil, nil, errors.New("workflow run completion timestamp is missing")
	}
	endpoint := fmt.Sprintf("repos/%s/actions/runs/%d/attempts/%d/jobs", repository, runID, attempt)
	jobs, err := getAllJobPages(ctx, api, endpoint, nil)
	if err != nil {
		return apiRun{}, nil, nil, err
	}
	for index := range jobs {
		if jobs[index].RunAttempt == 0 {
			jobs[index].RunAttempt = attempt
		}
		if jobs[index].RunAttempt != attempt {
			return apiRun{}, nil, nil, errors.New("attempt jobs response contains another attempt")
		}
	}
	allEndpoint := fmt.Sprintf("repos/%s/actions/runs/%d/jobs", repository, runID)
	allJobs, err := getAllJobPages(ctx, api, allEndpoint, map[string]string{"filter": "all"})
	if err != nil {
		return apiRun{}, nil, nil, err
	}
	attemptCounts := make([]int, attempt)
	latestIDs := make(map[int64]bool, len(jobs))
	for _, job := range jobs {
		latestIDs[job.ID] = true
	}
	observedLatestIDs := make(map[int64]bool, len(jobs))
	for _, job := range allJobs {
		if job.RunAttempt <= 0 || job.RunAttempt > attempt {
			return apiRun{}, nil, nil, errors.New("all-attempt jobs response has an invalid attempt")
		}
		attemptCounts[job.RunAttempt-1]++
		if job.RunAttempt == attempt {
			observedLatestIDs[job.ID] = true
		}
	}
	for index, count := range attemptCounts {
		if count == 0 {
			return apiRun{}, nil, nil, fmt.Errorf("all-attempt jobs response is missing attempt %d", index+1)
		}
	}
	if len(latestIDs) != len(observedLatestIDs) {
		return apiRun{}, nil, nil, errors.New("all-attempt jobs response does not contain the exact latest attempt")
	}
	for id := range latestIDs {
		if !observedLatestIDs[id] {
			return apiRun{}, nil, nil, errors.New("all-attempt jobs response does not contain the exact latest attempt")
		}
	}
	return run, jobs, allJobs, nil
}

func getAllJobPages(ctx context.Context, api apiClient, endpoint string, query map[string]string) ([]apiJob, error) {
	base := map[string]string{"per_page": "100"}
	for key, value := range query {
		base[key] = value
	}
	result := []apiJob{}
	seen := map[int64]bool{}
	expected := -1
	for page := 1; page <= 100; page++ {
		base["page"] = strconv.Itoa(page)
		var response apiJobs
		if err := api.get(ctx, endpoint, base, &response); err != nil {
			return nil, err
		}
		if expected == -1 {
			expected = response.TotalCount
		} else if response.TotalCount != expected {
			return nil, errors.New("jobs total changed during pagination")
		}
		if expected <= 0 || len(response.Jobs) == 0 {
			return nil, errors.New("jobs response is incomplete or empty")
		}
		for _, job := range response.Jobs {
			if job.ID <= 0 || seen[job.ID] {
				return nil, errors.New("jobs response contains an invalid or duplicated job")
			}
			seen[job.ID] = true
			result = append(result, job)
		}
		if len(result) == expected {
			return result, nil
		}
		if len(result) > expected {
			return nil, errors.New("jobs response exceeds declared total")
		}
	}
	return nil, errors.New("jobs pagination exceeded the fail-closed page limit")
}

func workflowRunEvidence(code string, run apiRun, jobs, allJobs []apiJob) WorkflowRun {
	result := WorkflowRun{Workflow: code, Event: run.Event, RunID: run.ID, RunAttempt: run.RunAttempt, HeadSHA: run.HeadSHA, Conclusion: run.Conclusion, RetryCount: run.RunAttempt - 1, JobCount: len(jobs), ExecutedJobCount: len(allJobs), TimingComplete: true, UnavailableReasonCodes: []string{}, Attempts: make([]AttemptMetrics, run.RunAttempt)}
	if code == "publisher" {
		switch run.Event {
		case "push":
			result.RepairMode = "none"
		case "workflow_dispatch":
			if match := repairRunTitlePattern.FindStringSubmatch(run.DisplayTitle); len(match) == 4 {
				result.RepairMode, result.RepairStateDigest = match[2], match[3]
			}
		}
	}
	for index := range result.Attempts {
		result.Attempts[index].Attempt = index + 1
	}
	if run.CreatedAt.IsZero() || run.RunStartedAt.IsZero() || run.RunStartedAt.Before(run.CreatedAt) {
		result.TimingComplete = false
		result.UnavailableReasonCodes = append(result.UnavailableReasonCodes, "run_queue_timestamps_unavailable")
	} else {
		result.QueueSeconds = secondsBetween(run.CreatedAt, run.RunStartedAt)
	}
	var completion time.Time
	for _, job := range allJobs {
		result.Attempts[job.RunAttempt-1].JobCount++
		if job.StartedAt.IsZero() || job.CompletedAt.IsZero() || job.CompletedAt.Before(job.StartedAt) {
			result.TimingComplete = false
			result.UnavailableReasonCodes = append(result.UnavailableReasonCodes, "job_timestamps_unavailable")
			continue
		}
		duration := secondsBetween(job.StartedAt, job.CompletedAt)
		result.AggregateRunnerSeconds += duration
		result.Attempts[job.RunAttempt-1].RunnerSeconds += duration
		if job.RunAttempt == run.RunAttempt && job.CompletedAt.After(completion) {
			completion = job.CompletedAt
		}
		for _, step := range job.Steps {
			kind := releasemetrics.ClassifyStep(step.Name)
			if kind == releasemetrics.StepOther {
				continue
			}
			if step.StartedAt.IsZero() || step.CompletedAt.IsZero() || step.CompletedAt.Before(step.StartedAt) {
				result.TimingComplete = false
				if kind == releasemetrics.StepCache {
					result.UnavailableReasonCodes = append(result.UnavailableReasonCodes, "cache_step_timestamps_unavailable")
				} else {
					result.UnavailableReasonCodes = append(result.UnavailableReasonCodes, "artifact_step_timestamps_unavailable")
				}
				continue
			}
			duration := secondsBetween(step.StartedAt, step.CompletedAt)
			if kind == releasemetrics.StepCache {
				result.CacheSeconds += duration
			} else {
				result.ArtifactTransferSeconds += duration
			}
		}
	}
	result.AttemptRunnerSeconds = result.Attempts[len(result.Attempts)-1].RunnerSeconds
	if completion.IsZero() || run.CreatedAt.IsZero() || run.RunStartedAt.IsZero() {
		result.TimingComplete = false
		result.UnavailableReasonCodes = append(result.UnavailableReasonCodes, "workflow_completion_timestamp_unavailable")
	} else {
		result.WallSeconds = secondsBetween(run.CreatedAt, completion)
		result.CriticalPathSeconds = secondsBetween(run.RunStartedAt, completion)
	}
	result.UnavailableReasonCodes = uniqueStrings(result.UnavailableReasonCodes)
	return result
}

func collectPublisherLineage(ctx context.Context, api apiClient, options CollectOptions, currentRun apiRun, current WorkflowRun) ([]WorkflowRun, error) {
	workflow, ok := options.Contract.WorkflowByID("publisher")
	if !ok {
		return nil, errors.New("publisher workflow contract is missing")
	}
	endpoint := fmt.Sprintf("repos/%s/actions/workflows/%s/runs", options.Repository, workflow.File)
	queries := []map[string]string{
		{"branch": options.Version, "event": "push", "status": "completed"},
		{"branch": options.Version, "event": "workflow_dispatch", "status": "completed"},
	}
	byID := map[int64]apiRun{}
	for _, query := range queries {
		runs, err := getAllRunPages(ctx, api, endpoint, query)
		if err != nil {
			return nil, err
		}
		for _, run := range runs {
			if run.Name == workflow.Name && run.Path == ".github/workflows/"+workflow.File && run.Status == "completed" && publisherRunMatchesRelease(run, options) && (run.CreatedAt.Before(currentRun.CreatedAt) || run.CreatedAt.Equal(currentRun.CreatedAt) && run.ID <= currentRun.ID) {
				byID[run.ID] = run
			}
		}
	}
	if _, ok := byID[current.RunID]; !ok {
		return nil, errors.New("current publisher run is missing from exact retry lineage")
	}
	runs := make([]apiRun, 0, len(byID))
	for _, run := range byID {
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].ID < runs[j].ID
		}
		return runs[i].CreatedAt.Before(runs[j].CreatedAt)
	})
	lineage := make([]WorkflowRun, 0, len(runs))
	for _, run := range runs {
		if run.ID == current.RunID {
			lineage = append(lineage, current)
			continue
		}
		exact, jobs, allJobs, err := collectExactRun(ctx, api, options.Repository, run.ID, run.RunAttempt)
		if err != nil {
			return nil, err
		}
		lineage = append(lineage, workflowRunEvidence("publisher", exact, jobs, allJobs))
	}
	return lineage, nil
}

func getAllRunPages(ctx context.Context, api apiClient, endpoint string, query map[string]string) ([]apiRun, error) {
	base := map[string]string{"per_page": "100"}
	for key, value := range query {
		base[key] = value
	}
	result := []apiRun{}
	seen := map[int64]bool{}
	expected := -1
	for page := 1; page <= 100; page++ {
		base["page"] = strconv.Itoa(page)
		var response apiRuns
		if err := api.get(ctx, endpoint, base, &response); err != nil {
			return nil, err
		}
		if expected == -1 {
			expected = response.TotalCount
		} else if response.TotalCount != expected {
			return nil, errors.New("workflow run total changed during pagination")
		}
		if expected == 0 {
			return result, nil
		}
		if len(response.WorkflowRuns) == 0 {
			return nil, errors.New("workflow runs response is incomplete")
		}
		for _, run := range response.WorkflowRuns {
			if run.ID <= 0 || seen[run.ID] {
				return nil, errors.New("workflow runs response contains an invalid or duplicated run")
			}
			seen[run.ID] = true
			result = append(result, run)
		}
		if len(result) == expected {
			return result, nil
		}
		if len(result) > expected {
			return nil, errors.New("workflow runs response exceeds declared total")
		}
	}
	return nil, errors.New("workflow run pagination exceeded the fail-closed page limit")
}

func secondsBetween(start, end time.Time) int64 {
	if end.Before(start) {
		return 0
	}
	return int64(end.Sub(start) / time.Second)
}

func resolveExactTag(ctx context.Context, api apiClient, repository, version string) (string, error) {
	endpoint := fmt.Sprintf("repos/%s/git/ref/tags/%s", repository, version)
	var reference apiTagReference
	if err := api.get(ctx, endpoint, nil, &reference); err != nil {
		return "", err
	}
	if reference.Ref != "refs/tags/"+version {
		return "", errors.New("tag reference name is malformed")
	}
	object := reference.Object
	seen := map[string]bool{}
	for depth := 0; depth < 8; depth++ {
		if !validSHA(object.SHA) {
			return "", errors.New("tag object SHA is malformed")
		}
		switch object.Type {
		case "commit":
			return object.SHA, nil
		case "tag":
			if seen[object.SHA] {
				return "", errors.New("annotated tag cycle")
			}
			seen[object.SHA] = true
			var annotated apiAnnotatedTag
			if err := api.get(ctx, fmt.Sprintf("repos/%s/git/tags/%s", repository, object.SHA), nil, &annotated); err != nil {
				return "", err
			}
			object = annotated.Object
		default:
			return "", errors.New("tag reference points to an unsupported object")
		}
	}
	return "", errors.New("annotated tag chain exceeds the deterministic bound")
}

func getRelease(ctx context.Context, api apiClient, repository, version string) (apiRelease, error) {
	var result apiRelease
	err := api.get(ctx, fmt.Sprintf("repos/%s/releases/tags/%s", repository, version), nil, &result)
	return result, err
}

func verifyReleaseAssets(release apiRelease, options CollectOptions) ([]Asset, map[string]string, error) {
	if release.TagName != options.Version || release.Draft || release.Prerelease || len(release.Assets) != len(options.Contract.Assets) {
		return nil, nil, errors.New("GitHub Release metadata or asset count does not match the contract")
	}
	remote := make(map[string]string, len(release.Assets))
	for _, asset := range release.Assets {
		digest, ok := strings.CutPrefix(asset.Digest, "sha256:")
		if !ok || !validDigest(digest) || asset.State != "uploaded" || remote[asset.Name] != "" {
			return nil, nil, fmt.Errorf("release asset %q has invalid state, digest, or duplicate identity", asset.Name)
		}
		remote[asset.Name] = digest
	}
	assets := make([]Asset, 0, len(options.Contract.Assets))
	archives := make(map[string]string, len(options.Contract.Platforms))
	for _, name := range options.Contract.Assets {
		remoteDigest, ok := remote[name]
		if !ok {
			return nil, nil, fmt.Errorf("release contract asset %q is missing", name)
		}
		path := filepath.Join(options.AssetsDirectory, name)
		localDigest, err := sha256File(path)
		if err != nil || localDigest != remoteDigest {
			return nil, nil, fmt.Errorf("local asset %q does not match the release digest", name)
		}
		assets = append(assets, Asset{Name: name, SHA256: localDigest, SourceSHA: options.SourceSHA})
	}
	for _, platform := range options.Contract.Platforms {
		archiveDigest := remote[platform.Archive]
		checksumPath := filepath.Join(options.AssetsDirectory, platform.Checksum)
		if err := verifyChecksumFile(checksumPath, platform.Archive, archiveDigest); err != nil {
			return nil, nil, err
		}
		archives[platform.Archive] = archiveDigest
	}
	return assets, archives, nil
}

func sha256File(filename string) (string, error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func verifyChecksumFile(filename, archive, digest string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	want := digest + "  " + archive + "\n"
	if string(data) != want {
		return fmt.Errorf("checksum file %q does not bind the exact archive digest and name", filepath.Base(filename))
	}
	return nil
}

type verificationEntry struct {
	VerificationResult struct {
		Signature struct {
			Certificate struct {
				GitHubWorkflowName       string `json:"githubWorkflowName"`
				GitHubWorkflowRepository string `json:"githubWorkflowRepository"`
				SourceRepositoryDigest   string `json:"sourceRepositoryDigest"`
				RunInvocationURI         string `json:"runInvocationURI"`
			} `json:"certificate"`
		} `json:"signature"`
		Statement struct {
			Subject       []verificationSubject `json:"subject"`
			PredicateType string                `json:"predicateType"`
		} `json:"statement"`
	} `json:"verificationResult"`
}

type verificationSubject struct {
	Name   string `json:"name"`
	Digest struct {
		SHA256 string `json:"sha256"`
	} `json:"digest"`
}

var invocationPattern = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/actions/runs/([1-9][0-9]*)/attempts/([1-9][0-9]*)$`)

func verifyAttestations(ctx context.Context, runner CommandRunner, options CollectOptions, archives map[string]string, workflow releasecontract.Workflow) ([]Attestation, error) {
	workflowIdentity := options.Repository + "/.github/workflows/" + workflow.File
	result := make([]Attestation, 0, 2)
	kinds := []struct{ code, predicate string }{{"provenance", "https://slsa.dev/provenance/v1"}, {"sbom", "https://spdx.dev/Document/v2.3"}}
	for _, kind := range kinds {
		var observed *Attestation
		for _, platform := range options.Contract.Platforms {
			path := filepath.Join(options.AssetsDirectory, platform.Archive)
			args := []string{"attestation", "verify", path, "--repo", options.Repository, "--signer-workflow", workflowIdentity, "--source-digest", options.SourceSHA, "--predicate-type", kind.predicate, "--format", "json"}
			data, err := runner.Run(ctx, args)
			if err != nil {
				return nil, fmt.Errorf("verify %s attestation for %s: %w", kind.code, platform.Archive, err)
			}
			attestation, err := parseAttestation(data, kind.code, kind.predicate, options, archives, workflow)
			if err != nil {
				return nil, err
			}
			if observed == nil {
				copy := attestation
				observed = &copy
			} else if observed.RunID != attestation.RunID || observed.RunAttempt != attestation.RunAttempt || !sameStrings(observed.SubjectSHA256s, attestation.SubjectSHA256s) {
				return nil, fmt.Errorf("%s archive verification resolved inconsistent attestations", kind.code)
			}
		}
		result = append(result, *observed)
	}
	return result, nil
}

func parseAttestation(data []byte, kind, predicate string, options CollectOptions, archives map[string]string, workflow releasecontract.Workflow) (Attestation, error) {
	var entries []verificationEntry
	if err := decodeSingleJSON(data, &entries); err != nil || len(entries) != 1 {
		return Attestation{}, fmt.Errorf("%s verification must return exactly one attestation", kind)
	}
	verification := entries[0].VerificationResult
	certificate := verification.Signature.Certificate
	if certificate.GitHubWorkflowName != workflow.Name || certificate.GitHubWorkflowRepository != options.Repository || certificate.SourceRepositoryDigest != options.SourceSHA || verification.Statement.PredicateType != predicate {
		return Attestation{}, fmt.Errorf("%s attestation certificate or predicate does not match the exact release", kind)
	}
	match := invocationPattern.FindStringSubmatch(certificate.RunInvocationURI)
	if len(match) != 4 || match[1] != options.Repository {
		return Attestation{}, fmt.Errorf("%s attestation has an invalid run invocation", kind)
	}
	runID, err1 := strconv.ParseInt(match[2], 10, 64)
	attempt, err2 := strconv.Atoi(match[3])
	if err1 != nil || err2 != nil {
		return Attestation{}, fmt.Errorf("%s attestation has an invalid run invocation", kind)
	}
	subjects := make(map[string]string, len(verification.Statement.Subject))
	for _, subject := range verification.Statement.Subject {
		if subjects[subject.Name] != "" || !validDigest(subject.Digest.SHA256) {
			return Attestation{}, fmt.Errorf("%s attestation has an invalid subject", kind)
		}
		subjects[subject.Name] = subject.Digest.SHA256
	}
	if len(subjects) != len(archives) {
		return Attestation{}, fmt.Errorf("%s attestation subject matrix is incomplete", kind)
	}
	digests := make([]string, 0, len(archives))
	for _, platform := range options.Contract.Platforms {
		if subjects[platform.Archive] != archives[platform.Archive] {
			return Attestation{}, fmt.Errorf("%s attestation subject for %s does not match", kind, platform.Archive)
		}
		digests = append(digests, archives[platform.Archive])
	}
	return Attestation{Kind: kind, SubjectSHA256s: digests, SourceSHA: options.SourceSHA, Workflow: workflow.File, RunID: runID, RunAttempt: attempt}, nil
}

func collectHomebrew(ctx context.Context, api apiClient, options CollectOptions) (*Homebrew, error) {
	tap, ok := options.Contract.AppByID("homebrew_tap")
	if !ok {
		return nil, errors.New("Homebrew tap contract is missing")
	}
	branch := "release/env-vault-" + options.Version
	pulls, err := getAllArrayPages[apiPull](ctx, api, fmt.Sprintf("repos/%s/pulls", tap.Repository), map[string]string{"base": "main", "head": strings.Split(tap.Repository, "/")[0] + ":" + branch, "state": "all"})
	if err != nil {
		return nil, err
	}
	var matches []apiPull
	markerPattern := regexp.MustCompile(`(?m)^<!-- env-vault-release version=` + regexp.QuoteMeta(options.Version) + ` source_sha=` + regexp.QuoteMeta(options.SourceSHA) + ` formula_sha256=([0-9a-f]{64}) -->$`)
	for _, pull := range pulls {
		if pull.State == "closed" && pull.MergedAt != nil && pull.MergeCommitSHA != "" && !pull.Draft && pull.Title == "env-vault "+options.Version && pull.User.Login == tap.Slug+"[bot]" && pull.Head.Ref == branch && pull.Head.Repo.FullName == tap.Repository && pull.Base.Ref == "main" && pull.Base.Repo.FullName == tap.Repository && strings.Count(pull.Body, "<!-- env-vault-release ") == 1 && len(markerPattern.FindAllString(pull.Body, -1)) == 1 {
			matches = append(matches, pull)
		}
	}
	if len(matches) != 1 || !validSHA(matches[0].Head.SHA) || !validSHA(matches[0].MergeCommitSHA) {
		return nil, errors.New("exactly one merged exact-source Homebrew PR is required")
	}
	pull := matches[0]
	markerMatch := markerPattern.FindStringSubmatch(pull.Body)
	if len(markerMatch) != 2 {
		return nil, errors.New("Homebrew PR exact-source marker is malformed")
	}
	files, err := getAllArrayPages[apiPullFile](ctx, api, fmt.Sprintf("repos/%s/pulls/%d/files", tap.Repository, pull.Number), nil)
	if err != nil || len(files) != 1 || files[0].Filename != "Formula/env-vault.rb" {
		return nil, errors.New("Homebrew PR must change only Formula/env-vault.rb")
	}
	headFormula, err := tapFormulaAtCommit(ctx, api, tap.Repository, pull.Head.SHA)
	if err != nil {
		return nil, errors.New("exact Homebrew PR-head formula is unavailable")
	}
	mergeFormula, err := tapFormulaAtCommit(ctx, api, tap.Repository, pull.MergeCommitSHA)
	if err != nil {
		return nil, errors.New("post-merge Homebrew formula is unavailable")
	}
	if err := validateTapFormulaLineage(headFormula, mergeFormula, strings.TrimPrefix(options.Version, "v"), markerMatch[1]); err != nil {
		return nil, err
	}
	if err := validateTapFormulaAssets(mergeFormula.Content, options, tap.Repository); err != nil {
		return nil, err
	}
	prRun, err := exactTapRun(ctx, api, tap, pull.Head.SHA, pull.Head.Ref, "pull_request")
	if err != nil {
		return nil, err
	}
	mergeRun, err := exactTapRun(ctx, api, tap, pull.MergeCommitSHA, pull.Base.Ref, "push")
	if err != nil {
		return nil, err
	}
	return &Homebrew{
		State: "complete", PullRequest: pull.Number, PRHeadSHA: pull.Head.SHA, MergeSHA: pull.MergeCommitSHA, TapVersion: mergeFormula.Version,
		PRHeadCI:       RunReference{RunID: prRun.ID, RunAttempt: prRun.RunAttempt, HeadSHA: prRun.HeadSHA, Conclusion: prRun.Conclusion},
		PostMergeTapCI: RunReference{RunID: mergeRun.ID, RunAttempt: mergeRun.RunAttempt, HeadSHA: mergeRun.HeadSHA, Conclusion: mergeRun.Conclusion},
	}, nil
}

func exactTapRun(ctx context.Context, api apiClient, tap releasecontract.App, sha, branch, event string) (apiRun, error) {
	endpoint := fmt.Sprintf("repos/%s/actions/workflows/%s/runs", tap.Repository, tap.CIWorkflowFile)
	runs, err := getAllRunPages(ctx, api, endpoint, map[string]string{"branch": branch, "event": event, "head_sha": sha, "status": "success"})
	if err != nil {
		return apiRun{}, err
	}
	var matches []apiRun
	for _, run := range runs {
		if run.Name == tap.CIWorkflowName && run.Path == ".github/workflows/"+tap.CIWorkflowFile && run.Event == event && run.HeadSHA == sha && run.Status == "completed" && run.Conclusion == "success" && run.RunAttempt > 0 {
			matches = append(matches, run)
		}
	}
	if len(matches) != 1 {
		return apiRun{}, fmt.Errorf("exact tap %s CI run count=%d, want 1", event, len(matches))
	}
	return matches[0], nil
}

type tapFormula struct {
	Version string
	SHA256  string
	Content []byte
}

func validateTapFormulaLineage(head, merge tapFormula, version, markerSHA256 string) error {
	if head.Version != version || merge.Version != version {
		return errors.New("Homebrew PR-head or post-merge formula version does not match the release")
	}
	if !validDigest(markerSHA256) || head.SHA256 != markerSHA256 {
		return errors.New("Homebrew PR marker does not bind the exact PR-head formula")
	}
	if merge.SHA256 != head.SHA256 || !bytes.Equal(merge.Content, head.Content) {
		return errors.New("post-merge Homebrew formula differs from the exact reviewed PR head")
	}
	return nil
}

func tapFormulaAtCommit(ctx context.Context, api apiClient, repository, sha string) (tapFormula, error) {
	var content apiContent
	if err := api.get(ctx, fmt.Sprintf("repos/%s/contents/Formula/env-vault.rb", repository), map[string]string{"ref": sha}, &content); err != nil {
		return tapFormula{}, err
	}
	if content.Type != "file" || content.Encoding != "base64" {
		return tapFormula{}, errors.New("Homebrew formula content response is malformed")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(content.Content, "\n", ""))
	if err != nil {
		return tapFormula{}, err
	}
	pattern := regexp.MustCompile(`(?m)^[[:space:]]*version "((?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))"[[:space:]]*$`)
	matches := pattern.FindAllSubmatch(decoded, -1)
	if len(matches) != 1 {
		return tapFormula{}, errors.New("Homebrew formula must contain exactly one strict version")
	}
	digest := sha256.Sum256(decoded)
	return tapFormula{Version: string(matches[0][1]), SHA256: hex.EncodeToString(digest[:]), Content: decoded}, nil
}

func validateTapFormulaAssets(content []byte, options CollectOptions, tapRepository string) error {
	byName := map[string]string{}
	for _, platform := range options.Contract.Platforms {
		if platform.GOOS == "windows" {
			continue
		}
		archiveDigest, err := sha256File(filepath.Join(options.AssetsDirectory, platform.Archive))
		if err != nil {
			return err
		}
		byName[platform.Archive] = archiveDigest
	}
	if len(byName) != 4 {
		return errors.New("Homebrew formula contract must contain four non-Windows release archives")
	}
	formula := string(content)
	releaseURLCount, shaCount := strings.Count(formula, `url "https://github.com/`), strings.Count(formula, `sha256 "`)
	if releaseURLCount != len(byName) || shaCount != len(byName) {
		return errors.New("Homebrew formula has an unexpected URL or checksum count")
	}
	for name, digest := range byName {
		pair := `url "https://github.com/` + options.Repository + `/releases/download/` + options.Version + `/` + name + `"` + "\n" + `      sha256 "` + digest + `"`
		if strings.Count(formula, pair) != 1 {
			return fmt.Errorf("Homebrew formula does not bind exact release archive %q", name)
		}
	}
	if tapRepository == options.Repository {
		return errors.New("Homebrew tap repository must remain external to the source repository")
	}
	return nil
}

func collectReleasePullRequest(ctx context.Context, api apiClient, options CollectOptions) (*PullRequest, error) {
	pulls, err := getAllArrayPages[apiPull](ctx, api, fmt.Sprintf("repos/%s/commits/%s/pulls", options.Repository, options.SourceSHA), nil)
	if err != nil {
		return nil, err
	}
	planningApp, ok := options.Contract.AppByID("release_planning")
	if !ok {
		return nil, errors.New("release planning App contract is missing")
	}
	expectedTitle := "chore(main): release env-vault " + options.Version
	expectedHead := "release-please--branches--main--components--env-vault"
	var matches []apiPull
	for _, pull := range pulls {
		labels := map[string]bool{}
		for _, label := range pull.Labels {
			labels[label.Name] = true
		}
		if pull.MergedAt != nil && pull.MergeCommitSHA == options.SourceSHA && pull.Base.Ref == "main" && pull.Base.Repo.FullName == options.Repository && pull.Head.Ref == expectedHead && pull.Head.Repo.FullName == options.Repository && pull.User.Login == planningApp.Slug+"[bot]" && pull.Title == expectedTitle && strings.Contains(pull.Body, "This PR was generated with Release Please.") && strings.Contains(pull.Body, "Merging this reviewed pull request authorizes publication of this exact version after the merge commit passes main CI.") && labels["autorelease: tagged"] && !labels["autorelease: pending"] {
			matches = append(matches, pull)
		}
	}
	if len(matches) != 1 || !validSHA(matches[0].Head.SHA) {
		return nil, errors.New("exactly one merged generated release pull request is required")
	}
	return &PullRequest{Number: matches[0].Number, HeadSHA: matches[0].Head.SHA}, nil
}

func collectMainCI(ctx context.Context, api apiClient, options CollectOptions) (WorkflowRun, error) {
	run, err := findMainCIRun(ctx, api, options.Repository, options.SourceSHA, options.Contract)
	if err != nil {
		return WorkflowRun{}, err
	}
	run, jobs, allJobs, err := collectExactRun(ctx, api, options.Repository, run.ID, run.RunAttempt)
	if err != nil {
		return WorkflowRun{}, err
	}
	return workflowRunEvidence("ci", run, jobs, allJobs), nil
}

func findMainCIRun(ctx context.Context, api apiClient, repository, sourceSHA string, contract releasecontract.Contract) (apiRun, error) {
	workflow, ok := contract.WorkflowByID("ci")
	if !ok {
		return apiRun{}, errors.New("CI workflow contract is missing")
	}
	endpoint := fmt.Sprintf("repos/%s/actions/workflows/%s/runs", repository, workflow.File)
	runs, err := getAllRunPages(ctx, api, endpoint, map[string]string{"branch": "main", "event": "push", "head_sha": sourceSHA, "status": "success"})
	if err != nil {
		return apiRun{}, err
	}
	var matches []apiRun
	for _, run := range runs {
		if run.Name == workflow.Name && run.Path == ".github/workflows/"+workflow.File && run.Event == "push" && run.HeadBranch == "main" && run.HeadSHA == sourceSHA && run.Status == "completed" && run.Conclusion == "success" && run.RunAttempt > 0 {
			matches = append(matches, run)
		}
	}
	if len(matches) != 1 {
		return apiRun{}, fmt.Errorf("exact successful main CI run count=%d, want 1", len(matches))
	}
	return matches[0], nil
}

func getAllArrayPages[T any](ctx context.Context, api apiClient, endpoint string, query map[string]string) ([]T, error) {
	base := map[string]string{"per_page": "100"}
	for key, value := range query {
		base[key] = value
	}
	result := []T{}
	for page := 1; page <= 100; page++ {
		base["page"] = strconv.Itoa(page)
		var values []T
		if err := api.get(ctx, endpoint, base, &values); err != nil {
			return nil, err
		}
		if len(values) > 100 {
			return nil, errors.New("array response exceeds requested page size")
		}
		result = append(result, values...)
		if len(values) < 100 {
			return result, nil
		}
	}
	return nil, errors.New("array pagination exceeded the fail-closed page limit")
}

func decodeSingleJSON(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
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

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
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

func sameStrings(left, right []string) bool {
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
