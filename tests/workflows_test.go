package tests

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	checkoutAction       = "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0"
	setupGoAction        = "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16"
	uploadArtifactAction = "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a"
	downloadAction       = "actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c"
	createAppTokenAction = "actions/create-github-app-token@bcd2ba49218906704ab6c1aa796996da409d3eb1"
	releasePleaseAction  = "googleapis/release-please-action@45996ed1f6d02564a971a2fa1b5860e934307cf7"
)

type workflow struct {
	Name        string                 `yaml:"name"`
	RunName     string                 `yaml:"run-name"`
	On          map[string]yaml.Node   `yaml:"on"`
	Permissions map[string]string      `yaml:"permissions"`
	Concurrency workflowConcurrency    `yaml:"concurrency"`
	Jobs        map[string]workflowJob `yaml:"jobs"`
}

type workflowConcurrency struct {
	Group            string `yaml:"group"`
	CancelInProgress bool   `yaml:"cancel-in-progress"`
	Queue            string `yaml:"queue"`
}

type workflowJob struct {
	Name           string            `yaml:"name"`
	If             string            `yaml:"if"`
	Needs          stringList        `yaml:"needs"`
	RunsOn         string            `yaml:"runs-on"`
	Uses           string            `yaml:"uses"`
	With           map[string]string `yaml:"with"`
	Env            map[string]string `yaml:"env"`
	Permissions    map[string]string `yaml:"permissions"`
	Outputs        map[string]string `yaml:"outputs"`
	Environment    string            `yaml:"environment"`
	TimeoutMinutes int               `yaml:"timeout-minutes"`
	Strategy       workflowStrategy  `yaml:"strategy"`
	Steps          []workflowStep    `yaml:"steps"`
}

type workflowStrategy struct {
	FailFast *bool     `yaml:"fail-fast"`
	Matrix   yaml.Node `yaml:"matrix"`
}

type workflowMatrix struct {
	Include []map[string]string `yaml:"include"`
}

type workflowStep struct {
	Name            string            `yaml:"name"`
	ID              string            `yaml:"id"`
	Uses            string            `yaml:"uses"`
	If              string            `yaml:"if"`
	Shell           string            `yaml:"shell"`
	Run             string            `yaml:"run"`
	ContinueOnError bool              `yaml:"continue-on-error"`
	Env             map[string]string `yaml:"env"`
	With            map[string]string `yaml:"with"`
}

type workflowRunTrigger struct {
	Workflows []string `yaml:"workflows"`
	Types     []string `yaml:"types"`
	Branches  []string `yaml:"branches"`
}

type pushTrigger struct {
	Branches []string `yaml:"branches"`
	Tags     []string `yaml:"tags"`
}

type pullRequestTrigger struct {
	Types []string `yaml:"types"`
}

type dispatchTrigger struct {
	Inputs map[string]workflowInput `yaml:"inputs"`
}

type callTrigger struct {
	Inputs map[string]workflowInput `yaml:"inputs"`
}

type workflowInput struct {
	Description string   `yaml:"description"`
	Required    bool     `yaml:"required"`
	Default     string   `yaml:"default"`
	Type        string   `yaml:"type"`
	Options     []string `yaml:"options"`
}

type stringList []string

func (list *stringList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case 0:
		return nil
	case yaml.ScalarNode:
		*list = []string{node.Value}
		return nil
	case yaml.SequenceNode:
		var values []string
		if err := node.Decode(&values); err != nil {
			return err
		}
		*list = values
		return nil
	default:
		return fmt.Errorf("needs must be a scalar or sequence, got YAML kind %d", node.Kind)
	}
}

type releaseContract struct {
	SchemaID      string             `json:"schema_id"`
	SchemaVersion int                `json:"schema_version"`
	Platforms     []contractPlatform `json:"platforms"`
	Assets        []string           `json:"assets"`
	Workflows     []contractWorkflow `json:"workflows"`
	VersionPolicy struct {
		ReleasePleaseRecovery struct {
			State                     string `json:"state"`
			AbandonedVersion          string `json:"abandoned_version"`
			AbandonedSourceSHA        string `json:"abandoned_source_sha"`
			GeneratedReleasePRNumber  int    `json:"generated_release_pr_number"`
			GeneratedReleasePRHeadSHA string `json:"generated_release_pr_head_sha"`
			ResumeVersion             string `json:"resume_version"`
			PendingLabel              string `json:"pending_label"`
			AbandonedLabel            string `json:"abandoned_label"`
			TaggedLabel               string `json:"tagged_label"`
			TagMustNotExist           bool   `json:"tag_must_not_exist"`
			GitHubReleaseMustNotExist bool   `json:"github_release_must_not_exist"`
			ReasonCode                string `json:"reason_code"`
			CompletedReleaseSourceSHA string `json:"completed_release_source_sha"`
		} `json:"release_please_recovery"`
		BlockedVersions []struct {
			Version                   string `json:"version"`
			TagSHA                    string `json:"tag_sha"`
			TagMustRemain             bool   `json:"tag_must_remain"`
			GitHubReleaseMustNotExist bool   `json:"github_release_must_not_exist"`
		} `json:"blocked_versions"`
		LegacyRebuild struct {
			GoVersion           string `json:"go_version"`
			PublicationEligible bool   `json:"publication_eligible"`
			Versions            []struct {
				Version string `json:"version"`
				TagSHA  string `json:"tag_sha"`
			} `json:"versions"`
		} `json:"legacy_rebuild"`
	} `json:"version_policy"`
}

type contractPlatform struct {
	ID       string `json:"id"`
	Runner   string `json:"runner"`
	GOOS     string `json:"goos"`
	GOARCH   string `json:"goarch"`
	CGO      string `json:"cgo"`
	Archive  string `json:"archive"`
	Checksum string `json:"checksum"`
	Binary   string `json:"binary"`
}

type contractWorkflow struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	File string `json:"file"`
}

func TestWorkflowFilesParseAndPinReviewedActions(t *testing.T) {
	expected := map[string]string{
		"actions/checkout":                 checkoutAction,
		"actions/setup-go":                 setupGoAction,
		"actions/upload-artifact":          uploadArtifactAction,
		"actions/download-artifact":        downloadAction,
		"actions/attest":                   "actions/attest@a1948c3f048ba23858d222213b7c278aabede763",
		"actions/create-github-app-token":  createAppTokenAction,
		"anchore/sbom-action":              "anchore/sbom-action@e22c389904149dbc22b58101806040fa8d37a610",
		"actions/dependency-review-action": "actions/dependency-review-action@a1d282b36b6f3519aa1f3fc636f609c47dddb294",
		"googleapis/release-please-action": releasePleaseAction,
	}

	paths := workflowPaths(t)
	if len(paths) < 9 {
		t.Fatalf("workflow count=%d, want at least 9", len(paths))
	}
	for _, path := range paths {
		wf := readWorkflow(t, path)
		if wf.Name == "" || len(wf.On) == 0 || len(wf.Jobs) == 0 {
			t.Fatalf("%s has incomplete top-level structure", path)
		}
		for jobName, job := range wf.Jobs {
			if job.Uses != "" && !strings.HasPrefix(job.Uses, "./") {
				assertPinnedAction(t, path, jobName, job.Uses, expected)
			}
			for _, step := range job.Steps {
				if step.Uses != "" && !strings.HasPrefix(step.Uses, "./") {
					assertPinnedAction(t, path, jobName, step.Uses, expected)
				}
			}
		}
	}
}

