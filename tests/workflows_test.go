package tests

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type workflow struct {
	On          workflowTriggers       `yaml:"on"`
	RunName     string                 `yaml:"run-name"`
	Concurrency workflowConcurrency    `yaml:"concurrency"`
	Permissions map[string]string      `yaml:"permissions"`
	Jobs        map[string]workflowJob `yaml:"jobs"`
}

type workflowConcurrency struct {
	Group            string `yaml:"group"`
	CancelInProgress bool   `yaml:"cancel-in-progress"`
	Queue            string `yaml:"queue"`
}

type workflowTriggers struct {
	WorkflowDispatch workflowDispatch    `yaml:"workflow_dispatch"`
	WorkflowCall     workflowCall        `yaml:"workflow_call"`
	WorkflowRun      workflowRun         `yaml:"workflow_run"`
	PullRequest      workflowPullRequest `yaml:"pull_request"`
	Push             workflowPush        `yaml:"push"`
}

type workflowPullRequest struct {
	Types []string `yaml:"types"`
}

type workflowRun struct {
	Workflows []string `yaml:"workflows"`
	Types     []string `yaml:"types"`
	Branches  []string `yaml:"branches"`
}

type workflowPush struct {
	Branches []string `yaml:"branches"`
}

type workflowCall struct {
	Inputs map[string]workflowInput `yaml:"inputs"`
}

type workflowDispatch struct {
	Inputs map[string]workflowInput `yaml:"inputs"`
}

type workflowInput struct {
	Description string   `yaml:"description"`
	Required    bool     `yaml:"required"`
	Default     string   `yaml:"default"`
	Type        string   `yaml:"type"`
	Options     []string `yaml:"options"`
}

type workflowJob struct {
	Name           string            `yaml:"name"`
	If             string            `yaml:"if"`
	Needs          []string          `yaml:"needs"`
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
	FailFast *bool          `yaml:"fail-fast"`
	Matrix   workflowMatrix `yaml:"matrix"`
}

type workflowMatrix struct {
	Include    []workflowTarget `yaml:"include"`
	Expression string           `yaml:"-"`
}

func (matrix *workflowMatrix) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		matrix.Expression = node.Value
		return nil
	}
	type plainMatrix workflowMatrix
	var decoded plainMatrix
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*matrix = workflowMatrix(decoded)
	return nil
}

