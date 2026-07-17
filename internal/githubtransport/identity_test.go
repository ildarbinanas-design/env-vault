package githubtransport

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestExactIntEnforcesNativeRangeBeforeConversion(t *testing.T) {
	maximum, overflow := "9223372036854775807", "9223372036854775808"
	if strconv.IntSize == 32 {
		maximum, overflow = "2147483647", "2147483648"
	}
	value, err := exactInt(map[string]json.RawMessage{"value": json.RawMessage(maximum)}, "value")
	if err != nil || strconv.Itoa(value) != maximum {
		t.Fatalf("native maximum value=%d error=%v", value, err)
	}
	for name, input := range map[string]string{
		"native overflow": overflow,
		"negative":        "-1",
		"zero":            "0",
		"non-integer":     "1.5",
		"exponent":        "1e0",
	} {
		t.Run(name, func(t *testing.T) {
			value, err := exactInt(map[string]json.RawMessage{"value": json.RawMessage(input)}, "value")
			if err == nil || value != 0 || !strings.Contains(err.Error(), "outside integer range") {
				t.Fatalf("value=%d error=%v", value, err)
			}
		})
	}
}

func TestActionsIdentitySurvivesCustomRunNameAndEmptyPullRequests(t *testing.T) {
	repository := "example/repo"
	head := strings.Repeat("a", 40)
	runID, attempt, jobID := int64(92), 2, int64(93)
	jobURL := fmt.Sprintf("https://github.com/%s/actions/runs/%d/job/%d", repository, runID, jobID)
	runner := &scriptedRunner{responses: []CommandResult{
		liveResponse(200, "", fmt.Sprintf(`{
          "id":92,"run_attempt":2,"repository":{"full_name":"example/repo"},
          "head_repository":{"full_name":"example/repo"},"head_sha":%q,
          "head_branch":"release-please--branches--main--components--env-vault",
          "event":"pull_request","status":"completed","conclusion":"success",
          "path":".github/workflows/ci.yml","html_url":"https://github.com/example/repo/actions/runs/92",
          "name":"custom release title","pull_requests":[]}`, head)),
		liveResponse(200, "", fmt.Sprintf(`{"total_count":1,"jobs":[{
          "id":93,"run_id":92,"head_sha":%q,"name":"quality-gate",
          "workflow_name":"custom release title","status":"completed","conclusion":"success",
          "html_url":%q}]}`, head, jobURL)),
	}}
	var sleeps []time.Duration
	document, transportErr := testClient(runner, &sleeps).ResolveActionsIdentity(context.Background(), ActionsIdentityOptions{
		Repository: repository, RunID: runID, RunAttempt: attempt,
		WorkflowPath: ".github/workflows/ci.yml", Event: "pull_request", HeadSHA: head,
		HeadRef: "release-please--branches--main--components--env-vault",
		JobID:   jobID, JobName: "quality-gate", JobURL: jobURL,
	})
	if transportErr != nil {
		t.Fatal(transportErr)
	}
	if !document.OK || document.Run.DiagnosticName != "custom release title" || document.Job == nil || document.Job.RunAttempt != attempt || document.Job.DiagnosticWorkflowName != "custom release title" {
		t.Fatalf("identity=%+v", document)
	}
	if strings.Contains(strings.Join(runner.apiCalls[1], " "), "actions/jobs/93") || !strings.Contains(strings.Join(runner.apiCalls[1], " "), "/attempts/2/jobs") {
		t.Fatalf("job lookup was not attempt-qualified: %v", runner.apiCalls[1])
	}
}

