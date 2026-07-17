package releasectl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const (
	legacyTestVersion = "v0.0.7"
	legacyTestSHA     = "4fbae380747e75a1f59498adbd76ccf5791e0480"
	legacyControlSHA  = "4444444444444444444444444444444444444444"
)

type legacyGitHub struct {
	tagSHA              string
	release             legacyReleaseAPI
	runs                []workflowRun
	runsTotalOverride   *int
	recordDispatchedRun bool
	mutations           []recordedMutation
}

func newLegacyGitHub() *legacyGitHub {
	contract := mustTestReleaseContract()
	assets := make([]legacyAssetSnapshot, 0, len(contract.Assets))
	for index, name := range contract.Assets {
		assets = append(assets, legacyAssetSnapshot{
			ID: int64(index + 1), Name: name, Size: int64(100 + index),
			Digest: "sha256:" + strings.Repeat(fmt.Sprintf("%x", index+1), 64),
		})
	}
	return &legacyGitHub{
		tagSHA: legacyTestSHA,
		release: legacyReleaseAPI{
			ID: 91, TagName: legacyTestVersion, TargetCommitish: "main", Assets: assets,
		},
		runs:                []workflowRun{},
		recordDispatchedRun: true,
	}
}

func (g *legacyGitHub) Get(_ context.Context, endpoint string, _ map[string]string, target any) error {
	switch endpoint {
	case "repos/" + testRepository + "/git/ref/tags/" + legacyTestVersion:
		*(target.(*tagRefResponse)) = tagRefResponse{Object: gitObject{Type: "commit", SHA: g.tagSHA}}
	case "repos/" + testRepository + "/git/ref/heads/main":
		*(target.(*tagRefResponse)) = tagRefResponse{Object: gitObject{Type: "commit", SHA: legacyControlSHA}}
	case "repos/" + testRepository + "/actions/workflows/legacy-rebuild.yml":
		*(target.(*legacyWorkflowAPI)) = legacyWorkflowAPI{ID: 81, Name: "legacy-rebuild", Path: ".github/workflows/legacy-rebuild.yml", State: "active"}
	case "repos/" + testRepository + "/releases/tags/" + legacyTestVersion:
		*(target.(*legacyReleaseAPI)) = g.release
	case "repos/" + testRepository + "/actions/workflows/legacy-rebuild.yml/runs":
		response := target.(*workflowRunsResponse)
		response.WorkflowRuns = append([]workflowRun{}, g.runs...)
		response.TotalCount = len(response.WorkflowRuns)
		if g.runsTotalOverride != nil {
			response.TotalCount = *g.runsTotalOverride
		}
	default:
		return fmt.Errorf("unexpected GET endpoint %s", endpoint)
	}
	return nil
}

func (g *legacyGitHub) Mutate(_ context.Context, method, endpoint string, body, _ any) error {
	g.mutations = append(g.mutations, recordedMutation{method: method, endpoint: endpoint, body: body})
	if g.recordDispatchedRun && method == "POST" && endpoint == "repos/"+testRepository+"/actions/workflows/legacy-rebuild.yml/dispatches" {
		inputs := body.(map[string]any)["inputs"].(map[string]string)
		g.runs = append([]workflowRun{{
			ID: 900 + int64(len(g.mutations)), RunAttempt: 1, Event: "workflow_dispatch", Status: "queued",
			HeadBranch: "main", HeadSHA: inputs["control_sha"], Path: ".github/workflows/legacy-rebuild.yml",
			HTMLURL:      "https://github.com/example/env-vault/actions/runs/901",
			DisplayTitle: "legacy-rebuild version=" + inputs["version"] + " source=" + inputs["source_sha"] + " plan=" + inputs["plan_digest"],
		}}, g.runs...)
	}
	return nil
}