func TestReleaseContractOwnsWorkflowAndNativeInventory(t *testing.T) {
	contract := readReleaseContract(t)
	if contract.SchemaID != "env-vault.release-contract.v1" || contract.SchemaVersion != 1 {
		t.Fatalf("contract schema=%s/%d", contract.SchemaID, contract.SchemaVersion)
	}

	wantPlatforms := map[string]contractPlatform{
		"linux-amd64":   {ID: "linux-amd64", Runner: "ubuntu-latest", GOOS: "linux", GOARCH: "amd64", CGO: "0", Archive: "env-vault-linux-amd64.tar.gz", Checksum: "env-vault-linux-amd64.tar.gz.sha256", Binary: "env-vault"},
		"linux-arm64":   {ID: "linux-arm64", Runner: "ubuntu-24.04-arm", GOOS: "linux", GOARCH: "arm64", CGO: "0", Archive: "env-vault-linux-arm64.tar.gz", Checksum: "env-vault-linux-arm64.tar.gz.sha256", Binary: "env-vault"},
		"darwin-amd64":  {ID: "darwin-amd64", Runner: "macos-15-intel", GOOS: "darwin", GOARCH: "amd64", CGO: "1", Archive: "env-vault-darwin-amd64.tar.gz", Checksum: "env-vault-darwin-amd64.tar.gz.sha256", Binary: "env-vault"},
		"darwin-arm64":  {ID: "darwin-arm64", Runner: "macos-15", GOOS: "darwin", GOARCH: "arm64", CGO: "1", Archive: "env-vault-darwin-arm64.tar.gz", Checksum: "env-vault-darwin-arm64.tar.gz.sha256", Binary: "env-vault"},
		"windows-amd64": {ID: "windows-amd64", Runner: "windows-latest", GOOS: "windows", GOARCH: "amd64", CGO: "0", Archive: "env-vault-windows-amd64.zip", Checksum: "env-vault-windows-amd64.zip.sha256", Binary: "env-vault.exe"},
	}
	gotPlatforms := make(map[string]contractPlatform, len(contract.Platforms))
	for _, platform := range contract.Platforms {
		if _, exists := gotPlatforms[platform.ID]; exists {
			t.Fatalf("duplicate contract platform %q", platform.ID)
		}
		gotPlatforms[platform.ID] = platform
	}
	if !reflect.DeepEqual(gotPlatforms, wantPlatforms) {
		t.Fatalf("contract platforms=%#v, want %#v", gotPlatforms, wantPlatforms)
	}

	wantAssets := make([]string, 0, 10)
	for _, platform := range contract.Platforms {
		wantAssets = append(wantAssets, platform.Archive, platform.Checksum)
	}
	sort.Strings(wantAssets)
	gotAssets := append([]string(nil), contract.Assets...)
	sort.Strings(gotAssets)
	if !slices.Equal(gotAssets, wantAssets) {
		t.Fatalf("contract assets=%v, want exact platform archive/checksum inventory %v", gotAssets, wantAssets)
	}

	actualFiles := make([]string, 0)
	for _, path := range workflowPaths(t) {
		actualFiles = append(actualFiles, filepath.Base(path))
	}
	contractFiles := make([]string, 0, len(contract.Workflows))
	seenIDs := map[string]bool{}
	for _, identity := range contract.Workflows {
		if identity.ID == "" || identity.Name == "" || identity.File == "" || seenIDs[identity.ID] {
			t.Fatalf("invalid or duplicate workflow identity: %+v", identity)
		}
		seenIDs[identity.ID] = true
		contractFiles = append(contractFiles, identity.File)
		wf := readWorkflow(t, filepath.Join("..", ".github", "workflows", identity.File))
		if wf.Name != identity.Name {
			t.Fatalf("contract workflow %s name=%q, YAML name=%q", identity.File, identity.Name, wf.Name)
		}
	}
	sort.Strings(actualFiles)
	sort.Strings(contractFiles)
	if !slices.Equal(actualFiles, contractFiles) {
		t.Fatalf("workflow files differ from release contract: YAML=%v contract=%v", actualFiles, contractFiles)
	}
}

func TestCIUsesReusableQualityAndCancellationSafeGate(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/ci.yml")
	assertPermissions(t, "ci", wf.Permissions, map[string]string{"contents": "read"})
	assertTrigger(t, wf, "push")
	assertTrigger(t, wf, "pull_request")
	assertTrigger(t, wf, "workflow_dispatch")
	push := decodeTrigger[pushTrigger](t, wf, "push")
	if !slices.Equal(push.Branches, []string{"main"}) {
		t.Fatalf("ci push branches=%v", push.Branches)
	}
	if !wf.Concurrency.CancelInProgress || !containsAll(wf.Concurrency.Group, "workflow_dispatch", "github.run_id", "github.run_attempt > 1", "rerun-", "github.ref") {
		t.Fatalf("ci concurrency does not isolate manual dispatch while cancelling superseded runs: %+v", wf.Concurrency)
	}
	assertJobIDs(t, wf, "quality", "quality-gate")

	quality := wf.Jobs["quality"]
	if quality.Uses != "./.github/workflows/reusable-quality.yml" {
		t.Fatalf("ci quality uses=%q", quality.Uses)
	}
	assertPermissions(t, "ci quality", quality.Permissions, map[string]string{"actions": "read", "contents": "read"})
	for key, want := range map[string]string{
		"source_sha":            "${{ github.sha }}",
		"version":               "auto",
		"event_name":            "${{ github.event_name }}",
		"pull_request_head_ref": "${{ github.event.pull_request.head.ref || '' }}",
		"pull_request_head_sha": "${{ github.event.pull_request.head.sha || '' }}",
	} {
		if quality.With[key] != want {
			t.Fatalf("ci quality input %s=%q, want %q", key, quality.With[key], want)
		}
	}

	gate := wf.Jobs["quality-gate"]
	if compactExpression(gate.If) != "always()" || !slices.Equal([]string(gate.Needs), []string{"quality"}) {
		t.Fatalf("quality-gate if=%q needs=%v", gate.If, gate.Needs)
	}
	if gateStep := namedStep(t, gate, "Require every reusable quality job"); gateStep.Env["QUALITY_RESULT"] != "${{ needs.quality.result }}" {
		t.Fatalf("quality gate does not consume reusable workflow result: %+v", gateStep.Env)
	}
}

func TestDependencyAndPullRequestWorkflowConcurrency(t *testing.T) {
	dependency := readWorkflow(t, "../.github/workflows/dependency-review.yml")
	assertTrigger(t, dependency, "pull_request")
	if dependency.Concurrency.Group != "dependency-review-${{ github.event.pull_request.number }}" || !dependency.Concurrency.CancelInProgress {
		t.Fatalf("dependency review concurrency=%+v", dependency.Concurrency)
	}
	assertPermissions(t, "dependency review", dependency.Permissions, map[string]string{"contents": "read"})
	assertJobIDs(t, dependency, "dependency-review")

	prTitle := readWorkflow(t, "../.github/workflows/pr-title.yml")
	assertTrigger(t, prTitle, "pull_request")
	if len(prTitle.Permissions) != 0 || prTitle.Concurrency.Group != "pr-title-${{ github.event.pull_request.number }}" || !prTitle.Concurrency.CancelInProgress {
		t.Fatalf("pr-title permissions/concurrency=%v %+v", prTitle.Permissions, prTitle.Concurrency)
	}
	step := namedStep(t, prTitle.Jobs["pr-title"], "Require a Conventional Commit pull request title")
	if step.Env["PR_TITLE"] != "${{ github.event.pull_request.title }}" {
		t.Fatalf("pr-title source=%q", step.Env["PR_TITLE"])
	}
}

