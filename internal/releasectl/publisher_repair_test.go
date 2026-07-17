package releasectl

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

const publisherTestVersion = "v0.0.9"

type publisherRepairGitHub struct {
	fixture        *fixtureGitHub
	dispatchedRuns []workflowRun
	mutations      []recordedMutation
}

func (g *publisherRepairGitHub) Get(ctx context.Context, endpoint string, query map[string]string, target any) error {
	switch endpoint {
	case "repos/" + testRepository + "/actions/workflows/build-binaries.yml":
		*(target.(*publisherWorkflowAPI)) = publisherWorkflowAPI{ID: 901, Name: "build-binaries", Path: ".github/workflows/build-binaries.yml", State: "active"}
		return nil
	case "repos/" + testRepository + "/actions/workflows/build-binaries.yml/runs":
		if response, ok := target.(*publisherRunsResponse); ok {
			response.WorkflowRuns = make([]workflowRun, len(g.dispatchedRuns))
			copy(response.WorkflowRuns, g.dispatchedRuns)
			response.TotalCount = len(response.WorkflowRuns)
			return nil
		}
	}
	return g.fixture.Get(ctx, endpoint, query, target)
}

func (g *publisherRepairGitHub) Mutate(_ context.Context, method, endpoint string, body, _ any) error {
	g.mutations = append(g.mutations, recordedMutation{method: method, endpoint: endpoint, body: body})
	return nil
}

func newPublisherRepairGitHub(mode string) *publisherRepairGitHub {
	fixture := successfulFixture()
	fixture.tagObject = gitObject{Type: "commit", SHA: testSHA}
	fixture.release.TagName = publisherTestVersion
	fixture.publisherRuns[0].HeadBranch = publisherTestVersion
	fixture.publisherRuns[0].Conclusion = "failure"
	for index := range fixture.jobs {
		fixture.jobs[index].Conclusion = "success"
	}
	switch mode {
	case "release-assets":
		fixture.releaseMissing = true
		for index := range fixture.jobs {
			switch fixture.jobs[index].Name {
			case "release":
				fixture.jobs[index].Conclusion = "failure"
			default:
				fixture.jobs[index].Conclusion = "skipped"
			}
		}
	case "homebrew":
		for index := range fixture.jobs {
			if fixture.jobs[index].Name == "homebrew" {
				fixture.jobs[index].Conclusion = "failure"
			}
			if fixture.jobs[index].Name == "health" {
				fixture.jobs[index].Conclusion = "skipped"
			}
		}
	case "health":
		for index := range fixture.jobs {
			if fixture.jobs[index].Name == "health" {
				fixture.jobs[index].Conclusion = "failure"
			}
		}
	default:
		panic("unsupported publisher repair fixture " + mode)
	}
	return &publisherRepairGitHub{fixture: fixture, dispatchedRuns: []workflowRun{}}
}

func TestPublisherRepairPlanDerivesAllCanonicalModesFromMachineStatus(t *testing.T) {
	contract := mustTestReleaseContract()
	for _, mode := range []string{"release-assets", "homebrew", "health"} {
		t.Run(mode, func(t *testing.T) {
			github := newPublisherRepairGitHub(mode)
			plan, err := planPublisherRepair(context.Background(), testRepository, publisherTestVersion, testSHA, contract, github, fixedClock())
			if err != nil {
				t.Fatal(err)
			}
			wantCode := map[string]string{
				"release-assets": "dispatch_release_assets_repair",
				"homebrew":       "dispatch_homebrew_repair",
				"health":         "dispatch_health_repair",
			}[mode]
			if plan.Schema != "env-vault.release-repair-plan.v1" || plan.Kind != repairKindPublisher || !plan.Applyable || plan.Inputs != (publisherRepairInputs{Version: publisherTestVersion, Repair: mode, StateDigest: plan.Preconditions.StateDigest}) || plan.Action.Code != wantCode || plan.Action.Method != "POST" {
				t.Fatalf("plan=%+v", plan)
			}
			wantEndpoint := "repos/" + testRepository + "/actions/workflows/build-binaries.yml/dispatches"
			if plan.WorkflowRef != publisherTestVersion || plan.Action.Endpoint != wantEndpoint || plan.Preconditions.TagSHA != testSHA || plan.Preconditions.DispatchSHA != testSHA || plan.Preconditions.StateDigest != digestPublisherRepairPreconditions(plan.Preconditions) || plan.PlanDigest != digestPublisherRepairPlan(plan) {
				t.Fatalf("identity or digest=%+v", plan)
			}
		})
	}
}

