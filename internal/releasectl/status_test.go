package releasectl

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
)

const (
	testRepository = "example/env-vault"
	testVersion    = "v0.0.8"
	testSHA        = "1111111111111111111111111111111111111111"
	otherSHA       = "2222222222222222222222222222222222222222"
	tapHeadSHA     = "3333333333333333333333333333333333333333"
	tapMergeSHA    = "4444444444444444444444444444444444444444"
	tapBaseSHA     = "5555555555555555555555555555555555555555"
)

type fixtureGitHub struct {
	tagObject         gitObject
	tagMissing        bool
	ciRuns            []workflowRun
	planningRuns      []workflowRun
	publisherRuns     []workflowRun
	repairRuns        []workflowRun
	jobs              []workflowJob
	jobsByRun         map[int64][]workflowJob
	artifacts         []workflowArtifact
	release           releaseResponse
	releaseMissing    bool
	attestations      map[string][]remoteAttestation
	tapPulls          []pullRequestResponse
	tapPullsSet       bool
	tapPRRuns         []workflowRun
	tapPRRunsSet      bool
	tapPushRuns       []workflowRun
	tapPushRunsSet    bool
	tapComparison     *compareResponse
	tapFormulaByRef   map[string][]byte
	runPages          map[string]workflowRunsResponse
	errors            map[string]error
	calls             []string
	verificationErr   error
	verificationCalls int
}

func (f *fixtureGitHub) Get(_ context.Context, endpoint string, query map[string]string, target any) error {
	f.calls = append(f.calls, endpoint)
	if err := f.errors[endpoint]; err != nil {
		return err
	}
	if page, ok := f.runPages[endpoint+"#"+query["page"]]; ok {
		response, ok := target.(*workflowRunsResponse)
		if !ok {
			return fmt.Errorf("unexpected paged workflow target %T", target)
		}
		*response = page
		return nil
	}
	switch {
	case endpoint == "repos/"+testRepository:
		response := target.(*repositoryResponse)
		response.FullName = testRepository
		response.DefaultBranch = "main"
	case endpoint == "repos/"+mustTestTapRepository():
		response := target.(*repositoryResponse)
		response.FullName = mustTestTapRepository()
		response.DefaultBranch = "main"
	case strings.Contains(endpoint, "/git/ref/tags/"):
		if f.tagMissing {
			return &apiError{Code: "NOT_FOUND", Endpoint: endpoint, HTTPStatus: 404}
		}
		response, ok := target.(*tagRefResponse)
		if !ok {
			return fmt.Errorf("unexpected tag target %T", target)
		}
		response.Object = f.tagObject
	case strings.Contains(endpoint, "/actions/workflows/ci.yml/runs"):
		response := target.(*workflowRunsResponse)
		response.WorkflowRuns = f.ciRuns
		response.TotalCount = len(response.WorkflowRuns)
	case strings.Contains(endpoint, "/actions/workflows/release-please.yml/runs"):
		response := target.(*workflowRunsResponse)
		response.WorkflowRuns = f.planningRuns
		response.TotalCount = len(response.WorkflowRuns)
	case strings.Contains(endpoint, "/actions/workflows/build-binaries.yml/runs"):
		response := target.(*workflowRunsResponse)
		if query["event"] == "workflow_dispatch" {
			response.WorkflowRuns = append([]workflowRun{}, f.repairRuns...)
		} else {
			response.WorkflowRuns = append([]workflowRun{}, f.publisherRuns...)
		}
		response.TotalCount = len(response.WorkflowRuns)
	case strings.Contains(endpoint, "/actions/workflows/test-formula.yml/runs"):
		response := target.(*workflowRunsResponse)
		if query["event"] == "pull_request" {
			if f.tapPRRunsSet {
				response.WorkflowRuns = append([]workflowRun{}, f.tapPRRuns...)
			} else {
				response.WorkflowRuns = []workflowRun{completedTapRun(401, "pull_request", "release/env-vault-"+f.release.TagName, tapHeadSHA)}
			}
		} else {
			if f.tapPushRunsSet {
				response.WorkflowRuns = append([]workflowRun{}, f.tapPushRuns...)
			} else {
				response.WorkflowRuns = []workflowRun{completedTapRun(402, "push", "main", tapMergeSHA)}
			}
		}
		response.TotalCount = len(response.WorkflowRuns)
	case strings.Contains(endpoint, "/attestations/sha256:"):
		response := target.(*attestationsResponse)
		if records, overridden := f.attestations[query["predicate_type"]]; overridden {
			response.Attestations = append([]remoteAttestation{}, records...)
		} else {
			response.Attestations = fixtureAttestations(f.release.Assets, query["predicate_type"])
		}
	case endpoint == "repos/"+mustTestTapRepository()+"/pulls":
		response := target.(*[]pullRequestResponse)
		if f.tapPullsSet {
			*response = append([]pullRequestResponse{}, f.tapPulls...)
		} else {
			*response = []pullRequestResponse{fixtureHomebrewPull(f.release.TagName)}
		}
	case endpoint == "repos/"+mustTestTapRepository()+"/contents/Formula/env-vault.rb":
		content := fixtureFormulaContent(f.release.TagName)
		if overridden, ok := f.tapFormulaByRef[query["ref"]]; ok {
			content = overridden
		}
		response := target.(*contentsResponse)
		response.Type = "file"
		response.Encoding = "base64"
		response.Size = int64(len(content))
		response.SHA = gitBlobSHA1(content)
		response.Path = "Formula/env-vault.rb"
		response.Content = base64.StdEncoding.EncodeToString(content)
	case strings.Contains(endpoint, "/compare/"):
		response := target.(*compareResponse)
		if f.tapComparison != nil {
			*response = *f.tapComparison
		} else {
			response.Status = "ahead"
			response.MergeBaseCommit.SHA = tapMergeSHA
		}
	case strings.Contains(endpoint, "/actions/runs/") && strings.HasSuffix(endpoint, "/artifacts"):
		response := target.(*artifactsResponse)
		response.Artifacts = append([]workflowArtifact{}, f.artifacts...)
		response.TotalCount = len(response.Artifacts)
	case strings.Contains(endpoint, "/actions/runs/") && strings.HasSuffix(endpoint, "/jobs"):
		response := target.(*jobsResponse)
		jobs := f.jobs
		for runID, candidate := range f.jobsByRun {
			if strings.Contains(endpoint, fmt.Sprintf("/actions/runs/%d/", runID)) {
				jobs = candidate
				break
			}
		}
		response.Jobs = append([]workflowJob{}, jobs...)
		response.TotalCount = len(response.Jobs)
	case strings.Contains(endpoint, "/releases/tags/"):
		if f.releaseMissing {
			return &apiError{Code: "NOT_FOUND", Endpoint: endpoint, HTTPStatus: 404}
		}
		response := target.(*releaseResponse)
		*response = f.release
	default:
		return fmt.Errorf("unexpected endpoint %s", endpoint)
	}
	return nil
}

