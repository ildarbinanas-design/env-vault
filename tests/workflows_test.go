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
	Include []workflowTarget `yaml:"include"`
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
	if verify.Env["GH_TOKEN"] != "${{ steps.app-token.outputs.token }}" {
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
		"repositories":              "env-vault",
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
	if identity.If != "steps.classify.outputs.publish == 'true' || steps.current.outputs.current == 'true'" || identity.Env["ACTUAL_APP_SLUG"] != "${{ steps.release-token.outputs.app-slug }}" || !strings.Contains(identity.Run, `"$ACTUAL_APP_SLUG" != "env-vault-release-planning"`) {
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
		"refs/heads/${DEFAULT_BRANCH}",
		"vMAJOR.MINOR.PATCH",
		"GITHUB_OUTPUT",
		"publish=false",
		"repair mode requires an explicit version",
		"scripts/release/resolve-tag-sha.sh",
		"publishing requires an existing exact tag created by release planning",
		`semver-compare.sh "${version#v}" "0.0.7"`,
		"get-release-state.sh",
		"legacy repair requires an existing stable GitHub Release",
		`compare/${source_sha}...${main_sha}`,
		`legacy_release" != "true`,
		"run_release",
		"run_homebrew",
		"source_sha",
		"classify-release-commit.sh",
		`verify-release-authorization.sh "$version" "$source_sha" tagged`,
		"for attempt in {1..12}",
		"sleep 5",
		`"$authorized" != "true"`,
		"tag source is not a deterministic release commit",
	} {
		if !strings.Contains(resolve.Run, snippet) {
			t.Fatalf("metadata resolution missing %q", snippet)
		}
	}
	if resolve.Env["RELEASE_APP_SLUG"] != "env-vault-release-planning" {
		t.Fatalf("release metadata App slug=%q", resolve.Env["RELEASE_APP_SLUG"])
	}
	if !strings.Contains(resolve.Run, "^v(0|[1-9][0-9]*)") {
		t.Fatal("metadata resolution missing strict semantic version gate")
	}

	release := wf.Jobs["release"]
	for _, need := range []string{"metadata", "preflight", "quality"} {
		if !slices.Contains(release.Needs, need) {
			t.Fatalf("release needs=%v, missing %q", release.Needs, need)
		}
	}
	for _, snippet := range []string{"always()", "!cancelled()", "needs.metadata.result == 'success'", "needs.preflight.result == 'success'", "run_release == 'true'", "needs.quality.result == 'success'"} {
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
		"https://github.com/ildarbinanas-design/homebrew-tap.git",
		"semver-compare.sh",
		"refusing release downgrade",
		"exit 1",
	} {
		if !strings.Contains(guard.Run, snippet) {
			t.Fatalf("preflight guard missing %q", snippet)
		}
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
	build := reusable.Jobs["build"]
	if step := namedStep(t, build, "Build"); step.Env["VERSION"] != "${{ inputs.version }}" {
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

	ci := readWorkflow(t, "../.github/workflows/ci.yml")
	if !slices.Equal(ci.On.Push.Branches, []string{"main"}) {
		t.Fatalf("CI push branches=%v, want main only to avoid duplicate PR branch runs", ci.On.Push.Branches)
	}
	if !slices.Equal(ci.On.PullRequest.Types, []string{"opened", "synchronize", "reopened"}) {
		t.Fatalf("CI pull_request types=%v, want code-bearing events only", ci.On.PullRequest.Types)
	}
	if ci.Concurrency.Group != "ci-${{ github.event.pull_request.number || github.ref }}" || !ci.Concurrency.CancelInProgress {
		t.Fatalf("CI concurrency=%+v, want stale full runs cancelled per pull request or branch", ci.Concurrency)
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
	if ciQuality.With["source_sha"] != "${{ github.sha }}" || ciQuality.With["version"] != "ci-${{ github.sha }}" {
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
	module := readWorkflowJob(t, "../.github/workflows/reusable-quality.yml", "module")
	if module.RunsOn != "ubuntu-latest" || module.Env["GOTOOLCHAIN"] != "local" {
		t.Fatalf("module job runner=%q env=%v", module.RunsOn, module.Env)
	}
	checkout := usesStep(t, module, "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0")
	if checkout.With["ref"] != "${{ inputs.source_sha }}" {
		t.Fatalf("module checkout ref=%q", checkout.With["ref"])
	}
	setup := usesStep(t, module, "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16")
	if setup.With["go-version-file"] != "go.mod" || setup.With["go-version"] != "" {
		t.Fatalf("module setup-go inputs=%v", setup.With)
	}
	if namedStep(t, module, "Require tidy module files").Run != "go mod tidy -diff" {
		t.Fatal("module job must fail on a non-idempotent go mod tidy")
	}
	if namedStep(t, module, "Verify module cache").Run != "go mod verify" {
		t.Fatal("module job must verify downloaded modules")
	}
}

func TestReusableQualityRunsE2EAgainstEveryNativeReleaseArtifact(t *testing.T) {
	reusable := readWorkflow(t, "../.github/workflows/reusable-quality.yml")
	e2e, ok := reusable.Jobs["e2e"]
	if !ok {
		t.Fatal("reusable quality workflow is missing e2e job")
	}
	if e2e.Name != "e2e-${{ matrix.goos }}-${{ matrix.goarch }}" {
		t.Fatalf("e2e name=%q", e2e.Name)
	}
	if !slices.Equal(e2e.Needs, []string{"build"}) {
		t.Fatalf("e2e needs=%v, want build artifacts", e2e.Needs)
	}
	if e2e.RunsOn != "${{ matrix.runner }}" || e2e.TimeoutMinutes != 90 {
		t.Fatalf("e2e runner=%q timeout=%d", e2e.RunsOn, e2e.TimeoutMinutes)
	}
	if e2e.Env["GOTOOLCHAIN"] != "local" {
		t.Fatalf("e2e GOTOOLCHAIN=%q, want local", e2e.Env["GOTOOLCHAIN"])
	}
	if e2e.Strategy.FailFast == nil || *e2e.Strategy.FailFast {
		t.Fatalf("e2e fail-fast=%v, want explicit false", e2e.Strategy.FailFast)
	}

	type targetContract struct {
		runner  string
		cgo     string
		archive string
	}
	wantTargets := map[string]targetContract{
		"linux/amd64":   {runner: "ubuntu-latest", cgo: "0", archive: "tar.gz"},
		"linux/arm64":   {runner: "ubuntu-24.04-arm", cgo: "0", archive: "tar.gz"},
		"darwin/amd64":  {runner: "macos-15-intel", cgo: "1", archive: "tar.gz"},
		"darwin/arm64":  {runner: "macos-15", cgo: "1", archive: "tar.gz"},
		"windows/amd64": {runner: "windows-latest", cgo: "0", archive: "zip"},
	}
	if len(e2e.Strategy.Matrix.Include) != len(wantTargets) {
		t.Fatalf("e2e targets=%d, want %d", len(e2e.Strategy.Matrix.Include), len(wantTargets))
	}
	for _, target := range e2e.Strategy.Matrix.Include {
		key := target.GOOS + "/" + target.GOARCH
		want, ok := wantTargets[key]
		if !ok {
			t.Fatalf("unexpected or duplicate e2e target %q", key)
		}
		if target.Runner != want.runner || target.CGO != want.cgo || target.Archive != want.archive {
			t.Fatalf("e2e %s=%+v, want runner=%q cgo=%q archive=%q", key, target, want.runner, want.cgo, want.archive)
		}
		delete(wantTargets, key)
	}
	if len(wantTargets) != 0 {
		t.Fatalf("e2e matrix missing native targets: %v", wantTargets)
	}

	checkout := usesStep(t, e2e, "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0")
	if checkout.With["ref"] != "${{ inputs.source_sha }}" {
		t.Fatalf("e2e checkout ref=%q", checkout.With["ref"])
	}
	setup := usesStep(t, e2e, "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16")
	if setup.With["go-version-file"] != "go.mod" || setup.With["go-version"] != "" {
		t.Fatalf("e2e setup-go inputs=%v, want project Go baseline", setup.With)
	}
	nativeConfig := namedStep(t, e2e, "Run native platform config tests")
	if nativeConfig.Run != "go test ./internal/config -count=1" || nativeConfig.Env["CGO_ENABLED"] != "${{ matrix.cgo }}" {
		t.Fatalf("native platform config test=%+v", nativeConfig)
	}
	windowsConfigBurnIn := namedStep(t, e2e, "Burn in Windows config concurrency")
	if windowsConfigBurnIn.If != "matrix.goos == 'windows'" ||
		windowsConfigBurnIn.Run != "go test ./internal/config -run '^TestConcurrentSavePublishesOnlyCompleteConfigs$' -count=10" ||
		windowsConfigBurnIn.Env["CGO_ENABLED"] != "${{ matrix.cgo }}" {
		t.Fatalf("Windows config concurrency burn-in=%+v", windowsConfigBurnIn)
	}
	download := namedStep(t, e2e, "Download release-like artifact")
	if download.Uses != "actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c" {
		t.Fatalf("e2e download action=%q", download.Uses)
	}
	if download.With["name"] != "env-vault-${{ matrix.goos }}-${{ matrix.goarch }}" || download.With["path"] != "dist" {
		t.Fatalf("e2e artifact download inputs=%v", download.With)
	}

	run := namedStep(t, e2e, "Run E2E and finalize reports")
	for _, snippet := range []string{
		"go run ./e2e/cmd/e2e-runner run",
		"--phase candidate",
		"--coverage-floor 60",
		"--command-timeout 3m",
		"--test-timeout 5m",
		`--artifact "dist/env-vault-${{ matrix.goos }}-${{ matrix.goarch }}.${{ matrix.archive }}"`,
		`--checksum "dist/env-vault-${{ matrix.goos }}-${{ matrix.goarch }}.${{ matrix.archive }}.sha256"`,
	} {
		if !strings.Contains(run.Run, snippet) {
			t.Fatalf("e2e runner missing %q in %q", snippet, run.Run)
		}
	}
	for key, want := range map[string]string{
		"CGO_ENABLED":              "${{ matrix.cgo }}",
		"ENV_VAULT_E2E_GOOS":       "${{ matrix.goos }}",
		"ENV_VAULT_E2E_GOARCH":     "${{ matrix.goarch }}",
		"ENV_VAULT_E2E_VERSION":    "${{ inputs.version }}",
		"ENV_VAULT_E2E_COMMIT_SHA": "${{ inputs.source_sha }}",
	} {
		if got := run.Env[key]; got != want {
			t.Fatalf("e2e runner env %s=%q, want %q", key, got, want)
		}
	}
	if strings.Contains(run.Run, "internal/cli.Run") {
		t.Fatal("E2E workflow must execute only the built public CLI binary")
	}

	upload := namedStep(t, e2e, "Upload E2E reports")
	if upload.If != "always()" || upload.Uses != "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a" {
		t.Fatalf("e2e upload if=%q uses=%q", upload.If, upload.Uses)
	}
	for key, want := range map[string]string{
		"name":              "env-vault-e2e-candidate-${{ matrix.goos }}-${{ matrix.goarch }}-attempt-${{ github.run_attempt }}",
		"path":              "reports/e2e/candidate",
		"if-no-files-found": "error",
		"retention-days":    "30",
	} {
		if got := upload.With[key]; got != want {
			t.Fatalf("e2e upload %s=%q, want %q", key, got, want)
		}
	}
}

func TestReusableQualityPinsGo126CompatibleE2EReporter(t *testing.T) {
	e2e := readWorkflowJob(t, "../.github/workflows/reusable-quality.yml", "e2e")
	install := namedStep(t, e2e, "Install pinned E2E reporter")
	if install.Run != "go install gotest.tools/gotestsum@v1.13.0" {
		t.Fatalf("E2E reporter install=%q, want Go-1.26-compatible gotestsum v1.13.0", install.Run)
	}
	if !install.ContinueOnError {
		t.Fatal("reporter pre-install must allow the runner's pinned fallback to finalize failure reports")
	}
	allSteps := fmt.Sprintf("%v", e2e.Steps)
	for _, forbidden := range []string{"gotestsum@latest", "--rerun-fails"} {
		if strings.Contains(allSteps, forbidden) {
			t.Fatalf("E2E workflow contains forbidden reporter option %q", forbidden)
		}
	}
}

func TestReusableQualityE2EGateFailsClosed(t *testing.T) {
	gate := readWorkflowJob(t, "../.github/workflows/reusable-quality.yml", "e2e-gate")
	if gate.If != "always()" || !slices.Equal(gate.Needs, []string{"e2e"}) {
		t.Fatalf("e2e gate if=%q needs=%v", gate.If, gate.Needs)
	}
	if gate.RunsOn != "ubuntu-latest" || gate.TimeoutMinutes != 10 || gate.Env["GOTOOLCHAIN"] != "local" {
		t.Fatalf("e2e gate runner=%q timeout=%d env=%v", gate.RunsOn, gate.TimeoutMinutes, gate.Env)
	}
	checkout := usesStep(t, gate, "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0")
	if checkout.With["ref"] != "${{ inputs.source_sha }}" {
		t.Fatalf("e2e gate checkout ref=%q", checkout.With["ref"])
	}
	setup := usesStep(t, gate, "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16")
	if setup.With["go-version-file"] != "go.mod" || setup.With["go-version"] != "" {
		t.Fatalf("e2e gate setup-go inputs=%v", setup.With)
	}
	download := namedStep(t, gate, "Download E2E report artifacts")
	if download.Uses != "actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c" {
		t.Fatalf("e2e gate download action=%q", download.Uses)
	}
	for key, want := range map[string]string{
		"pattern":        "env-vault-e2e-candidate-*-attempt-${{ github.run_attempt }}",
		"path":           "reports-download",
		"merge-multiple": "true",
	} {
		if got := download.With[key]; got != want {
			t.Fatalf("e2e gate download %s=%q, want %q", key, got, want)
		}
	}
	validate := namedStep(t, gate, "Validate complete E2E report matrix")
	if validate.If != "always()" {
		t.Fatalf("e2e matrix validation if=%q, want always()", validate.If)
	}
	for _, snippet := range []string{
		"go run ./e2e/cmd/e2e-runner validate-matrix",
		"--reports reports-download",
		"--phase candidate",
		`--expected-commit "${{ inputs.source_sha }}"`,
		`--expected-run-id "${{ github.run_id }}"`,
		`--expected-run-url "${{ github.server_url }}/${{ github.repository }}/actions/runs/${{ github.run_id }}"`,
		`--expected-run-attempt "${{ github.run_attempt }}"`,
		`--expected-repository "${{ github.repository }}"`,
		`--expected-reporter "v1.13.0"`,
	} {
		if !strings.Contains(validate.Run, snippet) {
			t.Fatalf("e2e matrix validation missing %q in %q", snippet, validate.Run)
		}
	}
	upload := namedStep(t, gate, "Upload matrix validation")
	if upload.If != "always()" || upload.Uses != "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a" || upload.With["if-no-files-found"] != "error" || upload.With["retention-days"] != "30" {
		t.Fatalf("matrix validation upload=%+v", upload)
	}
}

func TestReusableQualityComparesExactCanonicalBaseline(t *testing.T) {
	compare := readWorkflowJob(t, "../.github/workflows/reusable-quality.yml", "e2e-compare")
	if compare.If != "always()" || !slices.Equal(compare.Needs, []string{"e2e", "e2e-gate"}) || compare.RunsOn != "ubuntu-latest" || compare.TimeoutMinutes != 20 {
		t.Fatalf("compare job if=%q needs=%v runner=%q timeout=%d", compare.If, compare.Needs, compare.RunsOn, compare.TimeoutMinutes)
	}
	if len(compare.Permissions) != 2 || compare.Permissions["contents"] != "read" || compare.Permissions["actions"] != "read" {
		t.Fatalf("compare permissions=%v, want contents/actions read", compare.Permissions)
	}
	if compare.Env["GOTOOLCHAIN"] != "local" {
		t.Fatalf("compare GOTOOLCHAIN=%q", compare.Env["GOTOOLCHAIN"])
	}
	candidateCheckout := namedStep(t, compare, "Check out candidate source")
	if candidateCheckout.Uses != "actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0" || candidateCheckout.With["ref"] != "${{ inputs.source_sha }}" {
		t.Fatalf("candidate checkout=%+v", candidateCheckout)
	}
	baselineCheckout := namedStep(t, compare, "Check out canonical baseline source")
	for key, want := range map[string]string{
		"repository":          "ildarbinanas-design/env-vault",
		"ref":                 "7a044bdbf73aa592016bbb3a02d81f314f08fe63",
		"path":                "baseline-source",
		"persist-credentials": "false",
	} {
		if baselineCheckout.With[key] != want {
			t.Fatalf("baseline checkout %s=%q, want %q", key, baselineCheckout.With[key], want)
		}
	}
	setup := usesStep(t, compare, "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16")
	if setup.With["go-version-file"] != "go.mod" {
		t.Fatalf("compare setup-go inputs=%v", setup.With)
	}
	preload := namedStep(t, compare, "Preload recorded comparison toolchains")
	if preload.Shell != "bash" {
		t.Fatalf("comparison toolchain preload shell=%q", preload.Shell)
	}
	for _, snippet := range []string{"GOTOOLCHAIN=go1.22.12 go version", "GOTOOLCHAIN=go1.26.5 go version"} {
		if !strings.Contains(preload.Run, snippet) {
			t.Fatalf("comparison toolchain preload missing %q in %q", snippet, preload.Run)
		}
	}
	candidateDownload := namedStep(t, compare, "Download candidate E2E reports")
	if !candidateDownload.ContinueOnError || candidateDownload.With["pattern"] != "env-vault-e2e-candidate-*-attempt-${{ github.run_attempt }}" || candidateDownload.With["path"] != "candidate-download" || candidateDownload.With["merge-multiple"] != "true" {
		t.Fatalf("candidate report download=%+v", candidateDownload)
	}
	baselineDownload := namedStep(t, compare, "Download canonical baseline E2E reports")
	if !baselineDownload.ContinueOnError || baselineDownload.Uses != "actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c" {
		t.Fatalf("baseline report download=%+v", baselineDownload)
	}
	for key, want := range map[string]string{
		"github-token":   "${{ github.token }}",
		"repository":     "ildarbinanas-design/env-vault",
		"run-id":         "29441160687",
		"pattern":        "env-vault-e2e-baseline-*-attempt-1",
		"path":           "baseline-download",
		"merge-multiple": "true",
	} {
		if baselineDownload.With[key] != want {
			t.Fatalf("baseline report download %s=%q, want %q", key, baselineDownload.With[key], want)
		}
	}
	baselineValidate := namedStep(t, compare, "Validate canonical baseline against baseline source")
	if baselineValidate.ID != "baseline_source_validation" || baselineValidate.If != "always()" || !baselineValidate.ContinueOnError || baselineValidate.Shell != "bash" {
		t.Fatalf("baseline source validation=%+v", baselineValidate)
	}
	for _, snippet := range []string{
		"cd baseline-source",
		`rm -f -- \`,
		`"$GITHUB_WORKSPACE/baseline-download/matrix-validation.json"`,
		"GOTOOLCHAIN=go1.22.12 go run ./e2e/cmd/e2e-runner validate-matrix",
		`--reports "$GITHUB_WORKSPACE/baseline-download"`,
		"--phase baseline",
		`--expected-commit "7a044bdbf73aa592016bbb3a02d81f314f08fe63"`,
		`--expected-run-id "29441160687"`,
		`--expected-reporter "v1.12.2"`,
	} {
		if !strings.Contains(baselineValidate.Run, snippet) {
			t.Fatalf("baseline source validation missing %q in %q", snippet, baselineValidate.Run)
		}
	}
	if strings.Index(baselineValidate.Run, "matrix-validation.json") >= strings.Index(baselineValidate.Run, "go run ./e2e/cmd/e2e-runner validate-matrix") {
		t.Fatal("baseline stale matrix validation must be removed before source validation")
	}
	candidateValidate := namedStep(t, compare, "Validate candidate against candidate source")
	if candidateValidate.ID != "candidate_source_validation" || candidateValidate.If != "always()" || !candidateValidate.ContinueOnError || candidateValidate.Shell != "bash" {
		t.Fatalf("candidate source validation=%+v", candidateValidate)
	}
	for _, snippet := range []string{
		"GOTOOLCHAIN=go1.26.5 go run ./e2e/cmd/e2e-runner validate-matrix",
		`"$GITHUB_WORKSPACE/candidate-download/matrix-validation.json"`,
		`--reports "$GITHUB_WORKSPACE/candidate-download"`,
		"--phase candidate",
		`--expected-commit "${{ inputs.source_sha }}"`,
		`--expected-run-id "${{ github.run_id }}"`,
		`--expected-reporter "v1.13.0"`,
	} {
		if !strings.Contains(candidateValidate.Run, snippet) {
			t.Fatalf("candidate source validation missing %q in %q", snippet, candidateValidate.Run)
		}
	}
	if strings.Index(candidateValidate.Run, "matrix-validation.json") >= strings.Index(candidateValidate.Run, "go run ./e2e/cmd/e2e-runner validate-matrix") {
		t.Fatal("candidate stale matrix validation must be removed before source validation")
	}
	run := namedStep(t, compare, "Compare candidate with canonical baseline")
	if run.If != "always()" || run.Shell != "bash" {
		t.Fatalf("comparison execution if=%q shell=%q", run.If, run.Shell)
	}
	for _, snippet := range []string{
		"GOTOOLCHAIN=go1.26.5 go run ./cmd/e2e-compare",
		`--baseline "$GITHUB_WORKSPACE/baseline-download"`,
		`--candidate "$GITHUB_WORKSPACE/candidate-download"`,
		"--coverage-tolerance 0",
		`--expected-suite-hash "ace01466c8b504af9a1a2af2ec2ba3bcd9446e637044d94b4ce7d5dffa842fcf"`,
		`--baseline-validation-outcome "${{ steps.baseline_source_validation.outcome }}"`,
		`--candidate-validation-outcome "${{ steps.candidate_source_validation.outcome }}"`,
		`--baseline-commit "7a044bdbf73aa592016bbb3a02d81f314f08fe63"`,
		`--baseline-run-id "29441160687"`,
		`--baseline-run-url "https://github.com/ildarbinanas-design/env-vault/actions/runs/29441160687"`,
		`--baseline-run-attempt "1"`,
		`--baseline-repository "ildarbinanas-design/env-vault"`,
		`--baseline-reporter "v1.12.2"`,
		`--candidate-reporter "v1.13.0"`,
	} {
		if !strings.Contains(run.Run, snippet) {
			t.Fatalf("comparison command missing %q in %q", snippet, run.Run)
		}
	}
	positions := make(map[string]int)
	for index, step := range compare.Steps {
		positions[step.Name] = index
	}
	for before, after := range map[string]string{
		"Preload recorded comparison toolchains":              "Validate canonical baseline against baseline source",
		"Download canonical baseline E2E reports":             "Validate canonical baseline against baseline source",
		"Download candidate E2E reports":                      "Validate candidate against candidate source",
		"Validate canonical baseline against baseline source": "Compare candidate with canonical baseline",
		"Validate candidate against candidate source":         "Compare candidate with canonical baseline",
	} {
		if positions[before] >= positions[after] {
			t.Fatalf("comparison step %q must precede %q", before, after)
		}
	}
	identity := namedStep(t, compare, "Verify exact migration identity")
	for _, snippet := range []string{"ace01466c8b504af9a1a2af2ec2ba3bcd9446e637044d94b4ce7d5dffa842fcf", `"go1.22.12"`, `"go1.26.5"`} {
		if identity.If != "always()" || !strings.Contains(identity.Run, snippet) {
			t.Fatalf("exact migration identity missing %q in %+v", snippet, identity)
		}
	}
	summary := namedStep(t, compare, "Add comparison to job summary")
	if summary.If != "always()" || !strings.Contains(summary.Run, "GITHUB_STEP_SUMMARY") {
		t.Fatalf("comparison summary=%+v", summary)
	}
	upload := namedStep(t, compare, "Upload baseline comparison")
	if upload.If != "always()" || upload.Uses != "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a" || upload.With["if-no-files-found"] != "error" || upload.With["retention-days"] != "30" {
		t.Fatalf("comparison upload=%+v", upload)
	}
}

func TestGeneratedHomebrewFormulaPreservesDistributionContract(t *testing.T) {
	homebrew := readWorkflowJob(t, "../.github/workflows/build-binaries.yml", "homebrew")
	generate := namedStep(t, homebrew, "Generate formula")
	if !strings.Contains(generate.Run, "scripts/release/generate-homebrew-formula.sh") {
		t.Fatal("workflow must use the tested Homebrew formula generator")
	}
	quality := readWorkflow(t, "../.github/workflows/reusable-quality.yml")
	archiveBuild := namedStep(t, quality.Jobs["build"], "Build")
	archiveDocs := `cp README.md LICENSE THIRD_PARTY_NOTICES.md "dist/env-vault-${GOOS}-${GOARCH}/"`
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
	if len(homebrew.Permissions) != 1 || homebrew.Permissions["contents"] != "read" {
		t.Fatalf("homebrew permissions=%v", homebrew.Permissions)
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
		"owner":                    "${{ github.repository_owner }}",
		"repositories":             "homebrew-tap",
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

	publish := namedStep(t, homebrew, "Create or reuse Homebrew pull request")
	for _, snippet := range []string{"publish-homebrew-pr.sh", "tap-out/env-vault.rb", "ildarbinanas-design/homebrew-tap", "GITHUB_OUTPUT"} {
		if !strings.Contains(publish.Run, snippet) {
			t.Fatalf("Homebrew PR publication missing %q", snippet)
		}
	}
	if publish.Env["GH_TOKEN"] != "${{ steps.tap-token.outputs.token }}" || publish.Env["SOURCE_SHA"] != "${{ needs.metadata.outputs.source_sha }}" {
		t.Fatalf("Homebrew PR publication env=%v", publish.Env)
	}

	prCI := namedStep(t, homebrew, "Wait for exact Homebrew pull request CI")
	if prCI.If != "steps.publish-tap-pr.outputs.state == 'OPEN'" {
		t.Fatalf("Homebrew PR CI if=%q", prCI.If)
	}
	for _, snippet := range []string{"wait-tap-ci.sh", "test-formula.yml", `"$HEAD_SHA"`, "pull_request", "GITHUB_OUTPUT"} {
		if !strings.Contains(prCI.Run, snippet) {
			t.Fatalf("Homebrew PR CI missing %q", snippet)
		}
	}

	merge := namedStep(t, homebrew, "Merge exact Homebrew pull request head")
	for _, snippet := range []string{"merge-homebrew-pr.sh", `"$PR_NUMBER"`, `"$HEAD_SHA"`, "GITHUB_OUTPUT"} {
		if !strings.Contains(merge.Run, snippet) {
			t.Fatalf("Homebrew merge missing %q", snippet)
		}
	}

	pushCI := namedStep(t, homebrew, "Wait for exact Homebrew default-branch CI")
	for _, snippet := range []string{"wait-tap-ci.sh", "test-formula.yml", `"$TAP_SHA"`, "push", "GITHUB_OUTPUT"} {
		if !strings.Contains(pushCI.Run, snippet) {
			t.Fatalf("Homebrew push CI missing %q", snippet)
		}
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
	for _, jobName := range []string{"module", "test", "race", "smoke", "license", "build", "e2e", "e2e-gate", "e2e-compare"} {
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
		"release_write_checksum_pair",
		`release_verify_checksum_pair "$local_dir/$archive" "$pair_dir/$checksum"`,
		`gh release upload "$version" "$local_dir/$archive" "$local_dir/$checksum"`,
		"download-release-assets.sh",
	} {
		if !strings.Contains(reconcileScript, snippet) {
			t.Fatalf("asset reconciliation missing critical case %q", snippet)
		}
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
		"test-formula.yml",
		"push",
		"GITHUB_STEP_SUMMARY",
		"/releases/tag/${VERSION}",
		"/homebrew-tap/commit/${tap_sha}",
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

	wantSubjects := []string{
		"release-dist/env-vault-linux-amd64.tar.gz",
		"release-dist/env-vault-linux-arm64.tar.gz",
		"release-dist/env-vault-darwin-amd64.tar.gz",
		"release-dist/env-vault-darwin-arm64.tar.gz",
		"release-dist/env-vault-windows-amd64.zip",
	}
	for _, stepName := range []string{"Attest build provenance", "Attest SPDX SBOM"} {
		step := namedStep(t, supply, stepName)
		if step.Uses != "actions/attest@a1948c3f048ba23858d222213b7c278aabede763" {
			t.Fatalf("%s uses=%q", stepName, step.Uses)
		}
		if got := strings.Fields(step.With["subject-path"]); !slices.Equal(got, wantSubjects) {
			t.Fatalf("%s subjects=%v, want %v", stepName, got, wantSubjects)
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

func TestReleaseDarwinBuildUsesMacOSRunnerAndCGO(t *testing.T) {
	build := readWorkflowJob(t, "../.github/workflows/reusable-quality.yml", "build")
	assertBuildMatrix(t, build)
}

func TestReleaseArtifactsRunOnNativeRunnersAndReportExactVersion(t *testing.T) {
	reusable := readWorkflow(t, "../.github/workflows/reusable-quality.yml")
	build := reusable.Jobs["build"]
	upload := namedStep(t, build, "Upload artifact")
	if upload.Uses != "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a" || upload.With["name"] != "env-vault-${{ matrix.goos }}-${{ matrix.goarch }}" {
		t.Fatalf("build artifact upload uses=%q with=%v", upload.Uses, upload.With)
	}
	smoke := reusable.Jobs["native-smoke"]
	if !slices.Contains(smoke.Needs, "build") {
		t.Fatalf("smoke needs=%v, missing build", smoke.Needs)
	}
	if smoke.RunsOn != "${{ matrix.runner }}" {
		t.Fatalf("smoke runs-on=%q", smoke.RunsOn)
	}
	wantRunners := map[string]string{
		"linux/amd64":   "ubuntu-latest",
		"linux/arm64":   "ubuntu-24.04-arm",
		"darwin/amd64":  "macos-15-intel",
		"darwin/arm64":  "macos-15",
		"windows/amd64": "windows-latest",
	}
	if len(smoke.Strategy.Matrix.Include) != len(wantRunners) {
		t.Fatalf("smoke targets=%d, want %d", len(smoke.Strategy.Matrix.Include), len(wantRunners))
	}
	for _, target := range smoke.Strategy.Matrix.Include {
		key := target.GOOS + "/" + target.GOARCH
		if target.Runner != wantRunners[key] {
			t.Fatalf("smoke %s runner=%q, want %q", key, target.Runner, wantRunners[key])
		}
		delete(wantRunners, key)
	}
	if len(wantRunners) != 0 {
		t.Fatalf("missing native smoke targets: %v", wantRunners)
	}

	wantVersion := "${{ inputs.version }}"
	unix := namedStep(t, smoke, "Verify exact version on Unix")
	if unix.If != "runner.os != 'Windows'" || unix.Env["VERSION"] != wantVersion {
		t.Fatalf("unix smoke if=%q VERSION=%q", unix.If, unix.Env["VERSION"])
	}
	for _, snippet := range []string{
		`printf '%s\n' "$VERSION"`,
		`"$binary" --version`,
		`"$binary" version`,
		"diff -u expected-version.txt version-flag.txt",
		"diff -u expected-version.txt version-command.txt",
	} {
		if !strings.Contains(unix.Run, snippet) {
			t.Fatalf("unix version smoke missing %q", snippet)
		}
	}

	windows := namedStep(t, smoke, "Verify exact version on Windows")
	if windows.If != "runner.os == 'Windows'" || windows.Env["VERSION"] != wantVersion {
		t.Fatalf("windows smoke if=%q VERSION=%q", windows.If, windows.Env["VERSION"])
	}
	for _, snippet := range []string{
		"& $binary --version",
		"& $binary version",
		".Count -ne 1",
		"-cne $env:VERSION",
	} {
		if !strings.Contains(windows.Run, snippet) {
			t.Fatalf("windows version smoke missing %q", snippet)
		}
	}

	release := readWorkflowJob(t, "../.github/workflows/build-binaries.yml", "release")
	if !slices.Contains(release.Needs, "quality") || !strings.Contains(release.If, "needs.quality.result == 'success'") {
		t.Fatalf("release is not gated by reusable quality: needs=%v if=%q", release.Needs, release.If)
	}
	download := usesStep(t, release, "actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c")
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

func assertBuildMatrix(t *testing.T, build workflowJob) {
	t.Helper()
	if build.RunsOn != "${{ matrix.runner }}" {
		t.Fatalf("build runs-on=%q", build.RunsOn)
	}
	if len(build.Strategy.Matrix.Include) != 5 {
		t.Fatalf("build targets=%d, want 5", len(build.Strategy.Matrix.Include))
	}
	targets := map[string]workflowTarget{}
	for _, target := range build.Strategy.Matrix.Include {
		targets[target.GOOS+"/"+target.GOARCH] = target
	}

	assertTarget(t, targets["darwin/amd64"], "macos-15-intel", "1")
	assertTarget(t, targets["darwin/arm64"], "macos-15", "1")
	for _, key := range []string{"linux/amd64", "linux/arm64", "windows/amd64"} {
		assertTarget(t, targets[key], "ubuntu-latest", "0")
	}

	step := buildStep(t, build)
	if step.Env["CGO_ENABLED"] != "${{ matrix.cgo }}" {
		t.Fatalf("CGO_ENABLED=%q", step.Env["CGO_ENABLED"])
	}
}

func assertTarget(t *testing.T, target workflowTarget, runner, cgo string) {
	t.Helper()
	if target.GOOS == "" || target.GOARCH == "" {
		t.Fatalf("missing workflow target")
	}
	if target.Runner != runner {
		t.Fatalf("%s/%s runner=%q", target.GOOS, target.GOARCH, target.Runner)
	}
	if target.CGO != cgo {
		t.Fatalf("%s/%s cgo=%q", target.GOOS, target.GOARCH, target.CGO)
	}
}

func buildStep(t *testing.T, build workflowJob) workflowStep {
	t.Helper()
	for _, step := range build.Steps {
		if step.Name == "Build" || step.Run == "go build ./cmd/env-vault" {
			return step
		}
	}
	t.Fatalf("build step not found")
	return workflowStep{}
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