type workflowTarget struct {
	OS      string `yaml:"os"`
	GOOS    string `yaml:"goos"`
	GOARCH  string `yaml:"goarch"`
	Runner  string `yaml:"runner"`
	CGO     string `yaml:"cgo"`
	Ext     string `yaml:"ext"`
	Archive string `yaml:"archive"`
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

const (
	createAppTokenAction = "actions/create-github-app-token@bcd2ba49218906704ab6c1aa796996da409d3eb1"
	releasePleaseAction  = "googleapis/release-please-action@45996ed1f6d02564a971a2fa1b5860e934307cf7"
)

func TestWorkflowsPinExternalActionsToReviewedCommits(t *testing.T) {
	expected := map[string]string{
		"actions/checkout":                 "9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0",
		"actions/setup-go":                 "924ae3a1cded613372ab5595356fb5720e22ba16",
		"actions/upload-artifact":          "043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
		"actions/download-artifact":        "3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c",
		"actions/attest":                   "a1948c3f048ba23858d222213b7c278aabede763",
		"actions/create-github-app-token":  "bcd2ba49218906704ab6c1aa796996da409d3eb1",
		"anchore/sbom-action":              "e22c389904149dbc22b58101806040fa8d37a610",
		"actions/dependency-review-action": "a1d282b36b6f3519aa1f3fc636f609c47dddb294",
		"googleapis/release-please-action": "45996ed1f6d02564a971a2fa1b5860e934307cf7",
	}
	for _, path := range []string{
		"../.github/workflows/audit-release-planning-app.yml",
		"../.github/workflows/audit-release-app.yml",
		"../.github/workflows/build-binaries.yml",
		"../.github/workflows/ci.yml",
		"../.github/workflows/dependency-review.yml",
		"../.github/workflows/pr-title.yml",
		"../.github/workflows/release-please.yml",
		"../.github/workflows/reusable-quality.yml",
	} {
		wf := readWorkflow(t, path)
		for jobName, job := range wf.Jobs {
			for _, step := range job.Steps {
				if step.Uses == "" {
					continue
				}
				parts := strings.SplitN(step.Uses, "@", 2)
				want, ok := expected[parts[0]]
				if !ok {
					continue
				}
				if len(parts) != 2 || parts[1] != want {
					t.Fatalf("%s job %s uses %q, want %s@%s", path, jobName, step.Uses, parts[0], want)
				}
			}
		}
	}
}

func TestReleaseAppScopeAuditIsMetadataOnly(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/audit-release-app.yml")
	if wf.Permissions["contents"] != "read" || len(wf.Permissions) != 1 {
		t.Fatalf("scope audit workflow permissions=%v", wf.Permissions)
	}
	if wf.Concurrency.Group != "env-vault-release-app-audit" || wf.Concurrency.CancelInProgress {
		t.Fatalf("scope audit concurrency=%+v", wf.Concurrency)
	}
	scope := wf.Jobs["scope"]
	if scope.Environment != "release" || scope.TimeoutMinutes != 5 || scope.RunsOn != "ubuntu-latest" {
		t.Fatalf("scope audit environment=%q timeout=%d runner=%q", scope.Environment, scope.TimeoutMinutes, scope.RunsOn)
	}
	token := namedStep(t, scope, "Mint metadata-only installation token")
	if token.Uses != createAppTokenAction {
		t.Fatalf("scope audit token action=%q", token.Uses)
	}
	for key, want := range map[string]string{
		"client-id":           "${{ vars.TAP_APP_CLIENT_ID }}",
		"private-key":         "${{ secrets.TAP_APP_PRIVATE_KEY }}",
		"owner":               "${{ github.repository_owner }}",
		"permission-metadata": "read",
	} {
		if got := token.With[key]; got != want {
			t.Fatalf("scope audit token input %s=%q, want %q", key, got, want)
		}
	}
	for _, forbidden := range []string{"repositories", "permission-actions", "permission-contents", "permission-pull-requests", "skip-token-revoke"} {
		if _, ok := token.With[forbidden]; ok {
			t.Fatalf("scope audit token unexpectedly sets %q", forbidden)
		}
	}
	verify := namedStep(t, scope, "Require a single-repository installation")
	for _, snippet := range []string{
		"installation/repositories",
		"ildarbinanas-design/homebrew-tap",
		`${#repositories[@]}" != "1`,
		"GITHUB_STEP_SUMMARY",
		"metadata read",
	} {
		if !strings.Contains(verify.Run, snippet) {
			t.Fatalf("scope audit missing %q", snippet)
		}
	}
	if verify.Env["GH_TOKEN"] != "${{ steps.app-token.outputs.token }}" {
		t.Fatalf("scope audit verify env=%v", verify.Env)
	}
}

func TestReleasePlanningAppScopeAuditIsReadOnly(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/audit-release-planning-app.yml")
	if wf.Permissions["contents"] != "read" || len(wf.Permissions) != 1 {
		t.Fatalf("planning scope audit workflow permissions=%v", wf.Permissions)
	}
	if wf.Concurrency.Group != "env-vault-release-planning-app-audit" || wf.Concurrency.CancelInProgress {
		t.Fatalf("planning scope audit concurrency=%+v", wf.Concurrency)
	}
	scope := wf.Jobs["scope"]
	if scope.Environment != "release-planning" || scope.TimeoutMinutes != 5 || scope.RunsOn != "ubuntu-latest" {
		t.Fatalf("planning scope audit environment=%q timeout=%d runner=%q", scope.Environment, scope.TimeoutMinutes, scope.RunsOn)
	}
	checkout := usesStep(t, scope, "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0")
	if checkout.With["ref"] != "main" || checkout.With["persist-credentials"] != "false" {
		t.Fatalf("planning scope audit checkout=%v", checkout.With)
	}
	token := namedStep(t, scope, "Mint read-only installation audit token")
	if token.Uses != createAppTokenAction {
		t.Fatalf("planning scope audit token action=%q", token.Uses)
	}
	for key, want := range map[string]string{
		"client-id":                 "${{ vars.RELEASE_APP_CLIENT_ID }}",
		"private-key":               "${{ secrets.RELEASE_APP_PRIVATE_KEY }}",
		"owner":                     "${{ github.repository_owner }}",
		"permission-administration": "read",
		"permission-metadata":       "read",
	} {
		if got := token.With[key]; got != want {
			t.Fatalf("planning scope audit token input %s=%q, want %q", key, got, want)
		}
	}
	for _, forbidden := range []string{"repositories", "permission-actions", "permission-contents", "permission-issues", "permission-pull-requests", "skip-token-revoke"} {
		if _, ok := token.With[forbidden]; ok {
			t.Fatalf("planning scope audit unexpectedly sets %q", forbidden)
		}
	}
	identity := namedStep(t, scope, "Require the exact release planning App identity")
	if identity.Env["ACTUAL_APP_SLUG"] != "${{ steps.app-token.outputs.app-slug }}" || !strings.Contains(identity.Run, `"$ACTUAL_APP_SLUG" != "env-vault-release-planning"`) {
		t.Fatalf("planning scope App identity step=%+v", identity)
	}
	settings := namedStep(t, scope, "Verify repository release settings and bypass policy")
	if settings.Env["GH_TOKEN"] != "${{ steps.app-token.outputs.token }}" || settings.Run != "scripts/release/verify-repository-release-settings.sh" {
		t.Fatalf("planning scope repository settings step=%+v", settings)
	}
	verify := namedStep(t, scope, "Require a single-repository installation")
	for _, snippet := range []string{
		"installation/repositories",
		"ildarbinanas-design/env-vault",
		`${#repositories[@]}" != "1`,
		"GITHUB_STEP_SUMMARY",
		"metadata read and administration read",
	} {
		if !strings.Contains(verify.Run, snippet) {
			t.Fatalf("planning scope audit missing %q", snippet)
		}
	}
	if len(verify.Env) != 1 || verify.Env["GH_TOKEN"] != "${{ steps.app-token.outputs.token }}" {
		t.Fatalf("planning scope audit verify env=%v", verify.Env)
	}
}

func TestReleasePleaseWaitsForExactGreenMainAndDefersPublishing(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/release-please.yml")
	if !slices.Equal(wf.On.WorkflowRun.Workflows, []string{"ci"}) ||
		!slices.Equal(wf.On.WorkflowRun.Types, []string{"completed"}) ||
		!slices.Equal(wf.On.WorkflowRun.Branches, []string{"main"}) {
		t.Fatalf("release-please workflow_run=%+v", wf.On.WorkflowRun)
	}
	if len(wf.Permissions) != 4 || wf.Permissions["actions"] != "read" || wf.Permissions["contents"] != "read" || wf.Permissions["issues"] != "read" || wf.Permissions["pull-requests"] != "read" {
		t.Fatalf("release-please permissions=%v", wf.Permissions)
	}
	if wf.Concurrency.Group != "env-vault-release" || wf.Concurrency.CancelInProgress || wf.Concurrency.Queue != "max" {
		t.Fatalf("release-please concurrency=%+v", wf.Concurrency)
	}

	plan := wf.Jobs["plan"]
	for _, snippet := range []string{
		"workflow_run.conclusion == 'success'",
		"workflow_run.event == 'push'",
		"workflow_run.head_branch == 'main'",
		"workflow_run.head_repository.full_name == github.repository",
	} {
		if !strings.Contains(plan.If, snippet) {
			t.Fatalf("release-please job if=%q, missing %q", plan.If, snippet)
		}
	}
	if plan.Environment != "release-planning" || plan.RunsOn != "ubuntu-latest" || plan.TimeoutMinutes != 10 {
		t.Fatalf("release-please plan environment=%q runner=%q timeout=%d", plan.Environment, plan.RunsOn, plan.TimeoutMinutes)
	}
	for key, want := range map[string]string{
		"publish":    "${{ steps.classify.outputs.publish }}",
		"source_sha": "${{ steps.classify.outputs.source_sha }}",
		"version":    "${{ steps.classify.outputs.version }}",
	} {
		if plan.Outputs[key] != want {
			t.Fatalf("release-please output %s=%q, want %q", key, plan.Outputs[key], want)
		}
	}

	checkout := namedStep(t, plan, "Check out the exact green main commit")
	if checkout.Uses != "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0" {
		t.Fatalf("release-please checkout=%q", checkout.Uses)
	}
	for key, want := range map[string]string{
		"ref":                 "${{ github.event.workflow_run.head_sha }}",
		"fetch-depth":         "2",
		"fetch-tags":          "true",
		"persist-credentials": "false",
	} {
		if checkout.With[key] != want {
			t.Fatalf("release-please checkout %s=%q, want %q", key, checkout.With[key], want)
		}
	}
	classify := namedStep(t, plan, "Classify the exact green main commit")
	if classify.ID != "classify" || classify.Env["EXPECTED_SHA"] != "${{ github.event.workflow_run.head_sha }}" {
		t.Fatalf("release classifier=%+v", classify)
	}
	for _, snippet := range []string{"git rev-parse HEAD", `"$actual_sha" != "$EXPECTED_SHA"`, "classify-release-commit.sh", "GITHUB_OUTPUT"} {
		if !strings.Contains(classify.Run, snippet) {
			t.Fatalf("release classifier missing %q", snippet)
		}
	}
	promotionSetup := namedStep(t, plan, "Set up Go for promotion verification")
	if promotionSetup.If != "steps.classify.outputs.publish == 'true'" || promotionSetup.Uses != "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16" || promotionSetup.With["go-version-file"] != "go.mod" {
		t.Fatalf("promotion verification setup=%+v", promotionSetup)
	}
	promotionDownload := namedStep(t, plan, "Download exact promotion manifest from triggering CI attempt")
	if promotionDownload.If != "steps.classify.outputs.publish == 'true'" || promotionDownload.Uses != "actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c" {
		t.Fatalf("promotion manifest download=%+v", promotionDownload)
	}
	for key, want := range map[string]string{
		"name":         "env-vault-promotion-${{ steps.classify.outputs.source_sha }}-attempt-${{ github.event.workflow_run.run_attempt }}",
		"path":         "promotion",
		"github-token": "${{ github.token }}",
		"repository":   "${{ github.repository }}",
		"run-id":       "${{ github.event.workflow_run.id }}",
	} {
		if promotionDownload.With[key] != want {
			t.Fatalf("promotion manifest download %s=%q, want %q", key, promotionDownload.With[key], want)
		}
	}
	promotionVerify := namedStep(t, plan, "Verify exact promotion manifest before authorization and tag")
	if promotionVerify.If != "steps.classify.outputs.publish == 'true'" {
		t.Fatalf("promotion verification if=%q", promotionVerify.If)
	}
	for _, snippet := range []string{
		"go run ./cmd/release-promotion verify",
		"--manifest promotion/promotion-manifest.json",
		`--source-sha "${{ steps.classify.outputs.source_sha }}"`,
		`--version "${{ steps.classify.outputs.version }}"`,
		`--run-id "${{ github.event.workflow_run.id }}"`,
		`--run-attempt "${{ github.event.workflow_run.run_attempt }}"`,
	} {
		if !strings.Contains(promotionVerify.Run, snippet) {
			t.Fatalf("promotion verification missing %q", snippet)
		}
	}
	preTagInventory := namedStep(t, plan, "Recheck exact promotion artifact inventory immediately before tag")
	if preTagInventory.If != "steps.classify.outputs.publish == 'true'" || preTagInventory.Env["GH_TOKEN"] != "${{ github.token }}" {
		t.Fatalf("pre-tag inventory=%+v", preTagInventory)
	}
	for _, snippet := range []string{
		"actions/runs/${CI_RUN_ID}",
		"actions/runs/${CI_RUN_ID}/artifacts?per_page=100",
		"go run ./cmd/release-promotion inventory",
		"--run-json ci-run.json",
		"--artifacts-json ci-artifacts.json",
		`--source-sha "$SOURCE_SHA"`,
		`--run-attempt "$CI_RUN_ATTEMPT"`,
	} {
		if !strings.Contains(preTagInventory.Run, snippet) {
			t.Fatalf("pre-tag inventory missing %q", snippet)
		}
	}
	current := namedStep(t, plan, "Require the planning commit to remain current")
	if current.ID != "current" || current.Env["GH_TOKEN"] != "${{ github.token }}" || current.Env["EXPECTED_SHA"] != "${{ github.event.workflow_run.head_sha }}" {
		t.Fatalf("release planning current-head step=%+v", current)
	}
	for _, snippet := range []string{"git/ref/heads/main", "current=true", "current=false", "skipping stale planning run"} {
		if !strings.Contains(current.Run, snippet) {
			t.Fatalf("release planning current-head step missing %q", snippet)
		}
	}
	authorize := namedStep(t, plan, "Verify generated release pull request authorization")
	if authorize.ID != "authorize" || authorize.If != "steps.classify.outputs.publish == 'true'" || authorize.Env["GH_TOKEN"] != "${{ github.token }}" || authorize.Env["RELEASE_APP_SLUG"] != "${{ steps.release-token.outputs.app-slug }}" {
		t.Fatalf("release authorization step=%+v", authorize)
	}
	for _, snippet := range []string{"verify-release-authorization.sh \"$VERSION\" \"$SOURCE_SHA\" prepublish", "pr_number=", "GITHUB_OUTPUT"} {
		if !strings.Contains(authorize.Run, snippet) {
			t.Fatalf("release authorization step missing %q", snippet)
		}
	}

	token := namedStep(t, plan, "Mint repository-scoped release planning token")
	if token.ID != "release-token" || token.Uses != createAppTokenAction {
		t.Fatalf("release planning token=%+v", token)
	}
	if token.If != "steps.classify.outputs.publish == 'true' || steps.current.outputs.current == 'true'" {
		t.Fatalf("release planning token if=%q", token.If)
	}
	for key, want := range map[string]string{
		"client-id":                 "${{ vars.RELEASE_APP_CLIENT_ID }}",
		"private-key":               "${{ secrets.RELEASE_APP_PRIVATE_KEY }}",
		"owner":                     "${{ github.repository_owner }}",
		"repositories":              "${{ github.event.repository.name }}",
		"permission-administration": "read",
		"permission-contents":       "write",
		"permission-issues":         "write",
		"permission-pull-requests":  "write",
	} {
		if token.With[key] != want {
			t.Fatalf("release planning token %s=%q, want %q", key, token.With[key], want)
		}
	}
	identity := namedStep(t, plan, "Require the exact release planning App identity")
	if identity.If != "steps.classify.outputs.publish == 'true' || steps.current.outputs.current == 'true'" || identity.Env["ACTUAL_APP_SLUG"] != "${{ steps.release-token.outputs.app-slug }}" || !strings.Contains(identity.Run, `.apps[] | select(.id == "release_planning")`) || !strings.Contains(identity.Run, `"$ACTUAL_APP_SLUG" != "$expected_slug"`) {
		t.Fatalf("release planning App identity step=%+v", identity)
	}
	settings := namedStep(t, plan, "Verify repository release settings and bypass policy")
	if settings.If != "steps.classify.outputs.publish == 'true' || steps.current.outputs.current == 'true'" || settings.Env["GH_TOKEN"] != "${{ steps.release-token.outputs.token }}" || settings.Run != "scripts/release/verify-repository-release-settings.sh" {
		t.Fatalf("release repository settings step=%+v", settings)
	}
	labels := namedStep(t, plan, "Ensure release lifecycle labels")
	if labels.If != "steps.classify.outputs.publish == 'true' || steps.current.outputs.current == 'true'" || labels.Env["GH_TOKEN"] != "${{ steps.release-token.outputs.token }}" || labels.Run != "scripts/release/ensure-release-labels.sh" {
		t.Fatalf("release lifecycle labels step=%+v", labels)
	}

	releasePlease := namedStep(t, plan, "Create or update the reviewed release pull request")
	if releasePlease.ID != "release-please" || releasePlease.Uses != releasePleaseAction {
		t.Fatalf("release-please action=%+v", releasePlease)
	}
	if releasePlease.If != "steps.classify.outputs.publish != 'true' && steps.current.outputs.current == 'true'" {
		t.Fatalf("release-please action if=%q", releasePlease.If)
	}
	for key, want := range map[string]string{
		"token":               "${{ steps.release-token.outputs.token }}",
		"target-branch":       "main",
		"config-file":         "release-please-config.json",
		"manifest-file":       ".release-please-manifest.json",
		"skip-github-release": "true",
	} {
		if releasePlease.With[key] != want {
			t.Fatalf("release-please action %s=%q, want %q", key, releasePlease.With[key], want)
		}
	}
	proposal := namedStep(t, plan, "Verify the proposal is based on a green main commit")
	if proposal.If != "steps.classify.outputs.publish != 'true' && steps.current.outputs.current == 'true'" || proposal.Env["GH_TOKEN"] != "${{ github.token }}" || proposal.Env["RELEASE_APP_SLUG"] != "${{ steps.release-token.outputs.app-slug }}" || proposal.Run != "scripts/release/verify-release-proposal.sh" {
		t.Fatalf("release proposal verification step=%+v", proposal)
	}

	tag := namedStep(t, plan, "Create or verify the exact release tag")
	if tag.If != "steps.classify.outputs.publish == 'true'" || tag.Env["GH_TOKEN"] != "${{ steps.release-token.outputs.token }}" || tag.Env["PR_NUMBER"] != "${{ steps.authorize.outputs.pr_number }}" {
		t.Fatalf("release tag step=%+v", tag)
	}
	for _, snippet := range []string{
		"resolve-tag-sha.sh",
		`tag_status" == "4`,
		"repos/${GITHUB_REPOSITORY}/git/refs",
		`ref=refs/tags/${VERSION}`,
		`sha=${SOURCE_SHA}`,
		`"$verified_sha" != "$SOURCE_SHA"`,
		`mark-release-pr-tagged.sh "$PR_NUMBER"`,
	} {
		if !strings.Contains(tag.Run, snippet) {
			t.Fatalf("release tag step missing %q", snippet)
		}
	}
	positions := make(map[string]int, len(plan.Steps))
	for index, step := range plan.Steps {
		positions[step.Name] = index
	}
	if !(positions["Classify the exact green main commit"] < positions["Download exact promotion manifest from triggering CI attempt"] &&
		positions["Download exact promotion manifest from triggering CI attempt"] < positions["Verify exact promotion manifest before authorization and tag"] &&
		positions["Verify exact promotion manifest before authorization and tag"] < positions["Verify generated release pull request authorization"] &&
		positions["Verify generated release pull request authorization"] < positions["Recheck exact promotion artifact inventory immediately before tag"] &&
		positions["Recheck exact promotion artifact inventory immediately before tag"]+1 == positions["Create or verify the exact release tag"]) {
		t.Fatalf("promotion must verify before authorization/tag, step positions=%v", positions)
	}
	allSteps := fmt.Sprintf("%v", plan.Steps)
	for _, forbidden := range []string{"gh release create", "--generate-notes", "TAP_APP_PRIVATE_KEY"} {
		if strings.Contains(allSteps, forbidden) {
			t.Fatalf("release planning workflow contains forbidden publication capability %q", forbidden)
		}
	}
}

func TestReleasePleaseConfigTracksDocsAndDefersGitHubRelease(t *testing.T) {
	type releasePackage struct {
		ReleaseType           string `json:"release-type"`
		PackageName           string `json:"package-name"`
		Component             string `json:"component"`
		ChangelogPath         string `json:"changelog-path"`
		SkipGitHubRelease     bool   `json:"skip-github-release"`
		IncludeVInTag         bool   `json:"include-v-in-tag"`
		IncludeComponentInTag bool   `json:"include-component-in-tag"`
		PullRequestTitle      string `json:"pull-request-title-pattern"`
		PullRequestHeader     string `json:"pull-request-header"`
		PullRequestFooter     string `json:"pull-request-footer"`
		ChangelogSections     []struct {
			Type    string `json:"type"`
			Section string `json:"section"`
			Hidden  bool   `json:"hidden"`
		} `json:"changelog-sections"`
		ExtraFiles []struct {
			Type string `json:"type"`
			Path string `json:"path"`
		} `json:"extra-files"`
	}
	var config struct {
		Schema               string                    `json:"$schema"`
		SeparatePullRequests bool                      `json:"separate-pull-requests"`
		Packages             map[string]releasePackage `json:"packages"`
	}
	data, err := os.ReadFile("../release-please-config.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse release-please config: %v", err)
	}
	if len(config.Packages) != 1 {
		t.Fatalf("release-please packages=%v", config.Packages)
	}
	if config.Schema != "https://raw.githubusercontent.com/googleapis/release-please/v17.6.0/schemas/config.json" {
		t.Fatalf("release-please schema=%q", config.Schema)
	}
	if !config.SeparatePullRequests {
		t.Fatal("release-please must preserve its component branch and title by using separate pull requests")
	}
	pkg := config.Packages["."]
	if pkg.ReleaseType != "go" || pkg.PackageName != "env-vault" || pkg.Component != "env-vault" || pkg.ChangelogPath != "CHANGELOG.md" {
		t.Fatalf("release package=%+v", pkg)
	}
	if !pkg.SkipGitHubRelease || !pkg.IncludeVInTag || pkg.IncludeComponentInTag {
		t.Fatalf("release tag ownership config=%+v", pkg)
	}
	// Release Please renders ${version} without a v prefix and deliberately
	// renders ${component} as empty when include-component-in-tag is false.
	// Keep both the project name and v literal in this reviewed title contract
	// while preserving the public vX.Y.Z tag format.
	if pkg.PullRequestTitle != "chore${scope}: release env-vault v${version}" {
		t.Fatalf("release PR title pattern=%q", pkg.PullRequestTitle)
	}
	renderedTitle := strings.NewReplacer(
		"${scope}", "(main)",
		"${version}", "0.0.8",
	).Replace(pkg.PullRequestTitle)
	if renderedTitle != "chore(main): release env-vault v0.0.8" {
		t.Fatalf("rendered release PR title=%q", renderedTitle)
	}
	if pkg.PullRequestHeader != "Merging this reviewed pull request authorizes publication of this exact version after the merge commit passes main CI." {
		t.Fatalf("release PR authorization header=%q", pkg.PullRequestHeader)
	}
	if pkg.PullRequestFooter != "This PR was generated with Release Please." {
		t.Fatalf("release PR footer=%q", pkg.PullRequestFooter)
	}
	sectionTypes := make([]string, 0, len(pkg.ChangelogSections))
	for _, section := range pkg.ChangelogSections {
		if section.Section == "" || section.Hidden {
			t.Fatalf("release changelog section=%+v, want visible named section", section)
		}
		sectionTypes = append(sectionTypes, section.Type)
	}
	if !slices.Equal(sectionTypes, []string{"feat", "fix", "build", "ci", "docs", "test", "refactor", "perf", "revert"}) {
		t.Fatalf("release changelog types=%v", sectionTypes)
	}
	if len(pkg.ExtraFiles) != 1 || pkg.ExtraFiles[0].Type != "generic" || pkg.ExtraFiles[0].Path != "README.md" {
		t.Fatalf("release extra files=%+v", pkg.ExtraFiles)
	}

	manifestData, err := os.ReadFile("../.release-please-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest := make(map[string]string)
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatalf("parse release manifest: %v", err)
	}
	version := manifest["."]
	if len(manifest) != 1 || !regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(version) {
		t.Fatalf("release manifest=%v", manifest)
	}
	readme, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatal(err)
	}
	marker := fmt.Sprintf("Current stable release: `v%s`. <!-- x-release-please-version -->", version)
	if strings.Count(string(readme), marker) != 1 {
		t.Fatalf("README release marker count=%d", strings.Count(string(readme), marker))
	}
	changelog, err := os.ReadFile("../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(changelog), fmt.Sprintf("## [%s]", version)) {
		t.Fatal("CHANGELOG missing current published release")
	}
}