func TestPublisherRepairDerivesOnlySafeStructuredEvidenceRepairs(t *testing.T) {
	contract := mustTestReleaseContract()
	ciRun := completedRun(101, "push", "main", testSHA, "success")
	inventory := classifyPublisherArtifactInventory(ciRun, completePromotionArtifacts(ciRun), contract)
	base := publisherRepairStatusSnapshot{
		Overall: overall{State: "inconsistent", Terminal: true},
		Stages: stages{
			Tag: stage{State: stateSucceeded}, MainCI: stage{State: stateSucceeded}, Planning: stage{State: stateSucceeded},
			GitHubRelease: stage{State: stateSucceeded}, SupplyChain: stage{State: stateSucceeded}, Homebrew: stage{State: stateSucceeded},
		},
		FailedJobs: []failedJob{}, NextAction: nextAction{Code: "resolve_inconsistency"},
	}
	attestations := base
	attestations.Stages.SupplyChain = stage{State: stateInconsistent, Reason: "attestations_incomplete"}
	if mode, _, ok := classifyPublisherRepair(attestations, inventory); mode != "release-assets" || !ok {
		t.Fatalf("attestation evidence classification mode=%q applyable=%v", mode, ok)
	}
	homebrew := base
	homebrew.Stages.Homebrew = stage{State: stateInconsistent, Reason: "homebrew_pr_missing"}
	if mode, _, ok := classifyPublisherRepair(homebrew, inventory); mode != "homebrew" || !ok {
		t.Fatalf("Homebrew evidence classification mode=%q applyable=%v", mode, ok)
	}
	homebrew.Stages.Homebrew.Reason = "homebrew_pr_identity_mismatch"
	if mode, reason, ok := classifyPublisherRepair(homebrew, inventory); mode != "none" || ok || reason != "publisher_remote_state_inconsistent" {
		t.Fatalf("unsafe Homebrew inconsistency became repairable: mode=%q reason=%q applyable=%v", mode, reason, ok)
	}
}