func TestReusableQualityHasElevenJobsAndOneNativeMatrixSource(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/reusable-quality.yml")
	assertPermissions(t, "reusable quality", wf.Permissions, map[string]string{"contents": "read"})
	assertTrigger(t, wf, "workflow_call")
	call := decodeTrigger[callTrigger](t, wf, "workflow_call")
	for _, input := range []string{"source_sha", "version", "event_name", "pull_request_head_ref", "pull_request_head_sha"} {
		if _, ok := call.Inputs[input]; !ok {
			t.Fatalf("reusable workflow missing input %q", input)
		}
	}
	if !call.Inputs["source_sha"].Required || !call.Inputs["version"].Required {
		t.Fatalf("source/version inputs must be required: %+v", call.Inputs)
	}
	assertJobIDs(t, wf, "resolve", "source-quality", "license", "native", "e2e-gate")

	resolve := wf.Jobs["resolve"]
	contractStep := namedStep(t, resolve, "Validate release contract and resolve native matrix")
	if !containsAll(contractStep.Run, "releasecheck validate-contract", "releasecheck contract matrix --json", "length == 5") {
		t.Fatalf("resolve step does not derive the five-target matrix from the validated contract")
	}
	if countJobRunsContaining(wf, "releasecheck contract matrix --json") != 1 {
		t.Fatalf("reusable quality must derive the candidate matrix exactly once")
	}
	if resolve.Outputs["matrix"] != "${{ steps.contract.outputs.matrix }}" {
		t.Fatalf("resolved matrix output=%q", resolve.Outputs["matrix"])
	}

	native := wf.Jobs["native"]
	if !slices.Equal([]string(native.Needs), []string{"resolve"}) || native.RunsOn != "${{ matrix.runner }}" {
		t.Fatalf("native topology needs=%v runner=%q", native.Needs, native.RunsOn)
	}
	if native.Strategy.Matrix.Kind != yaml.ScalarNode || native.Strategy.Matrix.Value != "${{ fromJSON(needs.resolve.outputs.matrix) }}" {
		t.Fatalf("native matrix must consume the single resolved contract matrix, got kind=%d value=%q", native.Strategy.Matrix.Kind, native.Strategy.Matrix.Value)
	}
	if native.Strategy.FailFast == nil || *native.Strategy.FailFast {
		t.Fatalf("native fail-fast=%v", native.Strategy.FailFast)
	}
	if step := namedStep(t, native, "Burn in Windows config concurrency"); step.If != "matrix.goos == 'windows'" || !containsAll(step.Run, "TestConcurrentSavePublishesOnlyCompleteConfigs", "-count=10") {
		t.Fatalf("Windows concurrency burn-in was weakened: if=%q run=%q", step.If, step.Run)
	}
	windowsPackage := namedStep(t, native, "Package native release artifact on Windows")
	if windowsPackage.Shell != "pwsh" || !containsAll(windowsPackage.Run, "[System.IO.File]::WriteAllText", "`n", "[System.Text.Encoding]::ASCII") || strings.Contains(windowsPackage.Run, "`r") || strings.Contains(windowsPackage.Run, "Set-Content") {
		t.Fatalf("Windows checksum writer does not produce deterministic LF-terminated ASCII: shell=%q run=%q", windowsPackage.Shell, windowsPackage.Run)
	}
	upload := namedStep(t, native, "Upload current-attempt native release artifact")
	if upload.Uses != uploadArtifactAction || !containsAll(upload.With["name"], "matrix.id", "github.run_attempt") {
		t.Fatalf("native artifact is not attempt-qualified: uses=%q with=%v", upload.Uses, upload.With)
	}
	proof := namedStep(t, native, "Verify three literal versions and seal native proof")
	if !containsAll(proof.If, "release_candidate", "inputs.event_name == 'push'") || !containsAll(proof.Run, "release-version-probe", "releasecheck promotion record-platform", "--archive", "--checksum", "--binary", "--version-results") {
		t.Fatalf("native promotion proof is not exact-version/push-only: if=%q", proof.If)
	}

	licenseMatrix := decodeMatrix(t, wf.Jobs["license"].Strategy.Matrix)
	contract := readReleaseContract(t)
	expandedJobs := 1 + 1 + len(licenseMatrix.Include) + len(contract.Platforms) + 1
	if expandedJobs != 11 {
		t.Fatalf("reusable quality expands to %d jobs, want 11", expandedJobs)
	}
	if expandedJobs+1 != 12 { // one top-level quality-gate job in ci.yml
		t.Fatalf("main/PR CI total=%d jobs, want 12", expandedJobs+1)
	}

	for _, command := range []string{"go test ./...", "go vet ./...", "go test -race ./..."} {
		if !jobRunsExact(wf.Jobs["source-quality"], command) {
			t.Fatalf("source-quality missing %q", command)
		}
	}
	gate := wf.Jobs["e2e-gate"]
	assertCancellationSafe(t, "reusable e2e-gate", gate)
	assertNeeds(t, "reusable e2e-gate", gate, "resolve", "source-quality", "license", "native")
	validate := namedStep(t, gate, "Validate and seal the complete E2E matrix once")
	if !containsAll(validate.Run, "e2e-runner validate-matrix", "--contract release/contract.v1.json", "--expected-run-attempt") {
		t.Fatalf("E2E matrix validation does not bind the contract/current attempt")
	}
	baseline := namedStep(t, gate, "Verify sealed matrix against durable baseline")
	if !containsAll(baseline.Run, "e2e-baseline verify", "docs/e2e-baseline.json", "matrix-validation.json") {
		t.Fatalf("durable baseline verification missing")
	}
	manifest := namedStep(t, gate, "Assemble exact promotion manifest")
	if !containsAll(manifest.Run, "releasecheck promotion assemble", "--platform-proof", "--matrix-proof", "--run-attempt") {
		t.Fatalf("promotion manifest does not bind platform/matrix/current attempt")
	}
	manifestUpload := namedStep(t, gate, "Upload exact promotion manifest")
	if !containsAll(manifestUpload.With["name"], "inputs.source_sha", "github.run_attempt") {
		t.Fatalf("promotion manifest artifact is not source/attempt qualified: %v", manifestUpload.With)
	}
}