func TestReleaseConcurrencyIsGlobalAndNeverCancelsInProgress(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/build-binaries.yml")
	if wf.Concurrency.Group != "env-vault-release" {
		t.Fatalf("concurrency group=%q, want global env-vault-release", wf.Concurrency.Group)
	}
	if wf.Concurrency.CancelInProgress {
		t.Fatal("release workflow must never cancel an in-progress publication")
	}
	if wf.Concurrency.Queue != "max" {
		t.Fatalf("concurrency queue=%q, want max so pending releases are not replaced", wf.Concurrency.Queue)
	}
}

func TestSemverComparisonHandlesLargeNumericComponents(t *testing.T) {
	cases := []struct {
		left  string
		right string
		want  string
	}{
		{left: "0.0.10", right: "0.0.9", want: "1"},
		{left: "2.0.0", right: "10.0.0", want: "-1"},
		{left: "1.2.3", right: "1.2.3", want: "0"},
		{left: "999999999999999999999999.0.0", right: "999999999999999999999998.999.999", want: "1"},
	}
	for _, tc := range cases {
		cmd := exec.Command("bash", "../scripts/release/semver-compare.sh", tc.left, tc.right)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("compare %s %s: %v\n%s", tc.left, tc.right, err, output)
		}
		if got := strings.TrimSpace(string(output)); got != tc.want {
			t.Fatalf("compare %s %s=%q, want %q", tc.left, tc.right, got, tc.want)
		}
	}
	for _, invalid := range []string{"v1.2.3", "01.2.3", "1.2", "1.2.3-rc.1"} {
		if err := exec.Command("bash", "../scripts/release/semver-compare.sh", invalid, "1.2.3").Run(); err == nil {
			t.Fatalf("invalid semver %q unexpectedly accepted", invalid)
		}
	}
}

func TestManualReleaseInputAndGates(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/build-binaries.yml")
	const wantRunName = "env-vault-publication event=${{ github.event_name }} version=${{ github.event.inputs.version || github.ref_name }} repair=${{ github.event.inputs.repair || 'none' }} state=${{ github.event.inputs.repair_state_digest || 'automatic' }}"
	if wf.RunName != wantRunName {
		t.Fatalf("build-binaries run-name=%q, want %q", wf.RunName, wantRunName)
	}
	if len(wf.Permissions) != 4 || wf.Permissions["actions"] != "read" || wf.Permissions["contents"] != "read" || wf.Permissions["issues"] != "read" || wf.Permissions["pull-requests"] != "read" {
		t.Fatalf("build-binaries workflow permissions=%v", wf.Permissions)
	}
	version, ok := wf.On.WorkflowDispatch.Inputs["version"]
	if !ok {
		t.Fatal("workflow_dispatch missing optional version input")
	}
	if version.Required || version.Default != "" || version.Type != "string" {
		t.Fatalf("version input required=%v default=%q type=%q", version.Required, version.Default, version.Type)
	}
	repair, ok := wf.On.WorkflowDispatch.Inputs["repair"]
	if !ok {
		t.Fatal("workflow_dispatch missing repair input")
	}
	wantRepairOptions := []string{"none", "release-assets", "homebrew", "health"}
	if !repair.Required || repair.Default != "none" || repair.Type != "choice" || !slices.Equal(repair.Options, wantRepairOptions) {
		t.Fatalf("repair input required=%v default=%q type=%q options=%v", repair.Required, repair.Default, repair.Type, repair.Options)
	}
	repairState, ok := wf.On.WorkflowDispatch.Inputs["repair_state_digest"]
	if !ok || repairState.Required || repairState.Default != "" || repairState.Type != "string" {
		t.Fatalf("repair_state_digest input=%+v present=%v", repairState, ok)
	}

	metadata := wf.Jobs["metadata"]
	checkout := usesStep(t, metadata, "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0")
	for key, want := range map[string]string{
		"fetch-depth":         "2",
		"fetch-tags":          "true",
		"persist-credentials": "false",
	} {
		if checkout.With[key] != want {
			t.Fatalf("release metadata checkout %s=%q, want %q", key, checkout.With[key], want)
		}
	}
	resolve := namedStep(t, metadata, "Resolve build version and release mode")
	for _, snippet := range []string{
		"refs/tags/${INPUT_VERSION}",
		"manual repairs must run from the exact immutable release tag ref",
		`"$GITHUB_SHA" != "$source_sha"`,
		"GITHUB_OUTPUT",
		"publish=false",
		"repair mode requires an explicit version",
		"repair mode requires an exact lowercase 64-hex repair_state_digest",
		"repair_state_digest is only valid for an explicit repair mode",
		"manual steady-state publication requires a plan-derived repair mode",
		"scripts/release/resolve-tag-sha.sh",
		"publishing requires an existing exact tag created by release planning",
		`semver-compare.sh "${version#v}" "0.0.7"`,
		"outside the steady-state publisher",
		"releasectl release legacy-rebuild plan/apply",
		"run_release",
		"run_homebrew",
		"source_sha",
		"classify-release-commit.sh",
		`verify-release-authorization.sh "$version" "$source_sha" tagged`,
		"for attempt in {1..12}",
		"sleep 5",
		`"$authorized" != "true"`,
		"tag source is not a deterministic release commit",
		"v0.0.8 is an intentionally failed immutable tag",
		"actions/workflows/ci.yml/runs?event=push&status=success&head_sha=${source_sha}",
		`.head_repository.full_name == $repository`,
		`.path == ".github/workflows/ci.yml"`,
		`"$(jq 'length' <<< "$matches")" == "1"`,
		"ci_run_attempt",
		"use_promotion=true",
		`version="ci-${source_sha}"`,
	} {
		if !strings.Contains(resolve.Run, snippet) {
			t.Fatalf("metadata resolution missing %q", snippet)
		}
	}
	if resolve.Env["RELEASE_APP_SLUG"] != "" || !strings.Contains(resolve.Run, `.apps[] | select(.id == "release_planning")`) || !strings.Contains(resolve.Run, "release/contract.v1.json") || !strings.Contains(resolve.Run, "export RELEASE_APP_SLUG") {
		t.Fatalf("release metadata does not derive the App slug from the contract: env=%q run=%q", resolve.Env["RELEASE_APP_SLUG"], resolve.Run)
	}
	if !strings.Contains(resolve.Run, "source scripts/release/lib.sh") || !strings.Contains(resolve.Run, `release_require_version "$version"`) {
		t.Fatal("metadata resolution does not use the declarative release version policy")
	}
	for key, want := range map[string]string{
		"use_promotion":        "${{ steps.resolve.outputs.use_promotion }}",
		"ci_run_id":            "${{ steps.resolve.outputs.ci_run_id }}",
		"ci_run_attempt":       "${{ steps.resolve.outputs.ci_run_attempt }}",
		"tap_repository":       "${{ steps.resolve.outputs.tap_repository }}",
		"tap_repository_owner": "${{ steps.resolve.outputs.tap_repository_owner }}",
		"tap_repository_name":  "${{ steps.resolve.outputs.tap_repository_name }}",
		"tap_ci_workflow":      "${{ steps.resolve.outputs.tap_ci_workflow }}",
	} {
		if metadata.Outputs[key] != want {
			t.Fatalf("metadata output %s=%q, want %q", key, metadata.Outputs[key], want)
		}
	}

	release := wf.Jobs["release"]
	for _, need := range []string{"metadata", "preflight", "quality", "promotion"} {
		if !slices.Contains(release.Needs, need) {
			t.Fatalf("release needs=%v, missing %q", release.Needs, need)
		}
	}
	for _, snippet := range []string{"always()", "!cancelled()", "needs.metadata.result == 'success'", "needs.preflight.result == 'success'", "run_release == 'true'", "use_promotion == 'true'", "needs.quality.result == 'skipped'", "needs.promotion.result == 'success'"} {
		if !strings.Contains(release.If, snippet) {
			t.Fatalf("release if=%q, missing %q", release.If, snippet)
		}
	}

	for _, step := range release.Steps {
		if strings.Contains(step.Run, "git/refs") || strings.Contains(step.Name, "Create release tag") {
			t.Fatalf("publisher must not create version tags during manual dispatch: %+v", step)
		}
	}
	verifyTag := namedStep(t, release, "Verify release tag commit")
	for _, snippet := range []string{"resolve-tag-sha.sh", `existing_sha" != "$SOURCE_SHA`, "exit 1"} {
		if !strings.Contains(verifyTag.Run, snippet) {
			t.Fatalf("tag verification missing %q", snippet)
		}
	}
}

func TestPublisherConsumesOneVerifiedCIPromotionAttemptWithoutSteadyStateRebuild(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/build-binaries.yml")
	metadata := wf.Jobs["metadata"]
	resolve := namedStep(t, metadata, "Resolve build version and release mode")
	if strings.Contains(resolve.Run, "legacy_release") {
		t.Fatal("steady-state publisher still contains a legacy rebuild path")
	}
	for _, snippet := range []string{
		`ci_run_id="$(jq -er '.[0].id`,
		`ci_run_attempt="$(jq -er '.[0].run_attempt`,
		`use_promotion=true`,
		`if [[ "$repair" == "none" || "$repair" == "release-assets" ]]`,
		`run_build=false`,
	} {
		if !strings.Contains(resolve.Run, snippet) {
			t.Fatalf("publisher metadata missing promotion/rebuild split %q", snippet)
		}
	}

	quality := wf.Jobs["quality"]
	if quality.If != "needs.metadata.outputs.run_build == 'true'" {
		t.Fatalf("fallback quality condition=%q", quality.If)
	}
	promotion := wf.Jobs["promotion"]
	if promotion.If != "needs.metadata.outputs.use_promotion == 'true'" || !slices.Equal(promotion.Needs, []string{"metadata"}) || promotion.RunsOn != "ubuntu-latest" {
		t.Fatalf("promotion job if=%q needs=%v runner=%q", promotion.If, promotion.Needs, promotion.RunsOn)
	}
	if len(promotion.Permissions) != 2 || promotion.Permissions["actions"] != "read" || promotion.Permissions["contents"] != "read" || promotion.Environment != "" {
		t.Fatalf("promotion job permission boundary=%v environment=%q", promotion.Permissions, promotion.Environment)
	}
	checkout := usesStep(t, promotion, "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0")
	if checkout.With["ref"] != "${{ needs.metadata.outputs.source_sha }}" || checkout.With["persist-credentials"] != "false" {
		t.Fatalf("promotion checkout=%v", checkout.With)
	}
	inventory := namedStep(t, promotion, "Require exact current-attempt CI artifact inventory")
	for _, snippet := range []string{
		"actions/runs/${CI_RUN_ID}",
		"actions/runs/${CI_RUN_ID}/artifacts?per_page=100",
		"go run ./cmd/release-promotion inventory",
		"--run-json ci-run.json",
		"--artifacts-json ci-artifacts.json",
		`--source-sha "$SOURCE_SHA"`,
		`--run-attempt "$CI_RUN_ATTEMPT"`,
	} {
		if !strings.Contains(inventory.Run, snippet) {
			t.Fatalf("promotion inventory missing %q", snippet)
		}
	}
	manifestDownload := namedStep(t, promotion, "Download exact promotion manifest from CI")
	for key, want := range map[string]string{
		"name":       "env-vault-promotion-${{ needs.metadata.outputs.source_sha }}-attempt-${{ needs.metadata.outputs.ci_run_attempt }}",
		"run-id":     "${{ needs.metadata.outputs.ci_run_id }}",
		"repository": "${{ github.repository }}",
	} {
		if manifestDownload.With[key] != want {
			t.Fatalf("promotion manifest download %s=%q, want %q", key, manifestDownload.With[key], want)
		}
	}
	artifactsDownload := namedStep(t, promotion, "Download five exact native release artifacts from CI")
	if artifactsDownload.With["pattern"] != "env-vault-release-*-attempt-${{ needs.metadata.outputs.ci_run_attempt }}" || artifactsDownload.With["run-id"] != "${{ needs.metadata.outputs.ci_run_id }}" || artifactsDownload.With["merge-multiple"] != "true" {
		t.Fatalf("promotion artifacts download=%v", artifactsDownload.With)
	}
	verify := namedStep(t, promotion, "Verify promotion and stage exact publisher bundle")
	for _, snippet := range []string{
		"go run ./cmd/release-promotion verify",
		"--manifest promotion-manifest/promotion-manifest.json",
		`--source-sha "$SOURCE_SHA"`,
		`--version "$VERSION"`,
		`--run-id "$CI_RUN_ID"`,
		`--run-attempt "$CI_RUN_ATTEMPT"`,
		"--artifacts promotion-artifacts",
		`[[ "$(find verified-bundle -maxdepth 1 -type f | wc -l | tr -d '[:space:]')" == "11" ]]`,
	} {
		if !strings.Contains(verify.Run, snippet) {
			t.Fatalf("promotion bundle verification missing %q", snippet)
		}
	}
	bundleUpload := namedStep(t, promotion, "Upload publisher-local verified bundle")
	if bundleUpload.With["name"] != "env-vault-publisher-bundle-${{ needs.metadata.outputs.source_sha }}-attempt-${{ github.run_attempt }}" || bundleUpload.With["path"] != "verified-bundle" || bundleUpload.With["if-no-files-found"] != "error" {
		t.Fatalf("verified publisher bundle upload=%v", bundleUpload.With)
	}

	release := wf.Jobs["release"]
	promotionBundle := namedStep(t, release, "Download publisher-local verified promotion bundle")
	if promotionBundle.If != "needs.metadata.outputs.use_promotion == 'true'" || promotionBundle.With["name"] != "env-vault-publisher-bundle-${{ needs.metadata.outputs.source_sha }}-attempt-${{ github.run_attempt }}" {
		t.Fatalf("release promotion bundle download=%+v", promotionBundle)
	}
	for _, step := range release.Steps {
		if step.Name == "Download legacy rebuild artifacts" || strings.Contains(step.If, "legacy_release") {
			t.Fatalf("steady-state release retains legacy artifact path: %+v", step)
		}
	}
	for _, step := range release.Steps {
		if strings.HasPrefix(step.Uses, "actions/download-artifact@") && step.With["name"] == "" && step.With["pattern"] == "" {
			t.Fatalf("release contains unscoped artifact download: %+v", step)
		}
	}
	for _, jobName := range []string{"homebrew", "health"} {
		job := wf.Jobs[jobName]
		if !slices.Contains(job.Needs, "promotion") || !strings.Contains(job.If, "needs.metadata.outputs.use_promotion != 'true' || needs.promotion.result == 'success'") {
			t.Fatalf("%s does not fail closed on new-version promotion: needs=%v if=%q", jobName, job.Needs, job.If)
		}
	}
}

