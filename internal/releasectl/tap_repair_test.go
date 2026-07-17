package releasectl

import (
	"bytes"
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"
)

type tapRepairGitHub struct {
	*publisherRepairGitHub
	tapRun workflowRun
}

type advancingTapRepairGitHub struct {
	*tapRepairGitHub
	prRunLists int
}

func (g *advancingTapRepairGitHub) Get(ctx context.Context, endpoint string, query map[string]string, target any) error {
	if strings.Contains(endpoint, "/actions/workflows/test-formula.yml/runs") && query["event"] == "pull_request" {
		g.prRunLists++
		if g.prRunLists >= 2 {
			g.tapRun.Status = "completed"
			g.tapRun.Conclusion = "success"
			g.fixture.tapPRRuns = []workflowRun{g.tapRun}
		}
	}
	return g.tapRepairGitHub.Get(ctx, endpoint, query, target)
}

func (g *tapRepairGitHub) Get(ctx context.Context, endpoint string, query map[string]string, target any) error {
	if endpoint == "repos/"+mustTestTapRepository()+"/actions/runs/"+strconv.FormatInt(g.tapRun.ID, 10) {
		*(target.(*workflowRun)) = g.tapRun
		return nil
	}
	return g.publisherRepairGitHub.Get(ctx, endpoint, query, target)
}

func (g *tapRepairGitHub) Mutate(_ context.Context, method, endpoint string, body, _ any) error {
	g.mutations = append(g.mutations, recordedMutation{method: method, endpoint: endpoint, body: body})
	g.tapRun.RunAttempt++
	g.tapRun.Status = "in_progress"
	g.tapRun.Conclusion = ""
	if g.tapRun.Event == "pull_request" {
		g.fixture.tapPRRuns = []workflowRun{g.tapRun}
	} else {
		g.fixture.tapPushRuns = []workflowRun{g.tapRun}
	}
	return nil
}

func newTapRepairGitHub(stageName string) *tapRepairGitHub {
	base := newPublisherRepairGitHub("homebrew")
	var run workflowRun
	switch stageName {
	case "pr_head":
		pull := fixtureHomebrewPull(publisherTestVersion)
		pull.State, pull.MergedAt, pull.MergeCommitSHA = "open", nil, ""
		base.fixture.tapPulls, base.fixture.tapPullsSet = []pullRequestResponse{pull}, true
		run = completedTapRun(403, "pull_request", "release/env-vault-"+publisherTestVersion, tapHeadSHA)
		run.Conclusion = "failure"
		base.fixture.tapPRRuns, base.fixture.tapPRRunsSet = []workflowRun{run}, true
	case "post_merge":
		run = completedTapRun(404, "push", "main", tapMergeSHA)
		run.Conclusion = "failure"
		base.fixture.tapPushRuns, base.fixture.tapPushRunsSet = []workflowRun{run}, true
	default:
		panic("unsupported tap repair stage")
	}
	return &tapRepairGitHub{publisherRepairGitHub: base, tapRun: run}
}

func TestTapCIRepairPlanSelectsExactFullRunRerunBeforePublisher(t *testing.T) {
	contract := mustTestReleaseContract()
	for _, stageName := range []string{"pr_head", "post_merge"} {
		t.Run(stageName, func(t *testing.T) {
			github := newTapRepairGitHub(stageName)
			value, err := planPublisherOrTapRepair(context.Background(), testRepository, publisherTestVersion, testSHA, contract, github, fixedClock())
			if err != nil {
				t.Fatal(err)
			}
			plan, ok := value.(tapCIRepairPlan)
			if !ok {
				t.Fatalf("plan type=%T, want tapCIRepairPlan", value)
			}
			wantCode := map[string]string{"pr_head": "rerun_tap_pr_ci_all_jobs", "post_merge": "rerun_tap_post_merge_ci_all_jobs"}[stageName]
			wantEndpoint := "repos/" + mustTestTapRepository() + "/actions/runs/" + strconv.FormatInt(plan.Run.ID, 10) + "/rerun"
			if plan.Kind != repairKindTapCI || plan.Stage != stageName || !plan.Applyable || plan.Action.Code != wantCode || plan.Action.ReasonCode != "TAP_CI_ATTEMPT_FAILED" || plan.Action.Method != "POST" || plan.Action.Endpoint != wantEndpoint || plan.NextAction.Code != "wait_tap_ci" {
				t.Fatalf("plan=%+v", plan)
			}
			if strings.Contains(plan.Action.Endpoint, "rerun-failed-jobs") || plan.Repository != mustTestTapRepository() || plan.SourceRepository != testRepository || plan.Preconditions.StateDigest == "" || plan.PlanDigest != digestTapCIRepairPlan(plan) {
				t.Fatalf("unsafe or incomplete plan=%+v", plan)
			}
			publisherPlan, err := planPublisherRepair(context.Background(), testRepository, publisherTestVersion, testSHA, contract, github, fixedClock())
			if err != nil {
				t.Fatal(err)
			}
			if publisherPlan.Applyable || publisherPlan.Action.Code != "none" || publisherPlan.Action.ReasonCode != "tap_ci_repair_required" {
				t.Fatalf("publisher was dispatched before tap CI repair: %+v", publisherPlan)
			}
		})
	}
}

