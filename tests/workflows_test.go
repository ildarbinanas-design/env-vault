package tests

import (
	"encoding/json"
	"fmt"
	"maps"
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
	checkoutAction       = "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1"
	setupGoAction        = "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16"
	uploadArtifactAction = "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a"
	downloadAction       = "actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c"
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
	Group            string       `yaml:"group"`
	CancelInProgress workflowFlag `yaml:"cancel-in-progress"`
	Queue            string       `yaml:"queue"`
}

// workflowFlag is a boolean workflow field that may hold an expression instead.
type workflowFlag struct {
	Value      bool
	Expression string
}

func (f *workflowFlag) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!bool" {
		return node.Decode(&f.Value)
	}
	if node.Kind == yaml.ScalarNode && strings.HasPrefix(node.Value, "${{") && strings.HasSuffix(node.Value, "}}") {
		f.Expression = node.Value
		return nil
	}
	return fmt.Errorf("line %d: want a boolean or an expression, got %q", node.Line, node.Value)
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

type pushTrigger struct {
	Branches []string `yaml:"branches"`
	Tags     []string `yaml:"tags"`
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

func TestWorkflowFilesParseAndPinReviewedActions(t *testing.T) {
	expected := map[string]string{
		"actions/checkout":                 checkoutAction,
		"actions/setup-go":                 setupGoAction,
		"actions/upload-artifact":          uploadArtifactAction,
		"actions/download-artifact":        downloadAction,
		"actions/dependency-review-action": "actions/dependency-review-action@a1d282b36b6f3519aa1f3fc636f609c47dddb294",
		"googleapis/release-please-action": releasePleaseAction,
		"actions/attest":                   attestAction,
	}

	paths := workflowPaths(t)
	names := make([]string, 0, len(paths))
	for _, path := range paths {
		names = append(names, filepath.Base(path))
	}
	if want := []string{"ci.yml", "dependency-review.yml", "pr-title.yml", "release.yml", "reusable-quality.yml"}; !slices.Equal(names, want) {
		t.Fatalf("workflows=%v, want %v", names, want)
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
	if !wf.Concurrency.CancelInProgress.Value || !containsAll(wf.Concurrency.Group, "workflow_dispatch", "github.run_id", "github.run_attempt > 1", "rerun-", "github.ref") {
		t.Fatalf("ci concurrency does not isolate manual dispatch while cancelling superseded runs: %+v", wf.Concurrency)
	}
	assertJobIDs(t, wf, "quality", "quality-gate")

	quality := wf.Jobs["quality"]
	if quality.Uses != "./.github/workflows/reusable-quality.yml" {
		t.Fatalf("ci quality uses=%q", quality.Uses)
	}
	assertPermissions(t, "ci quality", quality.Permissions, map[string]string{"actions": "read", "contents": "read"})
	// ADR 0011: CI no longer classifies release commits, so it passes only
	// the commit to check.
	assertPermissions(t, "ci quality inputs", quality.With, map[string]string{"source_sha": "${{ github.sha }}"})

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
	if dependency.Concurrency.Group != "dependency-review-${{ github.event.pull_request.number }}" || !dependency.Concurrency.CancelInProgress.Value {
		t.Fatalf("dependency review concurrency=%+v", dependency.Concurrency)
	}
	assertPermissions(t, "dependency review", dependency.Permissions, map[string]string{"contents": "read"})
	assertJobIDs(t, dependency, "dependency-review")

	prTitle := readWorkflow(t, "../.github/workflows/pr-title.yml")
	assertTrigger(t, prTitle, "pull_request")
	if len(prTitle.Permissions) != 0 || prTitle.Concurrency.Group != "pr-title-${{ github.event.pull_request.number }}" || !prTitle.Concurrency.CancelInProgress.Value {
		t.Fatalf("pr-title permissions/concurrency=%v %+v", prTitle.Permissions, prTitle.Concurrency)
	}
	step := namedStep(t, prTitle.Jobs["pr-title"], "Require a Conventional Commit pull request title")
	if step.Env["PR_TITLE"] != "${{ github.event.pull_request.title }}" {
		t.Fatalf("pr-title source=%q", step.Env["PR_TITLE"])
	}
}

func TestReusableQualityBuildsTheReleaseTargetsAndRunsE2EOncePerOS(t *testing.T) {
	path := "../.github/workflows/reusable-quality.yml"
	wf := readWorkflow(t, path)
	raw := readFile(t, path)
	assertPermissions(t, "reusable quality", wf.Permissions, map[string]string{"contents": "read"})
	assertTrigger(t, wf, "workflow_call")
	call := decodeTrigger[callTrigger](t, wf, "workflow_call")
	if len(call.Inputs) != 1 || !call.Inputs["source_sha"].Required {
		t.Fatalf("reusable quality inputs=%+v, want only the required source_sha", call.Inputs)
	}
	// ADR 0011: release.yml builds releases. CI neither injects a version nor
	// reads a release contract; the binary reports the version Go stamps.
	for _, retired := range []string{"releasecheck", "release/contract", "internal/cli.Version", "ENV_VAULT_E2E_VERSION", "version=ci-", "validate-matrix", "promotion"} {
		if strings.Contains(raw, retired) {
			t.Fatalf("reusable quality still uses the retired release machinery via %q", retired)
		}
	}
	assertJobIDs(t, wf, "resolve", "source-quality", "license", "native", "e2e-gate")

	resolve := wf.Jobs["resolve"]
	if resolve.TimeoutMinutes != 15 || len(resolve.Outputs) != 0 {
		t.Fatalf("resolve timeout=%d outputs=%v, want 15 minutes and no outputs", resolve.TimeoutMinutes, resolve.Outputs)
	}
	var resolveSetup workflowStep
	for _, step := range resolve.Steps {
		if step.Uses == setupGoAction {
			resolveSetup = step
			break
		}
	}
	if !containsAll(resolveSetup.With["cache-dependency-path"], "go.sum", "tools/e2e-reporter/go.sum") {
		t.Fatalf("resolve Go cache does not include the isolated reporter graph: %v", resolveSetup.With)
	}
	// ADR 0011: E2E runs once per operating system.
	e2eTargets := []string{"linux-amd64", "darwin-arm64", "windows-amd64"}
	reporterBuild := namedStep(t, resolve, "Build exact E2E reporter bundle once")
	if !containsAll(reporterBuild.Run, `scripts/release/build-e2e-reporters.sh "$targets" reporter-tools`,
		`{id: "linux-amd64", goos: "linux", goarch: "amd64"}`,
		`{id: "darwin-arm64", goos: "darwin", goarch: "arm64"}`,
		`{id: "windows-amd64", goos: "windows", goarch: "amd64"}`) || strings.Count(reporterBuild.Run, "{id: ") != len(e2eTargets) {
		t.Fatalf("resolve does not build reporters for exactly the three E2E targets: %q", reporterBuild.Run)
	}
	reporterUploads := 0
	for _, target := range e2eTargets {
		upload := namedStep(t, resolve, "Upload "+target+" current-attempt E2E reporter")
		wantName := "env-vault-tooling-gotestsum-" + target + "-${{ inputs.source_sha }}-attempt-${{ github.run_attempt }}"
		if upload.Uses != uploadArtifactAction || upload.With["name"] != wantName ||
			upload.With["path"] != "reporter-tools/"+target || upload.With["if-no-files-found"] != "error" {
			t.Fatalf("%s reporter artifact is not exact-source/current-attempt qualified: uses=%q with=%v", target, upload.Uses, upload.With)
		}
		reporterUploads++
	}
	for _, step := range resolve.Steps {
		if strings.HasPrefix(step.With["name"], "env-vault-tooling-gotestsum-") {
			if step.Uses != uploadArtifactAction {
				t.Fatalf("reporter artifact uses unexpected action: %+v", step)
			}
			reporterUploads--
		}
	}
	if reporterUploads != 0 {
		t.Fatalf("reporter artifacts differ from the three E2E targets: residual=%d", reporterUploads)
	}

	native := wf.Jobs["native"]
	if !slices.Equal([]string(native.Needs), []string{"resolve"}) || native.RunsOn != "${{ matrix.runner }}" {
		t.Fatalf("native topology needs=%v runner=%q", native.Needs, native.RunsOn)
	}
	if native.Strategy.FailFast == nil || *native.Strategy.FailFast {
		t.Fatalf("native fail-fast=%v", native.Strategy.FailFast)
	}
	// CI builds exactly the targets release.yml builds, on the same runners.
	matrix := decodeMatrix(t, native.Strategy.Matrix)
	releaseMatrix := decodeMatrix(t, readWorkflow(t, "../.github/workflows/"+releaseWorkflowFile).Jobs["build"].Strategy.Matrix)
	if len(matrix.Include) != 5 || len(releaseMatrix.Include) != 5 {
		t.Fatalf("native matrix=%d targets, release matrix=%d, want 5 each", len(matrix.Include), len(releaseMatrix.Include))
	}
	for index, target := range matrix.Include {
		archive := "env-vault-" + target["id"] + ".tar.gz"
		if target["goos"] == "windows" {
			archive = "env-vault-" + target["id"] + ".zip"
		}
		want := maps.Clone(releaseMatrix.Include[index])
		want["archive"] = archive
		if !maps.Equal(target, want) || target["id"] != target["goos"]+"-"+target["goarch"] {
			t.Fatalf("native target %d=%v, want the release target %v", index, target, want)
		}
	}
	if native.Env["E2E"] != "${{ matrix.id == 'linux-amd64' || matrix.id == 'darwin-arm64' || matrix.id == 'windows-amd64' }}" {
		t.Fatalf("native E2E selection=%q, want one target per operating system", native.Env["E2E"])
	}
	for name, wantIf := range map[string]string{
		"Package native release artifact on Unix":     "env.E2E == 'true' && runner.os != 'Windows'",
		"Package native release artifact on Windows":  "env.E2E == 'true' && runner.os == 'Windows'",
		"Download exact current-attempt E2E reporter": "env.E2E == 'true'",
		"Run E2E and finalize reports":                "env.E2E == 'true'",
		"Upload current-attempt E2E reports":          "always() && env.E2E == 'true'",
		"Build native release artifact":               "",
		"Smoke-test the real OS secret store":         "",
	} {
		if step := namedStep(t, native, name); step.If != wantIf {
			t.Fatalf("native step %q if=%q, want %q", name, step.If, wantIf)
		}
	}
	build := namedStep(t, native, "Build native release artifact")
	if !strings.Contains(build.Run, `go build -trimpath -ldflags="-s -w" -o "dist/${name}/${BINARY}" ./cmd/env-vault`) || strings.Contains(build.Run, "-X ") {
		t.Fatalf("native build must match the release build flags without a version override: %q", build.Run)
	}
	reporterDownload := namedStep(t, native, "Download exact current-attempt E2E reporter")
	if reporterDownload.Uses != downloadAction ||
		reporterDownload.With["name"] != "env-vault-tooling-gotestsum-${{ matrix.id }}-${{ inputs.source_sha }}-attempt-${{ github.run_attempt }}" ||
		reporterDownload.With["path"] != "reporter-tool" {
		t.Fatalf("native reporter download is not bound to the current source/attempt: uses=%q with=%v", reporterDownload.Uses, reporterDownload.With)
	}
	runE2E := namedStep(t, native, "Run E2E and finalize reports")
	if runE2E.Shell != "bash" ||
		!containsAll(runE2E.Run, "reporter_name=gotestsum", "gotestsum.exe", "chmod 0755", "export GOPROXY=off", "go run ./e2e/cmd/e2e-runner run", "--reporter", "--reporter-checksum",
			`--artifact "dist/${{ matrix.archive }}"`, `--checksum "dist/${{ matrix.archive }}.sha256"`) ||
		strings.Contains(runE2E.Run, "export PATH=") ||
		strings.Contains(runE2E.Run, "GOSUMDB=off") {
		t.Fatalf("native E2E does not use the exact verified offline reporter: shell=%q run=%q", runE2E.Shell, runE2E.Run)
	}
	// CLI_VERSION_FORMS compares the binary's commit with the checked-out
	// commit only when this variable is set.
	if !maps.Equal(runE2E.Env, map[string]string{
		"CGO_ENABLED":              "${{ matrix.cgo }}",
		"ENV_VAULT_E2E_GOOS":       "${{ matrix.goos }}",
		"ENV_VAULT_E2E_GOARCH":     "${{ matrix.goarch }}",
		"ENV_VAULT_E2E_COMMIT_SHA": "${{ inputs.source_sha }}",
	}) {
		t.Fatalf("native E2E env=%v", runE2E.Env)
	}
	assertStepOrder(t, native,
		"Build native release artifact",
		"Download exact current-attempt E2E reporter",
		"Run E2E and finalize reports",
		"Smoke-test the real OS secret store",
	)
	if smoke := namedStep(t, native, "Smoke-test the real OS secret store"); !containsAll(smoke.Run, `scripts/backend-smoke.sh "$BINARY_PATH"`) ||
		smoke.Env["BINARY_PATH"] != "dist/env-vault-${{ matrix.goos }}-${{ matrix.goarch }}/${{ matrix.binary }}" || smoke.ContinueOnError {
		t.Fatalf("native job must smoke-test the release binary against the real secret store: %+v", smoke)
	}
	for _, job := range wf.Jobs {
		for _, step := range job.Steps {
			if strings.Contains(step.Run, "go install gotest.tools/gotestsum") || strings.Contains(step.Run, "go run gotest.tools/gotestsum") {
				t.Fatalf("workflow retains a matrix-time network reporter fallback: %q", step.Run)
			}
			if step.ContinueOnError && strings.Contains(strings.ToLower(step.Name), "reporter") {
				t.Fatalf("reporter bootstrap may not continue on error: %q", step.Name)
			}
			if step.Uses == uploadArtifactAction && strings.Contains(step.With["name"], "env-vault-release-") {
				t.Fatalf("native release artifacts only fed the retired publisher: %+v", step.With)
			}
		}
	}
	if step := namedStep(t, native, "Burn in Windows config concurrency"); step.If != "matrix.goos == 'windows'" || !containsAll(step.Run, "TestConcurrentSavePublishesOnlyCompleteConfigs", "-count=10") {
		t.Fatalf("Windows concurrency burn-in was weakened: if=%q run=%q", step.If, step.Run)
	}
	windowsPackage := namedStep(t, native, "Package native release artifact on Windows")
	if windowsPackage.Shell != "pwsh" || !containsAll(windowsPackage.Run, "[System.IO.File]::WriteAllText", "`n", "[System.Text.Encoding]::ASCII") || strings.Contains(windowsPackage.Run, "`r") || strings.Contains(windowsPackage.Run, "Set-Content") {
		t.Fatalf("Windows checksum writer does not produce deterministic LF-terminated ASCII: shell=%q run=%q", windowsPackage.Shell, windowsPackage.Run)
	}

	licenseMatrix := decodeMatrix(t, wf.Jobs["license"].Strategy.Matrix)
	expandedJobs := 1 + 1 + len(licenseMatrix.Include) + len(matrix.Include) + 1
	if expandedJobs != 11 {
		t.Fatalf("reusable quality expands to %d jobs, want 11", expandedJobs)
	}

	for _, command := range []string{"go test ./...", "go vet ./...", "go test -race ./..."} {
		if !jobRunsExact(wf.Jobs["source-quality"], command) {
			t.Fatalf("source-quality missing %q", command)
		}
	}
	gate := wf.Jobs["e2e-gate"]
	assertCancellationSafe(t, "reusable e2e-gate", gate)
	assertNeeds(t, "reusable e2e-gate", gate, "resolve", "source-quality", "license", "native")
	if len(gate.Steps) != 1 || gate.Steps[0].Name != "Require every upstream quality stage" {
		t.Fatalf("e2e-gate must only require every upstream stage: %+v", gate.Steps)
	}
}

func TestReleasePleaseConfigDraftsReleasesAndTracksVersionedDocs(t *testing.T) {
	data := []byte(readFile(t, "../release-please-config.json"))
	type changelogSection struct {
		Type    string `json:"type"`
		Section string `json:"section"`
		Hidden  bool   `json:"hidden"`
	}
	var config struct {
		LastReleaseSHA json.RawMessage `json:"last-release-sha"`
		Packages       map[string]struct {
			ReleaseType       string             `json:"release-type"`
			PackageName       string             `json:"package-name"`
			Component         string             `json:"component"`
			ChangelogPath     string             `json:"changelog-path"`
			SkipGitHubRelease *bool              `json:"skip-github-release"`
			Draft             bool               `json:"draft"`
			ForceTagCreation  bool               `json:"force-tag-creation"`
			IncludeVInTag     bool               `json:"include-v-in-tag"`
			ChangelogSections []changelogSection `json:"changelog-sections"`
			ExtraFiles        []struct {
				Type string `json:"type"`
				Path string `json:"path"`
			} `json:"extra-files"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse release-please-config.json: %v", err)
	}
	// Release Please finds the previous release from the tags. A pinned
	// last-release-sha would override that search.
	if config.LastReleaseSHA != nil || strings.Contains(string(data), "last-release-sha") {
		t.Fatalf("release-please-config.json must not pin last-release-sha: %s", config.LastReleaseSHA)
	}
	// ADR 0011: Release Please tags the merge commit and opens a draft release,
	// which release.yml publishes after it builds and attests the release.
	pkg, ok := config.Packages["."]
	if !ok || pkg.ReleaseType != "go" || pkg.PackageName != "env-vault" || pkg.Component != "env-vault" || pkg.ChangelogPath != "CHANGELOG.md" ||
		pkg.SkipGitHubRelease != nil || !pkg.Draft || !pkg.ForceTagCreation || !pkg.IncludeVInTag {
		t.Fatalf("release package config=%+v", pkg)
	}
	// Only product changes are visible, so only they create a release.
	wantSections := []changelogSection{
		{"feat", "Features", false},
		{"fix", "Bug Fixes", false},
		{"build", "Build System", true},
		{"ci", "Continuous Integration", true},
		{"docs", "Documentation", true},
		{"test", "Tests", true},
		{"refactor", "Refactoring", true},
		{"perf", "Performance", false},
		{"revert", "Reverts", false},
	}
	if !slices.Equal(pkg.ChangelogSections, wantSections) {
		t.Fatalf("changelog sections=%+v, want %+v", pkg.ChangelogSections, wantSections)
	}
	if len(pkg.ExtraFiles) != 1 || pkg.ExtraFiles[0].Type != "generic" || pkg.ExtraFiles[0].Path != "README.md" {
		t.Fatalf("versioned extra files=%+v", pkg.ExtraFiles)
	}

	// The README line that Release Please updates names the current version.
	var manifest map[string]string
	if err := json.Unmarshal([]byte(readFile(t, "../.release-please-manifest.json")), &manifest); err != nil {
		t.Fatalf("parse .release-please-manifest.json: %v", err)
	}
	version, ok := manifest["."]
	if !ok || len(manifest) != 1 || !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(version) {
		t.Fatalf("release manifest=%v, want one root version", manifest)
	}
	line := "Current version: `v" + version + "`. <!-- x-release-please-version -->\n"
	if count := strings.Count(readFile(t, "../README.md"), line); count != 1 {
		t.Fatalf("README version line count=%d, want 1; line=%q", count, line)
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

func jobRunsExact(job workflowJob, command string) bool {
	for _, step := range job.Steps {
		if strings.TrimSpace(step.Run) == command {
			return true
		}
	}
	return false
}