func TestHomebrewMonotonicPreflightRunsBeforeReleaseMutation(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/build-binaries.yml")
	preflight := wf.Jobs["preflight"]
	if !slices.Contains(preflight.Needs, "metadata") {
		t.Fatalf("preflight needs=%v", preflight.Needs)
	}
	for _, snippet := range []string{"always()", "!cancelled()", "publish == 'true'", "repair != 'health'"} {
		if !strings.Contains(preflight.If, snippet) {
			t.Fatalf("preflight if=%q, missing %q", preflight.If, snippet)
		}
	}
	guard := namedStep(t, preflight, "Guard Homebrew version monotonicity")
	for _, snippet := range []string{
		`"https://github.com/${TAP_REPOSITORY}.git"`,
		"semver-compare.sh",
		"refusing release downgrade",
		"exit 1",
	} {
		if !strings.Contains(guard.Run, snippet) {
			t.Fatalf("preflight guard missing %q", snippet)
		}
	}
	if guard.Env["TAP_REPOSITORY"] != "${{ needs.metadata.outputs.tap_repository }}" || strings.Contains(guard.Run, "ildarbinanas-design/homebrew-tap") {
		t.Fatalf("preflight does not use the contract-derived tap repository: env=%v run=%q", guard.Env, guard.Run)
	}
	if !slices.Contains(wf.Jobs["release"].Needs, "preflight") {
		t.Fatal("release mutation is not gated by monotonic preflight")
	}
}

func TestResolvedVersionFeedsAllReleaseStages(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/build-binaries.yml")
	want := "${{ needs.metadata.outputs.version }}"
	quality := wf.Jobs["quality"]
	if !slices.Contains(quality.Needs, "metadata") {
		t.Fatalf("quality needs=%v, missing metadata", quality.Needs)
	}
	if quality.Uses != "./.github/workflows/reusable-quality.yml" {
		t.Fatalf("quality uses=%q", quality.Uses)
	}
	if quality.With["version"] != want {
		t.Fatalf("quality version=%q, want %q", quality.With["version"], want)
	}
	if quality.With["source_sha"] != "${{ needs.metadata.outputs.source_sha }}" {
		t.Fatalf("quality source_sha=%q", quality.With["source_sha"])
	}

	reusable := readWorkflow(t, "../.github/workflows/reusable-quality.yml")
	build := reusable.Jobs["native"]
	if step := namedStep(t, build, "Build native release artifact"); step.Env["VERSION"] != "${{ needs.resolve.outputs.version }}" {
		t.Fatalf("reusable build VERSION=%q", step.Env["VERSION"])
	}
	checks := []struct {
		job  string
		step string
	}{
		{job: "release", step: "Create GitHub Release"},
		{job: "supply_chain", step: "Download canonical release assets"},
		{job: "homebrew", step: "Generate formula"},
		{job: "homebrew", step: "Create or reuse Homebrew pull request"},
	}
	for _, check := range checks {
		step := namedStep(t, wf.Jobs[check.job], check.step)
		if step.Env["VERSION"] != want {
			t.Fatalf("%s/%s VERSION=%q, want %q", check.job, check.step, step.Env["VERSION"], want)
		}
	}

	homebrew := wf.Jobs["homebrew"]
	if !slices.Contains(homebrew.Needs, "metadata") || !slices.Contains(homebrew.Needs, "preflight") || !slices.Contains(homebrew.Needs, "release") {
		t.Fatalf("homebrew needs=%v", homebrew.Needs)
	}
	for _, snippet := range []string{"always()", "!cancelled()", "needs.metadata.result == 'success'", "needs.preflight.result == 'success'", "run_homebrew == 'true'", "needs.release.result == 'success'", "repair == 'homebrew'", "needs.release.result == 'skipped'"} {
		if !strings.Contains(homebrew.If, snippet) {
			t.Fatalf("homebrew if=%q, missing %q", homebrew.If, snippet)
		}
	}
}

func TestNewGitHubReleaseNotesComeFromVersionedChangelog(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/build-binaries.yml")
	create := namedStep(t, wf.Jobs["release"], "Create GitHub Release")
	for _, snippet := range []string{
		"application/vnd.github.raw+json",
		`contents/CHANGELOG.md?ref=${SOURCE_SHA}`,
		`scripts/release/extract-changelog-section.sh "$VERSION" source-CHANGELOG.md > release-notes.md`,
		`--title "$VERSION"`,
		"--notes-file release-notes.md",
	} {
		if !strings.Contains(create.Run, snippet) {
			t.Fatalf("release creation missing %q", snippet)
		}
	}
	if create.Env["SOURCE_SHA"] != "${{ needs.metadata.outputs.source_sha }}" {
		t.Fatalf("release notes source SHA=%q", create.Env["SOURCE_SHA"])
	}
	if strings.Contains(create.Run, "--generate-notes") {
		t.Fatal("new releases must use the reviewed source CHANGELOG rather than regenerated notes")
	}
	for _, path := range []string{
		"../scripts/release/classify-release-commit.sh",
		"../scripts/release/ensure-release-labels.sh",
		"../scripts/release/extract-changelog-section.sh",
		"../scripts/release/mark-release-pr-tagged.sh",
		"../scripts/release/verify-release-authorization.sh",
		"../scripts/release/verify-release-proposal.sh",
		"../scripts/release/verify-repository-release-settings.sh",
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
			t.Fatalf("release helper is not executable: %s", path)
		}
	}
}

func TestCIAndReleaseCallReusableQuality(t *testing.T) {
	reusable := readWorkflow(t, "../.github/workflows/reusable-quality.yml")
	for _, inputName := range []string{"source_sha", "version"} {
		input, ok := reusable.On.WorkflowCall.Inputs[inputName]
		if !ok {
			t.Fatalf("workflow_call missing %q input", inputName)
		}
		if !input.Required || input.Type != "string" {
			t.Fatalf("workflow_call input %s required=%v type=%q", inputName, input.Required, input.Type)
		}
	}
	for _, inputName := range []string{"event_name", "pull_request_head_ref", "pull_request_head_sha"} {
		input, ok := reusable.On.WorkflowCall.Inputs[inputName]
		if !ok || input.Required || input.Type != "string" || input.Default != "" {
			t.Fatalf("optional workflow_call input %s=%+v", inputName, input)
		}
	}

	ci := readWorkflow(t, "../.github/workflows/ci.yml")
	if !slices.Equal(ci.On.Push.Branches, []string{"main"}) {
		t.Fatalf("CI push branches=%v, want main only to avoid duplicate PR branch runs", ci.On.Push.Branches)
	}
	if !slices.Equal(ci.On.PullRequest.Types, []string{"opened", "synchronize", "reopened"}) {
		t.Fatalf("CI pull_request types=%v, want code-bearing events only", ci.On.PullRequest.Types)
	}
	if ci.Concurrency.Group != "ci-${{ github.event_name == 'workflow_dispatch' && format('manual-{0}', github.run_id) || github.event.pull_request.number || github.ref }}" || !ci.Concurrency.CancelInProgress {
		t.Fatalf("CI concurrency=%+v, want manual runs isolated from automatic branch runs", ci.Concurrency)
	}
	if len(ci.Jobs) != 2 {
		t.Fatalf("CI has %d jobs, want reusable quality caller and stable gate", len(ci.Jobs))
	}
	if _, ok := ci.Jobs["pr-title"]; ok {
		t.Fatal("full CI must not own the metadata-only PR title check")
	}

	prTitleWorkflow := readWorkflow(t, "../.github/workflows/pr-title.yml")
	if !slices.Equal(prTitleWorkflow.On.PullRequest.Types, []string{"opened", "synchronize", "reopened", "edited"}) {
		t.Fatalf("PR-title pull_request types=%v, want code and metadata changes", prTitleWorkflow.On.PullRequest.Types)
	}
	if len(prTitleWorkflow.Jobs) != 1 {
		t.Fatalf("PR-title workflow has %d jobs, want one lightweight check", len(prTitleWorkflow.Jobs))
	}
	if len(prTitleWorkflow.Permissions) != 0 {
		t.Fatalf("PR-title workflow permissions=%v, want no token permissions", prTitleWorkflow.Permissions)
	}
	if prTitleWorkflow.Concurrency.Group != "pr-title-${{ github.event.pull_request.number }}" || !prTitleWorkflow.Concurrency.CancelInProgress {
		t.Fatalf("PR-title concurrency=%+v", prTitleWorkflow.Concurrency)
	}
	prTitle := prTitleWorkflow.Jobs["pr-title"]
	if prTitle.RunsOn != "ubuntu-latest" {
		t.Fatalf("PR title runner=%q", prTitle.RunsOn)
	}
	// The edited event must rerun the same required context. In particular, the
	// adversarial sequence invalid title -> body-only edit cannot reuse a stale
	// successful context or skip the job that previously failed.
	if prTitle.If != "" {
		t.Fatalf("PR title job has conditional skip %q; body edits must rerun validation", prTitle.If)
	}
	titleCheck := namedStep(t, prTitle, "Require a Conventional Commit pull request title")
	if titleCheck.Uses != "" {
		t.Fatalf("PR title check uses external action %q, want shell-only validation", titleCheck.Uses)
	}
	if len(titleCheck.Env) != 1 || titleCheck.Env["PR_TITLE"] != "${{ github.event.pull_request.title }}" {
		t.Fatalf("PR title check env=%v", titleCheck.Env)
	}
	for _, snippet := range []string{"Conventional Commits", "feat|fix|perf|refactor|build|ci|docs|test|chore|revert"} {
		if !strings.Contains(titleCheck.Run, snippet) {
			t.Fatalf("PR title check missing %q", snippet)
		}
	}
	ciQuality := ci.Jobs["quality"]
	if ciQuality.Uses != "./.github/workflows/reusable-quality.yml" {
		t.Fatalf("CI quality uses=%q", ciQuality.Uses)
	}
	if ciQuality.With["source_sha"] != "${{ github.sha }}" || ciQuality.With["version"] != "auto" ||
		ciQuality.With["event_name"] != "${{ github.event_name }}" ||
		ciQuality.With["pull_request_head_ref"] != "${{ github.event.pull_request.head.ref || '' }}" ||
		ciQuality.With["pull_request_head_sha"] != "${{ github.event.pull_request.head.sha || '' }}" {
		t.Fatalf("CI quality inputs=%v", ciQuality.With)
	}
	if len(ciQuality.Permissions) != 2 || ciQuality.Permissions["contents"] != "read" || ciQuality.Permissions["actions"] != "read" {
		t.Fatalf("CI reusable quality permissions=%v, want contents/actions read", ciQuality.Permissions)
	}
	gate := ci.Jobs["quality-gate"]
	if gate.If != "always()" || !slices.Equal(gate.Needs, []string{"quality"}) || gate.RunsOn != "ubuntu-latest" {
		t.Fatalf("quality gate if=%q needs=%v runner=%q", gate.If, gate.Needs, gate.RunsOn)
	}
	require := namedStep(t, gate, "Require every reusable quality job")
	if len(require.Env) != 1 || require.Env["QUALITY_RESULT"] != "${{ needs.quality.result }}" || !strings.Contains(require.Run, `"$QUALITY_RESULT" != "success"`) || strings.Contains(require.Run, "PR_TITLE_RESULT") {
		t.Fatalf("quality gate step env=%v run=%q", require.Env, require.Run)
	}

	release := readWorkflow(t, "../.github/workflows/build-binaries.yml")
	quality := release.Jobs["quality"]
	if quality.Uses != "./.github/workflows/reusable-quality.yml" {
		t.Fatalf("release quality uses=%q", quality.Uses)
	}
	if quality.If != "needs.metadata.outputs.run_build == 'true'" {
		t.Fatalf("release quality if=%q", quality.If)
	}
	if quality.With["source_sha"] != "${{ needs.metadata.outputs.source_sha }}" || quality.With["version"] != "${{ needs.metadata.outputs.version }}" {
		t.Fatalf("release quality inputs=%v", quality.With)
	}
	if len(quality.Permissions) != 2 || quality.Permissions["contents"] != "read" || quality.Permissions["actions"] != "read" {
		t.Fatalf("release reusable quality permissions=%v, want contents/actions read", quality.Permissions)
	}

	dependencyReview := readWorkflow(t, "../.github/workflows/dependency-review.yml")
	if dependencyReview.Concurrency.Group != "dependency-review-${{ github.event.pull_request.number }}" || !dependencyReview.Concurrency.CancelInProgress {
		t.Fatalf("dependency review concurrency=%+v", dependencyReview.Concurrency)
	}
	for _, removed := range []string{"verify", "license", "build", "smoke"} {
		if _, ok := release.Jobs[removed]; ok {
			t.Fatalf("release caller still duplicates reusable job %q", removed)
		}
	}
}