func TestReleasePleaseVerifiesExactAttemptBeforeTagAndOnlyFullReruns(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/release-please.yml")
	rawWorkflow := readFile(t, "../.github/workflows/release-please.yml")
	assertJobIDs(t, wf, "inspect", "rerun-incomplete-attempt", "plan")
	assertGlobalReleaseConcurrency(t, "release-please", wf)
	trigger := decodeTrigger[workflowRunTrigger](t, wf, "workflow_run")
	if !slices.Equal(trigger.Workflows, []string{"ci"}) || !slices.Equal(trigger.Types, []string{"completed"}) || !slices.Equal(trigger.Branches, []string{"main"}) {
		t.Fatalf("release-please trigger=%+v", trigger)
	}
	inspect := wf.Jobs["inspect"]
	assertPermissions(t, "release inspect", inspect.Permissions, map[string]string{
		"actions": "read", "contents": "read",
	})
	if inspect.Environment != "" || strings.Contains(inspect.If, "conclusion == 'success'") || !containsAll(inspect.If, "event == 'push'", "head_branch == 'main'", "head_repository.full_name == github.repository") {
		t.Fatalf("release inspection cannot classify failed repository-owned main attempts: environment=%q if=%q", inspect.Environment, inspect.If)
	}
	inspectAttempt := namedStep(t, inspect, "Classify the exact completed release-candidate attempt offline")
	if !containsAll(inspectAttempt.Run, "gh-api-read.sh", "classify-attempt", "rerun_all_jobs", "inspect_failure", "ATTEMPT_MATRIX_INCOMPLETE", "CI_ATTEMPT_FAILED", ".head_repository == $repository") {
		t.Fatalf("read-only attempt classifier is incomplete")
	}
	inspectUpload := namedStep(t, inspect, "Upload machine-readable attempt classification")
	if inspectUpload.Uses != uploadArtifactAction || !containsAll(inspectUpload.If, "always()", "!cancelled()") || !containsAll(inspectUpload.With["name"], "workflow_run.id", "workflow_run.run_attempt", "github.run_id", "github.run_attempt") || !containsAll(inspectUpload.With["path"], "ci-run.json", "ci-artifacts.json", "attempt-classification.json") {
		t.Fatalf("attempt classification artifact is not immutable across planning reruns: %+v", inspectUpload.With)
	}

	rerun := wf.Jobs["rerun-incomplete-attempt"]
	assertNeeds(t, "incomplete-attempt rerun", rerun, "inspect")
	assertPermissions(t, "incomplete-attempt rerun", rerun.Permissions, map[string]string{
		"actions": "write", "contents": "read",
	})
	if rerun.Environment != "" || !containsAll(rerun.If, "always()", "attempt_action == 'rerun_all_jobs'", "attempt == '1'") {
		t.Fatalf("rerun mutation is not isolated and bounded: environment=%q if=%q", rerun.Environment, rerun.If)
	}
	rerunStep := namedStep(t, rerun, "Reclassify current remote state and rerun the whole attempt")
	if !containsAll(rerunStep.Run, "gh-api-read.sh", "classify-attempt", "rerun-classified-attempt.sh", "ATTEMPT_MATRIX_INCOMPLETE") || strings.Contains(rerunStep.Run, "--failed") {
		t.Fatalf("bounded rerun does not reclassify exact state and issue only a full rerun")
	}

	plan := wf.Jobs["plan"]
	assertPermissions(t, "release plan", plan.Permissions, map[string]string{
		"actions": "read", "contents": "read", "issues": "read", "pull-requests": "read",
	})
	assertNeeds(t, "release plan", plan, "inspect")
	if plan.Environment != "release-planning" || !containsAll(plan.If, "needs.inspect.result == 'success'", "conclusion == 'success'", "event == 'push'", "head_branch == 'main'", "head_repository.full_name == github.repository", "attempt_action == 'none'") {
		t.Fatalf("release plan does not require exact green repository-owned main CI: environment=%q if=%q", plan.Environment, plan.If)
	}
	current := namedStep(t, plan, "Require the planning commit to remain current")
	if !containsAll(current.Run, "gh-api-read.sh", "main-ref.json", "EXPECTED_SHA") {
		t.Fatalf("planning current-main observation is not bounded and file-backed")
	}
	contractStep := namedStep(t, plan, "Build the offline checker and validate the release contract")
	if !containsAll(contractStep.Run,
		"recovery validate-config", "--config release-please-config.json",
		"--manifest .release-please-manifest.json", "recovery_state", "recovery_resume_version") {
		t.Fatalf("planning does not bind the one-time Release Please recovery offline")
	}

	attempt := namedStep(t, plan, "Snapshot and classify the exact triggering CI attempt")
	if !containsAll(attempt.Run, "gh-api-read.sh", "classify-attempt", "action_code == \"none\"", "rerun_failed_jobs_allowed == false", ".repository == $repository", ".head_repository == $repository") || strings.Contains(attempt.Run, "rerun-classified-attempt.sh") {
		t.Fatalf("pre-tag plan does not require a previously accepted exact attempt")
	}
	if !containsAll(attempt.Run, "include \"artifact-pages\"", "env_vault_artifacts") || strings.Contains(attempt.Run, ".[][]") {
		t.Fatalf("pre-tag artifact selection does not parse slurped page envelopes fail closed")
	}
	if strings.Contains(attempt.Run, "--failed") {
		t.Fatalf("pre-tag classifier must never recommend or issue a failed-jobs-only rerun")
	}
	downloadManifest := namedStep(t, plan, "Download the exact promotion manifest artifact")
	downloadAssets := namedStep(t, plan, "Download all five exact-attempt native release artifacts")
	for name, step := range map[string]workflowStep{"manifest": downloadManifest, "assets": downloadAssets} {
		if step.Uses != downloadAction || step.With["run-id"] != "${{ github.event.workflow_run.id }}" {
			t.Fatalf("pre-tag %s download is not tied to the triggering run: %+v", name, step.With)
		}
	}
	if !containsAll(downloadAssets.With["pattern"], "steps.attempt.outputs.platform_pattern") || downloadAssets.With["merge-multiple"] != "true" {
		t.Fatalf("pre-tag native artifact download=%v", downloadAssets.With)
	}
	verify := namedStep(t, plan, "Verify exact manifest and ten packaged assets offline")
	if !containsAll(verify.Run, "promotion verify", "--source-sha", "--release-version", "--run-id", "--run-attempt", "--artifacts-root") {
		t.Fatalf("pre-tag promotion verification does not bind exact tuple")
	}
	finalAttempt := namedStep(t, plan, "Recheck the exact CI attempt immediately before tag creation")
	if !containsAll(finalAttempt.Run, "gh-api-read.sh", "classify-attempt", "ci-run-final.json", "ci-artifacts-final.json", "cmp") {
		t.Fatalf("final pre-tag state is not re-snapshotted and compared")
	}
	if !containsAll(finalAttempt.Run, "include \"artifact-pages\"", "env_vault_artifacts") || strings.Contains(finalAttempt.Run, ".[][]") {
		t.Fatalf("final pre-tag artifact selection does not parse slurped page envelopes fail closed")
	}
	if strings.Count(rawWorkflow, "scripts/release/gh-api-read.sh") != 11 {
		t.Fatalf("release planning read-helper calls=%d, want 11", strings.Count(rawWorkflow, "scripts/release/gh-api-read.sh"))
	}
	for _, line := range strings.Split(rawWorkflow, "\n") {
		if strings.Contains(line, "gh api") && !strings.Contains(line, "gh api --method POST") {
			t.Fatalf("release planning retains an unbounded direct API read: %q", strings.TrimSpace(line))
		}
	}
	if strings.Count(rawWorkflow, "gh api --method POST") != 1 {
		t.Fatalf("release planning must contain exactly one direct tag POST")
	}
	settings := namedStep(t, plan, "Verify repository release settings and bypass policy")
	if !containsAll(settings.Env["RELEASE_SETTINGS_PROOF_OUTPUT"], "steps.classify.outputs.publish", "repository-release-settings-proof.json") ||
		settings.Env["RELEASE_SETTINGS_PLANNING_RUN_ID"] != "${{ github.run_id }}" ||
		settings.Env["RELEASE_SETTINGS_PLANNING_RUN_ATTEMPT"] != "${{ github.run_attempt }}" {
		t.Fatalf("pre-tag settings proof is not bound to the exact planning attempt: %+v", settings.Env)
	}
	settingsUpload := namedStep(t, plan, "Upload exact pre-tag repository settings proof")
	if settingsUpload.Uses != uploadArtifactAction || !containsAll(settingsUpload.With["name"], "source_sha", "github.run_attempt") ||
		settingsUpload.With["path"] != "${{ runner.temp }}/repository-release-settings-proof.json" {
		t.Fatalf("pre-tag settings proof artifact is not source/attempt qualified: %+v", settingsUpload.With)
	}
	recovery := namedStep(t, plan, "Reconcile the exact abandoned untagged release pull request")
	if recovery.Env["GH_TOKEN"] != "${{ steps.release-token.outputs.token }}" ||
		recovery.Env["RELEASECHECK"] != "${{ runner.temp }}/releasecheck" ||
		!containsAll(recovery.If, "publish != 'true'", "current == 'true'", "recovery_state == 'active'") ||
		!containsAll(recovery.Run, "reconcile-abandoned-release-pr.sh", "release-please-recovery.json") {
		t.Fatalf("abandoned release recovery is not exact, App-scoped, and offline-checker backed: %+v", recovery)
	}
	recoveryUpload := namedStep(t, plan, "Upload exact abandoned-release recovery evidence")
	if recoveryUpload.Uses != uploadArtifactAction ||
		!containsAll(recoveryUpload.If, "abandon-release.outcome == 'success'") ||
		!containsAll(recoveryUpload.With["name"], "github.run_id", "github.run_attempt") ||
		recoveryUpload.With["path"] != "${{ runner.temp }}/release-please-recovery.json" ||
		recoveryUpload.With["if-no-files-found"] != "error" {
		t.Fatalf("abandoned release recovery evidence is not attempt-qualified: %+v", recoveryUpload.With)
	}
	proposal := namedStep(t, plan, "Verify the proposal is based on a green main commit")
	if proposal.Env["EXPECTED_RELEASE_VERSION"] != "${{ steps.release-contract.outputs.recovery_state == 'active' && steps.release-contract.outputs.recovery_resume_version || '' }}" ||
		proposal.Run != "scripts/release/verify-release-proposal.sh" {
		t.Fatalf("active recovery proposal is not bound to the exact resume version: %+v", proposal)
	}
	tagMutation := namedStep(t, plan, "Create or verify the exact release tag")
	if strings.Count(tagMutation.Run, "gh api --method POST") != 1 || strings.Contains(tagMutation.Run, "gh-api-read.sh") ||
		!containsAll(tagMutation.Run, "verify-abandoned-release-policy.sh", `"$VERSION" "$SOURCE_SHA"`, "abandoned-release-policy.json") ||
		tagMutation.Env["RELEASECHECK"] != "${{ runner.temp }}/releasecheck" {
		t.Fatalf("immutable tag mutation must remain a one-shot direct API call")
	}
	pretagRecoveryUpload := namedStep(t, plan, "Upload the exact pre-tag abandoned-release proof")
	if pretagRecoveryUpload.Uses != uploadArtifactAction ||
		!containsAll(pretagRecoveryUpload.With["name"], "source_sha", "github.run_id", "github.run_attempt") ||
		pretagRecoveryUpload.With["path"] != "${{ runner.temp }}/pretag/abandoned-release-policy.json" {
		t.Fatalf("pre-tag abandoned-release proof is not source/planning-attempt qualified: %+v", pretagRecoveryUpload.With)
	}

	assertStepOrder(t, plan,
		"Snapshot and classify the exact triggering CI attempt",
		"Download the exact promotion manifest artifact",
		"Download all five exact-attempt native release artifacts",
		"Verify exact manifest and ten packaged assets offline",
		"Verify repository release settings and bypass policy",
		"Upload exact pre-tag repository settings proof",
		"Verify generated release pull request authorization",
		"Recheck the exact CI attempt immediately before tag creation",
		"Create or verify the exact release tag",
		"Upload the exact pre-tag abandoned-release proof",
	)
	assertStepOrder(t, plan,
		"Ensure release lifecycle labels",
		"Reconcile the exact abandoned untagged release pull request",
		"Upload exact abandoned-release recovery evidence",
		"Create or update the reviewed release pull request",
		"Verify the proposal is based on a green main commit",
	)

	helper := readFile(t, "../scripts/release/rerun-classified-attempt.sh")
	failedOnlyCommand := regexp.MustCompile(`(?m)^\s*(?:exec\s+)?gh\s+run\s+rerun\b[^\n]*--failed`)
	if !containsAll(helper, "rerun_all_jobs", "gh run rerun", "--repo") || failedOnlyCommand.MatchString(helper) {
		t.Fatalf("classified rerun helper must use gh full rerun and prohibit --failed")
	}
}

