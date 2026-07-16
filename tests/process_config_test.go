package tests

import (
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/ildarbinanas-design/env-vault/internal/e2ebaseline"
	"github.com/ildarbinanas-design/env-vault/internal/releasecontract"
	"gopkg.in/yaml.v3"
)

func TestCanonicalE2EBaselineIdentityIsPinned(t *testing.T) {
	contract, err := releasecontract.LoadFile("../release/contract.v1.json")
	if err != nil {
		t.Fatalf("load release contract: %v", err)
	}
	baseline, err := e2ebaseline.LoadFile("../docs/e2e-baseline.json", contract)
	if err != nil {
		t.Fatalf("strictly load canonical baseline: %v", err)
	}
	if baseline.SchemaID != e2ebaseline.SchemaID || baseline.SchemaVersion != e2ebaseline.SchemaVersion ||
		baseline.Provenance.Repository != "ildarbinanas-design/env-vault" ||
		baseline.Provenance.CommitSHA != "054d7b1c3f1c3a63e8a2ed162f72f3ad2f28a9b9" ||
		baseline.Provenance.RunID != "29526068945" ||
		baseline.Provenance.RunURL != "https://github.com/ildarbinanas-design/env-vault/actions/runs/29526068945" ||
		baseline.Provenance.RunAttempt != "1" || baseline.Provenance.Phase != "candidate" ||
		baseline.Toolchain.GoVersion != "go1.26.5" || baseline.Toolchain.GotestsumVersion != "v1.13.0" ||
		baseline.SemanticSuite.SourceReportHash != "edf35d2b5f2c69e61ebb2aa58226ceba27e55826ebe694710fb2974737d096f1" ||
		baseline.SemanticSuite.TransitionCode != "" || baseline.Migration != nil {
		t.Fatalf("canonical baseline identity is incomplete or changed: %+v", baseline)
	}
	wantPlatforms := map[string]bool{"linux-amd64": false, "linux-arm64": false, "darwin-amd64": false, "darwin-arm64": false, "windows-amd64": false}
	for _, report := range baseline.Platforms {
		if _, ok := wantPlatforms[report.ID]; !ok {
			t.Fatalf("unexpected canonical baseline platform %q", report.ID)
		}
		if report.Counts.Failed != 0 || report.Counts.Missing != 0 || report.Counts.Passed == 0 || report.CoverageFloorPercent < 60 || len(report.ContractSHA256) != 64 || report.Leak.Status != "pass" || report.Leak.Detected || report.Leak.RegistryRecords != 130 {
			t.Fatalf("invalid canonical baseline report for %s: %+v", report.ID, report)
		}
		if report.ID == "windows-amd64" {
			if report.Counts.Skipped != 2 || !slices.Equal(report.ExpectedSkips, []string{"EXEC_SIGNAL_FORWARDING", "PROFILE_SYMLINK_REJECTED"}) {
				t.Fatalf("Windows expected skips=%v count=%d", report.ExpectedSkips, report.Counts.Skipped)
			}
		} else if report.Counts.Skipped != 0 || len(report.ExpectedSkips) != 0 {
			t.Fatalf("unexpected non-Windows skips for %s: %+v", report.ID, report)
		}
		wantPlatforms[report.ID] = true
	}
	for platform, found := range wantPlatforms {
		if !found {
			t.Fatalf("canonical baseline missing %s", platform)
		}
	}
}

func TestLocalConfigAndTransactionLockAreIgnored(t *testing.T) {
	data, err := os.ReadFile("../.gitignore")
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	lines := make(map[string]bool)
	for _, line := range strings.Split(string(data), "\n") {
		lines[strings.TrimSpace(line)] = true
	}
	for _, path := range []string{".env-vault.yaml", ".env-vault.yaml.lock"} {
		if !lines[path] {
			t.Fatalf(".gitignore must contain exact local runtime path %q", path)
		}
	}
}