func TestReusableQualityRunsPinnedLicenseGateNatively(t *testing.T) {
	reusable := readWorkflow(t, "../.github/workflows/reusable-quality.yml")
	license := reusable.Jobs["license"]
	if license.RunsOn != "${{ matrix.runner }}" {
		t.Fatalf("license runs-on=%q", license.RunsOn)
	}
	wantPlatforms := map[string]string{
		"linux":   "ubuntu-latest",
		"darwin":  "macos-15",
		"windows": "windows-latest",
	}
	for _, target := range license.Strategy.Matrix.Include {
		if target.Runner != wantPlatforms[target.OS] {
			t.Fatalf("license %s runner=%q", target.OS, target.Runner)
		}
		delete(wantPlatforms, target.OS)
	}
	if len(wantPlatforms) != 0 {
		t.Fatalf("license matrix missing native platforms: %v", wantPlatforms)
	}
	setup := usesStep(t, license, "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16")
	if setup.With["go-version-file"] != "go.mod" || setup.With["go-version"] != "" {
		t.Fatalf("license toolchain inputs=%v, want exact project Go version", setup.With)
	}
	step := namedStep(t, license, "Check dependency licenses")
	if step.Run != "scripts/license-check.sh" || step.Shell != "bash" {
		t.Fatalf("license run=%q shell=%q", step.Run, step.Shell)
	}

	data, err := os.ReadFile("../scripts/license-check.sh")
	if err != nil {
		t.Fatalf("read license script: %v", err)
	}
	script := string(data)
	if !strings.Contains(script, "github.com/google/go-licenses/v2@${tool_version}") || !strings.Contains(script, `tool_version="v2.0.1"`) {
		t.Fatal("license script must pin go-licenses v2.0.1")
	}
	if strings.Contains(script, "@latest") {
		t.Fatal("license script must not use @latest")
	}
	for _, snippet := range []string{"go env GOHOSTOS", "windows)", "cygpath -w", "go-licenses.exe", `GOBIN="$gobin"`} {
		if !strings.Contains(script, snippet) {
			t.Fatalf("license script missing platform-aware case %q", snippet)
		}
	}
}

func TestReusableQualityRequiresTidyVerifiedModules(t *testing.T) {
	resolve := readWorkflowJob(t, "../.github/workflows/reusable-quality.yml", "resolve")
	if resolve.Name != "resolve-source-quality" || resolve.RunsOn != "ubuntu-latest" || resolve.Env["GOTOOLCHAIN"] != "local" {
		t.Fatalf("resolve/source-quality job name=%q runner=%q env=%v", resolve.Name, resolve.RunsOn, resolve.Env)
	}
	checkout := usesStep(t, resolve, "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0")
	if checkout.With["ref"] != "${{ inputs.source_sha }}" {
		t.Fatalf("resolve checkout ref=%q", checkout.With["ref"])
	}
	if checkout.With["fetch-depth"] != "3" {
		t.Fatalf("resolve checkout depth=%q, want synthetic merge, PR head, and its parent for deterministic classification", checkout.With["fetch-depth"])
	}
	setup := usesStep(t, resolve, "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16")
	if setup.With["go-version-file"] != "go.mod" || setup.With["go-version"] != "" {
		t.Fatalf("resolve setup-go inputs=%v", setup.With)
	}
	version := namedStep(t, resolve, "Resolve exact candidate version")
	for _, snippet := range []string{
		"release-please--branches--main--components--env-vault",
		"classify-release-commit.sh",
		`"${source_parents[2]}" != "$PR_HEAD_SHA"`,
		`resolved="ci-${SOURCE_SHA}"`,
		"release_candidate=true",
	} {
		if !strings.Contains(version.Run, snippet) {
			t.Fatalf("exact version resolver missing %q", snippet)
		}
	}
	contract := namedStep(t, resolve, "Validate release contract and resolve native matrix")
	for _, snippet := range []string{"go run ./cmd/release-contract matrix --json", "length == 5", "GITHUB_OUTPUT"} {
		if !strings.Contains(contract.Run, snippet) {
			t.Fatalf("release contract resolver missing %q", snippet)
		}
	}
	if resolve.Outputs["matrix"] != "${{ steps.contract.outputs.matrix }}" || resolve.Outputs["version"] != "${{ steps.version.outputs.version }}" {
		t.Fatalf("resolve outputs=%v", resolve.Outputs)
	}
	race := readWorkflowJob(t, "../.github/workflows/reusable-quality.yml", "race")
	if namedStep(t, race, "Require tidy module files").Run != "go mod tidy -diff" {
		t.Fatal("module job must fail on a non-idempotent go mod tidy")
	}
	if namedStep(t, race, "Verify module cache").Run != "go mod verify" {
		t.Fatal("module job must verify downloaded modules")
	}
	if namedStep(t, race, "Run source tests").Run != "go test ./..." || namedStep(t, race, "Vet source").Run != "go vet ./..." || namedStep(t, race, "Run source smoke tests").Run != "scripts/smoke.sh" || namedStep(t, race, "Run source race tests").Run != "go test -race ./..." {
		t.Fatal("source-quality job must retain tests, vet, and smoke guarantees")
	}
	for _, expensive := range []string{"Require tidy module files", "Verify module cache", "Run source tests", "Vet source", "Run source smoke tests"} {
		for _, step := range resolve.Steps {
			if step.Name == expensive {
				t.Fatalf("resolve serializes expensive source gate %q ahead of the native matrix", expensive)
			}
		}
	}
}

func TestReusableQualityRunsE2EAgainstEveryNativeReleaseArtifact(t *testing.T) {
	reusable := readWorkflow(t, "../.github/workflows/reusable-quality.yml")
	native, ok := reusable.Jobs["native"]
	if !ok {
		t.Fatal("reusable quality workflow is missing native artifact-quality job")
	}
	if native.Name != "artifact-quality-${{ matrix.goos }}-${{ matrix.goarch }}" {
		t.Fatalf("native name=%q", native.Name)
	}
	if !slices.Equal(native.Needs, []string{"resolve"}) {
		t.Fatalf("native needs=%v, want resolved contract and exact version", native.Needs)
	}
	if native.RunsOn != "${{ matrix.runner }}" || native.TimeoutMinutes != 90 {
		t.Fatalf("native runner=%q timeout=%d", native.RunsOn, native.TimeoutMinutes)
	}
	if native.Env["GOTOOLCHAIN"] != "local" {
		t.Fatalf("native GOTOOLCHAIN=%q, want local", native.Env["GOTOOLCHAIN"])
	}
	if native.Strategy.FailFast == nil || *native.Strategy.FailFast || native.Strategy.Matrix.Expression != "${{ fromJSON(needs.resolve.outputs.matrix) }}" {
		t.Fatalf("native strategy fail-fast=%v matrix=%q", native.Strategy.FailFast, native.Strategy.Matrix.Expression)
	}
	checkout := usesStep(t, native, "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0")
	if checkout.With["ref"] != "${{ inputs.source_sha }}" {
		t.Fatalf("native checkout ref=%q", checkout.With["ref"])
	}
	setup := usesStep(t, native, "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16")
	if setup.With["go-version-file"] != "go.mod" || setup.With["go-version"] != "" {
		t.Fatalf("native setup-go inputs=%v, want project Go baseline", setup.With)
	}
	nativeConfig := namedStep(t, native, "Run native platform config tests")
	if nativeConfig.Run != "go test -v ./internal/config -count=1" || nativeConfig.Env["CGO_ENABLED"] != "${{ matrix.cgo }}" {
		t.Fatalf("native platform config test=%+v", nativeConfig)
	}
	windowsConfigBurnIn := namedStep(t, native, "Burn in Windows config concurrency")
	if windowsConfigBurnIn.If != "matrix.goos == 'windows'" ||
		windowsConfigBurnIn.Run != "go test ./internal/config -run '^TestConcurrentSavePublishesOnlyCompleteConfigs$' -count=10" ||
		windowsConfigBurnIn.Env["CGO_ENABLED"] != "${{ matrix.cgo }}" {
		t.Fatalf("Windows config concurrency burn-in=%+v", windowsConfigBurnIn)
	}
	run := namedStep(t, native, "Run E2E and finalize reports")
	for _, snippet := range []string{
		"go run ./e2e/cmd/e2e-runner run",
		"--phase candidate",
		"--coverage-floor 60",
		"--command-timeout 3m",
		"--test-timeout 5m",
		`--artifact "dist/${{ matrix.archive }}"`,
		`--checksum "dist/${{ matrix.checksum }}"`,
	} {
		if !strings.Contains(run.Run, snippet) {
			t.Fatalf("e2e runner missing %q in %q", snippet, run.Run)
		}
	}
	for key, want := range map[string]string{
		"CGO_ENABLED":              "${{ matrix.cgo }}",
		"ENV_VAULT_E2E_GOOS":       "${{ matrix.goos }}",
		"ENV_VAULT_E2E_GOARCH":     "${{ matrix.goarch }}",
		"ENV_VAULT_E2E_VERSION":    "${{ needs.resolve.outputs.version }}",
		"ENV_VAULT_E2E_COMMIT_SHA": "${{ inputs.source_sha }}",
	} {
		if got := run.Env[key]; got != want {
			t.Fatalf("e2e runner env %s=%q, want %q", key, got, want)
		}
	}
	if strings.Contains(run.Run, "internal/cli.Run") {
		t.Fatal("E2E workflow must execute only the built public CLI binary")
	}

	upload := namedStep(t, native, "Upload E2E reports")
	if upload.If != "always()" || upload.Uses != "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a" {
		t.Fatalf("e2e upload if=%q uses=%q", upload.If, upload.Uses)
	}
	for key, want := range map[string]string{
		"name":              "env-vault-e2e-candidate-${{ matrix.id }}-attempt-${{ github.run_attempt }}",
		"path":              "reports/e2e/candidate",
		"if-no-files-found": "error",
		"retention-days":    "30",
	} {
		if got := upload.With[key]; got != want {
			t.Fatalf("e2e upload %s=%q, want %q", key, got, want)
		}
	}
}