func (f *fixtureGitHub) VerifyArtifactAttestations(_ context.Context, repository, sourceSHA, signerWorkflow string, assets []releaseAssetEvidence) error {
	f.verificationCalls++
	if repository != testRepository || sourceSHA != testSHA || signerWorkflow != testRepository+"/.github/workflows/build-binaries.yml" || len(assets) != 5 {
		return &attestationVerificationFailure{cause: errors.New("verification tuple mismatch")}
	}
	return f.verificationErr
}

func fixedClock() clock { return &fakeClock{now: time.Date(2026, 7, 16, 6, 17, 27, 0, time.UTC)} }

func TestSnapshotWithoutTagIsPendingAndDoesNotProbeLaterStages(t *testing.T) {
	github := &fixtureGitHub{tagMissing: true}
	doc, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository,
		Version:    testVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Overall.State != "pending" || doc.Overall.Terminal {
		t.Fatalf("overall=%+v", doc.Overall)
	}
	if doc.Stages.Tag.State != stateWaiting || doc.Stages.Tag.Reason != "tag_not_found" {
		t.Fatalf("tag=%+v", doc.Stages.Tag)
	}
	if len(github.calls) != 2 || !strings.Contains(github.calls[0], "/git/ref/tags/") || github.calls[1] != "repos/"+testRepository {
		t.Fatalf("calls=%v", github.calls)
	}
}

func TestSnapshotDoesNotTreatInaccessibleRepositoryAsMissingTag(t *testing.T) {
	tagEndpoint := "repos/" + testRepository + "/git/ref/tags/" + testVersion
	repositoryEndpoint := "repos/" + testRepository
	github := &fixtureGitHub{
		tagMissing: true,
		errors: map[string]error{
			repositoryEndpoint: &apiError{Code: "NOT_FOUND", Endpoint: repositoryEndpoint, HTTPStatus: 404},
		},
	}
	_, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository,
		Version:    testVersion,
	})
	var observationErr *observationError
	if !errors.As(err, &observationErr) || observationErr.code != "REPOSITORY_NOT_ACCESSIBLE" || observationErr.operation != repositoryEndpoint {
		t.Fatalf("tag endpoint=%s err=%T %v", tagEndpoint, err, err)
	}
}