func TestTapCIRepairApplyIsDryRunByDefaultAndRechecksExactRemoteState(t *testing.T) {
	github := newTapRepairGitHub("pr_head")
	contract := mustTestReleaseContract()
	value, err := planPublisherOrTapRepair(context.Background(), testRepository, publisherTestVersion, testSHA, contract, github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	plan := value.(tapCIRepairPlan)

	dryRun, code := applyTapCIRepair(context.Background(), plan, plan.PlanDigest, false, contract, github, github, fixedClock())
	if code != exitSuccess || !dryRun.OK || dryRun.Status != "dry_run" || len(github.mutations) != 0 {
		t.Fatalf("dry run mutated remote state: code=%d doc=%+v mutations=%v", code, dryRun, github.mutations)
	}

	github.tapRun.HeadSHA = otherSHA
	drifted, code := applyTapCIRepair(context.Background(), plan, plan.PlanDigest, true, contract, github, github, fixedClock())
	if code != exitPreconditionFailed || drifted.Error == nil || drifted.Error.Code != "REMOTE_PRECONDITION_FAILED" || len(github.mutations) != 0 {
		t.Fatalf("run identity drift was not blocked: code=%d doc=%+v", code, drifted)
	}
}

func TestTapCIRepairApplyStartsNewAttemptAndReturnsWatchAction(t *testing.T) {
	github := newTapRepairGitHub("post_merge")
	contract := mustTestReleaseContract()
	value, err := planPublisherOrTapRepair(context.Background(), testRepository, publisherTestVersion, testSHA, contract, github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	plan := value.(tapCIRepairPlan)
	doc, code := applyTapCIRepair(context.Background(), plan, plan.PlanDigest, true, contract, github, github, fixedClock())
	if code != exitSuccess || !doc.OK || doc.Status != "applied" || doc.FromAttempt != 1 || doc.ToAttempt != 2 || doc.NextAction == nil || doc.NextAction.Code != "wait_tap_ci" {
		t.Fatalf("apply result: code=%d doc=%+v", code, doc)
	}
	if len(github.mutations) != 1 || github.mutations[0].method != "POST" || github.mutations[0].endpoint != plan.Action.Endpoint || github.mutations[0].body != nil {
		t.Fatalf("mutations=%+v", github.mutations)
	}
	if strings.Contains(github.mutations[0].endpoint, "rerun-failed-jobs") {
		t.Fatalf("partial rerun endpoint used: %s", github.mutations[0].endpoint)
	}

	again, code := applyTapCIRepair(context.Background(), plan, plan.PlanDigest, true, contract, github, github, fixedClock())
	if code != exitSuccess || !again.OK || again.Status != "already_applied" || again.NextAction == nil || again.NextAction.Code != "wait_tap_ci" || len(github.mutations) != 1 {
		t.Fatalf("idempotent apply: code=%d doc=%+v mutations=%+v", code, again, github.mutations)
	}
}

func TestFailedHomebrewStageExposesMachineTapRepairAndReplanActions(t *testing.T) {
	github := newTapRepairGitHub("pr_head")
	doc, err := (collector{github: github, clock: fixedClock(), contract: mustTestReleaseContract()}).snapshot(context.Background(), query{
		Repository: testRepository, Version: publisherTestVersion, SourceSHA: testSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.NextAction == nil || doc.NextAction.Code != "rerun_tap_pr_ci_all_jobs" || doc.Stages.Homebrew.Homebrew == nil || doc.Stages.Homebrew.Homebrew.PRHeadCI == nil {
		t.Fatalf("failed PR-head run lacks deterministic repair evidence: %+v", doc)
	}

	github.tapRun.RunAttempt = 2
	github.tapRun.Status = "in_progress"
	github.tapRun.Conclusion = ""
	github.fixture.tapPRRuns = []workflowRun{github.tapRun}
	doc, err = (collector{github: github, clock: fixedClock(), contract: mustTestReleaseContract()}).snapshot(context.Background(), query{
		Repository: testRepository, Version: publisherTestVersion, SourceSHA: testSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.NextAction == nil || doc.NextAction.Code != "wait_tap_ci" {
		t.Fatalf("running tap repair is not watchable: %+v", doc.NextAction)
	}

	github.tapRun.Status = "completed"
	github.tapRun.Conclusion = "success"
	github.fixture.tapPRRuns = []workflowRun{github.tapRun}
	doc, err = (collector{github: github, clock: fixedClock(), contract: mustTestReleaseContract()}).snapshot(context.Background(), query{
		Repository: testRepository, Version: publisherTestVersion, SourceSHA: testSHA,
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.NextAction == nil || doc.NextAction.Code != "replan_publisher" {
		t.Fatalf("successful tap repair does not request publisher replanning: %+v", doc.NextAction)
	}
}

func TestReleaseWatchWaitsForTapAttemptThenRequestsPublisherReplan(t *testing.T) {
	base := newTapRepairGitHub("pr_head")
	base.tapRun.RunAttempt = 2
	base.tapRun.Status = "in_progress"
	base.tapRun.Conclusion = ""
	base.fixture.tapPRRuns = []workflowRun{base.tapRun}
	github := &advancingTapRepairGitHub{tapRepairGitHub: base}
	clk := fixedClock()
	var stdout bytes.Buffer
	code := runWatch(context.Background(), &stdout, query{
		Repository: testRepository, Version: publisherTestVersion, SourceSHA: testSHA,
	}, time.Second, 10*time.Second, dependencies{github: github, clock: clk, contract: mustTestReleaseContract()})
	if code != exitReleaseFailure {
		t.Fatalf("code=%d output=%s", code, stdout.String())
	}
	var doc document
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Watch == nil || doc.Watch.Polls != 2 || doc.Watch.TimedOut || doc.NextAction == nil || doc.NextAction.Code != "replan_publisher" {
		t.Fatalf("watch result=%+v", doc)
	}
}