func TestActionsIdentityRejectsWrongAttemptHeadURLAndStaleJob(t *testing.T) {
	repository := "example/repo"
	head := strings.Repeat("a", 40)
	baseRun := fmt.Sprintf(`{"id":92,"run_attempt":2,"repository":{"full_name":"example/repo"},"head_repository":{"full_name":"example/repo"},"head_sha":%q,"head_branch":"main","event":"push","status":"completed","conclusion":"success","path":".github/workflows/ci.yml","html_url":"https://github.com/example/repo/actions/runs/92"}`, head)
	for name, jobs := range map[string]string{
		"stale job":              `{"total_count":1,"jobs":[{"id":94,"run_id":92,"head_sha":"` + head + `","name":"quality-gate","status":"completed","conclusion":"success","html_url":"https://github.com/example/repo/actions/runs/92/job/94"}]}`,
		"wrong run id":           `{"total_count":1,"jobs":[{"id":93,"run_id":91,"head_sha":"` + head + `","name":"quality-gate","status":"completed","conclusion":"success","html_url":"https://github.com/example/repo/actions/runs/92/job/93"}]}`,
		"wrong check name":       `{"total_count":1,"jobs":[{"id":93,"run_id":92,"head_sha":"` + head + `","name":"not-quality-gate","status":"completed","conclusion":"success","html_url":"https://github.com/example/repo/actions/runs/92/job/93"}]}`,
		"wrong job url":          `{"total_count":1,"jobs":[{"id":93,"run_id":92,"head_sha":"` + head + `","name":"quality-gate","status":"completed","conclusion":"success","html_url":"https://github.com/example/repo/actions/runs/92/job/999"}]}`,
		"wrong head":             `{"total_count":1,"jobs":[{"id":93,"run_id":92,"head_sha":"` + strings.Repeat("b", 40) + `","name":"quality-gate","status":"completed","conclusion":"success","html_url":"https://github.com/example/repo/actions/runs/92/job/93"}]}`,
		"wrong optional attempt": `{"total_count":1,"jobs":[{"id":93,"run_id":92,"run_attempt":1,"head_sha":"` + head + `","name":"quality-gate","status":"completed","conclusion":"success","html_url":"https://github.com/example/repo/actions/runs/92/job/93"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			runner := &scriptedRunner{responses: []CommandResult{liveResponse(200, "", baseRun), liveResponse(200, "", jobs)}}
			var sleeps []time.Duration
			_, err := testClient(runner, &sleeps).ResolveActionsIdentity(context.Background(), ActionsIdentityOptions{
				Repository: repository, RunID: 92, RunAttempt: 2, WorkflowPath: ".github/workflows/ci.yml", Event: "push", HeadSHA: head, HeadRef: "main",
				JobID: 93, JobName: "quality-gate", JobURL: "https://github.com/example/repo/actions/runs/92/job/93",
			})
			if err == nil || err.Code != "IDENTITY_MISMATCH" {
				t.Fatalf("error=%+v", err)
			}
		})
	}
}

func TestActionsIdentityRejectsWrongRunAttemptObject(t *testing.T) {
	head := strings.Repeat("a", 40)
	runner := &scriptedRunner{responses: []CommandResult{liveResponse(200, "", fmt.Sprintf(`{
      "id":92,"run_attempt":1,"repository":{"full_name":"example/repo"},
      "head_repository":{"full_name":"example/repo"},"head_sha":%q,"head_branch":"main",
      "event":"push","status":"completed","conclusion":"success",
      "path":".github/workflows/ci.yml","html_url":"https://github.com/example/repo/actions/runs/92"}`, head))}}
	var sleeps []time.Duration
	_, transportErr := testClient(runner, &sleeps).ResolveActionsIdentity(context.Background(), ActionsIdentityOptions{
		Repository: "example/repo", RunID: 92, RunAttempt: 2, WorkflowPath: ".github/workflows/ci.yml",
		Event: "push", HeadSHA: head, HeadRef: "main",
	})
	if transportErr == nil || transportErr.Code != "IDENTITY_MISMATCH" || len(runner.apiCalls) != 1 {
		t.Fatalf("error=%+v calls=%d", transportErr, len(runner.apiCalls))
	}
}

func TestActionsIdentityAcceptsDirectHeadRunWithoutPullRequestAssociation(t *testing.T) {
	head := strings.Repeat("a", 40)
	runner := &scriptedRunner{responses: []CommandResult{liveResponse(200, "", fmt.Sprintf(`{
      "id":92,"run_attempt":2,"repository":{"full_name":"example/repo"},
      "head_repository":{"full_name":"example/repo"},"head_sha":%q,"head_branch":"main",
      "event":"push","status":"completed","conclusion":"success",
      "path":".github/workflows/ci.yml","html_url":"https://github.com/example/repo/actions/runs/92",
      "name":"CI","pull_requests":[]}`, head))}}
	var sleeps []time.Duration
	document, transportErr := testClient(runner, &sleeps).ResolveActionsIdentity(context.Background(), ActionsIdentityOptions{
		Repository: "example/repo", RunID: 92, RunAttempt: 2, WorkflowPath: ".github/workflows/ci.yml",
		Event: "push", HeadSHA: head, HeadRef: "main",
	})
	if transportErr != nil || !document.OK || document.Run.HeadRef != "main" {
		t.Fatalf("document=%+v error=%+v", document, transportErr)
	}
}

func TestTypedIdentityRejectsDotRepositoriesAndUnsafeWorkflowPathsBeforeNetwork(t *testing.T) {
	for name, options := range map[string]ActionsIdentityOptions{
		"dot repository": {
			Repository: "../..", RunID: 1, RunAttempt: 1, WorkflowPath: ".github/workflows/ci.yml",
			Event: "push", HeadSHA: strings.Repeat("a", 40), HeadRef: "main",
		},
		"workflow traversal": {
			Repository: "example/repo", RunID: 1, RunAttempt: 1, WorkflowPath: ".github/workflows/../ci.yml",
			Event: "push", HeadSHA: strings.Repeat("a", 40), HeadRef: "main",
		},
		"nested workflow": {
			Repository: "example/repo", RunID: 1, RunAttempt: 1, WorkflowPath: ".github/workflows/sub/ci.yml",
			Event: "push", HeadSHA: strings.Repeat("a", 40), HeadRef: "main",
		},
		"non yaml workflow": {
			Repository: "example/repo", RunID: 1, RunAttempt: 1, WorkflowPath: ".github/workflows/ci.txt",
			Event: "push", HeadSHA: strings.Repeat("a", 40), HeadRef: "main",
		},
	} {
		t.Run(name, func(t *testing.T) {
			runner := &scriptedRunner{}
			var sleeps []time.Duration
			_, transportErr := testClient(runner, &sleeps).ResolveActionsIdentity(context.Background(), options)
			if transportErr == nil || transportErr.Code != "INPUT_INVALID" || len(runner.allCalls) != 0 {
				t.Fatalf("error=%+v calls=%v", transportErr, runner.allCalls)
			}
		})
	}
}

func TestTypedIdentityRejectsUnsafeEnumsRefsAndJobNamesBeforeNetwork(t *testing.T) {
	head := strings.Repeat("a", 40)
	base := ActionsIdentityOptions{
		Repository: "example/repo", RunID: 1, RunAttempt: 1,
		WorkflowPath: ".github/workflows/ci.yml", Event: "push", HeadSHA: head, HeadRef: "main",
	}
	tests := map[string]func(*ActionsIdentityOptions){
		"unknown event":       func(options *ActionsIdentityOptions) { options.Event = "schedule" },
		"unknown status":      func(options *ActionsIdentityOptions) { options.Status = "queued" },
		"unknown conclusion":  func(options *ActionsIdentityOptions) { options.Conclusion = "future_state" },
		"control in head ref": func(options *ActionsIdentityOptions) { options.HeadRef = "main\nunsafe" },
		"ref traversal":       func(options *ActionsIdentityOptions) { options.HeadRef = "release/../main" },
		"oversize ref":        func(options *ActionsIdentityOptions) { options.HeadRef = strings.Repeat("a", 256) },
		"control in job name": func(options *ActionsIdentityOptions) {
			options.JobID = 2
			options.JobName = "quality\ngate"
			options.JobURL = "https://github.com/example/repo/actions/runs/1/job/2"
		},
		"oversize job name": func(options *ActionsIdentityOptions) {
			options.JobID = 2
			options.JobName = strings.Repeat("q", 129)
			options.JobURL = "https://github.com/example/repo/actions/runs/1/job/2"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			options := base
			mutate(&options)
			runner := &scriptedRunner{}
			var sleeps []time.Duration
			_, transportErr := testClient(runner, &sleeps).ResolveActionsIdentity(context.Background(), options)
			if transportErr == nil || transportErr.Code != "INPUT_INVALID" || len(runner.allCalls) != 0 {
				t.Fatalf("error=%+v calls=%v", transportErr, runner.allCalls)
			}
		})
	}
}