func TestSnapshotV008PublisherFailureReportsExactJobAndSteps(t *testing.T) {
	github := failedV008Fixture()
	doc, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository,
		Version:    testVersion,
		SourceSHA:  testSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Overall.State != "failed" || doc.Overall.Reason != "publisher_failed" || !doc.Overall.Terminal {
		t.Fatalf("overall=%+v", doc.Overall)
	}
	if doc.NextAction.Code != "inspect_publisher_failure" {
		t.Fatalf("next action=%+v", doc.NextAction)
	}
	if doc.Stages.Publisher.Run == nil || doc.Stages.Publisher.Run.ID != 29475939348 {
		t.Fatalf("publisher=%+v", doc.Stages.Publisher)
	}
	if doc.Stages.GitHubRelease.State != stateBlocked || doc.Stages.SupplyChain.State != stateBlocked || doc.Stages.Homebrew.State != stateBlocked || doc.Stages.Health.State != stateBlocked {
		t.Fatalf("downstream stages=%+v", doc.Stages)
	}
	if len(doc.FailedJobs) != 1 {
		t.Fatalf("failed jobs=%+v", doc.FailedJobs)
	}
	failed := doc.FailedJobs[0]
	if failed.Name != "quality / e2e-compare" {
		t.Fatalf("failed job=%+v", failed)
	}
	wantSteps := []failedStep{
		{Number: 10, Name: "Compare candidate with canonical baseline", Conclusion: "failure"},
		{Number: 11, Name: "Verify exact migration identity", Conclusion: "failure"},
	}
	if !reflect.DeepEqual(failed.FailedSteps, wantSteps) {
		t.Fatalf("steps=%+v, want %+v", failed.FailedSteps, wantSteps)
	}
}

func TestSnapshotCompleteChainSucceedsWithExactAssets(t *testing.T) {
	github := successfulFixture()
	doc, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository,
		Version:    testVersion,
		SourceSHA:  testSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Overall.State != "succeeded" || !doc.Overall.Terminal {
		t.Fatalf("overall=%+v stages=%+v", doc.Overall, doc.Stages)
	}
	if doc.NextAction.Code != "none" {
		t.Fatalf("next action=%+v", doc.NextAction)
	}
	assets := doc.Stages.GitHubRelease.Release.Assets
	if assets.ExpectedCount != 10 || assets.ObservedCount != 10 || len(assets.Missing)+len(assets.Unexpected)+len(assets.Duplicates) != 0 {
		t.Fatalf("assets=%+v", assets)
	}
	if len(doc.FailedJobs) != 0 {
		t.Fatalf("failed jobs=%+v", doc.FailedJobs)
	}
}

func TestSnapshotIncompleteAttemptEmitsFullRerunActionAndExactMissingTargets(t *testing.T) {
	github := successfulFixture()
	run := github.ciRuns[0]
	run.RunAttempt = 2
	run.Conclusion = "failure"
	github.ciRuns[0] = run
	github.artifacts = completeAttemptArtifacts(run)[:1]
	doc, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository, Version: testVersion, SourceSHA: testSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.NextAction == nil || doc.NextAction.Code != "rerun_all_jobs" || doc.AttemptMatrix == nil || doc.AttemptMatrix.RunID != run.ID || doc.AttemptMatrix.Attempt != 2 || doc.AttemptMatrix.ReasonCode != "ATTEMPT_MATRIX_INCOMPLETE" || len(doc.AttemptMatrix.MissingTargets) != 5 {
		t.Fatalf("doc=%+v", doc)
	}
	for _, call := range github.calls {
		if strings.Contains(call, "rerun-failed-jobs") {
			t.Fatalf("status referenced unsafe endpoint: %v", github.calls)
		}
	}
}

