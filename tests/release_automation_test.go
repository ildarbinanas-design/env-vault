package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	manifestV007  = "{\n  \".\": \"0.0.7\"\n}\n"
	readmeV007    = "# fixture\n\nCurrent stable release: `v0.0.7`. <!-- x-release-please-version -->\n"
	changelogV007 = "# Changelog\n\n" +
		"## [0.0.7](https://example.invalid/compare/v0.0.6...v0.0.7) (2026-07-15)\n\n" +
		"- Previous release.\n"
)

func TestClassifyReleaseCommit(t *testing.T) {
	t.Run("ordinary green main commit is planning only", func(t *testing.T) {
		repo := newReleaseAutomationRepo(t)
		writeReleaseFixture(t, repo, "code.txt", "changed\n")
		gitFixture(t, repo, "add", "code.txt")
		gitFixture(t, repo, "commit", "-m", "fix: ordinary change")

		output, err := runReleaseAutomationScript(t, repo, "classify-release-commit.sh", "HEAD")
		if err != nil {
			t.Fatalf("classify ordinary commit: %v\n%s", err, output)
		}
		if !strings.Contains(output, "publish=false\n") || !strings.Contains(output, "version=\n") {
			t.Fatalf("ordinary classification=%q", output)
		}
	})

	t.Run("reviewed release commit returns exact tag inputs", func(t *testing.T) {
		repo := newReleaseAutomationRepo(t)
		commitReleaseFixture(t, repo, releaseMutation{})

		output, err := runReleaseAutomationScript(t, repo, "classify-release-commit.sh", "HEAD")
		if err != nil {
			t.Fatalf("classify release commit: %v\n%s", err, output)
		}
		if !strings.Contains(output, "publish=true\n") || !strings.Contains(output, "version=v0.0.8\n") {
			t.Fatalf("release classification=%q", output)
		}
		sha := strings.TrimSpace(gitFixture(t, repo, "rev-parse", "HEAD"))
		if !strings.Contains(output, "source_sha="+sha+"\n") {
			t.Fatalf("release classification does not bind exact SHA: %q", output)
		}
	})

	cases := []struct {
		name     string
		mutation releaseMutation
	}{
		{name: "non-release subject", mutation: releaseMutation{subject: "chore: update files"}},
		{name: "version downgrade", mutation: releaseMutation{version: "0.0.6"}},
		{name: "stale README", mutation: releaseMutation{staleREADME: true}},
		{name: "missing changelog section", mutation: releaseMutation{missingChangelog: true}},
		{name: "empty changelog section", mutation: releaseMutation{emptyChangelog: true}},
		{name: "unexpected source path", mutation: releaseMutation{unexpectedPath: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name+" fails closed", func(t *testing.T) {
			repo := newReleaseAutomationRepo(t)
			commitReleaseFixture(t, repo, tc.mutation)
			output, err := runReleaseAutomationScript(t, repo, "classify-release-commit.sh", "HEAD")
			if err == nil {
				t.Fatalf("classification unexpectedly succeeded: %s", output)
			}
			if !strings.Contains(output, "release:") {
				t.Fatalf("classification failure is not structured: %q", output)
			}
		})
	}
}

func TestExtractChangelogSection(t *testing.T) {
	directory := t.TempDir()
	changelog := filepath.Join(directory, "CHANGELOG.md")
	writeReleaseFixture(t, directory, "CHANGELOG.md", "# Changelog\n\n"+
		"## [0.0.8](https://example.invalid/compare/v0.0.7...v0.0.8) (2026-07-16)\n\n"+
		"### Bug Fixes\n\n- Automate releases.\n\n"+
		"## [0.0.7](https://example.invalid/compare/v0.0.6...v0.0.7) (2026-07-15)\n\n- Previous release.\n")

	output, err := runReleaseAutomationScript(t, directory, "extract-changelog-section.sh", "v0.0.8", changelog)
	if err != nil {
		t.Fatalf("extract changelog: %v\n%s", err, output)
	}
	want := "### Bug Fixes\n\n- Automate releases.\n"
	if output != want {
		t.Fatalf("release notes=%q, want %q", output, want)
	}

	if output, err := runReleaseAutomationScript(t, directory, "extract-changelog-section.sh", "v0.0.9", changelog); err == nil {
		t.Fatalf("missing changelog section unexpectedly succeeded: %s", output)
	}
	writeReleaseFixture(t, directory, "CHANGELOG.md", "# Changelog\n\n## [0.0.8]\n\n- One.\n\n## [0.0.8]\n\n- Two.\n")
	if output, err := runReleaseAutomationScript(t, directory, "extract-changelog-section.sh", "v0.0.8", changelog); err == nil {
		t.Fatalf("duplicate changelog section unexpectedly succeeded: %s", output)
	}
}

func TestCurrentReleaseHasExtractableReviewedNotes(t *testing.T) {
	changelog, err := filepath.Abs("../CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	output, err := runReleaseAutomationScript(t, t.TempDir(), "extract-changelog-section.sh", "v0.0.7", changelog)
	if err != nil {
		t.Fatalf("extract current release notes: %v\n%s", err, output)
	}
	if !strings.Contains(output, "fix(security): harden runtime and Homebrew release contract") {
		t.Fatalf("current release notes are incomplete: %q", output)
	}
}

func TestVerifyReleaseAuthorization(t *testing.T) {
	const sourceSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	commandDir := installFakeReleaseGH(t)
	baseEnv := []string{
		"GITHUB_REPOSITORY=example/env-vault",
		"RELEASE_APP_SLUG=env-vault-release-planning",
		"FAKE_SOURCE_SHA=" + sourceSHA,
		"FAKE_MAIN_SHA=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"FAKE_MANIFEST_VERSION=0.0.8",
		"FAKE_COMPARE_STATUS=ahead",
		"FAKE_CI_CONCLUSION=success",
		"FAKE_PR_AUTHOR=env-vault-release-planning[bot]",
		"FAKE_PR_LABEL=autorelease: pending",
		"PATH=" + commandDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}

	output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), baseEnv, "verify-release-authorization.sh", "v0.0.8", sourceSHA, "prepublish")
	if err != nil {
		t.Fatalf("verify valid release authorization: %v\n%s", err, output)
	}
	if output != "42\n" {
		t.Fatalf("authorized PR=%q, want 42", output)
	}
	taggedEnv := append([]string{}, baseEnv...)
	taggedEnv = append(taggedEnv, "FAKE_PR_LABEL=autorelease: tagged")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), taggedEnv, "verify-release-authorization.sh", "v0.0.8", sourceSHA, "tagged"); err != nil || output != "42\n" {
		t.Fatalf("verify tagged release authorization: %v\n%s", err, output)
	}
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), baseEnv, "verify-release-authorization.sh", "v0.0.8", sourceSHA, "tagged"); err == nil {
		t.Fatalf("pending release unexpectedly authorized for publication: %s", output)
	}

	cases := []struct {
		name     string
		override string
	}{
		{name: "stale manifest", override: "FAKE_MANIFEST_VERSION=0.0.9"},
		{name: "diverged commit", override: "FAKE_COMPARE_STATUS=diverged"},
		{name: "failed ci", override: "FAKE_CI_CONCLUSION=failure"},
		{name: "wrong App author", override: "FAKE_PR_AUTHOR=github-actions[bot]"},
		{name: "missing lifecycle label", override: "FAKE_PR_LABEL=triage"},
	}
	for _, tc := range cases {
		t.Run(tc.name+" fails closed", func(t *testing.T) {
			env := append([]string{}, baseEnv...)
			env = append(env, tc.override)
			output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), env, "verify-release-authorization.sh", "v0.0.8", sourceSHA, "prepublish")
			if err == nil {
				t.Fatalf("authorization unexpectedly succeeded: %s", output)
			}
			if !strings.Contains(output, "release:") {
				t.Fatalf("authorization failure is not structured: %q", output)
			}
		})
	}
}

