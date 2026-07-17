package tests

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestE2EReporterToolGraphIsIsolatedAndExactlyPinned(t *testing.T) {
	productModule := readFile(t, "../go.mod")
	if strings.Contains(productModule, "gotest.tools/gotestsum") {
		t.Fatal("gotestsum tool graph leaked into the product module")
	}

	toolModule := readFile(t, "../tools/e2e-reporter/go.mod")
	if !strings.Contains(toolModule, "module github.com/ildarbinanas-design/env-vault/tools/e2e-reporter") {
		t.Fatal("isolated reporter module has the wrong module identity")
	}
	toolDirective := regexp.MustCompile(`(?m)^tool gotest\.tools/gotestsum$`)
	requireDirective := regexp.MustCompile(`(?m)^\s*gotest\.tools/gotestsum v1\.13\.0 // indirect$`)
	if len(toolDirective.FindAllString(toolModule, -1)) != 1 || len(requireDirective.FindAllString(toolModule, -1)) != 1 {
		t.Fatalf("tool module does not have one exact gotestsum v1.13.0 pin:\n%s", toolModule)
	}

	runnerTooling := readFile(t, "../e2e/cmd/e2e-runner/tooling.go")
	if !strings.Contains(runnerTooling, `gotestsumModuleVersion = "gotest.tools/gotestsum@v1.13.0"`) ||
		!strings.Contains(runnerTooling, `gotestsumVersion       = "v1.13.0"`) ||
		!strings.Contains(runnerTooling, `gotestsumModuleSum     = "h1:+Lh454O9mu9AMG1APV4o0y7oDYKyik/3kBOiCqiEpRo="`) {
		t.Fatal("E2E runner reporter identity differs from the isolated tool module")
	}

	toolSums := readFile(t, "../tools/e2e-reporter/go.sum")
	for _, required := range []string{
		"gotest.tools/gotestsum v1.13.0 h1:",
		"gotest.tools/gotestsum v1.13.0/go.mod h1:",
	} {
		if !strings.Contains(toolSums, required) {
			t.Fatalf("isolated tool checksum file is missing %q", required)
		}
	}
}

func TestE2EReporterBuilderIsExecutableAndFailClosed(t *testing.T) {
	path := filepath.Join("..", "scripts", "release", "build-e2e-reporters.sh")
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode()&0o111 == 0 {
		t.Fatalf("reporter builder must be an executable regular non-symlink file: %v", info.Mode())
	}

	build := readFile(t, path)
	for _, required := range []string{
		"for download_attempt in 1 2 3",
		"timeout --foreground --signal=TERM --kill-after=15s 2m",
		"run_bounded_tool_go mod download",
		"run_bounded_tool_go mod tidy -diff",
		"vendor directory is not allowed",
		"GOFLAGS=",
		"GOPROXY=off",
		"CGO_ENABLED=0",
		"-mod=readonly",
		"-buildvcs=false",
		"-ldflags='-s -w'",
		`$5 == sum`,
		"reporter build contains replacement metadata",
		"tool module Go version does not match the active exact toolchain",
		"sha256sum",
	} {
		if !strings.Contains(build, required) {
			t.Fatalf("reporter builder is missing %q", required)
		}
	}
	if strings.Contains(build, "GOSUMDB=off") {
		t.Fatal("reporter builder disabled checksum database verification")
	}

	resolver := readFile(t, "../e2e/cmd/e2e-runner/reporter.go")
	if strings.Contains(resolver, "gotestsumModuleVersion") || !strings.Contains(resolver, "network fallback is disabled") {
		t.Fatal("E2E reporter resolver retained a module-cache or network fallback")
	}
}
