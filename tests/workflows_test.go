package tests

import (
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type workflow struct {
	On   workflowTriggers       `yaml:"on"`
	Jobs map[string]workflowJob `yaml:"jobs"`
}

type workflowTriggers struct {
	WorkflowDispatch workflowDispatch `yaml:"workflow_dispatch"`
}

type workflowDispatch struct {
	Inputs map[string]workflowInput `yaml:"inputs"`
}

type workflowInput struct {
	Description string `yaml:"description"`
	Required    bool   `yaml:"required"`
	Default     string `yaml:"default"`
	Type        string `yaml:"type"`
}

type workflowJob struct {
	If       string           `yaml:"if"`
	Needs    []string         `yaml:"needs"`
	RunsOn   string           `yaml:"runs-on"`
	Strategy workflowStrategy `yaml:"strategy"`
	Steps    []workflowStep   `yaml:"steps"`
}

type workflowStrategy struct {
	Matrix workflowMatrix `yaml:"matrix"`
}

type workflowMatrix struct {
	Include []workflowTarget `yaml:"include"`
}

type workflowTarget struct {
	GOOS   string `yaml:"goos"`
	GOARCH string `yaml:"goarch"`
	Runner string `yaml:"runner"`
	CGO    string `yaml:"cgo"`
}

type workflowStep struct {
	Name string            `yaml:"name"`
	Uses string            `yaml:"uses"`
	If   string            `yaml:"if"`
	Run  string            `yaml:"run"`
	Env  map[string]string `yaml:"env"`
}

func TestWorkflowsUseNode24ActionMajors(t *testing.T) {
	expected := map[string]string{
		"actions/checkout":          "v7",
		"actions/setup-go":          "v6",
		"actions/upload-artifact":   "v7",
		"actions/download-artifact": "v8",
	}
	for _, path := range []string{"../.github/workflows/build-binaries.yml", "../.github/workflows/ci.yml"} {
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

func TestManualReleaseInputAndGates(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/build-binaries.yml")
	version, ok := wf.On.WorkflowDispatch.Inputs["version"]
	if !ok {
		t.Fatal("workflow_dispatch missing optional version input")
	}
	if version.Required || version.Default != "" || version.Type != "string" {
		t.Fatalf("version input required=%v default=%q type=%q", version.Required, version.Default, version.Type)
	}

	metadata := wf.Jobs["metadata"]
	resolve := namedStep(t, metadata, "Resolve build version and release mode")
	for _, snippet := range []string{"refs/heads/${DEFAULT_BRANCH}", "vMAJOR.MINOR.PATCH", "GITHUB_OUTPUT", "publish=false"} {
		if !strings.Contains(resolve.Run, snippet) {
			t.Fatalf("metadata resolution missing %q", snippet)
		}
	}
	if !strings.Contains(resolve.Run, "^v(0|[1-9][0-9]*)") {
		t.Fatal("metadata resolution missing strict semantic version gate")
	}

	release := wf.Jobs["release"]
	for _, need := range []string{"metadata", "verify", "license", "build"} {
		if !slices.Contains(release.Needs, need) {
			t.Fatalf("release needs=%v, missing %q", release.Needs, need)
		}
	}
	if release.If != "needs.metadata.outputs.publish == 'true'" {
		t.Fatalf("release if=%q", release.If)
	}

	createTag := namedStep(t, release, "Create release tag for manual dispatch")
	if createTag.If != "github.event_name == 'workflow_dispatch'" {
		t.Fatalf("manual tag step if=%q", createTag.If)
	}
	for _, snippet := range []string{"GITHUB_SHA", "git/refs", "existing_sha", "--raw-field"} {
		if !strings.Contains(createTag.Run, snippet) && createTag.Env[snippet] == "" {
			t.Fatalf("manual tag step missing %q", snippet)
		}
	}
	if createTag.Env["VERSION"] != "${{ needs.metadata.outputs.version }}" {
		t.Fatalf("manual tag VERSION=%q", createTag.Env["VERSION"])
	}
}

func TestResolvedVersionFeedsAllReleaseStages(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/build-binaries.yml")
	want := "${{ needs.metadata.outputs.version }}"
	if !slices.Contains(wf.Jobs["build"].Needs, "metadata") {
		t.Fatalf("build needs=%v, missing metadata", wf.Jobs["build"].Needs)
	}
	checks := []struct {
		job  string
		step string
	}{
		{job: "build", step: "Build"},
		{job: "release", step: "Create GitHub Release"},
		{job: "homebrew", step: "Generate formula"},
		{job: "homebrew", step: "Push formula to tap"},
	}
	for _, check := range checks {
		step := namedStep(t, wf.Jobs[check.job], check.step)
		if step.Env["VERSION"] != want {
			t.Fatalf("%s/%s VERSION=%q, want %q", check.job, check.step, step.Env["VERSION"], want)
		}
	}

	homebrew := wf.Jobs["homebrew"]
	if !slices.Contains(homebrew.Needs, "metadata") || !slices.Contains(homebrew.Needs, "release") {
		t.Fatalf("homebrew needs=%v", homebrew.Needs)
	}
	if homebrew.If != "needs.metadata.outputs.publish == 'true'" {
		t.Fatalf("homebrew if=%q", homebrew.If)
	}
}

func TestReleaseAndCIRunPinnedLicenseGate(t *testing.T) {
	for _, path := range []string{"../.github/workflows/build-binaries.yml", "../.github/workflows/ci.yml"} {
		license := readWorkflowJob(t, path, "license")
		step := runStep(t, license, "scripts/license-check.sh")
		if step.Run != "scripts/license-check.sh" {
			t.Fatalf("%s license step=%q", path, step.Run)
		}
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
}

func TestGeneratedHomebrewFormulaRequiresExactVersion(t *testing.T) {
	homebrew := readWorkflowJob(t, "../.github/workflows/build-binaries.yml", "homebrew")
	generate := namedStep(t, homebrew, "Generate formula")
	want := `assert_equal "v#{version}", shell_output("#{bin}/env-vault --version").strip`
	if !strings.Contains(generate.Run, want) {
		t.Fatalf("generated formula test missing exact assertion %q", want)
	}
	if strings.Contains(generate.Run, "assert_match") {
		t.Fatal("generated formula must not accept a version substring")
	}
	if strings.Contains(generate.Run, "link_overwrite") {
		t.Fatal("generated formula must not overwrite unmanaged files")
	}
}

func TestHomebrewPushDoesNotMaskCommitFailures(t *testing.T) {
	homebrew := readWorkflowJob(t, "../.github/workflows/build-binaries.yml", "homebrew")
	push := namedStep(t, homebrew, "Push formula to tap")
	if strings.Contains(push.Run, "git commit -m \"env-vault ${VERSION}\" || exit 0") {
		t.Fatal("homebrew push masks git commit failures")
	}
	for _, snippet := range []string{"git diff --cached --quiet", "git commit -m", "git push origin HEAD:main"} {
		if !strings.Contains(push.Run, snippet) {
			t.Fatalf("homebrew push missing %q", snippet)
		}
	}
	guard := strings.Index(push.Run, "git diff --cached --quiet")
	commit := strings.Index(push.Run, "git commit -m")
	pushIndex := strings.Index(push.Run, "git push origin HEAD:main")
	if !(guard < commit && commit < pushIndex) {
		t.Fatalf("homebrew publish order guard=%d commit=%d push=%d", guard, commit, pushIndex)
	}
}

func TestReleaseDarwinBuildUsesMacOSRunnerAndCGO(t *testing.T) {
	build := readWorkflowJob(t, "../.github/workflows/build-binaries.yml", "build")
	assertBuildMatrix(t, build)
}

func TestReleaseArtifactsRunOnNativeRunnersAndReportExactVersion(t *testing.T) {
	wf := readWorkflow(t, "../.github/workflows/build-binaries.yml")
	smoke := wf.Jobs["smoke"]
	for _, need := range []string{"metadata", "build"} {
		if !slices.Contains(smoke.Needs, need) {
			t.Fatalf("smoke needs=%v, missing %q", smoke.Needs, need)
		}
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

	wantVersion := "${{ needs.metadata.outputs.version }}"
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

	if !slices.Contains(wf.Jobs["release"].Needs, "smoke") {
		t.Fatalf("release needs=%v, missing smoke", wf.Jobs["release"].Needs)
	}
}

func TestCIDarwinBuildUsesMacOSRunnerAndCGO(t *testing.T) {
	build := readWorkflowJob(t, "../.github/workflows/ci.yml", "build")
	assertBuildMatrix(t, build)
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
