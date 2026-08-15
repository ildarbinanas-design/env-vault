package tests

import (
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

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
		"actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1":                 false,
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
