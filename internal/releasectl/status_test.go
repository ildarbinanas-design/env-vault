package releasectl

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	testRepository = "example/env-vault"
	testVersion    = "v0.0.8"
	testSHA        = "1111111111111111111111111111111111111111"
	otherSHA       = "2222222222222222222222222222222222222222"
)

type fixtureGitHub struct {
	tagObject      gitObject
	tagMissing     bool
	ciRuns         []workflowRun
	planningRuns   []workflowRun
	publisherRuns  []workflowRun
	jobs           []workflowJob
	release        releaseResponse
	releaseMissing bool
	errors         map[string]error
	calls          []string
}

func (f *fixtureGitHub) Get(_ context.Context, endpoint string, _ map[string]string, target any) error {
	f.calls = append(f.calls, endpoint)
	if err := f.errors[endpoint]; err != nil {
		return err
	}
	switch {
	case endpoint == "repos/"+testRepository:
		response := target.(*repositoryResponse)
		response.FullName = testRepository
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
	case strings.Contains(endpoint, "/actions/workflows/release-please.yml/runs"):
		response := target.(*workflowRunsResponse)
		response.WorkflowRuns = f.planningRuns
	case strings.Contains(endpoint, "/actions/workflows/build-binaries.yml/runs"):
		response := target.(*workflowRunsResponse)
		response.WorkflowRuns = f.publisherRuns
	case strings.Contains(endpoint, "/actions/runs/") && strings.HasSuffix(endpoint, "/jobs"):
		response := target.(*jobsResponse)
		response.Jobs = f.jobs
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
		{job: "homebrew", wantReason: "homebrew_failed", wantAction: "inspect_homebrew_failure"},
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
	return &fixtureGitHub{
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
}

func successfulFixture() *fixtureGitHub {
	assets := make([]releaseAsset, 0, len(expectedAssets))
	for _, name := range expectedAssets {
		assets = append(assets, releaseAsset{Name: name})
	}
	return &fixtureGitHub{
		tagObject:     gitObject{Type: "commit", SHA: testSHA},
		ciRuns:        []workflowRun{completedRun(101, "push", "main", testSHA, "success")},
		planningRuns:  []workflowRun{completedRun(102, "workflow_run", "main", testSHA, "success")},
		publisherRuns: []workflowRun{completedRun(103, "push", testVersion, testSHA, "success")},
		jobs: []workflowJob{
			completedJob(201, "release", "success", nil),
			completedJob(202, "supply_chain", "success", nil),
			completedJob(203, "homebrew", "success", nil),
			completedJob(204, "health", "success", nil),
		},
		release: releaseResponse{TagName: testVersion, HTMLURL: "https://github.com/example/env-vault/releases/tag/" + testVersion, Assets: assets},
		errors:  map[string]error{},
	}
}

func completedRun(id int64, event, branch, sha, conclusion string) workflowRun {
	return workflowRun{
		ID: id, RunAttempt: 1, Event: event, Status: "completed", Conclusion: conclusion,
		HeadBranch: branch, HeadSHA: sha, HTMLURL: fmt.Sprintf("https://github.com/example/env-vault/actions/runs/%d", id),
	}
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
