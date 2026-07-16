package releasectl

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type fakeClock struct {
	now    time.Time
	sleeps []time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) Sleep(_ context.Context, delay time.Duration) error {
	c.sleeps = append(c.sleeps, delay)
	c.now = c.now.Add(delay)
	return nil
}

func TestRunStatusReturnsReleaseFailureWithOneJSONDocument(t *testing.T) {
	github := failedV008Fixture()
	clock := &fakeClock{now: time.Date(2026, 7, 16, 6, 17, 27, 0, time.UTC)}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{
		"release", "status", "--version", testVersion, "--repo", testRepository,
		"--source-sha", testSHA, "--json",
	}, &stdout, &stderr, dependencies{github: github, clock: clock})
	if code != exitReleaseFailure {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 || strings.Count(stdout.String(), "\n") != 1 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	var doc document
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Schema != statusSchema || !doc.OK || doc.Overall.State != "failed" || len(doc.FailedJobs) != 1 {
		t.Fatalf("doc=%+v", doc)
	}
}

func TestRunStatusEmitsStructuredAuthErrorWithoutRawDiagnostic(t *testing.T) {
	endpoint := "repos/" + testRepository + "/git/ref/tags/" + testVersion
	github := &fixtureGitHub{errors: map[string]error{
		endpoint: &apiError{Code: "AUTH_REQUIRED", Endpoint: endpoint, HTTPStatus: 401, cause: context.Canceled},
	}}
	clock := &fakeClock{now: time.Date(2026, 7, 16, 6, 0, 0, 0, time.UTC)}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{"release", "status", "--version", testVersion, "--repo", testRepository, "--json"}, &stdout, &stderr, dependencies{github: github, clock: clock})
	if code != exitObservationError {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	var doc document
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.OK || doc.Error == nil || doc.Error.Code != "AUTH_REQUIRED" || doc.Error.HTTPStatus != 401 {
		t.Fatalf("doc=%+v", doc)
	}
	if strings.Contains(stdout.String(), "context canceled") {
		t.Fatalf("raw cause leaked: %s", stdout.String())
	}
}

func TestRunStatusPreservesRepositoryNotAccessibleError(t *testing.T) {
	tagEndpoint := "repos/" + testRepository + "/git/ref/tags/" + testVersion
	repositoryEndpoint := "repos/" + testRepository
	github := &fixtureGitHub{
		errors: map[string]error{
			tagEndpoint:        &apiError{Code: "NOT_FOUND", Endpoint: tagEndpoint, HTTPStatus: 404},
			repositoryEndpoint: &apiError{Code: "NOT_FOUND", Endpoint: repositoryEndpoint, HTTPStatus: 404},
		},
	}
	clock := &fakeClock{now: time.Date(2026, 7, 16, 6, 0, 0, 0, time.UTC)}
	var stdout bytes.Buffer
	code := run([]string{
		"release", "status", "--version", testVersion, "--repo", testRepository, "--json",
	}, &stdout, &bytes.Buffer{}, dependencies{github: github, clock: clock})
	if code != exitObservationError {
		t.Fatalf("code=%d stdout=%s", code, stdout.String())
	}
	var doc document
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.OK || doc.Error == nil || doc.Error.Code != "REPOSITORY_NOT_ACCESSIBLE" || doc.Error.Operation != repositoryEndpoint || doc.Error.HTTPStatus != 0 {
		t.Fatalf("doc=%+v", doc)
	}
}

func TestRunStatusDeadlineCancelsBlockedAPIRequest(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 16, 6, 0, 0, 0, time.UTC)}
	var stdout bytes.Buffer
	code := run([]string{
		"release", "status", "--version", testVersion, "--repo", testRepository,
		"--timeout", "10ms", "--json",
	}, &stdout, &bytes.Buffer{}, dependencies{github: blockingGitHub{}, clock: clock})
	if code != exitObservationError {
		t.Fatalf("code=%d stdout=%s", code, stdout.String())
	}
	var doc document
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.OK || doc.Error == nil || doc.Error.Code != "API_UNAVAILABLE" || !doc.Error.Retryable {
		t.Fatalf("doc=%+v", doc)
	}
}

type transitionGitHub struct {
	polls   int
	success *fixtureGitHub
}

type transientGitHub struct {
	failures int
	inner    githubGetter
}

