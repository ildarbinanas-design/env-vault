package tests

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCanonicalE2EBaselineIdentityIsPinned(t *testing.T) {
	data, err := os.ReadFile("../docs/e2e-baseline.json")
	if err != nil {
		t.Fatalf("read canonical baseline: %v", err)
	}
	var baseline struct {
		SchemaVersion            int    `json:"schema_version"`
		Phase                    string `json:"phase"`
		Repository               string `json:"repository"`
		CommitSHA                string `json:"commit_sha"`
		RunID                    string `json:"run_id"`
		RunURL                   string `json:"run_url"`
		RunAttempt               string `json:"run_attempt"`
		GoVersion                string `json:"go_version"`
		GotestsumVersion         string `json:"gotestsum_version"`
		SuiteHash                string `json:"suite_hash"`
		MinimumArtifactExpiresAt string `json:"minimum_artifact_expires_at"`
		Platforms                map[string]struct {
			Passed            int      `json:"passed"`
			Failed            int      `json:"failed"`
			Skipped           int      `json:"skipped"`
			ExpectedSkips     []string `json:"expected_skips"`
			StatementCoverage float64  `json:"statement_coverage"`
			BinarySHA256      string   `json:"binary_sha256"`
			ArtifactID        int64    `json:"artifact_id"`
			ArtifactExpiresAt string   `json:"artifact_expires_at"`
			ArtifactSHA256    string   `json:"artifact_sha256"`
		} `json:"platforms"`
	}
	if err := json.Unmarshal(data, &baseline); err != nil {
		t.Fatalf("parse canonical baseline: %v", err)
	}
	if baseline.SchemaVersion != 1 || baseline.Phase != "baseline" || baseline.Repository != "ildarbinanas-design/env-vault" || baseline.CommitSHA != "7a044bdbf73aa592016bbb3a02d81f314f08fe63" || baseline.RunID != "29441160687" || baseline.RunURL != "https://github.com/ildarbinanas-design/env-vault/actions/runs/29441160687" || baseline.RunAttempt != "1" || baseline.GoVersion != "go1.22.12" || baseline.GotestsumVersion != "v1.12.2" || baseline.SuiteHash != "ace01466c8b504af9a1a2af2ec2ba3bcd9446e637044d94b4ce7d5dffa842fcf" || baseline.MinimumArtifactExpiresAt != "2026-08-14T18:37:53Z" {
		t.Fatalf("canonical baseline identity is incomplete or changed: %+v", baseline)
	}
	wantPlatforms := map[string]bool{"linux-amd64": false, "linux-arm64": false, "darwin-amd64": false, "darwin-arm64": false, "windows-amd64": false}
	for platform, report := range baseline.Platforms {
		if _, ok := wantPlatforms[platform]; !ok {
			t.Fatalf("unexpected canonical baseline platform %q", platform)
		}
		if report.Failed != 0 || report.Passed == 0 || report.StatementCoverage < 60 || len(report.BinarySHA256) != 64 || report.ArtifactID == 0 || report.ArtifactExpiresAt == "" || len(report.ArtifactSHA256) != 64 {
			t.Fatalf("invalid canonical baseline report for %s: %+v", platform, report)
		}
		if platform == "windows-amd64" {
			if report.Skipped != 2 || !slices.Equal(report.ExpectedSkips, []string{"EXEC_SIGNAL_FORWARDING", "PROFILE_SYMLINK_REJECTED"}) {
				t.Fatalf("Windows expected skips=%v count=%d", report.ExpectedSkips, report.Skipped)
			}
		} else if report.Skipped != 0 || len(report.ExpectedSkips) != 0 {
			t.Fatalf("unexpected non-Windows skips for %s: %+v", platform, report)
		}
		wantPlatforms[platform] = true
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
		"actions/checkout@v7":                 false,
		"actions/dependency-review-action@v5": false,
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