func TestReleasePleaseSyntheticMergeRequiresThreeCommitFetchDepth(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local file URL shallow-clone proof is exercised by source-quality on Linux")
	}
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	run := func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Stdin = strings.NewReader(stdin)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=env-vault-test", "GIT_AUTHOR_EMAIL=test@example.invalid",
			"GIT_COMMITTER_NAME=env-vault-test", "GIT_COMMITTER_EMAIL=test@example.invalid",
		)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	run("", "init", "--bare", origin)
	tree := run("", "--git-dir", origin, "mktree")
	base := run("", "--git-dir", origin, "commit-tree", tree, "-m", "base")
	releaseHead := run("", "--git-dir", origin, "commit-tree", tree, "-p", base, "-m", "release head")
	merge := run("", "--git-dir", origin, "commit-tree", tree, "-p", base, "-p", releaseHead, "-m", "synthetic merge")
	run("", "--git-dir", origin, "update-ref", "refs/heads/main", merge)

	clone := func(depth string) string {
		t.Helper()
		destination := filepath.Join(root, "depth"+depth)
		run("", "clone", "--quiet", "--depth", depth, "--branch", "main", "file://"+filepath.ToSlash(origin), destination)
		return run("", "-C", destination, "rev-list", "--parents", "-n", "1", releaseHead)
	}
	if got := clone("2"); got != releaseHead {
		t.Fatalf("depth-2 release head ancestry=%q, want the head to be a shallow boundary", got)
	}
	if got := clone("3"); got != releaseHead+" "+base {
		t.Fatalf("depth-3 release head ancestry=%q, want exact head and parent", got)
	}
}

func TestReusableQualityPinsGo126CompatibleE2EReporter(t *testing.T) {
	native := readWorkflowJob(t, "../.github/workflows/reusable-quality.yml", "native")
	install := namedStep(t, native, "Install pinned E2E reporter")
	if install.Run != "go install gotest.tools/gotestsum@v1.13.0" {
		t.Fatalf("E2E reporter install=%q, want Go-1.26-compatible gotestsum v1.13.0", install.Run)
	}
	if !install.ContinueOnError {
		t.Fatal("reporter pre-install must allow the runner's pinned fallback to finalize failure reports")
	}
	allSteps := fmt.Sprintf("%v", native.Steps)
	for _, forbidden := range []string{"gotestsum@latest", "--rerun-fails"} {
		if strings.Contains(allSteps, forbidden) {
			t.Fatalf("E2E workflow contains forbidden reporter option %q", forbidden)
		}
	}
}

func TestReusableQualityE2EGateFailsClosed(t *testing.T) {
	gate := readWorkflowJob(t, "../.github/workflows/reusable-quality.yml", "e2e-gate")
	if gate.If != "always() && !cancelled()" || !slices.Equal(gate.Needs, []string{"resolve", "race", "license", "native"}) {
		t.Fatalf("aggregate gate if=%q needs=%v", gate.If, gate.Needs)
	}
	if gate.RunsOn != "ubuntu-latest" || gate.TimeoutMinutes != 10 || gate.Env["GOTOOLCHAIN"] != "local" {
		t.Fatalf("aggregate gate runner=%q timeout=%d env=%v", gate.RunsOn, gate.TimeoutMinutes, gate.Env)
	}
	require := namedStep(t, gate, "Require every quality stage")
	for _, key := range []string{"SOURCE_QUALITY_RESULT", "RACE_RESULT", "LICENSE_RESULT", "NATIVE_RESULT"} {
		if require.Env[key] == "" {
			t.Fatalf("aggregate gate does not inspect %s", key)
		}
	}
	if !strings.Contains(require.Run, `[[ "$result" == "success" ]]`) {
		t.Fatalf("aggregate upstream check does not fail closed: %q", require.Run)
	}
	checkout := usesStep(t, gate, "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0")
	if checkout.With["ref"] != "${{ inputs.source_sha }}" {
		t.Fatalf("aggregate checkout ref=%q", checkout.With["ref"])
	}
	download := namedStep(t, gate, "Download current-attempt E2E reports")
	for key, want := range map[string]string{
		"pattern":        "env-vault-e2e-candidate-*-attempt-${{ github.run_attempt }}",
		"path":           "reports-download",
		"merge-multiple": "true",
	} {
		if download.With[key] != want {
			t.Fatalf("aggregate report download %s=%q, want %q", key, download.With[key], want)
		}
	}
	validate := namedStep(t, gate, "Validate complete E2E report matrix")
	for _, snippet := range []string{
		"go run ./e2e/cmd/e2e-runner validate-matrix",
		"--reports reports-download",
		"--phase candidate",
		`--expected-commit "${{ inputs.source_sha }}"`,
		`--expected-run-id "${{ github.run_id }}"`,
		`--expected-run-attempt "${{ github.run_attempt }}"`,
		`--expected-reporter "v1.13.0"`,
	} {
		if !strings.Contains(validate.Run, snippet) {
			t.Fatalf("complete matrix validation missing %q", snippet)
		}
	}
	verify := namedStep(t, gate, "Verify validated matrix against durable baseline")
	for _, snippet := range []string{
		"go run ./cmd/e2e-baseline verify",
		"--baseline docs/e2e-baseline.json",
		"--proof reports-download/matrix-validation.json",
		"--output baseline-verification",
		"--phase candidate",
		`--expected-commit "${{ inputs.source_sha }}"`,
		`--expected-run-id "${{ github.run_id }}"`,
		`--expected-run-attempt "${{ github.run_attempt }}"`,
		`--expected-repository "${{ github.repository }}"`,
	} {
		if !strings.Contains(verify.Run, snippet) {
			t.Fatalf("durable baseline verification missing %q", snippet)
		}
	}
	if strings.Contains(verify.Run, "--reports") || strings.Contains(verify.Run, "--matrix-validation") {
		t.Fatalf("baseline verifier must consume only the sealed proof, not raw reports: %q", verify.Run)
	}
	upload := namedStep(t, gate, "Upload durable baseline verification")
	if upload.If != "always()" || upload.With["if-no-files-found"] != "error" || upload.With["retention-days"] != "30" {
		t.Fatalf("durable baseline evidence upload=%+v", upload)
	}
	if upload.With["name"] != "env-vault-e2e-baseline-verification-attempt-${{ github.run_attempt }}" ||
		!strings.Contains(upload.With["path"], "matrix-validation.json") ||
		!strings.Contains(upload.With["path"], "matrix-validation.md") ||
		!strings.Contains(upload.With["path"], "baseline-verification.json") ||
		!strings.Contains(upload.With["path"], "baseline-verification.md") {
		t.Fatalf("durable baseline evidence identity=%v", upload.With)
	}
}

func TestReusableQualityUsesCheckedInDurableBaseline(t *testing.T) {
	workflowData, err := os.ReadFile("../.github/workflows/reusable-quality.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(workflowData)
	for _, forbidden := range []string{
		"29441160687",
		"7a044bdbf73aa592016bbb3a02d81f314f08fe63",
		"baseline-download",
		"./cmd/e2e-compare",
		"e2e-compare:",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("reusable quality still contains migration-only comparator state %q", forbidden)
		}
	}
	if strings.Count(text, "go run ./cmd/e2e-baseline verify") != 1 {
		t.Fatalf("durable baseline verifier must run exactly once")
	}
	if strings.Count(text, "go run ./e2e/cmd/e2e-runner validate-matrix") != 1 {
		t.Fatalf("candidate report matrix must be regenerated and validated exactly once")
	}
}

func TestReusableQualityRecordsEveryPlatformAndPromotesOnlyExactPushReleaseAttempts(t *testing.T) {
	reusable := readWorkflow(t, "../.github/workflows/reusable-quality.yml")
	native := reusable.Jobs["native"]
	record := namedStep(t, native, "Verify checksum and literal version; record platform evidence")
	wantIf := "needs.resolve.outputs.release_candidate == 'true' && inputs.event_name == 'push'"
	if record.If != "" {
		t.Fatalf("platform evidence must cover release and source-bound CI builds, if=%q", record.If)
	}
	for _, snippet := range []string{
		"go run ./cmd/release-promotion record-platform",
		`--platform "$PLATFORM_ID"`,
		`--source-sha "$SOURCE_SHA"`,
		`--version "$VERSION"`,
		`--run-id "$GITHUB_RUN_ID"`,
		`--run-attempt "$GITHUB_RUN_ATTEMPT"`,
		`--archive "dist/$ARCHIVE"`,
		`--checksum "dist/$CHECKSUM"`,
		`--binary "dist/${name}/${BINARY}"`,
		"--reports reports/e2e/candidate",
		`--artifact-name "env-vault-release-${PLATFORM_ID}-attempt-${GITHUB_RUN_ATTEMPT}"`,
		`--e2e-artifact-name "env-vault-e2e-candidate-${PLATFORM_ID}-attempt-${GITHUB_RUN_ATTEMPT}"`,
	} {
		if !strings.Contains(record.Run, snippet) {
			t.Fatalf("platform promotion record missing %q", snippet)
		}
	}
	evidenceUpload := namedStep(t, native, "Upload platform quality evidence")
	if evidenceUpload.If != "" || evidenceUpload.With["name"] != "env-vault-promotion-platform-${{ matrix.id }}-attempt-${{ github.run_attempt }}" || evidenceUpload.With["if-no-files-found"] != "error" {
		t.Fatalf("platform promotion evidence upload=%+v", evidenceUpload)
	}

	gate := reusable.Jobs["e2e-gate"]
	download := namedStep(t, gate, "Download current-attempt platform promotion evidence")
	if download.If != wantIf || download.With["pattern"] != "env-vault-promotion-platform-*-attempt-${{ github.run_attempt }}" || download.With["merge-multiple"] != "true" {
		t.Fatalf("aggregate promotion evidence download=%+v", download)
	}
	assemble := namedStep(t, gate, "Assemble exact promotion manifest")
	if assemble.If != wantIf {
		t.Fatalf("promotion assemble if=%q", assemble.If)
	}
	for _, snippet := range []string{
		"go run ./cmd/release-promotion assemble",
		`[[ ${#evidence[@]} -eq 5 ]]`,
		`created_at="$(git show -s --format=%cI "$SOURCE_SHA")"`,
		`--run-id "$GITHUB_RUN_ID"`,
		`--run-attempt "$GITHUB_RUN_ATTEMPT"`,
		"--event push",
		"--output promotion/promotion-manifest.json",
	} {
		if !strings.Contains(assemble.Run, snippet) {
			t.Fatalf("promotion assembly missing %q", snippet)
		}
	}
	manifestUpload := namedStep(t, gate, "Upload exact promotion manifest")
	if manifestUpload.If != wantIf || manifestUpload.With["name"] != "env-vault-promotion-${{ inputs.source_sha }}-attempt-${{ github.run_attempt }}" || manifestUpload.With["path"] != "promotion/promotion-manifest.json" || manifestUpload.With["if-no-files-found"] != "error" {
		t.Fatalf("promotion manifest upload=%+v", manifestUpload)
	}
}

func TestGeneratedHomebrewFormulaPreservesDistributionContract(t *testing.T) {
	homebrew := readWorkflowJob(t, "../.github/workflows/build-binaries.yml", "homebrew")
	generate := namedStep(t, homebrew, "Generate formula")
	if !strings.Contains(generate.Run, "scripts/release/generate-homebrew-formula.sh") {
		t.Fatal("workflow must use the tested Homebrew formula generator")
	}
	quality := readWorkflow(t, "../.github/workflows/reusable-quality.yml")
	archiveBuild := namedStep(t, quality.Jobs["native"], "Build native release artifact")
	archiveDocs := `cp README.md LICENSE THIRD_PARTY_NOTICES.md "dist/${name}/"`
	if strings.Count(archiveBuild.Run, archiveDocs) != 1 {
		t.Fatalf("release build must archive all formula-installed documentation exactly once; build step:\n%s", archiveBuild.Run)
	}
	assetDir := t.TempDir()
	archiveDigests := make(map[string]string)
	for _, archive := range []string{
		"env-vault-darwin-arm64.tar.gz",
		"env-vault-darwin-amd64.tar.gz",
		"env-vault-linux-arm64.tar.gz",
		"env-vault-linux-amd64.tar.gz",
	} {
		contents := []byte("fixture:" + archive)
		if err := os.WriteFile(filepath.Join(assetDir, archive), contents, 0o644); err != nil {
			t.Fatalf("write archive fixture: %v", err)
		}
		digest := sha256.Sum256(contents)
		archiveDigests[archive] = fmt.Sprintf("%x", digest)
		checksum := fmt.Sprintf("%x  %s\n", digest, archive)
		if err := os.WriteFile(filepath.Join(assetDir, archive+".sha256"), []byte(checksum), 0o644); err != nil {
			t.Fatalf("write checksum fixture: %v", err)
		}
	}
	formulaPath := filepath.Join(t.TempDir(), "env-vault.rb")
	cmd := exec.Command("bash", "../scripts/release/generate-homebrew-formula.sh", "v1.2.3", assetDir, formulaPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate formula: %v\n%s", err, output)
	}
	verify := exec.Command("bash", "../scripts/release/verify-homebrew-formula.sh", "v1.2.3", assetDir, formulaPath)
	if output, err := verify.CombinedOutput(); err != nil {
		t.Fatalf("verify generated formula: %v\n%s", err, output)
	}
	data, err := os.ReadFile(formulaPath)
	if err != nil {
		t.Fatalf("read generated formula: %v", err)
	}
	formula := string(data)
	want := `assert_equal "v#{version}", shell_output("#{bin}/env-vault --version").strip`
	if !strings.Contains(formula, want) {
		t.Fatalf("generated formula test missing exact assertion %q", want)
	}
	if !strings.Contains(formula, `version "1.2.3"`) {
		t.Fatalf("generated formula has wrong version: %s", formula)
	}
	for snippet, wantCount := range map[string]int{
		"  on_macos do\n    depends_on macos: :sequoia": 1,
		"  on_linux do":   1,
		"    on_arm do":   2,
		"    on_intel do": 2,
		`    doc.install %w[README.md LICENSE THIRD_PARTY_NOTICES.md]`: 1,
	} {
		if got := strings.Count(formula, snippet); got != wantCount {
			t.Fatalf("generated formula occurrence count for %q=%d, want %d\n%s", snippet, got, wantCount, formula)
		}
	}
	macStart := strings.Index(formula, "  on_macos do\n")
	linuxStart := strings.Index(formula, "  on_linux do\n")
	installStart := strings.Index(formula, "  def install\n")
	if macStart < 0 || linuxStart <= macStart || installStart <= linuxStart {
		t.Fatalf("generated formula has invalid platform block ordering:\n%s", formula)
	}
	sections := map[string]string{
		"darwin": formula[macStart:linuxStart],
		"linux":  formula[linuxStart:installStart],
	}
	for _, target := range []struct {
		archive  string
		platform string
		selector string
	}{
		{archive: "env-vault-darwin-arm64.tar.gz", platform: "darwin", selector: "on_arm"},
		{archive: "env-vault-darwin-amd64.tar.gz", platform: "darwin", selector: "on_intel"},
		{archive: "env-vault-linux-arm64.tar.gz", platform: "linux", selector: "on_arm"},
		{archive: "env-vault-linux-amd64.tar.gz", platform: "linux", selector: "on_intel"},
	} {
		wantBlock := fmt.Sprintf(
			"    %s do\n      url \"https://github.com/ildarbinanas-design/env-vault/releases/download/v1.2.3/%s\"\n      sha256 \"%s\"\n    end",
			target.selector,
			target.archive,
			archiveDigests[target.archive],
		)
		if strings.Count(sections[target.platform], wantBlock) != 1 {
			t.Fatalf("generated formula must contain exact %s/%s URL/checksum block %q\n%s", target.platform, target.selector, wantBlock, formula)
		}
	}
	if strings.Contains(formula, "Hardware::CPU") {
		t.Fatal("generated formula must use Homebrew on_arm/on_intel blocks, not Hardware::CPU branching")
	}
	if strings.Contains(formula, "assert_match") {
		t.Fatal("generated formula must not accept a version substring")
	}
	if strings.Contains(formula, "link_overwrite") {
		t.Fatal("generated formula must not overwrite unmanaged files")
	}
}