type dependabotConfig struct {
	Version int `yaml:"version"`
	Updates []struct {
		PackageEcosystem   string `yaml:"package-ecosystem"`
		Directory          string `yaml:"directory"`
		VersioningStrategy string `yaml:"versioning-strategy"`
		Schedule           struct {
			Interval string `yaml:"interval"`
		} `yaml:"schedule"`
		Groups map[string]struct {
			AppliesTo       string   `yaml:"applies-to"`
			Patterns        []string `yaml:"patterns"`
			ExcludePatterns []string `yaml:"exclude-patterns"`
			UpdateTypes     []string `yaml:"update-types"`
		} `yaml:"groups"`
	} `yaml:"updates"`
}

func TestDependabotCoversGoModulesAndGitHubActions(t *testing.T) {
	data, err := os.ReadFile("../.github/dependabot.yml")
	if err != nil {
		t.Fatalf("read Dependabot config: %v", err)
	}
	var config dependabotConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse Dependabot config: %v", err)
	}
	if config.Version != 2 {
		t.Fatalf("Dependabot version=%d, want 2", config.Version)
	}
	want := map[string]bool{"gomod": false, "github-actions": false}
	for _, update := range config.Updates {
		if _, ok := want[update.PackageEcosystem]; !ok {
			continue
		}
		if update.Directory != "/" || update.Schedule.Interval != "weekly" || len(update.Groups) == 0 {
			t.Fatalf("Dependabot %s directory=%q interval=%q groups=%v", update.PackageEcosystem, update.Directory, update.Schedule.Interval, update.Groups)
		}
		if update.PackageEcosystem == "gomod" {
			if update.VersioningStrategy != "increase-if-necessary" {
				t.Fatalf("Dependabot gomod versioning-strategy=%q", update.VersioningStrategy)
			}
			group, ok := update.Groups["go-modules-minor-patch"]
			if !ok || group.AppliesTo != "version-updates" || !slices.Equal(group.Patterns, []string{"*"}) || !slices.Equal(group.UpdateTypes, []string{"minor", "patch"}) {
				t.Fatalf("Dependabot gomod group=%+v, want isolated minor/patch version updates", group)
			}
			for _, dependency := range []string{"github.com/gofrs/flock", "golang.org/x/term", "golang.org/x/sys"} {
				if !slices.Contains(group.ExcludePatterns, dependency) {
					t.Fatalf("Dependabot broad group must exclude toolchain-sensitive %s", dependency)
				}
			}
		}
		want[update.PackageEcosystem] = true
	}
	for ecosystem, found := range want {
		if !found {
			t.Fatalf("Dependabot missing %s updates", ecosystem)
		}
	}
}

func TestDependencyReviewUsesCurrentNode24Action(t *testing.T) {
	data, err := os.ReadFile("../.github/workflows/dependency-review.yml")
	if err != nil {
		t.Fatalf("read dependency review workflow: %v", err)
	}
	var config struct {
		On          map[string]any    `yaml:"on"`
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			Steps []workflowStep `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatalf("parse dependency review workflow: %v", err)
	}
	if _, ok := config.On["pull_request"]; !ok || len(config.On) != 1 {
		t.Fatalf("dependency review triggers=%v, want pull_request only", config.On)
	}
	if len(config.Permissions) != 1 || config.Permissions["contents"] != "read" {
		t.Fatalf("dependency review permissions=%v", config.Permissions)
	}
	job, ok := config.Jobs["dependency-review"]
	if !ok {
		t.Fatal("dependency review job missing")
	}
	wantUses := map[string]bool{
		"actions/checkout@9c091bb21b7c1c1d1991bb908d89e4e9dddfe3e0":                 false,
		"actions/dependency-review-action@a1d282b36b6f3519aa1f3fc636f609c47dddb294": false,
	}
	for _, step := range job.Steps {
		if _, ok := wantUses[step.Uses]; ok {
			wantUses[step.Uses] = true
		}
	}
	for uses, found := range wantUses {
		if !found {
			t.Fatalf("dependency review missing %s", uses)
		}
	}
}
