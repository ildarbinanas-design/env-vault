package tests

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

type workflow struct {
	Jobs map[string]workflowJob `yaml:"jobs"`
}

type workflowJob struct {
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
	Run  string            `yaml:"run"`
	Env  map[string]string `yaml:"env"`
}

func TestReleaseDarwinBuildUsesMacOSRunnerAndCGO(t *testing.T) {
	build := readWorkflowJob(t, "../.github/workflows/build-binaries.yml", "build")
	assertBuildMatrix(t, build)
}

func TestCIDarwinBuildUsesMacOSRunnerAndCGO(t *testing.T) {
	build := readWorkflowJob(t, "../.github/workflows/ci.yml", "build")
	assertBuildMatrix(t, build)
}

func readWorkflowJob(t *testing.T, path, jobName string) workflowJob {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var wf workflow
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	job, ok := wf.Jobs[jobName]
	if !ok {
		t.Fatalf("%s missing job %q", path, jobName)
	}
	return job
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