func TestHomebrewPublishesThroughScopedAppPRAndAwaitsExactCI(t *testing.T) {
	homebrew := readWorkflowJob(t, "../.github/workflows/build-binaries.yml", "homebrew")
	if homebrew.Environment != "release" || homebrew.TimeoutMinutes != 55 {
		t.Fatalf("homebrew environment=%q timeout=%d", homebrew.Environment, homebrew.TimeoutMinutes)
	}
	if len(homebrew.Permissions) != 2 || homebrew.Permissions["contents"] != "read" || homebrew.Permissions["attestations"] != "read" {
		t.Fatalf("homebrew permissions=%v", homebrew.Permissions)
	}
	attestations := namedStep(t, homebrew, "Require exact-source release attestations before tap mutation")
	for _, snippet := range []string{"artifact-attestation-state.sh", `"$SOURCE_SHA"`, `complete|complete`} {
		if !strings.Contains(attestations.Run, snippet) {
			t.Fatalf("Homebrew attestation precondition missing %q", snippet)
		}
	}
	for output, want := range map[string]string{
		"publication_state": "${{ steps.tap-result.outputs.publication_state }}",
		"pr_number":         "${{ steps.tap-result.outputs.pr_number }}",
		"pr_url":            "${{ steps.tap-result.outputs.pr_url }}",
		"tap_sha":           "${{ steps.tap-result.outputs.tap_sha }}",
		"tap_ci_url":        "${{ steps.tap-push-ci.outputs.run_url }}",
	} {
		if got := homebrew.Outputs[output]; got != want {
			t.Fatalf("homebrew output %s=%q, want %q", output, got, want)
		}
	}

	token := namedStep(t, homebrew, "Mint scoped Homebrew App token")
	if token.Uses != createAppTokenAction {
		t.Fatalf("Homebrew token action=%q", token.Uses)
	}
	for key, want := range map[string]string{
		"client-id":                "${{ vars.TAP_APP_CLIENT_ID }}",
		"private-key":              "${{ secrets.TAP_APP_PRIVATE_KEY }}",
		"owner":                    "${{ needs.metadata.outputs.tap_repository_owner }}",
		"repositories":             "${{ needs.metadata.outputs.tap_repository_name }}",
		"permission-actions":       "read",
		"permission-contents":      "write",
		"permission-pull-requests": "write",
	} {
		if got := token.With[key]; got != want {
			t.Fatalf("Homebrew token input %s=%q, want %q", key, got, want)
		}
	}
	if _, ok := token.With["skip-token-revoke"]; ok {
		t.Fatal("Homebrew App token must be revoked by the action post-step")
	}
	identity := namedStep(t, homebrew, "Require exact Homebrew App mutation identity and scope")
	for _, snippet := range []string{"release/contract.v1.json", `select(.id == "homebrew_tap")`, `"$ACTUAL_APP_SLUG" == "$expected_slug"`, "installation/repositories", `"${#repositories[@]}" == "1"`, `"$expected_repository"`} {
		if !strings.Contains(identity.Run, snippet) {
			t.Fatalf("Homebrew App inline precondition missing %q", snippet)
		}
	}
	if identity.Env["ACTUAL_APP_SLUG"] != "${{ steps.tap-token.outputs.app-slug }}" || identity.Env["GH_TOKEN"] != "${{ steps.tap-token.outputs.token }}" {
		t.Fatalf("Homebrew App identity env=%v", identity.Env)
	}

	publish := namedStep(t, homebrew, "Create or reuse Homebrew pull request")
	for _, snippet := range []string{"publish-homebrew-pr.sh", "tap-out/env-vault.rb", `"$TAP_REPOSITORY"`, "GITHUB_OUTPUT"} {
		if !strings.Contains(publish.Run, snippet) {
			t.Fatalf("Homebrew PR publication missing %q", snippet)
		}
	}
	if publish.Env["GH_TOKEN"] != "${{ steps.tap-token.outputs.token }}" || publish.Env["SOURCE_SHA"] != "${{ needs.metadata.outputs.source_sha }}" || publish.Env["TAP_REPOSITORY"] != "${{ needs.metadata.outputs.tap_repository }}" {
		t.Fatalf("Homebrew PR publication env=%v", publish.Env)
	}

	prCI := namedStep(t, homebrew, "Wait for exact Homebrew pull request CI")
	if prCI.If != "steps.publish-tap-pr.outputs.state == 'OPEN' || steps.publish-tap-pr.outputs.state == 'MERGED'" {
		t.Fatalf("Homebrew PR CI if=%q", prCI.If)
	}
	for _, snippet := range []string{"wait-tap-ci.sh", `"$TAP_CI_WORKFLOW"`, `"$TAP_REPOSITORY"`, `"$HEAD_SHA"`, "pull_request", "GITHUB_OUTPUT"} {
		if !strings.Contains(prCI.Run, snippet) {
			t.Fatalf("Homebrew PR CI missing %q", snippet)
		}
	}
	if prCI.Env["TAP_CI_WORKFLOW"] != "${{ needs.metadata.outputs.tap_ci_workflow }}" || prCI.Env["TAP_REPOSITORY"] != "${{ needs.metadata.outputs.tap_repository }}" {
		t.Fatalf("Homebrew PR CI does not use contract-derived identities: env=%v", prCI.Env)
	}

	merge := namedStep(t, homebrew, "Merge exact Homebrew pull request head")
	for _, snippet := range []string{"merge-homebrew-pr.sh", `"$PR_NUMBER"`, `"$HEAD_SHA"`, "GITHUB_OUTPUT"} {
		if !strings.Contains(merge.Run, snippet) {
			t.Fatalf("Homebrew merge missing %q", snippet)
		}
	}
	if merge.Env["TAP_REPOSITORY"] != "${{ needs.metadata.outputs.tap_repository }}" || !strings.Contains(merge.Run, `"$TAP_REPOSITORY"`) {
		t.Fatalf("Homebrew merge does not use the contract-derived repository: env=%v run=%q", merge.Env, merge.Run)
	}

	pushCI := namedStep(t, homebrew, "Wait for exact Homebrew default-branch CI")
	for _, snippet := range []string{"wait-tap-ci.sh", `"$TAP_CI_WORKFLOW"`, `"$TAP_REPOSITORY"`, `"$TAP_SHA"`, "push", "GITHUB_OUTPUT"} {
		if !strings.Contains(pushCI.Run, snippet) {
			t.Fatalf("Homebrew push CI missing %q", snippet)
		}
	}
	if pushCI.Env["TAP_CI_WORKFLOW"] != "${{ needs.metadata.outputs.tap_ci_workflow }}" || pushCI.Env["TAP_REPOSITORY"] != "${{ needs.metadata.outputs.tap_repository }}" {
		t.Fatalf("Homebrew push CI does not use contract-derived identities: env=%v", pushCI.Env)
	}

	allSteps := fmt.Sprintf("%v", homebrew.Steps)
	for _, forbidden := range []string{"TAP_DEPLOY_KEY", "HEAD:main", "tap_deploy_key", "ssh-keyscan", "--admin", "--force"} {
		if strings.Contains(allSteps, forbidden) {
			t.Fatalf("Homebrew workflow retains forbidden direct-push/bypass behavior %q", forbidden)
		}
	}
}

func TestRepairBuildsUseTheResolvedTagCommit(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/build-binaries.yml")
	quality := wf.Jobs["quality"]
	if quality.If != "needs.metadata.outputs.run_build == 'true'" {
		t.Fatalf("quality if=%q", quality.If)
	}
	if quality.With["source_sha"] != "${{ needs.metadata.outputs.source_sha }}" {
		t.Fatalf("quality source_sha=%q", quality.With["source_sha"])
	}

	reusable := readWorkflow(t, "../.github/workflows/reusable-quality.yml")
	for _, jobName := range []string{"resolve", "race", "license", "native", "e2e-gate"} {
		job := reusable.Jobs[jobName]
		checkout := usesStep(t, job, "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0")
		if checkout.With["ref"] != "${{ inputs.source_sha }}" {
			t.Fatalf("%s checkout ref=%q", jobName, checkout.With["ref"])
		}
	}
}

func TestReleaseReusesTagAndReleaseAndReconcilesAssets(t *testing.T) {
	release := readWorkflowJob(t, "../.github/workflows/build-binaries.yml", "release")
	create := namedStep(t, release, "Create GitHub Release")
	for _, snippet := range []string{
		"get-release-state.sh",
		`release_status" == "4`,
		"already exists; reconciling assets",
		"gh release create",
		"--verify-tag",
	} {
		if !strings.Contains(create.Run, snippet) {
			t.Fatalf("release reuse step missing %q", snippet)
		}
	}
	reconcile := namedStep(t, release, "Reconcile release assets")
	if reconcile.Run != `scripts/release/reconcile-release-assets.sh "$VERSION" dist` {
		t.Fatalf("reconcile run=%q", reconcile.Run)
	}

	for _, path := range []string{
		"../scripts/release/reconcile-release-assets.sh",
		"../scripts/release/download-release-assets.sh",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if strings.Contains(string(data), "--clobber") {
			t.Fatalf("%s must never overwrite an existing release asset", path)
		}
	}
	reconcileData, err := os.ReadFile("../scripts/release/reconcile-release-assets.sh")
	if err != nil {
		t.Fatalf("read reconcile script: %v", err)
	}
	reconcileScript := string(reconcileData)
	for _, snippet := range []string{
		`archive_count" == "1" && "$checksum_count" == "1`,
		`release_verify_checksum_pair "$local_dir/$archive" "$local_dir/$checksum"`,
		`cmp -s -- "$local_dir/$archive" "$remote_dir/$archive"`,
		`cmp -s -- "$local_dir/$asset" "$verified_dir/$asset"`,
		`gh release upload "$version" "$local_dir/$archive"`,
		"download-release-assets.sh",
	} {
		if !strings.Contains(reconcileScript, snippet) {
			t.Fatalf("asset reconciliation missing critical case %q", snippet)
		}
	}
	if strings.Contains(reconcileScript, "release_write_checksum_pair") {
		t.Fatal("reconciliation must never derive a checksum from remote bytes")
	}
}