func TestPublisherPromotesExactArtifactsWithoutProductRebuild(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/build-binaries.yml")
	assertGlobalReleaseConcurrency(t, "publisher", wf)
	assertJobIDs(t, wf, "metadata", "preflight", "promotion", "release", "supply_chain", "homebrew", "health")
	if len(wf.Jobs) != 7 {
		t.Fatalf("publisher job count=%d, want 7", len(wf.Jobs))
	}
	assertPermissions(t, "publisher", wf.Permissions, map[string]string{
		"actions": "read", "contents": "read", "issues": "read", "pull-requests": "read",
	})
	push := decodeTrigger[pushTrigger](t, wf, "push")
	if !slices.Equal(push.Tags, []string{"v*"}) {
		t.Fatalf("publisher tag trigger=%v", push.Tags)
	}
	dispatch := decodeTrigger[dispatchTrigger](t, wf, "workflow_dispatch")
	if dispatch.Inputs["version"].Type != "string" || !dispatch.Inputs["version"].Required || !slices.Equal(dispatch.Inputs["repair"].Options, []string{"health", "homebrew", "release-assets"}) {
		t.Fatalf("publisher manual inputs=%+v", dispatch.Inputs)
	}

	for _, jobID := range []string{"preflight", "promotion", "release", "supply_chain", "homebrew", "health"} {
		assertCancellationSafe(t, "publisher "+jobID, wf.Jobs[jobID])
	}
	assertNeeds(t, "preflight", wf.Jobs["preflight"], "metadata")
	assertNeeds(t, "promotion", wf.Jobs["promotion"], "metadata")
	assertNeeds(t, "release", wf.Jobs["release"], "metadata", "preflight", "promotion")
	assertNeeds(t, "supply_chain", wf.Jobs["supply_chain"], "metadata", "release")
	assertNeeds(t, "homebrew", wf.Jobs["homebrew"], "metadata", "preflight", "promotion", "release", "supply_chain")
	assertNeeds(t, "health", wf.Jobs["health"], "metadata", "promotion", "release", "supply_chain", "homebrew")

	metadata := wf.Jobs["metadata"]
	resolve := namedStep(t, metadata, "Resolve exact tag, source, CI attempt, and repair stage")
	if !containsAll(resolve.Run, "version_policy.blocked_versions", "outside the steady-state publisher", "actions/workflows/ci.yml/runs", "run_attempt", "release-assets|homebrew|health") {
		t.Fatalf("publisher metadata does not bind immutable tag/current CI attempt/repair policy")
	}
	for _, output := range []string{"version", "source_sha", "ci_run_id", "ci_run_attempt", "planning_run_id", "planning_run_attempt", "settings_proof_artifact_id", "settings_proof_artifact_name", "run_promotion", "run_release", "run_homebrew"} {
		if metadata.Outputs[output] == "" {
			t.Fatalf("publisher metadata missing output %q", output)
		}
	}
	if !containsAll(resolve.Run, "actions/workflows/release-please.yml/runs", "exactly one successful release-please.yml run", "planning-artifacts.json", "include \"artifact-pages\"", "env_vault_exact_artifact", "exact pre-tag repository settings proof artifact is missing or ambiguous") {
		t.Fatalf("publisher metadata does not uniquely bind the exact-source planning proof artifact")
	}

	promotion := wf.Jobs["promotion"]
	assertPermissions(t, "promotion", promotion.Permissions, map[string]string{"actions": "read", "contents": "read"})
	classifier := namedStep(t, promotion, "Save and classify the exact current CI attempt")
	if !containsAll(classifier.Run, "releasecheck classify-attempt", "CI_RUN_ATTEMPT", "ATTEMPT_MATRIX_COMPLETE", "rerun_failed_jobs_allowed == false") {
		t.Fatalf("publisher promotion classifier is not exact-attempt fail-closed")
	}
	promotionManifest := namedStep(t, promotion, "Download the exact promotion manifest from CI")
	nativeAssets := namedStep(t, promotion, "Download five exact native release artifacts from CI")
	for name, step := range map[string]workflowStep{"manifest": promotionManifest, "native assets": nativeAssets} {
		if step.Uses != downloadAction || step.With["run-id"] != "${{ needs.metadata.outputs.ci_run_id }}" {
			t.Fatalf("publisher %s download is not tied to exact CI run: %v", name, step.With)
		}
	}
	if !containsAll(nativeAssets.With["pattern"], "needs.metadata.outputs.ci_run_attempt") {
		t.Fatalf("native artifacts are not tied to CI attempt: %v", nativeAssets.With)
	}
	verify := namedStep(t, promotion, "Verify promotion and stage the exact publisher bundle")
	if !containsAll(verify.Run,
		"releasecheck promotion verify", "--run-attempt", "verified publisher bundle", "exactly ten regular release assets",
		"mapfile -t manifests", "find promotion-manifest -type f -name promotion-manifest.json",
		"${#manifests[@]} -eq 1", `--manifest "${manifests[0]}"`, `install -m 0600 "${manifests[0]}"`,
		"verified-bundle/assets/$archive", "verified-bundle/assets/$checksum",
		"find verified-bundle/assets -mindepth 1 -maxdepth 1", "find verified-bundle/assets -maxdepth 1 -type f",
	) {
		t.Fatalf("publisher promotion does not isolate one manifest from exactly ten regular assets")
	}
	if strings.Contains(verify.Run, "--manifest promotion-manifest/promotion-manifest.json") {
		t.Fatal("publisher reintroduced the incorrect flattened promotion-manifest path")
	}
	bundle := namedStep(t, promotion, "Upload publisher-local verified bundle")
	if !containsAll(bundle.With["name"], "source_sha", "github.run_attempt") || bundle.With["path"] != "verified-bundle" {
		t.Fatalf("publisher-local bundle is not current-run qualified: %v", bundle.With)
	}

	release := wf.Jobs["release"]
	downloadBundle := namedStep(t, release, "Download publisher-local verified promotion bundle")
	if downloadBundle.Uses != downloadAction || downloadBundle.With["name"] != bundle.With["name"] || downloadBundle.With["path"] != "dist" {
		t.Fatalf("release does not consume the exact publisher-local bundle: download=%v upload=%v", downloadBundle.With, bundle.With)
	}
	assertStepOrder(t, release,
		"Download publisher-local verified promotion bundle",
		"Reverify promotion immediately before mutation",
		"Verify release tag commit",
		"Create or verify stable GitHub Release",
		"No-clobber reconcile all ten release assets",
	)
	reverify := namedStep(t, release, "Reverify promotion immediately before mutation")
	if !containsAll(reverify.Run, "releasecheck promotion verify", "--run-attempt", "--manifest dist/promotion-manifest.json", "--artifacts-root dist/assets") {
		t.Fatalf("release mutation lacks immediate promotion re-verification")
	}
	reconcile := namedStep(t, release, "No-clobber reconcile all ten release assets")
	if reconcile.Run != `scripts/release/reconcile-release-assets.sh "$VERSION" dist/assets` {
		t.Fatalf("release no-clobber reconciliation does not use the exact verified asset inventory: %q", reconcile.Run)
	}

	raw := readFile(t, "../.github/workflows/build-binaries.yml")
	for _, forbidden := range []string{"go test ./...", "go test -race ./...", "./cmd/env-vault", "-ldflags="} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("publisher must promote tested artifacts, found product rebuild/source-quality marker %q", forbidden)
		}
	}
}