func TestSnapshotPublisherBundleCannotSatisfyIncompleteMainCIAttempt(t *testing.T) {
	github := successfulFixture()
	ciRun := github.ciRuns[0]
	ciRun.RunAttempt = 2
	ciRun.Conclusion = "failure"
	github.ciRuns[0] = ciRun
	publisherRun := github.publisherRuns[0]
	publisherRun.RunAttempt = 2
	github.artifacts = []workflowArtifact{{
		ID: 99, Name: "env-vault-publisher-bundle-" + testSHA + "-attempt-2",
		WorkflowRun: struct {
			ID      int64  `json:"id"`
			HeadSHA string `json:"head_sha"`
		}{ID: publisherRun.ID, HeadSHA: publisherRun.HeadSHA},
	}}
	doc, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository, Version: testVersion, SourceSHA: testSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.NextAction == nil || doc.NextAction.Code != "rerun_all_jobs" || doc.AttemptMatrix == nil {
		t.Fatalf("doc=%+v", doc)
	}
	if doc.AttemptMatrix.RunID != ciRun.ID || doc.AttemptMatrix.Attempt != ciRun.RunAttempt || doc.AttemptMatrix.Complete || len(doc.AttemptMatrix.MissingTargets) != 5 {
		t.Fatalf("attempt matrix=%+v", doc.AttemptMatrix)
	}
	if containsString(doc.AttemptMatrix.Observed, github.artifacts[0].Name) {
		t.Fatalf("publisher bundle was accepted as native CI evidence: %+v", doc.AttemptMatrix)
	}
}

func TestSnapshotDoesNotRecommendFullRerunForIncompletePublisherBundle(t *testing.T) {
	github := successfulFixture()
	ciRun := github.ciRuns[0]
	github.artifacts = completeAttemptArtifacts(ciRun)
	github.publisherRuns[0].Conclusion = "failure"
	doc, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository, Version: testVersion, SourceSHA: testSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.NextAction == nil || doc.NextAction.Code == "rerun_all_jobs" {
		t.Fatalf("publisher failure must not be classified as an incomplete CI attempt: %+v", doc)
	}
	if doc.AttemptMatrix == nil || doc.AttemptMatrix.RunID != ciRun.ID || !doc.AttemptMatrix.Complete {
		t.Fatalf("attempt matrix=%+v", doc.AttemptMatrix)
	}
}

func TestSnapshotIncludesManualRepairRunsForExactSourceSHA(t *testing.T) {
	github := successfulFixture()
	repair := completedRun(104, "workflow_dispatch", testVersion, testSHA, "failure")
	repair.RunAttempt = 2
	repair.DisplayTitle = "env-vault-publication event=workflow_dispatch version=" + testVersion + " repair=homebrew"
	github.repairRuns = []workflowRun{repair}
	doc, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository, Version: testVersion, SourceSHA: testSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.RepairRuns) != 1 || doc.RepairRuns[0].ID != repair.ID || doc.RepairRuns[0].Attempt != 2 || doc.RepairRuns[0].HeadSHA != testSHA {
		t.Fatalf("repair runs=%+v", doc.RepairRuns)
	}
	if doc.RepairRuns[0].RepairMode != "homebrew" || doc.RepairRuns[0].WorkflowPath != ".github/workflows/build-binaries.yml" {
		t.Fatalf("repair identity=%+v", doc.RepairRuns[0])
	}
}

func TestManualRepairModeAcceptsCanonicalStateDigestAndRejectsMalformedSuffix(t *testing.T) {
	contract := mustTestReleaseContract()
	base := completedRun(104, "workflow_dispatch", testVersion, otherSHA, "success")
	base.DisplayTitle = "env-vault-publication event=workflow_dispatch version=" + testVersion + " repair=health"
	for _, test := range []struct {
		name  string
		title string
		path  string
		ok    bool
	}{
		{name: "historical", title: base.DisplayTitle, path: base.Path, ok: true},
		{name: "digest", title: base.DisplayTitle + " state=" + strings.Repeat("a", 64), path: base.Path, ok: true},
		{name: "short", title: base.DisplayTitle + " state=abcd", path: base.Path},
		{name: "uppercase", title: base.DisplayTitle + " state=" + strings.Repeat("A", 64), path: base.Path},
		{name: "extra", title: base.DisplayTitle + " state=" + strings.Repeat("a", 64) + " extra", path: base.Path},
		{name: "wrong-workflow", title: base.DisplayTitle, path: ".github/workflows/ci.yml"},
	} {
		t.Run(test.name, func(t *testing.T) {
			run := base
			run.DisplayTitle, run.Path = test.title, test.path
			mode, ok := manualRepairMode(run, testVersion, contract)
			if ok != test.ok || ok && mode != "health" {
				t.Fatalf("mode=%q ok=%v", mode, ok)
			}
		})
	}
}