func TestLegacyRebuildPlanIsDiagnosticOnlyAndBindsExactRemoteState(t *testing.T) {
	github := newLegacyGitHub()
	contract := mustTestReleaseContract()
	plan, err := planLegacyRebuild(context.Background(), testRepository, legacyTestVersion, legacyTestSHA, contract, github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	if plan.Schema != legacyRebuildPlanSchema || !plan.OK || !plan.Applyable || plan.Action.Code != "dispatch_legacy_rebuild" || plan.Action.Method != "POST" {
		t.Fatalf("plan=%+v", plan)
	}
	if plan.PlanDigest == "" || plan.PlanDigest != digestLegacyRebuildPlan(plan) || plan.Preconditions.StateDigest != digestLegacyPreconditions(plan.Preconditions) {
		t.Fatalf("plan digest=%q preconditions=%+v", plan.PlanDigest, plan.Preconditions)
	}
	if plan.Preconditions.TagSHA != legacyTestSHA || plan.Preconditions.ControlSHA != legacyControlSHA || len(plan.Preconditions.Release.Assets) != 10 {
		t.Fatalf("preconditions=%+v", plan.Preconditions)
	}
	if plan.BuildScope.PublicationEligible || plan.BuildScope.Kind != "diagnostic_build_only" || plan.BuildScope.ReasonCode != "LEGACY_PROMOTION_PROOF_UNAVAILABLE" || len(plan.BuildScope.Platforms) != 5 || !containsString(plan.BuildScope.Limitations, "historical_e2e_contract_absent") {
		t.Fatalf("scope=%+v", plan.BuildScope)
	}
	if strings.Contains(plan.Action.Endpoint, "/releases") {
		t.Fatalf("action=%+v", plan.Action)
	}
	if _, supported := contract.LegacyVersion("v0.0.8"); supported {
		t.Fatal("v0.0.8 entered the declarative legacy path")
	}
	if _, supported := contract.LegacyVersion("v0.0.9"); supported {
		t.Fatal("v0.0.8 or a steady-state version entered the legacy path")
	}
}

func TestLegacyRebuildApplyDefaultsToDryRunThenDispatchesOnlyBuildWorkflow(t *testing.T) {
	github := newLegacyGitHub()
	contract := mustTestReleaseContract()
	plan, err := planLegacyRebuild(context.Background(), testRepository, legacyTestVersion, legacyTestSHA, contract, github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	dry, code := applyLegacyRebuild(context.Background(), plan, plan.PlanDigest, false, contract, github, github, fixedClock())
	if code != exitSuccess || !dry.OK || dry.Status != "dry_run" || !dry.DryRun || len(github.mutations) != 0 {
		t.Fatalf("dry=%+v error=%+v code=%d mutations=%v", dry, dry.Error, code, github.mutations)
	}
	applied, code := applyLegacyRebuild(context.Background(), plan, plan.PlanDigest, true, contract, github, github, fixedClock())
	if code != exitSuccess || !applied.OK || applied.Status != "applied" || applied.WorkflowRun == nil || applied.WorkflowRun.ID != 901 || len(github.mutations) != 1 {
		t.Fatalf("applied=%+v code=%d mutations=%v", applied, code, github.mutations)
	}
	mutation := github.mutations[0]
	if mutation.method != "POST" || mutation.endpoint != "repos/"+testRepository+"/actions/workflows/legacy-rebuild.yml/dispatches" || strings.Contains(mutation.endpoint, "release") && !strings.Contains(mutation.endpoint, "legacy-rebuild") {
		t.Fatalf("mutation=%+v", mutation)
	}
	body := mutation.body.(map[string]any)
	if body["ref"] != "main" {
		t.Fatalf("body=%+v", body)
	}
	inputs := body["inputs"].(map[string]string)
	wantInputs := map[string]string{
		"version": legacyTestVersion, "source_sha": legacyTestSHA, "control_sha": legacyControlSHA,
		"plan_digest": plan.PlanDigest, "release_state_digest": plan.Preconditions.StateDigest,
	}
	if !reflect.DeepEqual(inputs, wantInputs) {
		t.Fatalf("inputs=%v want=%v", inputs, wantInputs)
	}
}

func TestLegacyRebuildApplyFailsClosedOnAssetOrTagDrift(t *testing.T) {
	github := newLegacyGitHub()
	contract := mustTestReleaseContract()
	plan, err := planLegacyRebuild(context.Background(), testRepository, legacyTestVersion, legacyTestSHA, contract, github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	github.release.Assets[0].Digest = "sha256:" + strings.Repeat("a", 64)
	doc, code := applyLegacyRebuild(context.Background(), plan, plan.PlanDigest, true, contract, github, github, fixedClock())
	if code != exitPreconditionFailed || doc.Error == nil || doc.Error.Code != "REMOTE_PRECONDITION_FAILED" || len(github.mutations) != 0 {
		t.Fatalf("drift doc=%+v code=%d", doc, code)
	}
	github = newLegacyGitHub()
	github.tagSHA = otherSHA
	doc, code = applyLegacyRebuild(context.Background(), plan, plan.PlanDigest, true, contract, github, github, fixedClock())
	if code != exitPreconditionFailed || doc.Error == nil || doc.Error.Code != "REMOTE_PRECONDITION_FAILED" || len(github.mutations) != 0 {
		t.Fatalf("tag drift doc=%+v code=%d", doc, code)
	}
}

func TestLegacyRebuildApplyIsIdempotentForTheExactPlan(t *testing.T) {
	github := newLegacyGitHub()
	contract := mustTestReleaseContract()
	plan, err := planLegacyRebuild(context.Background(), testRepository, legacyTestVersion, legacyTestSHA, contract, github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	run := completedRun(812, "workflow_dispatch", "main", legacyControlSHA, "success")
	run.Path = ".github/workflows/legacy-rebuild.yml"
	run.DisplayTitle = legacyRunTitle(plan)
	github.runs = []workflowRun{run}
	doc, code := applyLegacyRebuild(context.Background(), plan, plan.PlanDigest, true, contract, github, github, fixedClock())
	if code != exitSuccess || !doc.OK || doc.Status != "already_applied" || doc.WorkflowRun == nil || doc.WorkflowRun.ID != run.ID || len(github.mutations) != 0 {
		t.Fatalf("doc=%+v code=%d", doc, code)
	}
}

func TestLegacyRebuildApplyFailsClosedWhenDispatchCannotBeObserved(t *testing.T) {
	github := newLegacyGitHub()
	github.recordDispatchedRun = false
	contract := mustTestReleaseContract()
	plan, err := planLegacyRebuild(context.Background(), testRepository, legacyTestVersion, legacyTestSHA, contract, github, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	clk := fixedClock().(*fakeClock)
	doc, code := applyLegacyRebuild(context.Background(), plan, plan.PlanDigest, true, contract, github, github, clk)
	if code != exitObservationError || doc.OK || doc.Error == nil || doc.Error.Code != "REMOTE_STATE_UNKNOWN" || doc.Error.Operation != "legacy_dispatch_not_observed" || !doc.Error.Retryable {
		t.Fatalf("doc=%+v code=%d", doc, code)
	}
	if len(github.mutations) != 1 || len(clk.sleeps) != legacyDispatchPolls-1 {
		t.Fatalf("mutations=%v sleeps=%v", github.mutations, clk.sleeps)
	}
}

func TestLegacyRebuildApplyRejectsHiddenOrSpoofedExactPlanRuns(t *testing.T) {
	contract := mustTestReleaseContract()
	for _, test := range []struct {
		name     string
		wantCode string
		alter    func(*legacyGitHub, legacyRebuildPlan)
	}{
		{
			name:     "inventory requires pagination",
			wantCode: "MALFORMED_RESPONSE",
			alter: func(github *legacyGitHub, _ legacyRebuildPlan) {
				total := 101
				github.runsTotalOverride = &total
			},
		},
		{
			name:     "matching title has wrong workflow path",
			wantCode: "REMOTE_PRECONDITION_FAILED",
			alter: func(github *legacyGitHub, plan legacyRebuildPlan) {
				run := completedRun(812, "workflow_dispatch", "main", legacyControlSHA, "success")
				run.Path = ".github/workflows/build-binaries.yml"
				run.DisplayTitle = legacyRunTitle(plan)
				github.runs = []workflowRun{run}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			github := newLegacyGitHub()
			plan, err := planLegacyRebuild(context.Background(), testRepository, legacyTestVersion, legacyTestSHA, contract, github, fixedClock())
			if err != nil {
				t.Fatal(err)
			}
			test.alter(github, plan)
			doc, code := applyLegacyRebuild(context.Background(), plan, plan.PlanDigest, true, contract, github, github, fixedClock())
			if code == exitSuccess || doc.OK || doc.Error == nil || doc.Error.Code != test.wantCode || len(github.mutations) != 0 {
				t.Fatalf("doc=%+v code=%d mutations=%v", doc, code, github.mutations)
			}
		})
	}
}

func TestLegacyRebuildPlanCLIEmitsVersionedJSON(t *testing.T) {
	github := newLegacyGitHub()
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"release", "legacy-rebuild", "plan", "--repo", testRepository,
		"--version", legacyTestVersion, "--source-sha", legacyTestSHA,
		"--contract", "../../release/contract.v1.json", "--json",
	}, &stdout, &stderr, dependencies{github: github, clock: fixedClock()})
	if code != exitSuccess || stderr.Len() != 0 || strings.Count(stdout.String(), "\n") != 1 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var plan legacyRebuildPlan
	if err := json.Unmarshal(stdout.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Schema != legacyRebuildPlanSchema || plan.Action.Code != "dispatch_legacy_rebuild" {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestLegacyRebuildCLIRejectsV008WithStableMachineCode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"release", "legacy-rebuild", "plan", "--repo", testRepository,
		"--version", "v0.0.8", "--source-sha", testSHA,
		"--contract", "../../release/contract.v1.json", "--json",
	}, &stdout, &stderr, dependencies{clock: fixedClock()})
	if code != exitMutationBlocked || stderr.Len() != 0 || strings.Count(stdout.String(), "\n") != 1 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var doc struct {
		Schema string     `json:"schema"`
		Error  *errorInfo `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Schema != legacyRebuildPlanSchema || doc.Error == nil || doc.Error.Code != "LEGACY_REBUILD_UNSUPPORTED" || doc.Error.Operation != "legacy_version" {
		t.Fatalf("doc=%+v", doc)
	}
}