func TestPublisherKeepsReleaseSupplyChainHomebrewAndHealthBoundaries(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/build-binaries.yml")
	assertPermissions(t, "release", wf.Jobs["release"].Permissions, map[string]string{"contents": "write"})
	assertPermissions(t, "supply chain", wf.Jobs["supply_chain"].Permissions, map[string]string{
		"contents": "read", "id-token": "write", "attestations": "write", "artifact-metadata": "write",
	})
	assertPermissions(t, "homebrew", wf.Jobs["homebrew"].Permissions, map[string]string{"contents": "read", "attestations": "read"})
	assertPermissions(t, "health", wf.Jobs["health"].Permissions, map[string]string{"actions": "read", "contents": "read", "attestations": "read", "pull-requests": "read"})

	supply := wf.Jobs["supply_chain"]
	if namedStep(t, supply, "Safely extract packages for SBOM").Run != "go run ./cmd/release-extract --input-dir release-dist --output-dir sbom-root" {
		t.Fatalf("supply chain does not use the shared safe extractor")
	}
	if namedStep(t, supply, "Attest build provenance").Uses == "" || namedStep(t, supply, "Attest SPDX SBOM").Uses == "" {
		t.Fatalf("supply chain provenance/SBOM attestation stages missing")
	}
	if finalVerify := namedStep(t, supply, "Require complete exact-source attestations after creation or reuse"); !containsAll(finalVerify.Run, "verify-artifact-attestations.sh", `"$SOURCE_SHA"`, "all") {
		t.Fatalf("supply chain does not fail closed on the exact post-create attestation tuple")
	}

	homebrew := wf.Jobs["homebrew"]
	if homebrew.Environment != "release" {
		t.Fatalf("Homebrew job environment=%q", homebrew.Environment)
	}
	for _, output := range []string{"publication_state", "pr_number", "pr_url", "pr_head_sha", "merge_sha", "tap_sha", "pr_ci_url", "tap_ci_url"} {
		if homebrew.Outputs[output] == "" {
			t.Fatalf("Homebrew job missing exact-state output %q", output)
		}
	}
	appToken := namedStep(t, homebrew, "Mint scoped Homebrew App token")
	assertPermissions(t, "Homebrew App token", appToken.With, map[string]string{
		"client-id":                "${{ vars.TAP_APP_CLIENT_ID }}",
		"private-key":              "${{ secrets.TAP_APP_PRIVATE_KEY }}",
		"owner":                    "${{ needs.metadata.outputs.tap_repository_owner }}",
		"repositories":             "${{ needs.metadata.outputs.tap_repository_name }}",
		"permission-actions":       "read",
		"permission-contents":      "write",
		"permission-pull-requests": "write",
	})
	prCI := namedStep(t, homebrew, "Require exact Homebrew pull-request head CI")
	postMergeCI := namedStep(t, homebrew, "Require exact Homebrew post-merge CI")
	if !containsAll(prCI.Run, "wait-tap-ci.sh", `"$HEAD_SHA" pull_request`) || !containsAll(postMergeCI.Run, "wait-tap-ci.sh", `"$MERGE_SHA" push`) {
		t.Fatalf("Homebrew must preserve both exact PR-head and post-merge CI gates")
	}
	assertStepOrder(t, homebrew,
		"Require exact-source attestations before tap mutation",
		"Create or reuse deterministic Homebrew pull request",
		"Require exact Homebrew pull-request head CI",
		"Merge exact Homebrew pull-request head",
		"Require exact Homebrew post-merge CI",
	)

	health := wf.Jobs["health"]
	settingsDownload := namedStep(t, health, "Download the exact pre-tag repository settings proof")
	if settingsDownload.Uses != downloadAction || settingsDownload.With["run-id"] != "${{ needs.metadata.outputs.planning_run_id }}" ||
		settingsDownload.With["name"] != "${{ needs.metadata.outputs.settings_proof_artifact_name }}" {
		t.Fatalf("health settings-proof download is not exact planning-run bound: %+v", settingsDownload.With)
	}
	settingsVerify := namedStep(t, health, "Verify the exact pre-tag repository settings proof offline")
	if !containsAll(settingsVerify.Run, "settings verify", "--planning-run-id", "--planning-run-attempt", "--source-sha", "--release-version", "cmp") {
		t.Fatalf("health does not replay the settings proof against the exact release/planning tuple")
	}
	healthVerify := namedStep(t, health, "Verify release, supply chain, Homebrew, blocked tags, and abandoned release")
	if healthVerify.Env["REPAIR_MODE"] != "${{ needs.metadata.outputs.repair }}" || !containsAll(healthVerify.Run, "publisher_repair_mode", `--arg repair_mode "$REPAIR_MODE"`) {
		t.Fatalf("health observation does not bind the exact publisher repair mode: env=%v", healthVerify.Env)
	}
	if !containsAll(healthVerify.Run,
		"wait-tap-ci.sh", "pull_request", "push", "version_policy.blocked_versions[]",
		"--verify-published-pr", "gh attestation verify", "runInvocationURI",
		"attestation-verifications.json", "document_sha256", "document_json",
		"merge_is_ancestor_of_tap", `merge-base --is-ancestor "$merge_sha" "$tap_sha"`,
		`wait-tap-ci.sh "$TAP_REPOSITORY" "$TAP_CI_WORKFLOW" "$merge_sha" push`,
		"blocked_versions:$blocked_versions", "verify-abandoned-release-policy.sh",
		"abandoned_release_policy_exact:true", "abandoned_release:$abandoned_release",
		"releasecheck evidence seal-health") {
		t.Fatalf("health does not independently re-observe release, attestations, both tap gates, and all blocked versions")
	}
	if strings.Contains(healthVerify.Run, "verify-repository-release-settings.sh") || !containsAll(healthVerify.Run, "repository_release_settings", "repository-release-settings-proof.json") {
		t.Fatalf("read-scoped health must embed the sealed proof without a live administration query")
	}
	healthUpload := namedStep(t, health, "Upload typed release observation and sealed health proof")
	if healthUpload.Uses != uploadArtifactAction || !containsAll(healthUpload.With["name"], "version", "github.run_attempt") ||
		!containsAll(healthUpload.With["path"], "release-observation.json", "health-proof.json", "attestation-verifications.json", "abandoned-release-policy.json", "repository-release-settings-proof.json") {
		t.Fatalf("machine-readable health observation is not version/current-attempt qualified: %v", healthUpload.With)
	}
}

func TestReleaseEvidenceBindsExactSuccessfulAttemptsAndPublishesNoClobber(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/release-evidence.yml")
	assertPermissions(t, "release evidence", wf.Permissions, map[string]string{
		"actions": "read", "contents": "read", "pull-requests": "read",
	})
	assertGlobalReleaseConcurrency(t, "release evidence", wf)
	trigger := decodeTrigger[workflowRunTrigger](t, wf, "workflow_run")
	if !slices.Equal(trigger.Workflows, []string{"build-binaries"}) || !slices.Equal(trigger.Types, []string{"completed"}) {
		t.Fatalf("release evidence trigger=%+v", trigger)
	}
	assertJobIDs(t, wf, "assemble", "publish")
	assembleJob := wf.Jobs["assemble"]
	if assembleJob.TimeoutMinutes != 20 || !containsAll(compactExpression(assembleJob.If),
		"conclusion=='success'", "head_repository.full_name==github.repository", "path=='.github/workflows/build-binaries.yml'", "event=='push'", "event=='workflow_dispatch'") {
		t.Fatalf("release evidence trust boundary is incomplete: timeout=%d if=%q", assembleJob.TimeoutMinutes, assembleJob.If)
	}

	observation := namedStep(t, assembleJob, "Download the exact publisher-attempt observation")
	if observation.Uses != downloadAction || !containsAll(observation.With["pattern"], "release-observation", "workflow_run.run_attempt") || observation.With["run-id"] != "${{ github.event.workflow_run.id }}" {
		t.Fatalf("release observation download is not exact-attempt bound: %v", observation.With)
	}
	identity := namedStep(t, assembleJob, "Resolve exact CI, release PR, and PR-head CI identities")
	if !containsAll(identity.Run,
		"classify-attempt", "ATTEMPT_MATRIX_COMPLETE", "verify-release-authorization.sh", "gh pr checks", "quality-gate", "run_attempt", ".head_branch == $version",
		"RELEASE_AUTHORIZATION_OUTPUT", "pull_requests", "confirmation", "generated_release_pr_head_sha",
		"release-authorization.json", "attestation-verifications.json", "repository_release_settings", "settings verify",
		"actions/runs/${planning_run_id}", "snapshots/planning-run.json") {
		t.Fatalf("evidence identity resolution is missing an exact tuple gate")
	}
	promotion := namedStep(t, assembleJob, "Download the exact promotion manifest")
	if promotion.Uses != downloadAction || !containsAll(promotion.With["name"], "source_sha", "ci_attempt") || promotion.With["run-id"] != "${{ steps.identity.outputs.ci_run_id }}" {
		t.Fatalf("evidence promotion download is not exact CI attempt bound: %v", promotion.With)
	}
	metrics := namedStep(t, assembleJob, "Compute exact current metrics and before/after comparison")
	if strings.Count(metrics.Run, "gh run view") != 3 || !containsAll(metrics.Run, "--attempt", "metrics compare", "metrics-comparison.json") {
		t.Fatalf("evidence metrics do not bind all three exact attempts")
	}
	assemble := namedStep(t, assembleJob, "Assemble and replay durable release evidence offline")
	if !containsAll(assemble.Run, "evidence assemble", "evidence verify", "promotion-manifest.json", "--authorization", "--attestations") {
		t.Fatalf("evidence is not assembled and replayed by the offline checker")
	}
	candidate := namedStep(t, assembleJob, "Upload immutable evidence candidate for the write-scoped job")
	if candidate.Uses != uploadArtifactAction || !containsAll(candidate.With["name"], "version", "source_sha", "github.run_id", "github.run_attempt") {
		t.Fatalf("read-scoped evidence candidate is not exact-run qualified: %v", candidate.With)
	}

	publishJob := wf.Jobs["publish"]
	assertCancellationSafe(t, "release evidence publish", publishJob)
	assertNeeds(t, "release evidence publish", publishJob, "assemble")
	assertPermissions(t, "release evidence publish", publishJob.Permissions, map[string]string{"actions": "read", "contents": "write"})
	replay := namedStep(t, publishJob, "Replay the complete candidate before granting it mutation authority")
	if !containsAll(replay.Run, "evidence verify", "metrics compare", "cmp candidate/final/index.md", "SOURCE_SHA") {
		t.Fatalf("write-scoped evidence job does not replay its complete candidate")
	}
	publish := namedStep(t, publishJob, "Publish durable no-clobber evidence branch state")
	if !containsAll(publish.Run, "publish-release-evidence.sh", "release-evidence.json", "metrics-comparison.json", "GITHUB_OUTPUT") {
		t.Fatalf("durable evidence publisher invocation is incomplete")
	}
	coordinates := namedStep(t, publishJob, "Report exact immutable evidence coordinates")
	if !containsAll(coordinates.Run, "blob/${EVIDENCE_COMMIT_SHA}/${EVIDENCE_PATH_PREFIX}", "publisher-runs/run-${PUBLISHER_RUN_ID}/attempt-${PUBLISHER_RUN_ATTEMPT}", "OUTPUT_PUBLISHER_RUN_ID", "OUTPUT_PUBLISHER_RUN_ATTEMPT", "EVIDENCE_REPAIR_MODE", "release-evidence.json", "metrics-comparison.json") {
		t.Fatalf("evidence links are not pinned to the exact immutable evidence commit and publisher-attempt lineage")
	}
	upload := namedStep(t, publishJob, "Upload replayable release evidence artifact")
	if upload.Uses != uploadArtifactAction || !containsAll(upload.With["name"], "version", "commit_sha", "workflow_run.id", "workflow_run.run_attempt", "github.run_id", "github.run_attempt") {
		t.Fatalf("release evidence artifact is not tuple-qualified: %v", upload.With)
	}
	if raw := readFile(t, "../.github/workflows/release-evidence.yml"); containsAll(raw, "go test ./...", "-ldflags=") || strings.Contains(raw, "browser") || strings.Contains(raw, "gh release edit") {
		t.Fatalf("release evidence workflow must not rebuild product quality or use browser automation")
	}
}