func TestSnapshotFoldsSuccessfulExactSourceHealthRepairIntoPublicationState(t *testing.T) {
	github := successfulFixture()
	github.publisherRuns[0].Conclusion = "failure"
	for index := range github.jobs {
		if github.jobs[index].Name == "health" {
			github.jobs[index] = completedJob(204, "health", "failure", []workflowStep{
				completedStep(1, "Verify published release health", "failure"),
			})
		}
	}
	repair := completedRun(104, "workflow_dispatch", testVersion, testSHA, "success")
	repair.DisplayTitle = "env-vault-publication event=workflow_dispatch version=" + testVersion + " repair=health"
	github.repairRuns = []workflowRun{repair}
	github.jobsByRun = map[int64][]workflowJob{
		repair.ID: {completedJob(301, "health", "success", nil)},
	}
	doc, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository, Version: testVersion, SourceSHA: testSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Overall.State != "succeeded" || doc.NextAction.Code != "none" {
		t.Fatalf("overall=%+v next=%+v stages=%+v", doc.Overall, doc.NextAction, doc.Stages)
	}
	if doc.Stages.Health.Job == nil || doc.Stages.Health.Job.ID != 301 || doc.Stages.Health.State != stateSucceeded {
		t.Fatalf("health=%+v", doc.Stages.Health)
	}
	if doc.Stages.Publisher.State != stateSucceeded || doc.Stages.Publisher.Reason != "manual_repair_reconciled" || doc.Stages.Publisher.Run == nil || doc.Stages.Publisher.Run.ID != repair.ID || doc.Stages.Publisher.Run.RepairMode != "health" {
		t.Fatalf("publisher=%+v", doc.Stages.Publisher)
	}
	if len(doc.FailedJobs) != 0 {
		t.Fatalf("stale automatic failures remained after exact repair: %+v", doc.FailedJobs)
	}
}

func TestSnapshotDoesNotFoldRepairFromDifferentTagSource(t *testing.T) {
	github := successfulFixture()
	github.publisherRuns[0].Conclusion = "failure"
	for index := range github.jobs {
		if github.jobs[index].Name == "health" {
			github.jobs[index].Conclusion = "failure"
		}
	}
	repair := completedRun(104, "workflow_dispatch", testVersion, otherSHA, "success")
	repair.DisplayTitle = "env-vault-publication event=workflow_dispatch version=" + testVersion + " repair=health"
	github.repairRuns = []workflowRun{repair}
	github.jobsByRun = map[int64][]workflowJob{
		repair.ID: {completedJob(301, "health", "success", nil)},
	}
	doc, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository, Version: testVersion, SourceSHA: testSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.RepairRuns) != 0 || doc.Overall.State != "failed" || doc.Stages.Health.State != stateFailed {
		t.Fatalf("different-source repair changed immutable-tag status: %+v", doc)
	}
	for _, call := range github.calls {
		if strings.Contains(call, fmt.Sprintf("/actions/runs/%d/", repair.ID)) {
			t.Fatalf("different-source repair jobs were queried: %v", github.calls)
		}
	}
}

func TestSnapshotUsesNewestExactRepairPerCoveredStage(t *testing.T) {
	github := successfulFixture()
	older := completedRun(104, "workflow_dispatch", testVersion, testSHA, "success")
	older.DisplayTitle = "env-vault-publication event=workflow_dispatch version=" + testVersion + " repair=health"
	newer := completedRun(105, "workflow_dispatch", testVersion, testSHA, "failure")
	newer.DisplayTitle = older.DisplayTitle
	github.repairRuns = []workflowRun{older, newer}
	github.jobsByRun = map[int64][]workflowJob{
		older.ID: {completedJob(301, "health", "success", nil)},
		newer.ID: {completedJob(302, "health", "failure", []workflowStep{
			completedStep(1, "Verify published release health", "failure"),
		})},
	}
	doc, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository, Version: testVersion, SourceSHA: testSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.RepairRuns) != 2 || doc.RepairRuns[0].ID != newer.ID || doc.RepairRuns[1].ID != older.ID {
		t.Fatalf("repair ordering=%+v", doc.RepairRuns)
	}
	if doc.Overall.State != "failed" || doc.Overall.Reason != "health_failed" || doc.Stages.Health.Job == nil || doc.Stages.Health.Job.ID != 302 {
		t.Fatalf("newest repair did not control health state: %+v", doc)
	}
	if len(doc.FailedJobs) != 1 || doc.FailedJobs[0].ID != 302 {
		t.Fatalf("failed jobs=%+v", doc.FailedJobs)
	}
	for _, call := range github.calls {
		if strings.Contains(call, fmt.Sprintf("/actions/runs/%d/", older.ID)) {
			t.Fatalf("superseded repair jobs were queried: %v", github.calls)
		}
	}
}