func TestVerifyReleaseProposal(t *testing.T) {
	const (
		headSHA   = "cccccccccccccccccccccccccccccccccccccccc"
		parentSHA = "dddddddddddddddddddddddddddddddddddddddd"
		mainSHA   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	commandDir := installFakeReleaseGH(t)
	baseEnv := []string{
		"GITHUB_REPOSITORY=example/env-vault",
		"RELEASE_APP_SLUG=env-vault-release-planning",
		"FAKE_PROPOSAL_HEAD_SHA=" + headSHA,
		"FAKE_PROPOSAL_PARENT_SHA=" + parentSHA,
		"FAKE_MAIN_SHA=" + mainSHA,
		"FAKE_COMPARE_STATUS=ahead",
		"FAKE_CI_HEAD_SHA=" + parentSHA,
		"FAKE_CI_CONCLUSION=success",
		"FAKE_MANIFEST_VERSION=0.0.8",
		"FAKE_PR_AUTHOR=env-vault-release-planning[bot]",
		"FAKE_PR_LABEL=autorelease: pending",
		"PATH=" + commandDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}

	output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), baseEnv, "verify-release-proposal.sh")
	if err != nil {
		t.Fatalf("verify valid release proposal: %v\n%s", err, output)
	}
	for _, line := range []string{"proposal=true\n", "proposal_sha=" + headSHA + "\n", "proposal_base_sha=" + parentSHA + "\n", "version=v0.0.8\n"} {
		if !strings.Contains(output, line) {
			t.Fatalf("release proposal output %q missing %q", output, line)
		}
	}

	badCIEnv := append([]string{}, baseEnv...)
	badCIEnv = append(badCIEnv, "FAKE_CI_CONCLUSION=failure")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), badCIEnv, "verify-release-proposal.sh"); err == nil {
		t.Fatalf("proposal based on failed main unexpectedly succeeded: %s", output)
	}

	badPathsEnv := append([]string{}, baseEnv...)
	badPathsEnv = append(badPathsEnv, "FAKE_PROPOSAL_UNEXPECTED_PATH=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), badPathsEnv, "verify-release-proposal.sh"); err == nil {
		t.Fatalf("proposal with unexpected path unexpectedly succeeded: %s", output)
	}
}