func TestLegacyRebuildIsDiagnosticOnlyAndCannotSelectV008(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/legacy-rebuild.yml")
	assertPermissions(t, "legacy rebuild", wf.Permissions, map[string]string{"contents": "read"})
	assertJobIDs(t, wf, "resolve", "diagnostic")
	if wf.Concurrency.CancelInProgress || !containsAll(wf.Concurrency.Group, "legacy-rebuild", "inputs.version", "github.run_id") {
		t.Fatalf("legacy diagnostic concurrency=%+v", wf.Concurrency)
	}
	dispatch := decodeTrigger[dispatchTrigger](t, wf, "workflow_dispatch")
	wantVersions := []string{"v0.0.1", "v0.0.2", "v0.0.3", "v0.0.4", "v0.0.5", "v0.0.6", "v0.0.7"}
	if !slices.Equal(dispatch.Inputs["version"].Options, wantVersions) {
		t.Fatalf("legacy choices=%v, want %v", dispatch.Inputs["version"].Options, wantVersions)
	}
	resolve := namedStep(t, wf.Jobs["resolve"], "Resolve immutable diagnostic contract")
	if !containsAll(resolve.Run, "releasecheck legacy", "publication_eligible == false", "dispatch_legacy_rebuild", "releasecheck contract matrix --json", "length == 5") {
		t.Fatalf("legacy resolver does not fail closed against the diagnostic-only contract")
	}
	diagnostic := wf.Jobs["diagnostic"]
	if diagnostic.Strategy.Matrix.Kind != yaml.ScalarNode || diagnostic.Strategy.Matrix.Value != "${{ fromJSON(needs.resolve.outputs.matrix) }}" {
		t.Fatalf("legacy diagnostics do not consume contract matrix: %q", diagnostic.Strategy.Matrix.Value)
	}
	upload := namedStep(t, diagnostic, "Upload diagnostic-only result")
	if !containsAll(upload.With["name"], "legacy-diagnostic", "github.run_id") || strings.Contains(upload.With["name"], "env-vault-release-") {
		t.Fatalf("legacy artifact could be mistaken for a publication artifact: %v", upload.With)
	}

	contract := readReleaseContract(t)
	if contract.VersionPolicy.LegacyRebuild.PublicationEligible || len(contract.VersionPolicy.LegacyRebuild.Versions) != 7 {
		t.Fatalf("contract legacy policy=%+v", contract.VersionPolicy.LegacyRebuild)
	}
	for i, legacy := range contract.VersionPolicy.LegacyRebuild.Versions {
		if legacy.Version != wantVersions[i] || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(legacy.TagSHA) {
			t.Fatalf("legacy contract entry=%+v", legacy)
		}
	}
	if len(contract.VersionPolicy.BlockedVersions) != 4 {
		t.Fatalf("blocked version policy=%+v", contract.VersionPolicy.BlockedVersions)
	}
	for index, expected := range []string{"v0.0.8", "v0.0.9", "v0.0.10", "v0.0.11"} {
		blocked := contract.VersionPolicy.BlockedVersions[index]
		if blocked.Version != expected || !blocked.TagMustRemain || !blocked.GitHubReleaseMustNotExist {
			t.Fatalf("%s immutable failed-tag policy=%+v", expected, blocked)
		}
	}

	raw := readFile(t, "../.github/workflows/legacy-rebuild.yml")
	for _, forbidden := range []string{"gh release", "reconcile-release-assets", "actions/attest", "publish-homebrew"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("legacy diagnostic workflow contains publication marker %q", forbidden)
		}
	}
}

func TestReleaseAppAuditWorkflowsKeepNarrowTokenScopes(t *testing.T) {
	tapAudit := readWorkflow(t, "../.github/workflows/audit-release-app.yml")
	assertPermissions(t, "tap App audit", tapAudit.Permissions, map[string]string{"contents": "read"})
	assertJobIDs(t, tapAudit, "scope")
	tapScope := tapAudit.Jobs["scope"]
	if tapScope.Environment != "release" || tapScope.TimeoutMinutes != 5 {
		t.Fatalf("tap App audit boundary environment=%q timeout=%d", tapScope.Environment, tapScope.TimeoutMinutes)
	}
	assertPermissions(t, "tap audit token", namedStep(t, tapScope, "Mint metadata-only installation token").With, map[string]string{
		"client-id":           "${{ vars.TAP_APP_CLIENT_ID }}",
		"private-key":         "${{ secrets.TAP_APP_PRIVATE_KEY }}",
		"owner":               "${{ github.repository_owner }}",
		"permission-metadata": "read",
	})

	planningAudit := readWorkflow(t, "../.github/workflows/audit-release-planning-app.yml")
	assertPermissions(t, "planning App audit", planningAudit.Permissions, map[string]string{"contents": "read"})
	assertJobIDs(t, planningAudit, "scope")
	planningScope := planningAudit.Jobs["scope"]
	if planningScope.Environment != "release-planning" || planningScope.TimeoutMinutes != 5 {
		t.Fatalf("planning App audit boundary environment=%q timeout=%d", planningScope.Environment, planningScope.TimeoutMinutes)
	}
	assertPermissions(t, "planning audit token", namedStep(t, planningScope, "Mint read-only installation audit token").With, map[string]string{
		"client-id":                 "${{ vars.RELEASE_APP_CLIENT_ID }}",
		"private-key":               "${{ secrets.RELEASE_APP_PRIVATE_KEY }}",
		"owner":                     "${{ github.repository_owner }}",
		"permission-administration": "read",
		"permission-metadata":       "read",
	})
	buildSettingsChecker := namedStep(t, planningScope, "Build the offline release settings checker")
	if !containsAll(buildSettingsChecker.Run, "go build", "./cmd/releasecheck", "$RUNNER_TEMP/releasecheck") {
		t.Fatalf("planning App audit does not build the offline settings checker: %q", buildSettingsChecker.Run)
	}
	verifySettings := namedStep(t, planningScope, "Verify repository release settings and bypass policy")
	if verifySettings.Env["RELEASECHECK"] != "${{ runner.temp }}/releasecheck" || verifySettings.Run != "scripts/release/verify-repository-release-settings.sh" {
		t.Fatalf("planning App audit settings verifier is not offline-checker backed: env=%v run=%q", verifySettings.Env, verifySettings.Run)
	}

	planningWorkflow := readWorkflow(t, "../.github/workflows/release-please.yml")
	planningToken := namedStep(t, planningWorkflow.Jobs["plan"], "Mint repository-scoped release planning token")
	assertPermissions(t, "operational planning token", planningToken.With, map[string]string{
		"client-id":                 "${{ vars.RELEASE_APP_CLIENT_ID }}",
		"private-key":               "${{ secrets.RELEASE_APP_PRIVATE_KEY }}",
		"owner":                     "${{ github.repository_owner }}",
		"repositories":              "${{ steps.release-contract.outputs.release_repository_name }}",
		"permission-administration": "read",
		"permission-contents":       "write",
		"permission-issues":         "write",
		"permission-pull-requests":  "write",
	})
}