func TestSnapshotDetectsTagSourceMismatch(t *testing.T) {
	github := successfulFixture()
	github.tagObject.SHA = otherSHA
	github.releaseMissing = true
	doc, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository,
		Version:    testVersion,
		SourceSHA:  testSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Overall.State != "inconsistent" || doc.Overall.Reason != "tag_inconsistent" {
		t.Fatalf("overall=%+v tag=%+v", doc.Overall, doc.Stages.Tag)
	}
	for _, call := range github.calls {
		if strings.Contains(call, "build-binaries") {
			t.Fatalf("publisher must not be queried after identity mismatch: %v", github.calls)
		}
	}
}

func TestSnapshotDoesNotMaskTagMismatchWithCIRerunRecommendation(t *testing.T) {
	github := successfulFixture()
	github.tagObject.SHA = otherSHA
	github.releaseMissing = true
	ciRun := github.ciRuns[0]
	ciRun.RunAttempt = 2
	ciRun.Conclusion = "failure"
	github.ciRuns[0] = ciRun
	github.artifacts = completeAttemptArtifacts(ciRun)[:1]
	doc, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository, Version: testVersion, SourceSHA: testSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Overall.State != "inconsistent" || doc.Overall.Reason != "tag_inconsistent" {
		t.Fatalf("overall=%+v", doc.Overall)
	}
	if doc.NextAction == nil || doc.NextAction.Code != "resolve_inconsistency" {
		t.Fatalf("tag mismatch was masked by a CI repair action: %+v", doc.NextAction)
	}
}

func TestSnapshotRejectsMalformedWorkflowResponse(t *testing.T) {
	github := successfulFixture()
	github.ciRuns = nil
	_, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository,
		Version:    testVersion,
		SourceSHA:  testSHA,
	})
	var observationErr *observationError
	if !errors.As(err, &observationErr) || observationErr.code != "MALFORMED_RESPONSE" {
		t.Fatalf("err=%T %v", err, err)
	}
}

func TestSnapshotRejectsTruncatedWorkflowRunPagination(t *testing.T) {
	github := successfulFixture()
	endpoint := "repos/" + testRepository + "/actions/workflows/ci.yml/runs"
	github.runPages = map[string]workflowRunsResponse{
		endpoint + "#1": {TotalCount: 2, WorkflowRuns: []workflowRun{github.ciRuns[0]}},
		endpoint + "#2": {TotalCount: 2, WorkflowRuns: []workflowRun{}},
	}
	_, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository, Version: testVersion, SourceSHA: testSHA,
	})
	var observationErr *observationError
	if !errors.As(err, &observationErr) || observationErr.code != "MALFORMED_RESPONSE" || observationErr.operation != endpoint {
		t.Fatalf("err=%T %v", err, err)
	}
	wantCalls := 0
	for _, call := range github.calls {
		if call == endpoint {
			wantCalls++
		}
	}
	if wantCalls != 2 {
		t.Fatalf("pagination calls=%v", github.calls)
	}
}

func TestSnapshotIncompleteAssetsAfterSuccessfulPublisherIsInconsistent(t *testing.T) {
	github := successfulFixture()
	github.release.Assets = github.release.Assets[:9]
	doc, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository,
		Version:    testVersion,
		SourceSHA:  testSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Overall.State != "inconsistent" || doc.Stages.GitHubRelease.Reason != "release_assets_incomplete" {
		t.Fatalf("overall=%+v release=%+v", doc.Overall, doc.Stages.GitHubRelease)
	}
	if len(doc.Stages.GitHubRelease.Release.Assets.Missing) != 1 {
		t.Fatalf("assets=%+v", doc.Stages.GitHubRelease.Release.Assets)
	}
}