func TestPublisherRepairUsesExactConsumedInventoryInsteadOfTenArtifactAttemptMatrix(t *testing.T) {
	github := newPublisherRepairGitHub("release-assets")
	for index := range github.fixture.artifacts {
		if strings.HasPrefix(github.fixture.artifacts[index].Name, "env-vault-promotion-platform-") {
			github.fixture.artifacts[index].Expired = true
		}
	}
	contract := mustTestReleaseContract()
	plan, err := planPublisherRepair(context.Background(), testRepository, publisherTestVersion, testSHA, contract, github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Applyable || plan.Inputs.Repair != "release-assets" || !plan.Preconditions.Artifacts.Complete || plan.Preconditions.Artifacts.ReasonCode != "publisher_promotion_inventory_complete" {
		t.Fatalf("expired non-consumed platform evidence blocked repair: %+v", plan)
	}
	if len(plan.Preconditions.Artifacts.Expected) != 6 || len(plan.Preconditions.Artifacts.Observed) != 6 {
		t.Fatalf("publisher inventory is not the exact six consumed artifacts: %+v", plan.Preconditions.Artifacts)
	}
}

func TestPublisherRepairBlocksMissingAndExpiredAggregateManifestDeterministically(t *testing.T) {
	for _, test := range []struct {
		name       string
		mutate     func(*publisherRepairGitHub)
		wantReason string
	}{
		{name: "missing", wantReason: "PUBLISHER_PROMOTION_MANIFEST_MISSING", mutate: func(github *publisherRepairGitHub) {
			artifacts := github.fixture.artifacts[:0]
			for _, artifact := range github.fixture.artifacts {
				if !strings.HasPrefix(artifact.Name, "env-vault-promotion-"+testSHA+"-attempt-") {
					artifacts = append(artifacts, artifact)
				}
			}
			github.fixture.artifacts = artifacts
		}},
		{name: "expired", wantReason: "PUBLISHER_PROMOTION_MANIFEST_EXPIRED", mutate: func(github *publisherRepairGitHub) {
			for index := range github.fixture.artifacts {
				if strings.HasPrefix(github.fixture.artifacts[index].Name, "env-vault-promotion-"+testSHA+"-attempt-") {
					github.fixture.artifacts[index].Expired = true
				}
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			github := newPublisherRepairGitHub("release-assets")
			test.mutate(github)
			plan, err := planPublisherRepair(context.Background(), testRepository, publisherTestVersion, testSHA, mustTestReleaseContract(), github, fixedClock())
			if err != nil {
				t.Fatal(err)
			}
			if plan.Applyable || plan.Action.Code != "none" || plan.Action.ReasonCode != test.wantReason || plan.Preconditions.Artifacts.Complete {
				t.Fatalf("invalid manifest became repairable: %+v", plan)
			}
		})
	}
}

func TestPublisherRepairInventoryRejectsDuplicateStaleWrongRunAndUnexpectedArtifacts(t *testing.T) {
	for _, test := range []struct {
		name       string
		mutate     func(*publisherRepairGitHub)
		wantReason string
	}{
		{name: "duplicate", wantReason: "PUBLISHER_PROMOTION_ARTIFACT_DUPLICATE", mutate: func(github *publisherRepairGitHub) {
			duplicate := github.fixture.artifacts[0]
			duplicate.ID = 9001
			github.fixture.artifacts = append(github.fixture.artifacts, duplicate)
		}},
		{name: "stale", wantReason: "PUBLISHER_PROMOTION_ARTIFACT_STALE", mutate: func(github *publisherRepairGitHub) {
			github.fixture.artifacts[0].WorkflowRun.HeadSHA = otherSHA
		}},
		{name: "wrong-run", wantReason: "PUBLISHER_PROMOTION_ARTIFACT_WRONG_RUN", mutate: func(github *publisherRepairGitHub) {
			github.fixture.artifacts[0].WorkflowRun.ID++
		}},
		{name: "unexpected", wantReason: "PUBLISHER_PROMOTION_ARTIFACT_UNEXPECTED", mutate: func(github *publisherRepairGitHub) {
			unexpected := github.fixture.artifacts[0]
			unexpected.ID = 9002
			unexpected.Name = "env-vault-release-linux-riscv64-attempt-1"
			github.fixture.artifacts = append(github.fixture.artifacts, unexpected)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			github := newPublisherRepairGitHub("release-assets")
			test.mutate(github)
			plan, err := planPublisherRepair(context.Background(), testRepository, publisherTestVersion, testSHA, mustTestReleaseContract(), github, fixedClock())
			if err != nil {
				t.Fatal(err)
			}
			if plan.Applyable || plan.Action.ReasonCode != test.wantReason || plan.Preconditions.Artifacts.Complete {
				t.Fatalf("invalid publisher artifact inventory became repairable: %+v", plan)
			}
		})
	}
}

func TestPublisherRepairApplyReobservesPublisherArtifactInventory(t *testing.T) {
	github := newPublisherRepairGitHub("release-assets")
	contract := mustTestReleaseContract()
	plan, err := planPublisherRepair(context.Background(), testRepository, publisherTestVersion, testSHA, contract, github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Applyable {
		t.Fatalf("initial exact inventory is not repairable: %+v", plan.Preconditions.Artifacts)
	}
	for index := range github.fixture.artifacts {
		if github.fixture.artifacts[index].Name == "env-vault-promotion-"+testSHA+"-attempt-1" {
			github.fixture.artifacts[index].Expired = true
		}
	}
	doc, code := applyPublisherRepair(context.Background(), plan, plan.PlanDigest, true, contract, github, github, fixedClock())
	if code != exitPreconditionFailed || doc.Error == nil || doc.Error.Code != "REMOTE_PRECONDITION_FAILED" || len(github.mutations) != 0 {
		t.Fatalf("artifact inventory drift was not rejected: code=%d doc=%+v mutations=%+v", code, doc, github.mutations)
	}
}

func TestPublisherRepairStateDigestBindsArtifactIdentityNotOnlyName(t *testing.T) {
	github := newPublisherRepairGitHub("release-assets")
	contract := mustTestReleaseContract()
	plan, err := planPublisherRepair(context.Background(), testRepository, publisherTestVersion, testSHA, contract, github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	github.fixture.artifacts[0].ID += 10_000
	reobserved, err := planPublisherRepair(context.Background(), testRepository, publisherTestVersion, testSHA, contract, github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	if reobserved.Preconditions.StateDigest == plan.Preconditions.StateDigest || reobserved.PlanDigest == plan.PlanDigest {
		t.Fatalf("same-name artifact replacement did not change the plan identity: before=%+v after=%+v", plan.Preconditions.Artifacts, reobserved.Preconditions.Artifacts)
	}
	doc, code := applyPublisherRepair(context.Background(), plan, plan.PlanDigest, true, contract, github, github, fixedClock())
	if code != exitPreconditionFailed || doc.Error == nil || doc.Error.Code != "REMOTE_PRECONDITION_FAILED" || len(github.mutations) != 0 {
		t.Fatalf("same-name artifact replacement was not rejected: code=%d doc=%+v mutations=%+v", code, doc, github.mutations)
	}
}

func TestPublisherRepairApplyIsDryRunThenUsesOnlyCanonicalDispatchBody(t *testing.T) {
	github := newPublisherRepairGitHub("homebrew")
	contract := mustTestReleaseContract()
	plan, err := planPublisherRepair(context.Background(), testRepository, publisherTestVersion, testSHA, contract, github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	dry, code := applyPublisherRepair(context.Background(), plan, plan.PlanDigest, false, contract, github, github, fixedClock())
	if code != exitSuccess || !dry.OK || dry.Status != "dry_run" || !dry.DryRun || len(github.mutations) != 0 {
		t.Fatalf("dry=%+v error=%+v code=%d mutations=%+v", dry, dry.Error, code, github.mutations)
	}
	applied, code := applyPublisherRepair(context.Background(), plan, plan.PlanDigest, true, contract, github, github, fixedClock())
	if code != exitSuccess || !applied.OK || applied.Status != "applied" || len(github.mutations) != 1 {
		t.Fatalf("applied=%+v code=%d mutations=%+v", applied, code, github.mutations)
	}
	mutation := github.mutations[0]
	if mutation.method != "POST" || mutation.endpoint != "repos/"+testRepository+"/actions/workflows/build-binaries.yml/dispatches" {
		t.Fatalf("mutation=%+v", mutation)
	}
	wantBody := map[string]any{"ref": publisherTestVersion, "inputs": map[string]string{
		"version": publisherTestVersion, "repair": "homebrew", "repair_state_digest": plan.Preconditions.StateDigest,
	}}
	if !reflect.DeepEqual(mutation.body, wantBody) {
		t.Fatalf("body=%#v want=%#v", mutation.body, wantBody)
	}
}

func TestPublisherRepairApplyRejectsRecomputedEndpointInputAndRefDrift(t *testing.T) {
	contract := mustTestReleaseContract()
	for _, mutate := range []struct {
		name string
		fn   func(*publisherRepairPlan, *publisherRepairGitHub)
	}{
		{name: "endpoint", fn: func(plan *publisherRepairPlan, _ *publisherRepairGitHub) {
			plan.Action.Endpoint = "repos/" + testRepository + "/actions/workflows/ci.yml/dispatches"
			plan.PlanDigest = digestPublisherRepairPlan(*plan)
		}},
		{name: "input", fn: func(plan *publisherRepairPlan, _ *publisherRepairGitHub) {
			plan.Inputs.Repair = "health"
			plan.PlanDigest = digestPublisherRepairPlan(*plan)
		}},
		{name: "ref", fn: func(plan *publisherRepairPlan, _ *publisherRepairGitHub) {
			plan.WorkflowRef = "main"
			plan.PlanDigest = digestPublisherRepairPlan(*plan)
		}},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			github := newPublisherRepairGitHub("homebrew")
			plan, err := planPublisherRepair(context.Background(), testRepository, publisherTestVersion, testSHA, contract, github, fixedClock())
			if err != nil {
				t.Fatal(err)
			}
			mutate.fn(&plan, github)
			doc, code := applyPublisherRepair(context.Background(), plan, plan.PlanDigest, true, contract, github, github, fixedClock())
			if code != exitPreconditionFailed || doc.Error == nil || len(github.mutations) != 0 {
				t.Fatalf("alteration accepted: code=%d doc=%+v error=%+v mutations=%+v", code, doc, doc.Error, github.mutations)
			}
		})
	}
}

func TestPublisherRepairApplyDetectsEquivalentPostPlanDispatchIdempotently(t *testing.T) {
	github := newPublisherRepairGitHub("health")
	contract := mustTestReleaseContract()
	plan, err := planPublisherRepair(context.Background(), testRepository, publisherTestVersion, testSHA, contract, github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	run := completedRun(991, "workflow_dispatch", publisherTestVersion, testSHA, "success")
	run.Path = ".github/workflows/build-binaries.yml"
	run.DisplayTitle = publisherRepairRunTitle(plan.Version, plan.Inputs.Repair, plan.Inputs.StateDigest)
	github.dispatchedRuns = []workflowRun{run}
	doc, code := applyPublisherRepair(context.Background(), plan, plan.PlanDigest, true, contract, github, github, fixedClock())
	if code != exitSuccess || !doc.OK || doc.Status != "already_applied" || doc.WorkflowRun == nil || doc.WorkflowRun.ID != run.ID || len(github.mutations) != 0 {
		t.Fatalf("idempotency=%+v code=%d mutations=%+v", doc, code, github.mutations)
	}
}

func TestPublisherRepairDoesNotTreatDifferentStateDigestAsEquivalent(t *testing.T) {
	github := newPublisherRepairGitHub("health")
	contract := mustTestReleaseContract()
	plan, err := planPublisherRepair(context.Background(), testRepository, publisherTestVersion, testSHA, contract, github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	run := completedRun(993, "workflow_dispatch", publisherTestVersion, testSHA, "failure")
	run.Path = ".github/workflows/build-binaries.yml"
	run.DisplayTitle = publisherRepairRunTitle(plan.Version, plan.Inputs.Repair, strings.Repeat("9", 64))
	github.dispatchedRuns = []workflowRun{run}
	doc, code := applyPublisherRepair(context.Background(), plan, plan.PlanDigest, true, contract, github, github, fixedClock())
	if code != exitSuccess || !doc.OK || doc.Status != "applied" || len(github.mutations) != 1 {
		t.Fatalf("different digest blocked exact dispatch: doc=%+v code=%d mutations=%+v", doc, code, github.mutations)
	}
}

func TestPublisherRepairFailsClosedOnSpoofedEquivalentDispatchAndBlockedVersions(t *testing.T) {
	github := newPublisherRepairGitHub("health")
	contract := mustTestReleaseContract()
	plan, err := planPublisherRepair(context.Background(), testRepository, publisherTestVersion, testSHA, contract, github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	run := completedRun(992, "workflow_dispatch", publisherTestVersion, otherSHA, "success")
	run.Path = ".github/workflows/build-binaries.yml"
	run.DisplayTitle = publisherRepairRunTitle(plan.Version, plan.Inputs.Repair, plan.Inputs.StateDigest)
	github.dispatchedRuns = []workflowRun{run}
	doc, code := applyPublisherRepair(context.Background(), plan, plan.PlanDigest, true, contract, github, github, fixedClock())
	if code != exitPreconditionFailed || doc.Error == nil || doc.Error.Code != "REMOTE_PRECONDITION_FAILED" || len(github.mutations) != 0 {
		t.Fatalf("spoofed run accepted: code=%d doc=%+v", code, doc)
	}
	for _, blocked := range []string{"v0.0.7", "v0.0.8"} {
		_, err := planPublisherRepair(context.Background(), testRepository, blocked, testSHA, contract, github, fixedClock())
		if err == nil || !strings.Contains(operatorErrorInfo(err).Operation, "publisher_repair") {
			t.Fatalf("blocked version %s was observed or planned: %v", blocked, err)
		}
	}
}

func TestPublisherRepairDoesNotMaskMovedTagAsAlreadyApplied(t *testing.T) {
	github := newPublisherRepairGitHub("health")
	contract := mustTestReleaseContract()
	plan, err := planPublisherRepair(context.Background(), testRepository, publisherTestVersion, testSHA, contract, github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	run := completedRun(994, "workflow_dispatch", publisherTestVersion, testSHA, "success")
	run.Path = ".github/workflows/build-binaries.yml"
	run.DisplayTitle = publisherRepairRunTitle(plan.Version, plan.Inputs.Repair, plan.Inputs.StateDigest)
	github.dispatchedRuns = []workflowRun{run}
	github.fixture.tagObject.SHA = otherSHA
	doc, code := applyPublisherRepair(context.Background(), plan, plan.PlanDigest, true, contract, github, github, fixedClock())
	if code != exitPreconditionFailed || doc.Error == nil || doc.Error.Code != "REMOTE_PRECONDITION_FAILED" || len(github.mutations) != 0 {
		t.Fatalf("moved tag was masked by idempotency: doc=%+v error=%+v code=%d", doc, doc.Error, code)
	}
}

func TestPublisherRepairPlanCLISelectsVersionAndSourceWithoutRunID(t *testing.T) {
	github := newPublisherRepairGitHub("health")
	var stdout, stderr strings.Builder
	code := run([]string{
		"release", "repair", "plan", "--repo", testRepository, "--version", publisherTestVersion,
		"--source-sha", testSHA, "--contract", "../../release/contract.v1.json", "--json",
	}, &stdout, &stderr, dependencies{github: github, clock: fixedClock()})
	if code != exitSuccess || stderr.Len() != 0 || strings.Count(stdout.String(), "\n") != 1 || !strings.Contains(stdout.String(), `"kind":"publisher_dispatch"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestPublisherRepairPlanCLIBlocksV008BeforeRemoteObservation(t *testing.T) {
	var stdout, stderr strings.Builder
	code := run([]string{
		"release", "repair", "plan", "--repo", testRepository, "--version", "v0.0.8",
		"--source-sha", testSHA, "--contract", "../../release/contract.v1.json", "--json",
	}, &stdout, &stderr, dependencies{clock: fixedClock()})
	if code != exitMutationBlocked || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"code":"REMOTE_PRECONDITION_FAILED"`) || !strings.Contains(stdout.String(), `"operation":"publisher_repair_blocked_version"`) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