func (g *transientGitHub) Get(ctx context.Context, endpoint string, query map[string]string, target any) error {
	if g.failures > 0 {
		g.failures--
		return &apiError{Code: "API_UNAVAILABLE", Endpoint: endpoint, Retryable: true, cause: context.DeadlineExceeded}
	}
	return g.inner.Get(ctx, endpoint, query, target)
}

type blockingGitHub struct{}

func (blockingGitHub) Get(ctx context.Context, endpoint string, _ map[string]string, _ any) error {
	<-ctx.Done()
	return &apiError{Code: "API_UNAVAILABLE", Endpoint: endpoint, Retryable: true, cause: ctx.Err()}
}

type outageAfterSnapshotGitHub struct {
	tagCalls int
	inner    githubGetter
}

type movingTagGitHub struct {
	tagCalls int
	inner    githubGetter
}

func (g *movingTagGitHub) Get(ctx context.Context, endpoint string, query map[string]string, target any) error {
	if strings.Contains(endpoint, "/git/ref/tags/") {
		g.tagCalls++
		if g.tagCalls > 1 {
			response := target.(*tagRefResponse)
			response.Object = gitObject{Type: "commit", SHA: otherSHA}
			return nil
		}
	}
	return g.inner.Get(ctx, endpoint, query, target)
}

func (g *outageAfterSnapshotGitHub) Get(ctx context.Context, endpoint string, query map[string]string, target any) error {
	if strings.Contains(endpoint, "/git/ref/tags/") {
		g.tagCalls++
		if g.tagCalls > 1 {
			return &apiError{Code: "API_UNAVAILABLE", Endpoint: endpoint, Retryable: true, cause: context.DeadlineExceeded}
		}
	}
	return g.inner.Get(ctx, endpoint, query, target)
}

func (g *transitionGitHub) Get(ctx context.Context, endpoint string, query map[string]string, target any) error {
	if strings.Contains(endpoint, "/git/ref/tags/") {
		g.polls++
		if g.polls == 1 {
			return &apiError{Code: "NOT_FOUND", Endpoint: endpoint, HTTPStatus: 404}
		}
	}
	return g.success.Get(ctx, endpoint, query, target)
}

func TestRunWatchPollsUntilSuccessAndWritesOnlyFinalSnapshot(t *testing.T) {
	github := &transitionGitHub{success: successfulFixture()}
	clock := &fakeClock{now: time.Date(2026, 7, 16, 6, 0, 0, 0, time.UTC)}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run([]string{
		"release", "watch", "--version", testVersion, "--repo", testRepository,
		"--source-sha", testSHA, "--interval", "30s", "--timeout", "2m", "--json",
	}, &stdout, &stderr, dependencies{github: github, clock: clock})
	if code != exitSuccess {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if strings.Count(stdout.String(), "\n") != 1 || stderr.Len() != 0 {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	var doc document
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Overall.State != "succeeded" || doc.Watch == nil || doc.Watch.Polls != 2 || doc.Watch.ElapsedSeconds != 30 || doc.Watch.TimedOut {
		t.Fatalf("doc=%+v", doc)
	}
	if len(clock.sleeps) != 1 || clock.sleeps[0] != 30*time.Second {
		t.Fatalf("sleeps=%v", clock.sleeps)
	}
}

func TestRunWatchRetriesTransientAPIError(t *testing.T) {
	github := &transientGitHub{failures: 1, inner: successfulFixture()}
	clock := &fakeClock{now: time.Date(2026, 7, 16, 6, 0, 0, 0, time.UTC)}
	var stdout bytes.Buffer
	code := run([]string{
		"release", "watch", "--version", testVersion, "--repo", testRepository,
		"--source-sha", testSHA, "--interval", "30s", "--timeout", "2m", "--json",
	}, &stdout, &bytes.Buffer{}, dependencies{github: github, clock: clock})
	if code != exitSuccess {
		t.Fatalf("code=%d stdout=%s", code, stdout.String())
	}
	var doc document
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Overall.State != "succeeded" || doc.Watch == nil || doc.Watch.Polls != 2 || doc.Watch.ElapsedSeconds != 30 {
		t.Fatalf("doc=%+v", doc)
	}
}

func TestRunWatchDeadlineCancelsBlockedAPIRequest(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 16, 6, 0, 0, 0, time.UTC)}
	var stdout bytes.Buffer
	code := run([]string{
		"release", "watch", "--version", testVersion, "--repo", testRepository,
		"--timeout", "10ms", "--json",
	}, &stdout, &bytes.Buffer{}, dependencies{github: blockingGitHub{}, clock: clock})
	if code != exitWatchTimeout {
		t.Fatalf("code=%d stdout=%s", code, stdout.String())
	}
	var doc document
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Error == nil || doc.Error.Code != "API_UNAVAILABLE" || doc.Watch == nil || !doc.Watch.TimedOut {
		t.Fatalf("doc=%+v", doc)
	}
}

