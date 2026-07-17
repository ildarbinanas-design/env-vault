package main

import (
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

func TestValidateGotestsumBuildInfoRequiresExactSupplyAndTargetIdentity(t *testing.T) {
	exact := func() *debug.BuildInfo {
		return &debug.BuildInfo{
			GoVersion: "go1.26.5",
			Path:      "gotest.tools/gotestsum",
			Main: debug.Module{
				Path:    "gotest.tools/gotestsum",
				Version: gotestsumVersion,
				Sum:     gotestsumModuleSum,
			},
			Deps: []*debug.Module{{Path: "example.invalid/dependency", Version: "v1.0.0", Sum: "h1:test"}},
			Settings: []debug.BuildSetting{
				{Key: "CGO_ENABLED", Value: "0"},
				{Key: "GOOS", Value: "linux"},
				{Key: "GOARCH", Value: "amd64"},
			},
		}
	}
	if err := validateGotestsumBuildInfo(exact(), "go1.26.5", "linux", "amd64"); err != nil {
		t.Fatalf("exact reporter identity rejected: %v", err)
	}

	cases := map[string]func(*debug.BuildInfo){
		"compiler":         func(info *debug.BuildInfo) { info.GoVersion = "go1.26.4" },
		"package path":     func(info *debug.BuildInfo) { info.Path = "example.invalid/spoof" },
		"module path":      func(info *debug.BuildInfo) { info.Main.Path = "example.invalid/spoof" },
		"module version":   func(info *debug.BuildInfo) { info.Main.Version = "v1.12.2" },
		"module checksum":  func(info *debug.BuildInfo) { info.Main.Sum = "h1:spoof" },
		"main replacement": func(info *debug.BuildInfo) { info.Main.Replace = &debug.Module{Path: "example.invalid/replacement"} },
		"dependency replacement": func(info *debug.BuildInfo) {
			info.Deps[0].Replace = &debug.Module{Path: "example.invalid/replacement"}
		},
		"cgo":    func(info *debug.BuildInfo) { info.Settings[0].Value = "1" },
		"goos":   func(info *debug.BuildInfo) { info.Settings[1].Value = "darwin" },
		"goarch": func(info *debug.BuildInfo) { info.Settings[2].Value = "arm64" },
		"duplicate setting": func(info *debug.BuildInfo) {
			info.Settings = append(info.Settings, debug.BuildSetting{Key: "GOOS", Value: "linux"})
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			info := exact()
			mutate(info)
			if err := validateGotestsumBuildInfo(info, "go1.26.5", "linux", "amd64"); err == nil {
				t.Fatal("inexact reporter identity accepted")
			}
		})
	}
	if err := validateGotestsumBuildInfo(nil, "go1.26.5", "linux", "amd64"); err == nil {
		t.Fatal("missing reporter build information accepted")
	}
}

func TestResolveGotestsumFailsClosedWithoutNetworkFallback(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	command, probe, err := resolveGotestsum(t.TempDir(), "", "", runtime.Version(), 100*time.Millisecond)
	if err == nil || command.name != "" || probe.ExitCode == 0 || !strings.Contains(err.Error(), "network fallback is disabled") {
		t.Fatalf("missing reporter resolution command=%+v probe=%+v err=%v", command, probe, err)
	}

	directory := t.TempDir()
	target := filepath.Join(directory, reporterExecutableName())
	if err := os.WriteFile(target, []byte("not a Go reporter\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "reporter-link")
	if err := os.Symlink(target, link); err == nil {
		command, probe, err = resolveGotestsum(directory, link, "", runtime.Version(), time.Second)
		if err == nil || command.name != "" || probe.ExitCode == 0 {
			t.Fatalf("symlink reporter accepted: command=%+v probe=%+v err=%v", command, probe, err)
		}
	}

	command, probe, err = resolveGotestsum(directory, target, "", runtime.Version(), time.Second)
	if err == nil || command.name != "" || probe.ExitCode == 0 || !strings.Contains(err.Error(), "build information") {
		t.Fatalf("spoofed reporter accepted: command=%+v probe=%+v err=%v", command, probe, err)
	}
}

func TestReporterChecksumFlagRequiresExplicitReporter(t *testing.T) {
	_, err := parseRunFlags([]string{
		"--phase", "candidate",
		"--reporter-checksum", "gotestsum.sha256",
	})
	if err == nil || !strings.Contains(err.Error(), "--reporter-checksum requires --reporter") {
		t.Fatalf("reporter checksum without reporter err=%v", err)
	}
}

func TestInvalidReporterFinalizesDeterministicFailureReports(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	reportsRoot, err := os.MkdirTemp(repoRoot, ".e2e-runner-reporter-failure-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(reportsRoot) })
	t.Setenv("GITHUB_STEP_SUMMARY", "")

	err = runSuite(runOptions{
		phase:              "candidate",
		binary:             filepath.Join(reportsRoot, "missing-env-vault"),
		reporter:           filepath.Join(reportsRoot, "missing-gotestsum"),
		reportsRoot:        reportsRoot,
		testPackage:        "./e2e",
		scenariosPath:      "e2e/scenarios.json",
		helperPackage:      "./e2e/missing-test-helper",
		commandTimeout:     5 * time.Second,
		testTimeout:        5 * time.Second,
		burnInCount:        3,
		lockingBurnInCount: 5,
		runnerOS:           runtime.GOOS,
	})
	if err == nil {
		t.Fatal("invalid reporter unexpectedly passed")
	}

	metadataMatches, err := filepath.Glob(filepath.Join(reportsRoot, "candidate", "*", "*", "metadata.json"))
	if err != nil || len(metadataMatches) != 1 {
		t.Fatalf("metadata matches=%v err=%v, want one finalized report", metadataMatches, err)
	}
	reportDir := filepath.Dir(metadataMatches[0])
	if err := validateReportArtifactsSyntax(reportDir); err != nil {
		t.Fatalf("invalid reporter left an incomplete report bundle: %v", err)
	}
	var metadata runMetadata
	if err := readJSON(metadataMatches[0], &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Status != "fail" {
		t.Fatalf("metadata status=%q, want fail", metadata.Status)
	}
	want := "coverage E2E was not started because the reporter prerequisite failed"
	for _, failure := range metadata.Failures {
		if strings.Contains(failure, "coverage E2E suite failed") {
			t.Fatalf("reporter failure was misclassified as a coverage execution: %q", failure)
		}
		if strings.Contains(failure, want) {
			return
		}
	}
	t.Fatalf("metadata failures do not contain %q: %v", want, metadata.Failures)
}