func TestSnapshotAttributesDownstreamPublisherFailures(t *testing.T) {
	tests := []struct {
		job        string
		wantReason string
		wantAction string
	}{
		{job: "release", wantReason: "github_release_failed", wantAction: "inspect_release_failure"},
		{job: "supply_chain", wantReason: "supply_chain_failed", wantAction: "inspect_supply_chain_failure"},
		{job: "homebrew", wantReason: "homebrew_failed", wantAction: "replan_publisher"},
		{job: "health", wantReason: "health_failed", wantAction: "inspect_health_failure"},
	}
	for _, test := range tests {
		t.Run(test.job, func(t *testing.T) {
			github := successfulFixture()
			github.publisherRuns[0].Conclusion = "failure"
			for index := range github.jobs {
				if github.jobs[index].Name == test.job {
					github.jobs[index].Conclusion = "failure"
					github.jobs[index].Steps = []workflowStep{completedStep(10, "Fail "+test.job, "failure")}
				}
			}
			if test.job == "release" {
				github.releaseMissing = true
				github.release = releaseResponse{}
			}
			doc, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
				Repository: testRepository,
				Version:    testVersion,
				SourceSHA:  testSHA,
			})
			if err != nil {
				t.Fatal(err)
			}
			if doc.Overall.State != "failed" || doc.Overall.Reason != test.wantReason || !doc.Overall.Terminal {
				t.Fatalf("overall=%+v stages=%+v", doc.Overall, doc.Stages)
			}
			if doc.NextAction.Code != test.wantAction {
				t.Fatalf("next action=%+v", doc.NextAction)
			}
		})
	}
}

