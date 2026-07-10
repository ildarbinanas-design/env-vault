package tests

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	WorkflowDispatch workflowDispatch `yaml:"workflow_dispatch"`
	WorkflowCall     workflowCall     `yaml:"workflow_call"`
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
	If             string            `yaml:"if"`
	Needs          []string          `yaml:"needs"`
	RunsOn         string            `yaml:"runs-on"`
	Uses           string            `yaml:"uses"`
	With           map[string]string `yaml:"with"`
	Permissions    map[string]string `yaml:"permissions"`
	Outputs        map[string]string `yaml:"outputs"`
	Environment    string            `yaml:"environment"`
	TimeoutMinutes int               `yaml:"timeout-minutes"`
	Strategy       workflowStrategy  `yaml:"strategy"`
	Steps          []workflowStep    `yaml:"steps"`
}

type workflowStrategy struct {
	Matrix workflowMatrix `yaml:"matrix"`
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
	Name  string            `yaml:"name"`
	Uses  string            `yaml:"uses"`
	If    string            `yaml:"if"`
	Shell string            `yaml:"shell"`
	Run   string            `yaml:"run"`
	Env   map[string]string `yaml:"env"`
	With  map[string]string `yaml:"with"`
}

func TestWorkflowsUseNode24ActionMajors(t *testing.T) {
	expected := map[string]string{
		"actions/checkout":                 "v7",
		"actions/setup-go":                 "v6",
		"actions/upload-artifact":          "v7",
		"actions/download-artifact":        "v8",
		"actions/attest":                   "v4",
		"actions/create-github-app-token":  "v3",
		"anchore/sbom-action":              "v0",
		"actions/dependency-review-action": "v5",
	}
	for _, path := range []string{
		"../.github/workflows/audit-release-app.yml",
		"../.github/workflows/build-binaries.yml",
		"../.github/workflows/ci.yml",
		"../.github/workflows/dependency-review.yml",
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
	if token.Uses != "actions/create-github-app-token@v3" {
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
	resolve := namedStep(t, metadata, "Resolve build version and release mode")
	for _, snippet := range []string{
		"refs/heads/${DEFAULT_BRANCH}",
		"vMAJOR.MINOR.PATCH",
		"GITHUB_OUTPUT",
		"publish=false",
		"repair mode requires an explicit version",
		"scripts/release/resolve-tag-sha.sh",
		"run_release",
		"run_homebrew",
		"source_sha",
	} {
		if !strings.Contains(resolve.Run, snippet) {
			t.Fatalf("metadata resolution missing %q", snippet)
		}
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

	createTag := namedStep(t, release, "Create release tag for manual dispatch")
	if createTag.If != "github.event_name == 'workflow_dispatch' && needs.metadata.outputs.repair == 'none'" {
		t.Fatalf("manual tag step if=%q", createTag.If)
	}
	for _, snippet := range []string{"SOURCE_SHA", "git/refs", "existing_sha", "--raw-field", "already points to the expected commit; no-op"} {
		if !strings.Contains(createTag.Run, snippet) && createTag.Env[snippet] == "" {
			t.Fatalf("manual tag step missing %q", snippet)
		}
	}
	if createTag.Env["VERSION"] != "${{ needs.metadata.outputs.version }}" {
		t.Fatalf("manual tag VERSION=%q", createTag.Env["VERSION"])
	}
	for _, snippet := range []string{`tag_status" == "0`, `tag_status" != "4`, `exit "$tag_status"`} {
		if !strings.Contains(createTag.Run, snippet) {
			t.Fatalf("manual tag error classification missing %q", snippet)
		}
	}
	if strings.Contains(createTag.Run, "2>/dev/null") {
		t.Fatal("manual tag creation must not hide resolver failures")
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
	if len(ci.Jobs) != 1 {
		t.Fatalf("CI has %d jobs, want only reusable quality caller", len(ci.Jobs))
	}
	ciQuality := ci.Jobs["quality"]
	if ciQuality.Uses != "./.github/workflows/reusable-quality.yml" {
		t.Fatalf("CI quality uses=%q", ciQuality.Uses)
	}
	if ciQuality.With["source_sha"] != "${{ github.sha }}" || ciQuality.With["version"] != "ci-${{ github.sha }}" {
		t.Fatalf("CI quality inputs=%v", ciQuality.With)
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

func TestGeneratedHomebrewFormulaRequiresExactVersion(t *testing.T) {
	homebrew := readWorkflowJob(t, "../.github/workflows/build-binaries.yml", "homebrew")
	generate := namedStep(t, homebrew, "Generate formula")
	if !strings.Contains(generate.Run, "scripts/release/generate-homebrew-formula.sh") {
		t.Fatal("workflow must use the tested Homebrew formula generator")
	}
	assetDir := t.TempDir()
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
	if token.Uses != "actions/create-github-app-token@v3" {
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
	for _, jobName := range []string{"test", "race", "smoke", "license", "build"} {
		job := reusable.Jobs[jobName]
		checkout := usesStep(t, job, "actions/checkout@v7")
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
	if setupGo.Uses != "actions/setup-go@v6" || setupGo.With["go-version-file"] != "go.mod" {
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
	if sbom.Uses != "anchore/sbom-action@v0" {
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
	if upload.Uses != "actions/upload-artifact@v7" || upload.With["retention-days"] != "14" {
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
		if step.Uses != "actions/attest@v4" {
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
	if upload.Uses != "actions/upload-artifact@v7" || upload.With["name"] != "env-vault-${{ matrix.goos }}-${{ matrix.goarch }}" {
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
	download := usesStep(t, release, "actions/download-artifact@v8")
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