func TestReleasePleaseConfigDefersPublicationAndTracksVersionedDocs(t *testing.T) {
	data := []byte(readFile(t, "../release-please-config.json"))
	var config struct {
		LastReleaseSHA string `json:"last-release-sha"`
		Packages       map[string]struct {
			ReleaseType       string `json:"release-type"`
			PackageName       string `json:"package-name"`
			Component         string `json:"component"`
			ChangelogPath     string `json:"changelog-path"`
			SkipGitHubRelease bool   `json:"skip-github-release"`
			IncludeVInTag     bool   `json:"include-v-in-tag"`
			ExtraFiles        []struct {
				Type string `json:"type"`
				Path string `json:"path"`
			} `json:"extra-files"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse release-please-config.json: %v", err)
	}
	recovery := readReleaseContract(t).VersionPolicy.ReleasePleaseRecovery
	if recovery.State != "active" || recovery.AbandonedVersion != "v0.0.12" ||
		recovery.AbandonedSourceSHA != "a0eb82cb1fc4fa486ff2032d50ddedf6bccdbb8b" ||
		recovery.GeneratedReleasePRNumber != 31 ||
		recovery.GeneratedReleasePRHeadSHA != "c7169946d9c430209928266d95be7629c93d5878" ||
		recovery.ResumeVersion != "v0.0.13" || config.LastReleaseSHA != recovery.AbandonedSourceSHA ||
		recovery.PendingLabel != "autorelease: pending" ||
		recovery.AbandonedLabel != "autorelease: abandoned" ||
		recovery.TaggedLabel != "autorelease: tagged" ||
		!recovery.TagMustNotExist || !recovery.GitHubReleaseMustNotExist ||
		recovery.ReasonCode != "PRETAG_AUTHORIZATION_MISSING" ||
		recovery.CompletedReleaseSourceSHA != "" {
		t.Fatalf("active Release Please recovery/config boundary=%+v config_sha=%q", recovery, config.LastReleaseSHA)
	}
	pkg, ok := config.Packages["."]
	if !ok || pkg.ReleaseType != "go" || pkg.PackageName != "env-vault" || pkg.Component != "env-vault" || pkg.ChangelogPath != "CHANGELOG.md" || !pkg.SkipGitHubRelease || !pkg.IncludeVInTag {
		t.Fatalf("release package config=%+v", pkg)
	}
	if len(pkg.ExtraFiles) != 1 || pkg.ExtraFiles[0].Type != "generic" || pkg.ExtraFiles[0].Path != "README.md" {
		t.Fatalf("versioned extra files=%+v", pkg.ExtraFiles)
	}

	plan := readWorkflow(t, "../.github/workflows/release-please.yml").Jobs["plan"]
	step := namedStep(t, plan, "Create or update the reviewed release pull request")
	if step.Uses != releasePleaseAction || step.With["skip-github-release"] != "true" || step.With["target-branch"] != "main" {
		t.Fatalf("Release Please action is allowed to publish directly: uses=%q with=%v", step.Uses, step.With)
	}
}

func TestObsoleteNetworkOperatorAndHistoricalComparatorAreAbsent(t *testing.T) {
	for _, path := range workflowPaths(t) {
		raw := readFile(t, path)
		for _, forbidden := range []string{"releasectl", "e2e-compare", "29441160687", "rerun --failed", "rerun-failed-jobs"} {
			if strings.Contains(raw, forbidden) {
				t.Fatalf("%s contains obsolete or unsafe release marker %q", path, forbidden)
			}
		}
	}
	for _, path := range []string{"../cmd/releasectl", "../internal/releasectl", "../cmd/e2e-compare"} {
		files, err := filepath.Glob(filepath.Join(path, "*.go"))
		if err != nil {
			t.Fatalf("glob obsolete implementation path %s: %v", path, err)
		}
		if len(files) != 0 {
			t.Fatalf("obsolete implementation files still exist: %v", files)
		}
	}
}

func readWorkflow(t *testing.T, path string) workflow {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var wf workflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return wf
}

func workflowPaths(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob("../.github/workflows/*.yml")
	if err != nil {
		t.Fatalf("glob workflows: %v", err)
	}
	sort.Strings(paths)
	return paths
}

func readReleaseContract(t *testing.T) releaseContract {
	t.Helper()
	data, err := os.ReadFile("../release/contract.v1.json")
	if err != nil {
		t.Fatalf("read release contract: %v", err)
	}
	var contract releaseContract
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatalf("parse release contract: %v", err)
	}
	return contract
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func decodeTrigger[T any](t *testing.T, wf workflow, name string) T {
	t.Helper()
	node, ok := wf.On[name]
	if !ok {
		t.Fatalf("workflow %q missing trigger %q", wf.Name, name)
	}
	var trigger T
	if node.Kind != 0 && node.Kind != yaml.ScalarNode || (node.Kind == yaml.ScalarNode && node.Tag != "!!null") {
		if err := node.Decode(&trigger); err != nil {
			t.Fatalf("decode %s trigger for %s: %v", name, wf.Name, err)
		}
	}
	return trigger
}

func assertTrigger(t *testing.T, wf workflow, name string) {
	t.Helper()
	if _, ok := wf.On[name]; !ok {
		t.Fatalf("workflow %q missing trigger %q", wf.Name, name)
	}
}

func decodeMatrix(t *testing.T, node yaml.Node) workflowMatrix {
	t.Helper()
	var matrix workflowMatrix
	if err := node.Decode(&matrix); err != nil {
		t.Fatalf("decode workflow matrix: %v", err)
	}
	return matrix
}

func assertPinnedAction(t *testing.T, path, jobName, uses string, expected map[string]string) {
	t.Helper()
	parts := strings.SplitN(uses, "@", 2)
	if len(parts) != 2 || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(parts[1]) {
		t.Fatalf("%s job %s action is not pinned to a full commit: %q", path, jobName, uses)
	}
	want, ok := expected[parts[0]]
	if !ok {
		t.Fatalf("%s job %s uses an unreviewed external action: %q", path, jobName, uses)
	}
	if uses != want {
		t.Fatalf("%s job %s uses %q, want reviewed pin %q", path, jobName, uses, want)
	}
}

func assertGlobalReleaseConcurrency(t *testing.T, label string, wf workflow) {
	t.Helper()
	if wf.Concurrency.Group != "env-vault-release" || wf.Concurrency.CancelInProgress || wf.Concurrency.Queue != "max" {
		t.Fatalf("%s changed global release serialization: %+v", label, wf.Concurrency)
	}
}

func assertCancellationSafe(t *testing.T, label string, job workflowJob) {
	t.Helper()
	if !containsAll(compactExpression(job.If), "always()", "!cancelled()") {
		t.Fatalf("%s if=%q, want always() && !cancelled()", label, job.If)
	}
}

func assertJobIDs(t *testing.T, wf workflow, want ...string) {
	t.Helper()
	got := make([]string, 0, len(wf.Jobs))
	for id := range wf.Jobs {
		got = append(got, id)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Fatalf("workflow %s jobs=%v, want %v", wf.Name, got, want)
	}
}

func assertNeeds(t *testing.T, label string, job workflowJob, want ...string) {
	t.Helper()
	got := append([]string(nil), job.Needs...)
	sort.Strings(got)
	sort.Strings(want)
	if !slices.Equal(got, want) {
		t.Fatalf("%s needs=%v, want %v", label, got, want)
	}
}

func assertPermissions(t *testing.T, label string, got, want map[string]string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s permissions/inputs=%v, want exact %v", label, got, want)
	}
}

func namedStep(t *testing.T, job workflowJob, name string) workflowStep {
	t.Helper()
	for _, step := range job.Steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("step %q not found", name)
	return workflowStep{}
}

func assertStepOrder(t *testing.T, job workflowJob, names ...string) {
	t.Helper()
	previous := -1
	for _, name := range names {
		index := -1
		for i, step := range job.Steps {
			if step.Name == name {
				index = i
				break
			}
		}
		if index < 0 {
			t.Fatalf("step %q not found", name)
		}
		if index <= previous {
			t.Fatalf("step %q occurs out of order", name)
		}
		previous = index
	}
}

func compactExpression(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "${{")
	value = strings.TrimSuffix(value, "}}")
	return strings.Join(strings.Fields(value), "")
}

func containsAll(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(value, fragment) {
			return false
		}
	}
	return true
}

func countJobRunsContaining(wf workflow, fragment string) int {
	count := 0
	for _, job := range wf.Jobs {
		for _, step := range job.Steps {
			if strings.Contains(step.Run, fragment) {
				count++
			}
		}
	}
	return count
}

func jobRunsExact(job workflowJob, command string) bool {
	for _, step := range job.Steps {
		if strings.TrimSpace(step.Run) == command {
			return true
		}
	}
	return false
}