func TestSnapshotTreatsBlockedStageAfterSuccessfulPublisherAsInconsistent(t *testing.T) {
	github := successfulFixture()
	github.releaseMissing = true
	github.release = releaseResponse{}
	for index := range github.jobs {
		if github.jobs[index].Name == "release" {
			github.jobs[index].Conclusion = "skipped"
		}
	}
	doc, err := (collector{github: github, clock: fixedClock()}).snapshot(context.Background(), query{
		Repository: testRepository,
		Version:    testVersion,
		SourceSHA:  testSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Overall.State != "inconsistent" || doc.Overall.Reason != "github_release_blocked" || !doc.Overall.Terminal {
		t.Fatalf("overall=%+v release=%+v", doc.Overall, doc.Stages.GitHubRelease)
	}
}

func failedV008Fixture() *fixtureGitHub {
	ci := completedRun(29475607744, "push", "main", testSHA, "success")
	planning := completedRun(29475926015, "workflow_run", "main", testSHA, "success")
	publisher := completedRun(29475939348, "push", testVersion, testSHA, "failure")
	fixture := &fixtureGitHub{
		tagObject:     gitObject{Type: "commit", SHA: testSHA},
		ciRuns:        []workflowRun{ci},
		planningRuns:  []workflowRun{planning},
		publisherRuns: []workflowRun{publisher},
		jobs: []workflowJob{
			completedJob(87549708400, "quality / e2e-compare", "failure", []workflowStep{
				completedStep(10, "Compare candidate with canonical baseline", "failure"),
				completedStep(11, "Verify exact migration identity", "failure"),
			}),
			completedJob(87549824736, "release", "skipped", nil),
			completedJob(87549824814, "supply_chain", "skipped", nil),
			completedJob(87549825090, "homebrew", "skipped", nil),
			completedJob(87549825359, "health", "skipped", nil),
		},
		releaseMissing: true,
		errors:         map[string]error{},
	}
	fixture.artifacts = completeAttemptArtifacts(ci)
	return fixture
}

func successfulFixture() *fixtureGitHub {
	contract := mustTestReleaseContract()
	assets := make([]releaseAsset, 0, len(contract.Assets))
	for index, name := range contract.Assets {
		digest := sha256.Sum256([]byte(name))
		assets = append(assets, releaseAsset{
			ID: int64(index + 1), Name: name, State: "uploaded", Size: int64(100 + index),
			Digest: fmt.Sprintf("sha256:%x", digest[:]),
		})
	}
	ci := completedRun(101, "push", "main", testSHA, "success")
	publisher := completedRun(103, "push", testVersion, testSHA, "success")
	fixture := &fixtureGitHub{
		tagObject:     gitObject{Type: "commit", SHA: testSHA},
		ciRuns:        []workflowRun{ci},
		planningRuns:  []workflowRun{completedRun(102, "workflow_run", "main", testSHA, "success")},
		publisherRuns: []workflowRun{publisher},
		jobs: []workflowJob{
			completedJob(201, "release", "success", nil),
			completedJob(202, "supply_chain", "success", nil),
			completedJob(203, "homebrew", "success", nil),
			completedJob(204, "health", "success", nil),
		},
		release: releaseResponse{TagName: testVersion, HTMLURL: "https://github.com/example/env-vault/releases/tag/" + testVersion, Assets: assets},
		errors:  map[string]error{},
	}
	fixture.artifacts = completePromotionArtifacts(ci)
	return fixture
}

func mustTestReleaseContract() releasecontract.Contract {
	contract, err := releasecontract.LoadFile("../../" + releasecontract.CanonicalPath)
	if err != nil {
		panic(err)
	}
	return contract
}

func completeAttemptArtifacts(run workflowRun) []workflowArtifact {
	contract := mustTestReleaseContract()
	result := make([]workflowArtifact, 0, len(contract.Platforms)*2)
	for index, platform := range contract.Platforms {
		for offset, prefix := range []string{"env-vault-release-", "env-vault-promotion-platform-"} {
			artifact := workflowArtifact{ID: int64(index*2 + offset + 1), Name: fmt.Sprintf("%s%s-attempt-%d", prefix, platform.ID, run.RunAttempt)}
			artifact.WorkflowRun.ID = run.ID
			artifact.WorkflowRun.HeadSHA = run.HeadSHA
			result = append(result, artifact)
		}
	}
	return result
}

func completePromotionArtifacts(run workflowRun) []workflowArtifact {
	result := completeAttemptArtifacts(run)
	manifest := workflowArtifact{
		ID:   int64(len(result) + 1),
		Name: fmt.Sprintf("env-vault-promotion-%s-attempt-%d", run.HeadSHA, run.RunAttempt),
	}
	manifest.WorkflowRun.ID = run.ID
	manifest.WorkflowRun.HeadSHA = run.HeadSHA
	return append(result, manifest)
}

func completedRun(id int64, event, branch, sha, conclusion string) workflowRun {
	path := ".github/workflows/build-binaries.yml"
	switch event {
	case "workflow_run":
		path = ".github/workflows/release-please.yml"
	case "push":
		if branch == "main" {
			path = ".github/workflows/ci.yml"
		}
	}
	return workflowRun{
		ID: id, RunAttempt: 1, Event: event, Status: "completed", Conclusion: conclusion,
		HeadBranch: branch, HeadSHA: sha, Path: path,
		HTMLURL: fmt.Sprintf("https://github.com/example/env-vault/actions/runs/%d", id),
	}
}

func completedTapRun(id int64, event, branch, sha string) workflowRun {
	run := completedRun(id, event, branch, sha, "success")
	run.Path = ".github/workflows/test-formula.yml"
	return run
}

func mustTestTapRepository() string {
	app, ok := mustTestReleaseContract().AppByID("homebrew_tap")
	if !ok {
		panic("homebrew_tap app missing")
	}
	return app.Repository
}

func fixtureHomebrewPull(version string) pullRequestResponse {
	mergedAt := time.Date(2026, 7, 16, 5, 0, 0, 0, time.UTC)
	repository := mustTestTapRepository()
	branch := "release/env-vault-" + version
	digest := sha256.Sum256(fixtureFormulaContent(version))
	formulaDigest := fmt.Sprintf("%x", digest[:])
	pull := pullRequestResponse{
		Number: 7, HTMLURL: "https://github.com/" + repository + "/pull/7", State: "closed",
		Title: "env-vault " + version, MergedAt: &mergedAt, MergeCommitSHA: tapMergeSHA,
		Body: "Automated Homebrew formula update for env-vault " + version + ".\n\n" +
			"Source release: https://github.com/" + testRepository + "/releases/tag/" + version + "\n\n" +
			"<!-- env-vault-release version=" + version + " source_sha=" + testSHA + " formula_sha256=" + formulaDigest + " -->",
	}
	pull.Head.Ref, pull.Head.SHA, pull.Head.Repository.FullName = branch, tapHeadSHA, repository
	pull.Base.Ref, pull.Base.SHA, pull.Base.Repository.FullName = "main", tapBaseSHA, repository
	return pull
}

func fixtureFormulaContent(version string) []byte {
	return []byte("class EnvVault < Formula\n  version \"" + strings.TrimPrefix(version, "v") + "\"\nend\n")
}

func completedJob(id int64, name, conclusion string, steps []workflowStep) workflowJob {
	return workflowJob{
		ID: id, Name: name, Status: "completed", Conclusion: conclusion,
		HTMLURL: fmt.Sprintf("https://github.com/example/env-vault/actions/jobs/%d", id), Steps: steps,
	}
}

func completedStep(number int, name, conclusion string) workflowStep {
	return workflowStep{Number: number, Name: name, Status: "completed", Conclusion: conclusion}
}