func TestMarkReleasePullRequestTagged(t *testing.T) {
	commandDir := installFakeReleaseGH(t)
	state := filepath.Join(t.TempDir(), "labels.json")
	if err := os.WriteFile(state, []byte(`[{"name":"autorelease: pending"},{"name":"documentation"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"GITHUB_REPOSITORY=example/env-vault",
		"FAKE_LABEL_STATE=" + state,
		"PATH=" + commandDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), env, "mark-release-pr-tagged.sh", "42")
	if err != nil {
		t.Fatalf("mark release PR tagged: %v\n%s", err, output)
	}
	data, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(data)); got != `[{"name":"documentation"},{"name":"autorelease: tagged"}]` {
		t.Fatalf("final labels=%s", got)
	}

	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), env, "mark-release-pr-tagged.sh", "42"); err != nil {
		t.Fatalf("idempotent label reconciliation: %v\n%s", err, output)
	}
}

func TestVerifyRepositoryReleaseSettings(t *testing.T) {
	commandDir := installFakeReleaseGH(t)
	baseEnv := []string{
		"GITHUB_REPOSITORY=example/env-vault",
		"PATH=" + commandDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), baseEnv, "verify-repository-release-settings.sh"); err != nil {
		t.Fatalf("verify valid repository settings: %v\n%s", err, output)
	}

	badEnv := append([]string{}, baseEnv...)
	badEnv = append(badEnv, "FAKE_ALLOW_REBASE=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), badEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("unsafe repository settings unexpectedly succeeded: %s", output)
	}

	missingRepositoryEnv := append([]string{}, baseEnv...)
	missingRepositoryEnv = append(missingRepositoryEnv, "FAKE_GRAPHQL_REPOSITORY_NULL=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), missingRepositoryEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("missing GraphQL repository unexpectedly succeeded: %s", output)
	}

	failedGraphQLEnv := append([]string{}, baseEnv...)
	failedGraphQLEnv = append(failedGraphQLEnv, "FAKE_GRAPHQL_FAIL=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), failedGraphQLEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("failed GraphQL settings read unexpectedly succeeded: %s", output)
	}

	partialGraphQLEnv := append([]string{}, baseEnv...)
	partialGraphQLEnv = append(partialGraphQLEnv, "FAKE_GRAPHQL_ERRORS=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), partialGraphQLEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("GraphQL response with errors unexpectedly succeeded: %s", output)
	}

	malformedGraphQLEnv := append([]string{}, baseEnv...)
	malformedGraphQLEnv = append(malformedGraphQLEnv, "FAKE_GRAPHQL_MALFORMED_ERRORS=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), malformedGraphQLEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("GraphQL response with malformed errors unexpectedly succeeded: %s", output)
	}

	badRulesetEnv := append([]string{}, baseEnv...)
	badRulesetEnv = append(badRulesetEnv, "FAKE_RULESET_ALLOW_REBASE=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), badRulesetEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("unsafe main ruleset unexpectedly succeeded: %s", output)
	}

	missingTitleCheckEnv := append([]string{}, baseEnv...)
	missingTitleCheckEnv = append(missingTitleCheckEnv, "FAKE_RULESET_OMIT_PR_TITLE=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), missingTitleCheckEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("main ruleset without required pr-title unexpectedly succeeded: %s", output)
	}

	badAppBypassEnv := append([]string{}, baseEnv...)
	badAppBypassEnv = append(badAppBypassEnv, "FAKE_RULESET_BYPASS=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), badAppBypassEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("release App ruleset bypass unexpectedly succeeded: %s", output)
	}

	badTagRulesetEnv := append([]string{}, baseEnv...)
	badTagRulesetEnv = append(badTagRulesetEnv, "FAKE_TAG_RULESET_ALLOW_UPDATE=true")
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), badTagRulesetEnv, "verify-repository-release-settings.sh"); err == nil {
		t.Fatalf("mutable release tag ruleset unexpectedly succeeded: %s", output)
	}
}

func TestEnsureReleaseLifecycleLabels(t *testing.T) {
	commandDir := installFakeReleaseGH(t)
	logPath := filepath.Join(t.TempDir(), "labels.log")
	env := []string{
		"GITHUB_REPOSITORY=example/env-vault",
		"FAKE_LABEL_CALL_LOG=" + logPath,
		"PATH=" + commandDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
	if output, err := runReleaseAutomationScriptEnv(t, t.TempDir(), env, "ensure-release-labels.sh"); err != nil {
		t.Fatalf("ensure release labels: %v\n%s", err, output)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	log := string(data)
	for _, snippet := range []string{
		"label create autorelease: pending",
		"label create autorelease: tagged",
		"labels/autorelease%3A%20pending",
		"labels/autorelease%3A%20tagged",
	} {
		if !strings.Contains(log, snippet) {
			t.Fatalf("release label bootstrap log missing %q: %s", snippet, log)
		}
	}
}

type releaseMutation struct {
	version          string
	subject          string
	staleREADME      bool
	missingChangelog bool
	emptyChangelog   bool
	unexpectedPath   bool
}

func newReleaseAutomationRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	gitFixture(t, repo, "init")
	gitFixture(t, repo, "config", "user.name", "Release Test")
	gitFixture(t, repo, "config", "user.email", "release-test@example.invalid")
	writeReleaseFixture(t, repo, ".release-please-manifest.json", manifestV007)
	writeReleaseFixture(t, repo, "README.md", readmeV007)
	writeReleaseFixture(t, repo, "CHANGELOG.md", changelogV007)
	writeReleaseFixture(t, repo, "code.txt", "initial\n")
	gitFixture(t, repo, "add", ".")
	gitFixture(t, repo, "commit", "-m", "chore: bootstrap")
	return repo
}

func commitReleaseFixture(t *testing.T, repo string, mutation releaseMutation) {
	t.Helper()
	version := mutation.version
	if version == "" {
		version = "0.0.8"
	}
	subject := mutation.subject
	if subject == "" {
		subject = "chore(main): release env-vault v" + version
	}
	writeReleaseFixture(t, repo, ".release-please-manifest.json", "{\n  \".\": \""+version+"\"\n}\n")
	if mutation.staleREADME {
		writeReleaseFixture(t, repo, "README.md", readmeV007+"\n")
	} else {
		writeReleaseFixture(t, repo, "README.md", "# fixture\n\nCurrent stable release: `v"+version+"`. <!-- x-release-please-version -->\n")
	}
	if mutation.missingChangelog {
		writeReleaseFixture(t, repo, "CHANGELOG.md", changelogV007+"\n")
	} else if mutation.emptyChangelog {
		writeReleaseFixture(t, repo, "CHANGELOG.md", "# Changelog\n\n## ["+version+"](https://example.invalid/release) (2026-07-16)\n\n"+strings.TrimPrefix(changelogV007, "# Changelog\n\n"))
	} else {
		writeReleaseFixture(t, repo, "CHANGELOG.md", "# Changelog\n\n## ["+version+"](https://example.invalid/release) (2026-07-16)\n\n- Release.\n\n"+strings.TrimPrefix(changelogV007, "# Changelog\n\n"))
	}
	if mutation.unexpectedPath {
		writeReleaseFixture(t, repo, "code.txt", "unexpected\n")
	}
	gitFixture(t, repo, "add", ".")
	gitFixture(t, repo, "commit", "-m", subject)
}

func writeReleaseFixture(t *testing.T, directory, relative, contents string) {
	t.Helper()
	path := filepath.Join(directory, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitFixture(t *testing.T, directory string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = directory
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func runReleaseAutomationScript(t *testing.T, directory, script string, args ...string) (string, error) {
	return runReleaseAutomationScriptEnv(t, directory, nil, script, args...)
}

func runReleaseAutomationScriptEnv(t *testing.T, directory string, extraEnv []string, script string, args ...string) (string, error) {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "scripts", "release", script))
	if err != nil {
		t.Fatal(err)
	}
	commandArgs := append([]string{filepath.ToSlash(path)}, args...)
	cmd := exec.Command("bash", commandArgs...)
	cmd.Dir = directory
	cmd.Env = append(os.Environ(), extraEnv...)
	output, runErr := cmd.CombinedOutput()
	return string(output), runErr
}

func installFakeReleaseGH(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	script := `#!/usr/bin/env bash
set -euo pipefail
args="$*"

if [[ "$args" == *"issues/42/labels"* ]]; then
  state="${FAKE_LABEL_STATE:?}"
  if [[ "$args" == *"--method POST"* ]]; then
    printf '%s\n' '[{"name":"autorelease: pending"},{"name":"documentation"},{"name":"autorelease: tagged"}]' > "$state"
    exit 0
  fi
  if [[ "$args" == *"--method DELETE"* ]]; then
    printf '%s\n' '[{"name":"documentation"},{"name":"autorelease: tagged"}]' > "$state"
    exit 0
  fi
  cat "$state"
  exit 0
fi

if [[ "$args" == label\ create* ]]; then
  printf '%s\n' "$args" >> "${FAKE_LABEL_CALL_LOG:?}"
  exit 0
fi
if [[ "$args" == *"labels/autorelease%3A%20pending"* ]]; then
  printf '%s\n' "$args" >> "${FAKE_LABEL_CALL_LOG:?}"
  printf '%s\n' $'autorelease: pending\tfbca04\tRelease Please proposal awaiting reviewed publication'
  exit 0
fi
if [[ "$args" == *"labels/autorelease%3A%20tagged"* ]]; then
  printf '%s\n' "$args" >> "${FAKE_LABEL_CALL_LOG:?}"
  printf '%s\n' $'autorelease: tagged\t0e8a16\tReviewed Release Please proposal with an exact release tag'
  exit 0
fi

case "$args" in
  *"api graphql"*)
    for required in \
      '-f owner=example' \
      '-f name=env-vault' \
      'defaultBranchRef' \
      'mergeCommitAllowed' \
      'rebaseMergeAllowed' \
      'squashMergeAllowed' \
      'squashMergeCommitTitle' \
      'squashMergeCommitMessage'; do
      if [[ "$args" != *"$required"* ]]; then
        printf 'GraphQL request missing %s\n' "$required" >&2
        exit 1
      fi
    done
    if [[ "${FAKE_GRAPHQL_FAIL:-false}" == "true" ]]; then
      printf '%s\n' 'GraphQL request failed' >&2
      exit 1
    fi
    if [[ "${FAKE_GRAPHQL_REPOSITORY_NULL:-false}" == "true" ]]; then
      printf '%s\n' '{"data":{"repository":null}}'
      exit 0
    fi
    if [[ "${FAKE_GRAPHQL_MALFORMED_ERRORS:-false}" == "true" ]]; then
      errors=',"errors":{}'
    elif [[ "${FAKE_GRAPHQL_ERRORS:-false}" == "true" ]]; then
      errors=',"errors":[{"message":"partial response"}]'
    else
      errors=''
    fi
    printf '{"data":{"repository":{"defaultBranchRef":{"name":"main"},"squashMergeAllowed":true,"mergeCommitAllowed":false,"rebaseMergeAllowed":%s,"squashMergeCommitTitle":"PR_TITLE","squashMergeCommitMessage":"PR_BODY"}}%s}\n' "${FAKE_ALLOW_REBASE:-false}" "$errors"
    ;;
  "api repos/example/env-vault")
    printf '{"default_branch":"main","allow_squash_merge":true,"allow_merge_commit":false,"allow_rebase_merge":%s,"squash_merge_commit_title":"PR_TITLE","squash_merge_commit_message":"PR_BODY"}\n' "${FAKE_ALLOW_REBASE:-false}"
    ;;
  *"rulesets?per_page=100"*)
    printf '[[{"id":7,"name":"Protect env-vault main","target":"branch","source_type":"Repository","enforcement":"active"},{"id":8,"name":"Protect env-vault release tags","target":"tag","source_type":"Repository","enforcement":"active"}]]\n'
    ;;
  *"rulesets/7"*)
    if [[ "${FAKE_RULESET_ALLOW_REBASE:-false}" == "true" ]]; then
      merge_methods='["squash","rebase"]'
    else
      merge_methods='["squash"]'
    fi
    if [[ "${FAKE_RULESET_BYPASS:-false}" == "true" ]]; then
      bypass='[{"actor_id":1,"actor_type":"Integration","bypass_mode":"always"}]'
      can_bypass='always'
    else
      bypass='[]'
      can_bypass='never'
    fi
    if [[ "${FAKE_RULESET_OMIT_PR_TITLE:-false}" == "true" ]]; then
      pr_title_integration_id=0
    else
      pr_title_integration_id=15368
    fi
    printf '{"id":7,"name":"Protect env-vault main","target":"branch","source_type":"Repository","source":"example/env-vault","enforcement":"active","bypass_actors":%s,"current_user_can_bypass":"%s","conditions":{"ref_name":{"exclude":[],"include":["refs/heads/main"]}},"rules":[{"type":"deletion"},{"type":"non_fast_forward"},{"type":"pull_request","parameters":{"required_review_thread_resolution":true,"allowed_merge_methods":%s}},{"type":"required_status_checks","parameters":{"strict_required_status_checks_policy":true,"do_not_enforce_on_create":false,"required_status_checks":[{"context":"quality-gate","integration_id":15368},{"context":"pr-title","integration_id":%s},{"context":"Dependency review","integration_id":15368},{"context":"Analyze (go)","integration_id":15368},{"context":"Analyze (actions)","integration_id":15368}]}}]}\n' "$bypass" "$can_bypass" "$merge_methods" "$pr_title_integration_id"
    ;;
  *"rulesets/8"*)
    if [[ "${FAKE_TAG_RULESET_ALLOW_UPDATE:-false}" == "true" ]]; then
      tag_rules='[{"type":"deletion"}]'
    else
      tag_rules='[{"type":"deletion"},{"type":"update"}]'
    fi
    printf '{"id":8,"name":"Protect env-vault release tags","target":"tag","source_type":"Repository","source":"example/env-vault","enforcement":"active","bypass_actors":[],"current_user_can_bypass":"never","conditions":{"ref_name":{"exclude":[],"include":["refs/tags/v*"]}},"rules":%s}\n' "$tag_rules"
    ;;
  *"api --paginate --slurp --method GET repos/example/env-vault/pulls"*)
    printf '[[{"number":43,"base":{"ref":"main","repo":{"full_name":"example/env-vault"}},"head":{"ref":"release-please--branches--main--components--env-vault","sha":"%s","repo":{"full_name":"example/env-vault"}},"user":{"login":"%s"},"title":"chore(main): release env-vault v0.0.8","body":"Merging this reviewed pull request authorizes publication of this exact version after the merge commit passes main CI. This PR was generated with Release Please.","labels":[{"name":"%s"}]}]]\n' \
      "${FAKE_PROPOSAL_HEAD_SHA:?}" "${FAKE_PR_AUTHOR:?}" "${FAKE_PR_LABEL:?}"
    ;;
  *"git/commits/"*)
    jq -cn --arg parent "${FAKE_PROPOSAL_PARENT_SHA:?}" '{message:"chore(main): release env-vault v0.0.8\n\nThis PR was generated with Release Please.",tree:{sha:"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"},parents:[{sha:$parent}]}'
    ;;
  *"compare/${FAKE_PROPOSAL_PARENT_SHA:-missing}...${FAKE_PROPOSAL_HEAD_SHA:-missing}"*)
    if [[ "${FAKE_PROPOSAL_UNEXPECTED_PATH:-false}" == "true" ]]; then
      files='[{"filename":".release-please-manifest.json","status":"modified"},{"filename":"CHANGELOG.md","status":"modified"},{"filename":"README.md","status":"modified"},{"filename":"code.txt","status":"modified"}]'
    else
      files='[{"filename":".release-please-manifest.json","status":"modified"},{"filename":"CHANGELOG.md","status":"modified"},{"filename":"README.md","status":"modified"}]'
    fi
    printf '{"status":"ahead","ahead_by":1,"total_commits":1,"files":%s}\n' "$files"
    ;;
  *"git/trees/eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee?recursive=1"*)
    printf '%s\n' '{"tree":[{"path":".release-please-manifest.json","mode":"100644","type":"blob"},{"path":"CHANGELOG.md","mode":"100644","type":"blob"},{"path":"README.md","mode":"100644","type":"blob"}]}'
    ;;
  *"git/ref/heads/main"*)
    printf '{"object":{"sha":"%s"}}\n' "${FAKE_MAIN_SHA:?}"
    ;;
  *"compare/"*)
    printf '{"status":"%s"}\n' "${FAKE_COMPARE_STATUS:?}"
    ;;
  *"contents/.release-please-manifest.json"*)
    printf '{".":"%s"}\n' "${FAKE_MANIFEST_VERSION:?}"
    ;;
  *"contents/README.md"*)
    printf '%b\n' 'Current stable release: \x60v0.0.8\x60. <!-- x-release-please-version -->'
    ;;
  *"contents/CHANGELOG.md"*)
    printf '%s\n' '# Changelog' '' '## [0.0.8](https://example.invalid/release) (2026-07-16)' '' '- Release.'
    ;;
  *"actions/workflows/ci.yml/runs"*)
    printf '{"workflow_runs":[{"head_sha":"%s","head_branch":"main","event":"push","conclusion":"%s"}]}\n' \
      "${FAKE_CI_HEAD_SHA:-${FAKE_SOURCE_SHA:?}}" "${FAKE_CI_CONCLUSION:?}"
    ;;
  *"commits/"*"/pulls"*)
    printf '[[{"number":42,"merged_at":"2026-07-16T00:00:00Z","merge_commit_sha":"%s","base":{"ref":"main","repo":{"full_name":"example/env-vault"}},"head":{"ref":"release-please--branches--main--components--env-vault","repo":{"full_name":"example/env-vault"}},"user":{"login":"%s"},"title":"chore(main): release env-vault v0.0.8","body":"Merging this reviewed pull request authorizes publication of this exact version after the merge commit passes main CI. This PR was generated with Release Please.","labels":[{"name":"%s"}]}]]\n' \
      "${FAKE_SOURCE_SHA:?}" "${FAKE_PR_AUTHOR:?}" "${FAKE_PR_LABEL:?}"
    ;;
  *)
    printf 'unexpected fake gh invocation: %s\n' "$args" >&2
    exit 1
    ;;
esac
`
	path := filepath.Join(directory, "gh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return directory
}