func TestRepairHealthValidatesTagReleaseAssetsAndFormula(t *testing.T) {
	health := readWorkflowJob(t, "../.github/workflows/build-binaries.yml", "health")
	for _, need := range []string{"metadata", "release", "supply_chain", "homebrew"} {
		if !slices.Contains(health.Needs, need) {
			t.Fatalf("health needs=%v, missing %q", health.Needs, need)
		}
	}
	for _, snippet := range []string{"always()", "!cancelled()", "needs.metadata.result == 'success'", "repair == 'health'", "needs.release.result == 'skipped'", "needs.supply_chain.result == 'success'", "needs.supply_chain.result == 'skipped'", "needs.homebrew.result == 'success'", "needs.homebrew.result == 'skipped'"} {
		if !strings.Contains(health.If, snippet) {
			t.Fatalf("health if=%q, missing %q", health.If, snippet)
		}
	}
	verify := namedStep(t, health, "Verify release and Homebrew health")
	for _, snippet := range []string{
		"resolve-tag-sha.sh",
		"get-release-state.sh",
		"download-release-assets.sh",
		"verify-artifact-attestations.sh",
		"verify-homebrew-formula.sh",
		"wait-tap-ci.sh",
		`"$TAP_CI_WORKFLOW"`,
		"push",
		"GITHUB_STEP_SUMMARY",
		"/releases/tag/${VERSION}",
		"/${TAP_REPOSITORY}/commit/${tap_sha}",
		"/actions/runs/${GITHUB_RUN_ID}",
		"/${GITHUB_REPOSITORY}/attestations",
		"Source commit",
		"Tap pull request",
		"Exact tap CI",
		"including exact tap CI",
	} {
		if !strings.Contains(verify.Run, snippet) {
			t.Fatalf("health verification missing %q", snippet)
		}
	}
	if verify.Env["TAP_REPOSITORY"] != "${{ needs.metadata.outputs.tap_repository }}" || verify.Env["TAP_CI_WORKFLOW"] != "${{ needs.metadata.outputs.tap_ci_workflow }}" || strings.Contains(verify.Run, "ildarbinanas-design/homebrew-tap") {
		t.Fatalf("health does not use contract-derived tap identities: env=%v run=%q", verify.Env, verify.Run)
	}
	if health.Environment != "" {
		t.Fatalf("read-only health job unexpectedly uses environment %q", health.Environment)
	}
	if strings.Contains(verify.Run, "Tap CI is not awaited") || strings.Contains(verify.Run, "requires manual verification") {
		t.Fatal("health still describes exact tap CI as a manual check")
	}
	if verify.Env["PUBLISHED_TAP_SHA"] != "${{ needs.homebrew.outputs.tap_sha }}" || verify.Env["PUBLISHED_TAP_CI_URL"] != "${{ needs.homebrew.outputs.tap_ci_url }}" {
		t.Fatalf("health does not consume exact Homebrew outputs: %v", verify.Env)
	}
}

func TestReleasePublishesIdempotentSupplyChainEvidence(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/build-binaries.yml")
	supply := wf.Jobs["supply_chain"]
	for _, need := range []string{"metadata", "release"} {
		if !slices.Contains(supply.Needs, need) {
			t.Fatalf("supply_chain needs=%v, missing %q", supply.Needs, need)
		}
	}
	for _, snippet := range []string{
		"always()",
		"!cancelled()",
		"publish == 'true'",
		"repair == 'none'",
		"repair == 'release-assets'",
		"needs.release.result == 'success'",
	} {
		if !strings.Contains(supply.If, snippet) {
			t.Fatalf("supply_chain if=%q, missing %q", supply.If, snippet)
		}
	}
	wantPermissions := map[string]string{
		"contents":          "read",
		"id-token":          "write",
		"attestations":      "write",
		"artifact-metadata": "write",
	}
	if len(supply.Permissions) != len(wantPermissions) {
		t.Fatalf("supply_chain permissions=%v", supply.Permissions)
	}
	for permission, want := range wantPermissions {
		if got := supply.Permissions[permission]; got != want {
			t.Fatalf("supply_chain permission %s=%q, want %q", permission, got, want)
		}
	}

	download := namedStep(t, supply, "Download canonical release assets")
	if download.Run != `scripts/release/download-release-assets.sh "$VERSION" release-dist` {
		t.Fatalf("supply-chain download=%q", download.Run)
	}
	state := namedStep(t, supply, "Detect complete existing attestations")
	for _, snippet := range []string{
		"artifact-attestation-state.sh",
		"$GITHUB_REPOSITORY/.github/workflows/build-binaries.yml",
		"$SOURCE_SHA",
		"complete|missing",
		"create_provenance",
		"create_sbom",
		"$RUN_SHA\" != \"$SOURCE_SHA",
		"rerun the original release workflow",
		"GITHUB_OUTPUT",
	} {
		if !strings.Contains(state.Run, snippet) {
			t.Fatalf("attestation state step missing %q", snippet)
		}
	}
	if state.Env["RUN_SHA"] != "${{ github.sha }}" || state.Env["SOURCE_SHA"] != "${{ needs.metadata.outputs.source_sha }}" {
		t.Fatalf("attestation state SHA inputs=%v", state.Env)
	}

	setupGo := namedStep(t, supply, "Set up Go for safe extraction")
	if setupGo.Uses != "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16" || setupGo.With["go-version-file"] != "go.mod" {
		t.Fatalf("safe extraction Go setup uses=%q with=%v", setupGo.Uses, setupGo.With)
	}
	extract := namedStep(t, supply, "Safely extract packages for SBOM")
	if extract.Run != "go run ./cmd/release-extract --input-dir release-dist --output-dir sbom-root" {
		t.Fatalf("safe SBOM extraction command=%q", extract.Run)
	}
	for _, step := range []workflowStep{setupGo, extract} {
		if step.If != "steps.attestation-state.outputs.create_sbom == 'true'" {
			t.Fatalf("safe extraction step if=%q", step.If)
		}
	}

	sbom := namedStep(t, supply, "Generate SPDX SBOM")
	if sbom.Uses != "anchore/sbom-action@e22c389904149dbc22b58101806040fa8d37a610" {
		t.Fatalf("SBOM action=%q", sbom.Uses)
	}
	for key, want := range map[string]string{
		"path":                  "sbom-root",
		"format":                "spdx-json",
		"output-file":           "env-vault-sbom.spdx.json",
		"syft-version":          "v1.44.0",
		"upload-artifact":       "false",
		"upload-release-assets": "false",
	} {
		if got := sbom.With[key]; got != want {
			t.Fatalf("SBOM input %s=%q, want %q", key, got, want)
		}
	}
	upload := namedStep(t, supply, "Upload SPDX SBOM workflow artifact")
	if upload.Uses != "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a" || upload.With["retention-days"] != "14" {
		t.Fatalf("SBOM artifact upload uses=%q with=%v", upload.Uses, upload.With)
	}
	if !strings.Contains(upload.With["name"], "github.run_attempt") {
		t.Fatalf("SBOM artifact name is not retry-safe: %q", upload.With["name"])
	}

	subjects := namedStep(t, supply, "Resolve attestation subjects from release contract")
	if !strings.Contains(subjects.Run, `.platforms[].archive | "release-dist/" + .`) || !strings.Contains(subjects.Run, `>> "$GITHUB_OUTPUT"`) {
		t.Fatalf("attestation subjects are not derived from the release contract: %q", subjects.Run)
	}
	for _, stepName := range []string{"Attest build provenance", "Attest SPDX SBOM"} {
		step := namedStep(t, supply, stepName)
		if step.Uses != "actions/attest@a1948c3f048ba23858d222213b7c278aabede763" {
			t.Fatalf("%s uses=%q", stepName, step.Uses)
		}
		if got := step.With["subject-path"]; got != "${{ steps.attestation-subjects.outputs.paths }}" {
			t.Fatalf("%s subjects=%q, want contract-derived output", stepName, got)
		}
	}
	provenanceAttestation := namedStep(t, supply, "Attest build provenance")
	if provenanceAttestation.If != "steps.attestation-state.outputs.create_provenance == 'true'" {
		t.Fatalf("provenance attestation if=%q", provenanceAttestation.If)
	}
	if sbomAttestation := namedStep(t, supply, "Attest SPDX SBOM"); sbomAttestation.With["sbom-path"] != "env-vault-sbom.spdx.json" {
		t.Fatalf("SBOM attestation inputs=%v", sbomAttestation.With)
	} else if sbomAttestation.If != "steps.attestation-state.outputs.create_sbom == 'true'" {
		t.Fatalf("SBOM attestation if=%q", sbomAttestation.If)
	}

	homebrew := wf.Jobs["homebrew"]
	if !slices.Contains(homebrew.Needs, "supply_chain") {
		t.Fatalf("homebrew needs=%v, missing supply_chain", homebrew.Needs)
	}
	for _, snippet := range []string{"needs.supply_chain.result == 'success'", "needs.supply_chain.result == 'skipped'"} {
		if !strings.Contains(homebrew.If, snippet) {
			t.Fatalf("homebrew supply-chain gate missing %q", snippet)
		}
	}

	health := wf.Jobs["health"]
	if health.Permissions["contents"] != "read" || health.Permissions["attestations"] != "read" {
		t.Fatalf("health permissions=%v", health.Permissions)
	}
}

func TestReleaseDarwinBuildUsesContractResolvedNativeMatrix(t *testing.T) {
	reusable := readWorkflow(t, "../.github/workflows/reusable-quality.yml")
	native := reusable.Jobs["native"]
	if native.RunsOn != "${{ matrix.runner }}" || native.Strategy.Matrix.Expression != "${{ fromJSON(needs.resolve.outputs.matrix) }}" {
		t.Fatalf("native job does not consume contract matrix: runs-on=%q matrix=%q", native.RunsOn, native.Strategy.Matrix.Expression)
	}
	contract := namedStep(t, reusable.Jobs["resolve"], "Validate release contract and resolve native matrix")
	if !strings.Contains(contract.Run, "go run ./cmd/release-contract matrix --json") {
		t.Fatalf("native matrix is not sourced from release contract: %q", contract.Run)
	}
	build := namedStep(t, native, "Build native release artifact")
	if build.Env["CGO_ENABLED"] != "${{ matrix.cgo }}" || build.Env["GOOS"] != "${{ matrix.goos }}" || build.Env["GOARCH"] != "${{ matrix.goarch }}" {
		t.Fatalf("native build target env=%v", build.Env)
	}
}

func TestReleaseArtifactsRunOnNativeRunnersAndReportExactVersion(t *testing.T) {
	reusable := readWorkflow(t, "../.github/workflows/reusable-quality.yml")
	if len(reusable.Jobs) != 5 {
		t.Fatalf("reusable quality has %d logical jobs, want resolve/race/license/native/aggregate", len(reusable.Jobs))
	}
	for _, removed := range []string{"module", "test", "smoke", "build", "native-smoke", "e2e", "e2e-compare"} {
		if _, ok := reusable.Jobs[removed]; ok {
			t.Fatalf("reusable quality retains duplicate job %q", removed)
		}
	}

	native := reusable.Jobs["native"]
	upload := namedStep(t, native, "Upload native release artifact")
	if upload.Uses != "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a" {
		t.Fatalf("native artifact upload uses=%q", upload.Uses)
	}
	if upload.With["name"] != "env-vault-release-${{ matrix.id }}-attempt-${{ github.run_attempt }}" {
		t.Fatalf("native artifact name=%q, want attempt-qualified identity", upload.With["name"])
	}
	if got, want := strings.Split(strings.TrimSpace(upload.With["path"]), "\n"), []string{"dist/${{ matrix.archive }}", "dist/${{ matrix.checksum }}"}; !slices.Equal(got, want) {
		t.Fatalf("native artifact paths=%v, want only archive and checksum %v", got, want)
	}
	if upload.With["if-no-files-found"] != "error" {
		t.Fatalf("native artifact upload does not fail closed: %v", upload.With)
	}

	wantVersion := "${{ needs.resolve.outputs.version }}"
	record := namedStep(t, native, "Verify checksum and literal version; record platform evidence")
	if record.If != "" || record.Env["VERSION"] != wantVersion {
		t.Fatalf("cross-platform exact-version gate if=%q VERSION=%q", record.If, record.Env["VERSION"])
	}
	for _, snippet := range []string{
		"go run ./cmd/release-promotion record-platform",
		`--version "$VERSION"`,
		`--archive "dist/$ARCHIVE"`,
		`--checksum "dist/$CHECKSUM"`,
		`--binary "dist/${name}/${BINARY}"`,
	} {
		if !strings.Contains(record.Run, snippet) {
			t.Fatalf("cross-platform exact-version gate missing %q", snippet)
		}
	}

	release := readWorkflowJob(t, "../.github/workflows/build-binaries.yml", "release")
	if !slices.Contains(release.Needs, "quality") || !slices.Contains(release.Needs, "promotion") ||
		!strings.Contains(release.If, "needs.quality.result == 'skipped'") ||
		!strings.Contains(release.If, "needs.promotion.result == 'success'") {
		t.Fatalf("release is not gated by exact promotion: needs=%v if=%q", release.Needs, release.If)
	}
	download := namedStep(t, release, "Download publisher-local verified promotion bundle")
	if download.With["path"] != "dist" || download.With["merge-multiple"] != "true" {
		t.Fatalf("release artifact download with=%v", download.With)
	}
}

func readWorkflowJob(t *testing.T, path, jobName string) workflowJob {
	t.Helper()
	wf := readWorkflow(t, path)
	job, ok := wf.Jobs[jobName]
	if !ok {
		t.Fatalf("%s missing job %q", path, jobName)
	}
	return job
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

func usesStep(t *testing.T, job workflowJob, uses string) workflowStep {
	t.Helper()
	for _, step := range job.Steps {
		if step.Uses == uses {
			return step
		}
	}
	t.Fatalf("uses step %q not found", uses)
	return workflowStep{}
}

func runStep(t *testing.T, job workflowJob, command string) workflowStep {
	t.Helper()
	for _, step := range job.Steps {
		if step.Run == command {
			return step
		}
	}
	t.Fatalf("run step %q not found", command)
	return workflowStep{}
}