func TestRunWatchReportsOutageAlongsideLastValidSnapshot(t *testing.T) {
	pending := successfulFixture()
	pending.publisherRuns = []workflowRun{}
	github := &outageAfterSnapshotGitHub{inner: pending}
	clock := &fakeClock{now: time.Date(2026, 7, 16, 6, 0, 0, 0, time.UTC)}
	var stdout bytes.Buffer
	code := run([]string{
		"release", "watch", "--version", testVersion, "--repo", testRepository,
		"--source-sha", testSHA, "--interval", "30s", "--timeout", "1m", "--json",
	}, &stdout, &bytes.Buffer{}, dependencies{github: github, clock: clock})
	if code != exitWatchTimeout {
		t.Fatalf("code=%d stdout=%s", code, stdout.String())
	}
	var doc document
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.OK || doc.Error == nil || doc.Error.Code != "API_UNAVAILABLE" || doc.Overall == nil || doc.Overall.State != "pending" || doc.Watch == nil || !doc.Watch.TimedOut {
		t.Fatalf("doc=%+v", doc)
	}
}

func TestRunWatchFreezesFirstObservedTagSHA(t *testing.T) {
	pending := successfulFixture()
	pending.publisherRuns = []workflowRun{}
	github := &movingTagGitHub{inner: pending}
	clock := &fakeClock{now: time.Date(2026, 7, 16, 6, 0, 0, 0, time.UTC)}
	var stdout bytes.Buffer
	code := run([]string{
		"release", "watch", "--version", testVersion, "--repo", testRepository,
		"--interval", "30s", "--timeout", "2m", "--json",
	}, &stdout, &bytes.Buffer{}, dependencies{github: github, clock: clock})
	if code != exitReleaseFailure {
		t.Fatalf("code=%d stdout=%s", code, stdout.String())
	}
	var doc document
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Overall == nil || doc.Overall.State != "inconsistent" || doc.Stages.Tag.Reason != "tag_source_sha_mismatch" || doc.Query.SourceSHA != testSHA || doc.Stages.Tag.SHA != otherSHA {
		t.Fatalf("doc=%+v", doc)
	}
}

func TestRunWatchTimeoutReturnsLastPendingSnapshot(t *testing.T) {
	github := &fixtureGitHub{tagMissing: true}
	clock := &fakeClock{now: time.Date(2026, 7, 16, 6, 0, 0, 0, time.UTC)}
	var stdout bytes.Buffer
	code := run([]string{
		"release", "watch", "--version", testVersion, "--repo", testRepository,
		"--interval", "30s", "--timeout", "1m", "--json",
	}, &stdout, &bytes.Buffer{}, dependencies{github: github, clock: clock})
	if code != exitWatchTimeout {
		t.Fatalf("code=%d stdout=%s", code, stdout.String())
	}
	var doc document
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Overall.State != "pending" || doc.Watch == nil || !doc.Watch.TimedOut || doc.Watch.Polls != 3 || doc.Watch.ElapsedSeconds != 60 {
		t.Fatalf("doc=%+v", doc)
	}
}

func TestRunRequiresMachineReadableOutputAndValidIdentity(t *testing.T) {
	tests := [][]string{
		{"release", "status", "--version", testVersion},
		{"release", "status", "--version", "0.0.8", "--json"},
		{"release", "status", "--version", testVersion, "--source-sha", "ABC", "--json"},
	}
	for _, args := range tests {
		var stderr bytes.Buffer
		if code := run(args, &bytes.Buffer{}, &stderr, dependencies{}); code != exitUsage {
			t.Fatalf("args=%v code=%d stderr=%s", args, code, stderr.String())
		}
	}
}

func TestDefaultWatchTimeoutCoversDeclaredReleaseJobLimits(t *testing.T) {
	if defaultStatusTimeout <= 0 || defaultStatusTimeout >= defaultWatchTimeout {
		t.Fatalf("default status timeout=%s watch timeout=%s", defaultStatusTimeout, defaultWatchTimeout)
	}
	if defaultWatchTimeout < 3*time.Hour {
		t.Fatalf("default watch timeout=%s", defaultWatchTimeout)
	}
}
